package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit diagnostic: synthetic state only, with the production commit path.
// Does not start a harness or read the user's engine data.
func BenchmarkLatencyInvestigation(b *testing.B) {
	for _, mib := range []int{0, 8, 32} {
		b.Run(fmt.Sprintf("stream_production_history_%dMiB", mib), func(b *testing.B) {
			a := latencyDiagnosticApp(b, mib)
			p := &adapter{app: a, id: "active"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := p.commitStreamUpdates([]streamUpdate{{event: Event{ID: a.state.Sessions[0].Events[0].ID, Text: "word "}, appendText: true}}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("stream_production_long_active_session_8MiB", func(b *testing.B) {
		a := latencyDiagnosticApp(b, 8)
		for i := 0; i < 4096; i++ {
			a.state.Sessions[0].Events = append(a.state.Sessions[0].Events, event("tool", "completed"))
		}
		a.streamIndexes = nil // fixture populated directly, before the measured path
		p := &adapter{app: a, id: "active"}
		update := []streamUpdate{{event: Event{ID: a.state.Sessions[0].Events[0].ID, Text: "word "}, appendText: true}}
		if err := p.commitStreamUpdates(update); err != nil {
			b.Fatal(err)
		} // warm the event index
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := p.commitStreamUpdates(update); err != nil {
				b.Fatal(err)
			}
		}
	})
	for _, mib := range []int{0, 8, 32} {
		b.Run(fmt.Sprintf("commit_history_%dMiB", mib), func(b *testing.B) {
			a := latencyDiagnosticApp(b, mib)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				a.mu.Lock()
				err := a.commitLocked(func(d *diskState) {
					s := d.session("active")
					e := s.Events[0]
					e.Text += "word "
					d.setEvent(s, 0, e)
					s.UpdatedAt = now()
				})
				a.mu.Unlock()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("reconcile_32_unchanged_parts_history_8MiB", func(b *testing.B) {
		a := latencyDiagnosticApp(b, 8)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p := &adapter{app: a, id: "active", harness: "opencode", keys: map[string]string{}, turn: &turn{ctx: ctx, cancel: cancel}}
		for i := 0; i < 32; i++ {
			key := fmt.Sprintf("part-%d", i)
			p.keys[key] = key
			a.state.Sessions[0].Events = append(a.state.Sessions[0].Events, Event{ID: key, Type: "tool", Title: "Read", Text: "result", Status: "completed", CreatedAt: now(), Data: map[string]any{"harness": "opencode"}})
		}
		a.streamIndexes = nil
		b.ReportAllocs()
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			for i := 0; i < 32; i++ {
				if err := p.put(fmt.Sprintf("part-%d", i), "tool", "Read", "result", "completed", false, nil); err != nil {
					b.Fatal(err)
				}
			}
		}
		b.ReportMetric(float64(a.state.GraphRevision)/float64(b.N), "commits/reconciliation")
	})
	b.Run("reconcile_32_changed_parts_history_8MiB", func(b *testing.B) {
		a := latencyDiagnosticApp(b, 8)
		p := &adapter{app: a, id: "active", harness: "opencode", keys: map[string]string{}, turn: &turn{}}
		for i := 0; i < 32; i++ {
			key := fmt.Sprintf("part-%d", i)
			p.keys[key] = key
			a.state.Sessions[0].Events = append(a.state.Sessions[0].Events, Event{ID: key, Type: "tool", Title: "Read", Text: "pending", Status: "running", CreatedAt: now(), Data: map[string]any{"harness": "opencode"}})
		}
		a.streamIndexes = nil
		b.ReportAllocs()
		b.ResetTimer()
		for n := 0; n < b.N; n++ {
			p.finalBatch = []streamUpdate{}
			for i := 0; i < 32; i++ {
				if err := p.put(fmt.Sprintf("part-%d", i), "tool", "Read", fmt.Sprintf("result %d", n), "completed", false, nil); err != nil {
					b.Fatal(err)
				}
			}
			updates := p.finalBatch
			p.finalBatch = nil
			if err := p.commitStreamUpdates(updates); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(a.state.GraphRevision)/float64(b.N), "commits/reconciliation")
	})
}

// Real HTTP handlers, including validation, durable acceptance and response
// serialization. All state and journal files are confined to b.TempDir().
func BenchmarkLatencyCommands(b *testing.B) {
	for _, mib := range []int{0, 8, 32} {
		for _, mode := range []string{"queue", "steer"} {
			b.Run(fmt.Sprintf("send_%s_active_turn_%dMiB", mode, mib), func(b *testing.B) {
				a := latencyDiagnosticApp(b, mib)
				a.state.Projects[0].Folder = a.dir
				a.state.session("active").Harness = "opencode"
				a.binaries = map[string]binary{"opencode": {}}
				// A retained active turn prevents dispatching a model. The handler
				// still prepares and durably accepts each queue/steer request.
				a.runs = map[string]*turn{"active": {}}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					r := httptest.NewRequest("POST", "/api/sessions/active/messages", strings.NewReader(fmt.Sprintf(`{"clientId":"bench-%d","text":"hello","mode":"%s"}`, i, mode)))
					r.SetPathValue("id", "active")
					w := httptest.NewRecorder()
					a.message(w, r)
					if w.Code != 202 {
						b.Fatalf("send returned %d: %s", w.Code, w.Body.String())
					}
				}
			})
		}
		b.Run(fmt.Sprintf("yolo_settings_%dMiB", mib), func(b *testing.B) {
			a := latencyDiagnosticApp(b, mib)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := httptest.NewRequest("PATCH", "/api/sessions/active/permission-settings", strings.NewReader(fmt.Sprintf(`{"yolo":%t}`, i%2 == 0)))
				r.SetPathValue("id", "active")
				w := httptest.NewRecorder()
				a.updatePermissionSettings(w, r)
				if w.Code != 200 {
					b.Fatalf("settings returned %d: %s", w.Code, w.Body.String())
				}
			}
		})
		b.Run(fmt.Sprintf("stop_without_runtime_%dMiB", mib), func(b *testing.B) {
			a := latencyDiagnosticApp(b, mib)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := httptest.NewRequest("POST", "/api/sessions/active/stop", nil)
				r.SetPathValue("id", "active")
				w := httptest.NewRecorder()
				a.stop(w, r)
				if w.Code != 200 {
					b.Fatalf("stop returned %d: %s", w.Code, w.Body.String())
				}
			}
		})
		b.Run(fmt.Sprintf("steering_claim_ack_%dMiB", mib), func(b *testing.B) {
			a := latencyDiagnosticApp(b, mib)
			p := &adapter{app: a, id: "active", turn: &turn{ctx: context.Background()}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				q := QueuedMessage{ID: fmt.Sprintf("steer-%d", i), Text: "steer", Status: "steering", Mode: "steer"}
				a.mu.Lock()
				err := a.commitLocked(func(d *diskState) {
					d.session("active").Queue = []QueuedMessage{q}
					d.QueuePayloads = map[string]queuedPayload{q.ID: {Submission: submission{Text: q.Text}}}
				})
				a.mu.Unlock()
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				claimed, _, err := p.takeSteering()
				if err != nil || claimed == nil {
					b.Fatalf("claim: %v", err)
				}
				if err := p.finishSteering(claimed.ID, ""); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("stop_pending_without_runtime_%dMiB", mib), func(b *testing.B) {
			a := latencyDiagnosticApp(b, mib)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				a.mu.Lock()
				err := a.commitLocked(func(d *diskState) {
					d.session("active").Queue = []QueuedMessage{{ID: "pending", Text: "next", Status: "queued", Mode: "queue"}}
				})
				a.mu.Unlock()
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				r := httptest.NewRequest("POST", "/", nil)
				r.SetPathValue("id", "active")
				w := httptest.NewRecorder()
				a.stop(w, r)
				if w.Code != 200 || a.state.session("active").Queue[0].Status != "paused" {
					b.Fatalf("stop: %d %s", w.Code, w.Body.String())
				}
			}
		})
		b.Run(fmt.Sprintf("model_settings_cached_catalog_%dMiB", mib), func(b *testing.B) {
			a := latencyDiagnosticApp(b, mib)
			a.state.session("active").Status = "idle"
			a.catalogs = map[string]catalogCache{"project/": {catalog: &ModelCatalog{Models: []ModelOption{{ID: "fixture", Efforts: []string{"high", "low"}}}}, expires: time.Now().Add(time.Hour)}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				effort := "high"
				if i%2 == 1 {
					effort = "low"
				}
				r := httptest.NewRequest("PATCH", "/api/sessions/active/model", strings.NewReader(fmt.Sprintf(`{"model":"fixture","effort":"%s"}`, effort)))
				r.SetPathValue("id", "active")
				w := httptest.NewRecorder()
				a.updateModelSettings(w, r)
				if w.Code != 200 {
					b.Fatalf("model: %d %s", w.Code, w.Body.String())
				}
			}
		})
		b.Run(fmt.Sprintf("terminal_state_production_%dMiB", mib), func(b *testing.B) {
			a := latencyDiagnosticApp(b, mib)
			p := &adapter{app: a, id: "active", turn: &turn{ctx: context.Background()}, completed: true}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				a.mu.Lock()
				err := p.finishTurnStateLocked(nil)
				a.mu.Unlock()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("yolo_long_active_4096_events", func(b *testing.B) {
		a := latencyDiagnosticApp(b, 8)
		for i := 0; i < 4096; i++ {
			a.state.session("active").Events = append(a.state.session("active").Events, event("assistant", "old"))
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			r := httptest.NewRequest("PATCH", "/", strings.NewReader(fmt.Sprintf(`{"yolo":%t}`, i%2 == 0)))
			r.SetPathValue("id", "active")
			w := httptest.NewRecorder()
			a.updatePermissionSettings(w, r)
			if w.Code != 200 {
				b.Fatal(w.Body.String())
			}
		}
	})
}

func BenchmarkLatencyCheckpoint(b *testing.B) {
	for _, mib := range []int{0, 8, 32} {
		b.Run(fmt.Sprintf("history_%dMiB", mib), func(b *testing.B) {
			a := latencyDiagnosticApp(b, mib)
			if err := saveState(a.dir, &a.state); err != nil {
				b.Fatal(err)
			}
			var maxWait atomic.Int64
			done := make(chan struct{})
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				for {
					select {
					case <-done:
						return
					default:
					}
					start := time.Now()
					a.mu.Lock()
					wait := time.Since(start).Nanoseconds()
					a.mu.Unlock()
					for old := maxWait.Load(); wait > old && !maxWait.CompareAndSwap(old, wait); old = maxWait.Load() {
					}
					time.Sleep(time.Millisecond)
				}
			}()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				a.mu.Lock()
				err := a.commitLocked(func(d *diskState) { d.session("active").Title = fmt.Sprint(i) })
				a.mu.Unlock()
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if err := a.checkpointJournal(); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			close(done)
			<-stopped
			b.ReportMetric(float64(maxWait.Load()), "max-reader-wait-ns")
		})
	}
}

func latencyDiagnosticApp(b *testing.B, mib int) *app {
	b.Helper()
	text := strings.Repeat("history ", 1024)
	history := make([]Event, mib*128)
	for i := range history {
		history[i] = event("assistant", text)
	}
	a := &app{dir: b.TempDir(), state: diskState{Version: 2, Native: map[string]nativeSession{}, Projects: []Project{{ID: "project", Folders: []string{}}}, Sessions: []Session{
		{ID: "active", ProjectID: "project", Status: "running", Events: []Event{event("assistant", "start")}},
		{ID: "unrelated", ProjectID: "project", Status: "idle", Events: history},
	}}}
	normalizeTranscriptionSettings(&a.state.Transcription)
	a.indexMessageIDsLocked()
	return a
}
