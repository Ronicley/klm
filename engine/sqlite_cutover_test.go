package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This test importer proves publication/recovery mechanics with the existing
// normalized storage implementation. It does NOT substitute for the production
// domain semantic validation/normalization gate that has yet to be wired.
func sqliteFixturePrepare(ctx context.Context, store *sqliteStore, backup string) error {
	f, err := os.Open(filepath.Join(backup, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		body, err := json.Marshal(sqliteLegacyFixture())
		if err != nil {
			return err
		}
		if err := stageLegacyCheckpoint(ctx, store, bytes.NewReader(body)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		defer f.Close()
		if err := stageLegacyCheckpoint(ctx, store, f); err != nil {
			return err
		}
	}
	if _, err := replayLegacyStage(ctx, store, backup); err != nil {
		return err
	}
	if err := importStagedDomains(ctx, store); err != nil {
		return err
	}
	_, err = store.writer.ExecContext(ctx, `DROP TABLE migration_record_refs; DROP TABLE migration_collection_refs; DROP TABLE legacy_nodes; INSERT INTO store_meta(key,value) VALUES('domain_validation','complete');`)
	return err
}

func sqliteCutoverFixture(t *testing.T) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	body, err := json.MarshalIndent(sqliteLegacyFixture(), "  ", "\t")
	if err != nil {
		t.Fatal(err)
	}
	body = append([]byte(" \r\n"), body...)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := appendStreamRecord(dir, streamRecord{Revision: 1, Session: "a", Updates: []streamOperation{{Event: Event{ID: "reply", Text: " replayed"}, AppendText: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(streamJournalPath(dir), sealedJournalPath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := appendStreamRecord(dir, streamRecord{Format: 1, Revision: 2, Transaction: []statePatch{{Path: []string{"sessions", "0", "title"}, Value: json.RawMessage(`"journal title"`)}}}); err != nil {
		t.Fatal(err)
	}
	return dir, body
}

func TestSQLiteRecoveryCutoverCrashMatrix(t *testing.T) {
	for _, boundary := range []string{"backup-file:state.json", "backup-manifest", "import-complete", "prepared-close", "publish-intent", "database-published", "prepared-manifest", "fence-published", "fenced-manifest", "authority-committed", "committed-manifest"} {
		t.Run(boundary, func(t *testing.T) {
			dir, original := sqliteCutoverFixture(t)
			ctx := context.Background()
			store, err := openOrMigrateSQLite(ctx, dir, sqliteFixturePrepare, func(at string) error {
				if at == boundary {
					return errors.New("injected interruption")
				}
				return nil
			})
			if err == nil || store != nil {
				t.Fatal("interruption boundary not reached")
			}
			store, err = openOrMigrateSQLite(ctx, dir, sqliteFixturePrepare, nil)
			if err != nil {
				t.Fatal(err)
			}
			page, err := store.readHistory(ctx, "a", "", "", 50)
			if err != nil {
				store.close()
				t.Fatal(err)
			}
			if len(page.Events) != 2 || page.Events[1].Event.Text != "before replayed" {
				store.close()
				t.Fatal("replay result lost at cutover")
			}
			m, authority, err := sqliteManifestFromStore(ctx, store)
			if err != nil || authority != "committed" {
				store.close()
				t.Fatalf("authority: %s %v", authority, err)
			}
			backup := filepath.Join(dir, "legacy-backups", m.Backup.ID)
			got, err := os.ReadFile(filepath.Join(backup, "state.json"))
			if err != nil || !bytes.Equal(original, got) {
				store.close()
				t.Fatal("backup checkpoint bytes changed")
			}
			if err := verifyLegacyFiles(backup, m.Backup); err != nil {
				store.close()
				t.Fatal(err)
			}
			if err := removeAbandonedTasks(dir); err != nil {
				store.close()
				t.Fatal(err)
			}
			if _, err := loadState(dir); err == nil {
				store.close()
				t.Fatal("old loader accepted live fence")
			}
			if _, err := store.writer.ExecContext(ctx, `INSERT INTO store_meta(key,value) VALUES('sql-only-write','retained')`); err != nil {
				store.close()
				t.Fatal(err)
			}
			if err := store.checkpoint(ctx); err != nil {
				store.close()
				t.Fatal(err)
			}
			if err := store.close(); err != nil {
				t.Fatal(err)
			}
			// Reopen after SQL-only write. The callback must NEVER run again.
			store, err = openOrMigrateSQLite(ctx, dir, func(context.Context, *sqliteStore, string) error { return errors.New("reimported authoritative SQL") }, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer store.close()
			var value string
			if err := store.readers.QueryRow(`SELECT value FROM store_meta WHERE key='sql-only-write'`).Scan(&value); err != nil || value != "retained" {
				t.Fatalf("SQL authority lost: %q %v", value, err)
			}
			if err := verifyLegacyFiles(backup, m.Backup); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSQLiteRecoveryLegacyWriteBeforeFence(t *testing.T) {
	ctx := context.Background()
	dir, _ := sqliteCutoverFixture(t)
	_, err := openOrMigrateSQLite(ctx, dir, sqliteFixturePrepare, func(at string) error {
		if at == "database-published" {
			return errors.New("crash")
		}
		return nil
	})
	if err == nil {
		t.Fatal("crash not injected")
	}
	var before sqliteCutoverManifest
	if err := readSQLiteMarker(filepath.Join(dir, "storage-cutover.json"), &before); err != nil {
		t.Fatal(err)
	}
	// An old engine advances its journal before the migration's fence exists.
	if err := appendStreamRecord(dir, streamRecord{Revision: 3, Session: "a", Updates: []streamOperation{{Event: Event{ID: "reply", Text: " newer"}, AppendText: true}}}); err != nil {
		t.Fatal(err)
	}
	// Interrupt during the two-generation replacement intent as well.
	_, err = openOrMigrateSQLite(ctx, dir, sqliteFixturePrepare, func(at string) error {
		if at == "publish-intent" {
			return errors.New("second crash")
		}
		return nil
	})
	if err == nil {
		t.Fatal("replacement crash not injected")
	}
	store, err := openOrMigrateSQLite(ctx, dir, sqliteFixturePrepare, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	page, err := store.readHistory(ctx, "a", "", "", 50)
	if err != nil || page.Events[1].Event.Text != "before replayed newer" {
		t.Fatalf("stale generation imported: %+v %v", page, err)
	}
	after, _, err := sqliteManifestFromStore(ctx, store)
	if err != nil || after.Backup.ID == before.Backup.ID {
		t.Fatalf("old backup generation reused: %v", err)
	}
	if err := verifyLegacyFiles(filepath.Join(dir, "legacy-backups", before.Backup.ID), before.Backup); err != nil {
		t.Fatal(err)
	}
	if err := verifyLegacyFiles(filepath.Join(dir, "legacy-backups", after.Backup.ID), after.Backup); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRecoveryAuthorityAndMissingDatabase(t *testing.T) {
	ctx := context.Background()
	dir, _ := sqliteCutoverFixture(t)
	store, err := openOrMigrateSQLite(ctx, dir, sqliteFixturePrepare, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := sqliteManifestFromStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	// A stale checkpoint edited after authority is preserved as conflict evidence,
	// never interpreted as an instruction to roll SQL back to the old generation.
	conflict := []byte(`{"version":2,"projects":[],"sessions":[],"native":{}}`)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), conflict, 0600); err != nil {
		t.Fatal(err)
	}
	store, err = openOrMigrateSQLite(ctx, dir, func(context.Context, *sqliteStore, string) error { return errors.New("unexpected import") }, nil)
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.readHistory(ctx, "a", "", "", 50)
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("legacy file won over SQL: %v", err)
	}
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "legacy-conflicts", "*", "state.json"))
	if err != nil || len(paths) != 1 {
		t.Fatal("edited checkpoint evidence missing")
	}
	got, err := os.ReadFile(paths[0])
	if err != nil || !bytes.Equal(got, conflict) {
		t.Fatal("conflict bytes changed")
	}
	if err := os.Remove(filepath.Join(dir, "engine.sqlite3")); err != nil {
		t.Fatal(err)
	}
	if _, err := openOrMigrateSQLite(ctx, dir, sqliteFixturePrepare, nil); err == nil || !strings.Contains(err.Error(), "manual recovery") {
		t.Fatalf("missing authoritative DB silently restored: %v", err)
	}
	if err := verifyLegacyFiles(filepath.Join(dir, "legacy-backups", m.Backup.ID), m.Backup); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteRecoveryFreshFenceAndRejectStaging(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := openOrMigrateSQLite(ctx, dir, sqliteFixturePrepare, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	if _, err := loadState(dir); err == nil {
		t.Fatal("fresh install permits an old empty competing store")
	}
	other, _ := sqliteCutoverFixture(t)
	_, err = openOrMigrateSQLite(ctx, other, func(ctx context.Context, s *sqliteStore, backup string) error {
		f, err := os.Open(filepath.Join(backup, "state.json"))
		if err != nil {
			return err
		}
		defer f.Close()
		return stageLegacyCheckpoint(ctx, s, f)
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "positional") {
		t.Fatalf("raw positional tree published: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, "engine.sqlite3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("incomplete importer published a database")
	}
}
