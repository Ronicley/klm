package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSQLiteGeneralHistoryAndStreamingExport(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	text := strings.Repeat("界", 50000) + "\nQuoted \"Needle 😀\" and tail " + strings.Repeat("tail", 50000)
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		_, err := st.appendEvent(s.ID, Event{ID: "large", Type: "assistant", Text: text, Data: map[string]any{"answer": "Other field"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	args := generalToolArgs{SessionID: s.ID, Limit: 1}
	first, err := a.readSQLGeneralHistory(a.ctx, "session_messages_list", s.ID, args)
	if err != nil {
		t.Fatal(err)
	}
	page := first.(map[string]any)
	items := page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["encoding"] != "base64-json" {
		t.Fatal("large record was not paged", page)
	}
	args.Cursor = page["nextCursor"].(string)
	if _, err := a.readSQLGeneralHistory(a.ctx, "session_messages_list", s.ID, args); err != nil {
		t.Fatal("content continuation", err)
	}
	search, err := a.readSQLGeneralHistory(a.ctx, "session_messages_search", s.ID, generalToolArgs{SessionID: s.ID, Query: "needle 😀"})
	if err != nil {
		t.Fatal(err)
	}
	results := search.(map[string]any)["items"].([]any)
	if len(results) != 1 || !strings.Contains(results[0].(map[string]any)["text"].(string), "Needle 😀") {
		t.Fatal("literal streamed search missed escaped Unicode text", results)
	}
	req := httptest.NewRequest("GET", "http://localhost/export", nil)
	req.SetPathValue("id", s.ID)
	res := httptest.NewRecorder()
	a.exportSQLSession(res, req)
	var exported Session
	if err := json.Unmarshal(res.Body.Bytes(), &exported); err != nil {
		t.Fatal("invalid streamed export", err)
	}
	if len(exported.Events) != 1 || exported.Events[0].Text != text {
		t.Fatal("streamed export lost event content")
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		_, err := st.tx.ExecContext(st.ctx, `INSERT INTO store_meta(key,value) VALUES('legacy:graphRevision','1') ON CONFLICT(key) DO UPDATE SET value='1'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	since := uint64(0)
	delta, err := a.readSQLGeneralHistory(a.ctx, "session_events_read", s.ID, generalToolArgs{SessionID: s.ID, SinceRevision: &since, Limit: 1})
	if err != nil || !delta.(map[string]any)["resetRequired"].(bool) {
		t.Fatal("pre-migration delta did not request reset", err)
	}
}
