package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (st *sqliteSessionTx) initializeAnalytics() error {
	var a *AnalyticsState
	err := decodeSQLiteRecord(st.ctx, st.tx, "analytics_settings", "settings", 64<<10, &a)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if a != nil {
		return nil
	}
	if err := st.putRoot("analytics_settings", "settings", AnalyticsState{Version: 1, Started: now()}); err != nil {
		return err
	}
	for _, field := range []string{"executions", "counters", "graphs"} {
		if err := st.emptyCollection("analytics_settings", "settings", field, "object"); err != nil {
			return err
		}
	}
	return nil
}

func (st *sqliteSessionTx) analyticsExecution(id string) (AnalyticsExecution, string, error) {
	key := sqliteOwnedKey("settings", id)
	var e AnalyticsExecution
	err := decodeSQLiteRecord(st.ctx, st.tx, "analytics_executions", key, 1<<20, &e)
	return e, key, err
}

func (st *sqliteSessionTx) analyticsOwner(s Session) (Session, error) {
	if s.GraphRunID != "" {
		var r GraphRun
		if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_runs", s.GraphRunID, 4<<20, &r); err != nil {
			return s, err
		}
		return st.sessionMeta(r.ConversationID)
	}
	for depth := 0; s.ParentID != "" && depth < 3; depth++ {
		parent, err := st.sessionMeta(s.ParentID)
		if err != nil {
			return s, err
		}
		s = parent
	}
	if s.ParentID != "" {
		return s, errors.New("invalid accounting owner hierarchy")
	}
	return s, nil
}

func (st *sqliteSessionTx) reserveAnalytics(t *turn, s Session) error {
	if err := st.initializeAnalytics(); err != nil {
		return err
	}
	owner, err := st.analyticsOwner(s)
	if err != nil {
		return err
	}
	role := s.Role
	if role == "" {
		role = "main"
	}
	stamp := now()
	e := AnalyticsExecution{ID: t.analyticsID, SessionID: s.ID, OwnerID: owner.ID, ProjectID: owner.ProjectID, Role: role, Harness: s.Harness, GraphRunID: s.GraphRunID, Reserved: stamp, Last: stamp}
	if s.GraphRunID != "" {
		var id string
		err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_graph_activations WHERE x_runId=? AND session_id=? AND status IN ('reserved','running','waiting_user','sealing') ORDER BY position DESC LIMIT 1`, s.GraphRunID, s.ID).Scan(&id)
		if err == nil {
			var x GraphActivation
			if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_activations", id, 4<<20, &x); err != nil {
				return err
			}
			e.ActivationID, e.GraphTurnID = x.ID, x.CurrentTurnID
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	key := sqliteOwnedKey("settings", e.ID)
	if err := st.putRecord("analytics_executions", key, "settings", e.ID, -1, e); err != nil {
		return err
	}
	for _, field := range []string{"spans", "usage"} {
		if err := st.emptyCollection("analytics_executions", key, field, "array"); err != nil {
			return err
		}
	}
	return nil
}

func (st *sqliteSessionTx) analyticsReasonExists(key, reason string) (bool, error) {
	var found bool
	err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_analytics_reasons WHERE owner_id=? AND scalar_value=?)`, key, reason).Scan(&found)
	return found, err
}
func (st *sqliteSessionTx) clearPreparationTiming(key string) error {
	return st.eachMatching("analytics_reasons", `owner_id=? AND scalar_value IN ('clock_discontinuity','termination_or_clock_unknown','interrupted_tail_unknown','dispatch_acceptance_unknown')`, []any{key}, func(id string) error { return st.deleteRecord("analytics_reasons", id) })
}

// Advance only the open tail; historical spans/usage never enter the turn cache.
func (st *sqliteSessionTx) analyticsAdvance(e *AnalyticsExecution, key string, at time.Time) error {
	if e.Accepted == "" || e.Ended != "" {
		return nil
	}
	bad, err := st.analyticsReasonExists(key, "clock_discontinuity")
	if err != nil || bad {
		return err
	}
	if at.Before(analyticsTime(e.Last)) {
		e.UnknownUntil = now()
		return st.analyticsReason(key, "clock_discontinuity")
	}
	if !at.After(analyticsTime(e.Last)) {
		return nil
	}
	kind := "working"
	var waiting, unknown bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_analytics_waits WHERE owner_id=?),EXISTS(SELECT 1 FROM data_analytics_unknown_waits WHERE owner_id=?)`, key, key).Scan(&waiting, &unknown); err != nil {
		return err
	}
	if waiting {
		kind = "blocked"
	}
	if unknown {
		kind = "unknown"
	}
	stamp := analyticsStamp(at)
	var last string
	err = st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_analytics_spans WHERE owner_id=? ORDER BY position DESC LIMIT 1`, key).Scan(&last)
	if err == nil {
		var span AnalyticsSpan
		if err := decodeSQLiteRecord(st.ctx, st.tx, "analytics_spans", last, 64<<10, &span); err != nil {
			return err
		}
		if span.Kind == kind && span.End == e.Last {
			span.End = stamp
			if err := st.putExistingRecord("analytics_spans", last, span); err != nil {
				return err
			}
			e.Last = stamp
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count int64
	if err := st.tx.QueryRowContext(st.ctx, `SELECT size FROM record_collections WHERE parent_table='analytics_executions' AND parent_id=? AND field='spans'`, key).Scan(&count); err != nil {
		return err
	}
	if count >= 100000 {
		return errors.New("analytics execution capacity exceeded")
	}
	if err := st.appendScalarChild("analytics_spans", key, AnalyticsSpan{Start: e.Last, End: stamp, Kind: kind}); err != nil {
		return err
	}
	e.Last = stamp
	return nil
}

func (st *sqliteSessionTx) analyticsWait(t *turn, request string, start, attributable, exclusive bool) error {
	if t == nil || t.analyticsID == "" {
		return nil
	}
	e, key, err := st.analyticsExecution(t.analyticsID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.Ended != "" {
		return nil
	}
	if !attributable {
		return st.analyticsReason(key, "wait_origin_or_exclusivity_unknown")
	}
	if e.Accepted != "" {
		at, drift := t.analyticsInstant()
		if drift {
			e.UnknownUntil = now()
			if err := st.analyticsReason(key, "clock_discontinuity"); err != nil {
				return err
			}
		} else if err := st.analyticsAdvance(&e, key, at); err != nil {
			return err
		}
	}
	if start {
		table := "analytics_waits"
		if !exclusive {
			table = "analytics_unknown_waits"
			if err := st.analyticsReason(key, "wait_exclusivity_unknown"); err != nil {
				return err
			}
		}
		var count int
		if err := st.tx.QueryRowContext(st.ctx, `SELECT (SELECT count(*) FROM data_analytics_waits WHERE owner_id=?)+(SELECT count(*) FROM data_analytics_unknown_waits WHERE owner_id=?)`, key, key).Scan(&count); err != nil {
			return err
		}
		if count >= 1024 {
			return errors.New("analytics wait capacity exceeded")
		}
		if err := st.putRecord(table, sqliteOwnedKey(key, request), key, request, -1, true); err != nil {
			return err
		}
	} else {
		for _, table := range []string{"analytics_waits", "analytics_unknown_waits"} {
			if err := st.deleteRecord(table, sqliteOwnedKey(key, request)); err != nil {
				return err
			}
		}
	}
	return st.putExistingRecord("analytics_executions", key, e)
}

func (st *sqliteSessionTx) finishAnalytics(t *turn, uncertain bool, confirmed *time.Time) error {
	if t == nil || t.analyticsID == "" {
		return nil
	}
	e, key, err := st.analyticsExecution(t.analyticsID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.Ended != "" {
		return nil
	}
	if e.Accepted == "" {
		if err := st.clearPreparationTiming(key); err != nil {
			return err
		}
		e.UnknownUntil = ""
		if e.Dispatched != "" {
			e.Last = e.Dispatched
			e.UnknownUntil = now()
			if err := st.analyticsReason(key, "dispatch_acceptance_unknown"); err != nil {
				return err
			}
		}
	} else {
		at, drift := t.analyticsInstant()
		if uncertain || drift {
			e.UnknownUntil = now()
			reason := "termination_or_clock_unknown"
			if confirmed != nil {
				reason = "clock_discontinuity"
			}
			if err := st.analyticsReason(key, reason); err != nil {
				return err
			}
		} else {
			if confirmed != nil {
				end := t.analyticsAnchor.Add(confirmed.Sub(t.analyticsAnchor)).UTC()
				if end.Before(at) {
					at = end
				}
			}
			if confirmed != nil && !at.Before(analyticsTime(e.Accepted)) && at.Before(analyticsTime(e.Last)) {
				if err := st.eachMatching("analytics_spans", `owner_id=? AND x_start>=?`, []any{key, sqliteTimeKey(analyticsStamp(at))}, func(id string) error { return st.deleteRecord("analytics_spans", id) }); err != nil {
					return err
				}
				var id string
				err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_analytics_spans WHERE owner_id=? ORDER BY position DESC LIMIT 1`, key).Scan(&id)
				if err == nil {
					var span AnalyticsSpan
					if err := decodeSQLiteRecord(st.ctx, st.tx, "analytics_spans", id, 64<<10, &span); err != nil {
						return err
					}
					if analyticsTime(span.End).After(at) {
						span.End = analyticsStamp(at)
						if err := st.putExistingRecord("analytics_spans", id, span); err != nil {
							return err
						}
					}
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				e.Last = analyticsStamp(at)
			} else if err := st.analyticsAdvance(&e, key, at); err != nil {
				return err
			}
		}
	}
	if confirmed != nil {
		unknown, err := st.analyticsReasonExists(key, "clock_discontinuity")
		if err != nil {
			return err
		}
		if unknown {
			e.UnknownUntil = now()
		}
	}
	e.Ended = e.Last
	for _, table := range []string{"analytics_waits", "analytics_unknown_waits"} {
		if err := st.clearChildren(table, key); err != nil {
			return err
		}
	}
	return st.putExistingRecord("analytics_executions", key, e)
}

func (p *adapter) sqlAnalyticsWrite(change func(*sqliteSessionTx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.app.persistSQLLocked(ctx, change)
}
