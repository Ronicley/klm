package main

import (
	"database/sql"
	"errors"
)

func (st *sqliteSessionTx) markManagedRequestOwner(childID string) error {
	var parent string
	err := st.tx.QueryRowContext(st.ctx, `SELECT x_parentSessionId FROM data_delegation_children WHERE id=?`, childID).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, ok := st.changes.Sessions[parent]; !ok {
		st.changes.Sessions[parent] = []int64{}
	}
	var owner string
	err = st.tx.QueryRowContext(st.ctx, `SELECT b.x_ownerConversationId FROM data_delegation_invocations i JOIN data_delegation_batches b ON b.id=i.x_batchId WHERE i.x_childSessionId=? AND i.x_state<>'terminal' LIMIT 1`, childID).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if owner != "" {
		if _, ok := st.changes.Sessions[owner]; !ok {
			st.changes.Sessions[owner] = []int64{}
		}
	}
	return nil
}
