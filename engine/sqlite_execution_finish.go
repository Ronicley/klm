package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (st *sqliteSessionTx) finishAutomation(r *AutomationRun, status, reason string) error {
	if r == nil || r.Status != "running" {
		return nil
	}
	r.Status, r.Reason, r.FinishedAt, r.NeedsContinuation = status, reason, now(), false
	if err := st.putRoot("automation_runs", r.ID, *r); err != nil {
		return err
	}
	s, err := st.sessionMeta(r.SessionID)
	if err != nil {
		return err
	}
	if s.Automation != nil && s.Automation.RunID == r.ID {
		s.Status = "idle"
		if status == "execution_failed" {
			s.Status = "error"
		}
		s.Automation.Status = status
		if _, err := st.recoverQueue(s.ID, "Automation finished. Send this message when ready."); err != nil {
			return err
		}
		e := event("status", "Automation: "+status+". "+reason)
		e.Data = map[string]any{"automationId": r.AutomationID, "runId": r.ID}
		if _, err := st.appendEvent(s.ID, e); err != nil {
			return err
		}
		s.UpdatedAt = now()
		return st.putSessionMeta(s)
	}
	return nil
}

func (p *adapter) settleSQLAutomation(st *sqliteSessionTx, runErr error) error {
	r, err := st.activeAutomation(p.id)
	if err != nil {
		return err
	}
	if r == nil || r.ID != p.automationRunID {
		return nil
	}
	switch {
	case p.turn.stopErr != nil || !p.processesDrained:
		r.Reason = "Process shutdown could not be confirmed. Stop this conversation again to recover."
		return st.putRoot("automation_runs", r.ID, *r)
	case r.StopRequested:
		return st.finishAutomation(r, "interrupted_by_user", "Stopped by user.")
	case runErr != nil || p.failed || !p.completed || p.turn.ctx.Err() != nil:
		if p.app.closing {
			recovery := AutomationRecovery{ID: newID(), RunID: r.ID, Snapshot: copyAutomation(r.Snapshot), Capabilities: r.Capabilities, Variables: r.Trigger.Variables, Source: "interrupted", From: r.StartedAt, Until: now(), SessionID: r.SessionID, Delivery: "pending"}
			if err := st.putRoot("automation_recoveries", recovery.ID, recovery); err != nil {
				return err
			}
		}
		reason := "Harness execution failed."
		if runErr != nil {
			reason = runErr.Error()
		}
		return st.finishAutomation(r, "execution_failed", reason)
	case r.Decision != "":
		return st.finishAutomation(r, r.Decision, r.Reason)
	default:
		r.NeedsContinuation = true
		if err := st.putRoot("automation_runs", r.ID, *r); err != nil {
			return err
		}
		s, err := st.sessionMeta(p.id)
		if err != nil {
			return err
		}
		s.Status = "running"
		return st.putSessionMeta(s)
	}
}

func (p *adapter) finishSQLTurnLocked(runErr error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t := p.turn
	err := p.app.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
		if err := st.finishAnalytics(t, !p.processesDrained || t.stopErr != nil, nil); err != nil {
			return err
		}
		s, err := st.sessionMeta(p.id)
		if err != nil {
			return err
		}
		s.Status, s.UpdatedAt = "idle", now()
		if err := st.putSingleChild("capability_preparing", sqliteOwnedKey(p.id, ""), nil); err != nil {
			return err
		}
		for _, table := range []string{"pending_permissions", "pending_questions"} {
			if err := st.clearChildren(table, p.id); err != nil {
				return err
			}
		}
		if t.ctx.Err() != nil || runErr != nil || p.failed || !p.completed {
			if _, err := st.recoverQueue(p.id, "Execution stopped. Send queued messages when ready."); err != nil {
				return err
			}
		} else {
			if err := st.eachMatching("queued_messages", `owner_id=? AND status IN ('sending','accepted')`, []any{p.id}, func(id string) error {
				var q QueuedMessage
				if err := decodeSQLiteRecord(ctx, st.tx, "queued_messages", id, 1<<20, &q); err != nil {
					return err
				}
				q.Status, q.Error = "uncertain", "Delivery was not confirmed. Check the conversation before sending again."
				return st.putExistingRecord("queued_messages", id, q)
			}); err != nil {
				return err
			}
		}
		if t.restartMessageID != "" && t.stopErr == nil && !p.app.closing && t.ctx.Err() != nil {
			key, err := st.queueKey(p.id, t.restartMessageID)
			if err == nil {
				var q QueuedMessage
				if err := decodeSQLiteRecord(ctx, st.tx, "queued_messages", key, 1<<20, &q); err != nil {
					return err
				}
				q.Status, q.Error = "queued", ""
				if err := st.putExistingRecord("queued_messages", key, q); err != nil {
					return err
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var last Event
		switch {
		case t.stopErr != nil:
			s.Status = "error"
			last = event("error", "Could not confirm all session processes stopped: "+t.stopErr.Error())
		case t.ctx.Err() != nil:
			last = event("status", "Execution stopped.")
			last.Status = "cancelled"
		case runErr != nil:
			s.Status = "error"
			last = event("error", runErr.Error())
		case p.failed:
			s.Status = "error"
		case !p.completed:
			s.Status = "error"
			last = event("error", "Harness exited without reporting a completed turn.")
		}
		if last.ID != "" {
			last.ConsultationID = t.consultationID
			if _, err := st.appendEvent(p.id, last); err != nil {
				return err
			}
		}
		pendingWhere := `owner_id=? AND status IN ('running','pending')`
		if p.eventMetadataErr != nil {
			pendingWhere += ` AND payload_bytes<=65536`
		}
		if err := st.eachMatching("session_events", pendingWhere, []any{p.id}, func(key string) error {
			return st.patchEventMetadata(p.id, key, func(e *Event) error {
				e.Status = "cancelled"
				if s.Status == "error" {
					e.Status = "error"
				}
				return nil
			})
		}); err != nil {
			return err
		}
		if err := st.putSessionMeta(s); err != nil {
			return err
		}
		if _, err := st.tx.ExecContext(ctx, `UPDATE runtime_activity SET reasoning_title='' WHERE session_id=?`, p.id); err != nil {
			return err
		}
		if t.consultationID != "" {
			var c Consultation
			if err := decodeSQLiteRecord(ctx, st.tx, "consultations", t.consultationID, 1<<20, &c); err != nil {
				return err
			}
			if t.delivery {
				if c.Delivery != "cancelled" {
					c.Delivery = "delivered"
					if t.ctx.Err() != nil || s.Status == "error" {
						c.Delivery = "failed"
					}
				}
			} else if !consultationTerminal(c.Status) {
				c.Status, c.Error = "failed", "Agent finished without returning a consultation answer."
				if runErr != nil {
					c.Error = runErr.Error()
				}
				if t.ctx.Err() != nil {
					c.Status, c.Error = "cancelled", "Consultation stopped."
				}
			}
			c.UpdatedAt = now()
			if err := st.putRoot("consultations", c.ID, c); err != nil {
				return err
			}
			if err := st.syncConsultationCards(c, c.UpdatedAt); err != nil {
				return err
			}
		}
		if t.ctx.Err() != nil || s.Status == "error" {
			if err := st.eachMatching("consultations", `x_from=? AND x_delivery IN ('waiting','pending')`, []any{p.id}, func(id string) error {
				var c Consultation
				if err := decodeSQLiteRecord(ctx, st.tx, "consultations", id, 1<<20, &c); err != nil {
					return err
				}
				c.Delivery = "cancelled"
				if !consultationTerminal(c.Status) {
					c.Status, c.Error = "cancelled", "Requesting turn stopped."
				}
				c.UpdatedAt = now()
				if err := st.putRoot("consultations", id, c); err != nil {
					return err
				}
				return st.syncConsultationCards(c, c.UpdatedAt)
			}); err != nil {
				return err
			}
		}
		if err := p.settleSQLAutomation(st, runErr); err != nil {
			return err
		}
		for _, id := range t.automationRecoveryIDs {
			var r AutomationRecovery
			err := decodeSQLiteRecord(ctx, st.tx, "automation_recoveries", id, 32<<20, &r)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			r.Delivery = "delivered"
			if runErr != nil || p.failed || !p.completed || t.ctx.Err() != nil {
				r.Delivery = "uncertain"
			}
			if err := st.putRoot("automation_recoveries", id, r); err != nil {
				return err
			}
		}
		return st.pruneMCPCredentials()
	})
	if err != nil {
		return p.app.failStorageLocked(err)
	}
	return nil
}
