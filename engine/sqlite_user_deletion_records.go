package main

import (
	"errors"
)

// Only explicit user deletion may purge immutable owned history, after tombstones.
func (st *sqliteSessionTx) deleteRecordForSessionDeletion(table, id string) error {
	return st.deleteRecordScoped(table, id, true)
}

func (st *sqliteSessionTx) authorizeSessionDeletionRecord(table, id string) error {
	owners := []string{}
	switch table {
	case "native_sessions":
		owners = append(owners, id)
	case "delegation_batches":
		var parent string
		if err := st.tx.QueryRowContext(st.ctx, `SELECT x_parentSessionId FROM data_delegation_batches WHERE id=?`, id).Scan(&parent); err != nil {
			return err
		}
		owners = append(owners, parent)
	case "delegation_invocations":
		var parent, child string
		if err := st.tx.QueryRowContext(st.ctx, `SELECT x_parentSessionId,x_childSessionId FROM data_delegation_invocations WHERE id=?`, id).Scan(&parent, &child); err != nil {
			return err
		}
		owners = append(owners, parent, child)
	case "delegation_children":
		var parent string
		if err := st.tx.QueryRowContext(st.ctx, `SELECT x_parentSessionId FROM data_delegation_children WHERE id=?`, id).Scan(&parent); err != nil {
			return err
		}
		owners = append(owners, parent, id)
	case "delegation_calls":
		var parent string
		if err := st.tx.QueryRowContext(st.ctx, `SELECT b.x_parentSessionId FROM data_delegation_calls c JOIN data_delegation_batches b ON b.id=c.owner_id WHERE c.id=?`, id).Scan(&parent); err != nil {
			return err
		}
		owners = append(owners, parent)
	default:
		return errors.New("Unknown protected deletion domain.")
	}
	for _, owner := range owners {
		if _, authorized := st.deletedSessions[owner]; !authorized {
			return errors.New("History purge requires explicit owning-session deletion.")
		}
		var tombstone deletedSession
		if err := decodeSQLiteRecord(st.ctx, st.tx, "deleted_sessions", owner, 1<<20, &tombstone); err != nil {
			return err
		}
		if _, rootDeleted := st.deletedSessions[tombstone.RootID]; tombstone.RootID == "" || !rootDeleted {
			return errors.New("History purge requires a durable owning-session tombstone.")
		}
	}
	return nil
}
