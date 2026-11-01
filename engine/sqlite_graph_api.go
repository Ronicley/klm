package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

func (st *sqliteSessionTx) graphHTTPMain(id string) (Session, error) {
	s, err := st.sessionMeta(id)
	if err != nil {
		return s, err
	}
	if s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
		return s, sql.ErrNoRows
	}
	available, err := st.projectAvailable(s.ProjectID)
	if err != nil {
		return s, err
	}
	if !available {
		return s, sql.ErrNoRows
	}
	return s, nil
}
func (a *app) getSQLGraphRun(w http.ResponseWriter, r *http.Request) {
	var result any
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		run, err := st.graphRunRecord(r.PathValue("runID"))
		if err != nil {
			return err
		}
		if _, err := st.graphHTTPMain(run.ConversationID); err != nil {
			return err
		}
		result = map[string]any{"id": run.ID, "activityId": run.ActivityID, "conversationId": run.ConversationID, "graphId": run.GraphID, "status": run.Status, "active": graphRunActive(run.Status), "revision": sqliteRevision(run.Revision), "result": run.Result, "createdAt": run.CreatedAt, "endedAt": run.EndedAt}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, result)
}
func (a *app) getSQLConversationGraph(w http.ResponseWriter, r *http.Request) {
	offset, err := sqlOffset(r)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	result := map[string]any{"sessionId": r.PathValue("id"), "requests": []GraphRequestProjection{}, "run": nil, "requestsHasMore": false, "requestsNextOffset": offset}
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		s, err := st.graphHTTPMain(r.PathValue("id"))
		if err != nil {
			return err
		}
		result["selectedGraphId"] = s.SelectedGraphID
		var revision string
		if err := tx.QueryRowContext(st.ctx, `SELECT value FROM store_meta WHERE key='revision'`).Scan(&revision); err != nil {
			return err
		}
		result["revision"] = revision
		var id string
		err = tx.QueryRowContext(st.ctx, `SELECT id FROM data_graph_runs WHERE x_conversationId=? AND status IN ('starting','running','ending') LIMIT 1`, s.ID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		run, err := st.graphRun(s.ID, id)
		if err != nil {
			return err
		}
		var snapshot GraphSnapshot
		if err := decodeSQLiteRecord(st.ctx, tx, "graph_snapshots", sqliteOwnedKey(id, ""), maxStreamRecordBytes, &snapshot); err != nil {
			return err
		}
		active, err := sqliteQueryIDs(st.ctx, tx, `SELECT DISTINCT x_nodeId FROM data_graph_activations WHERE x_runId=? AND status IN ('reserved','running','waiting_user','sealing') ORDER BY x_nodeId`, id)
		if err != nil {
			return err
		}
		completed, err := sqliteQueryIDs(st.ctx, tx, `SELECT DISTINCT a.x_nodeId FROM data_graph_activations a WHERE a.x_runId=? AND a.status='completed' AND NOT EXISTS(SELECT 1 FROM data_graph_activations b WHERE b.x_runId=a.x_runId AND b.x_nodeId=a.x_nodeId AND b.status IN ('reserved','running','waiting_user','sealing')) AND NOT EXISTS(SELECT 1 FROM data_graph_join_rounds j WHERE j.x_runId=a.x_runId AND j.x_nodeId=a.x_nodeId AND j.status='collecting') ORDER BY a.x_nodeId`, id)
		if err != nil {
			return err
		}
		choices, err := sqliteQueryIDs(st.ctx, tx, `SELECT DISTINCT x_submission_choice_id FROM data_graph_activations WHERE x_runId=? AND status='completed' AND x_submission_choice_origin='graph' AND x_submission_acceptedAt<>'' ORDER BY x_submission_choice_id`, id)
		if err != nil {
			return err
		}
		collecting, err := sqliteQueryIDs(st.ctx, tx, `SELECT DISTINCT x_nodeId FROM data_graph_join_rounds WHERE x_runId=? AND status='collecting' ORDER BY x_nodeId`, id)
		if err != nil {
			return err
		}
		graphID := run.GraphID
		if run.CatalogGraphID != nil {
			graphID = *run.CatalogGraphID
		}
		result["run"] = map[string]any{"id": id, "graphId": graphID, "active": true, "status": run.Status, "revision": revision, "snapshot": snapshot.Graph, "activeNodeIds": active, "completedNodeIds": completed, "completedChoiceIds": choices, "collectingJoinIds": collecting}
		rows, err := tx.QueryContext(st.ctx, `SELECT kind,id,activation,node,session FROM (
		 SELECT 'permission' AS kind,p.id,a.id AS activation,a.x_nodeId AS node,a.session_id AS session,p.position FROM data_pending_permissions p JOIN data_graph_activations a ON a.session_id=p.owner_id WHERE a.x_runId=? AND a.status IN ('reserved','running','waiting_user','sealing')
		 UNION ALL SELECT 'question',p.id,a.id,a.x_nodeId,a.session_id,p.position FROM data_pending_questions p JOIN data_graph_activations a ON a.session_id=p.owner_id WHERE a.x_runId=? AND a.status IN ('reserved','running','waiting_user','sealing')
		 ) ORDER BY activation,kind,position,id LIMIT 51 OFFSET ?`, id, id, offset)
		if err != nil {
			return err
		}
		type ref struct{ kind, key, activation, node, session string }
		refs := []ref{}
		for rows.Next() {
			var ref ref
			if err := rows.Scan(&ref.kind, &ref.key, &ref.activation, &ref.node, &ref.session); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, ref)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		more := len(refs) > 50
		if more {
			refs = refs[:50]
		}
		items := []GraphRequestProjection{}
		bytes := 0
		for _, ref := range refs {
			item := GraphRequestProjection{RunID: id, GraphID: run.GraphID, NodeID: ref.node, NodeName: snapshot.Graph.Definition.Nodes[ref.node].Name, ActivationID: ref.activation, SessionID: ref.session, Kind: ref.kind}
			if item.NodeName == "" {
				item.NodeName = item.NodeID
			}
			if ref.kind == "permission" {
				var permission Permission
				if err := decodeSQLiteRecord(st.ctx, tx, "pending_permissions", ref.key, 2<<20, &permission); err != nil {
					return err
				}
				item.Permission, item.RequestID = &permission, permission.ID
			} else {
				var question QuestionRequest
				if err := decodeSQLiteRecord(st.ctx, tx, "pending_questions", ref.key, 2<<20, &question); err != nil {
					return err
				}
				item.Question, item.RequestID = &question, question.ID
			}
			encoded, _ := json.Marshal(item)
			if bytes+len(encoded) > 2<<20 && len(items) > 0 {
				more = true
				break
			}
			items = append(items, item)
			bytes += len(encoded)
		}
		result["requests"], result["requestsHasMore"], result["requestsNextOffset"] = items, more, offset+len(items)
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, result)
}
func (a *app) sqlGraphContentHTTP(w http.ResponseWriter, r *http.Request) {
	args := map[string]string{"domain": r.URL.Query().Get("domain"), "key": r.URL.Query().Get("key"), "digest": r.URL.Query().Get("digest"), "offset": r.URL.Query().Get("offset")}
	raw, _ := json.Marshal(args)
	value, err := a.readSQLGraphTool(r.Context(), r.PathValue("id"), "graph_record_content", raw)
	if errors.Is(err, errSQLiteContentChanged) {
		fail(w, 409, err.Error())
		return
	}
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, value)
}
func (a *app) getSQLGraphNodeActivity(w http.ResponseWriter, r *http.Request) {
	offset, err := sqlOffset(r)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	eventOffset := 0
	if raw := r.URL.Query().Get("eventOffset"); raw != "" {
		eventOffset, err = strconv.Atoi(raw)
		if err != nil || eventOffset < 0 {
			fail(w, 400, "Invalid event offset.")
			return
		}
	}
	result := map[string]any{}
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		s, err := st.graphHTTPMain(r.PathValue("id"))
		if err != nil {
			return err
		}
		run, err := st.graphRun(s.ID, r.PathValue("runId"))
		if err != nil {
			return err
		}
		var snapshot GraphSnapshot
		if err := decodeSQLiteRecord(st.ctx, tx, "graph_snapshots", sqliteOwnedKey(run.ID, ""), maxStreamRecordBytes, &snapshot); err != nil {
			return err
		}
		nodeID := r.PathValue("nodeId")
		node, ok := snapshot.Graph.Definition.Nodes[nodeID]
		if !ok || (node.Type != "agent" && node.Type != "join") {
			return sql.ErrNoRows
		}
		rows, err := tx.QueryContext(st.ctx, `SELECT id,x_occurrence,status FROM data_graph_activations WHERE x_runId=? AND x_nodeId=? ORDER BY length(x_occurrence),x_occurrence LIMIT 51 OFFSET ?`, run.ID, nodeID, offset)
		if err != nil {
			return err
		}
		items := []map[string]string{}
		more := false
		for rows.Next() {
			var id, occurrence, status string
			if err := rows.Scan(&id, &occurrence, &status); err != nil {
				rows.Close()
				return err
			}
			if len(items) == 50 {
				more = true
				break
			}
			items = append(items, map[string]string{"id": id, "occurrence": occurrence, "status": status})
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		result = map[string]any{"runActive": graphRunActive(run.Status), "activations": items, "activation": nil, "hasMore": more, "nextOffset": offset + len(items), "capabilities": snapshot.Capabilities[nodeID]}
		id := r.URL.Query().Get("activationId")
		if id == "" {
			err = tx.QueryRowContext(st.ctx, `SELECT id FROM data_graph_activations WHERE x_runId=? AND x_nodeId=? ORDER BY length(x_occurrence) DESC,x_occurrence DESC LIMIT 1`, run.ID, nodeID).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
		}
		x, err := st.graphActivation(id)
		if err != nil {
			return err
		}
		if x.RunID != run.ID || x.NodeID != nodeID {
			return sql.ErrNoRows
		}
		var eventCount int
		if err := tx.QueryRowContext(st.ctx, `SELECT (SELECT count(*) FROM data_activation_events WHERE owner_id=?)+(SELECT count(*) FROM data_session_events WHERE owner_id=? AND position>=? AND (? OR position<?))`, x.ID, x.SessionID, x.NativeEventStart, graphActivationActive(x.Status), x.NativeEventEnd).Scan(&eventCount); err != nil {
			return err
		}
		if r.URL.Query().Get("eventOffset") == "" {
			eventOffset = max(0, eventCount-50)
		}
		rows, err = tx.QueryContext(st.ctx, `SELECT domain,id FROM (
		 SELECT 'activation_events' AS domain,id,created_at FROM data_activation_events WHERE owner_id=?
		 UNION ALL SELECT 'session_events',id,created_at FROM data_session_events WHERE owner_id=? AND position>=? AND (? OR position<?)
		 ) ORDER BY created_at,domain,id LIMIT 51 OFFSET ?`, x.ID, x.SessionID, x.NativeEventStart, graphActivationActive(x.Status), x.NativeEventEnd, eventOffset)
		if err != nil {
			return err
		}
		type ref struct{ domain, key string }
		refs := []ref{}
		for rows.Next() {
			var ref ref
			if err := rows.Scan(&ref.domain, &ref.key); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, ref)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		eventMore := len(refs) > 50
		if eventMore {
			refs = refs[:50]
		}
		events := []any{}
		budget := int64(2 << 20)
		for _, ref := range refs {
			value, used, err := st.graphPageRecord(ref.domain, ref.key, budget)
			if err != nil {
				return err
			}
			events = append(events, value)
			budget -= used
		}
		result["activation"] = map[string]any{"id": x.ID, "occurrence": sqliteRevision(x.Occurrence), "status": x.Status, "input": x.Input, "submission": x.Submission, "events": events, "error": x.Error, "createdAt": x.CreatedAt, "updatedAt": x.UpdatedAt, "hasMore": eventMore, "nextOffset": eventOffset + len(events), "hasEarlier": eventOffset > 0, "previousOffset": max(0, eventOffset-50), "total": eventCount}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, result)
}
