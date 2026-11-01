package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestCodexRuntimeRetainedAfterNativeEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a := &app{ctx: ctx, runtimes: map[string]*sessionRuntime{}}
	defer a.shutdownSessionRuntimes()
	r, err := a.beginSessionRuntime("codex-session")
	if err != nil {
		t.Fatal(err)
	}
	// Use the native process helper to exercise EOF/Wait and real Windows Job
	// ownership without a model request or engine data.
	t.Setenv("KLM_OWNED_PROCESS_HELPER", "eof")
	b := binary{path: os.Args[0]}
	cwd := t.TempDir()
	turn := &turn{ctx: ctx}
	process, err := startInteractive(turn, b, []string{"-test.run=^TestOwnedWindowsProcessHelper$"}, cwd, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.retainInteractive(process, "codex", "", "", "captured-profile", true, nil); err != nil {
		t.Fatal(err)
	}
	group := r.group
	p := &adapter{app: a, turn: turn, runtime: r, harness: "codex", capabilities: &CapabilitySnapshot{}}
	if err := p.finishCodexNative(process, b, cwd, "thread", "turn"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-process.Done:
	default:
		t.Fatal("native process did not settle before returning success")
	}
	if r.interactive != nil {
		t.Fatal("exited native transport was retained")
	}
	r.finishTurn()
	if !r.active() || a.runtimes[r.id] != r || group.closed {
		t.Fatal("native EOF disposed the idle session runtime")
	}
	next, err := a.beginSessionRuntime(r.id)
	if err != nil {
		t.Fatal(err)
	}
	if next != r || group.closed || !r.active() || r.interactive != nil || r.refreshBeforeTurn {
		t.Fatal("next turn did not preserve the container while retiring the native profile")
	}
}
