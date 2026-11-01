package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteRuntimeSessionRecoveryWithoutClients(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	state := sqliteLegacyFixture()
	stamp := now()
	state.Sessions[0].Status = "running"
	state.Sessions[0].Harness = "pi"
	state.Sessions[1].Status = "running"
	state.Sessions[0].Queue = []QueuedMessage{{ID: "queued", Text: "next", Mode: "queue", Status: "queued"}, {ID: "sending", Text: "in flight", Mode: "steer", Status: "sending"}}
	state.Sessions[0].Permissions = []Permission{{ID: "permission"}}
	state.Sessions[0].Questions = []QuestionRequest{{ID: "question"}}
	state.QueuePayloads = map[string]queuedPayload{"queued": {Submission: submission{Text: "next"}, Directory: dir}, "sending": {Submission: submission{Text: "in flight"}, Directory: dir}}
	state.AcceptedMessages["a/sending"] = "retained-fingerprint"
	state.Grants = []permissionGrant{{ID: "grant", ProjectID: "p", SessionID: "a", Harness: "pi"}}
	state.Sessions[0].Events = append(state.Sessions[0].Events, Event{ID: "invocation", Type: "subagent", Status: "running", CreatedAt: stamp, Data: map[string]any{"childSessionId": "child"}})
	state.Sessions = append(state.Sessions, Session{ID: "child", ParentID: "a", ProjectID: "p", Role: sessionRoleSubagent, Harness: "pi", Status: "running", Events: []Event{{ID: "child-event", Type: "assistant", Text: "partial"}}})
	state.Native["child"] = nativeSession{ID: "native-child", Path: filepath.Join(dir, "owned-child.jsonl")}
	state.Consultations = []Consultation{{ID: "consult", From: "a", To: "b", Question: "Check this", Topic: "Fixture", Status: "answering", Delivery: "waiting", CreatedAt: stamp}}
	body, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	n, _, err := store.recoverConsultationBatch(ctx, time.Now())
	if err != nil || n != 1 {
		t.Fatal("consultation recovery", n, err)
	}
	// Stop between recovery phases, then reopen SQL. Correlation must survive.
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	store, err = openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	if _, err := store.writer.Exec(`CREATE TRIGGER reject_recovery BEFORE UPDATE ON data_sessions WHEN NEW.id='child' BEGIN SELECT RAISE(FAIL,'injected recovery failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, changes, err := store.recoverSessionBatch(ctx, time.Now()); err == nil || changes.Revision != "" {
		t.Fatal("failed recovery published changes")
	}
	var pending int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_pending_permissions`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("failed recovery cleared permissions", err)
	}
	if _, err := store.writer.Exec(`DROP TRIGGER reject_recovery`); err != nil {
		t.Fatal(err)
	}
	n, changes, err := store.recoverSessionBatch(ctx, time.Now())
	if err != nil || n != 3 || len(changes.Sessions) != 3 {
		t.Fatalf("session recovery: n=%d changes=%+v err=%v", n, changes, err)
	}
	for _, id := range []string{"a", "b", "child"} {
		meta, err := store.readSessionMeta(ctx, id)
		if err != nil || meta.Status != "error" {
			t.Fatal("session did not settle", id, err)
		}
		page, err := store.readHistory(ctx, id, "", "", 50)
		if err != nil {
			t.Fatal(err)
		}
		errors := 0
		for _, entry := range page.Events {
			if entry.Event.Type == "error" {
				errors++
				if id == "b" && entry.Event.ConsultationID != "consult" {
					t.Fatal("restart interruption lost consultation correlation")
				}
			}
			if id == "a" && entry.ID == "invocation" && entry.Event.Status != "interrupted" {
				t.Fatal("native child parent invocation not settled")
			}
		}
		if errors != 1 {
			t.Fatal("interruption event count", id, errors)
		}
	}
	for id, want := range map[string]string{"queued": "paused", "sending": "uncertain"} {
		var status string
		if err := store.readers.QueryRow(`SELECT status FROM data_queued_messages WHERE record_id=?`, id).Scan(&status); err != nil || status != want {
			t.Fatal("queue recovery lost uncertainty", id, status, err)
		}
	}
	var count int
	for _, table := range []string{"pending_permissions", "pending_questions"} {
		if err := store.readers.QueryRow(`SELECT count(*) FROM data_` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("dead request remains", table, err)
		}
	}
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_permission_grants`).Scan(&count); err != nil || count != 1 {
		t.Fatal("saved permission grant lost", err)
	}
	var native nativeSession
	if err := decodeSQLiteRecord(ctx, store.readers, "native_sessions", "child", 64<<10, &native); err != nil || native.ID != "native-child" {
		t.Fatal("native identity lost", err)
	}
	var fingerprint string
	if err := decodeSQLiteRecord(ctx, store.readers, "accepted_messages", "a/sending", 64<<10, &fingerprint); err != nil || fingerprint != "retained-fingerprint" {
		t.Fatal("acceptance receipt lost", err)
	}
	if n, changes, err := store.recoverSessionBatch(ctx, time.Now()); err != nil || n != 0 || len(changes.Sessions) != 0 {
		t.Fatal("session recovery retry was not idempotent", n, err)
	}
	if n, _, err := store.recoverConsultationBatch(ctx, time.Now()); err != nil || n != 0 {
		t.Fatal("consultation recovery retry was not idempotent", n, err)
	}
}
