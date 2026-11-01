package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const sqlPendingRecovery = `x_action='' AND x_resolved<>'true'`

func (st *sqliteSessionTx) recoveryNotice(owner string, r *AutomationRecovery) error {
	if r.NoticeEventID != "" {
		return nil
	}
	e := event("status", "Automation recovery requires a human decision: "+r.Snapshot.Name+" ("+r.Source+").")
	e.Data = map[string]any{"automationRecoveryNotice": true}
	if _, err := st.appendEvent(owner, e); err != nil {
		return err
	}
	r.NoticeEventID = e.ID
	return st.putRoot("automation_recoveries", r.ID, *r)
}
func (st *sqliteSessionTx) recoveryDecision(owner, id string) (*AutomationRecoveryOccurrence, error) {
	var key string
	err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_automation_occurrence_decisions WHERE owner_id=? AND record_id=? LIMIT 1`, owner, id).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var item AutomationRecoveryOccurrence
	err = decodeSQLiteRecord(st.ctx, st.tx, "automation_occurrence_decisions", key, 64<<10, &item)
	return &item, err
}

// Called under the shared tool dispatch lock. Only the requested page or selected
// decisions/receipt are loaded, never a recovery group's growing collections.
func (p *adapter) callSQLAutomationRecovery(name string, raw json.RawMessage) (any, error) {
	if name == "automation_recovery_read" {
		return p.readSQLAutomationRecovery(raw)
	}
	var args automationRecoveryResolveArgs
	if err := decodeGraphTool(raw, &args); err != nil {
		return nil, err
	}
	if (args.Action != "recover" && args.Action != "discard") || !validLinkedText(args.OperationID, 200) || len(args.TimeZone) > 100 || len(args.OccurrenceIDs) > 50 {
		return nil, errors.New("Provide recover/discard, a stable operationId and at most 50 occurrence IDs.")
	}
	err := p.app.writeSQLLocked(p.turn.ctx, func(st *sqliteSessionTx) error {
		var r AutomationRecovery
		if err := decodeSQLiteRecord(st.ctx, st.tx, "automation_recoveries", args.RecoveryID, 32<<20, &r); err != nil {
			return err
		}
		var receiptKey string
		err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_automation_resolutions WHERE owner_id=? AND x_operationId=? LIMIT 1`, r.ID, args.OperationID).Scan(&receiptKey)
		if err == nil {
			var receipt AutomationRecoveryResolution
			if err := decodeSQLiteRecord(st.ctx, st.tx, "automation_resolutions", receiptKey, 64<<10, &receipt); err != nil {
				return err
			}
			ids, more, err := readSQLChildren[string](st.ctx, st.tx, "automation_resolution_occurrences", receiptKey, 51, 64<<10)
			if err != nil {
				return err
			}
			if more || len(ids) > 50 {
				return errors.New("Invalid saved recovery receipt.")
			}
			receipt.OccurrenceIDs = ids
			r.Resolutions = []AutomationRecoveryResolution{receipt}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		ids := args.OccurrenceIDs
		if len(ids) == 0 && r.Source != "missed_schedule" {
			ids = []string{r.ID}
		}
		for _, id := range ids {
			decision, err := st.recoveryDecision(r.ID, id)
			if err != nil {
				return err
			}
			if decision != nil {
				r.OccurrenceDecisions = append(r.OccurrenceDecisions, *decision)
			}
		}
		occurrences, repeated, err := planRecoveryResolution(&r, &args)
		if err != nil {
			return err
		}
		if repeated {
			return nil
		}
		var notice, user int64
		if err := st.tx.QueryRowContext(st.ctx, `SELECT position FROM data_session_events WHERE owner_id=? AND record_id=?`, p.id, r.NoticeEventID).Scan(&notice); err != nil {
			return errors.New("Present the recovery first, then reference the user's real later recovery decision.")
		}
		if err := st.tx.QueryRowContext(st.ctx, `SELECT position FROM data_session_events WHERE owner_id=? AND record_id=? AND kind='user'`, p.id, args.SourceUserEventID).Scan(&user); err != nil || user <= notice {
			return errors.New("Present the recovery first, then reference the user's real later recovery decision.")
		}
		human, err := (sqliteValidation{ctx: st.ctx, tx: st.tx}).userEvent(p.id, args.SourceUserEventID)
		if err != nil {
			return err
		}
		if !human {
			return errors.New("Recovery requires a real human decision.")
		}
		if args.Action == "recover" {
			v, err := st.automation(r.Snapshot.ProjectID, r.Snapshot.ID, false)
			if err != nil {
				return err
			}
			target := r.Snapshot
			if automationSessionMode(target) == "reuse_created" && automationSessionMode(v) == "reuse_created" && v.SessionID != "" {
				target.SessionID = v.SessionID
			}
			if automationUsesSession(target) {
				if _, err := st.automationSessionEligible(target.ProjectID, target.SessionID); err != nil {
					return err
				}
			} else {
				topic, _, err := st.topic(target.ProjectID, target.TopicID)
				if err != nil {
					return err
				}
				if topic.Archived {
					return errors.New("Restore the automation's topic before recovery.")
				}
			}
			var pending bool
			if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&pending); err != nil {
				return err
			}
			if pending {
				return errors.New("Finish Library recovery first.")
			}
		}
		// Collections are explicit row writes below. Keep the metadata write small.
		r.Resolutions, r.OccurrenceDecisions = nil, nil
		if r.Source == "missed_schedule" && r.TimeZone == "" {
			r.TimeZone = args.TimeZone
		}
		for _, occurrence := range occurrences {
			occurrence.Action = args.Action
			if err := st.putRecord("automation_occurrence_decisions", sqliteOwnedKey(r.ID, occurrence.ID), r.ID, occurrence.ID, -1, occurrence); err != nil {
				return err
			}
			if args.Action == "recover" {
				snapshot := copyAutomation(r.Snapshot)
				id, err := st.enqueueAutomation(snapshot, "recovery/"+occurrence.ID, "recovery", occurrence.ScheduledAt, r.Variables, &snapshot)
				if err != nil {
					return err
				}
				var trigger AutomationTrigger
				if err := decodeSQLiteRecord(st.ctx, st.tx, "automation_triggers", id, 32<<20, &trigger); err != nil {
					return err
				}
				trigger.Capabilities = r.Capabilities
				if err := st.putRoot("automation_triggers", id, trigger); err != nil {
					return err
				}
			}
		}
		receipt := AutomationRecoveryResolution{TimeZone: args.TimeZone, OperationID: args.OperationID, Action: args.Action, UserEventID: args.SourceUserEventID}
		receiptKey = sqliteOwnedKey(r.ID, args.OperationID)
		if err := st.putRecord("automation_resolutions", receiptKey, r.ID, args.OperationID, -1, receipt); err != nil {
			return err
		}
		for _, id := range args.OccurrenceIDs {
			if err := st.appendScalarChild("automation_resolution_occurrences", receiptKey, id); err != nil {
				return err
			}
		}
		if r.Source != "missed_schedule" {
			r.Action, r.OperationID, r.UserEventID, r.Resolved = args.Action, args.OperationID, args.SourceUserEventID, true
		} else {
			stamp := r.NextOccurrenceAt
			if stamp == "" {
				stamp = r.From
			}
			at, err := time.Parse(time.RFC3339Nano, stamp)
			if err != nil {
				return err
			}
			end, err := time.Parse(time.RFC3339Nano, r.Until)
			if err != nil {
				return err
			}
			for !at.IsZero() && !at.After(end) {
				decision, err := st.recoveryDecision(r.ID, recoveryOccurrence(&r, at).ID)
				if err != nil {
					return err
				}
				if decision == nil {
					break
				}
				at = nextAutomationOccurrence(r.Snapshot.Schedule, recoveryZone(&r), at)
			}
			r.Resolved = at.IsZero() || at.After(end)
			r.NextOccurrenceAt = ""
			if !r.Resolved {
				r.NextOccurrenceAt = at.Format(time.RFC3339Nano)
			}
		}
		return st.putRoot("automation_recoveries", r.ID, r)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"accepted": true, "action": args.Action, "occurrenceIds": args.OccurrenceIDs}, nil
}

func (p *adapter) readSQLAutomationRecovery(raw json.RawMessage) (any, error) {
	var args struct {
		TimeZone   string `json:"timeZone"`
		RecoveryID string `json:"recoveryId"`
		After      string `json:"after"`
		Offset     int    `json:"offset"`
		Limit      int    `json:"limit"`
	}
	if err := decodeGraphTool(raw, &args); err != nil {
		return nil, err
	}
	if args.Offset < 0 || args.Limit < 0 || args.Limit > 50 || len(args.After) > 100 || len(args.TimeZone) > 100 {
		return nil, errors.New("Invalid recovery pagination bounds.")
	}
	if args.Limit == 0 {
		args.Limit = 50
	}
	var result any
	err := p.app.writeSQLLocked(p.turn.ctx, func(st *sqliteSessionTx) error {
		if args.RecoveryID != "" {
			var r AutomationRecovery
			if err := decodeSQLiteRecord(st.ctx, st.tx, "automation_recoveries", args.RecoveryID, 32<<20, &r); err != nil {
				return err
			}
			if err := st.recoveryNotice(p.id, &r); err != nil {
				return err
			}
			if r.Source == "missed_schedule" && r.TimeZone == "" {
				if args.TimeZone == "" {
					result = map[string]any{"recoveryId": r.ID, "requiresTimeZone": true, "instruction": "This legacy interval did not save its time zone. Ask the user for the original IANA time zone, then supply it to read and resolve. Do not substitute the engine's current setting."}
					return nil
				}
				if args.TimeZone == "Local" {
					return errors.New("Provide an IANA time zone.")
				}
				if _, err := time.LoadLocation(args.TimeZone); err != nil {
					return err
				}
				r.TimeZone = args.TimeZone
			}
			items, next, more, err := recoveryOccurrencePage(&r, args.After, args.Limit)
			if err != nil {
				return err
			}
			for i := range items {
				decision, err := st.recoveryDecision(r.ID, items[i].ID)
				if err != nil {
					return err
				}
				if decision != nil {
					items[i].Action = decision.Action
				}
			}
			result = map[string]any{"recoveryId": r.ID, "name": r.Snapshot.Name, "timeZone": recoveryZone(&r), "items": items, "nextAfter": next, "hasMore": more, "instruction": "Present the actual missed times and ask which occurrence IDs to recover or discard. Each selected occurrence creates its own run using its saved session mode; none is selected automatically."}
			return nil
		}
		ids, err := sqliteQueryIDs(st.ctx, st.tx, `SELECT id FROM data_automation_recoveries WHERE `+sqlPendingRecovery+` ORDER BY position,id LIMIT ? OFFSET ?`, args.Limit+1, args.Offset)
		if err != nil {
			return err
		}
		more := len(ids) > args.Limit
		if more {
			ids = ids[:args.Limit]
		}
		items := []any{}
		for _, id := range ids {
			var r AutomationRecovery
			if err := decodeSQLiteRecord(st.ctx, st.tx, "automation_recoveries", id, 32<<20, &r); err != nil {
				return err
			}
			if err := st.recoveryNotice(p.id, &r); err != nil {
				return err
			}
			items = append(items, map[string]any{"id": r.ID, "name": r.Snapshot.Name, "projectId": r.Snapshot.ProjectID, "source": r.Source, "from": r.From, "until": r.Until, "timeZone": recoveryZone(&r), "nextOccurrenceAt": r.NextOccurrenceAt, "sessionId": r.SessionID, "delivery": r.Delivery})
		}
		result = map[string]any{"items": items, "hasMore": more, "nextOffset": args.Offset + len(items), "instruction": "Read each chosen recoveryId to enumerate the missed occurrence times and IDs. Explain them and ask which to recover or discard; each recovered occurrence creates its own run using its saved session mode. Use only a real later human reply for resolution."}
		return nil
	})
	return result, err
}

func (a *app) scheduleSQLAutomationRecoveryLocked() error {
	if a.closing || a.storageErr != nil || a.harnessInstalling {
		return nil
	}
	var id string
	err := a.store.readers.QueryRowContext(a.ctx, `SELECT id FROM data_sessions WHERE role='general_agent' LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if a.runs[id] != nil {
		return nil
	}
	s, err := a.store.readSessionMeta(a.ctx, id)
	if err != nil {
		return err
	}
	filter := ""
	if !s.HarnessLocked {
		filter = ` AND x_noticeEventId=''`
	}
	ids, err := sqliteQueryIDs(a.ctx, a.store.readers, `SELECT id FROM data_automation_recoveries WHERE `+sqlPendingRecovery+` AND x_delivery='pending'`+filter+` ORDER BY position,id LIMIT 100`)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	b, installed := a.binaries[s.Harness]
	cwd := ""
	var native nativeSession
	if s.HarnessLocked {
		if !installed {
			return nil
		}
		cwd, err = existingDirectory(s.ExecutionCWD)
		if err != nil {
			return nil
		}
		_, native, _, err = a.store.readTurnSession(a.ctx, s.ID)
		if err != nil {
			return err
		}
	}
	err = a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
		e := event("status", "Automations need recovery decisions after engine restart.")
		e.Data = map[string]any{"automationRecoveryNotice": true}
		if !s.HarnessLocked {
			e.Text = "Automations need recovery decisions. Select a harness and message KLM Agent to continue."
			e.Status = "warning"
		}
		if _, err := st.appendEvent(s.ID, e); err != nil {
			return err
		}
		for _, id := range ids {
			var r AutomationRecovery
			if err := decodeSQLiteRecord(st.ctx, st.tx, "automation_recoveries", id, 32<<20, &r); err != nil {
				return err
			}
			r.NoticeEventID = e.ID
			if s.HarnessLocked {
				r.Delivery = "delivering"
			}
			if err := st.putRoot("automation_recoveries", r.ID, r); err != nil {
				return err
			}
		}
		if s.HarnessLocked {
			s.Status, s.UpdatedAt = "running", now()
			return st.putSessionMeta(s)
		}
		return nil
	})
	if err != nil || !s.HarnessLocked {
		return err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	t := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{}), automationRecoveryIDs: ids}
	a.runs[s.ID] = t
	a.wg.Add(1)
	text := "The engine restarted and detected automation runs interrupted or scheduled occurrences missed during downtime. Call automation_recovery_read, then read the affected recoveryId groups to enumerate actual scheduled times and stable occurrence IDs. Analyze and ask the user which occurrences to recover or discard. Do not execute a recovery before an explicit later human decision. Every selected occurrence admits its own run using saved configuration, session mode and normal serialization; reusable destinations get a follow-up in the same session. Do not replace several missed occurrences with one catch-up. Do not automatically retry uncertain notification delivery."
	go a.execute(t, s, native, b, cwd, submission{Text: text})
	return nil
}
