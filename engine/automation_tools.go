package main

import (
	"encoding/json"
	"errors"
)

func automationTools() []map[string]any {
	tools := []map[string]any{}
	for _, name := range []string{"automation_succeed", "automation_fail"} {
		description := "Report that this automation's objective is satisfied. Classification becomes final after the current turn settles."
		if name == "automation_fail" {
			description = "Report that this automation's objective was not satisfied. Classification becomes final after the current turn settles."
		}
		tools = append(tools, map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"reason": map[string]any{"type": "string", "maxLength": 1000}}, "required": []string{"reason"}, "additionalProperties": false}})
	}
	return tools
}

func (p *adapter) automationToolAvailable() bool {
	// Catalog construction can hold a runtime mutex. Never acquire app.mu here;
	// the immutable turn binding is revalidated under app.mu at every dispatch.
	return p.automationRunID != "" && !p.subagent && p.role == "" && !p.graphNode() && p.turn.ctx.Err() == nil
}

func (p *adapter) callAutomationTool(name string, raw json.RawMessage) (any, error) {
	var args struct {
		Reason string `json:"reason"`
	}
	if err := decodeGraphTool(raw, &args); err != nil {
		return nil, err
	}
	if !validLinkedText(args.Reason, 1000) {
		return nil, errors.New("Provide a short outcome reason.")
	}
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store != nil {
		return p.recordSQLAutomationOutcome(name, args.Reason)
	}
	r := a.state.activeAutomation(p.id)
	if r == nil || r.ID != p.automationRunID || p.subagent || p.role != "" || a.runs[p.id] != p.turn || p.turn.ctx.Err() != nil || r.StopRequested {
		return nil, errors.New("This turn does not own an active automation run.")
	}
	status := "succeeded"
	if name == "automation_fail" {
		status = "failed"
	}
	if r.Decision != "" && (r.Decision != status || r.Reason != args.Reason) {
		return nil, errors.New("An outcome is already recorded for this run.")
	}
	if err := a.commitLocked(func(d *diskState) { next := d.automationRun(r.ID); next.Decision, next.Reason = status, args.Reason }); err != nil {
		return nil, err
	}
	return map[string]any{"recorded": true, "status": status, "instruction": "Finish this turn. The engine will finalize the run after settlement."}, nil
}
