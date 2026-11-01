package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicationSchemaChain(t *testing.T) {
	for _, version := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "fixture.sqlite3")
			s, err := createSQLiteStore(ctx, path, "publication-fixture")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.close() }()
			err = s.write(ctx, func(tx *sql.Tx) error {
				if version < 4 {
					for _, table := range []string{"delegation_calls", "delegation_invocations", "delegation_children", "delegation_batches"} {
						if _, err := tx.ExecContext(ctx, "DROP TABLE data_"+table+"_chunks; DROP TABLE data_"+table); err != nil {
							return err
						}
					}
				}
				if version < 3 {
					for _, table := range []string{"agent_bindings", "agent_catalog_changes", "agent_sources", "global_agents"} {
						if _, err := tx.ExecContext(ctx, "DROP TABLE data_"+table+"_chunks; DROP TABLE data_"+table); err != nil {
							return err
						}
					}
				}
				if version < 2 {
					if _, err := tx.ExecContext(ctx, `DROP TABLE event_content_appends; DROP TABLE event_content_publications; DROP TABLE event_content_generation_chunks; DROP TABLE event_content_generations`); err != nil {
						return err
					}
				}
				_, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version=%d; UPDATE store_meta SET value='committed' WHERE key='cutover'; INSERT INTO store_meta VALUES('publication-sentinel','retain exact data')`, version))
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.enableRuntime(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.validatePublicationSchemaLayout(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.close(); err != nil {
				t.Fatal(err)
			}
			s, err = openSQLiteStore(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.enableRuntime(ctx); err != nil {
				t.Fatal(err)
			}
			var current int
			var value string
			if err := s.writer.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil || current != 4 {
				t.Fatal(current, err)
			}
			if err := s.writer.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='publication-sentinel'`).Scan(&value); err != nil || value != "retain exact data" {
				t.Fatal(value, err)
			}
		})
	}
}

func TestPublicationRejectStandalone(t *testing.T) {
	for _, version := range []int{2, 3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "standalone.sqlite3")
			s, err := createSQLiteStore(ctx, path, "standalone-fixture")
			if err != nil {
				t.Fatal(err)
			}
			err = s.write(ctx, func(tx *sql.Tx) error {
				if version == 2 {
					for _, table := range []string{"delegation_calls", "delegation_invocations", "delegation_children", "delegation_batches"} {
						if _, err := tx.ExecContext(ctx, "DROP TABLE data_"+table+"_chunks; DROP TABLE data_"+table); err != nil {
							return err
						}
					}
				}
				_, err := tx.ExecContext(ctx, fmt.Sprintf(`DROP TABLE event_content_appends; DROP TABLE event_content_publications; DROP TABLE event_content_generation_chunks; DROP TABLE event_content_generations; PRAGMA user_version=%d; UPDATE store_meta SET value='committed' WHERE key='cutover'`, version))
				return err
			})
			if err != nil {
				_ = s.close()
				t.Fatal(err)
			}
			if err := s.enableRuntime(ctx); err == nil || !strings.Contains(err.Error(), "Unpublished standalone agents") {
				_ = s.close()
				t.Fatal(err)
			}
			var current int
			if err := s.writer.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil || current != version {
				_ = s.close()
				t.Fatal(current, err)
			}
			if err := s.close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			opened, err := openSQLiteStore(ctx, path)
			if opened != nil {
				_ = opened.close()
				t.Fatal("unsupported database admitted")
			}
			if err == nil || !strings.Contains(err.Error(), "Unpublished standalone agents") {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatal("rejection mutated database bytes", err)
			}
		})
	}
}
