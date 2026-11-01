package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Correlate a real native result event to the persisted alias, never a model RPC.
func (c *delegationExecution) nativeResultSeen(callID, nativeTurnID string) error {
	a := c.parent.app
	a.mu.Lock()
	call, err := c.nativeCallLocked(c.ctx, callID, nativeTurnID)
	var batchID string
	if err == nil {
		err = a.store.readers.QueryRowContext(c.ctx, `SELECT owner_id FROM data_delegation_calls WHERE x_parentTurnId=? AND x_sessionId=? AND x_turnId=? AND x_callId=?`, call.ParentTurnID, call.SessionID, call.TurnID, call.CallID).Scan(&batchID)
	}
	a.mu.Unlock()
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} // A rejected input has no admission.
	if err != nil {
		return err
	}
	return c.resultSeen(batchID, callID, nativeTurnID)
}

func (st *sqliteSessionTx) setDelegationAliasDelivery(batchID string, call DelegationNativeCall, next DelegationDelivery) error {
	var key string
	err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_delegation_calls WHERE owner_id=? AND x_parentTurnId=? AND x_sessionId=? AND x_turnId=? AND x_callId=?`, batchID, call.ParentTurnID, call.SessionID, call.TurnID, call.CallID).Scan(&key)
	if err != nil {
		return err
	}
	var previous DelegationNativeCall
	if err := decodeSQLiteRecord(st.ctx, st.tx, "delegation_calls", key, 64<<10, &previous); err != nil {
		return err
	}
	if previous.Delivery == next {
		return nil
	}
	previous.Delivery = next
	return st.putExistingRecord("delegation_calls", key, previous)
}

// Called before the consolidated bytes leave the owned bridge/native handler.
func (c *delegationExecution) returning(batchID, callID, nativeTurnID string) (DelegationBatchResult, error) {
	var result DelegationBatchResult
	a := c.parent.app
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !c.liveLocked() {
		return result, errors.New("Delegation owner is not active.")
	}
	call, err := c.nativeCallLocked(ctx, callID, nativeTurnID)
	if err != nil {
		return result, err
	}
	err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		batch, err := st.delegationBatch(batchID)
		if err != nil {
			return err
		}
		if !c.ownsBatch(batch) || batch.State != DelegationSettled {
			return errors.New("Delegation result is not settled for this owner.")
		}
		members, err := st.delegationMembers(batchID)
		if err != nil {
			return err
		}
		result, err = buildDelegationResult(batch, members)
		if err != nil {
			return err
		}
		if !result.CanContinue {
			return errDelegationParentStopRequired
		}
		if err := st.setDelegationAliasDelivery(batchID, call, DelegationDeliverySending); err != nil {
			return err
		}
		if batch.Delivery == DelegationDeliveryPending {
			batch.Delivery, batch.DeliveryTurnID, batch.UpdatedAt = DelegationDeliverySending, c.parentTurnID, now()
			if err := st.putDelegationBatch(&batch); err != nil {
				return err
			}
		}
		return st.syncDelegationCards(batch, members)
	})
	return result, err
}

// Only an adapter's correlated native tool-result event invokes this method.
// It is intentionally not an RPC callable by the model or an external MCP.
func (c *delegationExecution) resultSeen(batchID, callID, nativeTurnID string) error {
	a := c.parent.app
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !c.liveLocked() {
		return errors.New("Native result does not belong to an active owner.")
	}
	call, err := c.nativeCallLocked(ctx, callID, nativeTurnID)
	if err != nil {
		return err
	}
	err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		batch, err := st.delegationBatch(batchID)
		if err != nil {
			return err
		}
		if !c.ownsBatch(batch) || batch.State != DelegationSettled {
			return errors.New("Native result correlation does not match its batch.")
		}
		var delivery string
		if err := st.tx.QueryRowContext(ctx, `SELECT x_delivery FROM data_delegation_calls WHERE owner_id=? AND x_parentTurnId=? AND x_sessionId=? AND x_turnId=? AND x_callId=?`, batchID, call.ParentTurnID, call.SessionID, call.TurnID, call.CallID).Scan(&delivery); err != nil {
			return err
		}
		if delivery != "sending" && delivery != "delivered" {
			return errors.New("Native result arrived before consolidated delivery.")
		}
		if err := st.setDelegationAliasDelivery(batchID, call, DelegationDeliveryDelivered); err != nil {
			return err
		}
		var pending int
		if err := st.tx.QueryRowContext(ctx, `SELECT count(*) FROM data_delegation_calls WHERE owner_id=? AND x_parentTurnId=? AND x_delivery<>'delivered'`, batchID, c.parentTurnID).Scan(&pending); err != nil {
			return err
		}
		if pending == 0 && batch.Delivery == DelegationDeliverySending {
			batch.Delivery, batch.UpdatedAt = DelegationDeliveryDelivered, now()
			if err := st.putDelegationBatch(&batch); err != nil {
				return err
			}
		}
		members, err := st.delegationMembers(batchID)
		if err != nil {
			return err
		}
		return st.syncDelegationCards(batch, members)
	})
	c.notifyLocked()
	a.wakeSteeringLocked(c.parent.id)
	return err
}

func (c *delegationExecution) wait(ctx context.Context, batchID, revision, callID, nativeTurnID string) (DelegationWaitReply, error) {
	a := c.parent.app
	read := func() (DelegationBatchRecord, <-chan struct{}, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if !c.liveLocked() {
			return DelegationBatchRecord{}, nil, errors.New("Delegation owner stopped.")
		}
		var batch DelegationBatchRecord
		err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
			var err error
			batch, err = (&sqliteSessionTx{ctx: ctx, tx: tx}).delegationBatch(batchID)
			return err
		})
		if err == nil && !c.ownsBatch(batch) {
			err = sql.ErrNoRows
		}
		return batch, c.changed, err
	}
	batch, changed, err := read()
	if err != nil {
		return DelegationWaitReply{}, err
	}
	if batch.State != DelegationSettled && batch.Revision == revision {
		timer := time.NewTimer(8 * time.Second)
		defer timer.Stop()
		select {
		case <-changed:
		case <-timer.C:
		case <-ctx.Done():
			return DelegationWaitReply{}, ctx.Err()
		case <-c.ctx.Done():
			return DelegationWaitReply{}, c.ctx.Err()
		}
		batch, _, err = read()
		if err != nil {
			return DelegationWaitReply{}, err
		}
	}
	reply := DelegationWaitReply{Revision: batch.Revision, State: "waiting"}
	if batch.State == DelegationSettled {
		result, err := c.returning(batch.ID, callID, nativeTurnID)
		if errors.Is(err, errDelegationParentStopRequired) {
			reply.State, reply.StopRequired = "stop", true
			return reply, nil
		}
		if err != nil {
			return reply, err
		}
		reply.State, reply.Result = "settled", &result
	}
	return reply, nil
}
