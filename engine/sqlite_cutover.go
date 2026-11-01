package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

type sqliteCutoverManifest struct {
	Version            int                  `json:"version"`
	DatabaseID         string               `json:"databaseId"`
	PreviousDatabaseID string               `json:"previousDatabaseId,omitempty"`
	Phase              string               `json:"phase"` // publishing -> prepared -> fenced -> committed
	Backup             legacyBackupManifest `json:"backup"`
}

type sqliteDowngradeFence struct {
	Version       int    `json:"version"`
	StorageFormat string `json:"storageFormat"`
	DatabaseID    string `json:"databaseId"`
	Backup        string `json:"originalBackup"`
}

// The domain importer must validate every supported record, normalize legacy
// semantics, build query projections and drop positional staging before returning.
// Production startup must not call the coordinator with a partial importer.
type sqlitePrepareFunc func(context.Context, *sqliteStore, string) error

func readSQLiteMarker(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 128<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid SQLite marker trailing content")
	}
	return nil
}

func validateSQLiteManifest(m sqliteCutoverManifest) error {
	if m.Version != 1 || !validMigrationID(m.DatabaseID) || !validMigrationID(m.Backup.ID) || m.Backup.Version != 1 || len(m.Backup.Files) != len(sqliteLegacyFiles) {
		return errors.New("invalid SQLite cutover manifest")
	}
	if m.PreviousDatabaseID != "" && !validMigrationID(m.PreviousDatabaseID) {
		return errors.New("invalid previous SQLite identity")
	}
	switch m.Phase {
	case "publishing", "prepared", "fenced", "committed":
	default:
		return errors.New("unknown SQLite cutover phase")
	}
	for _, name := range sqliteLegacyFiles {
		if _, ok := m.Backup.Files[name]; !ok {
			return errors.New("incomplete SQLite source manifest")
		}
	}
	return nil
}

func sqliteManifestFromStore(ctx context.Context, store *sqliteStore) (sqliteCutoverManifest, string, error) {
	var m sqliteCutoverManifest
	var source, phase string
	if err := store.writer.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='cutover_manifest'`).Scan(&source); err != nil {
		return m, phase, err
	}
	if err := json.Unmarshal([]byte(source), &m); err != nil {
		return m, phase, err
	}
	if err := validateSQLiteManifest(m); err != nil {
		return m, phase, err
	}
	var id string
	if err := store.writer.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='database_id'`).Scan(&id); err != nil {
		return m, phase, err
	}
	if id != m.DatabaseID {
		return m, phase, errors.New("SQLite database/source identity mismatch")
	}
	if err := store.writer.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='cutover'`).Scan(&phase); err != nil {
		return m, phase, err
	}
	if phase != "prepared" && phase != "committed" {
		return m, phase, errors.New("unprepared SQLite database published")
	}
	return m, phase, nil
}

func verifySQLiteBackupManifest(dir string, m sqliteCutoverManifest, hashFiles bool) error {
	backup := filepath.Join(dir, "legacy-backups", m.Backup.ID)
	var disk legacyBackupManifest
	if err := readSQLiteMarker(filepath.Join(backup, "manifest.json"), &disk); err != nil {
		return err
	}
	if !reflect.DeepEqual(disk, m.Backup) {
		return errors.New("SQLite backup manifest identity mismatch")
	}
	if hashFiles {
		return verifyLegacyFiles(backup, m.Backup)
	}
	return nil
}

func readSQLiteFence(dir string) (*sqliteDowngradeFence, error) {
	f, err := os.Open(filepath.Join(dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 16<<10 {
		return nil, nil
	} // A fence is tiny; don't read a checkpoint.
	b, err := io.ReadAll(io.LimitReader(f, 16<<10))
	if err != nil {
		return nil, err
	}
	var value struct {
		StorageFormat string `json:"storageFormat"`
	}
	if json.Unmarshal(b, &value) != nil || value.StorageFormat == "" {
		return nil, nil
	}
	var fence sqliteDowngradeFence
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&fence); err != nil {
		return nil, err
	}
	if fence.Version != 600 || fence.StorageFormat != "klm-sqlite" || !validMigrationID(fence.DatabaseID) {
		return nil, errors.New("unrecognized storage fence; refusing migration")
	}
	return &fence, nil
}

// Caller already owns the engine data-directory lock and reserved API port.
// This does not start workers, serve readiness, or run filesystem outboxes.
func openOrMigrateSQLite(ctx context.Context, dir string, prepare sqlitePrepareFunc, hook sqliteMigrationHook) (*sqliteStore, error) {
	if !filepath.IsAbs(dir) || prepare == nil {
		return nil, errors.New("SQLite migration requires an absolute directory and complete importer")
	}
	manifestPath := filepath.Join(dir, "storage-cutover.json")
	var marker sqliteCutoverManifest
	markerErr := readSQLiteMarker(manifestPath, &marker)
	if markerErr == nil {
		if err := validateSQLiteManifest(marker); err != nil {
			return nil, err
		}
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return nil, markerErr
	}
	fence, err := readSQLiteFence(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "engine.sqlite3")
	_, statErr := os.Stat(path)
	if statErr == nil {
		store, err := openSQLiteStore(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("cannot open existing SQLite database; no legacy fallback: %w", err)
		}
		m, authority, err := sqliteManifestFromStore(ctx, store)
		if err != nil {
			store.close()
			return nil, err
		}
		if authority == "committed" {
			if markerErr == nil && marker.DatabaseID != m.DatabaseID {
				store.close()
				return nil, errors.New("committed SQLite/manifest identity mismatch")
			}
			if fence != nil && fence.DatabaseID != m.DatabaseID {
				store.close()
				return nil, errors.New("committed SQLite/fence identity mismatch")
			}
			if err := verifySQLiteBackupManifest(dir, m, false); err != nil {
				store.close()
				return nil, err
			}
			if fence == nil {
				// Keep conflicting edited checkpoint bytes as evidence before repairing
				// the fence. Journals/auth originals remain inert and are never merged.
				if _, err := os.Stat(filepath.Join(dir, "state.json")); err == nil {
					conflict := filepath.Join(dir, "legacy-conflicts", newID())
					if err := os.MkdirAll(conflict, 0700); err != nil {
						store.close()
						return nil, err
					}
					if err := copyLegacyFile(filepath.Join(dir, "state.json"), filepath.Join(conflict, "state.json")); err != nil {
						store.close()
						return nil, err
					}
				} else if !errors.Is(err, os.ErrNotExist) {
					store.close()
					return nil, err
				}
				if err := publishSQLiteFence(dir, m); err != nil {
					store.close()
					return nil, err
				}
			}
			m.Phase, m.PreviousDatabaseID = "committed", ""
			if err := writeSQLiteMarker(manifestPath, m); err != nil {
				store.close()
				return nil, err
			}
			if err := store.enableRuntime(ctx); err != nil {
				store.close()
				return nil, err
			}
			if err := store.openReaders(ctx, path); err != nil {
				store.close()
				return nil, err
			}
			return store, nil
		}
		store.close() // Prepared databases are closed DELETE-journal files.
		if markerErr == nil && marker.DatabaseID != m.DatabaseID {
			if marker.Phase != "publishing" || marker.PreviousDatabaseID != m.DatabaseID || fence != nil {
				return nil, errors.New("prepared SQLite/manifest identity mismatch")
			}
			// A crash before swapping a stale prepared generation: finish the named
			// candidate only if the original source still matches its captured input.
			if err := verifyLegacyFiles(dir, marker.Backup); err == nil {
				if err := prepareSQLiteGeneration(ctx, dir, marker, prepare, hook); err != nil {
					return nil, err
				}
				return finishSQLiteCutover(ctx, dir, marker, hook)
			}
		}
		if fence != nil {
			if fence.DatabaseID != m.DatabaseID {
				return nil, errors.New("prepared SQLite/fence identity mismatch")
			}
			return finishSQLiteCutover(ctx, dir, m, hook)
		}
		if err := verifyLegacyFiles(dir, m.Backup); err == nil {
			return finishSQLiteCutover(ctx, dir, m, hook)
		}
		// An old engine wrote before the fence. Never commit the stale generation.
		backup, err := captureLegacyBackup(dir, hook)
		if err != nil {
			return nil, err
		}
		next := sqliteCutoverManifest{Version: 1, DatabaseID: newID(), PreviousDatabaseID: m.DatabaseID, Phase: "publishing", Backup: backup}
		if err := prepareSQLiteGeneration(ctx, dir, next, prepare, hook); err != nil {
			return nil, err
		}
		return finishSQLiteCutover(ctx, dir, next, hook)
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	if markerErr == nil {
		// Authority cannot have committed before the durable 'fenced' marker.
		if marker.Phase == "fenced" || marker.Phase == "committed" {
			return nil, errors.New("authoritative SQLite database is missing; manual recovery required")
		}
		if fence != nil && fence.DatabaseID != marker.DatabaseID {
			return nil, errors.New("missing SQLite database/fence identity mismatch")
		}
		if fence != nil || verifyLegacyFiles(dir, marker.Backup) == nil {
			if err := prepareSQLiteGeneration(ctx, dir, marker, prepare, hook); err != nil {
				return nil, err
			}
			return finishSQLiteCutover(ctx, dir, marker, hook)
		}
	} else if fence != nil {
		return nil, errors.New("storage fence exists without its database/manifest; manual recovery required")
	}
	backup, err := captureLegacyBackup(dir, hook)
	if err != nil {
		return nil, err
	}
	m := sqliteCutoverManifest{Version: 1, DatabaseID: newID(), Phase: "publishing", Backup: backup}
	if err := prepareSQLiteGeneration(ctx, dir, m, prepare, hook); err != nil {
		return nil, err
	}
	return finishSQLiteCutover(ctx, dir, m, hook)
}

func prepareSQLiteGeneration(ctx context.Context, dir string, m sqliteCutoverManifest, prepare sqlitePrepareFunc, hook sqliteMigrationHook) error {
	if err := verifySQLiteBackupManifest(dir, m, true); err != nil {
		return err
	}
	stageDir, err := os.MkdirTemp(dir, ".sqlite-migration-"+m.DatabaseID+"-")
	if err != nil {
		return err
	}
	stagePath := filepath.Join(stageDir, "engine.sqlite3")
	store, err := createSQLiteStore(ctx, stagePath, m.DatabaseID)
	if err != nil {
		return err
	}
	defer store.close()
	if err := prepare(ctx, store, filepath.Join(dir, "legacy-backups", m.Backup.ID)); err != nil {
		return err
	}
	if err := hook.reach("import-complete"); err != nil {
		return err
	}
	var stagingTables int
	if err := store.writer.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name IN ('legacy_nodes','migration_record_refs','migration_collection_refs')`).Scan(&stagingTables); err != nil {
		return err
	}
	if stagingTables != 0 {
		return errors.New("positional legacy staging cannot become authoritative")
	}
	var validated string
	if err := store.writer.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='domain_validation'`).Scan(&validated); err != nil || validated != "complete" {
		return errors.New("domain import validation is incomplete")
	}
	if err := store.check(ctx); err != nil {
		return err
	}
	m.Phase = "prepared"
	encoded, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := store.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO store_meta(key,value) VALUES('cutover_manifest',?)`, string(encoded)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE store_meta SET value='prepared' WHERE key='cutover'`)
		return err
	}); err != nil {
		return err
	}
	var mode string
	if err := store.writer.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		return err
	}
	if mode != "delete" {
		return errors.New("migration database is not a publishable single file")
	}
	if err := store.close(); err != nil {
		return err
	}
	f, err := os.OpenFile(stagePath, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err := errors.Join(f.Sync(), f.Close()); err != nil {
		return err
	}
	if err := hook.reach("prepared-close"); err != nil {
		return err
	}
	if err := verifySQLiteBackupManifest(dir, m, true); err != nil {
		return err
	}
	fence, err := readSQLiteFence(dir)
	if err != nil {
		return err
	}
	if fence == nil {
		if err := verifyLegacyFiles(dir, m.Backup); err != nil {
			return err
		}
	} else if fence.DatabaseID != m.DatabaseID {
		return errors.New("source fence changed during preparation")
	}
	m.Phase = "publishing"
	if err := writeSQLiteMarker(filepath.Join(dir, "storage-cutover.json"), m); err != nil {
		return err
	}
	if err := hook.reach("publish-intent"); err != nil {
		return err
	}
	if err := replaceFile(stagePath, filepath.Join(dir, "engine.sqlite3")); err != nil {
		return err
	}
	if err := hook.reach("database-published"); err != nil {
		return err
	}
	m.Phase, m.PreviousDatabaseID = "prepared", ""
	if err := writeSQLiteMarker(filepath.Join(dir, "storage-cutover.json"), m); err != nil {
		return err
	}
	return hook.reach("prepared-manifest")
}

func publishSQLiteFence(dir string, m sqliteCutoverManifest) error {
	return writeSQLiteMarker(filepath.Join(dir, "state.json"), sqliteDowngradeFence{Version: 600, StorageFormat: "klm-sqlite", DatabaseID: m.DatabaseID,
		Backup: filepath.ToSlash(filepath.Join("legacy-backups", m.Backup.ID, "state.json"))})
}

func finishSQLiteCutover(ctx context.Context, dir string, m sqliteCutoverManifest, hook sqliteMigrationHook) (*sqliteStore, error) {
	if err := verifySQLiteBackupManifest(dir, m, true); err != nil {
		return nil, err
	}
	fence, err := readSQLiteFence(dir)
	if err != nil {
		return nil, err
	}
	if fence == nil {
		if err := verifyLegacyFiles(dir, m.Backup); err != nil {
			return nil, err
		}
		if err := publishSQLiteFence(dir, m); err != nil {
			return nil, err
		}
	} else if fence.DatabaseID != m.DatabaseID {
		return nil, errors.New("cutover fence identity mismatch")
	}
	if err := hook.reach("fence-published"); err != nil {
		return nil, err
	}
	m.Phase, m.PreviousDatabaseID = "fenced", ""
	if err := writeSQLiteMarker(filepath.Join(dir, "storage-cutover.json"), m); err != nil {
		return nil, err
	}
	if err := hook.reach("fenced-manifest"); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "engine.sqlite3")
	store, err := openSQLiteStore(ctx, path)
	if err != nil {
		return nil, err
	}
	stored, authority, err := sqliteManifestFromStore(ctx, store)
	if err != nil || stored.DatabaseID != m.DatabaseID || !reflect.DeepEqual(stored.Backup, m.Backup) {
		store.close()
		return nil, errors.New("prepared SQLite identity mismatch")
	}
	if authority != "prepared" {
		store.close()
		return nil, errors.New("unexpected authority transition")
	}
	if err := store.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE store_meta SET value='committed' WHERE key='cutover' AND value='prepared'`)
		return err
	}); err != nil {
		store.close()
		return nil, err
	}
	if err := hook.reach("authority-committed"); err != nil {
		store.close()
		return nil, err
	}
	m.Phase = "committed"
	if err := writeSQLiteMarker(filepath.Join(dir, "storage-cutover.json"), m); err != nil {
		store.close()
		return nil, err
	}
	if err := hook.reach("committed-manifest"); err != nil {
		store.close()
		return nil, err
	}
	if err := store.enableRuntime(ctx); err != nil {
		store.close()
		return nil, err
	}
	if err := store.openReaders(ctx, path); err != nil {
		store.close()
		return nil, err
	}
	return store, nil
}
