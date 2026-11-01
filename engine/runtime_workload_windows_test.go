package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// The parent represents a harness: it launches workload only after the engine
// marks startup infrastructure, then exits on EOF without terminating its child.
func TestRuntimeWorkloadHelper(t *testing.T) {
	if os.Getenv("KLM_RUNTIME_WORKLOAD_HELPER") != "1" {
		return
	}
	in := bufio.NewReader(os.Stdin)
	if _, err := in.ReadString('\n'); err != nil {
		os.Exit(2)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestOwnedWindowsProcessHelper$")
	child.Env = append(os.Environ(), "KLM_OWNED_PROCESS_HELPER=descendant")
	// Optional real-browser probe: a local Node script starts Playwright, emits
	// {"url":"http://127.0.0.1:<CDP port>/json/version"}, and keeps it open.
	script := os.Getenv("KLM_RUNTIME_PLAYWRIGHT_PROBE")
	if script != "" {
		child = exec.Command("node", script)
	}
	out, err := child.StdoutPipe()
	if err != nil {
		os.Exit(2)
	}
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	ready := map[string]any{}
	if script != "" {
		if err := json.NewDecoder(out).Decode(&ready); err != nil {
			os.Exit(2)
		}
	}
	ready["pid"] = child.Process.Pid
	_ = json.NewEncoder(os.Stdout).Encode(ready)
	_, _ = io.Copy(io.Discard, in)
	os.Exit(0)
}

func TestRuntimeRefreshKeepsWorkload(t *testing.T) {
	for _, harness := range []string{"opencode", "codex"} {
		t.Run(harness, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			a := &app{ctx: ctx, runtimes: map[string]*sessionRuntime{}}
			defer a.shutdownSessionRuntimes()
			r, err := a.beginSessionRuntime("session")
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("KLM_RUNTIME_WORKLOAD_HELPER", "1")
			turn := &turn{ctx: ctx}
			b := binary{path: os.Args[0]}
			cwd := t.TempDir()
			process, err := startInteractive(turn, b, []string{"-test.run=^TestRuntimeWorkloadHelper$"}, cwd, r)
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
			r.markInfrastructure()
			if err := process.Send(map[string]bool{"start": true}); err != nil {
				t.Fatal(err)
			}
			var ready map[string]any
			select {
			case ready = <-process.Frames:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			number, ok := ready["pid"].(json.Number)
			pid, err := number.Int64()
			if !ok || err != nil || pid <= 0 {
				t.Fatal("workload helper did not report a child PID")
			}
			child, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
			if err != nil {
				t.Fatal(err)
			}
			defer syscall.CloseHandle(child)
			browserID := func() string {
				url, _ := ready["url"].(string)
				if url == "" {
					return ""
				}
				client := &http.Client{Timeout: 2 * time.Second}
				response, err := client.Get(url)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				var version struct {
					WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
				}
				if err := json.NewDecoder(response.Body).Decode(&version); err != nil || version.WebSocketDebuggerURL == "" {
					t.Fatalf("invalid live-browser response: %v", err)
				}
				return version.WebSocketDebuggerURL
			}
			before := browserID()
			if harness == "codex" {
				_ = r.retainInteractive(process, harness, "", "", "old-profile", true, nil)
				p := &adapter{app: a, turn: turn, runtime: r, capabilities: &CapabilitySnapshot{}}
				if err := p.finishCodexNative(process, b, cwd, "thread", "turn"); err != nil {
					t.Fatal(err)
				}
			} else {
				_ = r.retainOpenCode(&openCodeServer{owner: process.owner, processDone: process.Done, transport: &http.Transport{}}, "old-profile", true)
			}
			// Deliberately skip the idle watcher: an immediate next message must
			// preserve/classify workload even before the first idle tick.
			r.group.EndTurn()
			next, err := a.beginSessionRuntime(r.id)
			if err != nil {
				t.Fatal(err)
			}
			if state, err := syscall.WaitForSingleObject(child, 0); err != nil || state != syscall.WAIT_TIMEOUT {
				t.Fatalf("next turn killed workload pid=%d: state=%d err=%v", pid, state, err)
			}
			if after := browserID(); after != before {
				t.Fatal("browser identity changed across turns")
			}
			next.markInfrastructure()
			next.group.EndTurn()
			if !next.group.HasWorkload() {
				t.Fatal("carried workload was reclassified as harness infrastructure")
			}
			if err := next.shutdown(); err != nil {
				t.Fatal(err)
			}
			if state, err := syscall.WaitForSingleObject(child, 1000); err != nil || state != syscall.WAIT_OBJECT_0 {
				t.Fatalf("explicit shutdown did not stop workload: state=%d err=%v", state, err)
			}
			t.Logf("workload pid=%d survived harness renewal and stopped with its session; live browser=%t", pid, before != "")
		})
	}
}
