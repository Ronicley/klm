package main

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

type delegationAdmissionError struct{ code, message string }

func (e *delegationAdmissionError) Error() string { return e.message }
func delegationRejected(code, message string) error {
	return &delegationAdmissionError{code: code, message: message}
}

type delegationChildOutcome struct {
	Outcome       DelegationOutcome
	Finality      DelegationFinality
	Result, Error string
	EventEnd      int64
}

// Runner must own/join the existing adapter invocation and immediate process/MCP
// teardown. There is no default success runner or detached/background mode.
type delegationChildRunner func(context.Context, DelegationInvocationRecord) delegationChildOutcome

type delegationPreparation struct {
	id, hash string
	request  DelegationRunRequest
	call     DelegationNativeCall
	done     chan struct{}
	receipt  *DelegationAdmissionReceipt
	err      error
}

type delegationExecution struct {
	parent *adapter
	ctx    context.Context
	cancel context.CancelFunc
	runner delegationChildRunner
	wg     sync.WaitGroup
	// app.mu protects these fields; no lock is held through discovery/native I/O.
	closed       bool
	fatal        error
	preparing    *delegationPreparation
	preparations map[string]*delegationPreparation
	changed      chan struct{}
	parentTurnID string
}

type DelegationPrepareReply struct {
	Version       int                         `json:"version"`
	Preparing     bool                        `json:"preparing"`
	PreparationID string                      `json:"preparationId,omitempty"`
	Receipt       *DelegationAdmissionReceipt `json:"receipt,omitempty"`
}

func newDelegationExecution(parent *adapter, runner delegationChildRunner) (*delegationExecution, error) {
	if runner == nil || parent == nil || parent.app == nil || parent.turn == nil || parent.turn.ctx == nil || parent.app.store == nil || !delegationDomainsKnown() || parent.role == "graph_node" && parent.turn.graphDelegation == nil || parent.role == sessionRoleSubagent || !delegationParentRole(parent.role) {
		return nil, errors.New("Ordinary delegation runtime/storage is unavailable.")
	}
	ctx, cancel := context.WithCancel(parent.turn.ctx)
	turnID := newID()
	if parent.turn.graphDelegation != nil {
		turnID = parent.turn.graphDelegation.Scope.TurnID
	}
	return &delegationExecution{parent: parent, ctx: ctx, cancel: cancel, runner: runner, preparations: map[string]*delegationPreparation{}, changed: make(chan struct{}), parentTurnID: turnID}, nil
}

func (c *delegationExecution) liveLocked() bool {
	a := c.parent.app
	return !c.closed && c.fatal == nil && c.ctx.Err() == nil && !a.closing && a.storageErr == nil && a.runs[c.parent.id] == c.parent.turn
}

func (c *delegationExecution) notifyLocked() { close(c.changed); c.changed = make(chan struct{}) }

func (c *delegationExecution) nativeCallLocked(ctx context.Context, callID, nativeTurnID string) (DelegationNativeCall, error) {
	var native nativeSession
	err := c.parent.app.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		var readErr error
		native, readErr = (&sqliteSessionTx{ctx: ctx, tx: tx}).native(c.parent.id)
		return readErr
	})
	if err != nil {
		return DelegationNativeCall{}, err
	}
	if native.ID == "" || !delegationText(callID, 256) {
		return DelegationNativeCall{}, errors.New("Native parent call is not bound.")
	}
	return DelegationNativeCall{ParentTurnID: c.parentTurnID, SessionID: native.ID, TurnID: nativeTurnID, CallID: callID, Delivery: DelegationDeliveryPending}, nil
}

func delegationReceipt(batch DelegationBatchRecord) DelegationAdmissionReceipt {
	return DelegationAdmissionReceipt{Version: 1, Accepted: true, BatchID: batch.ID, OperationID: batch.OperationID, RequestHash: batch.RequestHash, ChildCount: batch.ExpectedChildren, MaxConcurrency: delegationConcurrency}
}

// Starts only owner-bound preparation. No child is dispatched/accepted until the
// whole captured set and receipt commit together. Every HTTP response is short.
func (c *delegationExecution) prepare(request DelegationRunRequest, callID, nativeTurnID string) (DelegationPrepareReply, error) {
	fastCtx, stopFast := context.WithTimeout(c.ctx, 5*time.Second)
	defer stopFast()
	reply := DelegationPrepareReply{Version: 1}
	if err := request.Validate(); err != nil {
		return reply, delegationRejected("invalid_request", err.Error())
	}
	hash, err := delegationHash(request)
	if err != nil {
		return reply, err
	}
	a := c.parent.app
	a.mu.Lock()
	if !c.liveLocked() {
		a.mu.Unlock()
		return reply, errors.New("Delegation owner is not active.")
	}
	if err := a.store.readSnapshot(fastCtx, func(tx *sql.Tx) error {
		return (&sqliteSessionTx{ctx: fastCtx, tx: tx}).checkGraphDelegationAdmission(c.parent.id, c.graphScope())
	}); err != nil {
		a.mu.Unlock()
		return reply, delegationRejected("batch_busy", err.Error())
	}
	call, err := c.nativeCallLocked(fastCtx, callID, nativeTurnID)
	if err != nil {
		a.mu.Unlock()
		return reply, err
	}
	var existing *DelegationBatchRecord
	err = a.writeSQLLocked(fastCtx, func(st *sqliteSessionTx) error {
		var readErr error
		existing, readErr = st.existingDelegation(c.parent.id, request.OperationID)
		if readErr != nil || existing == nil {
			return readErr
		}
		if existing.RequestHash != hash {
			return delegationRejected("operation_conflict", "operationId belongs to a different accepted request.")
		}
		if !c.ownsBatch(*existing) {
			return delegationRejected("operation_conflict", "operationId belongs to another graph activation/turn.")
		}
		active, err := st.activeDelegation(c.parent.id)
		if err != nil {
			return err
		}
		if active != nil && active.ID != existing.ID {
			return delegationRejected("batch_busy", "Parent already owns another batch or delivery.")
		}
		return st.appendDelegationCall(existing.ID, call)
	})
	if err != nil {
		a.mu.Unlock()
		return reply, err
	}
	if existing != nil {
		receipt := delegationReceipt(*existing)
		reply.Receipt = &receipt
		a.mu.Unlock()
		return reply, nil
	}
	if c.preparing != nil {
		if c.preparing.request.OperationID != request.OperationID || c.preparing.hash != hash {
			a.mu.Unlock()
			return reply, delegationRejected("batch_busy", "Parent already owns another preparation.")
		}
		// The later alias is durably attached when it obtains the final receipt;
		// it never starts another resolver/capture/dispatch job.
		reply.Preparing, reply.PreparationID = true, c.preparing.id
		a.mu.Unlock()
		return reply, nil
	}
	var active *DelegationBatchRecord
	err = a.store.readSnapshot(fastCtx, func(tx *sql.Tx) error {
		var readErr error
		active, readErr = (&sqliteSessionTx{ctx: fastCtx, tx: tx}).activeDelegation(c.parent.id)
		return readErr
	})
	if err != nil {
		a.mu.Unlock()
		return reply, err
	}
	if active != nil {
		a.mu.Unlock()
		return reply, delegationRejected("batch_busy", "Parent still owns an unsettled batch or delivery.")
	}
	if len(c.preparations) >= 32 {
		a.mu.Unlock()
		return reply, delegationRejected("configuration_blocked", "Too many preparation attempts in this turn.")
	}
	job := &delegationPreparation{id: newID(), hash: hash, request: request, call: call, done: make(chan struct{})}
	c.preparing, c.preparations[job.id] = job, job
	c.wg.Add(1)
	a.wg.Add(1)
	go c.prepareJob(job)
	reply.Preparing, reply.PreparationID = true, job.id
	a.mu.Unlock()
	return reply, nil
}

func (c *delegationExecution) prepareJob(job *delegationPreparation) {
	defer c.wg.Done()
	defer c.parent.app.wg.Done()
	ctx, cancel := context.WithTimeout(c.ctx, 3*modelCatalogTimeout+30*time.Second)
	defer cancel()
	prepared, err := c.parent.prepareDelegationChildren(ctx, job.request)
	var receipt *DelegationAdmissionReceipt
	if err != nil {
		err = delegationRejected("configuration_blocked", err.Error())
	} else {
		receipt, err = c.commitPrepared(ctx, job, prepared)
	}
	a := c.parent.app
	a.mu.Lock()
	job.receipt, job.err = receipt, err
	if c.preparing == job {
		c.preparing = nil
	}
	close(job.done)
	c.notifyLocked()
	a.mu.Unlock()
}

func (c *delegationExecution) commitPrepared(ctx context.Context, job *delegationPreparation, prepared []preparedDelegationChild) (*DelegationAdmissionReceipt, error) {
	a := c.parent.app
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	for _, child := range prepared {
		if child.Resolution == nil || child.Resolution.SourceStatus != "linked" {
			continue
		}
		var err error
		if child.Child == nil {
			err = a.recheckNewAgentLocked(ctx, *child.Resolution, child.Session.ProjectID)
		} else {
			err = a.recheckBoundAgentLocked(ctx, *child.Resolution, child.Session.ProjectID)
		}
		if err != nil {
			return nil, delegationRejected("configuration_blocked", err.Error())
		}
	}
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !c.liveLocked() {
		return nil, errors.New("Parent stopped before delegation acceptance.")
	}
	var batch DelegationBatchRecord
	var members []DelegationInvocationRecord
	var rejected error
	err := a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		parent, err := st.spawnSource(c.parent.id)
		if c.graphScope() != nil {
			parent, err = st.sessionMeta(c.parent.id)
		}
		if err != nil {
			return err
		}
		if err := st.checkGraphDelegationAdmission(parent.ID, c.graphScope()); err != nil {
			return delegationRejected("batch_busy", err.Error())
		}
		active, err := st.activeDelegation(parent.ID)
		if err != nil {
			return err
		}
		if active != nil {
			rejected = delegationRejected("batch_busy", "Parent owns another accepted batch.")
			return rejected
		}
		batch = DelegationBatchRecord{Version: 1, ID: newID(), ParentSessionID: parent.ID, OwnerConversationID: parent.ID, ParentTurnID: c.parentTurnID, OperationID: job.request.OperationID, RequestHash: job.hash, Revision: "0", NativeCall: job.call, State: DelegationAdmitted, ExpectedChildren: len(prepared), CreatedAt: now(), UpdatedAt: now(), DeliveryTurnID: c.parentTurnID}
		if scope := c.graphScope(); scope != nil {
			batch.Graph = scope
			batch.OwnerConversationID = c.parent.turn.graphDelegation.OwnerID
		}
		members, err = st.admitPreparedDelegation(parent, batch, prepared, a.dir)
		return err
	})
	if err != nil {
		if rejected != nil {
			return nil, rejected
		}
		return nil, err
	}
	c.wg.Add(1)
	a.wg.Add(1)
	go c.runBatch(batch.ID, members)
	receipt := delegationReceipt(batch)
	return &receipt, nil
}

func (c *delegationExecution) prepared(ctx context.Context, id, callID, nativeTurnID string) (DelegationPrepareReply, error) {
	reply := DelegationPrepareReply{Version: 1, Preparing: true, PreparationID: id}
	a := c.parent.app
	a.mu.Lock()
	job := c.preparations[id]
	live := c.liveLocked()
	a.mu.Unlock()
	if !live || job == nil {
		return reply, errors.New("Preparation does not belong to the active parent.")
	}
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	select {
	case <-job.done:
	case <-timer.C:
		return reply, nil
	case <-ctx.Done():
		return reply, ctx.Err()
	case <-c.ctx.Done():
		return reply, c.ctx.Err()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if job.err != nil {
		return reply, job.err
	}
	if job.receipt == nil {
		return reply, errors.New("Preparation ended without an admission receipt.")
	}
	fastCtx, stopFast := context.WithTimeout(ctx, 2*time.Second)
	defer stopFast()
	call, err := c.nativeCallLocked(fastCtx, callID, nativeTurnID)
	if err != nil {
		return reply, err
	}
	if err := a.writeSQLLocked(fastCtx, func(st *sqliteSessionTx) error { return st.appendDelegationCall(job.receipt.BatchID, call) }); err != nil {
		return reply, err
	}
	reply.Preparing, reply.Receipt = false, job.receipt
	return reply, nil
}

func (c *delegationExecution) runBatch(batchID string, members []DelegationInvocationRecord) {
	defer c.wg.Done()
	defer c.parent.app.wg.Done()
	jobs := make(chan DelegationInvocationRecord, len(members))
	for _, item := range members {
		jobs <- item
	}
	close(jobs)
	var workers sync.WaitGroup
	for index := 0; index < min(delegationConcurrency, len(members)); index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				outcome := delegationChildOutcome{Outcome: DelegationCancelled, Error: "Parent stopped before child dispatch.", Finality: DelegationFinality{NativeSettled: true, ProcessesDrained: true, ToolCallsSettled: true}}
				if c.ctx.Err() == nil {
					if err := c.markDispatch(item.ID); err != nil {
						c.fail(err)
						outcome.Outcome, outcome.Error = DelegationFailed, err.Error()
					} else {
						outcome = c.runner(c.ctx, item)
					}
				}
				if err := c.finishItem(item.ID, outcome); err != nil {
					c.fail(err)
				}
				if !outcome.Finality.Confirmed() {
					c.fail(errDelegationParentStopRequired)
				}
			}
		}()
	}
	workers.Wait()
	a := c.parent.app
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.mu.Lock()
	err := a.persistSQLLocked(ctx, func(st *sqliteSessionTx) error { _, err := st.settleDelegation(batchID); return err })
	if err != nil && c.fatal == nil {
		c.fatal = err
	}
	c.notifyLocked()
	a.mu.Unlock()
	if err != nil {
		c.fail(err)
	}
}

func (c *delegationExecution) markDispatch(id string) error {
	a := c.parent.app
	a.mu.Lock()
	defer a.mu.Unlock()
	if !c.liveLocked() {
		return context.Canceled
	}
	return a.writeSQLLocked(c.ctx, func(st *sqliteSessionTx) error {
		item, err := st.delegationInvocation(id)
		if err != nil {
			return err
		}
		if item.State != DelegationInvocationAdmitted || item.DispatchAttempted {
			return errors.New("Child dispatch was already attempted.")
		}
		batch, err := st.delegationBatch(item.BatchID)
		if err != nil {
			return err
		}
		if batch.State == DelegationAdmitted {
			batch.State, batch.UpdatedAt = DelegationPreparing, now()
			if err := st.putDelegationBatch(&batch); err != nil {
				return err
			}
		}
		item.State, item.DispatchAttempted = DelegationInvocationPreparing, true
		return st.putDelegationInvocation(item)
	})
}

func (c *delegationExecution) finishItem(id string, outcome delegationChildOutcome) error {
	a := c.parent.app
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
		return st.finishDelegationItem(id, outcome.Outcome, outcome.Finality, outcome.Result, outcome.Error, outcome.EventEnd)
	})
	c.notifyLocked()
	return err
}

func (c *delegationExecution) fail(err error) {
	a := c.parent.app
	a.mu.Lock()
	if c.fatal == nil {
		c.fatal = err
	}
	c.notifyLocked()
	a.mu.Unlock()
	c.cancel()
	c.parent.turn.cancel()
}

func (c *delegationExecution) close() error {
	a := c.parent.app
	a.mu.Lock()
	c.closed = true
	c.cancel()
	a.mu.Unlock()
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		a.mu.Lock()
		err := c.fatal
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		premature := false
		cleanupErr := a.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
			return st.eachMatching("delegation_batches", `x_parentSessionId=? AND x_state='settled' AND (x_parentTurnId=? AND x_delivery IN ('pending','sending') OR EXISTS(SELECT 1 FROM data_delegation_calls c WHERE c.owner_id=data_delegation_batches.id AND c.x_parentTurnId=? AND c.x_delivery IN ('pending','sending')))`, []any{c.parent.id, c.parentTurnID, c.parentTurnID}, func(id string) error {
				batch, err := st.delegationBatch(id)
				if err != nil {
					return err
				}
				premature = true
				next := DelegationDeliveryCancelled
				if batch.Delivery == DelegationDeliverySending {
					next = DelegationDeliveryUncertain
				}
				if batch.Delivery == DelegationDeliveryPending || batch.Delivery == DelegationDeliverySending {
					batch.Delivery, batch.CancellationRequested, batch.UpdatedAt = next, true, now()
					if err := st.putDelegationBatch(&batch); err != nil {
						return err
					}
				}
				return st.eachMatching("delegation_calls", `owner_id=? AND x_parentTurnId=? AND x_delivery IN ('pending','sending')`, []any{id, c.parentTurnID}, func(key string) error {
					var call DelegationNativeCall
					if err := decodeSQLiteRecord(ctx, st.tx, "delegation_calls", key, 64<<10, &call); err != nil {
						return err
					}
					if call.Delivery == DelegationDeliverySending {
						call.Delivery = DelegationDeliveryUncertain
					} else {
						call.Delivery = DelegationDeliveryCancelled
					}
					return st.putExistingRecord("delegation_calls", key, call)
				})
			})
		})
		cancel()
		if premature && c.parent.turn.ctx.Err() == nil {
			err = errors.Join(err, errors.New("Parent ended before every native delegation result was confirmed."))
		}
		a.mu.Unlock()
		return errors.Join(err, cleanupErr)
	case <-timer.C:
		return errors.New("Owned child/preparation shutdown could not be confirmed.")
	}
}
