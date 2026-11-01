package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func sqliteSessionStore(t *testing.T, coldEvents int) (*sqliteStore, string) {
	t.Helper()
	state := sqliteLegacyFixture()
	state.AcceptedMessages["b/tombstone"] = "old-removed-queue-fingerprint"
	state.Sessions = append(state.Sessions, Session{ID: "general", Role: sessionRoleGeneralAgent, Status: "idle", ExecutionCWD: filepath.Join(t.TempDir(), "general-workspace"), Events: []Event{}})
	for i := 0; i < coldEvents; i++ {
		state.Sessions[1].Events = append(state.Sessions[1].Events, Event{ID: fmt.Sprintf("cold-%d", i), Type: "assistant", Text: "unrelated historical event"})
	}
	store, path := sqliteTestStore(t)
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := stageLegacyCheckpoint(context.Background(), store, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := importStagedDomains(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if err := store.openReaders(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	return store, path
}

func TestSQLiteAtomicitySessionAcceptanceAndReopen(t *testing.T) {
	ctx := context.Background()
	store, path := sqliteSessionStore(t, 0)
	entry := Event{ID: "new-message", Type: "user", Text: "persist once", CreatedAt: now()}
	changes, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		accepted, err := st.acceptUserEvent("general", entry.ID, "fingerprint", entry)
		if err == nil && !accepted {
			t.Fatal("first acceptance reported duplicate")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if changes.Revision != "1" || len(changes.Sessions) != 1 || len(changes.Sessions["general"]) != 1 {
		t.Fatalf("bad committed change set: %+v", changes)
	}
	meta, err := store.readSessionMeta(ctx, "general")
	if err != nil || !meta.HarnessLocked || meta.Events != nil {
		t.Fatalf("metadata/harness lock: %+v %v", meta, err)
	}
	duplicate, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		accepted, err := st.acceptUserEvent("general", entry.ID, "fingerprint", entry)
		if accepted {
			t.Fatal("retry accepted duplicate")
		}
		return err
	})
	if err != nil || len(duplicate.Sessions) != 0 || duplicate.Revision != changes.Revision {
		t.Fatalf("retry advanced revision: %+v %v", duplicate, err)
	}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, err := st.acceptUserEvent("general", entry.ID, "changed", entry)
		return err
	}); err == nil {
		t.Fatal("fingerprint conflict accepted")
	}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, err := st.acceptUserEvent("general", "tombstone", "new", Event{ID: "tombstone", Type: "user"})
		return err
	}); err == nil {
		t.Fatal("removed queued acceptance identity reused")
	}
	// Fail after the event, receipt and identity rows were staged but before the
	// owning metadata update. Neither rows nor success changes may escape rollback.
	if _, err := store.writer.Exec(`CREATE TRIGGER reject_session_metadata BEFORE UPDATE ON data_sessions BEGIN SELECT RAISE(FAIL,'injected storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	failed, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, err := st.acceptUserEvent("general", "failed-message", "failed-fingerprint", Event{ID: "failed-message", Type: "user"})
		return err
	})
	if err == nil || failed.Sessions != nil || failed.Revision != "" {
		t.Fatal("failed transaction published changes")
	}
	if _, err := store.writer.Exec(`DROP TRIGGER reject_session_metadata`); err != nil {
		t.Fatal(err)
	}
	page, err := store.readHistory(ctx, "general", "", "", 50)
	if err != nil || len(page.Events) != 1 || page.Count != "1" {
		t.Fatalf("failed transaction leaked event/count: %+v %v", page, err)
	}
	var count int
	if err := store.writer.QueryRow(`SELECT count(*) FROM accepted_message_ids WHERE message_id='failed-message'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed transaction leaked identity", err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openSQLiteStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if err := reopened.openReaders(ctx, path); err != nil {
		t.Fatal(err)
	}
	meta, err = reopened.readSessionMeta(ctx, "general")
	if err != nil || !meta.HarnessLocked {
		t.Fatal("harness lock lost after reopen", err)
	}
	_, err = reopened.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		value, found, err := st.acceptedMessage("general", entry.ID)
		if err == nil && (!found || value != "fingerprint") {
			t.Fatal("acceptance receipt lost after reopen")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteBoundedReadsSessionMutationWriteSet(t *testing.T) {
	var writes int64
	for _, cold := range []int{50, 500} {
		store, _ := sqliteSessionStore(t, cold)
		var before, after int64
		if err := store.writer.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		changes, err := store.sessionTransaction(context.Background(), func(st *sqliteSessionTx) error {
			e, ordinal, err := st.eventByID("a", "reply")
			if err != nil {
				return err
			}
			e.Text += " appended"
			e.Favorite = false
			return st.writeEvent("a", ordinal, e, false)
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.writer.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if cold == 50 {
			writes = after - before
		} else if after-before != writes {
			t.Fatalf("unrelated history amplified mutation: %d vs %d", writes, after-before)
		}
		if len(changes.Sessions) != 1 || len(changes.Sessions["a"]) != 1 {
			t.Fatal("unrelated session in change set")
		}
		t.Logf("cold history=%d; changed SQL rows=%d", cold, after-before)
	}
}
