//go:build linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func configureEngineWorker(cmd *exec.Cmd) {
	// No inherited terminal or stdin. CLI workers and restart helpers must survive
	// the invoking SSH session; systemd still owns their cgroup when deployed.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func validateEngineDataDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("engine data directory must be absolute")
	}
	// Linux sockaddr_un.sun_path has 108 bytes, including the trailing NUL.
	if len(filepath.Join(dir, "control.sock")) >= 108 {
		return errors.New("engine control socket path is too long (maximum 107 bytes); use a shorter absolute HOME/XDG_CONFIG_HOME")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0700 {
		return errors.New("engine data directory must be a real directory owned by this user with mode 0700")
	}
	return nil
}

func openEngineLog(dir string) (*os.File, error) {
	if err := validateEngineDataDir(dir); err != nil {
		return nil, err
	}
	// Never follow a substituted log symlink or block opening a FIFO.
	f, err := os.OpenFile(filepath.Join(dir, "engine.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0600 {
			err = errors.New("engine log must be a regular file owned by this user with mode 0600")
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func abortEngineStartup(cmd *exec.Cmd, exited <-chan error) error {
	// Only this invocation's child, never a PID file or an arbitrary socket peer.
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
		return nil
	case <-time.After(5 * time.Second):
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("cannot terminate the startup worker: %w", err)
	}
	select {
	case <-exited:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("startup worker shutdown is unconfirmed; inspect the engine before retrying")
	}
}

func linuxControlPath(dir string) (string, error) {
	if err := validateEngineDataDir(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, "control.sock"), nil
}

func sameUserControlPeer(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var credentialErr error
	err = raw.Control(func(fd uintptr) {
		var peer *unix.Ucred
		peer, credentialErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if credentialErr == nil && peer.Uid != uint32(os.Geteuid()) {
			credentialErr = errors.New("engine control peer belongs to a different user")
		}
	})
	return errors.Join(err, credentialErr)
}

func controlSocketInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if info.Mode()&os.ModeSocket == 0 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("engine control path is not a socket owned by this user; refusing to use or remove it")
	}
	if info.Mode().Perm() != 0600 {
		return nil, errors.New("engine control socket must have mode 0600; refusing to use or remove it")
	}
	return info, nil
}

func engineControl(dir, command string) error {
	path, err := linuxControlPath(dir)
	if err != nil {
		return err
	}
	if _, err := controlSocketInfo(path); err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := sameUserControlPeer(conn.(*net.UnixConn)); err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintln(conn, command); err != nil {
		return err
	}
	line, err := bufio.NewReader(io.LimitReader(conn, 1024)).ReadString('\n')
	if err != nil {
		return errors.New("cannot read a complete engine control response")
	}
	if command == "pair" && strings.HasPrefix(line, "pair ") {
		code := strings.TrimSuffix(strings.TrimPrefix(line, "pair "), "\n")
		if len(code) != 43 || strings.ContainsAny(code, " \t\r\n") {
			return errors.New("invalid engine pairing response")
		}
		fmt.Printf("Pairing code (one use, expires in 5 minutes):\n%s\n", code)
		return nil
	}
	if line != "ok\n" {
		// Only fixed protocol errors may reach logs, never arbitrary peer output.
		switch line {
		case "Engine is not ready.\n", "Cannot generate pairing code.\n", "Cannot persist client revocation.\n", "Unknown control request.\n":
			return errors.New(strings.TrimSuffix(line, "\n"))
		default:
			return errors.New("engine rejected local control request")
		}
	}
	return nil
}

// The caller holds engine.lock throughout this listener's lifetime. Only that
// owner may reclaim a stale socket; a dial timeout or any ambiguous failure is
// not evidence of staleness.
func serveEngineControl(dir string, a *app, shutdown chan struct{}) (net.Listener, error) {
	path, err := linuxControlPath(dir)
	if err != nil {
		return nil, err
	}
	if info, err := controlSocketInfo(path); err == nil {
		conn, dialErr := net.DialTimeout("unix", path, time.Second)
		if dialErr == nil {
			conn.Close()
			return nil, errors.New("engine control socket is already live")
		}
		if !errors.Is(dialErr, unix.ECONNREFUSED) {
			return nil, fmt.Errorf("cannot establish that the engine control socket is stale: %w", dialErr)
		}
		if err := removeControlSocket(path, info); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("cannot open engine control socket: %w", err)
	}
	// Go otherwise unlinks by pathname, which could remove a replacement socket.
	listener.SetUnlinkOnClose(false)
	info, err := os.Lstat(path)
	if err != nil {
		listener.Close()
		return nil, err
	}
	owned := &linuxControlListener{UnixListener: listener, path: path, info: info}
	if err := os.Chmod(path, 0600); err != nil {
		owned.Close()
		return nil, err
	}
	var stopOnce sync.Once
	go func() {
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				if err := sameUserControlPeer(conn); err != nil {
					return
				}
				line, err := bufio.NewReader(io.LimitReader(conn, 128)).ReadString('\n')
				if err != nil {
					return
				}
				if line == "stop\n" {
					a.mu.Lock()
					a.controlReady = false
					stopOnce.Do(func() { close(shutdown) })
					a.mu.Unlock()
					fmt.Fprintln(conn, "ok")
					return
				}
				// Gate all other commands until initialization and HTTP startup finish.
				// Keep the lock through auth operations to fence shutdown admission.
				a.mu.Lock()
				defer a.mu.Unlock()
				stopping := false
				select {
				case <-shutdown:
					stopping = true
				default:
				}
				if !a.controlReady || a.closing || a.storageErr != nil || stopping {
					fmt.Fprintln(conn, "Engine is not ready.")
					return
				}
				switch line {
				case "ready\n":
					fmt.Fprintln(conn, "ok")
				case "pair\n":
					code, err := a.clientAuth.issuePair()
					if err != nil {
						fmt.Fprintln(conn, "Cannot generate pairing code.")
					} else {
						fmt.Fprintln(conn, "pair "+code)
					}
				case "revoke-clients\n":
					if err := a.clientAuth.revoke(""); err != nil {
						fmt.Fprintln(conn, "Cannot persist client revocation.")
					} else {
						fmt.Fprintln(conn, "ok")
					}
				default:
					fmt.Fprintln(conn, "Unknown control request.")
				}
			}()
		}
	}()
	return owned, nil
}

func removeControlSocket(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSocket == 0 || !os.SameFile(expected, current) {
		return errors.New("engine control socket changed; refusing to remove the replacement")
	}
	return os.Remove(path)
}

type linuxControlListener struct {
	*net.UnixListener
	path string
	info os.FileInfo
	once sync.Once
	err  error
}

func (l *linuxControlListener) Close() error {
	l.once.Do(func() {
		l.err = errors.Join(l.UnixListener.Close(), removeControlSocket(l.path, l.info))
	})
	return l.err
}
