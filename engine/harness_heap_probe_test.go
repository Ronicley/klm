package main

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Generated input, not a fixture-sized string. GC-sampled live heap measures
// retained ingestion/adapter allocations; it is not a claim about client RSS.
func TestHarnessLargeContentLiveHeap(t *testing.T) {
	for _, size := range []int{1 << 20, 25 << 20, 100 << 20, 256 << 20} {
		t.Run(fmt.Sprintf("%dMiB", size>>20), func(t *testing.T) {
			ctx := context.Background()
			store, _ := sqliteSessionStore(t, 0)
			dir := protocolTestDir(t)
			scope := &protocolContentScope{ctx: ctx, dir: dir}
			defer scope.Close()
			p := &adapter{app: &app{store: store, dir: dir}, id: "general", harness: "pi", keys: map[string]string{}, commands: map[string]any{}, toolNames: map[string]string{}, turn: &turn{ctx: ctx, protocolScope: scope}}
			runtime.GC()
			var baseline runtime.MemStats
			runtime.ReadMemStats(&baseline)
			var peak atomic.Uint64
			peak.Store(baseline.HeapAlloc)
			stop, done := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
						runtime.GC()
						var m runtime.MemStats
						runtime.ReadMemStats(&m)
						for old := peak.Load(); m.HeapAlloc > old && !peak.CompareAndSwap(old, m.HeapAlloc); old = peak.Load() {
						}
					}
				}
			}()
			defer func() { close(stop); <-done }()
			frame := &protocolFrame{ctx: ctx, dir: dir}
			defer func() {
				if !frame.retained {
					_ = frame.Close()
				}
			}()
			input := io.MultiReader(strings.NewReader(`{"type":"tool_execution_end","toolCallId":"heap","toolName":"read","result":{"content":[{"type":"text","text":"`), &openCodeRepeatedBytes{size}, strings.NewReader(`"}]}}`))
			if _, err := io.CopyBuffer(frame, input, make([]byte, protocolChunkSize)); err != nil {
				t.Fatal(err)
			}
			value, err := frame.projectNative("pi", 4<<20, scope)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.pi(value); err != nil {
				t.Fatal(err)
			}
			extra := uint64(0)
			if peak.Load() > baseline.HeapAlloc {
				extra = peak.Load() - baseline.HeapAlloc
			}
			t.Logf("generated_bytes=%d baseline_live_heap=%d peak_sampled_live_heap=%d additional_live_heap=%d", size, baseline.HeapAlloc, peak.Load(), extra)
			if extra > 16<<20 {
				t.Fatalf("sampled additional live heap exceeds 16 MiB: %d", extra)
			}
		})
	}
}
