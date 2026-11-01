package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSQLiteSessionDeletionLegacyTombstones(t *testing.T) {
	a, root := sqliteDeletionFixture(t)
	requestSQLDeletion(t, a, root.ID, 200)
	child, other := "legacy-child", "other-root"
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		for id, owner := range map[string]string{child: root.ID, other: other} {
			if err := st.putRoot("deleted_sessions", id, deletedSession{RootID: owner, CleanupPending: true}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Imported tombstones retain blank routing when startup file cleanup fails.
	nativeDir := filepath.Join(a.dir, "sessions")
	if err := os.WriteFile(nativeDir, []byte("block startup cleanup"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.cleanupSQLDeletedSessions(""); err == nil {
		t.Fatal("startup cleanup did not fail")
	}
	var parent, status string
	if err := a.store.readers.QueryRow(`SELECT parent_id,status FROM data_deleted_sessions WHERE id=?`, child).Scan(&parent, &status); err != nil || parent != "" || status != "" {
		t.Fatal("fixture no longer exercises unprojected imported tombstone", parent, status, err)
	}
	if err := os.Remove(nativeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(nativeDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{child, other} {
		if err := os.WriteFile(filepath.Join(nativeDir, id+".jsonl"), []byte("pending native history"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		ids, cleanup := requestSQLDeletion(t, a, root.ID, 200)
		if cleanup != "" || !slices.Equal(ids, []string{child, root.ID}) {
			t.Fatal("root retry omitted imported child or included unrelated root", ids, cleanup)
		}
	}
	if _, err := os.Stat(filepath.Join(nativeDir, child+".jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("imported child artifact remains", err)
	}
	if _, err := os.Stat(filepath.Join(nativeDir, other+".jsonl")); err != nil {
		t.Fatal("root-specific retry touched unrelated artifact", err)
	}
	var retained deletedSession
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "deleted_sessions", other, 1<<20, &retained); err != nil || !retained.CleanupPending {
		t.Fatal("root-specific retry acknowledged unrelated cleanup", err)
	}
	if err := a.store.readers.QueryRow(`SELECT parent_id,status FROM data_deleted_sessions WHERE id=?`, other).Scan(&parent, &status); err != nil || parent != "" || status != "" {
		t.Fatal("root-specific retry rewrote unrelated routing", parent, status, err)
	}
}
