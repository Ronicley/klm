package main

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"time"
)

func (st *sqliteSessionTx) putExistingRecord(table, id string, value any) error {
	var owner, key string
	var position int64
	if err := st.tx.QueryRowContext(st.ctx, `SELECT owner_id,map_key,position FROM data_`+table+` WHERE id=?`, id).Scan(&owner, &key, &position); err != nil {
		return err
	}
	return st.putRecord(table, id, owner, key, position, value)
}

func (st *sqliteSessionTx) clearChildren(table, owner string) error {
	spec, ok := sqliteDomainByTable()[table]
	if !ok || spec.parentTable == "" {
		return errors.New("invalid child collection")
	}
	for {
		ids, err := sqliteQueryIDs(st.ctx, st.tx, `SELECT id FROM data_`+table+` WHERE owner_id=? LIMIT 100`, owner)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := st.deleteRecord(table, id); err != nil {
				return err
			}
		}
	}
	_, err := st.tx.ExecContext(st.ctx, `UPDATE record_collections SET kind='null',size=0 WHERE parent_table=? AND parent_id=? AND field=?`, spec.parentTable, owner, spec.field)
	return err
}

func (st *sqliteSessionTx) recoverQueue(sessionID string, reasons ...string) (bool, error) {
	changed := false
	reason := "Engine restarted. Send queued messages when ready."
	if len(reasons) > 0 {
		reason = reasons[0]
	}
	for {
		ids, err := sqliteQueryIDs(st.ctx, st.tx, `SELECT id FROM data_queued_messages WHERE owner_id=? AND status IN ('sending','accepted','queued','steering','interrupting') ORDER BY position LIMIT 100`, sessionID)
		if err != nil {
			return false, err
		}
		if len(ids) == 0 {
			return changed, nil
		}
		for _, id := range ids {
			var q QueuedMessage
			if err := decodeSQLiteRecord(st.ctx, st.tx, "queued_messages", id, 1<<20, &q); err != nil {
				return false, err
			}
			s := Session{Queue: []QueuedMessage{q}}
			pauseMessageQueue(&s, reason)
			if err := st.putExistingRecord("queued_messages", id, s.Queue[0]); err != nil {
				return false, err
			}
			changed = true
		}
	}
}

func (st *sqliteSessionTx) syncConsultationCards(c Consultation, stamp string) error {
	from, err := st.sessionMeta(c.From)
	if err != nil {
		return err
	}
	to, err := st.sessionMeta(c.To)
	if err != nil {
		return err
	}
	name := func(a, b Session) string {
		if a.ParentID == b.ID || b.ParentID == a.ID {
			return "the " + agentName(&b)
		}
		return b.Title + " (" + b.ID + ")"
	}
	for _, s := range []Session{from, to} {
		title := "Asking " + name(from, to) + " about " + c.Topic
		if s.ID == c.To {
			title = "Question from " + name(to, from)
			if c.Status == "answering" {
				title = "Answering a question from " + name(to, from)
			}
			if c.Status == "completed" {
				title = "Answered a question from " + name(to, from)
			}
		}
		e := Event{ID: "consultation/" + c.ID, Type: "consultation", Title: title, Text: c.Question, Status: c.Status, CreatedAt: c.CreatedAt, Data: map[string]any{"requestId": c.ID, "answer": c.Answer, "error": c.Error, "from": c.From, "to": c.To, "delivery": c.Delivery}}
		var ordinal int64
		err := st.tx.QueryRowContext(st.ctx, `SELECT position FROM data_session_events WHERE owner_id=? AND record_id=? ORDER BY position LIMIT 1`, s.ID, e.ID).Scan(&ordinal)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = st.appendEvent(s.ID, e)
		} else if err == nil {
			err = st.writeEvent(s.ID, ordinal, e, false)
		}
		if err != nil {
			return err
		}
		s.UpdatedAt = stamp
		if err := st.putSessionMeta(s); err != nil {
			return err
		}
	}
	return nil
}

// Consultation interruption and both visible cards share one transaction. The
// small durable correlation table survives a crash before chat recovery runs.
func (s *sqliteStore) recoverConsultationBatch(ctx context.Context, at time.Time) (int, sqliteSessionChanges, error) {
	count := 0
	stamp := at.UTC().Format(time.RFC3339Nano)
	changes, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		ids, err := sqliteQueryIDs(ctx, st.tx, `SELECT id FROM data_consultations WHERE status IN ('queued','answering') OR x_delivery IN ('pending','delivering','waiting') ORDER BY position LIMIT 100`)
		if err != nil {
			return err
		}
		for _, id := range ids {
			var c Consultation
			if err := decodeSQLiteRecord(ctx, st.tx, "consultations", id, 1<<20, &c); err != nil {
				return err
			}
			correlate := func(session string) error {
				_, err := st.tx.ExecContext(ctx, `INSERT INTO recovery_consultations(session_id,consultation_id) VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET consultation_id=excluded.consultation_id`, session, c.ID)
				return err
			}
			if c.Status == "answering" {
				if err := correlate(c.To); err != nil {
					return err
				}
			}
			if c.Delivery == "delivering" {
				if err := correlate(c.From); err != nil {
					return err
				}
			}
			if !consultationTerminal(c.Status) {
				c.Status, c.Error = "interrupted", "Consultation interrupted by engine restart."
			}
			c.Delivery, c.UpdatedAt = "interrupted", stamp
			if err := st.putRoot("consultations", c.ID, c); err != nil {
				return err
			}
			if err := st.syncConsultationCards(c, stamp); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, sqliteSessionChanges{}, err
	}
	return count, changes, nil
}

func (st *sqliteSessionTx) interruptSubagentParent(child Session, stamp string) error {
	if child.Role != sessionRoleSubagent {
		return nil
	}
	for {
		rows, err := st.tx.QueryContext(st.ctx, `SELECT id,position FROM data_session_events WHERE owner_id=? AND kind='subagent' AND x_data_childSessionId=? AND status IN ('running','pending','started') ORDER BY position LIMIT 100`, child.ParentID, child.ID)
		if err != nil {
			return err
		}
		type ref struct {
			id       string
			position int64
		}
		refs := []ref{}
		for rows.Next() {
			var r ref
			if err := rows.Scan(&r.id, &r.position); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, r)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(refs) == 0 {
			return nil
		}
		for _, r := range refs {
			var e Event
			if err := decodeSQLiteRecord(st.ctx, st.tx, "session_events", r.id, protocolChunkSize, &e); err != nil {
				return err
			}
			e.Status = "interrupted"
			if err := st.writeEvent(child.ParentID, r.position, e, false); err != nil {
				return err
			}
		}
		parent, err := st.sessionMeta(child.ParentID)
		if err != nil {
			return err
		}
		parent.UpdatedAt = stamp
		if err := st.putSessionMeta(parent); err != nil {
			return err
		}
	}
}

// Run after graph/automation/accounting recovery and consultation interruption,
// before readiness. Each session settles atomically; terminal states remove it
// from the indexed eligibility query, making restart retries idempotent.
func (s *sqliteStore) recoverSessionBatch(ctx context.Context, at time.Time) (int, sqliteSessionChanges, error) {
	// Startup owns the data directory and has not admitted workers. Prepare large
	// status changes before entering the recovery writer transaction.
	owners, prepErr := sqliteQueryIDs(ctx, s.readers, `SELECT DISTINCT owner_id FROM data_session_events WHERE status IN ('running','pending','started') AND payload_bytes>65536 LIMIT 100`)
	if prepErr != nil {
		return 0, sqliteSessionChanges{}, prepErr
	}
	preparer := &app{store: s, dir: s.dir}
	for _, owner := range owners {
		if err := preparer.prepareSessionEventStatuses(ctx, owner, "interrupted"); err != nil {
			return 0, sqliteSessionChanges{}, err
		}
	}
	count := 0
	stamp := at.UTC().Format(time.RFC3339Nano)
	changes, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		ids, err := sqliteQueryIDs(ctx, st.tx, `SELECT id FROM (
 SELECT id FROM data_sessions WHERE status='running'
 UNION SELECT owner_id FROM data_queued_messages WHERE status IN ('sending','accepted','queued','steering','interrupting')
 UNION SELECT owner_id FROM data_pending_permissions
 UNION SELECT owner_id FROM data_pending_questions
 UNION SELECT c.owner_id FROM data_session_capabilities c JOIN data_capability_preparing p ON p.owner_id=c.id WHERE p.x_version<>''
 UNION SELECT session_id FROM recovery_consultations
) ORDER BY id LIMIT 100`)
		if err != nil {
			return err
		}
		for _, id := range ids {
			meta, err := st.sessionMeta(id)
			if err != nil {
				return err
			}
			before := meta
			queueChanged, err := st.recoverQueue(id)
			if err != nil {
				return err
			}
			var requests bool
			if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_pending_permissions WHERE owner_id=?) OR EXISTS(SELECT 1 FROM data_pending_questions WHERE owner_id=?)`, id, id).Scan(&requests); err != nil {
				return err
			}
			for _, table := range []string{"pending_permissions", "pending_questions"} {
				if err := st.clearChildren(table, id); err != nil {
					return err
				}
			}
			key := sqliteOwnedKey(id, "")
			var preparing bool
			if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_capability_preparing WHERE owner_id=? AND x_version<>'')`, key).Scan(&preparing); err != nil {
				return err
			}
			if preparing {
				if err := st.putSingleChild("capability_preparing", key, nil); err != nil {
					return err
				}
			}
			if meta.Status == "running" {
				meta.Status = "error"
				e := event("error", "Execution interrupted by engine restart.")
				e.CreatedAt = stamp
				if err := st.tx.QueryRowContext(ctx, `SELECT consultation_id FROM recovery_consultations WHERE session_id=?`, id).Scan(&e.ConsultationID); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if _, err := st.appendEvent(id, e); err != nil {
					return err
				}
				if err := st.interruptSubagentParent(meta, stamp); err != nil {
					return err
				}
			}
			if _, err := st.tx.ExecContext(ctx, `DELETE FROM recovery_consultations WHERE session_id=?`, id); err != nil {
				return err
			}
			if queueChanged || requests || !reflect.DeepEqual(before, meta) {
				meta.UpdatedAt = stamp
			}
			if err := st.putSessionMeta(meta); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err != nil {
		return 0, sqliteSessionChanges{}, err
	}
	return count, changes, nil
}
