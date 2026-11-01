package main

import (
	"database/sql"
	"net/http"
)

func (a *app) sqlSessionActivity(w http.ResponseWriter, r *http.Request) {
	limit, err := sqlPageLimit(r, 100)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	cursor := sqlPageCursor{Project: "activity"}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		if err := decodeSQLCursor(raw, &cursor); err != nil {
			sqlHTTPError(w, err)
			return
		}
		if cursor.Project != "activity" {
			fail(w, 400, "Invalid activity cursor scope.")
			return
		}
	}
	items := []any{}
	more := false
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		query := `SELECT s.id FROM data_sessions s JOIN data_projects p ON p.id=s.project_id LEFT JOIN data_topics t ON t.owner_id=s.project_id AND t.record_id=s.x_topicId WHERE p.x_removed<>'true' AND s.role='' AND s.parent_id='' AND s.x_archived<>'true' AND coalesce(t.x_archived,'')<>'true' AND (s.status IN ('running','error') OR EXISTS(SELECT 1 FROM runtime_activity r WHERE r.session_id=s.id AND r.active=1) OR EXISTS(SELECT 1 FROM data_pending_permissions q WHERE q.owner_id=s.id) OR EXISTS(SELECT 1 FROM data_pending_questions q WHERE q.owner_id=s.id))`
		args := []any{}
		if cursor.ID != "" {
			query += ` AND (s.created_at<? OR s.created_at=? AND s.id>?)`
			args = append(args, cursor.Created, cursor.Created, cursor.ID)
		}
		query += ` ORDER BY s.created_at DESC,s.id LIMIT ?`
		args = append(args, limit+1)
		ids, err := sqliteQueryIDs(st.ctx, tx, query, args...)
		if err != nil {
			return err
		}
		more = len(ids) > limit
		if more {
			ids = ids[:limit]
		}
		for _, id := range ids {
			s, err := st.sessionMeta(id)
			if err != nil {
				return err
			}
			var permissions, questions int
			var active bool
			var title string
			if err := tx.QueryRowContext(st.ctx, `SELECT (SELECT count(*) FROM data_pending_permissions WHERE owner_id=?),(SELECT count(*) FROM data_pending_questions WHERE owner_id=?),coalesce((SELECT active FROM runtime_activity WHERE session_id=?),0),coalesce((SELECT reasoning_title FROM runtime_activity WHERE session_id=?),'')`, id, id, id, id).Scan(&permissions, &questions, &active, &title); err != nil {
				return err
			}
			view := sessionSQLView(s, "")
			view.Topic, err = st.sessionTopicView(s)
			if err != nil {
				return err
			}
			items = append(items, struct {
				sqlSessionView
				RuntimeActive        bool   `json:"runtimeActive"`
				PermissionCount      int    `json:"permissionCount"`
				QuestionCount        int    `json:"questionCount"`
				LatestReasoningTitle string `json:"latestReasoningTitle"`
			}{view, active, permissions, questions, title})
			cursor.Created, cursor.ID = sqliteTimeKey(s.CreatedAt), id
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	next := ""
	if more {
		next = encodeSQLCursor(cursor)
	}
	respond(w, 200, map[string]any{"items": items, "hasMore": more, "nextCursor": next})
}
