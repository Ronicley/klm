package main

import (
	"context"
	"database/sql"
)

// Codex has no owned pre-tool gate. A turn started with native full access
// cannot safely be downgraded without ending that turn. Capture its real native
// policy at dispatch, not the earlier queue/session snapshot.
func (p *adapter) captureNativeYOLO() bool {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	value := p.yolo
	if p.app.store != nil {
		err := p.app.store.readSnapshot(p.turn.ctx, func(tx *sql.Tx) error {
			st := &sqliteSessionTx{ctx: p.turn.ctx, tx: tx}
			s, err := st.sessionMeta(p.id)
			if err != nil {
				return err
			}
			value, err = st.sessionYOLO(s)
			return err
		})
		if err != nil {
			p.app.failStorageLocked(err)
			p.turn.cancel()
			return false
		}
		p.turn.nativeYOLO, p.turn.nativeYOLOConfigured = value, true
		return value
	}
	if s := p.app.state.session(p.id); s != nil {
		value = p.app.state.sessionYOLO(s)
	}
	p.turn.nativeYOLO, p.turn.nativeYOLOConfigured = value, true
	return value
}

// Spawn receipts preserve independent conversations' ancestry without changing
// their visual grouping or permission-grant ownership.
func (d *diskState) yoloDescendants(id string) map[string]bool {
	// ponytail: fixed-point scan; index ancestry if inventories become large.
	ids := map[string]bool{id: true}
	for changed := true; changed; {
		changed = false
		for _, s := range d.Sessions {
			owner := s.ParentID
			if run := d.graphRun(s.GraphRunID); run != nil {
				owner = run.ConversationID
			}
			if !ids[s.ID] && ids[owner] {
				ids[s.ID], changed = true, true
			}
		}
		for _, receipt := range d.SessionSpawns {
			if !ids[receipt.SessionID] && ids[receipt.From] {
				ids[receipt.SessionID], changed = true, true
			}
		}
	}
	return ids
}

func (a *app) validateYOLOChangeLocked(ids map[string]bool, enabled bool) error {
	for id := range ids {
		s := a.state.session(id)
		if s == nil {
			continue
		}
		if t := a.runs[id]; t != nil {
			if t.ctx.Err() != nil {
				return controlError(409, "A descendant execution is still stopping.")
			}
			if !enabled && s.Harness == "codex" && t.nativeYOLOConfigured && t.nativeYOLO {
				return controlError(409, "Codex started a conversation or descendant turn with a full-access sandbox. Finish or stop that turn before disabling YOLO.")
			}
		}
	}
	return nil
}

func (d *diskState) setYOLO(ids map[string]bool, enabled bool) {
	for id := range ids {
		if s := d.session(id); s != nil && s.YOLO != enabled {
			s.YOLO, s.UpdatedAt = enabled, now()
		}
	}
}

func (a *app) changeSessionYOLO(ctx context.Context, id string, enabled bool) error {
	if a.store != nil {
		a.mu.Lock()
		err := a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
			s, err := st.sessionMeta(id)
			if err != nil {
				return err
			}
			if s.GraphRunID != "" || s.Role == sessionRoleSubagent {
				return controlError(404, "Conversation not found.")
			}
			if run, err := st.activeAutomation(id); err != nil {
				return err
			} else if run != nil {
				return controlError(409, "Automation run settings are read-only until the run ends.")
			}
			if _, err := st.conversationDirectory(s); err != nil {
				return err
			}
			if t := a.runs[id]; t != nil {
				if t.ctx.Err() != nil {
					return controlError(409, "Execution is still stopping.")
				}
				if !enabled && s.Harness == "codex" && t.nativeYOLOConfigured && t.nativeYOLO {
					return controlError(409, "Codex started this turn with a full-access sandbox. Finish or stop that turn before disabling YOLO.")
				}
			}
			if !enabled {
				for _, child := range a.runs {
					binding := child.delegationChild
					if binding != nil && binding.owner.parent.id == id && child.nativeYOLOConfigured && child.nativeYOLO {
						return controlError(409, "A Codex child started with full access. Finish or stop the owning turn before disabling YOLO.")
					}
				}
			}
			if s.YOLO == enabled {
				return nil
			}
			s.YOLO, s.UpdatedAt = enabled, now()
			return st.putSessionMeta(s)
		})
		a.mu.Unlock()
		if err == nil && enabled {
			a.resolveRememberedPermissions()
		}
		return err
	}
	a.mu.Lock()
	s := a.state.session(id)
	if a.state.activeAutomation(id) != nil {
		a.mu.Unlock()
		return controlError(409, "Automation run settings are read-only until the run ends.")
	}
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
	ids := a.state.yoloDescendants(id)
	if err := a.validateYOLOChangeLocked(ids, enabled); err != nil {
		a.mu.Unlock()
		return err
	}
	if err := a.commitLocked(func(d *diskState) { d.setYOLO(ids, enabled) }); err != nil {
		a.mu.Unlock()
		return err
	}
	a.mu.Unlock()
	if enabled {
		a.resolveRememberedPermissions()
	}
	return nil
}
