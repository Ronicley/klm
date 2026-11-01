package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

const (
	sqliteApplicationID = 0x4b4c4d31 // KLM1; independent of product version.
	sqliteSchemaVersion = 4          // Content v2, catalog v3, owned delegation v4.
	sqliteReaderLimit   = 4
)

// Lock order for callers: domain authoring/library lock -> app.mu -> SQL writer.
// Auth may take its own mutex -> SQL writer, but must never acquire app.mu from
// inside a SQL transaction. No external effects or publication inside write().
// A single writer connection serializes commits; readers have a separate budget.
type sqliteStore struct {
	dir           string
	contentMu     sync.Mutex
	contentLive   map[string]bool
	contentStaged map[string]int64
	writer        *sql.DB
	readers       *sql.DB
}

func sqliteDSN(path string, readOnly bool) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows file:///C:/...; url.URL escapes ?, # and spaces.
	}
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{}
	q.Set("mode", "rw") // Never accidentally initialize a missing authoritative DB.
	if readOnly {
		q.Set("mode", "ro")
		q.Add("_pragma", "query_only(ON)")
	}
	// DSN pragmas run on EVERY connection, including replacement pool connections.
	for _, pragma := range []string{"foreign_keys(ON)", "busy_timeout(5000)", "synchronous(FULL)", "temp_store(FILE)", "cache_size(-2048)", "mmap_size(0)", "wal_autocheckpoint(1000)"} {
		q.Add("_pragma", pragma)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func sqlitePool(ctx context.Context, path string, readOnly bool, count int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path, readOnly))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(count)
	db.SetMaxIdleConns(count)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// createSQLiteStore is for exclusively owned unpublished staging files only.
// Existing files (including corrupt ones) are NEVER replaced by initialization.
func createSQLiteStore(ctx context.Context, path, id string) (*sqliteStore, error) {
	if !filepath.IsAbs(path) || id == "" {
		return nil, errors.New("SQLite creation requires an absolute path and identity")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	db, err := sqlitePool(ctx, path, false, 1)
	if err != nil {
		return nil, err
	}
	s := &sqliteStore{writer: db, dir: filepath.Dir(path)}
	err = s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, sqliteSchema); err != nil {
			return err
		}
		domainSchema, err := sqliteDomainSchema()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, domainSchema); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, sqliteContentGenerationSchema); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO store_meta(key,value) VALUES ('database_id',?),('cutover','building'),('revision','0')`, id)
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// openSQLiteStore does not grant authority. The cutover coordinator must verify
// its manifest/identity/committed marker before enabling runtime writes or WAL.
func openSQLiteStore(ctx context.Context, path string) (*sqliteStore, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("SQLite path must be absolute")
	}
	db, err := sqlitePool(ctx, path, false, 1)
	if err != nil {
		return nil, err
	}
	s := &sqliteStore{writer: db, dir: filepath.Dir(path)}
	var appID, version int
	if err = db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&appID); err == nil {
		err = db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version)
	}
	if err == nil && (appID != sqliteApplicationID || version < 1 || version > sqliteSchemaVersion) {
		err = fmt.Errorf("unsupported SQLite identity/schema (%x/%d); refusing startup", appID, version)
	}
	if err == nil {
		err = s.validatePublicationSchemaLayout(ctx)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *sqliteStore) enableRuntime(ctx context.Context) error {
	var authority string
	if err := s.writer.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='cutover'`).Scan(&authority); err != nil {
		return err
	}
	if authority != "committed" {
		return errors.New("SQLite cutover is not committed")
	}
	// The cutover coordinator has verified database/manifest authority before this
	// entry point. Never upgrade an unverified or merely prepared database in open().
	if err := s.validatePublicationSchemaLayout(ctx); err != nil {
		return err
	}
	if err := s.migrateContentGenerations(ctx); err != nil {
		return err
	}
	if err := s.upgradeAgentSchema(ctx); err != nil {
		return err
	}
	if err := s.upgradeDelegationSchema(ctx); err != nil {
		return err
	}
	if err := s.upgradeAgentReferenceIndexes(ctx); err != nil {
		return err
	}
	if err := s.recoverAgentCatalogChanges(ctx); err != nil {
		return err
	}
	var mode string
	if err := s.writer.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
		return err
	}
	if mode != "wal" {
		return fmt.Errorf("SQLite refused WAL: %s", mode)
	}
	if err := s.recoverEventGenerations(ctx); err != nil {
		return err
	}
	return nil
}

func (s *sqliteStore) openReaders(ctx context.Context, path string) error {
	if s.readers != nil {
		return errors.New("SQLite readers already open")
	}
	var err error
	s.readers, err = sqlitePool(ctx, path, true, sqliteReaderLimit)
	return err
}

func (s *sqliteStore) write(ctx context.Context, change func(*sql.Tx) error) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := change(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) check(ctx context.Context) error {
	rows, err := s.writer.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	invalid := rows.Next()
	err = errors.Join(rows.Err(), rows.Close())
	if invalid {
		return errors.New("SQLite foreign key check failed")
	}
	if err != nil {
		return err
	}
	var result string
	if err := s.writer.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("SQLite integrity check failed")
	}
	return nil
}

// Call only after workers/writers and read/export handlers have drained. Busy
// checkpoint results are errors, not a claim that a single-file DB is publishable.
func (s *sqliteStore) checkpoint(ctx context.Context) error {
	var busy, log, checkpointed int
	if err := s.writer.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil {
		return err
	}
	if busy != 0 {
		return fmt.Errorf("SQLite checkpoint busy (%d/%d frames)", checkpointed, log)
	}
	return nil
}

func (s *sqliteStore) close() error {
	var err error
	if s.readers != nil {
		err = s.readers.Close()
	}
	return errors.Join(err, s.writer.Close())
}

// uint64 revisions/sequences are decimal TEXT, never SQL signed INTEGER or JS
// numbers. Ordering uses length(value),value (canonical decimal, no leading zero).
func sqliteRevision(n uint64) string { return strconv.FormatUint(n, 10) }
