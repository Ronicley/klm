package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func readSQLiteLegacyJSON(path string, limit int64, target any) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() > limit {
		return false, errors.New("legacy settings exceed supported size")
	}
	d := json.NewDecoder(io.LimitReader(f, limit+1))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return false, err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return false, errors.New("invalid trailing legacy settings")
	}
	return true, nil
}

func importSQLiteAuth(ctx context.Context, s *sqliteStore, backup string) error {
	var clients clientAuthDisk
	if _, err := readSQLiteLegacyJSON(filepath.Join(backup, "client-auth.json"), 1<<20, &clients); err != nil {
		return err
	}
	if len(clients.Clients) > 128 {
		return errors.New("legacy client authentication storage exceeds capacity")
	}
	var access engineAccessSettings
	present, err := readSQLiteLegacyJSON(filepath.Join(backup, "engine-access.json"), 64<<10, &access)
	if err != nil {
		return err
	}
	if present {
		access, err = normalizeEngineAccessSettings(access)
		if err != nil {
			return err
		}
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		for _, client := range clients.Clients {
			if _, err := tx.ExecContext(ctx, `INSERT INTO paired_clients(id,name,credential_hash,created_at,last_used_at) VALUES(?,?,?,?,?)`,
				client.ID, client.Name, client.Hash, client.CreatedAt.Format(time.RFC3339Nano), client.LastUsedAt.Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
		return putSQLiteAccess(ctx, tx, access, present)
	})
}

func (s *sqliteStore) loadPairedClients(ctx context.Context) (clientAuthDisk, error) {
	result := clientAuthDisk{Clients: []pairedClient{}}
	rows, err := s.writer.QueryContext(ctx, `SELECT id,name,credential_hash,created_at,last_used_at FROM paired_clients ORDER BY id LIMIT 129`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var client pairedClient
		var created, used string
		if err := rows.Scan(&client.ID, &client.Name, &client.Hash, &created, &used); err != nil {
			return result, err
		}
		client.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return result, err
		}
		client.LastUsedAt, err = time.Parse(time.RFC3339Nano, used)
		if err != nil {
			return result, err
		}
		result.Clients = append(result.Clients, client)
	}
	if len(result.Clients) > 128 {
		return result, errors.New("client authentication storage exceeds capacity")
	}
	return result, rows.Err()
}

// Bounded client inventory (128 max). Only changed/new rows and revoked IDs are
// written. Publication of the in-memory credential set follows this SQL COMMIT.
func (s *sqliteStore) savePairedClients(ctx context.Context, previous, next clientAuthDisk) error {
	if len(next.Clients) > 128 {
		return errors.New("client authentication storage exceeds capacity")
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		old := map[string]pairedClient{}
		for _, c := range previous.Clients {
			old[c.ID] = c
		}
		seen := map[string]bool{}
		for _, c := range next.Clients {
			if seen[c.ID] {
				return errors.New("duplicate paired client identity")
			}
			seen[c.ID] = true
			if existing, ok := old[c.ID]; ok && existing == c {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO paired_clients(id,name,credential_hash,created_at,last_used_at) VALUES(?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET name=excluded.name,credential_hash=excluded.credential_hash,created_at=excluded.created_at,last_used_at=excluded.last_used_at`,
				c.ID, c.Name, c.Hash, c.CreatedAt.Format(time.RFC3339Nano), c.LastUsedAt.Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
		for id := range old {
			if !seen[id] {
				if _, err := tx.ExecContext(ctx, `DELETE FROM paired_clients WHERE id=?`, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func putSQLiteAccess(ctx context.Context, tx *sql.Tx, access engineAccessSettings, initialized bool) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO engine_access_settings(singleton,initialized) VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET initialized=excluded.initialized`, initialized); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine_access_values`); err != nil {
		return err
	}
	for kind, values := range map[string][]string{"origin": access.AllowedOrigins, "proxy": access.TrustedTLSProxies} {
		for i, value := range values {
			if _, err := tx.ExecContext(ctx, `INSERT INTO engine_access_values(kind,position,value) VALUES(?,?,?)`, kind, i, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *sqliteStore) saveEngineAccess(ctx context.Context, access engineAccessSettings) error {
	return s.write(ctx, func(tx *sql.Tx) error { return putSQLiteAccess(ctx, tx, access, true) })
}

func (s *sqliteStore) loadEngineAccess(ctx context.Context) (engineAccessSettings, error) {
	settings := engineAccessSettings{AllowedOrigins: []string{}, TrustedTLSProxies: []string{}}
	tx, err := s.writer.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return settings, err
	}
	defer tx.Rollback()
	var initialized bool
	err = tx.QueryRowContext(ctx, `SELECT initialized FROM engine_access_settings WHERE singleton=1`).Scan(&initialized)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, errors.New("SQLite access settings import marker is missing")
	}
	if err != nil {
		return settings, err
	}
	if !initialized {
		settings.AllowedOrigins = strings.Split(os.Getenv("KLM_ALLOWED_ORIGINS"), ",")
		settings.TrustedTLSProxies = strings.Split(os.Getenv("KLM_TRUSTED_TLS_PROXIES"), ",")
	} else {
		rows, err := tx.QueryContext(ctx, `SELECT kind,value FROM engine_access_values ORDER BY kind,position LIMIT 65`)
		if err != nil {
			return settings, err
		}
		for rows.Next() {
			var kind, value string
			if err := rows.Scan(&kind, &value); err != nil {
				rows.Close()
				return settings, err
			}
			if kind == "origin" {
				settings.AllowedOrigins = append(settings.AllowedOrigins, value)
			} else {
				settings.TrustedTLSProxies = append(settings.TrustedTLSProxies, value)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return settings, err
		}
	}
	if err := tx.Commit(); err != nil {
		return settings, err
	}
	return normalizeEngineAccessSettings(settings)
}
