package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDeleteSessionPersistsAndProtectsRetries(t *testing.T) {
	a := transactionFixture(t)
	if err := a.commitLocked(func(d *diskState) {
		d.Sessions = append(d.Sessions, Session{ID: "side", ProjectID: "p", ParentID: "s", Role: sessionRoleSideAgent, Harness: "pi", Status: "idle", Events: []Event{event("assistant", "side history")}})
		d.Native["side"] = nativeSession{Path: filepath.Join(a.dir, "sessions", "side.jsonl")}
		d.session("other").Events = append(d.session("other").Events, Event{ID: "other-user", Type: "user", Text: "Create a session"})
		d.SessionSpawns = []SessionSpawn{{From: "s", SessionID: "other", SourceUserEventID: "user", OperationID: "create-independent", Request: spawnArgs{}}, {From: "other", SessionID: "s", SourceUserEventID: "other-user", OperationID: "accepted-spawn", Request: spawnArgs{}}}
		d.SessionCommands = []SessionCommand{{From: "other", Request: sendArgs{SessionID: "s", Instruction: "accepted instruction", OperationID: "accepted-send"}, MessageID: "instruction", CreatedAt: now()}}
	}); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("s"))
	attachmentDir := filepath.Join(a.dir, "attachments", hex.EncodeToString(hash[:]))
	nativePath := filepath.Join(a.dir, "sessions", "s-"+newID()+".jsonl")
	if err := os.MkdirAll(filepath.Dir(nativePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nativePath, []byte("native history"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(attachmentDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attachmentDir, "file.txt"), []byte("private content"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		r := httptest.NewRequest("DELETE", "/api/sessions/s", nil)
		r.SetPathValue("id", "s")
		w := httptest.NewRecorder()
		a.deleteSession(w, r)
		var result struct {
			DeletedSessionIDs []string `json:"deletedSessionIds"`
			CleanupError      string   `json:"cleanupError"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.CleanupError != "" {
			t.Fatalf("delete failed: %d %s", w.Code, w.Body.String())
		}
	}
	d := assertRecoveredTransaction(t, a)
	if d.session("s") != nil || d.session("side") != nil || d.session("other") == nil || len(d.SessionSpawns) != 0 || len(d.SessionCommands) != 0 || len(d.Native) != 0 {
		t.Fatal("deletion lost independent session or retained owned records")
	}
	if _, err := os.Stat(attachmentDir); !os.IsNotExist(err) {
		t.Fatal("attachments remain", err)
	}
	if _, err := os.Stat(nativePath); !os.IsNotExist(err) {
		t.Fatal("older native history remains", err)
	}
	if !d.DeletedOperations["send/other/accepted-send"] || d.DeletedSessions["s"].CleanupPending {
		t.Fatal("retry/cleanup state missing")
	}
	if _, err := a.sendSessionLocked(d.session("other"), sendArgs{SessionID: "s", Instruction: "accepted instruction", OperationID: "accepted-send"}); err == nil {
		t.Fatal("accepted deleted instruction was retried")
	}
	if _, err := a.existingSpawnLocked(d.session("other"), spawnArgs{OperationID: "accepted-spawn", SourceUserEventID: "other-user"}); err == nil {
		t.Fatal("accepted deleted spawn was retried")
	}
}

func TestDeleteSessionBlocksPausedGraphAndOwnedChat(t *testing.T) {
	for _, graph := range []bool{false, true} {
		a := transactionFixture(t)
		if graph {
			a.state.GraphRuns = []GraphRun{{ID: "run", ConversationID: "s", Status: "running", PauseRequested: true, Paused: true}}
		} else {
			a.state.Sessions = append(a.state.Sessions, Session{ID: "side", ParentID: "s", Status: "running"})
		}
		r := httptest.NewRequest("DELETE", "/api/sessions/s", nil)
		r.SetPathValue("id", "s")
		w := httptest.NewRecorder()
		a.deleteSession(w, r)
		if w.Code != 409 || a.state.session("s") == nil || len(a.state.DeletedSessions) != 0 {
			t.Fatal("active work was deleted", w.Code, w.Body.String())
		}
	}
}

func TestDeleteGraphRecordsPreservesOtherActivationJournal(t *testing.T) {
	before := diskState{Sessions: []Session{{ID: "owner", Events: []Event{}}, {ID: "node", GraphRunID: "owned", Events: []Event{}}, {ID: "other", Events: []Event{}}},
		GraphRuns:        []GraphRun{{ID: "owned", ConversationID: "owner"}, {ID: "retained", ConversationID: "other"}},
		GraphActivations: []GraphActivation{{ID: "first", RunID: "owned", Events: []Event{event("assistant", "deleted"), event("assistant", "deleted too")}}, {ID: "second", RunID: "retained", Events: []Event{event("assistant", "retained")}}}}
	after := transactionState(before)
	ids, runs := after.sessionDeletionSet("owner")
	removeSessionRecords(&after, "owner", ids, runs)
	var patches []statePatch
	if err := diffTransaction(reflect.ValueOf(before), reflect.ValueOf(after), nil, &patches); err != nil {
		t.Fatal(err)
	}
	if err := replayTransaction(&before, patches); err != nil {
		t.Fatal(err)
	}
	if before.session("node") != nil || before.session("other") == nil || len(before.GraphActivations) != 1 || before.GraphActivations[0].Events[0].Text != "retained" {
		t.Fatal("journal did not preserve unrelated history")
	}
}
