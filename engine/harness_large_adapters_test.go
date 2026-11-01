package main

import (
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"
)

func TestHarnessLargeUsefulAdapters(t *testing.T) {
	for _, test := range []struct {
		name, harness, prefix, suffix string
		consume                       func(*adapter, map[string]any) error
	}{
		{"pi-final", "pi", `{"message":{"role":"assistant","content":[{"type":"text","text":"`, `"}],"stopReason":"stop"},"type":"message_end"}`, func(p *adapter, frame map[string]any) error { return p.pi(frame) }},
		{"pi-tool", "pi", `{"type":"tool_execution_end","toolCallId":"tool_1","toolName":"read","result":{"content":[{"type":"text","text":"`, `"}]}}`, func(p *adapter, frame map[string]any) error { return p.pi(frame) }},
		{"codex-command", "codex", `{"method":"item/completed","params":{"threadId":"thread_1","turnId":"turn_1","item":{"id":"item_1","type":"commandExecution","status":"completed","command":"echo example","aggregatedOutput":"`, `","exitCode":0}}}`, func(p *adapter, frame map[string]any) error {
			c := &codexInteractive{p: p, threadID: "thread_1", turnID: "turn_1", items: map[string]map[string]any{}, parts: map[string]map[string]bool{}}
			return c.frame(frame)
		}},
		{"codex-files", "codex", `{"method":"item/completed","params":{"threadId":"thread_1","turnId":"turn_1","item":{"id":"item_1","type":"fileChange","status":"completed","changes":[{"path":"file.txt","diff":"`, `","kind":{"type":"update"}}]}}}`, func(p *adapter, frame map[string]any) error {
			c := &codexInteractive{p: p, threadID: "thread_1", turnID: "turn_1", items: map[string]map[string]any{}, parts: map[string]map[string]bool{}}
			return c.frame(frame)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, _ := sqliteSessionStore(t, 0)
			dir := protocolTestDir(t)
			scope := &protocolContentScope{dir: dir, ctx: ctx}
			defer scope.Close()
			p := &adapter{app: &app{store: store, dir: dir}, id: "general", harness: test.harness, keys: map[string]string{}, commands: map[string]any{}, toolNames: map[string]string{}, turn: &turn{ctx: ctx, protocolScope: scope}}
			frame := &protocolFrame{ctx: ctx, dir: dir}
			defer func() {
				if !frame.retained {
					_ = frame.Close()
				}
			}()
			body := io.MultiReader(strings.NewReader(test.prefix), &openCodeRepeatedBytes{25 << 20}, strings.NewReader(test.suffix))
			if _, err := io.CopyBuffer(frame, body, make([]byte, protocolChunkSize)); err != nil {
				t.Fatal(err)
			}
			value, err := frame.projectNative(test.harness, 4<<20, scope)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.consume(p, value); err != nil {
				t.Fatal(err)
			}
			page, err := store.readHistory(ctx, "general", "", "", 50)
			if err != nil || len(page.Events) != 1 || page.Events[0].Content == nil {
				t.Fatal("large event not durably referenced", err)
			}
			err = store.readSnapshot(ctx, func(tx *sql.Tx) error {
				text, more, err := sqliteEventTextWindow(ctx, tx, sqliteOwnedKey("general", "0"), 25<<20-8, 128)
				if err == nil && (len(text) < 8 || more) {
					t.Fatalf("useful tail not complete: %q more=%v", text, more)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
