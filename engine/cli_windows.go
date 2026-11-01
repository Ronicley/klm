package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func configureEngineWorker(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008 | 0x00000200} // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
}

func controlIdentity(dir string) (string, string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", "", err
	}
	sid := user.User.Sid.String()
	hash := sha256.Sum256([]byte(strings.ToLower(dir)))
	return fmt.Sprintf(`\\.\pipe\klm-engine-%s-%x`, sid, hash[:8]), sid, nil
}

func engineControl(dir, command string) error {
	name, _, err := controlIdentity(dir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		return err
	}
	defer conn.Close()
	var process windows.Handle
	if command == "stop" {
		// Resolve the live pipe peer through the kernel, then hold a synchronization
		// handle before asking it to exit. No persisted PID and no process killing.
		var pid uint32
		fd := conn.(interface{ Fd() uintptr }).Fd()
		getPID := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetNamedPipeServerProcessId")
		if ok, _, err := getPID.Call(fd, uintptr(unsafe.Pointer(&pid))); ok == 0 {
			return fmt.Errorf("cannot identify the engine control peer: %w", err)
		}
		process, err = windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
		if err != nil {
			return fmt.Errorf("cannot wait for engine shutdown: %w", err)
		}
		defer windows.CloseHandle(process)
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintln(conn, command); err != nil {
		return err
	}
	line, err := bufio.NewReader(io.LimitReader(conn, 1024)).ReadString('\n')
	if err != nil {
		return err
	}
	if command == "pair" && strings.HasPrefix(line, "pair ") {
		fmt.Printf("Pairing code (one use, expires in 5 minutes):\n%s\n", strings.TrimSpace(strings.TrimPrefix(line, "pair ")))
		return nil
	}
	if line != "ok\n" {
		return errors.New(strings.TrimSpace(line))
	}
	if process != 0 {
		status, err := windows.WaitForSingleObject(process, 60_000)
		if err != nil {
			return err
		}
		if status != windows.WAIT_OBJECT_0 {
			return errors.New("engine shutdown is still in progress; retry klm stop")
		}
	}
	return nil
}

func serveEngineControl(dir string, a *app, shutdown chan struct{}) (net.Listener, error) {
	name, sid, err := controlIdentity(dir)
	if err != nil {
		return nil, err
	}
	// Only this Windows user; explicitly deny network logons, including the same
	// user's credentials over SMB. This control endpoint is never part of the LAN API.
	listener, err := winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: "D:P(D;;GA;;;NU)(A;;GA;;;" + sid + ")",
	})
	if err != nil {
		return nil, fmt.Errorf("cannot open engine control pipe: %w", err)
	}
	var once sync.Once
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				line, err := bufio.NewReader(io.LimitReader(conn, 128)).ReadString('\n')
				if err != nil {
					return
				}
				switch line {
				case "pair\n":
					a.mu.Lock()
					ready := a.controlReady && !a.closing && a.clientAuth != nil
					auth := a.clientAuth
					a.mu.Unlock()
					if !ready {
						fmt.Fprintln(conn, "Engine is not ready.")
						return
					}
					code, err := auth.issuePair()
					if err != nil {
						fmt.Fprintln(conn, "Cannot generate pairing code.")
					} else {
						fmt.Fprintln(conn, "pair "+code)
					}
				case "revoke-clients\n":
					a.mu.Lock()
					ready := a.controlReady && !a.closing && a.clientAuth != nil
					auth := a.clientAuth
					a.mu.Unlock()
					if !ready {
						fmt.Fprintln(conn, "Engine is not ready.")
						return
					}
					if err := auth.revoke(""); err != nil {
						fmt.Fprintln(conn, err.Error())
					} else {
						fmt.Fprintln(conn, "ok")
					}
				case "ready\n":
					a.mu.Lock()
					ready := a.controlReady && !a.closing && a.storageErr == nil
					a.mu.Unlock()
					if ready {
						fmt.Fprintln(conn, "ok")
					} else {
						fmt.Fprintln(conn, "Engine is not ready.")
					}
				case "stop\n":
					fmt.Fprintln(conn, "ok")
					once.Do(func() { close(shutdown) })
				default:
					fmt.Fprintln(conn, "Unknown control request.")
				}
				// Keep the response pipe alive until the client has consumed it and
				// closed, bounded by the deadline above.
				_, _ = conn.Read(make([]byte, 1))
			}()
		}
	}()
	return listener, nil
}
