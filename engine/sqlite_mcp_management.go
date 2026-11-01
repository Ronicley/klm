package main

import (
	"context"
	"database/sql"
	"errors"
)

// Caller holds app.mu. Only the exact connection configuration is read; upstream
// credential material is separately encrypted and never part of this projection.
func (a *app) mcpConnectionLocked(ctx context.Context, id string) (MCPConnection, bool, error) {
	if a.store == nil {
		c, ok := a.state.MCPConnections[id]
		return c, ok, nil
	}
	var c MCPConnection
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error { return decodeSQLiteRecord(ctx, tx, "mcp_connections", id, 8<<20, &c) })
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}
func (a *app) sqlSaveMCPConnectionLocked(ctx context.Context, c MCPConnection, old MCPConnection, exists bool, blob []byte) error {
	return a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		var pending bool
		if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return controlError(409, "A library deletion needs recovery before editing connections.")
		}
		if exists {
			var current MCPConnection
			if err := decodeSQLiteRecord(ctx, st.tx, "mcp_connections", c.ID, 8<<20, &current); err != nil {
				return err
			}
			if current.Revision != old.Revision {
				return controlError(409, "Connection changed. Reload before saving.")
			}
		} else {
			var count int
			if err := st.tx.QueryRowContext(ctx, `SELECT count(*) FROM data_mcp_connections`).Scan(&count); err != nil {
				return err
			}
			if count >= 64 {
				return controlError(409, "Library supports at most 64 MCP connections.")
			}
		}
		if err := st.putRoot("mcp_connections", c.ID, c); err != nil {
			return err
		}
		if blob != nil {
			if err := st.putRoot("mcp_credentials", c.CredentialID, blob); err != nil {
				return err
			}
		}
		return st.pruneMCPCredentials()
	})
}
func (st *sqliteSessionTx) disconnectMCPCredentials(connection, current string) error {
	return st.eachMatching("mcp_credentials", `id=? OR id IN (SELECT credential FROM credential_connections WHERE connection=?)`, []any{current, connection}, func(id string) error { return st.deleteRecord("mcp_credentials", id) })
}
