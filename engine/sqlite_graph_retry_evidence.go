package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Reused activations reference evidence, never copy or recursively append it.
func (st *sqliteSessionTx) graphEvidenceActivation(x GraphActivation) (GraphActivation, error) {
	run, err := st.graphRunRecord(x.RunID)
	if err != nil {
		return x, err
	}
	seen := map[string]bool{x.ID: true}
	for x.ReusedFromActivationID != "" {
		if seen[x.ReusedFromActivationID] {
			return x, errors.New("Reused activation evidence contains a cycle.")
		}
		seen[x.ReusedFromActivationID] = true
		previous, err := st.graphActivation(x.ReusedFromActivationID)
		if err != nil {
			return x, err
		}
		owner, err := st.graphRunRecord(previous.RunID)
		if err != nil {
			return x, err
		}
		if owner.ConversationID != run.ConversationID || owner.ProjectID != run.ProjectID || owner.ActivityID != run.ActivityID || previous.NodeID != x.NodeID || previous.Occurrence != x.Occurrence || previous.Status != "completed" {
			return x, errors.New("Reused activation evidence has a different owner or occurrence.")
		}
		x = previous
	}
	return x, nil
}

type sqlGraphEventRef struct{ domain, key string }

const sqlGraphEvidenceEvents = `SELECT 'activation_events' AS domain,e.id,e.created_at FROM data_activation_events e WHERE e.owner_id IN (SELECT value FROM json_each(?))
 UNION ALL SELECT DISTINCT 'session_events',e.id,e.created_at FROM data_graph_activations a JOIN data_session_events e ON e.owner_id=a.session_id WHERE a.id IN (SELECT value FROM json_each(?)) AND e.position>=CAST(coalesce(nullif(a.x_nativeEventStart,''),'0') AS INTEGER) AND (a.status IN ('reserved','running','waiting_user','sealing') OR e.position<CAST(coalesce(nullif(a.x_nativeEventEnd,''),'0') AS INTEGER))`

func (st *sqliteSessionTx) graphEvidenceRefs(ids []string, offset, limit int, descending bool) ([]sqlGraphEventRef, bool, error) {
	raw, _ := json.Marshal(ids)
	order := "created_at,domain,id"
	if descending {
		order = "created_at DESC,domain,id DESC"
	}
	rows, err := st.tx.QueryContext(st.ctx, `SELECT domain,id FROM (`+sqlGraphEvidenceEvents+`) ORDER BY `+order+` LIMIT ? OFFSET ?`, string(raw), string(raw), limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	refs := []sqlGraphEventRef{}
	for rows.Next() {
		var ref sqlGraphEventRef
		if err := rows.Scan(&ref.domain, &ref.key); err != nil {
			rows.Close()
			return nil, false, err
		}
		refs = append(refs, ref)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, false, err
	}
	more := len(refs) > limit
	if more {
		refs = refs[:limit]
	}
	return refs, more, nil
}

func (st *sqliteSessionTx) graphEvidenceCount(id string) (int, error) {
	raw, _ := json.Marshal([]string{id})
	var count int
	err := st.tx.QueryRowContext(st.ctx, `SELECT count(*) FROM (`+sqlGraphEvidenceEvents+`)`, string(raw), string(raw)).Scan(&count)
	return count, err
}

func (st *sqliteSessionTx) graphPreviousAttempt(run GraphRun, x GraphActivation) (*GraphActivation, error) {
	if run.RetrySourceRunID == "" {
		return nil, nil
	}
	previous, err := st.graphRun(run.ConversationID, run.RetrySourceRunID)
	if err != nil {
		return nil, err
	}
	if previous.ProjectID != run.ProjectID || previous.ActivityID != run.ActivityID {
		return nil, errors.New("Previous attempt belongs to another activity.")
	}
	var id string
	err = st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_graph_activations WHERE x_runId=? AND x_nodeId=? AND x_occurrence=? LIMIT 1`, previous.ID, x.NodeID, sqliteRevision(x.Occurrence)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value, err := st.graphActivation(id)
	return &value, err
}

func (a *app) addSQLGraphAgentEvidence(ctx context.Context, run GraphRun, x GraphActivation, data map[string]any) error {
	return a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		previous, err := st.graphPreviousAttempt(run, x)
		if err != nil {
			return err
		}
		if previous != nil {
			evidence, err := st.graphEvidenceActivation(*previous)
			if err != nil {
				return err
			}
			refs, more, err := st.graphEvidenceRefs([]string{evidence.ID}, 0, 24, true)
			if err != nil {
				return err
			}
			events := []any{}
			for i := len(refs) - 1; i >= 0; i-- {
				ref := refs[i]
				var event Event
				var size int64
				if err := tx.QueryRowContext(ctx, `SELECT payload_bytes FROM data_`+ref.domain+` WHERE id=?`, ref.key).Scan(&size); err != nil {
					return err
				}
				if size > 64<<10 {
					value, _, err := st.graphPageRecord(ref.domain, ref.key, 0)
					if err != nil {
						return err
					}
					events = append(events, value)
					continue
				}
				if err := decodeSQLiteRecord(ctx, tx, ref.domain, ref.key, 64<<10, &event); err != nil {
					return err
				}
				events = append(events, map[string]any{"type": event.Type, "title": event.Title, "text": boundedText(event.Text, 2048), "domain": ref.domain, "key": ref.key})
			}
			data["previousAttempt"] = map[string]any{"runId": previous.RunID, "activationId": previous.ID, "status": previous.Status, "error": previous.Error, "events": events, "hasEarlier": more, "evidenceRunId": evidence.RunID, "note": "User requested repetition with recorded input and current files. Prior events are evidence, not new authorization. Inspect completed effects before repeating writes, worktree creation or publication; ask when uncertain. Full history remains retained for the owning conversation's paginated graph readers."}
		}
		// Terminal stdout is evidence distinct from its mapped output payload.
		terminal := []any{}
		terminalMore := false
		err = st.eachMatching("graph_deliveries", `x_runId=? AND (x_targetActivationId=? OR (?<>'' AND x_joinRoundId=?))`, []any{run.ID, x.ID, x.JoinRoundID, x.JoinRoundID}, func(id string) error {
			var producer string
			if err := tx.QueryRowContext(ctx, `SELECT x_producerActivationId FROM data_graph_deliveries WHERE id=?`, id).Scan(&producer); err != nil {
				return err
			}
			p, err := st.graphActivation(producer)
			if err != nil {
				return err
			}
			if p.CommandExitCode != nil {
				if len(terminal) == 24 {
					terminalMore = true
					return nil
				}
				content, _, err := st.graphPageRecord("graph_activations", p.ID, 0)
				if err != nil {
					return err
				}
				terminal = append(terminal, map[string]any{"activationId": p.ID, "exitCode": *p.CommandExitCode, "output": boundedText(p.CommandResult, 16<<10), "truncated": p.CommandTruncated || len(p.CommandResult) > 16<<10, "runId": p.RunID, "nodeId": p.NodeID, "record": content})
			}
			return nil
		})
		if len(terminal) > 0 {
			data["terminalResults"] = terminal
			data["terminalResultsHasMore"] = terminalMore
		}
		return err
	})
}
