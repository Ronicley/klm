package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (st *sqliteSessionTx) cancelConsultations(id string) ([]string, error) {
	cancelled := []string{}
	err := st.eachMatching("consultations", `(x_from=? OR x_to=?) AND (status IN ('queued','answering') OR (x_from=? AND x_delivery IN ('pending','delivering')))`, []any{id, id, id}, func(key string) error {
		var c Consultation
		if err := decodeSQLiteRecord(st.ctx, st.tx, "consultations", key, 1<<20, &c); err != nil {
			return err
		}
		if !consultationTerminal(c.Status) {
			c.Status, c.Error = "cancelled", "Consultation cancelled."
			cancelled = append(cancelled, c.ID)
		}
		c.Delivery, c.UpdatedAt = "cancelled", now()
		if err := st.putRoot("consultations", key, c); err != nil {
			return err
		}
		return st.syncConsultationCards(c, c.UpdatedAt)
	})
	return cancelled, err
}

func (a *app) stopSQLSessionLocked(id string, origin *SpawnOrigin) (*turn, error) {
	var automation *AutomationRun
	err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: a.ctx, tx: tx}
		if _, err := st.sessionMeta(id); err != nil {
			return err
		}
		var err error
		automation, err = st.activeAutomation(id)
		return err
	})
	if err != nil {
		return nil, err
	}
	if automation != nil && !automation.StopRequested {
		return nil, controlError(409, "Confirm stopping the automation run in its conversation first.")
	}
	if a.stopVersions == nil {
		a.stopVersions = map[string]uint64{}
	}
	a.stopVersions[id]++
	t := a.runs[id]
	if t != nil {
		t.restartMessageID = ""
		t.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var consultations []string
	err = a.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
		if err := st.eachMatching("queued_messages", `owner_id=? AND status IN ('queued','steering','interrupting')`, []any{id}, func(key string) error {
			var q QueuedMessage
			if err := decodeSQLiteRecord(ctx, st.tx, "queued_messages", key, 1<<20, &q); err != nil {
				return err
			}
			q.Status, q.Error = "paused", "Execution stopped. Send queued messages when ready."
			return st.putExistingRecord("queued_messages", key, q)
		}); err != nil {
			return err
		}
		if origin != nil {
			e := event("status", "Session stop requested")
			e.Title, e.Status, e.Data = "Session stop requested", "completed", map[string]any{"origin": origin}
			if _, err := st.appendEvent(id, e); err != nil {
				return err
			}
		}
		var err error
		consultations, err = st.cancelConsultations(id)
		return err
	})
	if err != nil {
		return t, err
	}
	for _, request := range consultations {
		for _, recipient := range a.runs {
			if recipient.consultationID == request {
				recipient.cancel()
			}
		}
	}
	if t == nil && (a.runtimes[id] != nil || automation != nil) {
		turnCtx, stop := context.WithCancel(a.ctx)
		stop()
		t = &turn{ctx: turnCtx, cancel: stop, done: make(chan struct{})}
		if a.runs == nil {
			a.runs = map[string]*turn{}
		}
		a.runs[id] = t
		runtime := a.runtimes[id]
		a.wg.Add(1)
		go func(t *turn) {
			defer a.wg.Done()
			if runtime != nil {
				t.stopErr = runtime.shutdown()
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			if t.stopErr == nil && automation != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = a.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
					r, err := st.activeAutomation(id)
					if err != nil {
						return err
					}
					if r == nil || r.ID != automation.ID {
						return nil
					}
					return st.finishAutomation(r, "interrupted_by_user", "Stopped by user.")
				})
			}
			delete(a.runs, id)
			close(t.done)
			a.scheduleLinkedLocked()
		}(t)
	}
	return t, nil
}

func (a *app) confirmSQLAutomationStopLocked(ctx context.Context, id, runID string) error {
	return a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		r, err := st.activeAutomation(id)
		if err != nil {
			return err
		}
		if r == nil {
			if runID != "" {
				return controlError(409, "This automation run has already ended. Refresh before stopping it.")
			}
			return nil
		}
		if runID != r.ID {
			return controlError(409, "Confirm stopping this automation run first.")
		}
		r.StopRequested = true
		return st.putRoot("automation_runs", r.ID, *r)
	})
}

func (st *sqliteSessionTx) syncSubagentParent(child Session, status, parentEventID string) error {
	if child.Role != sessionRoleSubagent {
		return nil
	}
	where := `owner_id=? AND kind='subagent' AND x_data_childSessionId=?`
	args := []any{child.ParentID, child.ID}
	if parentEventID != "" {
		where += ` AND record_id=?`
		args = append(args, parentEventID)
	} else {
		where += ` AND status IN ('running','pending','started')`
	}
	return st.eachMatching("session_events", where, args, func(key string) error {
		if err := st.patchEventMetadata(child.ParentID, key, func(e *Event) error { e.Status = status; return nil }); err != nil {
			return err
		}
		parent, err := st.sessionMeta(child.ParentID)
		if err != nil {
			return err
		}
		parent.UpdatedAt = now()
		return st.putSessionMeta(parent)
	})
}

func (p *adapter) ensureSQLSubagentLocked(nativeID, title, model, effort string) (*Session, error) {
	var child Session
	allowed := false
	err := p.app.writeSQLLocked(p.turn.ctx, func(st *sqliteSessionTx) error {
		parent, err := st.sessionMeta(p.id)
		if err != nil {
			return err
		}
		if parent.ParentID != "" && parent.Role != sessionRoleSideAgent || parent.Role == sessionRoleSubagent || parent.GraphRunID != "" {
			return nil
		}
		allowed = true
		var id string
		err = st.tx.QueryRowContext(st.ctx, `SELECT s.id FROM data_sessions s JOIN data_native_sessions n ON n.map_key=s.id WHERE s.parent_id=? AND s.role='subagent' AND n.record_id=? LIMIT 1`, p.id, nativeID).Scan(&id)
		if err == nil {
			child, err = st.sessionMeta(id)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
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
		child = Session{ID: newID(), ParentID: p.id, ProjectID: parent.ProjectID, Title: subagentTitle(title), Workspace: parent.Workspace, Role: sessionRoleSubagent, Harness: parent.Harness, Model: model, Effort: effort, Status: "running", CreatedAt: now(), UpdatedAt: now()}
		if err := st.createSession(child, p.app.dir); err != nil {
			return err
		}
		return st.putRoot("native_sessions", child.ID, nativeSession{ID: nativeID})
	})
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, nil
	}
	return &child, nil
}
