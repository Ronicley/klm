package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

func (st *sqliteSessionTx) delegationBatch(id string) (DelegationBatchRecord, error) {
	var value DelegationBatchRecord
	err := decodeSQLiteRecord(st.ctx, st.tx, "delegation_batches", id, 1<<20, &value)
	return value, err
}
func (st *sqliteSessionTx) delegationChild(id string) (DelegationChildRecord, error) {
	var value DelegationChildRecord
	err := decodeSQLiteRecord(st.ctx, st.tx, "delegation_children", id, 1<<20, &value)
	return value, err
}
func (st *sqliteSessionTx) delegationInvocation(id string) (DelegationInvocationRecord, error) {
	var value DelegationInvocationRecord
	err := decodeSQLiteRecord(st.ctx, st.tx, "delegation_invocations", id, maxStreamRecordBytes, &value)
	return value, err
}

func (st *sqliteSessionTx) delegationMembers(batchID string) ([]DelegationInvocationRecord, error) {
	ids, err := sqliteQueryIDs(st.ctx, st.tx, `SELECT id FROM data_delegation_invocations WHERE x_batchId=? ORDER BY CAST(x_ordinal AS INTEGER),id LIMIT 9`, batchID)
	if err != nil {
		return nil, err
	}
	if len(ids) > delegationMaxChildren {
		return nil, errors.New("Delegation batch exceeds its membership bound.")
	}
	result := []DelegationInvocationRecord{}
	for _, id := range ids {
		value, err := st.delegationInvocation(id)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

// State/dispatch/delivery CAS counter belongs to this repository, not the pure
// transition helpers. A nonempty Calls slice is split into immutable alias rows.
func (st *sqliteSessionTx) saveDelegationBatch(value DelegationBatchRecord, expected string) (DelegationBatchRecord, error) {
	old, err := st.delegationBatch(value.ID)
	fresh := errors.Is(err, sql.ErrNoRows)
	if err != nil && !fresh {
		return value, err
	}
	if fresh {
		if expected != "" {
			return value, controlError(409, "Delegation batch was removed.")
		}
		value.Revision = "0"
	} else {
		if expected != old.Revision {
			return value, controlError(409, "Delegation batch revision changed.")
		}
		value.Revision, err = nextAgentRevision(old.Revision)
		if err != nil {
			return value, err
		}
	}
	if err := value.Validate(); err != nil {
		return value, err
	}
	aliases := value.Calls
	value.Calls = nil
	if err := st.putRoot("delegation_batches", value.ID, value); err != nil {
		return value, err
	}
	if fresh {
		if err := st.emptyCollection("delegation_batches", value.ID, "calls", "array"); err != nil {
			return value, err
		}
		if err := st.appendDelegationCall(value.ID, value.NativeCall); err != nil {
			return value, err
		}
	}
	for _, call := range aliases {
		if err := st.appendDelegationCall(value.ID, call); err != nil {
			return value, err
		}
	}
	return value, nil
}

func validateDelegationCall(call DelegationNativeCall) error {
	if !delegationText(call.ParentTurnID, 256) || !delegationText(call.SessionID, 4096) || !delegationText(call.CallID, 256) {
		return errors.New("Incomplete delegation native call correlation.")
	}
	return nil
}

func sameDelegationStoredCallIdentity(left, right DelegationNativeCall) bool {
	return left.ParentTurnID == right.ParentTurnID && left.SessionID == right.SessionID && left.TurnID == right.TurnID && left.CallID == right.CallID
}
func validateDelegationAliasDelivery(previous, next []byte) error {
	var old, new struct {
		Delivery DelegationDelivery `json:"delivery"`
	}
	if err := json.Unmarshal(previous, &old); err != nil {
		return err
	}
	if err := json.Unmarshal(next, &new); err != nil {
		return err
	}
	if old.Delivery == new.Delivery {
		return nil
	}
	valid := old.Delivery == "" && (new.Delivery == DelegationDeliveryPending || new.Delivery == DelegationDeliverySending || new.Delivery == DelegationDeliveryUncertain || new.Delivery == DelegationDeliveryCancelled) || old.Delivery == DelegationDeliveryPending && (new.Delivery == DelegationDeliverySending || new.Delivery == DelegationDeliveryUncertain || new.Delivery == DelegationDeliveryCancelled) || old.Delivery == DelegationDeliverySending && (new.Delivery == DelegationDeliveryDelivered || new.Delivery == DelegationDeliveryUncertain || new.Delivery == DelegationDeliveryCancelled)
	if !valid {
		return errors.New("Invalid delegation alias delivery transition.")
	}
	return nil
}

func sameSettledDelegationPayload(previous, next DelegationBatchRecord) bool {
	left, _ := json.Marshal(previous)
	right, _ := json.Marshal(next)
	var a, b map[string]any
	_ = json.Unmarshal(left, &a)
	_ = json.Unmarshal(right, &b)
	for _, key := range []string{"delivery", "deliveryTurnId", "revision", "updatedAt", "cancellationRequested"} {
		delete(a, key)
		delete(b, key)
	}
	if call, ok := a["nativeCall"].(map[string]any); ok {
		delete(call, "delivery")
	}
	if call, ok := b["nativeCall"].(map[string]any); ok {
		delete(call, "delivery")
	}
	return reflect.DeepEqual(a, b)
}

func (st *sqliteSessionTx) appendDelegationCall(batchID string, call DelegationNativeCall) error {
	if err := validateDelegationCall(call); err != nil {
		return err
	}
	var existing string
	err := st.tx.QueryRowContext(st.ctx, `SELECT owner_id FROM data_delegation_calls WHERE x_parentTurnId=? AND x_sessionId=? AND x_turnId=? AND x_callId=?`, call.ParentTurnID, call.SessionID, call.TurnID, call.CallID).Scan(&existing)
	if err == nil {
		if existing != batchID {
			return controlError(409, "Native call alias belongs to a different delegation batch.")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := st.delegationBatch(batchID); err != nil {
		return err
	}
	hash, err := delegationHash([4]string{call.ParentTurnID, call.SessionID, call.TurnID, call.CallID})
	if err != nil {
		return err
	}
	return st.putRecord("delegation_calls", sqliteOwnedKey(batchID, hash), batchID, hash, -1, call)
}

func (st *sqliteSessionTx) putDelegationChild(value DelegationChildRecord) error {
	if err := value.Validate(); err != nil {
		return err
	}
	return st.putRoot("delegation_children", value.ID, value)
}
func (st *sqliteSessionTx) putDelegationInvocation(value DelegationInvocationRecord) error {
	if err := value.Validate(); err != nil {
		return err
	}
	return st.putRoot("delegation_invocations", value.ID, value)
}

func (st *sqliteSessionTx) putNativeDelegationMetadata(sessionID string, value nativeSession) error {
	return st.putRoot("native_sessions", sessionID, value)
}

// This gate applies even when future callers use generic putRoot. Cross-record
// checks run once at transaction end so all identities/leases can be admitted
// atomically in any safe write order, never half committed.
func (st *sqliteSessionTx) validateDelegationWrite(table, id, owner string, raw []byte) error {
	switch table {
	case "sessions":
		var next Session
		if err := json.Unmarshal(raw, &next); err != nil {
			return err
		}
		return st.validateManagedSessionUpdate(next)
	case "delegation_batches":
		var next DelegationBatchRecord
		if err := json.Unmarshal(raw, &next); err != nil {
			return err
		}
		if next.ID != id {
			return errors.New("Delegation batch key differs from its identity.")
		}
		if err := next.Validate(); err != nil {
			return err
		}
		old, err := st.delegationBatch(id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if next.ParentSessionID != old.ParentSessionID || next.OwnerConversationID != old.OwnerConversationID || next.ParentTurnID != old.ParentTurnID || next.OperationID != old.OperationID || next.RequestHash != old.RequestHash || !sameDelegationStoredCallIdentity(next.NativeCall, old.NativeCall) || next.ExpectedChildren != old.ExpectedChildren || !reflect.DeepEqual(next.Graph, old.Graph) {
			return errors.New("Accepted delegation receipt identity cannot change.")
		}
		if !reflect.DeepEqual(old, next) {
			expected, err := nextAgentRevision(old.Revision)
			if err != nil || next.Revision != expected {
				return controlError(409, "Delegation batch needs an exact next CAS revision.")
			}
		}
		if old.State == DelegationSettled {
			if !sameSettledDelegationPayload(old, next) {
				return errors.New("Settled delegation result is immutable.")
			}
			if old.CancellationRequested && !next.CancellationRequested {
				return errors.New("Cancellation intent cannot be cleared.")
			}
			if next.Delivery != old.Delivery {
				if _, err := advanceDelegationDelivery(old, next.Delivery); err != nil {
					return err
				}
			}
		}
	case "delegation_invocations":
		var next DelegationInvocationRecord
		if err := json.Unmarshal(raw, &next); err != nil {
			return err
		}
		if next.ID != id {
			return errors.New("Delegation invocation key differs from its identity.")
		}
		if err := next.Validate(); err != nil {
			return err
		}
		old, err := st.delegationInvocation(id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if old.State == DelegationInvocationTerminal && !reflect.DeepEqual(old, next) {
			return errors.New("Terminal delegation invocation is immutable.")
		}
		if old.BatchID != next.BatchID || old.Key != next.Key || old.Ordinal != next.Ordinal || old.ChildSessionID != next.ChildSessionID || old.ParentSessionID != next.ParentSessionID || old.ParentTurnID != next.ParentTurnID || old.Task != next.Task || old.ExecutionCWD != next.ExecutionCWD || old.BindingRevision != next.BindingRevision || !reflect.DeepEqual(old.Capture, next.Capture) || !reflect.DeepEqual(old.Graph, next.Graph) {
			return errors.New("Admitted delegation task/configuration/ownership is immutable.")
		}
		if old.DispatchAttempted && !next.DispatchAttempted || old.NativeAccepted && !next.NativeAccepted {
			return errors.New("Delegation dispatch evidence cannot be erased.")
		}
	case "delegation_children":
		var next DelegationChildRecord
		if err := json.Unmarshal(raw, &next); err != nil {
			return err
		}
		if next.ID != id {
			return errors.New("Delegation child map key differs from the session identity.")
		}
		if err := next.Validate(); err != nil {
			return err
		}
		old, err := st.delegationChild(id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if old.ParentSessionID != next.ParentSessionID || old.Harness != next.Harness || old.ExecutionCWD != next.ExecutionCWD || old.GraphRunID != next.GraphRunID || old.WorkspaceID != next.WorkspaceID || old.Origin != next.Origin {
			return errors.New("Managed child parent/harness/workspace ownership is immutable.")
		}
		if old.ActiveInvocationID != "" && old.ActiveInvocationID != next.ActiveInvocationID {
			item, err := st.delegationInvocation(old.ActiveInvocationID)
			if err != nil {
				return err
			}
			if item.State != DelegationInvocationTerminal || !item.Finality.Confirmed() {
				return errors.New("Child lease cannot be released without confirmed finality evidence.")
			}
		}
		if old.Quarantined && !next.Quarantined {
			item, err := st.delegationInvocation(old.ActiveInvocationID)
			if err != nil || !item.Finality.Confirmed() {
				return errors.New("Child quarantine cannot be cleared by assertion.")
			}
		}
	case "delegation_calls":
		var next DelegationNativeCall
		if err := json.Unmarshal(raw, &next); err != nil {
			return err
		}
		if err := validateDelegationCall(next); err != nil {
			return err
		}
		var previousRaw json.RawMessage
		err := decodeSQLiteRecord(st.ctx, st.tx, table, id, 64<<10, &previousRaw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			var previous DelegationNativeCall
			if err := json.Unmarshal(previousRaw, &previous); err != nil {
				return err
			}
			if !sameDelegationStoredCallIdentity(previous, next) {
				return errors.New("Accepted native call alias identity is immutable.")
			}
			if err := validateDelegationAliasDelivery(previousRaw, raw); err != nil {
				return err
			}
		} else {
			if err := validateDelegationAliasDelivery([]byte(`{}`), raw); err != nil {
				return err
			}
		}
	case "native_sessions":
		var next nativeSession
		if err := json.Unmarshal(raw, &next); err != nil {
			return err
		}
		if err := validateNativeDelegationShape(next); err != nil {
			return err
		}
		var previous nativeSession
		err := decodeSQLiteRecord(st.ctx, st.tx, table, id, 64<<10, &previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if previous.ID != "" && previous.Delegation == nil && next.Delegation != nil {
			return errors.New("An existing native history cannot adopt delegation metadata.")
		}
		if previous.Delegation != nil && (previous.ID != next.ID || !reflect.DeepEqual(previous.Delegation, next.Delegation)) {
			return errors.New("Native delegation manifest/thread identity must be preserved.")
		}
		if next.Delegation != nil {
			session, err := st.sessionMeta(id)
			if err != nil {
				return err
			}
			if session.Harness != "codex" || !delegationParentRole(session.Role) {
				return errors.New("Only a Codex parent history can persist a delegation manifest.")
			}
		}
	}
	return nil
}

func validateNativeDelegationShape(value nativeSession) error {
	if value.Delegation == nil {
		return nil
	}
	manifest := value.Delegation
	if value.ID == "" || manifest.Version < 1 || !delegationText(manifest.ToolName, 200) || !delegationDigest(manifest.SpecDigest) {
		return errors.New("Invalid native delegation manifest.")
	}
	return nil
}

func (st *sqliteSessionTx) markDelegationRecord(table, id, owner string, raw []byte) error {
	var parentID, childID string
	switch table {
	case "delegation_batches":
		var row DelegationBatchRecord
		if err := json.Unmarshal(raw, &row); err != nil {
			return err
		}
		parentID = row.ParentSessionID
	case "delegation_invocations":
		var row DelegationInvocationRecord
		if err := json.Unmarshal(raw, &row); err != nil {
			return err
		}
		parentID, childID = row.ParentSessionID, row.ChildSessionID
	case "delegation_children":
		var row DelegationChildRecord
		if err := json.Unmarshal(raw, &row); err != nil {
			return err
		}
		parentID, childID = row.ParentSessionID, row.ID
	case "delegation_calls":
		batch, err := st.delegationBatch(owner)
		if err != nil {
			return err
		}
		parentID = batch.ParentSessionID
	default:
		return nil
	}
	parent, err := st.sessionMeta(parentID)
	if err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, "UPDATE data_"+table+" SET project_id=?,session_id=?,parent_id=? WHERE id=?", parent.ProjectID, parentID, parentID, id); err != nil {
		return err
	}
	st.changes.Sessions[parentID] = []int64{}
	if childID != "" {
		st.changes.Sessions[childID] = []int64{}
	}
	if parent.ProjectID != "" {
		st.changes.Projects[parent.ProjectID] = true
	}
	if parent.Role == "graph_node" {
		run, err := (sqliteValidation{ctx: st.ctx, tx: st.tx}).run(parent.GraphRunID)
		if err != nil {
			return err
		}
		st.changes.Sessions[run.ConversationID] = []int64{}
	}
	return nil
}

func delegationStorageDomain(table string) bool {
	return table == "delegation_batches" || table == "delegation_invocations" || table == "delegation_children" || table == "delegation_calls"
}

func (st *sqliteSessionTx) validateDelegationChanges() error {
	batches := map[string]bool{}
	children := map[string]bool{}
	for table, ids := range st.changes.Records {
		if !delegationStorageDomain(table) {
			continue
		}
		for _, id := range ids {
			switch table {
			case "delegation_batches":
				batches[id] = true
			case "delegation_children":
				children[id] = true
			case "delegation_calls":
				var owner string
				err := st.tx.QueryRowContext(st.ctx, `SELECT owner_id FROM data_delegation_calls WHERE id=?`, id).Scan(&owner)
				if err == nil {
					batches[owner] = true
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			case "delegation_invocations":
				row, err := st.delegationInvocation(id)
				if err == nil {
					batches[row.BatchID] = true
					children[row.ChildSessionID] = true
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
		}
	}
	v := sqliteValidation{ctx: st.ctx, tx: st.tx}
	for table := range st.changes.Invalidated {
		if delegationStorageDomain(table) {
			return v.validateDelegationRecords()
		}
	}
	for _, id := range sortedGraphKeys(batches) {
		exists, err := v.exists("delegation_batches", id)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("Delegation batch %s cannot be deleted by a state transition.", id)
		}
		if err := v.validateDelegationBatch(id); err != nil {
			return err
		}
	}
	for _, id := range sortedGraphKeys(children) {
		exists, err := v.exists("delegation_children", id)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("Managed child ownership cannot be deleted by a state transition.")
		}
		if err := v.validateManagedDelegationChild(id); err != nil {
			return err
		}
	}
	return nil
}
