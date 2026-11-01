package main

import (
	"testing"
)

func TestSQLiteSessionDeletionDelegation(t *testing.T) {
	a, root := sqliteDeletionFixture(t)
	project, err := a.store.readProjectMeta(a.ctx, root.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	root.Harness = "codex"
	child, owned, batch, item := delegationStorageRecords(root, project.Folder, "delete")
	manifest, err := delegationCodexManifestV1()
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		meta, err := st.sessionMeta(root.ID)
		if err != nil {
			return err
		}
		meta.Harness = "codex"
		if err := st.putSessionMeta(meta); err != nil {
			return err
		}
		if err := st.putNativeDelegationMetadata(root.ID, nativeSession{ID: "instrumented-native", Delegation: &manifest}); err != nil {
			return err
		}
		return persistDelegationStorageFixture(st, a.dir, child, owned, batch, item)
	})
	if err != nil {
		t.Fatal(err)
	}
	requestSQLDeletion(t, a, root.ID, 409)
	for _, table := range []string{"delegation_batches", "native_sessions"} {
		_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
			id := root.ID
			if table == "delegation_batches" {
				id = batch.ID
			}
			return st.deleteRecord(table, id)
		})
		if err == nil {
			t.Fatalf("generic deletion bypassed %s guard", table)
		}
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.finishDelegationItem(item.ID, DelegationCancelled, DelegationFinality{NativeSettled: true, ProcessesDrained: true, ToolCallsSettled: true}, "", "Cancelled fixture", 0); err != nil {
			return err
		}
		if _, err := st.settleDelegation(batch.ID); err != nil {
			return err
		}
		saved, err := st.delegationBatch(batch.ID)
		if err != nil {
			return err
		}
		saved, err = advanceDelegationDelivery(saved, DelegationDeliveryCancelled)
		if err != nil {
			return err
		}
		return st.putDelegationBatch(&saved)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, cleanup := requestSQLDeletion(t, a, root.ID, 200); cleanup != "" {
		t.Fatal(cleanup)
	}
	for _, table := range []string{"delegation_batches", "delegation_calls", "delegation_invocations", "delegation_children", "native_sessions"} {
		var n int
		if err := a.store.readers.QueryRow("SELECT count(*) FROM data_" + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("instrumented history remains", table, n, err)
		}
	}
}

func TestSQLiteSessionDeletionWorkspace(t *testing.T) {
	for _, reference := range []string{"use", "decision", "directory"} {
		t.Run(reference, func(t *testing.T) {
			a, root := sqliteDeletionFixture(t)
			other := root
			other.ID = "outside"
			directory := t.TempDir()
			_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
				if reference == "directory" {
					other.ExecutionCWD = directory
				}
				if err := st.createSession(other, a.dir); err != nil {
					return err
				}
				if err := st.putRoot("graph_runs", "owned", GraphRun{ID: "owned", ConversationID: root.ID, Status: "completed"}); err != nil {
					return err
				}
				if err := st.putRoot("graph_workspaces", "workspace", WorkspaceRecord{ID: "workspace", CreatedByRunID: "owned", Directory: directory}); err != nil {
					return err
				}
				if reference == "decision" {
					return st.putRoot("graph_activities", "outside-activity", GraphActivity{ID: "outside-activity", ConversationID: other.ID, Workspace: GraphWorkspaceDecision{Reuse: map[string]string{"initial": "workspace"}}})
				}
				if reference == "use" {
					return st.putRoot("graph_workspace_uses", "outside-use", WorkspaceUse{ID: "outside-use", WorkspaceID: "workspace", RunID: "outside-run"})
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			requestSQLDeletion(t, a, root.ID, 409)
			if _, err := a.store.readSessionMeta(a.ctx, root.ID); err != nil {
				t.Fatal("workspace protection lost owner", err)
			}
		})
	}
}

func TestSQLiteSessionDeletionRollback(t *testing.T) {
	a, root := sqliteDeletionFixture(t)
	if _, err := a.store.writer.Exec(`CREATE TRIGGER reject_session_delete BEFORE DELETE ON data_sessions BEGIN SELECT RAISE(FAIL,'injected deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	requestSQLDeletion(t, a, root.ID, 503)
	if _, err := a.store.readSessionMeta(a.ctx, root.ID); err != nil {
		t.Fatal("failed delete removed owner", err)
	}
	var n int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_deleted_sessions`).Scan(&n); err != nil || n != 0 {
		t.Fatal("failed delete persisted tombstone", n, err)
	}
}
