package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type delegationChildBinding struct {
	owner           *delegationExecution
	invocationID    string
	promptAttempted bool                   // app.mu; set before native prompt bytes can be written
	result          delegationChildOutcome // published before turn.done
	mcpTracked      bool
	uncertainMCP    []DelegationUncertainCall
}

// This is the actual existing adapter executor, not another model loop. It is
// called only after the batch/identities/leases have durably committed.
func (a *app) executeDelegationChild(ctx context.Context, owner *delegationExecution, item DelegationInvocationRecord) delegationChildOutcome {
	failure := func(err error) delegationChildOutcome {
		return delegationChildOutcome{Outcome: DelegationFailed, Error: err.Error(), Finality: DelegationFinality{NativeSettled: true, ProcessesDrained: true, ToolCallsSettled: true}}
	}
	preparation, err := a.prepareAgentTurn(ctx, item.ChildSessionID, &item.Capture)
	if err != nil {
		return failure(err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	t := &turn{ctx: childCtx, cancel: cancel, done: make(chan struct{}), delegationChild: &delegationChildBinding{owner: owner, invocationID: item.ID, mcpTracked: true}}
	a.mu.Lock()
	if ctx.Err() != nil || a.closing || a.storageErr != nil || a.runs[item.ChildSessionID] != nil {
		a.mu.Unlock()
		cancel()
		return failure(errors.New("Child owner stopped or child already has an active execution."))
	}
	var session Session
	var native nativeSession
	err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		owned, err := st.delegationChild(item.ChildSessionID)
		if err != nil {
			return err
		}
		if owned.ActiveInvocationID != item.ID || owned.ParentSessionID != owner.parent.id || owned.Quarantined {
			return errors.New("Child execution lease changed before dispatch.")
		}
		session, err = st.sessionMeta(item.ChildSessionID)
		if err != nil {
			return err
		}
		session.Status, session.UpdatedAt = "running", now()
		if err := st.putSessionMeta(session); err != nil {
			return err
		}
		native, err = st.native(item.ChildSessionID)
		return err
	})
	b, installed := a.binaries[item.Capture.Config.Harness]
	if err != nil || !installed {
		a.mu.Unlock()
		cancel()
		if err == nil {
			err = errors.New("Child harness is unavailable.")
		}
		return failure(err)
	}
	a.runs[session.ID] = t
	a.wg.Add(1)
	a.mu.Unlock()
	go a.execute(t, session, native, b, item.ExecutionCWD, submission{Text: item.Task}, preparation)
	<-t.done // Native/owned runtime/MCP cleanup publishes the result before done.
	return t.delegationChild.result
}

func (p *adapter) delegationPromptAttempted() {
	if p.turn.delegationChild == nil {
		return
	}
	p.app.mu.Lock()
	p.turn.delegationChild.promptAttempted = true
	p.app.mu.Unlock()
}

func (p *adapter) delegationNativeStarted(nativeTurnID string) error {
	child := p.turn.delegationChild
	if child == nil {
		return nil
	}
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writeSQLLocked(p.turn.ctx, func(st *sqliteSessionTx) error {
		item, err := st.delegationInvocation(child.invocationID)
		if err != nil {
			return err
		}
		if item.State == DelegationInvocationRunning {
			return nil
		}
		if item.State != DelegationInvocationPreparing || !item.DispatchAttempted {
			return errors.New("Native child acceptance does not belong to its dispatch epoch.")
		}
		native, err := st.native(p.id)
		if err != nil {
			return err
		}
		if native.ID == "" {
			return errors.New("Native child acceptance has no native identity.")
		}
		item.NativeAccepted, item.NativeSessionID, item.NativeTurnID = true, native.ID, nativeTurnID
		item.StartedAt, item.State = now(), DelegationInvocationRunning
		batch, err := st.delegationBatch(item.BatchID)
		if err != nil {
			return err
		}
		if batch.State == DelegationPreparing {
			batch.State, batch.UpdatedAt = DelegationRunning, now()
			if err := st.putDelegationBatch(&batch); err != nil {
				return err
			}
		}
		return st.putDelegationInvocation(item)
	})
}

// Called after stream flush, native settlement, immediate owned-group shutdown
// and gateway cleanup. Incomplete MCP coverage is never manufactured as success.
func (p *adapter) captureDelegationChildResult(runErr error) {
	child := p.turn.delegationChild
	if child == nil {
		return
	}
	result := delegationChildOutcome{Outcome: DelegationCompleted}
	if runErr != nil || p.failed || !p.completed {
		result.Outcome = DelegationFailed
	}
	if p.turn.ctx.Err() != nil {
		result.Outcome = DelegationCancelled
	}
	if runErr != nil {
		result.Error = runErr.Error()
	}
	p.app.mu.Lock()
	attempted := child.promptAttempted
	p.app.mu.Unlock()
	result.Finality = DelegationFinality{NativeSettled: p.nativeSettled || !attempted, ProcessesDrained: p.processesDrained && !p.processDrainFailed && p.turn.stopErr == nil, ToolCallsSettled: p.mcp == nil || child.mcpTracked, UncertainToolCalls: child.uncertainMCP}
	if p.mcp != nil && len(p.mcp.routes) == 0 {
		result.Finality.ToolCallsSettled = true
	}
	if !result.Finality.Confirmed() {
		result.Outcome = DelegationFinalityUnconfirmed
		if result.Error == "" {
			result.Error = "Child native/process/tool finality could not be confirmed."
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := p.app.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		item, err := st.delegationInvocation(child.invocationID)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT size FROM record_collections WHERE parent_table='sessions' AND parent_id=? AND field='events'`, p.id).Scan(&result.EventEnd); err != nil {
			return err
		}
		var key string
		err = tx.QueryRowContext(ctx, `SELECT id FROM data_session_events WHERE owner_id=? AND position>=? AND kind='assistant' ORDER BY position DESC LIMIT 1`, p.id, item.EventStart).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var final Event
		if err := decodeSQLiteRecord(ctx, tx, "session_events", key, maxStreamRecordBytes, &final); err != nil {
			return err
		}
		result.Result = final.Text
		return nil
	})
	if err != nil {
		result.Outcome, result.Error = DelegationFailed, err.Error()
	}
	child.result = result
}
