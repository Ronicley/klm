package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestSQLiteGraphRetrySchemaBackfill(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "retry-upgrade.sqlite3")
	s, err := createSQLiteStore(ctx, path, "retry-upgrade")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.close() }()
	legacy := GraphRun{ID: "original-random-id", ConversationID: "owner", RetrySourceRunID: "source", RetryActivationID: "activation", RetryOperationID: "old-operation", Status: "interrupted", Revision: 7}
	if _, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.putRoot("graph_runs", legacy.ID, legacy) }); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.writer.QueryRowContext(ctx, `SELECT digest FROM data_graph_runs WHERE id=?`, legacy.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.write(ctx, func(tx *sql.Tx) error {
		ddl := fmt.Sprintf(`DROP INDEX data_graph_runs_retry_operation; DROP INDEX data_graph_runs_lookup_%d; ALTER TABLE data_graph_runs DROP COLUMN x_retryOperationId; PRAGMA user_version=5; UPDATE store_meta SET value='committed' WHERE key='cutover'`, len(sqliteIndexFields["graph_runs"])-1)
		_, err := tx.ExecContext(ctx, ddl)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	s, err = openSQLiteStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.enableRuntime(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var after, operation string
	var version int
	if err := s.writer.QueryRowContext(ctx, `SELECT digest,x_retryOperationId FROM data_graph_runs WHERE id=?`, legacy.ID).Scan(&after, &operation); err != nil || before != after || operation != legacy.RetryOperationID {
		t.Fatal("retry backfill changed content or lost operation identity", before, after, operation, err)
	}
	if err := s.writer.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != 6 {
		t.Fatal(version, err)
	}
	if err := s.write(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		receipt, err := st.graphRetryReceipt(legacy.ConversationID, legacy.RetrySourceRunID, legacy.RetryActivationID, legacy.RetryOperationID)
		if err != nil || receipt == nil || receipt.ID != legacy.ID {
			t.Fatal("imported retry identity was not preserved", receipt, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteGraphRetrySchemaLayout(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "retry-layout.sqlite3")
	s, err := createSQLiteStore(ctx, path, "retry-layout")
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if _, err := s.writer.ExecContext(ctx, `DROP INDEX data_graph_runs_retry_operation`); err != nil {
		t.Fatal(err)
	}
	if err := s.validatePublicationSchemaLayout(ctx); err == nil {
		t.Fatal("schema 6 admitted an incomplete retry layout")
	}
}
