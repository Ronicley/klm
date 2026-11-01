package main

import (
	"context"
	"testing"
)

func TestSQLiteCapabilityWarningKeepsTurnsActive(t *testing.T) {
	store, _ := sqliteSessionStore(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	otherCtx, otherCancel := context.WithCancel(context.Background())
	defer otherCancel()
	running := &turn{ctx: ctx, cancel: cancel}
	a := &app{ctx: context.Background(), store: store, runs: map[string]*turn{
		"general": running,
		"b":       {ctx: otherCtx, cancel: otherCancel},
	}}
	diagnostic := "ai-memory is unavailable. Selection is retained; check connection or authenticate in Library."
	p := &adapter{app: a, id: "general", turn: running, capabilities: &CapabilitySnapshot{Diagnostics: []string{diagnostic}}}
	defer p.closeMCP()
	// Exercise the warning-emission boundary without connecting to a real MCP
	// server or starting a harness. A diagnostic must not poison engine storage.
	if err := p.prepareMCP(); err != nil {
		t.Fatal(err)
	}
	if a.storageErr != nil || ctx.Err() != nil || otherCtx.Err() != nil {
		t.Fatalf("warning stopped engine/turns: storage=%v own=%v other=%v", a.storageErr, ctx.Err(), otherCtx.Err())
	}
	page, err := store.readHistory(context.Background(), "general", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Event.Type != "status" || page.Events[0].Event.Text != "Library: "+diagnostic {
		t.Fatalf("warning was not saved in SQL history: %+v", page)
	}
	meta, err := store.readSessionMeta(context.Background(), "general")
	if err != nil || meta.UpdatedAt == "" || meta.Status != "idle" {
		t.Fatalf("warning changed session status or missed metadata: %+v %v", meta, err)
	}
	a.mu.Lock()
	err = a.writeSQLLocked(context.Background(), func(st *sqliteSessionTx) error {
		_, err := st.appendEvent("b", event("user", "Still writable after another conversation's warning."))
		return err
	})
	a.mu.Unlock()
	if err != nil {
		t.Fatalf("warning blocked subsequent writes: %v", err)
	}
}
