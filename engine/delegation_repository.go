package main

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// Shared schema/shape readers and CAS writers are A-owned sqlite_delegation.go.
// Runtime orchestration, cards and scoped reads remain in B-owned files.
func delegationDomainsKnown() bool {
	return sqliteKnownTable("delegation_batches") && sqliteKnownTable("delegation_invocations") && sqliteKnownTable("delegation_children") && sqliteKnownTable("delegation_calls")
}

func (st *sqliteSessionTx) existingDelegation(parent, operation string) (*DelegationBatchRecord, error) {
	var id string
	err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_delegation_batches WHERE x_parentSessionId=? AND x_operationId=? LIMIT 1`, parent, operation).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value, err := st.delegationBatch(id)
	return &value, err
}

func (st *sqliteSessionTx) activeDelegation(parent string) (*DelegationBatchRecord, error) {
	var id string
	err := st.tx.QueryRowContext(st.ctx, `SELECT b.id FROM data_delegation_batches b WHERE x_parentSessionId=? AND (x_state<>'settled' OR x_delivery IN ('pending','sending','uncertain') OR x_outcome='finality_unconfirmed' OR EXISTS(SELECT 1 FROM data_delegation_calls c WHERE c.owner_id=b.id AND c.x_delivery IN ('pending','sending','uncertain'))) LIMIT 1`, parent).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value, err := st.delegationBatch(id)
	return &value, err
}

func (st *sqliteSessionTx) putDelegationBatch(batch *DelegationBatchRecord) error {
	expected := batch.Revision
	if _, err := st.delegationBatch(batch.ID); errors.Is(err, sql.ErrNoRows) {
		expected = ""
	} else if err != nil {
		return err
	}
	saved, err := st.saveDelegationBatch(*batch, expected)
	if err == nil {
		*batch = saved
	}
	return err
}

func (st *sqliteSessionTx) upsertDelegationEvent(owner string, e Event) error {
	var key string
	err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_session_events WHERE owner_id=? AND record_id=? LIMIT 1`, owner, e.ID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = st.appendEvent(owner, e)
		return err
	}
	if err != nil {
		return err
	}
	return st.patchEventMetadata(owner, key, func(current *Event) error {
		current.Status, current.Data, current.Title = e.Status, e.Data, e.Title
		return nil
	})
}

// Public cards contain routing, effective settings and factual outcome only.
func (st *sqliteSessionTx) syncDelegationCards(batch DelegationBatchRecord, members []DelegationInvocationRecord) error {
	terminal := 0
	for _, item := range members {
		if item.State == DelegationInvocationTerminal {
			terminal++
		}
		e := Event{ID: "delegation/" + item.ID, Type: "subagent", Title: "Subagent", CreatedAt: item.CreatedAt, Status: "running", Data: map[string]any{
			"managed": true, "kind": "invocation", "batchId": batch.ID, "invocationId": item.ID, "childSessionId": item.ChildSessionID, "parentSessionId": batch.ParentSessionID, "ownerConversationId": batch.OwnerConversationID, "key": item.Key,
			"harness": item.Capture.Config.Harness, "model": item.Capture.Config.Model, "effort": item.Capture.Config.Effort, "executionState": item.State, "outcome": item.Outcome,
		}}
		if item.Capture.Source != nil {
			e.Data["agentId"] = item.Capture.Source.Source.ID
		}
		if item.State == DelegationInvocationTerminal {
			e.Status = string(item.Outcome)
		}
		if err := st.upsertDelegationEvent(batch.ParentSessionID, e); err != nil {
			return err
		}
	}
	e := Event{ID: "delegation-batch/" + batch.ID, Type: "subagent", Title: "Subagents", Status: "running", CreatedAt: batch.CreatedAt, Data: map[string]any{
		"managed": true, "kind": "batch", "id": batch.ID, "batchId": batch.ID, "parentSessionId": batch.ParentSessionID, "state": batch.State, "total": batch.ExpectedChildren, "terminal": terminal, "remaining": batch.ExpectedChildren - terminal,
		"waiting": batch.Delivery != DelegationDeliveryDelivered && batch.Delivery != DelegationDeliveryCancelled, "delivery": batch.Delivery, "outcome": batch.Outcome, "revision": batch.Revision,
	}}
	if batch.State == DelegationSettled {
		e.Status = string(batch.Outcome)
	}
	return st.upsertDelegationEvent(batch.ParentSessionID, e)
}

func (st *sqliteSessionTx) finishDelegationItem(id string, outcome DelegationOutcome, finality DelegationFinality, result, message string, eventEnd int64) error {
	item, err := st.delegationInvocation(id)
	if err != nil {
		return err
	}
	if item.State == DelegationInvocationTerminal {
		return nil
	}
	if eventEnd == 0 {
		if err := st.tx.QueryRowContext(st.ctx, `SELECT size FROM record_collections WHERE parent_table='sessions' AND parent_id=? AND field='events'`, item.ChildSessionID).Scan(&eventEnd); err != nil {
			return err
		}
	}
	item.State, item.EventEnd = DelegationInvocationDraining, eventEnd
	item, err = completeDelegationInvocation(item, outcome, finality, result, message, now())
	if err != nil {
		return err
	}
	if err := st.putDelegationInvocation(item); err != nil {
		return err
	}
	child, err := st.delegationChild(item.ChildSessionID)
	if err != nil {
		return err
	}
	child, err = finishDelegationChild(child, item)
	if err != nil {
		return err
	}
	if err := st.putDelegationChild(child); err != nil {
		return err
	}
	session, err := st.sessionMeta(item.ChildSessionID)
	if err != nil {
		return err
	}
	session.Status, session.UpdatedAt = "idle", now()
	if outcome == DelegationFailed || outcome == DelegationFinalityUnconfirmed {
		session.Status = "error"
	}
	return st.putSessionMeta(session)
}

func (st *sqliteSessionTx) settleDelegation(id string) (DelegationBatchResult, error) {
	batch, err := st.delegationBatch(id)
	if err != nil {
		return DelegationBatchResult{}, err
	}
	members, err := st.delegationMembers(id)
	if err != nil {
		return DelegationBatchResult{}, err
	}
	if batch.State == DelegationSettled {
		return buildDelegationResult(batch, members)
	}
	batch.State = DelegationJoining
	batch, result, err := settleDelegationBatch(batch, members, now())
	if err != nil {
		return result, err
	}
	if err := st.putDelegationBatch(&batch); err != nil {
		return result, err
	}
	return result, st.syncDelegationCards(batch, members)
}

func (st *sqliteSessionTx) delegationReadOwner(owner string) (Session, error) {
	s, err := st.sessionMeta(owner)
	if err != nil {
		return s, err
	}
	if !delegationParentRole(s.Role) {
		return s, sql.ErrNoRows
	}
	if s.Role != sessionRoleGeneralAgent {
		available, err := st.projectAvailable(s.ProjectID)
		if err != nil {
			return s, err
		}
		if !available {
			return s, sql.ErrNoRows
		}
	}
	return s, nil
}

func (a *app) readDelegationOutput(ctx context.Context, owner, batchID, invocationID, digest string, offset int64, limit int, activations ...string) (DelegationOutputPage, error) {
	var page DelegationOutputPage
	if !delegationDomainsKnown() {
		return page, errors.New("Delegation storage is unavailable.")
	}
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		if _, err := st.delegationReadOwner(owner); err != nil {
			return err
		}
		batch, err := st.delegationBatch(batchID)
		if err != nil {
			return err
		}
		activation := ""
		if len(activations) > 0 {
			activation = activations[0]
		}
		if batch.ParentSessionID != owner || batch.Graph != nil && batch.Graph.ActivationID != activation || batch.Graph == nil && activation != "" {
			return sql.ErrNoRows
		}
		item, err := st.delegationInvocation(invocationID)
		if err != nil {
			return err
		}
		if item.BatchID != batch.ID || item.ParentSessionID != owner {
			return sql.ErrNoRows
		}
		page, err = delegationOutputPage(item, digest, offset, limit)
		return err
	})
	return page, err
}

func delegationDecimalOffset(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	offset, err := strconv.ParseInt(value, 10, 64)
	if err != nil || offset < 0 {
		return 0, errors.New("Invalid output continuation offset.")
	}
	return offset, nil
}
