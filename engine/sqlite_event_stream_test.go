package main

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestSQLiteBoundedEventAppend(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	ctx := a.ctx
	text := strings.Repeat("界", 700000)
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, err := st.appendEvent(s.ID, Event{ID: "stream", Type: "assistant", Text: text, Status: "running", CreatedAt: now(), Data: map[string]any{"opaque": json.Number("9007199254740993")}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := a.store.writer.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, _, err := st.streamEvent(s.ID, streamUpdate{event: Event{ID: "stream", Text: " tail 😀"}, appendText: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.writer.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after-before > 30 {
		t.Fatal("append rewrote old chunks", after-before)
	}
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, _, err := st.streamEvent(s.ID, streamUpdate{event: Event{ID: "stream", Status: "completed"}, appendText: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var result Event
	if err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		result, _, err = st.eventByID(s.ID, "stream")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if result.Text != text+" tail 😀" || result.Status != "completed" {
		t.Fatal("chunk append/metadata rewrite changed content")
	}
	if result.Data["opaque"] != json.Number("9007199254740993") {
		t.Fatal("event metadata lost integer precision", result.Data)
	}
	var staging int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_session_events_rewrite_chunks`).Scan(&staging); err != nil || staging != 0 {
		t.Fatal("rewrite staging did not settle", err)
	}
}
