package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"
)

func (p *adapter) commitSQLStreamLocked(updates []streamUpdate) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := p.app.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
		meta, err := st.sessionMeta(p.id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		changed := false
		trackReasoning := func(e Event) error {
			if meta.Status != "running" || e.Type != "reasoning" {
				return nil
			}
			title := reasoningTitleLine(e.Title)
			if title == "" {
				title = reasoningTitleLine(e.Text)
			}
			if title == "" {
				return nil
			}
			_, err := st.tx.ExecContext(ctx, `UPDATE runtime_activity SET reasoning_title=? WHERE session_id=?`, title, p.id)
			return err
		}
		for _, update := range updates {
			e, updated, err := st.streamEvent(p.id, update)
			if err != nil {
				return err
			}
			if !updated {
				continue
			}
			if err := trackReasoning(e); err != nil {
				return err
			}
			changed = true
		}
		if changed {
			meta.UpdatedAt = now()
			return st.putSessionMeta(meta)
		}
		return nil
	})
	if err != nil {
		return p.app.failStorageLocked(err)
	}
	return nil
}

func (p *adapter) setSQLUsageLocked(usage *SessionUsage, baseline, final bool) error {
	if !p.subagent && p.app.runs[p.id] != p.turn {
		return nil
	}
	encoded, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	var owned *SessionUsage
	if err := json.Unmarshal(encoded, &owned); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = p.app.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
		meta, err := st.sessionMeta(p.id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var previous *SessionUsage
		err = decodeSQLiteRecord(ctx, st.tx, "session_usage", sqliteOwnedKey(p.id, ""), 64<<10, &previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var mutation *analyticsUsageMutation
		if !p.subagent && p.analyticsNative != "" && p.turn != nil && p.turn.analyticsID != "" {
			key := sqliteOwnedKey("settings", p.turn.analyticsID)
			var execution AnalyticsExecution
			if err := decodeSQLiteRecord(ctx, st.tx, "analytics_executions", key, 1<<20, &execution); err == nil {
				if execution.Sequence == ^uint64(0) {
					return errors.New("analytics sequence exhausted")
				}
				var counter AnalyticsCounter
				if err := decodeSQLiteRecord(ctx, st.tx, "analytics_counters", sqliteOwnedKey("settings", p.analyticsNative), 64<<10, &counter); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				mutation = analyticsUsageFor(execution, counter, p.analyticsNative, owned, baseline, final)
				if mutation != nil {
					if execution.SessionID != p.id || !analyticsValidCount(mutation.Counter.Input) || !analyticsValidCount(mutation.Counter.Output) {
						return errors.New("analytics observation owner/count mismatch")
					}
					if mutation.Delta.Input != nil || mutation.Delta.Output != nil {
						var count int64
						if err := st.tx.QueryRowContext(ctx, `SELECT coalesce((SELECT size FROM record_collections WHERE parent_table='analytics_executions' AND parent_id=? AND field='usage'),0)`, key).Scan(&count); err != nil {
							return err
						}
						if count >= 100000 {
							return errors.New("analytics execution capacity exceeded")
						}
						if err := st.appendScalarChild("analytics_usage", key, mutation.Delta); err != nil {
							return err
						}
					}
					execution.Sequence = mutation.Delta.Sequence
					execution.InputBaseline, execution.OutputBaseline = mutation.InputBaseline, mutation.OutputBaseline
					execution.InputFinal, execution.OutputFinal = mutation.InputFinal, mutation.OutputFinal
					if mutation.Reason != "" {
						if err := st.analyticsReason(key, mutation.Reason); err != nil {
							return err
						}
					}
					if err := st.putExistingRecord("analytics_executions", key, execution); err != nil {
						return err
					}
					if err := st.putRecord("analytics_counters", sqliteOwnedKey("settings", p.analyticsNative), "settings", p.analyticsNative, -1, mutation.Counter); err != nil {
						return err
					}
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if mutation == nil && reflect.DeepEqual(previous, owned) {
			return nil
		}
		if err := st.putSingleChild("session_usage", p.id, owned); err != nil {
			return err
		}
		meta.UpdatedAt = now()
		return st.putSessionMeta(meta)
	})
	if err != nil {
		return p.app.failStorageLocked(err)
	}
	return nil
}
