package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

const sqliteReadContract = "3"

type sqlProjectView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Folder string `json:"folder"`
	Icon   string `json:"icon"`
}

func projectSQLView(p Project) sqlProjectView { return sqlProjectView{p.ID, p.Name, p.Folder, p.Icon} }

type sqlSessionView struct {
	Topic                *sqlTopicView `json:"topic,omitempty"`
	RuntimeActive        bool          `json:"runtimeActive"`
	PermissionCount      int           `json:"permissionCount"`
	QuestionCount        int           `json:"questionCount"`
	LatestReasoningTitle string        `json:"latestReasoningTitle"`
	LastMessageAt        string        `json:"lastMessageAt,omitempty"`
	ID                   string        `json:"id"`
	ProjectID            string        `json:"projectId"`
	Title                string        `json:"title"`
	TopicID              string        `json:"topicId,omitempty"`
	Role                 string        `json:"role,omitempty"`
	ParentID             string        `json:"parentId,omitempty"`
	Harness              string        `json:"harness"`
	HarnessLocked        bool          `json:"harnessLocked"`
	Status               string        `json:"status"`
	Archived             bool          `json:"archived"`
	CreatedAt            string        `json:"createdAt"`
	UpdatedAt            string        `json:"updatedAt"`
	Revision             string        `json:"revision"`
}

func sessionSQLView(s Session, revision string) sqlSessionView {
	return sqlSessionView{ID: s.ID, ProjectID: s.ProjectID, Title: s.Title, TopicID: s.TopicID, Role: s.Role, ParentID: s.ParentID, Harness: s.Harness, HarnessLocked: s.HarnessLocked, Status: s.Status, Archived: s.Archived, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Revision: revision}
}

func (a *app) getSQLState(w http.ResponseWriter, r *http.Request) {
	projects := []sqlProjectView{}
	var general *sqlSessionView
	var revision string
	var engineID string
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(r.Context(), `SELECT value FROM store_meta WHERE key='database_id'`).Scan(&engineID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(r.Context(), `SELECT value FROM store_meta WHERE key='revision'`).Scan(&revision); err != nil {
			return err
		}
		var after int64 = -1
		for {
			var id string
			var position int64
			err := tx.QueryRowContext(r.Context(), `SELECT id,position FROM data_projects WHERE x_removed<>'true' AND position>? ORDER BY position LIMIT 1`, after).Scan(&id, &position)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return err
			}
			var p Project
			if err := decodeSQLiteRecord(r.Context(), tx, "projects", id, 1<<20, &p); err != nil {
				return err
			}
			projects = append(projects, projectSQLView(p))
			after = position
		}
		var id string
		err := tx.QueryRowContext(r.Context(), `SELECT id FROM data_sessions WHERE role='general_agent' LIMIT 1`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var s Session
		if err := decodeSQLiteRecord(r.Context(), tx, "sessions", id, 1<<20, &s); err != nil {
			return err
		}
		view := sessionSQLView(s, revision)
		if err := (&sqliteSessionTx{ctx: r.Context(), tx: tx}).sessionActivityView(&view); err != nil {
			return err
		}
		general = &view
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, struct {
		Contract  string           `json:"contract"`
		EngineID  string           `json:"engineId"`
		Revision  string           `json:"revision"`
		Projects  []sqlProjectView `json:"projects"`
		Harnesses []Harness        `json:"harnesses"`
		General   *sqlSessionView  `json:"general,omitempty"`
	}{sqliteReadContract, engineID, revision, projects, a.harnessSnapshot(), general})
}

type sqlPageCursor struct {
	Project, Topic, Search, Created, ID string
	Revision                            string
	HasTopic                            bool
	Archived                            bool
	AllArchived                         bool
	ActiveOnly                          bool
	Position                            int64
}

func encodeSQLCursor(c sqlPageCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeSQLCursor(raw string, c *sqlPageCursor) error {
	if len(raw) > 4096 {
		return controlError(400, "Invalid page cursor.")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return controlError(400, "Invalid page cursor.")
	}
	if json.Unmarshal(b, c) != nil {
		return controlError(400, "Invalid page cursor.")
	}
	return nil
}
func sqlPageLimit(r *http.Request, max int) (int, error) {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return min(100, max), nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > max {
		return 0, controlError(400, "Invalid page limit.")
	}
	return n, nil
}

func (a *app) projectSQLSessions(w http.ResponseWriter, r *http.Request) {
	limit, err := sqlPageLimit(r, 200)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	q := r.URL.Query()
	scope := sqlPageCursor{Project: r.PathValue("id"), Topic: q.Get("topicId"), HasTopic: q.Has("topicId"), Search: strings.TrimSpace(q.Get("search")), Archived: q.Get("archived") == "true", AllArchived: q.Get("archived") == "all"}
	if len(scope.Search) > 800 {
		fail(w, 400, "Session search is too long.")
		return
	}
	cursor := scope
	if raw := q.Get("cursor"); raw != "" {
		if err := decodeSQLCursor(raw, &cursor); err != nil {
			sqlHTTPError(w, err)
			return
		}
		if cursor.Project != scope.Project || cursor.Topic != scope.Topic || cursor.HasTopic != scope.HasTopic || cursor.Search != scope.Search || cursor.Archived != scope.Archived || cursor.AllArchived != scope.AllArchived {
			fail(w, 400, "Page cursor belongs to a different scope.")
			return
		}
	}
	items := []sqlSessionView{}
	hasMore := false
	next := ""
	var revision string
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		available, err := st.projectAvailable(scope.Project)
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		if err := tx.QueryRowContext(r.Context(), `SELECT revision FROM data_projects WHERE id=?`, scope.Project).Scan(&revision); err != nil {
			return err
		}
		if cursor.ID != "" && cursor.Revision != revision {
			return controlError(409, "Project list changed. Reload this page.")
		}
		cursor.Revision = revision
		query := `SELECT s.id,s.created_at,s.revision FROM data_sessions s LEFT JOIN data_topics t ON t.owner_id=s.project_id AND t.record_id=s.x_topicId WHERE s.project_id=? AND s.role='' AND s.parent_id='' AND s.x_graphRunId=''`
		args := []any{scope.Project}
		if scope.Archived {
			query += ` AND (s.x_archived='true' OR t.x_archived='true')`
		} else if !scope.AllArchived {
			query += ` AND s.x_archived<>'true' AND coalesce(t.x_archived,'')<>'true'`
		}
		if scope.HasTopic {
			query += ` AND s.x_topicId=?`
			args = append(args, scope.Topic)
		}
		if scope.Search != "" {
			query += ` AND (instr(klm_lower(s.x_title),klm_lower(?))>0 OR instr(klm_lower(coalesce(t.x_name,'')),klm_lower(?))>0)`
			args = append(args, scope.Search, scope.Search)
		}
		if cursor.ID != "" {
			query += ` AND (s.created_at<? OR s.created_at=? AND s.id>?)`
			args = append(args, cursor.Created, cursor.Created, cursor.ID)
		}
		query += ` ORDER BY s.created_at DESC,s.id LIMIT ?`
		args = append(args, limit+1)
		rows, err := tx.QueryContext(r.Context(), query, args...)
		if err != nil {
			return err
		}
		type ref struct{ id, created, revision string }
		refs := []ref{}
		for rows.Next() {
			var v ref
			if err := rows.Scan(&v.id, &v.created, &v.revision); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, v)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		hasMore = len(refs) > limit
		if hasMore {
			refs = refs[:limit]
		}
		for _, ref := range refs {
			var s Session
			if err := decodeSQLiteRecord(r.Context(), tx, "sessions", ref.id, 1<<20, &s); err != nil {
				return err
			}
			view := sessionSQLView(s, ref.revision)
			if err := st.sessionActivityView(&view); err != nil {
				return err
			}
			view.Topic, err = st.sessionTopicView(s)
			if err != nil {
				return err
			}
			if err := tx.QueryRowContext(r.Context(), `SELECT coalesce((SELECT message_at FROM data_session_events WHERE owner_id=? AND message_at<>'' ORDER BY position DESC LIMIT 1),'')`, s.ID).Scan(&view.LastMessageAt); err != nil {
				return err
			}
			items = append(items, view)
			cursor.Created, cursor.ID = ref.created, ref.id
		}
		if hasMore {
			next = encodeSQLCursor(cursor)
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "hasMore": hasMore, "nextCursor": next, "revision": revision})
}

func (a *app) projectSQLTopics(w http.ResponseWriter, r *http.Request) {
	limit, err := sqlPageLimit(r, 200)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	activeOnly := r.URL.Query().Get("active") == "true"
	if len(search) > 800 {
		fail(w, 400, "Topic search is too long.")
		return
	}
	cursor := sqlPageCursor{Project: r.PathValue("id"), Position: -1, Search: search, ActiveOnly: activeOnly}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		if err := decodeSQLCursor(raw, &cursor); err != nil {
			sqlHTTPError(w, err)
			return
		}
		if cursor.Project != r.PathValue("id") || cursor.Search != search || cursor.ActiveOnly != activeOnly {
			fail(w, 400, "Page cursor belongs to a different project.")
			return
		}
	}
	items := []sqlTopicView{}
	more := false
	next := ""
	var revision string
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		ok, err := st.projectAvailable(cursor.Project)
		if err != nil {
			return err
		}
		if !ok {
			return sql.ErrNoRows
		}
		if err := tx.QueryRowContext(r.Context(), `SELECT revision FROM data_projects WHERE id=?`, cursor.Project).Scan(&revision); err != nil {
			return err
		}
		if cursor.Position >= 0 && cursor.Revision != revision {
			return controlError(409, "Topic order changed. Reload this page.")
		}
		cursor.Revision = revision
		rows, err := tx.QueryContext(r.Context(), `WITH ordered(id,position) AS (`+sqlTopicOrder+`) SELECT o.id,o.position,t.x_name FROM ordered o JOIN data_topics t ON t.owner_id=? AND t.record_id=o.id WHERE o.position>? AND (?=0 OR t.x_archived<>'true') ORDER BY o.position`, cursor.Project, "", cursor.Project, cursor.Position, activeOnly)
		if err != nil {
			return err
		}
		type ref struct {
			id       string
			position int64
		}
		refs := []ref{}
		for rows.Next() {
			var v ref
			var name string
			if err := rows.Scan(&v.id, &v.position, &name); err != nil {
				rows.Close()
				return err
			}
			if search != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(search)) {
				continue
			}
			refs = append(refs, v)
			if len(refs) > limit {
				break
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		more = len(refs) > limit
		if more {
			refs = refs[:limit]
		}
		for _, ref := range refs {
			t, _, err := st.topic(cursor.Project, ref.id)
			if err != nil {
				return err
			}
			items = append(items, sqlTopicView{Topic: t, Order: ref.position, Revision: revision})
			cursor.Position = ref.position
		}
		if more {
			next = encodeSQLCursor(cursor)
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "hasMore": more, "nextCursor": next, "revision": revision})
}

func (a *app) respondSQLSession(w http.ResponseWriter, r *http.Request, id string, status int) {
	var s Session
	var delegation *DelegationSessionStatus
	var topic *sqlTopicView
	var revision, count string
	more := map[string]bool{}
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		if err := decodeSQLiteRecord(r.Context(), tx, "sessions", id, 1<<20, &s); err != nil {
			return err
		}
		if s.ProjectID != "" {
			st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
			ok, err := st.projectAvailable(s.ProjectID)
			if err != nil {
				return err
			}
			if !ok {
				return sql.ErrNoRows
			}
		}
		if err := tx.QueryRowContext(r.Context(), `SELECT revision FROM data_sessions WHERE id=?`, id).Scan(&revision); err != nil {
			return err
		}
		if err := tx.QueryRowContext(r.Context(), `SELECT CAST(size AS TEXT) FROM record_collections WHERE parent_table='sessions' AND parent_id=? AND field='events'`, id).Scan(&count); err != nil {
			return err
		}
		var err error
		topic, err = (&sqliteSessionTx{ctx: r.Context(), tx: tx}).sessionTopicView(s)
		if err != nil {
			return err
		}
		if topic != nil && s.Role == sessionRoleSideAgent {
			s.TopicID = topic.ID
		}
		s.Queue, more["queue"], err = readSQLChildren[QueuedMessage](r.Context(), tx, "queued_messages", id, 32, 4<<20)
		if err != nil {
			return err
		}
		s.Permissions, more["permissions"], err = readSQLChildren[Permission](r.Context(), tx, "pending_permissions", id, 100, 2<<20)
		if err != nil {
			return err
		}
		s.Questions, more["questions"], err = readSQLChildren[QuestionRequest](r.Context(), tx, "pending_questions", id, 100, 2<<20)
		if err != nil {
			return err
		}
		capKey := sqliteOwnedKey(id, "")
		if err := decodeSQLiteRecord(r.Context(), tx, "session_capabilities", capKey, 64<<10, &s.Capabilities); err != nil {
			return err
		}
		if err := decodeSQLiteRecord(r.Context(), tx, "capability_selections", sqliteOwnedKey(capKey, ""), 8<<20, &s.Capabilities.Desired); err != nil {
			return err
		}
		if err := readSQLCapabilityView(r.Context(), tx, "capability_applied", sqliteOwnedKey(capKey, ""), &s.Capabilities.Applied); err != nil {
			return err
		}
		if err := decodeSQLiteRecord(r.Context(), tx, "session_usage", sqliteOwnedKey(id, ""), 64<<10, &s.Usage); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		delegation, err = (&sqliteSessionTx{ctx: r.Context(), tx: tx}).delegationSessionStatus(s)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.mu.Lock()
	if t := a.runs[id]; t != nil && t.delegation != nil && t.delegation.preparing != nil && delegation != nil {
		delegation.Waiting = true
		delegation.Actions.Steer, delegation.Actions.Settings = false, false
	}
	if runtime := a.runtimes[id]; runtime != nil {
		s.RuntimeActive = runtime.active()
	}
	a.mu.Unlock()
	wire := struct {
		Session
		Capabilities any                      `json:"capabilities"`
		Topic        *sqlTopicView            `json:"topic,omitempty"`
		Delegation   *DelegationSessionStatus `json:"delegation,omitempty"`
	}{s, map[string]any{"revision": sqliteRevision(s.Capabilities.Revision), "desired": s.Capabilities.Desired, "applied": s.Capabilities.Applied, "preparing": s.Capabilities.Preparing}, topic, delegation}
	respond(w, status, map[string]any{"session": wire, "revision": revision, "eventCount": count, "more": more})
}

func readSQLCapabilityView(ctx context.Context, q sqliteQuery, domain, id string, target **CapabilitySnapshot) error {
	var body []byte
	err := q.QueryRowContext(ctx, `SELECT payload FROM capability_views WHERE domain=? AND record_key=?`, domain, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func readSQLChildren[T any](ctx context.Context, q sqliteQuery, table, owner string, limit int, budget int64) ([]T, bool, error) {
	if !sqliteKnownTable(table) {
		return nil, false, errors.New("unknown SQL collection")
	}
	rows, err := q.QueryContext(ctx, `SELECT id,payload_bytes FROM data_`+table+` WHERE owner_id=? ORDER BY position LIMIT ?`, owner, limit+1)
	if err != nil {
		return nil, false, err
	}
	type ref struct {
		id   string
		size int64
	}
	refs := []ref{}
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.id, &r.size); err != nil {
			rows.Close()
			return nil, false, err
		}
		refs = append(refs, r)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, false, err
	}
	if len(refs) > 0 && refs[0].size > budget {
		return nil, false, controlError(413, "Pending request exceeds the response size limit.")
	}
	result := []T{}
	more := len(refs) > limit
	if more {
		refs = refs[:limit]
	}
	for _, ref := range refs {
		if ref.size > budget {
			more = true
			break
		}
		var value T
		if err := decodeSQLiteRecord(ctx, q, table, ref.id, budget, &value); err != nil {
			return nil, false, err
		}
		result = append(result, value)
		budget -= ref.size
	}
	return result, more, nil
}

func (a *app) getSQLSession(w http.ResponseWriter, r *http.Request) {
	a.respondSQLSession(w, r, r.PathValue("id"), 200)
}

func (a *app) sqlPendingPage(w http.ResponseWriter, r *http.Request) {
	tables := map[string]string{"queue": "queued_messages", "permissions": "pending_permissions", "questions": "pending_questions"}
	table := tables[r.URL.Query().Get("kind")]
	if table == "" {
		fail(w, 400, "Choose queue, permissions or questions.")
		return
	}
	items := []json.RawMessage{}
	next := ""
	more := false
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		var after int64 = -1
		if id := r.URL.Query().Get("afterId"); id != "" {
			if err := tx.QueryRowContext(r.Context(), `SELECT position FROM data_`+table+` WHERE owner_id=? AND record_id=? ORDER BY position DESC LIMIT 1`, r.PathValue("id"), id).Scan(&after); err != nil {
				return controlError(409, "Pending requests changed. Reload this conversation.")
			}
		}
		rows, err := tx.QueryContext(r.Context(), `SELECT id,record_id,payload_bytes FROM data_`+table+` WHERE owner_id=? AND position>? ORDER BY position LIMIT 101`, r.PathValue("id"), after)
		if err != nil {
			return err
		}
		type ref struct {
			key, id string
			size    int64
		}
		refs := []ref{}
		for rows.Next() {
			var x ref
			if err := rows.Scan(&x.key, &x.id, &x.size); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, x)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		remaining := int64(4 << 20)
		if len(refs) > 0 && refs[0].size > remaining {
			return controlError(413, "Pending request exceeds the response size limit.")
		}
		for i, ref := range refs {
			if i == 100 || ref.size > remaining {
				more = true
				break
			}
			var value json.RawMessage
			if err := decodeSQLiteRecord(r.Context(), tx, table, ref.key, remaining, &value); err != nil {
				return err
			}
			items = append(items, value)
			next = ref.id
			remaining -= ref.size
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "hasMore": more, "nextAfterId": next})
}
func (a *app) sqlHistory(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 50 {
			fail(w, 400, "Invalid history page limit.")
			return
		}
	}
	page, err := a.store.readHistory(r.Context(), r.PathValue("id"), r.URL.Query().Get("before"), r.URL.Query().Get("after"), limit)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, page)
}

func (a *app) sqlEventContent(w http.ResponseWriter, r *http.Request) {
	ordinal, err := strconv.ParseInt(r.URL.Query().Get("ordinal"), 10, 64)
	if err != nil || ordinal < 0 {
		fail(w, 400, "Invalid event ordinal.")
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		fail(w, 400, "Invalid content offset.")
		return
	}
	var page sqliteContentPage
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		var err error
		page, err = readSQLiteContent(r.Context(), tx, "session_events", sqliteOwnedKey(r.PathValue("id"), strconv.FormatInt(ordinal, 10)), r.URL.Query().Get("digest"), offset, sqliteContentChunkBytes)
		return err
	})
	if errors.Is(err, errSQLiteContentChanged) {
		fail(w, 409, err.Error())
		return
	}
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, page)
}

// Reuses paired-client protection and the existing routes. Until each remaining
// handler is converted, SQL ownership never falls through into the JSON backend.
func sqlRouteAvailable(pattern string) bool {
	switch pattern {
	case "POST /api/sessions/{id}/content-downloads", "POST /api/sessions/{id}/export-downloads", "GET /api/content-downloads/{ticket}":
		return true
	case "GET /api/analytics":
		return true
	case "GET /api/transcription/settings", "PATCH /api/transcription/settings", "POST /api/transcriptions", "PATCH /api/sessions/{id}/events/{eventID}", "GET /api/sessions/{id}/events":
		return true
	case "GET /api/activity-board/config", "PATCH /api/activity-board/config", "POST /api/activity-board/discover", "POST /api/activity-board/connections", "PATCH /api/activity-board/connections/{id}", "DELETE /api/activity-board/connections/{id}", "GET /api/activity-board/items", "GET /api/activity-board/sprints", "POST /api/activity-board/refresh", "POST /api/activity-board/items/{key}/phase", "POST /api/activity-board/items/{key}/phase/cancel", "GET /api/activity-board/moves/{id}":
		return true
	case "GET /api/activity/github", "GET /api/activity/github/catalog", "GET /api/activity/github/bookmarks", "GET /api/activity/github/{eventId}":
		return true
	case "GET /api/sessions/{id}/export":
		return true
	case "POST /api/projects/{id}/agents", "PATCH /api/projects/{id}/agents/{item}", "DELETE /api/projects/{id}/agents/{item}", "POST /api/projects/{id}/graph-drafts", "POST /api/projects/{id}/graphs/{item}", "PATCH /api/projects/{id}/graphs/{item}", "DELETE /api/projects/{id}/graphs/{item}", "POST /api/projects/{id}/graphs/{item}/layout":
		return true
	case "GET /api/projects/{id}/webhooks", "POST /api/projects/{id}/webhooks", "PATCH /api/projects/{id}/webhooks/{webhookId}", "DELETE /api/projects/{id}/webhooks/{webhookId}", "GET /api/projects/{id}/webhooks/{webhookId}/events", "GET /api/projects/{id}/webhooks/{webhookId}/events/{eventId}", "GET /api/projects/{id}/webhook-events", "POST /api/webhooks/github/{webhookId}":
		return true
	case "GET /api/library/{kind}/{itemID}/deletion", "DELETE /api/library/{kind}/{itemID}":
		return true
	case "GET /api/library", "GET /api/library/mcp/{connectionID}", "POST /api/library/mcp", "PATCH /api/library/mcp/{connectionID}", "POST /api/library/mcp/{connectionID}/test", "POST /api/library/mcp/{connectionID}/oauth", "POST /api/library/mcp/{connectionID}/disconnect", "GET /api/library/mcp/oauth/callback":
		return true
	case "GET /api/sessions/{id}/graph", "PATCH /api/sessions/{id}/graph", "GET /api/sessions/{id}/graph/runs/{runId}/nodes/{nodeId}/activity", "GET /api/graph-runs/{runID}", "GET /api/sessions/{id}/graph/content":
		return true
	case "GET /api/filesystem/roots", "GET /api/filesystem/directories", "POST /api/filesystem/directories", "PATCH /api/filesystem/directories", "DELETE /api/filesystem/directories":
		return true
	case "GET /api/projects/{id}/automations", "GET /api/projects/{id}/automations/{automationId}", "POST /api/projects/{id}/automations", "PATCH /api/projects/{id}/automations/{automationId}", "DELETE /api/projects/{id}/automations/{automationId}", "GET /api/projects/{id}/automations/{automationId}/runs", "POST /api/projects/{id}/automations/{automationId}/run-now", "POST /api/projects/{id}/automations/preview", "GET /api/settings/engine", "PATCH /api/settings/engine":
		return true
	case "GET /api/sessions/{id}/capabilities", "PATCH /api/sessions/{id}/capabilities":
		return true
	case "POST /api/sessions/{id}/questions/{questionID}/elicitation":
		return true
	case "GET /api/permission-rules", "DELETE /api/permission-rules/{ruleID}":
		return true
	case "PATCH /api/sessions/{id}/settings":
		return true
	case "POST /api/sessions/{id}/messages", "POST /api/sessions/{id}/stop", "POST /api/sessions/{id}/queue/{messageID}/send", "DELETE /api/sessions/{id}/queue/{messageID}", "POST /api/sessions/{id}/permissions/{permissionID}", "POST /api/sessions/{id}/questions/{questionID}/reply", "PATCH /api/sessions/{id}/permissions":
		return true
	case "POST /api/library/recover":
		return true
	case "GET /api/sessions/{id}/pending":
		return true
	case "GET /api/projects/{id}/paths", "GET /api/projects/{id}/authoring", "GET /api/projects/{id}/models/{harness}", "GET /api/sessions/{id}/models", "GET /api/sessions/{id}/metadata", "GET /api/sessions/{id}/quota", "POST /api/sessions/{id}/side", "PATCH /api/sessions/{id}/harness":
		return true
	case "GET /api/updates":
		return true
	case "GET /api/health", "GET /api/version", "GET /api/state", "GET /api/auth/session", "POST /api/auth/pair", "GET /api/auth/devices", "DELETE /api/auth/devices/{clientId}", "DELETE /api/auth/devices",
		"GET /api/settings/access", "PATCH /api/settings/access", "POST /api/engine/restart", "POST /api/dialogs/directory",
		"POST /api/projects", "PATCH /api/projects/{id}", "DELETE /api/projects/{id}", "GET /api/projects/{id}/sessions", "GET /api/projects/{id}/topics", "POST /api/projects/{id}/topics", "PATCH /api/projects/{id}/topics/{topicId}",
		"POST /api/sessions", "GET /api/sessions/{id}", "PATCH /api/sessions/{id}", "GET /api/sessions/{id}/history", "GET /api/sessions/{id}/content", "POST /api/general-session":
		return true
	}
	return false
}

func (a *app) sqlHealth(ctx context.Context) (int, error) {
	var n int
	err := a.store.readers.QueryRowContext(ctx, `SELECT count(*) FROM data_graph_runs WHERE status IN ('starting','running','ending')`).Scan(&n)
	return n, err
}
