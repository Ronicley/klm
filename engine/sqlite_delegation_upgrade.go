package main

import (
	"context"
	"database/sql"
	"errors"
)

func (s *sqliteStore) upgradeDelegationSchema(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
			return err
		}
		if version == 4 {
			return nil
		}
		if version != 3 {
			return errors.New("Delegation storage upgrade requires publication schema 3.")
		}
		ddl, err := sqliteDomainSchemaSelection(map[string]bool{"delegation_batches": true, "delegation_calls": true, "delegation_invocations": true, "delegation_children": true})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return err
		}
		// No native-history/child/manifest inference. Optional JSON metadata remains
		// absent, and existing agent_bindings/captures/receipts are never rewritten.
		_, err = tx.ExecContext(ctx, `PRAGMA user_version=4`)
		return err
	})
}
