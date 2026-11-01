package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Only routing metadata is backfilled. Captures, identities and history stay exact.
func (s *sqliteStore) upgradeGraphRetrySchema(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
			return err
		}
		if version == 6 {
			return nil
		}
		if version != 5 {
			return errors.New("Graph retry storage upgrade requires publication schema 5.")
		}
		var present int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('data_graph_runs') WHERE name='x_retryOperationId'`).Scan(&present); err != nil {
			return err
		}
		if present == 0 {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE data_graph_runs ADD COLUMN x_retryOperationId TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		}
		after := ""
		for {
			ids, err := sqliteQueryIDs(ctx, tx, `SELECT id FROM data_graph_runs WHERE id>? ORDER BY id LIMIT 100`, after)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
			for _, id := range ids {
				var run GraphRun
				if err := decodeSQLiteRecord(ctx, tx, "graph_runs", id, 4<<20, &run); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE data_graph_runs SET x_retryOperationId=? WHERE id=?`, run.RetryOperationID, id); err != nil {
					return err
				}
				after = id
			}
		}
		index := len(sqliteIndexFields["graph_runs"]) - 1
		ddl := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS data_graph_runs_lookup_%d ON data_graph_runs(x_retryOperationId,id); CREATE UNIQUE INDEX IF NOT EXISTS data_graph_runs_retry_operation ON data_graph_runs(x_conversationId,x_retryOperationId) WHERE x_retryOperationId<>''; PRAGMA user_version=6`, index)
		_, err := tx.ExecContext(ctx, ddl)
		return err
	})
}
