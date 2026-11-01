//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func replaceFile(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(to))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func lockDataDir(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("data directory is locked by another engine")
	}
	if err = f.Truncate(0); err == nil {
		_, err = fmt.Fprintf(f, "%d\n", os.Getpid())
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Short read-only probes retain their bounded process-group cancellation. Managed
// commands use the dedicated subreaper below, never group ancestry as kill authority.
func configureProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func killTree(p *os.Process) error   { return syscall.Kill(-p.Pid, syscall.SIGKILL) }
func pickerCommand(context.Context) (*exec.Cmd, error) {
	return nil, errors.New("native directory picker is currently Windows-only")
}

const linuxSupervisorMode = "__klm_linux_supervisor"
const linuxProxyMode = "__klm_linux_proxy"
const linuxSupervisionTimeout = 8 * time.Second

// Helpers run before normal CLI/storage dispatch, including in a focused test
// binary. Only inherited socket capabilities are accepted; no listener or path.
func init() {
	if len(os.Args) != 2 {
		return
	}
	switch os.Args[1] {
	case linuxSupervisorMode:
		os.Exit(runLinuxSupervisor())
	case linuxProxyMode:
		os.Exit(runLinuxProxy())
	}
}

type runtimeProcessGroup struct {
	mu       sync.Mutex
	conn     *net.UnixConn
	process  *exec.Cmd
	done     chan struct{}
	waitErr  error // written before done closes
	closed   bool
	drained  bool
	closeErr error
	fault    error
}
type ownedProcess struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	group     *runtimeProcessGroup
	exclusive bool
	token     string
	proxy     *os.File
	ready     *os.File
	nativePID int
	stopped   bool
	stopErr   error
}

func linuxSupervisionAvailable() error {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return errors.New("Linux managed chats require pidfd support for the engine user.")
	}
	unix.Close(fd)
	return nil
}
func newRuntimeProcessGroup() (*runtimeProcessGroup, error) {
	if err := linuxSupervisionAvailable(); err != nil {
		return nil, err
	}
	parent, child, err := linuxSocketPair()
	if err != nil {
		return nil, err
	}
	conn, err := linuxConn(parent)
	if err != nil {
		child.Close()
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		conn.Close()
		child.Close()
		return nil, err
	}
	cmd := exec.Command(exe, linuxSupervisorMode)
	cmd.ExtraFiles = []*os.File{child}
	// No target/provider environment is needed by this trusted supervisor.
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Stderr = &cappedBuffer{}
	g := &runtimeProcessGroup{conn: conn, process: cmd, done: make(chan struct{})}
	err = cmd.Start()
	child.Close()
	if err != nil {
		conn.Close()
		return nil, errors.New("Could not start the nonroot Linux process supervisor.")
	}
	go func() { g.waitErr = cmd.Wait(); close(g.done) }()
	_ = conn.SetReadDeadline(time.Now().Add(linuxSupervisionTimeout))
	hello, files, err := readLinuxPacket(conn)
	closeLinuxFiles(files)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil || !hello.OK {
		conn.Close()
		_ = cmd.Process.Kill()
		<-g.done
		return nil, errors.New("Linux process supervisor could not establish its subreaper/pidfd boundary.")
	}
	return g, nil
}
func prepareOwnedProcess(cmd *exec.Cmd) (*ownedProcess, error) {
	group, err := newRuntimeProcessGroup()
	if err != nil {
		return nil, err
	}
	owner, err := prepareSessionOwnedProcess(cmd, group)
	if err != nil {
		_ = group.Close()
		return nil, err
	}
	owner.exclusive = true
	return owner, nil
}
func prepareSessionOwnedProcess(cmd *exec.Cmd, group *runtimeProcessGroup) (*ownedProcess, error) {
	if len(cmd.ExtraFiles) != 0 || cmd.SysProcAttr != nil {
		return nil, errors.New("Linux owned commands cannot import an unmanaged process configuration.")
	}
	server, proxy, err := linuxSocketPair()
	if err != nil {
		return nil, err
	}
	ready, writer, err := os.Pipe()
	if err != nil {
		server.Close()
		proxy.Close()
		return nil, err
	}
	token := newID()
	original := linuxPacket{Op: "prepare", Token: token, Path: cmd.Path, Args: cmd.Args, Env: cmd.Environ(), Dir: cmd.Dir}
	_, err = group.call(original, []*os.File{server, writer})
	server.Close()
	writer.Close()
	if err != nil {
		proxy.Close()
		ready.Close()
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		proxy.Close()
		ready.Close()
		_, _ = group.call(linuxPacket{Op: "root-stop", Token: token}, nil)
		return nil, err
	}
	owner := &ownedProcess{cmd: cmd, group: group, token: token, proxy: proxy, ready: ready}
	cmd.Path = exe
	cmd.Args = []string{exe, linuxProxyMode}
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.ExtraFiles = []*os.File{proxy}
	return owner, nil
}
func (o *ownedProcess) Attach() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped {
		return errors.New("Linux owned command was cancelled before admission.")
	}
	// Start has duplicated ExtraFiles. The parent must release its copy so EOF is
	// meaningful if the proxy dies before asking the supervisor to start the target.
	if o.proxy != nil {
		o.proxy.Close()
		o.proxy = nil
	}
	_ = o.ready.SetReadDeadline(time.Now().Add(linuxSupervisionTimeout))
	var status linuxPacket
	err := json.NewDecoder(o.ready).Decode(&status)
	o.ready.Close()
	o.ready = nil
	if err != nil || !status.OK {
		return errors.New("Linux target admission failed before the native command was ready.")
	}
	o.nativePID = status.PID
	return nil
}
func (o *ownedProcess) Stop() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped {
		return o.stopErr
	}
	o.stopped = true
	if o.proxy != nil {
		o.proxy.Close()
		o.proxy = nil
	}
	if o.ready != nil {
		o.ready.Close()
		o.ready = nil
	}
	if o.exclusive {
		o.stopErr = o.group.Close()
	} else {
		_, o.stopErr = o.group.call(linuxPacket{Op: "root-stop", Token: o.token}, nil)
	}
	if o.stopErr != nil && o.cmd.Process != nil {
		// The trusted proxy is our direct child, not kill authority for native
		// descendants. Release local pipes even when native teardown is uncertain.
		_ = o.cmd.Process.Kill()
	}
	return o.stopErr
}
func (o *ownedProcess) Close() error { return o.Stop() }
func (o *ownedProcess) Drained() bool {
	o.group.mu.Lock()
	defer o.group.mu.Unlock()
	return o.exclusive && o.group.drained && o.group.fault == nil && o.group.closeErr == nil
}
func (g *runtimeProcessGroup) call(p linuxPacket, files []*os.File) (linuxPacket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.callLocked(p, files)
}
func (g *runtimeProcessGroup) callLocked(p linuxPacket, files []*os.File) (linuxPacket, error) {
	if g.fault != nil {
		return linuxPacket{}, g.fault
	}
	if g.closed {
		if (p.Op == "root-stop" || p.Op == "health") && g.drained && g.closeErr == nil {
			return linuxPacket{OK: true, Confirmed: true}, nil
		}
		return linuxPacket{}, errors.New("Linux process domain is closed; no command can be reused.")
	}
	_ = g.conn.SetDeadline(time.Now().Add(linuxSupervisionTimeout))
	defer g.conn.SetDeadline(time.Time{})
	if err := writeLinuxPacket(g.conn, p, files); err != nil {
		return linuxPacket{}, g.lostLocked()
	}
	result, received, err := readLinuxPacket(g.conn)
	closeLinuxFiles(received)
	if err != nil {
		return linuxPacket{}, g.lostLocked()
	}
	if !result.OK {
		g.fault = errors.New("Linux process supervision operation failed; termination is unconfirmed. The runtime cannot be reused.")
		return result, g.fault
	}
	return result, nil
}
func (g *runtimeProcessGroup) lostLocked() error {
	g.fault = errors.New("Linux process supervisor was lost; owned-process termination is unconfirmed. The runtime cannot be reused.")
	return g.fault
}
func watchRuntimeGroup(g *runtimeProcessGroup, failed func(error)) {
	go func() {
		<-g.done
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			return
		}
		err := g.lostLocked()
		g.mu.Unlock()
		failed(err)
	}()
}

func runtimeGroupHealth(g *runtimeProcessGroup) error {
	_, err := g.call(linuxPacket{Op: "health"}, nil)
	return err
}

func (g *runtimeProcessGroup) BeginTurn() error {
	_, err := g.call(linuxPacket{Op: "begin"}, nil)
	return err
}
func (g *runtimeProcessGroup) EndTurn() { _, _ = g.call(linuxPacket{Op: "end"}, nil) }
func (g *runtimeProcessGroup) MarkInfrastructure() {
	_, _ = g.call(linuxPacket{Op: "infrastructure"}, nil)
}
func (g *runtimeProcessGroup) HasWorkload() bool {
	p, err := g.call(linuxPacket{Op: "workload"}, nil)
	return err != nil || p.Workload
}
func (g *runtimeProcessGroup) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return g.closeErr
	}
	response, err := g.callLocked(linuxPacket{Op: "close"}, nil)
	g.closed = true
	_ = g.conn.Close() // EOF also requests cleanup if the reply was lost.
	timer := time.NewTimer(linuxSupervisionTimeout)
	defer timer.Stop()
	select {
	case <-g.done:
		if g.waitErr != nil {
			err = errors.Join(err, errors.New("Linux process supervisor exited unexpectedly; termination is unconfirmed."))
		}
	case <-timer.C:
		err = errors.Join(err, errors.New("Linux supervisor shutdown deadline exceeded; termination is unconfirmed."))
	}
	if !response.Confirmed {
		err = errors.Join(err, errors.New("Linux owned-process domain did not confirm complete drainage."))
	}
	g.drained = err == nil && g.fault == nil && response.Confirmed
	g.closeErr = err
	return err
}
