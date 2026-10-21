package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (a *app) activityBoardRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/activity-board/config", a.boardGetConfig)
	m.HandleFunc("PATCH /api/activity-board/config", a.boardPatchConfig)
	m.HandleFunc("POST /api/activity-board/discover", a.boardDiscover)
	m.HandleFunc("POST /api/activity-board/connections", a.boardSaveConnection)
	m.HandleFunc("PATCH /api/activity-board/connections/{id}", a.boardSaveConnection)
	m.HandleFunc("DELETE /api/activity-board/connections/{id}", a.boardDeleteConnection)
	m.HandleFunc("GET /api/activity-board/items", a.boardItems)
	m.HandleFunc("GET /api/activity-board/sprints", a.boardSprints)
	m.HandleFunc("POST /api/activity-board/refresh", a.boardRefresh)
	m.HandleFunc("POST /api/activity-board/items/{key}/phase", a.boardMove)
	m.HandleFunc("POST /api/activity-board/items/{key}/phase/cancel", a.boardMove)
	m.HandleFunc("GET /api/activity-board/moves/{id}", a.boardGetMove)
}
func boardFail(w http.ResponseWriter, code int, text string) {
	respond(w, code, map[string]string{"error": text, "code": "activity_board_" + strconv.Itoa(code)})
}
func (a *app) boardAvailableProject(id string) bool {
	p := a.state.project(id)
	return p != nil && !p.Removed
}
func (a *app) boardConfigDTO() any {
	connections := []any{}
	ids := []string{}
	for id := range a.state.ActivityBoardConnections {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := a.state.ActivityBoardConnections[id]
		connections = append(connections, struct {
			activityBoardConnection
			CredentialConfigured bool              `json:"credentialConfigured"`
			Available            bool              `json:"available"`
			Sync                 activityBoardSync `json:"sync"`
			Syncing              bool              `json:"syncing"`
		}{c, len(a.state.ActivityBoardCredentials[id]) > 0, a.boardAvailableProject(c.ProjectID), a.state.ActivityBoardSync[id], a.activityBoardBusy[id]})
	}
	moves := []activityBoardMove{}
	for _, m := range a.state.ActivityBoardMoves {
		if boardMoveUnresolved(m) {
			moves = append(moves, m)
		}
	}
	return map[string]any{"revision": strconv.FormatUint(a.state.ActivityBoardConfig.Revision, 10), "projectionRevision": strconv.FormatUint(a.state.ActivityBoardConfig.Projection, 10), "phases": a.state.ActivityBoardConfig.Phases, "connections": connections, "unresolvedMoves": moves}
}
func (a *app) boardGetConfig(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	dto := a.boardConfigDTO()
	a.mu.Unlock()
	respond(w, 200, dto)
}
func (a *app) boardCheckConfig(revision uint64, credentialOnly ...bool) error {
	if a.closing || a.storageErr != nil {
		return errors.New("Board persistence unavailable.")
	}
	if a.state.ActivityBoardConfig.Revision != revision {
		return errors.New("Board configuration changed. Reload settings.")
	}
	if len(credentialOnly) == 0 || !credentialOnly[0] {
		for _, m := range a.state.ActivityBoardMoves {
			if boardMoveUnresolved(m) {
				return errors.New("Reconcile unconfirmed updates with Refresh before changing configuration.")
			}
		}
	}
	for _, busy := range a.activityBoardBusy {
		if busy {
			return errors.New("Board is refreshing or updating. Wait before changing configuration.")
		}
	}
	return nil
}
func boardCommitConfig(d *diskState) error {
	d.ActivityBoardConfig.Revision++
	d.ActivityBoardConfig.Projection++
	return validateActivityBoardRecords(d)
}
func (a *app) boardPatchConfig(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(100 * time.Second))
	var input struct {
		ExpectedRevision string                          `json:"expectedRevision"`
		PhaseID          string                          `json:"phaseId"`
		Name             string                          `json:"name"`
		Mappings         map[string][]activityBoardValue `json:"mappings"`
	}
	if !decode(w, r, &input) {
		return
	}
	revision, err := boardRevision(input.ExpectedRevision)
	name, valid := cleanLabel(input.Name, 100)
	if err != nil || !valid {
		boardFail(w, 400, "A phase name and valid revision are required.")
		return
	}
	a.mu.Lock()
	if err := a.boardCheckConfig(revision); err != nil {
		a.mu.Unlock()
		boardFail(w, 409, err.Error())
		return
	}
	connections := []activityBoardConnection{}
	tokens := map[string][]byte{}
	for _, c := range a.state.ActivityBoardConnections {
		connections = append(connections, c)
		tokens[c.ID] = a.state.ActivityBoardCredentials[c.ID]
	}
	phases := append([]activityBoardPhase{}, a.state.ActivityBoardConfig.Phases...)
	a.mu.Unlock()
	id := input.PhaseID
	if id == "" {
		id = newID()
		phases = append(phases, activityBoardPhase{id, name})
		if len(input.Mappings) != len(connections) {
			boardFail(w, 400, "Map the new phase in every board connection.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		for i, c := range connections {
			if input.Mappings[c.ID] == nil {
				boardFail(w, 400, "Map the new phase in every board connection.")
				return
			}
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
			resolved.Mappings[id] = input.Mappings[c.ID]
			if err := boardResolveMappings(phases, resolved.Mappings, catalog); err != nil {
				boardFail(w, 400, err.Error())
				return
			}
			connections[i] = resolved
		}
	} else {
		found := false
		for i := range phases {
			if phases[i].ID == id {
				phases[i].Name = name
				found = true
			}
		}
		if !found {
			boardFail(w, 404, "Phase not found.")
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.boardCheckConfig(revision); err != nil {
		boardFail(w, 409, err.Error())
		return
	}
	err = a.commitTransactionLocked(func(d *diskState) error {
		d.ActivityBoardConfig.Phases = phases
		for _, c := range connections {
			d.ActivityBoardConnections[c.ID] = c
		}
		return boardCommitConfig(d)
	})
	if err != nil {
		boardFail(w, 503, "Could not persist board configuration.")
		return
	}
	respond(w, 200, a.boardConfigDTO())
}
func cloneTransactionValueOfMappings(m map[string][]activityBoardValue) map[string][]activityBoardValue {
	result := map[string][]activityBoardValue{}
	for k, v := range m {
		result[k] = append([]activityBoardValue{}, v...)
	}
	return result
}
func boardSameMappingIDs(a, b map[string][]activityBoardValue) bool {
	if len(a) != len(b) {
		return false
	}
	for phase, values := range a {
		if len(values) != len(b[phase]) {
			return false
		}
		for i, v := range values {
			if v.ID != b[phase][i].ID {
				return false
			}
		}
	}
	return true
}

type boardConnectionInput struct {
	ExpectedRevision string                          `json:"expectedRevision"`
	ConnectionID     string                          `json:"connectionId"`
	Provider         string                          `json:"provider"`
	Name             string                          `json:"name"`
	ProjectID        string                          `json:"projectId"`
	Scope            activityBoardScope              `json:"scope"`
	Token            string                          `json:"token"`
	Mappings         map[string][]activityBoardValue `json:"mappings"`
	CreateLabel      string                          `json:"createLabel"`
	Discovery        string                          `json:"discovery"`
	OnlyAssignedToMe *bool                           `json:"onlyAssignedToMe"` // Accepted only for old-client compatibility; never changes the mandatory Azure filter.
}

func (a *app) boardInputConnection(in boardConnectionInput, allowRetained ...bool) (activityBoardConnection, string, error) {
	c := activityBoardConnection{ID: in.ConnectionID, Name: in.Name, Provider: in.Provider, ProjectID: in.ProjectID, Scope: in.Scope, Mappings: in.Mappings}
	if in.ConnectionID != "" {
		a.mu.Lock()
		saved, ok := a.state.ActivityBoardConnections[in.ConnectionID]
		protected := a.state.ActivityBoardCredentials[in.ConnectionID]
		available := a.boardAvailableProject(saved.ProjectID)
		a.mu.Unlock()
		if !ok || !available && !(len(allowRetained) > 0 && allowRetained[0]) {
			return c, "", errors.New("Board connection unavailable.")
		}
		c = saved
		if in.Token == "" {
			plain, err := boardCredential(protected, false)
			if err != nil {
				return c, "", err
			}
			in.Token = string(plain)
		}
	}
	c.Mappings = cloneTransactionValueOfMappings(c.Mappings)
	if err := boardValidateToken(in.Token); err != nil {
		return c, "", err
	}
	return c, in.Token, nil
}
func (a *app) boardDiscover(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(100 * time.Second))
	var in boardConnectionInput
	if !decode(w, r, &in) {
		return
	}
	phaseMappings := in.Discovery == "phase-mappings"
	if phaseMappings && (in.ConnectionID == "" || in.CreateLabel != "") {
		boardFail(w, 400, "Phase mapping discovery requires an existing connection and is read-only.")
		return
	}
	c, token, err := a.boardInputConnection(in, phaseMappings)
	if err != nil {
		boardFail(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if in.Discovery != "" && !phaseMappings {
		if c.Provider != "azure" || in.CreateLabel != "" || (in.Discovery != "projects" && in.Discovery != "work-item-types") {
			boardFail(w, 400, "Choose Azure project or work item type discovery.")
			return
		}
		resolved, values, err := boardDiscoverAzureOptions(ctx, c, token, in.Discovery)
		if err != nil {
			boardFail(w, 502, err.Error())
			return
		}
		respond(w, 200, map[string]any{"scope": resolved.Scope, "values": values})
		return
	}
	c, catalog, err := boardDiscover(ctx, c, token)
	if err != nil {
		boardFail(w, 502, err.Error())
		return
	}
	if in.CreateLabel != "" {
		name, ok := cleanLabel(in.CreateLabel, 50)
		if c.Provider != "github" || !ok {
			boardFail(w, 400, "Enter a GitHub label name (up to 50 characters).")
			return
		}
		exists := false
		for _, v := range catalog {
			if strings.EqualFold(v.Name, name) {
				exists = true
			}
		}
		if !exists {
			if err := boardAPI(ctx, c, token, "POST", boardGithubPath(c)+"/labels", map[string]string{"name": name, "color": "6e7bdb"}, nil); err != nil {
				boardFail(w, 502, "Label creation unconfirmed. Discover labels before trying again.")
				return
			}
			c, catalog, err = boardDiscover(ctx, c, token)
			if err != nil {
				boardFail(w, 502, "Label created; catalog refresh failed. Discover labels again.")
				return
			}
		}
	}
	respond(w, 200, map[string]any{"scope": c.Scope, "values": catalog})
}
func (a *app) boardSaveConnection(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(100 * time.Second))
	var in boardConnectionInput
	if !decode(w, r, &in) {
		return
	}
	revision, err := boardRevision(in.ExpectedRevision)
	name, ok := cleanLabel(in.Name, 100)
	if err != nil || !ok {
		boardFail(w, 400, "Connection name and revision are required.")
		return
	}
	in.ConnectionID = r.PathValue("id")
	a.mu.Lock()
	existing := a.state.ActivityBoardConnections[in.ConnectionID]
	credentialOnly := in.ConnectionID != "" && boardSameMappingIDs(in.Mappings, existing.Mappings)
	if err := a.boardCheckConfig(revision, credentialOnly); err != nil {
		a.mu.Unlock()
		boardFail(w, 409, err.Error())
		return
	}
	phases := append([]activityBoardPhase{}, a.state.ActivityBoardConfig.Phases...)
	available := a.boardAvailableProject(in.ProjectID)
	a.mu.Unlock()
	if !available {
		boardFail(w, 404, "Choose an active local project.")
		return
	}
	c, token, err := a.boardInputConnection(in)
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
	c.Name, c.Mappings = name, in.Mappings
	c.OnlyAssignedToMe = c.Provider == "azure"
	if in.ConnectionID != "" && in.ProjectID != c.ProjectID {
		boardFail(w, 400, "Scope and local project are fixed. Create a new connection to change them.")
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
	if err := a.boardCheckConfig(revision, credentialOnly); err != nil {
		a.mu.Unlock()
		boardFail(w, 409, err.Error())
		return
	}
	if !a.boardAvailableProject(c.ProjectID) {
		a.mu.Unlock()
		boardFail(w, 404, "Project unavailable.")
		return
	}
	for id, existing := range a.state.ActivityBoardConnections {
		if id != c.ID && boardScopeKey(existing) == boardScopeKey(c) {
			a.mu.Unlock()
			boardFail(w, 409, "This provider scope already has a board connection.")
			return
		}
	}
	err = a.commitTransactionLocked(func(d *diskState) error {
		previous := d.ActivityBoardConnections[c.ID]
		sync := d.ActivityBoardSync[c.ID]
		sync.Stale, sync.Error = true, ""
		// An old unfiltered snapshot or another token owner's tasks must not be
		// presented as the mandatory @Me inventory. Only a full scan releases it.
		if c.Provider == "azure" && (!previous.OnlyAssignedToMe || in.Token != "") {
			sync.SnapshotPending = true
		}
		d.ActivityBoardConnections[c.ID] = c
		d.ActivityBoardCredentials[c.ID] = protected
		d.ActivityBoardSync[c.ID] = sync
		return boardCommitConfig(d)
	})
	if err != nil {
		a.mu.Unlock()
		boardFail(w, 503, "Could not persist board connection.")
		return
	}
	a.boardScheduleRefreshLocked(c.ID)
	dto := a.boardConfigDTO()
	a.mu.Unlock()
	respond(w, 200, dto)
}
func (a *app) boardDeleteConnection(w http.ResponseWriter, r *http.Request) {
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
	defer a.mu.Unlock()
	if err := a.boardCheckConfig(revision); err != nil {
		boardFail(w, 409, err.Error())
		return
	}
	id := r.PathValue("id")
	if _, ok := a.state.ActivityBoardConnections[id]; !ok {
		boardFail(w, 404, "Connection not found.")
		return
	}
	err = a.commitTransactionLocked(func(d *diskState) error {
		delete(d.ActivityBoardConnections, id)
		delete(d.ActivityBoardCredentials, id)
		delete(d.ActivityBoardSync, id)
		for key, item := range d.ActivityBoardItems {
			if item.ConnectionID == id {
				delete(d.ActivityBoardItems, key)
			}
		}
		return boardCommitConfig(d)
	})
	if err != nil {
		boardFail(w, 503, "Could not persist connection removal.")
		return
	}
	respond(w, 200, a.boardConfigDTO())
}

type boardItemsCursor struct {
	Revision uint64 `json:"r"`
	Project  string `json:"p"`
	Phase    string `json:"f"`
	After    string `json:"a"`
	Sprint   string `json:"s,omitempty"`
}

func (a *app) boardItems(w http.ResponseWriter, r *http.Request) {
	project, phase := r.URL.Query().Get("projectId"), r.URL.Query().Get("phaseId")
	sprint := strings.ToLower(r.URL.Query().Get("sprintId"))
	if sprint != "" {
		decoded, err := hex.DecodeString(sprint)
		if err != nil || len(decoded) != 32 {
			boardFail(w, 400, "Invalid sprint filter.")
			return
		}
	}
	limit := 40
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			boardFail(w, 400, "Invalid page limit.")
			return
		}
		limit = n
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if project != "" && !a.boardAvailableProject(project) {
		boardFail(w, 404, "Project unavailable.")
		return
	}
	cursor := boardItemsCursor{Revision: a.state.ActivityBoardConfig.Projection, Project: project, Phase: phase, Sprint: sprint}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || len(b) > 2048 || json.Unmarshal(b, &cursor) != nil {
			boardFail(w, 400, "Invalid board cursor.")
			return
		}
		if cursor.Revision != a.state.ActivityBoardConfig.Projection || cursor.Project != project || cursor.Phase != phase || cursor.Sprint != sprint {
			boardFail(w, 409, "Board changed. Reload this column.")
			return
		}
	}
	keys := []string{}
	total := 0
	for key, item := range a.state.ActivityBoardItems {
		c := a.state.ActivityBoardConnections[item.ConnectionID]
		if !a.boardAvailableProject(c.ProjectID) || project != "" && c.ProjectID != project {
			continue
		}
		if a.state.ActivityBoardSync[c.ID].SnapshotPending || c.Provider == "azure" && !c.OnlyAssignedToMe {
			continue
		}
		if sprint != "" && boardSprintKey(c, item) != sprint {
			continue
		}
		classification := boardClassify(item, c)
		if classification == "" || phase != "" && classification != phase {
			continue
		}
		total++
		if key > cursor.After {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	more := len(keys) > limit
	if more {
		keys = keys[:limit]
	}
	items := []any{}
	for _, key := range keys {
		item := a.state.ActivityBoardItems[key]
		c := a.state.ActivityBoardConnections[item.ConnectionID]
		move := (*activityBoardMove)(nil)
		for _, m := range a.state.ActivityBoardMoves {
			if m.Key == key && boardMoveUnresolved(m) {
				copy := m
				move = &copy
				break
			}
		}
		items = append(items, struct {
			activityBoardItem
			PhaseID   string             `json:"phaseId"`
			ProjectID string             `json:"projectId"`
			Provider  string             `json:"provider"`
			Move      *activityBoardMove `json:"move,omitempty"`
		}{item, boardClassify(item, c), c.ProjectID, c.Provider, move})
	}
	next := ""
	if more && len(keys) > 0 {
		cursor.After = keys[len(keys)-1]
		b, _ := json.Marshal(cursor)
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	respond(w, 200, map[string]any{"items": items, "nextCursor": next, "hasMore": more, "total": total, "projectionRevision": strconv.FormatUint(cursor.Revision, 10)})
}
func (a *app) boardRefresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConnectionID string `json:"connectionId"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.mu.Lock()
	if in.ConnectionID != "" && a.state.ActivityBoardConnections[in.ConnectionID].ID == "" {
		a.mu.Unlock()
		boardFail(w, 404, "Connection not found.")
		return
	}
	for id := range a.state.ActivityBoardConnections {
		if in.ConnectionID == "" || in.ConnectionID == id {
			a.boardScheduleRefreshLocked(id)
		}
	}
	dto := a.boardConfigDTO()
	a.mu.Unlock()
	respond(w, 202, dto)
}
func (a *app) boardMove(w http.ResponseWriter, r *http.Request) {
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
	key := r.PathValue("key")
	a.mu.Lock()
	defer a.mu.Unlock()
	if old, ok := a.state.ActivityBoardMoves[in.OperationID]; ok {
		if old.Key != key || old.PhaseID != in.PhaseID || old.ConfigRevision != revision || old.ObservedVersion != in.ObservedVersion {
			boardFail(w, 409, "Operation ID already has a different request.")
			return
		}
		respond(w, 200, old)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/cancel") {
		// This durable receipt reserves an unaccepted operation ID under the same
		// lock as dispatch. A delayed original POST can only return this receipt.
		m := activityBoardMove{OperationID: in.OperationID, Key: key, ConnectionID: a.state.ActivityBoardItems[key].ConnectionID, PhaseID: in.PhaseID, ConfigRevision: revision, ObservedVersion: in.ObservedVersion, Status: "failed", Message: "Update cancelled before engine acceptance. No new provider write was dispatched.", CreatedAt: now(), UpdatedAt: now()}
		if err := a.commitTransactionLocked(func(d *diskState) error {
			d.ActivityBoardMoves[m.OperationID] = m
			pruneBoardMoves(d)
			d.ActivityBoardConfig.Projection++
			return nil
		}); err != nil {
			boardFail(w, 503, "Could not persist cancellation. Check the update receipt before trying again.")
			return
		}
		respond(w, 200, m)
		return
	}
	item, ok := a.state.ActivityBoardItems[key]
	c := a.state.ActivityBoardConnections[item.ConnectionID]
	c.Mappings = cloneTransactionValueOfMappings(c.Mappings)
	if !ok || !a.boardAvailableProject(c.ProjectID) {
		boardFail(w, 404, "Item unavailable.")
		return
	}
	if revision != a.state.ActivityBoardConfig.Revision || item.Version != in.ObservedVersion {
		boardFail(w, 409, "Item or configuration changed. Refresh and try again.")
		return
	}
	if len(c.Mappings[in.PhaseID]) == 0 {
		boardFail(w, 400, "Mapping required.")
		return
	}
	if a.activityBoardBusy[c.ID] || a.state.ActivityBoardSync[c.ID].Stale {
		boardFail(w, 409, "Refresh this connection and wait for it to finish before moving an item.")
		return
	}
	for _, m := range a.state.ActivityBoardMoves {
		if m.Key == key && boardMoveUnresolved(m) {
			boardFail(w, 409, "Update unconfirmed. Refresh to reconcile before another update.")
			return
		}
	}
	m := activityBoardMove{OperationID: in.OperationID, Key: key, ConnectionID: c.ID, PhaseID: in.PhaseID, ConfigRevision: revision, ObservedVersion: in.ObservedVersion, Status: "pending", CreatedAt: now(), UpdatedAt: now()}
	if err := a.commitTransactionLocked(func(d *diskState) error { d.ActivityBoardMoves[m.OperationID] = m; pruneBoardMoves(d); return nil }); err != nil {
		boardFail(w, 503, "Could not persist update intent; nothing was sent.")
		return
	}
	a.activityBoardBusy[c.ID] = true
	protected := a.state.ActivityBoardCredentials[c.ID]
	a.wg.Add(1)
	go func() { defer a.wg.Done(); a.boardExecuteMove(c, protected, item, m) }()
	respond(w, 202, m)
}
func (a *app) boardGetMove(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	m, ok := a.state.ActivityBoardMoves[r.PathValue("id")]
	a.mu.Unlock()
	if !ok {
		boardFail(w, 404, "Update receipt not found. Refresh before making another update.")
		return
	}
	respond(w, 200, m)
}
