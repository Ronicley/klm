package main

import (
	"context"
	"database/sql"
	"errors"
)

func (s *sqliteStore) upgradeForkSchema(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
			return err
		}
		if version >= 5 && version <= sqliteSchemaVersion {
			return nil
		}
		if version != 4 {
			return errors.New("Fork storage upgrade requires publication schema 4.")
		}
		ddl, err := sqliteDomainSchemaSelection(map[string]bool{"opencode_settings": true, "speech_settings": true, "deleted_sessions": true, "deleted_operations": true, "chat_images": true})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `PRAGMA user_version=5`)
		return err
	})
}
