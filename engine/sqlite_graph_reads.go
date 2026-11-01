package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

// One inspected collection/page is bounded before payload reads. Larger records
// retain lossless digest-bound continuation rather than being silently truncated.
func (st *sqliteSessionTx) graphPage(table, where string, values []any, offset, limit int) (any, error) {
	ids, err := sqliteQueryIDs(st.ctx, st.tx, `SELECT id FROM data_`+table+` WHERE `+where+` ORDER BY position,id LIMIT ? OFFSET ?`, append(append([]any{}, values...), limit+1, offset)...)
	if err != nil {
		return nil, err
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	items := []any{}
	remaining := int64(256 << 10)
	for _, id := range ids {
		value, used, err := st.graphPageRecord(table, id, remaining)
		if err != nil {
			return nil, err
		}
		items = append(items, value)
		remaining -= used
	}
	return map[string]any{"items": items, "hasMore": more, "nextOffset": offset + len(items)}, nil
}
func (st *sqliteSessionTx) graphPageRecord(table, id string, budget int64) (any, int64, error) {
	var size int64
	var digest, record string
	if err := st.tx.QueryRowContext(st.ctx, `SELECT payload_bytes,digest,record_id FROM data_`+table+` WHERE id=?`, id).Scan(&size, &digest, &record); err != nil {
		return nil, 0, err
	}
	if size > budget {
		return map[string]any{"id": record, "content": map[string]any{"domain": table, "key": id, "digest": digest, "bytes": strconv.FormatInt(size, 10)}}, 0, nil
	}
	var raw json.RawMessage
	if err := decodeSQLiteRecord(st.ctx, st.tx, table, id, budget, &raw); err != nil {
		return nil, 0, err
	}
	value, err := sqlGraphWire(raw)
	return value, size, err
}

func (st *sqliteSessionTx) graphContentOwned(owner, table, key string) error {
	s, err := st.sessionMeta(owner)
	if err != nil {
		return err
	}
	available, err := st.projectAvailable(s.ProjectID)
	if err != nil {
		return err
	}
	if !available {
		return sql.ErrNoRows
	}
	query := ""
	switch table {
	case "graph_activities":
		query = `SELECT x_conversationId FROM data_graph_activities WHERE id=?`
	case "graph_runs":
		query = `SELECT x_conversationId FROM data_graph_runs WHERE id=?`
	case "graph_authorization", "graph_dependencies", "graph_assessments", "graph_operations":
		query = `SELECT a.x_conversationId FROM data_` + table + ` c JOIN data_graph_activities a ON a.id=c.owner_id WHERE c.id=?`
	case "graph_activations", "graph_deliveries", "graph_join_rounds", "graph_workspace_uses":
		query = `SELECT r.x_conversationId FROM data_` + table + ` c JOIN data_graph_runs r ON r.id=c.x_runId WHERE c.id=?`
	case "graph_join_expected", "graph_join_arrivals":
		query = `SELECT r.x_conversationId FROM data_` + table + ` c JOIN data_graph_join_rounds j ON j.id=c.owner_id JOIN data_graph_runs r ON r.id=j.x_runId WHERE c.id=?`
	case "activation_events":
		query = `SELECT r.x_conversationId FROM data_activation_events e JOIN data_graph_activations a ON a.id=e.owner_id JOIN data_graph_runs r ON r.id=a.x_runId WHERE e.id=?`
	case "session_events":
		query = `SELECT r.x_conversationId FROM data_session_events e JOIN data_sessions s ON s.id=e.owner_id JOIN data_graph_runs r ON r.id=s.x_graphRunId WHERE e.id=?`
	case "graph_workspaces":
		var available bool
		err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_workspaces w WHERE w.id=? AND (EXISTS(SELECT 1 FROM data_graph_runs r WHERE r.id=w.x_createdByRunId AND r.x_conversationId=?) OR EXISTS(SELECT 1 FROM data_graph_workspace_uses u JOIN data_graph_runs r ON r.id=u.x_runId WHERE u.x_workspaceId=w.id AND r.x_conversationId=?)))`, key, owner, owner).Scan(&available)
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		return nil
	default:
		return errors.New("Unknown graph content domain.")
	}
	var actual string
	if err := st.tx.QueryRowContext(st.ctx, query, key).Scan(&actual); err != nil {
		return err
	}
	if actual != owner {
		return sql.ErrNoRows
	}
	return nil
}

func (a *app) readSQLGraphTool(ctx context.Context, owner, name string, raw json.RawMessage) (any, error) {
	var result any
	if name == "graph_record_content" {
		var args struct {
			Domain string `json:"domain"`
			Key    string `json:"key"`
			Digest string `json:"digest"`
			Offset string `json:"offset"`
		}
		if err := decodeGraphTool(raw, &args); err != nil {
			return nil, err
		}
		offset, err := strconv.ParseInt(args.Offset, 10, 64)
		if err != nil || offset < 0 {
			return nil, errors.New("Invalid content offset.")
		}
		err = a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
			st := &sqliteSessionTx{ctx: ctx, tx: tx}
			if err := st.graphContentOwned(owner, args.Domain, args.Key); err != nil {
				return err
			}
			page, err := readSQLiteContent(ctx, tx, args.Domain, args.Key, args.Digest, offset, 16<<10)
			if err != nil {
				return err
			}
			result = map[string]any{"data": page.Data, "digest": page.Digest, "offset": strconv.FormatInt(page.Offset, 10), "nextOffset": strconv.FormatInt(page.NextOffset, 10), "totalBytes": strconv.FormatInt(page.TotalBytes, 10), "hasMore": page.HasMore}
			return nil
		})
		return result, err
	}
	var args struct {
		ActivityID string `json:"activityId"`
		RunID      string `json:"runId"`
		NodeID     string `json:"nodeId"`
		RoundID    string `json:"roundId"`
		Collection string `json:"collection"`
		Offset     int    `json:"offset"`
		Limit      int    `json:"limit"`
	}
	if err := decodeGraphTool(raw, &args); err != nil {
		return nil, err
	}
	if args.Offset < 0 || args.Limit < 0 || args.Limit > 50 {
		return nil, errors.New("Invalid graph pagination bounds.")
	}
	if args.Limit == 0 {
		args.Limit = 20
	}
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		s, err := st.sessionMeta(owner)
		if err != nil {
			return err
		}
		available, err := st.projectAvailable(s.ProjectID)
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		if name == "graph_activities" {
			if args.ActivityID == "" {
				result, err = st.graphPage("graph_activities", `x_conversationId=?`, []any{owner}, args.Offset, args.Limit)
				return err
			}
			v, err := st.graphActivity(owner, args.ActivityID)
			if err != nil {
				return err
			}
			if args.Collection == "" {
				result, err = st.graphActivityResult(v)
				return err
			}
			table := map[string]string{"authorization": "graph_authorization", "dependencies": "graph_dependencies", "assessments": "graph_assessments", "operations": "graph_operations"}[args.Collection]
			if table == "" {
				return errors.New("Unknown activity collection.")
			}
			result, err = st.graphPage(table, `owner_id=?`, []any{v.ID}, args.Offset, args.Limit)
			return err
		}
		r, err := st.graphRun(owner, args.RunID)
		if err != nil {
			return err
		}
		if name == "graph_recent_events" {
			var captured GraphSnapshot
			if err := decodeSQLiteRecord(ctx, tx, "graph_snapshots", sqliteOwnedKey(r.ID, ""), maxStreamRecordBytes, &captured); err != nil {
				return err
			}
			if _, ok := captured.Graph.Definition.Nodes[args.NodeID]; !ok {
				return errors.New("Node not found in this run.")
			}
			rows, err := tx.QueryContext(ctx, `SELECT domain,id FROM (
			 SELECT 'activation_events' AS domain,e.id,e.created_at FROM data_activation_events e JOIN data_graph_activations a ON a.id=e.owner_id WHERE a.x_runId=? AND a.x_nodeId=?
			 UNION ALL SELECT 'session_events',e.id,e.created_at FROM data_session_events e WHERE EXISTS(SELECT 1 FROM data_graph_activations a WHERE a.x_runId=? AND a.x_nodeId=? AND a.session_id=e.owner_id AND e.position>=CAST(coalesce(nullif(a.x_nativeEventStart,''),'0') AS INTEGER) AND (a.status IN ('reserved','running','waiting_user','sealing') OR e.position<CAST(coalesce(nullif(a.x_nativeEventEnd,''),'0') AS INTEGER)))
			 ) ORDER BY created_at DESC,domain,id DESC LIMIT ? OFFSET ?`, r.ID, args.NodeID, r.ID, args.NodeID, args.Limit+1, args.Offset)
			if err != nil {
				return err
			}
			type ref struct{ table, id string }
			refs := []ref{}
			for rows.Next() {
				var item ref
				if err := rows.Scan(&item.table, &item.id); err != nil {
					rows.Close()
					return err
				}
				refs = append(refs, item)
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
			more := len(refs) > args.Limit
			if more {
				refs = refs[:args.Limit]
			}
			items := []any{}
			remaining := int64(256 << 10)
			for _, item := range refs {
				value, used, err := st.graphPageRecord(item.table, item.id, remaining)
				if err != nil {
					return err
				}
				items = append(items, value)
				remaining -= used
			}
			result = map[string]any{"items": items, "hasMore": more, "nextOffset": args.Offset + len(items)}
			return nil
		}
		if args.Collection == "" {
			result = map[string]any{"runId": r.ID, "activityId": r.ActivityID, "graphId": r.GraphID, "status": r.Status, "active": graphRunActive(r.Status), "revision": sqliteRevision(r.Revision), "task": r.Input.Task, "result": r.Result, "finalityError": r.FinalityError, "collections": []string{"activations", "deliveries", "joinRounds", "workspaces", "workspaceUses"}}
			return nil
		}
		table := map[string]string{"activations": "graph_activations", "deliveries": "graph_deliveries", "joinRounds": "graph_join_rounds", "workspaceUses": "graph_workspace_uses", "workspaces": "graph_workspaces", "joinExpected": "graph_join_expected", "joinArrivals": "graph_join_arrivals"}[args.Collection]
		if table == "" {
			return errors.New("Unknown run collection.")
		}
		where := `x_runId=?`
		values := []any{r.ID}
		if table == "graph_workspaces" {
			where = `x_createdByRunId=? OR EXISTS(SELECT 1 FROM data_graph_workspace_uses u WHERE u.x_workspaceId=data_graph_workspaces.id AND u.x_runId=?)`
			values = append(values, r.ID)
		}
		if args.Collection == "joinExpected" || args.Collection == "joinArrivals" {
			var found bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_join_rounds WHERE id=? AND x_runId=?)`, args.RoundID, r.ID).Scan(&found); err != nil {
				return err
			}
			if !found {
				return sql.ErrNoRows
			}
			where = `owner_id=?`
			values = []any{args.RoundID}
		}
		result, err = st.graphPage(table, where, values, args.Offset, args.Limit)
		return err
	})
	return result, err
}
