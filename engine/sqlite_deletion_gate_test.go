package main

import "testing"

func TestSQLiteDeletedSessionPublication(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	changes, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("deleted_sessions", s.ID, deletedSession{RootID: s.ID, CleanupPending: true}); err != nil {
			return err
		}
		st.deletedSessions = map[string]string{s.ID: s.ProjectID}
		return st.deleteRecord("sessions", s.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, notified := changes.Sessions[s.ID]; !notified || !changes.Projects[s.ProjectID] || changes.Revision == "" {
		t.Fatal("deleted conversation/project was not published", changes)
	}
	var revision string
	if err := a.store.readers.QueryRow(`SELECT revision FROM data_projects WHERE id=?`, s.ProjectID).Scan(&revision); err != nil || revision != changes.Revision {
		t.Fatal("project revision did not include deletion", revision, err)
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error { return st.createSession(s, a.dir) }); err == nil {
		t.Fatal("deleted session identity was recreated")
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error { return st.putRoot("sessions", s.ID, s) }); err == nil {
		t.Fatal("generic session insertion bypassed deletion protection")
	}
}

func TestSQLiteProtectedDeletionScope(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	for _, table := range []string{"delegation_batches", "delegation_invocations", "delegation_children", "delegation_calls"} {
		if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error { return st.deleteRecord(table, "missing") }); err == nil {
			t.Fatalf("ordinary mutation can delete protected %s", table)
		}
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		return st.deleteRecordForSessionDeletion("native_sessions", s.ID)
	}); err == nil {
		t.Fatal("native history purge lacked explicit owner/tombstone scope")
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		st.deletedSessions = map[string]string{s.ID: s.ProjectID}
		return st.deleteRecordForSessionDeletion("native_sessions", s.ID)
	}); err == nil {
		t.Fatal("native history purge lacked durable tombstone")
	}
	var native nativeSession
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "native_sessions", s.ID, 64<<10, &native); err != nil {
		t.Fatal("rejected purge removed native identity", err)
	}
}

func TestSQLiteDeletionRequiresTombstone(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		st.deletedSessions = map[string]string{s.ID: s.ProjectID}
		return st.deleteRecord("sessions", s.ID)
	})
	if err == nil {
		t.Fatal("unrecorded deletion was accepted")
	}
	if _, err := a.store.readSessionMeta(a.ctx, s.ID); err != nil {
		t.Fatal("failed deletion did not roll back", err)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		st.changes.Sessions["missing-session"] = nil
		return nil
	})
	if err == nil {
		t.Fatal("missing session without explicit deletion scope was ignored")
	}
}
