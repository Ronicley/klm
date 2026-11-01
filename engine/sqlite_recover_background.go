package main

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

const sqliteRestartGraphCause = "Graph run interrupted by engine restart; execution was not replayed."

// Only unfinished scheduler rows are read. Captured definitions and historical
// activation payloads stay cold; scheduled activities retain their identity.
func (s *sqliteStore) recoverGraphBatch(ctx context.Context, at time.Time) (int, sqliteSessionChanges, error) {
	count := 0
	stamp := at.UTC().Format(time.RFC3339Nano)
	changes, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		ids, err := sqliteQueryIDs(ctx, st.tx, `SELECT id FROM data_graph_runs WHERE status IN ('starting','running','ending') ORDER BY id LIMIT 100`)
		if err != nil {
			return err
		}
		for _, id := range ids {
			var r GraphRun
			if err := decodeSQLiteRecord(ctx, st.tx, "graph_runs", id, 4<<20, &r); err != nil {
				return err
			}
			if r.Revision == ^uint64(0) {
				return errors.New("graph revision exhausted")
			}
			r.Status, r.UpdatedAt, r.EndedAt = "interrupted", stamp, stamp
			r.Revision++
			r.Result = &GraphRunResult{Kind: "interrupted", Error: sqliteRestartGraphCause}
			if err := st.putRoot("graph_runs", id, r); err != nil {
				return err
			}
			var a GraphActivity
			if err := decodeSQLiteRecord(ctx, st.tx, "graph_activities", r.ActivityID, 4<<20, &a); err != nil {
				return err
			}
			if a.Version == ^uint64(0) {
				return errors.New("activity version exhausted")
			}
			a.Status, a.UpdatedAt = "awaiting_assessment", stamp
			a.Version++
			if err := st.putRoot("graph_activities", a.ID, a); err != nil {
				return err
			}
			var exists bool
			if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_notifications WHERE x_runId=?)`, id).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				n := RunNotification{ID: "graph-result:" + id, RunID: id, ConversationID: r.ConversationID, Status: "pending", CreatedAt: stamp}
				if err := st.putRoot("graph_notifications", n.ID, n); err != nil {
					return err
				}
			}
			count++
		}
		for _, category := range []struct{ table, where string }{
			{"graph_activations", `status IN ('reserved','running','waiting_user','sealing')`},
			{"graph_deliveries", `status='pending'`}, {"graph_join_rounds", `status IN ('collecting','running')`},
			{"graph_workspaces", `status='reserved'`}, {"graph_notifications", `status='delivering'`},
		} {
			ids, err := sqliteQueryIDs(ctx, st.tx, `SELECT id FROM data_`+category.table+` WHERE `+category.where+` ORDER BY id LIMIT 100`)
			if err != nil {
				return err
			}
			for _, id := range ids {
				var value any
				switch category.table {
				case "graph_activations":
					var x GraphActivation
					if err := decodeSQLiteRecord(ctx, st.tx, category.table, id, 4<<20, &x); err != nil {
						return err
					}
					x.Status, x.Error, x.UpdatedAt, x.EndedAt = "interrupted", sqliteRestartGraphCause, stamp, stamp
					value = x
				case "graph_deliveries":
					var x GraphDelivery
					if err := decodeSQLiteRecord(ctx, st.tx, category.table, id, 4<<20, &x); err != nil {
						return err
					}
					x.Status = "interrupted"
					value = x
				case "graph_join_rounds":
					var x JoinRound
					if err := decodeSQLiteRecord(ctx, st.tx, category.table, id, 4<<20, &x); err != nil {
						return err
					}
					x.Status, x.EndedAt = "interrupted", stamp
					value = x
				case "graph_workspaces":
					var x WorkspaceRecord
					if err := decodeSQLiteRecord(ctx, st.tx, category.table, id, 4<<20, &x); err != nil {
						return err
					}
					x.Status, x.Error = "interrupted", sqliteRestartGraphCause
					value = x
				case "graph_notifications":
					var x RunNotification
					if err := decodeSQLiteRecord(ctx, st.tx, category.table, id, 4<<20, &x); err != nil {
						return err
					}
					x.Status = "pending"
					value = x
				}
				if err := st.putRoot(category.table, id, value); err != nil {
					return err
				}
				count++
			}
		}
		return nil
	})
	if err != nil {
		return 0, sqliteSessionChanges{}, err
	}
	return count, changes, nil
}

func (st *sqliteSessionTx) appendScalarChild(table, owner string, value any) error {
	var position int64
	if err := st.tx.QueryRowContext(st.ctx, `SELECT coalesce(max(position),-1)+1 FROM data_`+table+` WHERE owner_id=?`, owner).Scan(&position); err != nil {
		return err
	}
	key := strconv.FormatInt(position, 10)
	return st.putRecord(table, sqliteOwnedKey(owner, key), owner, key, position, value)
}

func (st *sqliteSessionTx) analyticsReason(owner, reason string) error {
	var exists bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_analytics_reasons WHERE owner_id=? AND scalar_value=?)`, owner, reason).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	return st.appendScalarChild("analytics_reasons", owner, reason)
}

func (s *sqliteStore) recoverAnalyticsBatch(ctx context.Context, at time.Time) (int, sqliteSessionChanges, error) {
	count := 0
	stamp := at.UTC().Format(time.RFC3339Nano)
	changes, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		ids, err := sqliteQueryIDs(ctx, st.tx, `SELECT id FROM data_analytics_executions WHERE x_ended='' ORDER BY id LIMIT 100`)
		if err != nil {
			return err
		}
		for _, id := range ids {
			var e AnalyticsExecution
			if err := decodeSQLiteRecord(ctx, st.tx, "analytics_executions", id, 1<<20, &e); err != nil {
				return err
			}
			if e.Accepted == "" {
				for {
					reasons, err := sqliteQueryIDs(ctx, st.tx, `SELECT id FROM data_analytics_reasons WHERE owner_id=? AND scalar_value IN ('clock_discontinuity','termination_or_clock_unknown','interrupted_tail_unknown','dispatch_acceptance_unknown') LIMIT 100`, id)
					if err != nil {
						return err
					}
					if len(reasons) == 0 {
						break
					}
					for _, key := range reasons {
						if err := st.deleteRecord("analytics_reasons", key); err != nil {
							return err
						}
					}
				}
				e.UnknownUntil = ""
				if e.Dispatched != "" {
					e.Last = e.Dispatched
					e.UnknownUntil = stamp
					if err := st.analyticsReason(id, "dispatch_acceptance_unknown"); err != nil {
						return err
					}
				}
			} else {
				e.UnknownUntil = stamp
				if err := st.analyticsReason(id, "interrupted_tail_unknown"); err != nil {
					return err
				}
			}
			e.Ended = e.Last
			if err := st.clearChildren("analytics_waits", id); err != nil {
				return err
			}
			if err := st.clearChildren("analytics_unknown_waits", id); err != nil {
				return err
			}
			if err := st.putExistingRecord("analytics_executions", id, e); err != nil {
				return err
			}
			count++
		}
		ids, err = sqliteQueryIDs(ctx, st.tx, `SELECT id FROM data_analytics_graphs WHERE x_end='' ORDER BY id LIMIT 100`)
		if err != nil {
			return err
		}
		for _, id := range ids {
			var g AnalyticsGraphTiming
			if err := decodeSQLiteRecord(ctx, st.tx, "analytics_graphs", id, 64<<10, &g); err != nil {
				return err
			}
			g.End, g.Interrupted = g.Last, true
			if err := st.putExistingRecord("analytics_graphs", id, g); err != nil {
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

func (s *sqliteStore) recoverAutomationBatch(ctx context.Context, at time.Time) (int, sqliteSessionChanges, error) {
	count := 0
	stamp := at.UTC().Format(time.RFC3339Nano)
	changes, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		var settings EngineSettings
		if err := decodeSQLiteRecord(ctx, st.tx, "engine_settings", "settings", 64<<10, &settings); err != nil {
			return err
		}
		if settings.TimeZone == "" {
			settings.TimeZone = defaultEngineTimeZone
			if err := st.putRoot("engine_settings", "settings", settings); err != nil {
				return err
			}
			count++
		}
		ids, err := sqliteQueryIDs(ctx, st.tx, `SELECT id FROM data_automation_runs WHERE status='running' ORDER BY id LIMIT 100`)
		if err != nil {
			return err
		}
		for _, id := range ids {
			var r AutomationRun
			if err := decodeSQLiteRecord(ctx, st.tx, "automation_runs", id, 8<<20, &r); err != nil {
				return err
			}
			recovery := AutomationRecovery{ID: newID(), RunID: r.ID, Snapshot: copyAutomation(r.Snapshot), Capabilities: r.Capabilities, Variables: r.Trigger.Variables, Source: "interrupted", From: r.StartedAt, Until: stamp, SessionID: r.SessionID, Delivery: "pending"}
			if err := st.putRoot("automation_recoveries", recovery.ID, recovery); err != nil {
				return err
			}
			r.Status, r.Reason, r.FinishedAt, r.NeedsContinuation = "execution_failed", "Execution interrupted by engine restart. Recovery requires a human decision.", stamp, false
			if err := st.putRoot("automation_runs", id, r); err != nil {
				return err
			}
			meta, err := st.sessionMeta(r.SessionID)
			if err != nil {
				return err
			}
			if meta.Automation != nil && meta.Automation.RunID == r.ID {
				meta.Status, meta.UpdatedAt = "error", stamp
				meta.Automation.Status = r.Status
				if _, err := st.recoverQueue(meta.ID, "Automation finished. Send this message when ready."); err != nil {
					return err
				}
				e := event("status", "Automation: "+r.Status+". "+r.Reason)
				e.CreatedAt = stamp
				e.Data = map[string]any{"automationId": r.AutomationID, "runId": r.ID}
				if _, err := st.appendEvent(meta.ID, e); err != nil {
					return err
				}
				if err := st.putSessionMeta(meta); err != nil {
					return err
				}
			}
			count++
		}
		ids, err = sqliteQueryIDs(ctx, st.tx, `SELECT id FROM data_automations WHERE x_enabled='true' AND x_deleted<>'true' AND x_mode='schedule' AND x_nextScheduledAt<>'' AND x_nextScheduledAt<=? ORDER BY x_nextScheduledAt,id LIMIT 100`, sqliteTimeKey(stamp))
		if err != nil {
			return err
		}
		for _, id := range ids {
			var a Automation
			if err := decodeSQLiteRecord(ctx, st.tx, "automations", id, 1<<20, &a); err != nil {
				return err
			}
			snapshot := copyAutomation(a)
			if automationUsesSession(snapshot) {
				meta, err := st.sessionMeta(snapshot.SessionID)
				if err == nil {
					if err := decodeSQLiteRecord(ctx, st.tx, "capability_selections", sqliteOwnedKey(sqliteOwnedKey(meta.ID, ""), ""), 8<<20, &meta.Capabilities.Desired); err != nil {
						return err
					}
					captureAutomationSessionSettings(&snapshot, &meta)
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
			recovery := AutomationRecovery{ID: newID(), Snapshot: snapshot, TimeZone: settings.TimeZone, NextOccurrenceAt: a.NextScheduledAt, Source: "missed_schedule", From: a.NextScheduledAt, Until: stamp, Delivery: "pending"}
			if err := st.putRoot("automation_recoveries", recovery.ID, recovery); err != nil {
				return err
			}
			if a.Schedule.Recurrence == "once" {
				a.OnceTriggeredAt = a.NextScheduledAt
			}
			next := nextAutomationOccurrence(a.Schedule, settings.TimeZone, at)
			a.NextScheduledAt = ""
			if !next.IsZero() {
				a.NextScheduledAt = next.Format(time.RFC3339Nano)
			}
			if err := st.putRoot("automations", a.ID, a); err != nil {
				return err
			}
			count++
		}
		ids, err = sqliteQueryIDs(ctx, st.tx, `SELECT id FROM data_automation_recoveries WHERE x_delivery='delivering' OR (x_source='missed_schedule' AND x_action='' AND x_resolved<>'true' AND x_nextOccurrenceAt='' AND x_from<>'') ORDER BY id LIMIT 100`)
		if err != nil {
			return err
		}
		for _, id := range ids {
			var r AutomationRecovery
			if err := decodeSQLiteRecord(ctx, st.tx, "automation_recoveries", id, 8<<20, &r); err != nil {
				return err
			}
			if r.Delivery == "delivering" {
				r.Delivery = "uncertain"
			}
			if r.Source == "missed_schedule" && recoveryPending(&r) && r.NextOccurrenceAt == "" {
				r.NextOccurrenceAt = r.From
			}
			if err := st.putRoot("automation_recoveries", id, r); err != nil {
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

// SQL recovery barrier, before client/control readiness. Filesystem outbox
// roll-forward and board reconciliation remain separate startup stages.
func recoverSQLiteRuntime(ctx context.Context, s *sqliteStore, at time.Time, hook sqliteMigrationHook) error {
	for {
		count := 0
		_, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
			rows, err := st.tx.QueryContext(ctx, `SELECT request_id,session_id,grant_id FROM permission_reply_intents LIMIT 100`)
			if err != nil {
				return err
			}
			type intent struct{ request, session, grant string }
			items := []intent{}
			for rows.Next() {
				var i intent
				if err := rows.Scan(&i.request, &i.session, &i.grant); err != nil {
					rows.Close()
					return err
				}
				items = append(items, i)
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
			for _, i := range items {
				if i.grant != "" {
					if err := st.deleteRecord("permission_grants", i.grant); err != nil {
						return err
					}
				}
				if _, err := st.tx.ExecContext(ctx, `DELETE FROM permission_reply_intents WHERE request_id=?`, i.request); err != nil {
					return err
				}
				st.changes.Sessions[i.session] = nil
				count++
			}
			return nil
		})
		if err != nil {
			return err
		}
		if count == 0 {
			break
		}
	}
	if err := s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM runtime_activity WHERE active=1 OR reasoning_title<>'' OR session_id IN(SELECT id FROM data_sessions WHERE status='running')`)
		return err
	}); err != nil {
		return err
	}
	for _, phase := range []struct {
		name  string
		batch func(context.Context, time.Time) (int, sqliteSessionChanges, error)
	}{
		{"analytics", s.recoverAnalyticsBatch}, {"automations", s.recoverAutomationBatch}, {"graphs", s.recoverGraphBatch}, {"consultations", s.recoverConsultationBatch}, {"sessions", s.recoverSessionBatch},
	} {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, _, err := phase.batch(ctx, at)
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
			if err := hook.reach("recovery:" + phase.name); err != nil {
				return err
			}
		}
	}
	return nil
}
