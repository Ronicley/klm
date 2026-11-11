package main

import (
	"database/sql"
	"net/http"
)

// Read pending identities and attribution only, never histories or question/permission bodies.
const sqlRequestAlerts = `WITH pending AS (
 SELECT 'permission:'||id AS id,owner_id,'permission' AS kind FROM data_pending_permissions
 UNION ALL SELECT 'question:'||id,owner_id,'question' FROM data_pending_questions
)
SELECT q.id,q.kind,coalesce(p.x_name,''),owner.x_title,
 CASE WHEN s.id<>owner.id THEN s.x_title ELSE '' END
FROM pending q
JOIN data_sessions s ON s.id=q.owner_id
LEFT JOIN data_delegation_invocations i ON i.id=(
 SELECT id FROM data_delegation_invocations WHERE x_childSessionId=s.id AND x_state<>'terminal' LIMIT 1
)
LEFT JOIN data_delegation_batches b ON b.id=i.x_batchId
LEFT JOIN data_graph_runs g ON g.id=s.x_graphRunId AND g.status IN ('starting','running','ending')
JOIN data_sessions owner ON owner.id=coalesce(b.x_ownerConversationId,g.x_conversationId,s.id)
LEFT JOIN data_sessions parent ON parent.id=owner.parent_id
LEFT JOIN data_projects p ON p.id=owner.project_id
LEFT JOIN data_topics t ON t.owner_id=owner.project_id AND t.record_id=CASE WHEN owner.role='side_agent' THEN parent.x_topicId ELSE owner.x_topicId END
WHERE q.id>? AND owner.role IN ('','general_agent','side_agent')
 AND s.x_archived<>'true' AND owner.x_archived<>'true'
 AND coalesce(parent.x_archived,'')<>'true' AND coalesce(t.x_archived,'')<>'true'
 AND (owner.role='general_agent' OR p.id IS NOT NULL AND p.x_removed<>'true')
 AND (s.x_graphRunId='' OR g.id IS NOT NULL AND EXISTS (
  SELECT 1 FROM data_graph_activations a WHERE a.session_id=s.id AND a.x_runId=g.id AND a.status IN ('reserved','running','waiting_user','sealing')
 ))
ORDER BY q.id LIMIT ?`

type sqlRequestAlert struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	ProjectName  string `json:"projectName"`
	SessionTitle string `json:"sessionTitle"`
	Origin       string `json:"origin"`
}

func (a *app) sqlRequestAlerts(w http.ResponseWriter, r *http.Request) {
	limit, err := sqlPageLimit(r, 100)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	after := r.URL.Query().Get("afterId")
	if len(after) > 4096 {
		fail(w, 400, "Invalid request alert cursor.")
		return
	}
	items := []sqlRequestAlert{}
	var engineID string
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(r.Context(), `SELECT value FROM store_meta WHERE key='database_id'`).Scan(&engineID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(r.Context(), sqlRequestAlerts, after, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item sqlRequestAlert
			if err := rows.Scan(&item.ID, &item.Kind, &item.ProjectName, &item.SessionTitle, &item.Origin); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	more, next := len(items) > limit, ""
	if more {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	respond(w, 200, map[string]any{"engineId": engineID, "items": items, "hasMore": more, "nextAfterId": next})
}
