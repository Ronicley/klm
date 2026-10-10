package main

import (
	"errors"
	"strings"
)

const (
	sessionRoleSideAgent = "side_agent"
	sessionRoleSubagent  = "subagent"
)

func subagentTitle(value string) string {
	if title, ok := cleanLabel(value, 200); ok {
		return title
	}
	return "Subagent"
}

// Subagents are persisted as hidden child sessions so their native event stream
// can use the same history, pagination, and rendering contract as a normal chat.
func (p *adapter) ensureSubagent(nativeID, title, model, effort string) (*adapter, error) {
	if nativeID == "" || len(nativeID) > 256 || strings.IndexFunc(nativeID, func(r rune) bool { return r <= ' ' || r == 127 }) != -1 {
		return nil, errors.New("Harness returned an invalid subagent identifier.")
	}
	if child := p.subagents[nativeID]; child != nil {
		return child, nil
	}
	p.app.mu.Lock()
	parent := p.app.state.session(p.id)
	if parent == nil || parent.ParentID != "" || parent.GraphRunID != "" {
		p.app.mu.Unlock()
		return nil, nil
	}
	if model == "" {
		model = parent.ResolvedModel
		if model == "" {
			model = parent.Model
		}
	}
	if effort == "" {
		effort = parent.ResolvedEffort
		if effort == "" {
			effort = parent.Effort
		}
	}
	var childSession *Session
	for i := range p.app.state.Sessions {
		candidate := &p.app.state.Sessions[i]
		if candidate.ParentID == parent.ID && candidate.Role == sessionRoleSubagent && p.app.state.Native[candidate.ID].ID == nativeID {
			childSession = candidate
			break
		}
	}
	if childSession == nil {
		created := now()
		child := Session{ID: newID(), ParentID: parent.ID, ProjectID: parent.ProjectID, Title: subagentTitle(title), Workspace: parent.Workspace,
			Role: sessionRoleSubagent, Harness: parent.Harness, Model: model, Effort: effort, Status: "running", Events: []Event{}, CreatedAt: created, UpdatedAt: created}
		if err := p.app.commitLocked(func(d *diskState) {
			d.Sessions = append(d.Sessions, child)
			d.Native[child.ID] = nativeSession{ID: nativeID}
		}); err != nil {
			p.app.mu.Unlock()
			return nil, err
		}
		childSession = p.app.state.session(child.ID)
	}
	childID := childSession.ID
	p.app.mu.Unlock()
	child := &adapter{app: p.app, turn: p.turn, id: childID, harness: p.harness, role: sessionRoleSubagent, subagent: true, yolo: p.yolo, cwd: p.cwd,
		keys: map[string]string{}, toolNames: map[string]string{}, commands: map[string]string{}, processesDrained: true, model: model, effort: effort}
	child.stream = newStreamBatch(child)
	if p.subagents == nil {
		p.subagents = map[string]*adapter{}
	}
	p.subagents[nativeID] = child
	return child, nil
}

func (p *adapter) startSubagent(child *adapter) error {
	if child == nil {
		return nil
	}
	child.app.mu.Lock()
	defer child.app.mu.Unlock()
	if s := child.app.state.session(child.id); s == nil || s.Status == "running" {
		return nil
	}
	return child.app.commitLocked(func(d *diskState) {
		if s := d.session(child.id); s != nil {
			s.Status, s.UpdatedAt = "running", now()
		}
	})
}

func (p *adapter) finishSubagent(child *adapter, status string) error {
	return p.finishSubagentInvocation(child, status, "")
}

func (p *adapter) finishSubagentInvocation(child *adapter, status, parentEventID string) error {
	if child == nil {
		return nil
	}
	// Completion must follow queued text, otherwise a delayed flush can put
	// running events back into an already completed child session.
	if child.stream != nil {
		if err := child.stream.flush(nil); err != nil {
			return err
		}
	}
	sessionStatus := "idle"
	if status == "error" || status == "failed" || status == "interrupted" {
		sessionStatus = "error"
	}
	child.app.mu.Lock()
	defer child.app.mu.Unlock()
	return child.app.commitLocked(func(d *diskState) {
		s := d.session(child.id)
		if s == nil {
			return
		}
		if s.Status == "error" && sessionStatus == "idle" {
			syncSubagentParentInvocation(d, s, "error", parentEventID)
			return
		}
		s.Status, s.UpdatedAt = sessionStatus, now()
		for i := range s.Events {
			if s.Events[i].Status == "running" || s.Events[i].Status == "pending" {
				e := s.Events[i]
				e.Status = status
				d.setEvent(s, i, e)
			}
		}
		syncSubagentParentInvocation(d, s, status, parentEventID)
	})
}

func syncSubagentParent(d *diskState, child *Session, status string) {
	syncSubagentParentInvocation(d, child, status, "")
}

func syncSubagentParentInvocation(d *diskState, child *Session, status, parentEventID string) {
	if child == nil || child.Role != sessionRoleSubagent {
		return
	}
	parent := d.session(child.ParentID)
	if parent == nil {
		return
	}
	for i := range parent.Events {
		value, _ := parent.Events[i].Data["childSessionId"].(string)
		active := parent.Events[i].Status == "running" || parent.Events[i].Status == "pending" || parent.Events[i].Status == "started"
		matchesInvocation := parentEventID != "" && parent.Events[i].ID == parentEventID || parentEventID == "" && active
		if parent.Events[i].Type == "subagent" && value == child.ID && matchesInvocation {
			e := parent.Events[i]
			e.Status = status
			d.setEvent(parent, i, e)
			parent.UpdatedAt = now()
		}
	}
}

func (p *adapter) closeSubagents(failed, cancelled bool) error {
	var result error
	for _, child := range p.subagents {
		result = errors.Join(result, child.stream.close())
		status := "completed"
		if cancelled {
			status = "cancelled"
		} else if failed {
			status = "error"
		}
		result = errors.Join(result, p.finishSubagent(child, status))
	}
	return result
}
