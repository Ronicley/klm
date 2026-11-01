package main

import (
	"context"
	"database/sql"
	"errors"
)

// Publication schema 3 follows the predecessor's content-generation schema 2.
// The standalone source's catalog-v2 numbering was never published in this stack.
func (s *sqliteStore) upgradeAgentSchema(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
			return err
		}
		if version >= 3 && version <= sqliteSchemaVersion {
			return nil
		}
		if version != 2 {
			return errors.New("Unsupported SQLite upgrade source.")
		}
		ddl, err := sqliteDomainSchemaSelection(map[string]bool{"global_agents": true, "agent_sources": true, "agent_catalog_changes": true, "agent_bindings": true})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return err
		}
		// Optional binding rows remain absent until a later released creation path
		// writes one. No session/history/capture/receipt is decoded or rewritten.
		_, err = tx.ExecContext(ctx, `PRAGMA user_version=3`)
		return err
	})
}
