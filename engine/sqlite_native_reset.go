package main

import (
	"encoding/json"
	"errors"
	"reflect"
)

// Ordinary writes cannot reset an instrumented native thread. User edits must
// archive its exact identity in a new, hidden status event in this transaction.
func (st *sqliteSessionTx) validateNativeResetArchive(id string, previous nativeSession) error {
	archiveID := st.nativeResets[id]
	if archiveID == "" {
		return errors.New("Native delegation manifest/thread identity must be preserved.")
	}
	var revision string
	if err := st.tx.QueryRowContext(st.ctx, `SELECT revision FROM data_session_events WHERE owner_id=? AND record_id=?`, id, archiveID).Scan(&revision); err != nil {
		return err
	}
	e, _, err := st.eventByID(id, archiveID)
	if err != nil {
		return err
	}
	if revision != st.changes.Revision || e.Type != "status" || e.Status != "completed" {
		return errors.New("Native reset requires a new owned archive event.")
	}
	raw, err := json.Marshal(e.Data["nativeBeforeReset"])
	if err != nil {
		return err
	}
	var archived nativeSession
	if err := json.Unmarshal(raw, &archived); err != nil {
		return err
	}
	if !reflect.DeepEqual(archived, previous) {
		return errors.New("Native reset archive does not match the previous thread identity.")
	}
	var pending bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_delegation_batches WHERE x_parentSessionId=? AND (x_state<>'settled' OR x_delivery NOT IN ('delivered','cancelled'))) OR EXISTS(SELECT 1 FROM data_delegation_children WHERE x_parentSessionId=? AND x_quarantined='true')`, id, id).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return controlError(409, "Finish pending delegation and resolve uncertain child processes before resetting this conversation.")
	}
	return nil
}
