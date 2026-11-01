//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const linuxPacketLimit = 1 << 20

// These packets never enter engine persistence/logging. Inherited private Unix
// socket descriptors, not a filename/listener or target-supplied token, authorize IPC.
type linuxPacket struct {
	Op        string   `json:"op,omitempty"`
	Token     string   `json:"token,omitempty"`
	Path      string   `json:"path,omitempty"`
	Args      []string `json:"args,omitempty"`
	Env       []string `json:"env,omitempty"`
	Dir       string   `json:"dir,omitempty"`
	OK        bool     `json:"ok"`
	PID       int      `json:"pid,omitempty"`
	Code      int      `json:"code,omitempty"`
	Confirmed bool     `json:"confirmed,omitempty"`
	Workload  bool     `json:"workload,omitempty"`
}

func linuxSocketPair() (*os.File, *os.File, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	return os.NewFile(uintptr(fds[0]), "owned-parent"), os.NewFile(uintptr(fds[1]), "owned-child"), nil
}
func linuxConn(f *os.File) (*net.UnixConn, error) {
	conn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	u, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		return nil, errors.New("Invalid Linux process capability.")
	}
	return u, nil
}
func inheritedLinuxConn() (*net.UnixConn, error) { return linuxConn(os.NewFile(3, "owned-capability")) }
func closeLinuxFiles(files []*os.File) {
	for _, f := range files {
		f.Close()
	}
}
func writeLinuxPacket(conn *net.UnixConn, p linuxPacket, files []*os.File) error {
	data, err := json.Marshal(p)
	if err != nil || len(data) > linuxPacketLimit {
		return errors.New("Linux process packet exceeds its limit.")
	}
	fds := make([]int, len(files))
	for i, f := range files {
		fds[i] = int(f.Fd())
	}
	var rights []byte
	if len(fds) > 0 {
		rights = unix.UnixRights(fds...)
	}
	n, _, err := conn.WriteMsgUnix(data, rights, nil)
	if err != nil {
		return err
	}
	if n != len(data) {
		return errors.New("Incomplete Linux process packet.")
	}
	return nil
}
func readLinuxPacket(conn *net.UnixConn) (linuxPacket, []*os.File, error) {
	data, oob := make([]byte, linuxPacketLimit), make([]byte, unix.CmsgSpace(16*4))
	var n, oobn, flags int
	var receiveErr error
	raw, err := conn.SyscallConn()
	if err != nil {
		return linuxPacket{}, nil, err
	}
	// Atomic CLOEXEC is essential: another goroutine can launch a native command
	// while control messages arrive. Never leak a management FD through that race.
	err = raw.Read(func(fd uintptr) bool {
		n, oobn, flags, _, receiveErr = unix.Recvmsg(int(fd), data, oob, unix.MSG_CMSG_CLOEXEC)
		return receiveErr != unix.EAGAIN && receiveErr != unix.EWOULDBLOCK
	})
	if err != nil {
		return linuxPacket{}, nil, err
	}
	if receiveErr != nil {
		return linuxPacket{}, nil, receiveErr
	}
	controls, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return linuxPacket{}, nil, err
	}
	files := []*os.File{}
	for _, c := range controls {
		fds, e := unix.ParseUnixRights(&c)
		if e != nil {
			closeLinuxFiles(files)
			return linuxPacket{}, nil, e
		}
		for _, fd := range fds {
			files = append(files, os.NewFile(uintptr(fd), "owned-transfer"))
		}
	}
	if n == 0 || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
		closeLinuxFiles(files)
		return linuxPacket{}, nil, errors.New("Linux process control connection closed or exceeded its bounds.")
	}
	var p linuxPacket
	if json.Unmarshal(data[:n], &p) != nil {
		closeLinuxFiles(files)
		return linuxPacket{}, nil, errors.New("Invalid Linux process control packet.")
	}
	return p, files, nil
}

// A proxy transfers only its standard streams. Target execution occurs in the
// already-established supervisor's child lineage, not under this proxy/engine.
func runLinuxProxy() int {
	conn, err := inheritedLinuxConn()
	if err != nil {
		return 125
	}
	defer conn.Close()
	streams := []*os.File{os.Stdin, os.Stdout, os.Stderr}
	if writeLinuxPacket(conn, linuxPacket{Op: "streams"}, streams) != nil {
		return 125
	}
	// Release our copies: native EOF/pipe lifetime must not depend on proxy exit.
	closeLinuxFiles(streams)
	result, files, err := readLinuxPacket(conn)
	closeLinuxFiles(files)
	if err != nil || result.Op != "exit" || result.Code < 0 || result.Code > 255 {
		return 125
	}
	return result.Code
}

type linuxTask struct {
	config      linuxPacket
	io          *net.UnixConn
	ready       *os.File
	pid         int
	pidfd       int
	started     bool
	exited      bool
	cancelled   bool
	waitingStop bool
}
type linuxEvent struct {
	kind   string
	packet linuxPacket
	files  []*os.File
	token  string
	err    error
}
type linuxSupervisor struct {
	conn           *net.UnixConn
	events         chan linuxEvent
	tasks          map[string]*linuxTask
	roots          map[int]*linuxTask
	infrastructure map[string]bool
	workload       map[string]bool
	turnActive     bool
}

func runLinuxSupervisor() int {
	runtime.LockOSThread() // Single launch/reaper authority; roots also use PDEATHSIG.
	conn, err := inheritedLinuxConn()
	if err != nil {
		return 125
	}
	defer conn.Close()
	if unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) != nil || linuxSupervisionAvailable() != nil {
		_ = writeLinuxPacket(conn, linuxPacket{}, nil)
		return 125
	}
	s := &linuxSupervisor{conn: conn, events: make(chan linuxEvent, 32), tasks: map[string]*linuxTask{}, roots: map[int]*linuxTask{}, infrastructure: map[string]bool{}, workload: map[string]bool{}}
	if writeLinuxPacket(conn, linuxPacket{OK: true}, nil) != nil {
		return 125
	}
	go func() {
		for {
			p, files, err := readLinuxPacket(conn)
			s.events <- linuxEvent{kind: "control", packet: p, files: files, err: err}
			if err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case event := <-s.events:
			if event.kind == "control" {
				if event.err != nil {
					if s.drain() {
						return 0
					}
					return 125
				}
				if event.packet.Op == "close" {
					closeLinuxFiles(event.files)
					confirmed := s.drain()
					_ = writeLinuxPacket(conn, linuxPacket{OK: confirmed, Confirmed: confirmed}, nil)
					if confirmed {
						return 0
					}
					return 125
				}
				s.control(event.packet, event.files)
			} else {
				s.launch(event)
			}
		case <-ticker.C:
			s.reap()
		}
	}
}
func (s *linuxSupervisor) reply(ok bool) { _ = writeLinuxPacket(s.conn, linuxPacket{OK: ok}, nil) }
func (s *linuxSupervisor) control(p linuxPacket, files []*os.File) {
	if p.Op != "prepare" {
		closeLinuxFiles(files)
	}
	switch p.Op {
	case "prepare":
		if len(files) != 2 || len(s.tasks) >= 4096 || p.Token == "" || s.tasks[p.Token] != nil || !filepath.IsAbs(p.Path) || len(p.Args) == 0 {
			closeLinuxFiles(files)
			s.reply(false)
			return
		}
		io, err := linuxConn(files[0])
		if err != nil {
			files[1].Close()
			s.reply(false)
			return
		}
		task := &linuxTask{config: p, io: io, ready: files[1], pidfd: -1}
		s.tasks[p.Token] = task
		go func() {
			packet, fds, err := readLinuxPacket(io)
			s.events <- linuxEvent{kind: "launch", token: p.Token, packet: packet, files: fds, err: err}
		}()
		s.reply(true)
	case "root-stop":
		task := s.tasks[p.Token]
		if task == nil {
			s.reply(false)
			return
		}
		task.cancelled = true
		if !task.started {
			s.finish(task, 125)
			s.reply(true)
			return
		}
		if task.exited {
			s.reply(true)
			return
		}
		if err := unix.PidfdSendSignal(task.pidfd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
			s.reply(false)
			return
		}
		// Reply only after our sole reaper confirms this exact native root's exit.
		task.waitingStop = true
	case "begin":
		ok := s.observe(false)
		s.turnActive = true
		s.reply(ok)
	case "health":
		s.reply(true)
	case "end":
		s.turnActive = false
		s.reply(true)
	case "infrastructure":
		s.reply(s.observe(true))
	case "workload":
		ok := s.observe(false)
		present := false
		if !s.turnActive {
			for _, workload := range s.workload {
				present = present || workload
			}
		}
		_ = writeLinuxPacket(s.conn, linuxPacket{OK: ok, Workload: present}, nil)
	default:
		s.reply(false)
	}
}
func (s *linuxSupervisor) launch(event linuxEvent) {
	defer closeLinuxFiles(event.files)
	task := s.tasks[event.token]
	if task == nil {
		return
	}
	if task.cancelled || task.exited {
		return
	}
	if event.err != nil || event.packet.Op != "streams" || len(event.files) != 3 {
		s.finish(task, 125)
		return
	}
	cfg := task.config
	cmd := exec.Command(cfg.Path, cfg.Args[1:]...)
	cmd.Args = cfg.Args
	cmd.Dir = cfg.Dir
	cmd.Env = cfg.Env
	cmd.Stdin = event.files[0]
	cmd.Stdout = event.files[1]
	cmd.Stderr = event.files[2]
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		s.finish(task, 125)
		return
	}
	// Before any reap, even an exited direct child retains its PID as a zombie.
	// This pidfd therefore cannot accidentally select a reused unrelated PID.
	task.started = true
	task.pid = cmd.Process.Pid
	s.roots[task.pid] = task
	task.pidfd, _ = unix.PidfdOpen(task.pid, 0)
	_ = cmd.Process.Release()
	task.config = linuxPacket{} // Discard native argv/environment after launch.
	if task.pidfd < 0 {
		task.cancelled = true
		_ = unix.Kill(task.pid, unix.SIGKILL)
		s.ready(task, false)
		return
	}
	s.ready(task, true)
}
func (s *linuxSupervisor) ready(task *linuxTask, ok bool) {
	if task.ready != nil {
		_ = json.NewEncoder(task.ready).Encode(linuxPacket{OK: ok, PID: task.pid})
		task.ready.Close()
		task.ready = nil
	}
}
func (s *linuxSupervisor) finish(task *linuxTask, code int) {
	if task.exited {
		return
	}
	task.exited = true
	s.ready(task, false)
	_ = writeLinuxPacket(task.io, linuxPacket{Op: "exit", OK: true, Code: code}, nil)
	task.io.Close()
	task.config = linuxPacket{}
	if task.pidfd >= 0 {
		unix.Close(task.pidfd)
		task.pidfd = -1
	}
	if task.waitingStop {
		s.reply(true)
		task.waitingStop = false
	}
}
func (s *linuxSupervisor) reap() bool {
	for {
		var status unix.WaitStatus
		pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
		if errors.Is(err, unix.ECHILD) {
			return true
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || pid == 0 {
			return false
		}
		if task := s.roots[pid]; task != nil {
			code := 125
			if status.Exited() {
				code = status.ExitStatus()
			} else if status.Signaled() {
				code = 128 + int(status.Signal())
			}
			s.finish(task, code)
			delete(s.roots, pid)
		}
	}
}
func (s *linuxSupervisor) drain() bool {
	for _, task := range s.tasks {
		task.cancelled = true
		if !task.started {
			s.finish(task, 125)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// Ownership authority is the kernel's direct-child relation to THIS dedicated
		// supervisor. Do not signal from global ancestry/name scans. Only this thread
		// reaps, so child identities cannot be recycled between list and pidfd open.
		files, err := filepath.Glob("/proc/self/task/*/children")
		if err != nil || len(files) == 0 {
			return false
		}
		handles := []int{}
		failed := false
		for _, path := range files {
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				failed = true
				break
			}
			for _, field := range strings.Fields(string(data)) {
				pid, err := strconv.Atoi(field)
				if err != nil {
					failed = true
					break
				}
				fd, err := unix.PidfdOpen(pid, 0)
				if errors.Is(err, unix.ESRCH) {
					continue
				}
				if err != nil {
					failed = true
					break
				}
				handles = append(handles, fd)
			}
		}
		for _, fd := range handles {
			err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
			if err != nil && !errors.Is(err, unix.ESRCH) {
				failed = true
			}
			unix.Close(fd)
		}
		if failed {
			return false
		}
		if s.reap() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

type linuxProc struct {
	parent int
	key    string
}

func linuxProcIdentity(pid int) (linuxProc, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return linuxProc{}, err
	}
	text := string(data)
	end := strings.LastIndexByte(text, ')')
	if end < 0 {
		return linuxProc{}, errors.New("Invalid process identity.")
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) < 20 {
		return linuxProc{}, errors.New("Incomplete process identity.")
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return linuxProc{}, err
	}
	return linuxProc{parent: parent, key: strconv.Itoa(pid) + "/" + fields[19]}, nil
}

// Accounting only: these observations never authorize a signal or prove drainage.
// A stable pidfd and repeated start-time identity reject a recycled accounting PID.
func (s *linuxSupervisor) observe(mark bool) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	nodes := map[int]linuxProc{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		node, err := linuxProcIdentity(pid)
		if err == nil {
			nodes[pid] = node
		}
	}
	self := os.Getpid()
	active := map[string]bool{}
	for pid, node := range nodes {
		if pid == self {
			continue
		}
		parent := node.parent
		seen := map[int]bool{}
		for parent != self && parent > 0 && !seen[parent] {
			seen[parent] = true
			ancestor, ok := nodes[parent]
			if !ok {
				parent = 0
				break
			}
			parent = ancestor.parent
		}
		if parent != self {
			continue
		}
		fd, err := unix.PidfdOpen(pid, 0)
		if err != nil {
			continue
		}
		again, err := linuxProcIdentity(pid)
		unix.Close(fd)
		if err != nil || again.key != node.key {
			continue
		}
		active[node.key] = true
	}
	for key := range s.infrastructure {
		if !active[key] {
			delete(s.infrastructure, key)
		}
	}
	for key := range s.workload {
		if !active[key] {
			delete(s.workload, key)
		}
	}
	for key := range active {
		if mark && !s.workload[key] {
			s.infrastructure[key] = true
		}
		if !s.infrastructure[key] {
			s.workload[key] = true
		}
	}
	return true
}
