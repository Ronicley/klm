package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

func (p *adapter) initializeDelegation(native nativeSession) error {
	if p.app.store == nil || !delegationDomainsKnown() || p.role == "graph_node" && p.turn.graphDelegation == nil || p.role == sessionRoleSubagent || !delegationParentRole(p.role) {
		return nil
	}
	if !delegationAvailability(p.role, p.harness, native.ID, native.Delegation).Available {
		return nil
	}
	if err := p.app.store.readSnapshot(p.turn.ctx, func(tx *sql.Tx) error {
		batch, err := (&sqliteSessionTx{ctx: p.turn.ctx, tx: tx}).activeDelegation(p.id)
		if err != nil {
			return err
		}
		if batch != nil {
			return errors.New("Previous child execution or result delivery remains unconfirmed.")
		}
		return nil
	}); err != nil {
		return err
	}
	var controller *delegationExecution
	var err error
	controller, err = newDelegationExecution(p, func(ctx context.Context, item DelegationInvocationRecord) delegationChildOutcome {
		return p.app.executeDelegationChild(ctx, controller, item)
	})
	if err != nil {
		return err
	}
	p.delegation = controller
	p.app.mu.Lock()
	p.turn.delegation = controller
	p.app.mu.Unlock()
	return nil
}

func (p *adapter) writeDelegationAssets(dir string) (string, string, error) {
	if p.delegation == nil && p.turn.delegationChild == nil {
		return "", "", nil
	}
	files, err := delegationAssetFiles(DelegationAssetConfig{Enabled: true, ParentRole: p.role, BridgeURL: p.bridge.url, BridgeToken: p.bridge.token})
	if err != nil {
		return "", "", err
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			return "", "", err
		}
	}
	return filepath.Join(dir, "delegation-pi.mjs"), filepath.Join(dir, "delegation-opencode.mjs"), nil
}

func (p *adapter) delegationRPC(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	c := p.delegation
	if c == nil {
		return nil, errors.New("Delegation is unavailable to this native history/role.")
	}
	switch method {
	case "klm/delegation/ready":
		var args struct {
			Version    int    `json:"version"`
			SpecDigest string `json:"specDigest"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		p.app.mu.Lock()
		live := c.liveLocked()
		p.app.mu.Unlock()
		if !live {
			return nil, errors.New("Delegation owner is inactive.")
		}
		return delegationReadyReplyV1(args.Version, args.SpecDigest)
	case "klm/delegation/gate":
		var args struct {
			Tool string `json:"tool"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		if err := c.gate(ctx, args.Tool); err != nil {
			return nil, err
		}
		return map[string]any{"allowed": true}, nil
	case "klm/delegation/admit":
		var args DelegationAdmitRequest
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, delegationRejected("invalid_request", err.Error())
		}
		if _, err := delegationReadyReplyV1(args.Version, args.SpecDigest); err != nil {
			return nil, err
		}
		return c.prepare(args.Request, args.NativeCallID, "")
	case "klm/delegation/prepared":
		var args struct {
			PreparationID string `json:"preparationId"`
			NativeCallID  string `json:"nativeCallId"`
			WaitMS        int    `json:"waitMs"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		return c.prepared(ctx, args.PreparationID, args.NativeCallID, "")
	case "klm/delegation/wait":
		var args DelegationWaitRequest
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		return c.wait(ctx, args.BatchID, args.Revision, args.NativeCallID, "")
	case "klm/delegation/cancel":
		var args struct {
			BatchID string `json:"batchId"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		// Parent cancellation owns all its admitted siblings, never just one slot.
		var owned bool
		err := p.app.store.readSnapshot(ctx, func(tx *sql.Tx) error {
			batch, err := (&sqliteSessionTx{ctx: ctx, tx: tx}).delegationBatch(args.BatchID)
			owned = err == nil && batch.ParentSessionID == p.id
			return err
		})
		if err != nil || !owned {
			return nil, errors.New("Batch does not belong to this parent.")
		}
		c.cancel()
		return map[string]any{"cancelled": true}, nil
	}
	return nil, errors.New("Unknown delegation operation.")
}

func (c *delegationExecution) gate(ctx context.Context, tool string) error {
	if tool == "task" || tool == "subagent" {
		return errors.New("Native delegation is replaced by the owned runtime.")
	}
	a := c.parent.app
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for {
		a.mu.Lock()
		if !c.liveLocked() {
			a.mu.Unlock()
			return errors.New("Delegation owner stopped.")
		}
		if tool == delegationToolName {
			a.mu.Unlock()
			return nil
		}
		if c.preparing != nil {
			a.mu.Unlock()
			return errors.New("Parent is preparing its child batch.")
		}
		var batch *DelegationBatchRecord
		err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
			var err error
			batch, err = (&sqliteSessionTx{ctx: ctx, tx: tx}).activeDelegation(c.parent.id)
			return err
		})
		changed := c.changed
		a.mu.Unlock()
		if err != nil {
			return err
		}
		if batch == nil {
			return nil
		}
		// Native result events and the next boundary can arrive concurrently.
		// Wait only for that short acknowledgement, never for running children.
		if batch.State != DelegationSettled || batch.Outcome == DelegationFinalityUnconfirmed {
			return errors.New("Parent waits for every child and its native result delivery.")
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		case <-c.ctx.Done():
			return c.ctx.Err()
		case <-timer.C:
			return errors.New("Native delegation result acknowledgement timed out.")
		}
	}
}

func delegationRPCErrorData(method string, raw json.RawMessage, err error) any {
	var rejected *delegationAdmissionError
	if !errors.As(err, &rejected) {
		return nil
	}
	data := DelegationRejection{Accepted: false, Code: rejected.code}
	if method == "klm/delegation/prepared" {
		var args struct {
			PreparationID string `json:"preparationId"`
		}
		_ = json.Unmarshal(raw, &args)
		data.PreparationID, data.Phase = args.PreparationID, "pre_admission"
	}
	return data
}
