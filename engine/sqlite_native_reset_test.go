package main

import "testing"

func TestSQLiteNativeResetArchive(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	manifest, err := delegationCodexManifestV1()
	if err != nil {
		t.Fatal(err)
	}
	previous := nativeSession{ID: "previous-thread", Delegation: &manifest}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		meta, err := st.sessionMeta(s.ID)
		if err != nil {
			return err
		}
		meta.Harness = "codex"
		if err := st.putSessionMeta(meta); err != nil {
			return err
		}
		return st.putRoot("native_sessions", s.ID, previous)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		return st.putRoot("native_sessions", s.ID, nativeSession{})
	}); err == nil {
		t.Fatal("ordinary write erased instrumented native identity")
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		return st.deleteRecord("native_sessions", s.ID)
	}); err == nil {
		t.Fatal("ordinary deletion erased instrumented native identity")
	}
	var acceptedArchive string
	for _, matches := range []bool{false, true} {
		archive := previous
		if !matches {
			archive.ID = "another-thread"
		}
		_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
			e := event("status", "")
			if matches {
				acceptedArchive = e.ID
			}
			e.Status = "completed"
			e.Data = map[string]any{"nativeBeforeReset": archive}
			if _, err := st.appendEvent(s.ID, e); err != nil {
				return err
			}
			st.nativeResets = map[string]string{s.ID: e.ID}
			return st.putRoot("native_sessions", s.ID, nativeSession{})
		})
		if (err == nil) != matches {
			t.Fatalf("native archive matches=%v: %v", matches, err)
		}
		var current nativeSession
		if err := decodeSQLiteRecord(a.ctx, a.store.readers, "native_sessions", s.ID, 64<<10, &current); err != nil {
			t.Fatal(err)
		}
		if matches && (current.ID != "" || current.Delegation != nil) || !matches && current.ID != previous.ID {
			t.Fatal("native reset did not publish/roll back atomically", current)
		}
	}
	var archived int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_session_events WHERE owner_id=? AND kind='status'`, s.ID).Scan(&archived); err != nil || archived != 1 {
		t.Fatal("native reset archive missing or failed preparation committed", archived, err)
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		return st.putRoot("native_sessions", s.ID, previous)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		st.nativeResets = map[string]string{s.ID: acceptedArchive}
		return st.putRoot("native_sessions", s.ID, nativeSession{})
	}); err == nil {
		t.Fatal("prior archive reused as authorization for another reset")
	}
}
