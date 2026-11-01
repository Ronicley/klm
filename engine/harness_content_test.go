package main

import (
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"
)

func TestHarnessStreamedOpenCodeText(t *testing.T) {
	ctx := context.Background()
	store, _ := sqliteSessionStore(t, 0)
	dir := protocolTestDir(t)
	scope := &protocolContentScope{dir: dir}
	defer scope.Close()
	p := &adapter{app: &app{store: store, dir: dir}, id: "general", harness: "opencode", keys: map[string]string{}, turn: &turn{ctx: ctx, protocolScope: scope}}
	body := io.MultiReader(strings.NewReader(`data: {"properties":{"part":{"id":"part_1","messageID":"msg_1","sessionID":"native_1","type":"text","text":"`), &openCodeRepeatedBytes{25 << 20}, strings.NewReader(`é🐱"}},"type":"message.part.updated"}`+"\n\n"))
	err := readProjectedSSE(ctx, body, dir, nil, func(frame *protocolFrame) error {
		event, err := frame.projectNative("opencode", openCodeFrameLimit, scope)
		if err != nil {
			return err
		}
		part := object(object(event["properties"])["part"])
		if _, ok := part["text"].(*protocolContent); !ok {
			t.Fatal("large native string was materialized")
		}
		return p.put("opencode/msg_1/part_1", "assistant", "", displayNativeText(part["text"]), "completed", false, map[string]any{"type": "text", "partID": "part_1"})
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.readHistory(ctx, "general", "", "", 50)
	if err != nil || len(page.Events) != 1 || page.Events[0].Content == nil {
		t.Fatal("streamed canonical event missing", err)
	}
	err = store.readSnapshot(ctx, func(tx *sql.Tx) error {
		text, more, err := sqliteEventTextWindow(ctx, tx, sqliteOwnedKey("general", "0"), 25<<20, 8)
		if err == nil && (text != "é🐱" || more) {
			t.Fatalf("canonical text tail: %q %v", text, more)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHarnessContentPersistenceFailure(t *testing.T) {
	ctx := context.Background()
	store, _ := sqliteSessionStore(t, 0)
	dir := protocolTestDir(t)
	if _, err := store.writer.Exec(`CREATE TRIGGER reject_content_stage BEFORE INSERT ON event_content_generation_chunks BEGIN SELECT RAISE(ABORT,'fixture staging failure'); END;`); err != nil {
		t.Fatal(err)
	}
	scope := &protocolContentScope{ctx: ctx, dir: dir}
	defer scope.Close()
	p := &adapter{app: &app{store: store, dir: dir}, id: "general", harness: "pi", keys: map[string]string{}, turn: &turn{ctx: ctx, protocolScope: scope}}
	if err := p.put("text", "assistant", "", strings.Repeat("x", 128<<10), "completed", false, nil); err == nil || p.app.storageErr == nil {
		t.Fatalf("genuine SQL failure did not protect storage: err=%v storage=%v", err, p.app.storageErr)
	}
	page, err := store.readHistory(ctx, "general", "", "", 50)
	if err != nil || len(page.Events) != 0 {
		t.Fatal("failed staged content became visible", err)
	}
}

func TestHarnessContentCapacityFailureIsScoped(t *testing.T) {
	ctx := context.Background()
	store, _ := sqliteSessionStore(t, 0)
	dir := protocolTestDir(t)
	scope := &protocolContentScope{ctx: ctx, dir: dir}
	defer scope.Close()
	p := &adapter{app: &app{store: store, dir: dir}, id: "general", harness: "pi", keys: map[string]string{}, turn: &turn{ctx: ctx, protocolScope: scope}}
	protocolStagingBudget.Lock()
	before := protocolStagingBudget.bytes
	protocolStagingBudget.bytes = protocolStagingLimit - 1
	protocolStagingBudget.Unlock()
	err := p.put("text", "assistant", "", strings.Repeat("x", 128<<10), "completed", false, nil)
	protocolStagingBudget.Lock()
	protocolStagingBudget.bytes = before
	protocolStagingBudget.Unlock()
	if err == nil || p.app.storageErr != nil {
		t.Fatalf("content capacity escaped its operation: err=%v storage=%v", err, p.app.storageErr)
	}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, err := st.appendEvent("b", Event{ID: "independent-small-event", Type: "assistant", Text: "still writable"})
		return err
	}); err != nil {
		t.Fatal("another conversation could not persist", err)
	}
}
