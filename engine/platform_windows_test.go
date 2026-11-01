package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The helper exercises only native process ownership, without starting an engine
// or harness. Its descendants remain inside the test's owned Job.
func TestOwnedWindowsProcessHelper(t *testing.T) {
	mode := os.Getenv("KLM_OWNED_PROCESS_HELPER")
	if mode == "" {
		return
	}
	if mode == "descendant" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if mode == "parent" {
		child := exec.Command(os.Args[0], "-test.run=^TestOwnedWindowsProcessHelper$")
		child.Env = append(os.Environ(), "KLM_OWNED_PROCESS_HELPER=descendant")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Println(child.Process.Pid)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if mode == "failure" {
		os.Exit(7)
	}
	os.Exit(0)
}

func TestOwnedWindowsProcessLifecycle(t *testing.T) {
	for _, mode := range []string{"eof", "failure", "cancel", "parent", "exclusive-parent"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			group, err := newRuntimeProcessGroup()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := group.Close(); err != nil {
					t.Error(err)
				}
			})
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnedWindowsProcessHelper$")
			cmd.Env = append(os.Environ(), "KLM_OWNED_PROCESS_HELPER="+strings.TrimPrefix(mode, "exclusive-"))
			owner := prepareRuntimeOwnedProcess(cmd, group, mode == "exclusive-parent")
			cmd.Cancel = owner.Stop
			cmd.WaitDelay = time.Second
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := owner.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := owner.Attach(); err != nil {
				_ = owner.Stop()
				_ = cmd.Wait()
				t.Fatal(err)
			}

			var descendant syscall.Handle
			if strings.HasSuffix(mode, "parent") {
				line, err := bufio.NewReader(stdout).ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(strings.TrimSpace(line))
				if err != nil {
					t.Fatal(err)
				}
				descendant, err = syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
				if err != nil {
					t.Fatal(err)
				}
				defer syscall.CloseHandle(descendant)
			}
			if mode == "cancel" {
				cancel() // cmd.Cancel must still terminate a process waiting for input.
			} else if err := stdin.Close(); err != nil {
				t.Fatal(err)
			}
			waitErr := cmd.Wait()
			switch mode {
			case "cancel":
				if waitErr == nil {
					t.Fatal("cancelled process unexpectedly succeeded")
				}
			case "failure":
				var exit *exec.ExitError
				if !errors.As(waitErr, &exit) || exit.ExitCode() != 7 {
					t.Fatalf("process failure lost: %v", waitErr)
				}
			default:
				if waitErr != nil {
					t.Fatal(waitErr)
				}
			}
			cancel() // Reproduce EOF -> Wait -> cancel -> Close on Go 1.24 Windows.
			var closers sync.WaitGroup
			for i := 0; i < 4; i++ {
				closers.Add(1)
				go func() {
					defer closers.Done()
					if err := owner.Close(); err != nil {
						t.Errorf("cleanup after Wait: %v", err)
					}
				}()
			}
			closers.Wait()
			if owner.process != 0 {
				t.Fatal("retained process handle was not released")
			}
			if descendant != 0 {
				if mode == "parent" {
					if state, err := syscall.WaitForSingleObject(descendant, 0); err != nil || state != syscall.WAIT_TIMEOUT {
						t.Fatalf("shared owner closed a live descendant: state=%d err=%v", state, err)
					}
				} else if !owner.Drained() {
					t.Fatal("exclusive owner did not confirm Job drainage")
				}
				if err := group.Close(); err != nil {
					t.Fatal(err)
				}
				if state, err := syscall.WaitForSingleObject(descendant, 1000); err != nil || state != syscall.WAIT_OBJECT_0 {
					t.Fatalf("Job did not terminate descendant: state=%d err=%v", state, err)
				}
				if !group.drained {
					t.Fatal("Job drainage was not confirmed")
				}
			}
		})
	}
}

func TestOwnedWindowsProcessCleanupError(t *testing.T) {
	// A missing Job handle must remain a real cleanup failure on repeated Close,
	// including when the first Stop came from cmd.Cancel rather than its caller.
	owner := &ownedProcess{exclusive: true, group: &runtimeProcessGroup{}}
	first := owner.Stop()
	if first == nil {
		t.Fatal("invalid Job handle unexpectedly succeeded")
	}
	if err := owner.Close(); err != first {
		t.Fatalf("cleanup failure was lost: first=%v close=%v", first, err)
	}
	if err := stopOwnedWindowsProcess(0); err == nil {
		t.Fatal("invalid process handle unexpectedly succeeded")
	}
}
