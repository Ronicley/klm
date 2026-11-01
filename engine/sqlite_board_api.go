package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (a *app) sqlActivityBoardRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/activity-board/config", a.sqlBoardGetConfig)
	m.HandleFunc("PATCH /api/activity-board/config", a.sqlBoardPatchConfig)
	m.HandleFunc("POST /api/activity-board/discover", a.boardDiscover)
	m.HandleFunc("POST /api/activity-board/connections", a.sqlBoardSaveConnection)
	m.HandleFunc("PATCH /api/activity-board/connections/{id}", a.sqlBoardSaveConnection)
	m.HandleFunc("DELETE /api/activity-board/connections/{id}", a.sqlBoardDeleteConnection)
	m.HandleFunc("GET /api/activity-board/items", a.sqlBoardItems)
	m.HandleFunc("GET /api/activity-board/sprints", a.sqlBoardSprints)
	m.HandleFunc("POST /api/activity-board/refresh", a.sqlBoardRefresh)
	m.HandleFunc("POST /api/activity-board/items/{key}/phase", a.sqlBoardMove)
	m.HandleFunc("POST /api/activity-board/items/{key}/phase/cancel", a.sqlBoardMove)
	m.HandleFunc("GET /api/activity-board/moves/{id}", a.sqlBoardGetMove)
}
func (a *app) sqlBoardGetConfig(w http.ResponseWriter, r *http.Request) {
	a.respondSQLBoardConfig(w, r, 200)
}
func (a *app) respondSQLBoardConfig(w http.ResponseWriter, r *http.Request, status int) {
	offset, err := sqlOffset(r)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	var result any
	a.mu.Lock()
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		config, err := st.boardConfig()
		if err != nil {
			return err
		}
		connections := []any{}
		if err := st.eachMatching("board_connections", "1", nil, func(id string) error {
			c, err := st.boardConnection(id)
			if err != nil {
				return err
			}
			sync, err := st.boardSync(id)
			if err != nil {
				return err
			}
			available, err := st.projectAvailable(c.ProjectID)
			if err != nil {
				return err
			}
			var credential bool
			if err := tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_board_credentials WHERE id=?)`, id).Scan(&credential); err != nil {
				return err
			}
			connections = append(connections, struct {
				activityBoardConnection
				CredentialConfigured bool              `json:"credentialConfigured"`
				Available            bool              `json:"available"`
				Sync                 activityBoardSync `json:"sync"`
				Syncing              bool              `json:"syncing"`
			}{c, credential, available, sync, a.activityBoardBusy[id]})
			return nil
		}); err != nil {
			return err
		}
		ids, err := sqliteQueryIDs(st.ctx, tx, `SELECT id FROM data_board_moves WHERE status IN ('pending','unconfirmed') ORDER BY id LIMIT 101 OFFSET ?`, offset)
		if err != nil {
			return err
		}
		more := len(ids) > 100
		if more {
			ids = ids[:100]
		}
		moves := []activityBoardMove{}
		for _, id := range ids {
			var move activityBoardMove
			if err := decodeSQLiteRecord(st.ctx, tx, "board_moves", id, 64<<10, &move); err != nil {
				return err
			}
			moves = append(moves, move)
		}
		result = map[string]any{"revision": sqliteRevision(config.Revision), "projectionRevision": sqliteRevision(config.Projection), "phases": config.Phases, "connections": connections, "unresolvedMoves": moves, "movesHasMore": more, "movesNextOffset": offset + len(moves)}
		return nil
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, status, result)
}
func (a *app) sqlBoardPatchConfig(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(100 * time.Second))
	var in struct {
		ExpectedRevision string                          `json:"expectedRevision"`
		PhaseID          string                          `json:"phaseId"`
		Name             string                          `json:"name"`
		Mappings         map[string][]activityBoardValue `json:"mappings"`
	}
	if !decode(w, r, &in) {
		return
	}
	revision, err := boardRevision(in.ExpectedRevision)
	name, valid := cleanLabel(in.Name, 100)
	if err != nil || !valid {
		boardFail(w, 400, "A phase name and valid revision are required.")
		return
	}
	var config activityBoardConfig
	connections := []activityBoardConnection{}
	tokens := map[string][]byte{}
	a.mu.Lock()
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		if err := a.sqlBoardCheck(st, revision, false); err != nil {
			return err
		}
		var err error
		config, err = st.boardConfig()
		if err != nil {
			return err
		}
		return st.eachMatching("board_connections", "1", nil, func(id string) error {
			c, err := st.boardConnection(id)
			if err != nil {
				return err
			}
			var secret []byte
			if err := decodeSQLiteRecord(st.ctx, tx, "board_credentials", id, 1<<20, &secret); err != nil {
				return err
			}
			connections = append(connections, c)
			tokens[id] = secret
			return nil
		})
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	id := in.PhaseID
	if id == "" {
		id = newID()
		config.Phases = append(config.Phases, activityBoardPhase{ID: id, Name: name})
		if len(in.Mappings) != len(connections) {
			boardFail(w, 400, "Map the new phase in every board connection.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		for i, c := range connections {
			token, err := boardCredential(tokens[c.ID], false)
			if err != nil {
				boardFail(w, 400, err.Error())
				return
			}
			resolved, catalog, err := boardDiscover(ctx, c, string(token))
			if err != nil {
				boardFail(w, 502, err.Error())
				return
			}
			resolved.Mappings = cloneTransactionValueOfMappings(c.Mappings)
			resolved.Mappings[id] = in.Mappings[c.ID]
			if err := boardResolveMappings(config.Phases, resolved.Mappings, catalog); err != nil {
				boardFail(w, 400, err.Error())
				return
			}
			connections[i] = resolved
		}
	} else {
		found := false
		for i := range config.Phases {
			if config.Phases[i].ID == id {
				config.Phases[i].Name = name
				found = true
			}
		}
		if !found {
			boardFail(w, 404, "Phase not found.")
			return
		}
	}
	a.mu.Lock()
	err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		if err := a.sqlBoardCheck(st, revision, false); err != nil {
			return err
		}
		if err := st.clearChildren("board_phases", "settings"); err != nil {
			return err
		}
		for _, phase := range config.Phases {
			if err := st.putRecord("board_phases", sqliteOwnedKey("settings", phase.ID), "settings", phase.ID, -1, phase); err != nil {
				return err
			}
		}
		for _, c := range connections {
			if err := validateActivityBoardMappings(config.Phases, c.Mappings); err != nil {
				return err
			}
			if err := st.putRoot("board_connections", c.ID, c); err != nil {
				return err
			}
			if err := st.eachMatching("board_items", `x_connectionId=?`, []any{c.ID}, func(key string) error {
				var item activityBoardItem
				if err := decodeSQLiteRecord(st.ctx, st.tx, "board_items", key, 2<<20, &item); err != nil {
					return err
				}
				return indexSQLBoardItem(st.ctx, st.tx, item, c)
			}); err != nil {
				return err
			}
		}
		return st.boardProjection(true)
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.respondSQLBoardConfig(w, r, 200)
}
func (a *app) sqlBoardSaveConnection(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(100 * time.Second))
	var in boardConnectionInput
	if !decode(w, r, &in) {
		return
	}
	revision, err := boardRevision(in.ExpectedRevision)
	name, valid := cleanLabel(in.Name, 100)
	if err != nil || !valid {
		boardFail(w, 400, "Connection name and revision are required.")
		return
	}
	in.ConnectionID = r.PathValue("id")
	credentialOnly := false
	var phases []activityBoardPhase
	a.mu.Lock()
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		if in.ConnectionID != "" {
			c, err := st.boardConnection(in.ConnectionID)
			if err != nil {
				return err
			}
			credentialOnly = boardSameMappingIDs(in.Mappings, c.Mappings)
		}
		if err := a.sqlBoardCheck(st, revision, credentialOnly); err != nil {
			return err
		}
		available, err := st.projectAvailable(in.ProjectID)
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		config, err := st.boardConfig()
		phases = config.Phases
		return err
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	c, token, err := a.sqlBoardInputConnection(in, false)
	if err != nil {
		boardFail(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	c, catalog, err := boardDiscover(ctx, c, token)
	if err != nil {
		boardFail(w, 502, err.Error())
		return
	}
	c.Name, c.Mappings, c.OnlyAssignedToMe = name, in.Mappings, c.Provider == "azure"
	if in.ConnectionID != "" && in.ProjectID != c.ProjectID {
		boardFail(w, 400, "Scope and local project are fixed. Create another connection to change them.")
		return
	}
	if err := boardResolveMappings(phases, c.Mappings, catalog); err != nil {
		boardFail(w, 400, err.Error())
		return
	}
	protected, err := boardCredential([]byte(token), true)
	if err != nil {
		boardFail(w, 400, err.Error())
		return
	}
	if c.ID == "" {
		c.ID = newID()
	}
	a.mu.Lock()
	err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		if err := a.sqlBoardCheck(st, revision, credentialOnly); err != nil {
			return err
		}
		available, err := st.projectAvailable(c.ProjectID)
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		if err := st.eachMatching("board_connections", `id<>?`, []any{c.ID}, func(id string) error {
			other, err := st.boardConnection(id)
			if err != nil {
				return err
			}
			if boardScopeKey(other) == boardScopeKey(c) {
				return controlError(409, "This provider scope already has a board connection.")
			}
			return nil
		}); err != nil {
			return err
		}
		old, err := st.boardConnection(c.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		sync, err := st.boardSync(c.ID)
		if err != nil {
			return err
		}
		sync.Stale, sync.Error = true, ""
		if c.Provider == "azure" && (!old.OnlyAssignedToMe || in.Token != "") {
			sync.SnapshotPending = true
		}
		if err := st.putRoot("board_connections", c.ID, c); err != nil {
			return err
		}
		if err := st.putRoot("board_credentials", c.ID, protected); err != nil {
			return err
		}
		if err := st.putRoot("board_sync", c.ID, sync); err != nil {
			return err
		}
		if err := st.eachMatching("board_items", `x_connectionId=?`, []any{c.ID}, func(id string) error {
			var item activityBoardItem
			if err := decodeSQLiteRecord(st.ctx, st.tx, "board_items", id, 2<<20, &item); err != nil {
				return err
			}
			return indexSQLBoardItem(st.ctx, st.tx, item, c)
		}); err != nil {
			return err
		}
		return st.boardProjection(true)
	})
	if err == nil {
		a.boardScheduleRefreshLocked(c.ID)
	}
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.respondSQLBoardConfig(w, r, 200)
}
func (a *app) sqlBoardDeleteConnection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ExpectedRevision string `json:"expectedRevision"`
	}
	if !decode(w, r, &in) {
		return
	}
	revision, err := boardRevision(in.ExpectedRevision)
	if err != nil {
		boardFail(w, 400, err.Error())
		return
	}
	a.mu.Lock()
	err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		if err := a.sqlBoardCheck(st, revision, false); err != nil {
			return err
		}
		id := r.PathValue("id")
		if _, err := st.boardConnection(id); err != nil {
			return err
		}
		if err := st.eachMatching("board_items", `x_connectionId=?`, []any{id}, func(key string) error { return st.deleteRecord("board_items", key) }); err != nil {
			return err
		}
		for _, table := range []string{"board_connections", "board_credentials", "board_sync"} {
			if err := st.deleteRecord(table, id); err != nil {
				return err
			}
		}
		return st.boardProjection(true)
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.respondSQLBoardConfig(w, r, 200)
}
func (a *app) sqlBoardItems(w http.ResponseWriter, r *http.Request) {
	project, phase, sprint := r.URL.Query().Get("projectId"), r.URL.Query().Get("phaseId"), strings.ToLower(r.URL.Query().Get("sprintId"))
	if sprint != "" {
		v, err := hex.DecodeString(sprint)
		if err != nil || len(v) != 32 {
			boardFail(w, 400, "Invalid sprint filter.")
			return
		}
	}
	limit, err := sqlPageLimit(r, 100)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	cursor := boardItemsCursor{Project: project, Phase: phase, Sprint: sprint}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || len(b) > 2048 || json.Unmarshal(b, &cursor) != nil {
			boardFail(w, 400, "Invalid board cursor.")
			return
		}
	}
	items := []any{}
	more := false
	total := 0
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		config, err := st.boardConfig()
		if err != nil {
			return err
		}
		if r.URL.Query().Get("cursor") != "" && (cursor.Revision != config.Projection || cursor.Project != project || cursor.Phase != phase || cursor.Sprint != sprint) {
			return controlError(409, "Board changed. Reload this column.")
		}
		cursor.Revision = config.Projection
		if project != "" {
			available, err := st.projectAvailable(project)
			if err != nil {
				return err
			}
			if !available {
				return sql.ErrNoRows
			}
		}
		from := ` FROM data_board_items i JOIN board_item_views v ON v.item_id=i.id JOIN data_board_connections c ON c.id=i.x_connectionId JOIN data_projects p ON p.id=c.project_id LEFT JOIN data_board_sync s ON s.id=c.id WHERE p.x_removed<>'true' AND coalesce(s.x_snapshotPending,'')<>'true' AND (c.x_provider<>'azure' OR c.x_onlyAssignedToMe='true') AND v.phase<>'' AND (?='' OR c.project_id=?) AND (?='' OR v.phase=?) AND (?='' OR v.sprint=?)`
		args := []any{project, project, phase, phase, sprint, sprint}
		if err := tx.QueryRowContext(st.ctx, `SELECT count(*)`+from, args...).Scan(&total); err != nil {
			return err
		}
		ids, err := sqliteQueryIDs(st.ctx, tx, `SELECT i.id`+from+` AND i.id>? ORDER BY i.id LIMIT ?`, append(args, cursor.After, limit+1)...)
		if err != nil {
			return err
		}
		more = len(ids) > limit
		if more {
			ids = ids[:limit]
		}
		for _, id := range ids {
			var item activityBoardItem
			if err := decodeSQLiteRecord(st.ctx, tx, "board_items", id, 2<<20, &item); err != nil {
				return err
			}
			c, err := st.boardConnection(item.ConnectionID)
			if err != nil {
				return err
			}
			var pending *activityBoardMove
			var key string
			err = tx.QueryRowContext(st.ctx, `SELECT id FROM data_board_moves WHERE x_key=? AND status IN ('pending','unconfirmed') LIMIT 1`, id).Scan(&key)
			if err == nil {
				var m activityBoardMove
				if err := decodeSQLiteRecord(st.ctx, tx, "board_moves", key, 64<<10, &m); err != nil {
					return err
				}
				pending = &m
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			items = append(items, struct {
				activityBoardItem
				PhaseID   string             `json:"phaseId"`
				ProjectID string             `json:"projectId"`
				Provider  string             `json:"provider"`
				Move      *activityBoardMove `json:"move,omitempty"`
			}{item, boardClassify(item, c), c.ProjectID, c.Provider, pending})
			cursor.After = id
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	next := ""
	if more {
		b, _ := json.Marshal(cursor)
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	respond(w, 200, map[string]any{"items": items, "nextCursor": next, "hasMore": more, "total": total, "projectionRevision": sqliteRevision(cursor.Revision)})
}
func (a *app) sqlBoardRefresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConnectionID string `json:"connectionId"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.mu.Lock()
	ids, err := sqliteQueryIDs(r.Context(), a.store.readers, `SELECT id FROM data_board_connections WHERE (?='' OR id=?) ORDER BY id`, in.ConnectionID, in.ConnectionID)
	if err == nil && in.ConnectionID != "" && len(ids) == 0 {
		err = sql.ErrNoRows
	}
	if err == nil {
		for _, id := range ids {
			a.boardScheduleRefreshLocked(id)
		}
	}
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.respondSQLBoardConfig(w, r, 202)
}
func (a *app) sqlBoardMove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OperationID            string `json:"operationId"`
		PhaseID                string `json:"phaseId"`
		ExpectedConfigRevision string `json:"expectedConfigRevision"`
		ObservedVersion        string `json:"observedVersion"`
	}
	if !decode(w, r, &in) {
		return
	}
	revision, err := boardRevision(in.ExpectedConfigRevision)
	if err != nil || len(in.OperationID) < 16 || len(in.OperationID) > 100 || in.PhaseID == "" || in.ObservedVersion == "" {
		boardFail(w, 400, "An operation ID, phase and observed version are required.")
		return
	}
	var move activityBoardMove
	var item activityBoardItem
	var c activityBoardConnection
	var protected []byte
	dispatch := false
	a.mu.Lock()
	err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		err := decodeSQLiteRecord(st.ctx, st.tx, "board_moves", in.OperationID, 64<<10, &move)
		if err == nil {
			if move.Key != r.PathValue("key") || move.PhaseID != in.PhaseID || move.ConfigRevision != revision || move.ObservedVersion != in.ObservedVersion {
				return controlError(409, "Operation ID already has a different request.")
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		itemErr := decodeSQLiteRecord(st.ctx, st.tx, "board_items", r.PathValue("key"), 2<<20, &item)
		move = activityBoardMove{OperationID: in.OperationID, Key: r.PathValue("key"), ConnectionID: item.ConnectionID, PhaseID: in.PhaseID, ConfigRevision: revision, ObservedVersion: in.ObservedVersion, Status: "pending", CreatedAt: now(), UpdatedAt: now()}
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			if itemErr != nil && !errors.Is(itemErr, sql.ErrNoRows) {
				return itemErr
			}
			move.Status, move.Message = "failed", "Update cancelled before engine acceptance. No new provider write was dispatched."
			if err := st.putRoot("board_moves", move.OperationID, move); err != nil {
				return err
			}
			if err := st.pruneBoardMoves(); err != nil {
				return err
			}
			return st.boardProjection(false)
		}
		if itemErr != nil {
			return itemErr
		}
		c, err = st.boardConnection(item.ConnectionID)
		if err != nil {
			return err
		}
		available, err := st.projectAvailable(c.ProjectID)
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		config, err := st.boardConfig()
		if err != nil {
			return err
		}
		if config.Revision != revision || item.Version != in.ObservedVersion {
			return controlError(409, "Item or configuration changed. Refresh and try again.")
		}
		if len(c.Mappings[in.PhaseID]) == 0 {
			return controlError(400, "Mapping required.")
		}
		sync, err := st.boardSync(c.ID)
		if err != nil {
			return err
		}
		if a.activityBoardBusy[c.ID] || sync.Stale || sync.SnapshotPending {
			return controlError(409, "Refresh this connection and wait before moving an item.")
		}
		var pending bool
		if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_board_moves WHERE x_key=? AND status IN ('pending','unconfirmed'))`, item.Key).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return controlError(409, "Update unconfirmed. Refresh to reconcile before another update.")
		}
		if err := decodeSQLiteRecord(st.ctx, st.tx, "board_credentials", c.ID, 1<<20, &protected); err != nil {
			return err
		}
		if err := st.putRoot("board_moves", move.OperationID, move); err != nil {
			return err
		}
		dispatch = true
		return st.pruneBoardMoves()
	})
	if err == nil && dispatch {
		if a.activityBoardBusy == nil {
			a.activityBoardBusy = map[string]bool{}
		}
		a.activityBoardBusy[c.ID] = true
		a.wg.Add(1)
		go func() { defer a.wg.Done(); a.boardExecuteMove(c, protected, item, move) }()
	}
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	status := 200
	if dispatch {
		status = 202
	}
	respond(w, status, move)
}
func (a *app) sqlBoardGetMove(w http.ResponseWriter, r *http.Request) {
	var move activityBoardMove
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		return decodeSQLiteRecord(r.Context(), tx, "board_moves", r.PathValue("id"), 64<<10, &move)
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, move)
}
