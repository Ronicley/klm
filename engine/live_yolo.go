package main

import "context"

// Codex has no owned pre-tool gate. A turn started with native full access
// cannot safely be downgraded without ending that turn. Capture its real native
// policy at dispatch, not the earlier queue/session snapshot.
func (p *adapter) captureNativeYOLO() bool {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	value := p.yolo
	if s := p.app.state.session(p.id); s != nil {
		value = s.YOLO
	}
	p.turn.nativeYOLO, p.turn.nativeYOLOConfigured = value, true
	return value
}

func (a *app) changeSessionYOLO(ctx context.Context, id string, enabled bool) error {
	a.mu.Lock()
	s := a.state.session(id)
	if err := ctx.Err(); err != nil {
		a.mu.Unlock()
		return err
	}
	if a.closing || a.storageErr != nil {
		a.mu.Unlock()
		return controlError(503, "Engine is unavailable.")
	}
	if s == nil || s.GraphRunID != "" || s.Role == sessionRoleSubagent {
		a.mu.Unlock()
		return controlError(404, "Conversation not found.")
	}
	if _, available := a.state.conversationDirectory(s); !available {
		a.mu.Unlock()
		return controlError(404, "Conversation directory is unavailable.")
	}
	if t := a.runs[id]; t != nil {
		if t.ctx.Err() != nil {
			a.mu.Unlock()
			return controlError(409, "Execution is still stopping.")
		}
		if !enabled && s.Harness == "codex" && t.nativeYOLOConfigured && t.nativeYOLO {
			a.mu.Unlock()
			return controlError(409, "Codex started this turn with a full-access sandbox. Finish or stop that turn before disabling YOLO; its native sandbox cannot be downgraded mid-turn.")
		}
	}
	if s.YOLO != enabled {
		if err := a.commitLocked(func(d *diskState) { s := d.session(id); s.YOLO, s.UpdatedAt = enabled, now() }); err != nil {
			a.mu.Unlock()
			return err
		}
	}
	a.mu.Unlock()
	if enabled {
		a.resolveRememberedPermissions()
	}
	return nil
}
