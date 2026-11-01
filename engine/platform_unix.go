//go:build !windows && !linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
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
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("data directory is locked by another engine")
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTree(p *os.Process) error { return syscall.Kill(-p.Pid, syscall.SIGKILL) }

// Process groups retain the existing cancellation behavior but are not secure
// containment: a descendant can setsid/setpgid. Never advertise graph drainage.
type runtimeProcessGroup struct {
	mu     sync.Mutex
	groups map[int]bool
	closed bool
}

type ownedProcess struct {
	cmd       *exec.Cmd
	group     *runtimeProcessGroup
	exclusive bool
}

func newRuntimeProcessGroup() (*runtimeProcessGroup, error) {
	return &runtimeProcessGroup{groups: map[int]bool{}}, nil
}

func prepareOwnedProcess(cmd *exec.Cmd) (*ownedProcess, error) {
	group, _ := newRuntimeProcessGroup()
	configureProcess(cmd)
	return &ownedProcess{cmd: cmd, group: group, exclusive: true}, nil
}

func prepareSessionOwnedProcess(cmd *exec.Cmd, group *runtimeProcessGroup) (*ownedProcess, error) {
	configureProcess(cmd)
	return &ownedProcess{cmd: cmd, group: group}, nil
}
func (o *ownedProcess) Attach() error {
	o.group.mu.Lock()
	defer o.group.mu.Unlock()
	if o.group.closed {
		return errors.New("Owned harness runtime closed before process assignment.")
	}
	o.group.groups[o.cmd.Process.Pid] = true
	return nil
}
func (o *ownedProcess) Stop() error {
	if o.cmd.Process == nil {
		return nil
	}
	if !o.exclusive {
		return o.cmd.Process.Kill()
	}
	return killTree(o.cmd.Process)
}
func (o *ownedProcess) Close() error  { return o.Stop() }
func (o *ownedProcess) Drained() bool { return false }

func (g *runtimeProcessGroup) BeginTurn() error    { return nil }
func (g *runtimeProcessGroup) EndTurn()            {}
func (g *runtimeProcessGroup) MarkInfrastructure() {}

// Portable process groups cannot safely distinguish infrastructure from useful
// descendants. Use the bounded idle lifetime rather than pinning them forever.
func (g *runtimeProcessGroup) HasWorkload() bool { return false }
func (g *runtimeProcessGroup) Close() error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	groups := make([]int, 0, len(g.groups))
	for id := range g.groups {
		groups = append(groups, id)
	}
	g.mu.Unlock()
	var err error
	for _, id := range groups {
		if killErr := syscall.Kill(-id, syscall.SIGKILL); killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
			err = errors.Join(err, killErr)
		}
	}
	return err
}

func pickerCommand(ctx context.Context) (*exec.Cmd, error) {
	return nil, errors.New("native directory picker is currently Windows-only")
}
