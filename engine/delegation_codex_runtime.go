package main

import (
	"encoding/json"
	"errors"
	"sync/atomic"
)

// Keep the sole native reader running while discovery, children and join wait.
func (c *codexInteractive) delegationRequest(frame map[string]any, sourceID string) error {
	p := c.p
	if p.delegation == nil || p.pendingCodexDelegation == nil {
		return c.send(map[string]any{"id": frame["id"], "error": map[string]any{"code": -32601, "message": "Delegation is unavailable in this native history."}})
	}
	if c.requests[sourceID] != nil {
		return errors.New("Duplicate Codex delegation request.")
	}
	raw, err := json.Marshal(frame["params"])
	if err != nil {
		return err
	}
	call, request, err := decodeDelegationCodexCall(raw, c.threadID, c.turnID, *p.pendingCodexDelegation)
	if err != nil {
		return c.send(map[string]any{"id": frame["id"], "result": map[string]any{"success": false, "contentItems": []any{map[string]any{"type": "inputText", "text": err.Error()}}}})
	}
	live := &atomic.Bool{}
	live.Store(true)
	c.requests[sourceID] = live
	owner := p.delegation
	p.app.mu.Lock()
	if !owner.liveLocked() {
		p.app.mu.Unlock()
		return errors.New("Delegation owner stopped.")
	}
	owner.wg.Add(1)
	p.app.mu.Unlock()
	go func() {
		defer owner.wg.Done()
		prepared, err := owner.prepare(request, call.CallID, call.TurnID)
		for err == nil && prepared.Preparing {
			prepared, err = owner.prepared(owner.ctx, prepared.PreparationID, call.CallID, call.TurnID)
		}
		if err != nil {
			var rejected *delegationAdmissionError
			if errors.As(err, &rejected) {
				err = c.send(map[string]any{"id": frame["id"], "result": map[string]any{"success": false, "contentItems": []any{map[string]any{"type": "inputText", "text": rejected.Error()}}}})
				if err == nil {
					live.Store(false)
					return
				}
			}
			owner.fail(err)
			return
		}
		if prepared.Receipt == nil {
			owner.fail(errors.New("Missing delegation receipt."))
			return
		}
		revision := ""
		for {
			reply, err := owner.wait(owner.ctx, prepared.Receipt.BatchID, revision, call.CallID, call.TurnID)
			if err != nil {
				owner.fail(err)
				return
			}
			revision = reply.Revision
			if reply.State == "waiting" {
				continue
			}
			if reply.StopRequired || reply.Result == nil {
				owner.fail(errDelegationParentStopRequired)
				return
			}
			result, err := delegationCodexResponse(*reply.Result)
			if err == nil {
				err = c.send(map[string]any{"id": frame["id"], "result": result})
			}
			if err != nil {
				owner.fail(err)
				return
			}
			live.Store(false)
			return
		}
	}()
	return nil
}
