package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func respond(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, code int, text string) {
	respond(w, code, map[string]string{"error": text})
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		fail(w, http.StatusBadRequest, "Invalid JSON request body or body too large.")
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		fail(w, http.StatusBadRequest, "Expected one JSON object.")
		return false
	}
	return true
}

func loopbackHost(hostport string) bool {
	host := hostport
	if strings.HasPrefix(hostport, "[") && strings.HasSuffix(hostport, "]") {
		host = hostport[1 : len(hostport)-1]
	} else if strings.Contains(hostport, ":") {
		var err error
		host, _, err = net.SplitHostPort(hostport)
		if err != nil {
			return false
		}
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/state", a.getState)
	mux.HandleFunc("GET /api/transcription/settings", a.getTranscriptionSettings)
	mux.HandleFunc("PATCH /api/transcription/settings", a.updateTranscriptionSettings)
	mux.HandleFunc("POST /api/transcriptions", a.transcribe)
	mux.HandleFunc("POST /api/projects", a.createProject)
	mux.HandleFunc("PATCH /api/projects/{id}", a.updateProject)
	mux.HandleFunc("DELETE /api/projects/{id}", a.removeProject)
	mux.HandleFunc("POST /api/projects/{id}/folders", a.createFolder)
	mux.HandleFunc("PATCH /api/projects/{id}/folders", a.patchFolder)
	mux.HandleFunc("GET /api/projects/{id}/paths", a.projectPaths)
	mux.HandleFunc("GET /api/projects/{id}/authoring", a.authoring)
	mux.HandleFunc("GET /api/projects/{id}/models/{harness}", a.projectModels)
	mux.HandleFunc("POST /api/projects/{id}/agents", a.saveAgent)
	mux.HandleFunc("PATCH /api/projects/{id}/agents/{item}", a.saveAgent)
	mux.HandleFunc("DELETE /api/projects/{id}/agents/{item}", a.deleteAgent)
	mux.HandleFunc("POST /api/projects/{id}/graph-drafts", a.createGraphDraft)
	mux.HandleFunc("POST /api/projects/{id}/graphs/{item}", a.saveGraph)
	mux.HandleFunc("PATCH /api/projects/{id}/graphs/{item}", a.patchGraph)
	mux.HandleFunc("DELETE /api/projects/{id}/graphs/{item}", a.deleteGraph)
	mux.HandleFunc("POST /api/projects/{id}/graphs/{item}/layout", a.saveGraphLayout)
	mux.HandleFunc("POST /api/sessions", a.createSession)
	mux.HandleFunc("POST /api/general-session", a.generalConversation)
	mux.HandleFunc("GET /api/sessions/{id}/graph", a.getConversationGraph)
	mux.HandleFunc("POST /api/sessions/{id}/graph/runs/{runId}/{control}", a.controlGraphRun)
	mux.HandleFunc("GET /api/sessions/{id}/graph/runs/{runId}/nodes/{nodeId}/activity", a.getGraphNodeActivity)
	mux.HandleFunc("PATCH /api/sessions/{id}/graph", a.selectConversationGraph)
	mux.HandleFunc("PATCH /api/sessions/{id}/agent", a.chatSessionHandler(a.selectConversationAgent))
	mux.HandleFunc("GET /api/sessions/{id}/files/{fileID}", a.chatSessionHandler(a.chatFileContent))
	mux.HandleFunc("GET /api/images/{imageID}", a.getStoredImage)
	mux.HandleFunc("GET /api/graph-runs/{runID}", a.getGraphRun)
	mux.HandleFunc("POST /api/sessions/{id}/side", a.chatSessionHandler(a.sideConversation))
	mux.HandleFunc("PATCH /api/sessions/{id}/harness", a.chatSessionHandler(a.sideHarness))
	mux.HandleFunc("POST /api/sessions/{id}/consultations/{requestID}/cancel", a.chatSessionHandler(a.cancelConsultation))
	mux.HandleFunc("PATCH /api/sessions/{id}", a.chatSessionHandler(a.patchSession))
	mux.HandleFunc("DELETE /api/sessions/{id}", a.deleteSession)
	mux.HandleFunc("GET /api/sessions/{id}/models", a.getModels)
	mux.HandleFunc("GET /api/sessions/{id}/metadata", a.chatSessionHandler(a.getSessionMetadata))
	mux.HandleFunc("PATCH /api/sessions/{id}/settings", a.chatSessionHandler(a.updateModelSettings))
	mux.HandleFunc("GET /api/sessions/{id}/quota", a.getQuota)
	mux.HandleFunc("POST /api/sessions/{id}/messages", a.chatSessionHandler(a.message))
	mux.HandleFunc("POST /api/sessions/{id}/queue/{messageID}/send", a.chatSessionHandler(a.changeQueuedMessage))
	mux.HandleFunc("DELETE /api/sessions/{id}/queue/{messageID}", a.chatSessionHandler(a.changeQueuedMessage))
	mux.HandleFunc("GET /api/sessions/{id}/history", a.chatSessionHandler(a.history))
	mux.HandleFunc("GET /api/sessions/{id}/export", a.chatSessionHandler(a.exportSession))
	mux.HandleFunc("GET /api/sessions/{id}/events", a.chatSessionHandler(a.events))
	mux.HandleFunc("GET /api/updates", a.updates)
	mux.HandleFunc("PATCH /api/sessions/{id}/events/{eventID}", a.chatSessionHandler(a.patchEvent))
	mux.HandleFunc("POST /api/sessions/{id}/stop", a.chatSessionHandler(a.stop))
	mux.HandleFunc("POST /api/sessions/{id}/permissions/{permissionID}", a.permissionDecision)
	mux.HandleFunc("PATCH /api/sessions/{id}/permissions", a.chatSessionHandler(a.updatePermissionSettings))
	mux.HandleFunc("GET /api/permission-rules", a.permissionRules)
	mux.HandleFunc("DELETE /api/permission-rules/{ruleID}", a.deletePermissionRule)
	mux.HandleFunc("POST /api/sessions/{id}/questions/{questionID}/reply", a.answerQuestion)
	mux.HandleFunc("POST /api/dialogs/directory", a.directory)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "Endpoint not found.") })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Public clients may use any domain/IP, including reverse proxies and tunnels.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			transcriptionUpload := r.Method == http.MethodPost && r.URL.Path == "/api/transcriptions"
			messageUpload := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/sessions/") && strings.HasSuffix(r.URL.Path, "/messages")
			kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if (!transcriptionUpload && (err != nil || (kind != "application/json" && !(messageUpload && kind == "multipart/form-data")))) ||
				(transcriptionUpload && (err != nil || kind != "multipart/form-data")) {
				if transcriptionUpload {
					fail(w, 415, "Transcriptions require multipart/form-data.")
				} else {
					fail(w, 415, "Mutations require Content-Type: application/json.")
				}
				return
			}
			a.mu.Lock()
			blocked := a.closing || a.storageErr != nil
			a.mu.Unlock()
			if blocked {
				fail(w, 503, "Engine is shutting down or state persistence has failed.")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	if a.storageErr != nil {
		err := a.storageErr
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	activeGraphs := 0
	for _, run := range a.state.GraphRuns {
		if graphRunActive(run.Status) {
			activeGraphs++
		}
	}
	response := map[string]any{"status": "ok", "version": 2, "runningSessions": len(a.runs), "activeGraphRuns": activeGraphs}
	a.mu.Unlock()
	respond(w, 200, response)
}

func (a *app) getState(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	projects := []Project{}
	sessions := []SessionSummary{}
	visible := map[string]bool{}
	for _, project := range a.state.Projects {
		if !project.Removed {
			projects = append(projects, project)
			visible[project.ID] = true
		}
	}
	for _, session := range a.state.Sessions {
		if visible[session.ProjectID] && session.GraphRunID == "" {
			sessions = append(sessions, *a.sessionSummaryLocked(session.ID))
		}
	}
	state := struct {
		Projects          []Project        `json:"projects"`
		Sessions          []SessionSummary `json:"sessions"`
		Harnesses         []Harness        `json:"harnesses"`
		Revision          uint64           `json:"revision"`
		DeletedSessionIDs []string         `json:"deletedSessionIds"`
	}{projects, sessions, a.harnesses, a.state.GraphRevision, a.state.deletedSessionIDs()}
	a.mu.Unlock()
	respond(w, 200, state)
}

func cleanLabel(value string, max int) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max &&
		strings.IndexFunc(value, unicode.IsControl) == -1
}

func existingDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("Folder must be an absolute existing directory.")
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", errors.New("Folder must be an absolute existing directory.")
	}
	return path, nil
}

func validIcon(icon string) bool {
	if icon == "" {
		return true
	}
	const prefix = "data:image/png;base64,"
	if len(icon) > 128<<10 || !strings.HasPrefix(icon, prefix) {
		return false
	}
	b, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(icon, prefix))
	if err != nil {
		return false
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width > 1024 || cfg.Height > 1024 {
		return false
	}
	_, err = png.Decode(bytes.NewReader(b))
	return err == nil
}

type listPosition struct {
	BeforeID string `json:"beforeId"`
}

func moveProjectBefore(projects []Project, id, beforeID string) []Project {
	index := -1
	for i := range projects {
		if projects[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return projects
	}
	moved := projects[index]
	projects = append(projects[:index], projects[index+1:]...)
	insert := len(projects)
	if beforeID != "" {
		for i := range projects {
			if projects[i].ID == beforeID {
				insert = i
				break
			}
		}
	}
	projects = append(projects, Project{})
	copy(projects[insert+1:], projects[insert:])
	projects[insert] = moved
	return projects
}

func moveStringBefore(values []string, value, before string) []string {
	index := -1
	for i := range values {
		if values[i] == value {
			index = i
			break
		}
	}
	if index < 0 {
		return values
	}
	moved := values[index]
	values = append(values[:index], values[index+1:]...)
	insert := len(values)
	if before != "" {
		for i := range values {
			if values[i] == before {
				insert = i
				break
			}
		}
	}
	values = append(values, "")
	copy(values[insert+1:], values[insert:])
	values[insert] = moved
	return values
}

func moveSessionBefore(sessions []Session, id, beforeID string) []Session {
	index := -1
	for i := range sessions {
		if sessions[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return sessions
	}
	moved := sessions[index]
	sessions = append(sessions[:index], sessions[index+1:]...)
	insert := len(sessions)
	if beforeID != "" {
		for i := range sessions {
			if sessions[i].ID == beforeID {
				insert = i
				break
			}
		}
	}
	sessions = append(sessions, Session{})
	copy(sessions[insert+1:], sessions[insert:])
	sessions[insert] = moved
	return sessions
}

func (a *app) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Folder, Icon string }
	if !decode(w, r, &body) {
		return
	}
	name, ok := cleanLabel(body.Name, 60)
	if !ok {
		fail(w, 400, "Project name must contain 1 to 60 characters.")
		return
	}
	folder, err := existingDirectory(body.Folder)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !validIcon(body.Icon) {
		fail(w, 400, "Icon must be a PNG data URL, at most 128 KiB and 1024 by 1024 pixels.")
		return
	}
	p := Project{ID: newID(), Name: name, Folder: folder, Icon: body.Icon, Folders: []string{}, ArchivedFolders: []string{}}
	a.mu.Lock()
	for _, previous := range a.state.Projects {
		sameFolder := previous.Folder == folder || (runtime.GOOS == "windows" && strings.EqualFold(previous.Folder, folder))
		if previous.Removed && sameFolder {
			p.ID, p.Folders, p.ArchivedFolders = previous.ID, previous.Folders, previous.ArchivedFolders
			break
		}
	}
	err = a.commitLocked(func(d *diskState) {
		if previous := d.project(p.ID); previous != nil {
			*previous = p
		} else {
			d.Projects = append(d.Projects, p)
		}
	})
	a.mu.Unlock()
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 201, p)
}

func (a *app) updateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     *string       `json:"name"`
		Icon     *string       `json:"icon"`
		Position *listPosition `json:"position"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Name == nil && body.Icon == nil && body.Position == nil {
		fail(w, 400, "Provide a project name, icon, or position to update.")
		return
	}
	if body.Name != nil {
		name, ok := cleanLabel(*body.Name, 60)
		if !ok {
			fail(w, 400, "Project name must contain 1 to 60 characters.")
			return
		}
		body.Name = &name
	}
	if body.Icon != nil && !validIcon(*body.Icon) {
		fail(w, 400, "Icon must be a PNG data URL, at most 128 KiB and 1024 by 1024 pixels.")
		return
	}
	a.mu.Lock()
	id := r.PathValue("id")
	if p := a.state.project(id); p == nil || p.Removed {
		a.mu.Unlock()
		fail(w, 404, "Project not found.")
		return
	}
	if body.Position != nil && body.Position.BeforeID != "" {
		before := a.state.project(body.Position.BeforeID)
		if before == nil || before.Removed {
			a.mu.Unlock()
			fail(w, 400, "Position target must be an existing project.")
			return
		}
	}
	if err := a.commitLocked(func(d *diskState) {
		p := d.project(id)
		if body.Name != nil {
			p.Name = *body.Name
		}
		if body.Icon != nil {
			p.Icon = *body.Icon
		}
		if body.Position != nil && body.Position.BeforeID != id {
			d.Projects = moveProjectBefore(d.Projects, id, body.Position.BeforeID)
		}
	}); err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	response := *a.state.project(id)
	a.mu.Unlock()
	respond(w, 200, response)
}

func (a *app) removeProject(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	id := r.PathValue("id")
	p := a.state.project(id)
	if p == nil {
		a.mu.Unlock()
		fail(w, 404, "Project not found.")
		return
	}
	for _, session := range a.state.Sessions {
		if session.ProjectID == id && a.runs[session.ID] != nil {
			a.mu.Unlock()
			fail(w, 409, "Stop running sessions before removing this project.")
			return
		}
	}
	for _, run := range a.state.GraphRuns {
		if run.ProjectID == id && graphRunActive(run.Status) {
			a.mu.Unlock()
			fail(w, 409, "Project has an active graph run.")
			return
		}
	}
	if !p.Removed {
		if err := a.commitLocked(func(d *diskState) { d.project(id).Removed = true }); err != nil {
			a.mu.Unlock()
			fail(w, 503, err.Error())
			return
		}
	}
	a.mu.Unlock()
	respond(w, 200, map[string]bool{"removed": true})
}

func (a *app) createFolder(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name string }
	if !decode(w, r, &body) {
		return
	}
	name, ok := cleanLabel(body.Name, 60)
	if !ok || strings.EqualFold(name, "Ungrouped") {
		fail(w, 400, "Folder name must contain 1 to 60 characters and cannot be Ungrouped.")
		return
	}
	a.mu.Lock()
	p := a.state.project(r.PathValue("id"))
	if p == nil || p.Removed {
		a.mu.Unlock()
		fail(w, 404, "Project not found.")
		return
	}
	for _, f := range p.Folders {
		if strings.EqualFold(f, name) {
			a.mu.Unlock()
			fail(w, 409, "Folder already exists.")
			return
		}
	}
	id := p.ID
	if err := a.commitLocked(func(d *diskState) {
		p := d.project(id)
		p.Folders = append(p.Folders, name)
	}); err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	response := *a.state.project(id)
	a.mu.Unlock()
	respond(w, 201, response)
}

func (a *app) patchFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Archived *bool  `json:"archived"`
		Position *struct {
			Before string `json:"before"`
		} `json:"position"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Archived == nil && body.Position == nil {
		fail(w, 400, "Provide archived or position to update the folder.")
		return
	}
	a.mu.Lock()
	p := a.state.project(r.PathValue("id"))
	if p == nil || p.Removed {
		a.mu.Unlock()
		fail(w, 404, "Project not found.")
		return
	}
	name := ""
	for _, folder := range p.Folders {
		if strings.EqualFold(folder, strings.TrimSpace(body.Name)) {
			name = folder
			break
		}
	}
	if name == "" {
		a.mu.Unlock()
		fail(w, 404, "Session folder not found.")
		return
	}
	isArchived := false
	for _, folder := range p.ArchivedFolders {
		if folder == name {
			isArchived = true
			break
		}
	}
	if body.Position != nil {
		if isArchived {
			a.mu.Unlock()
			fail(w, 400, "Archived folders cannot be reordered.")
			return
		}
		if body.Position.Before != "" {
			beforeFound := false
			for _, folder := range p.Folders {
				if folder == body.Position.Before && !folderArchived(p, folder) {
					beforeFound = true
					break
				}
			}
			if !beforeFound {
				a.mu.Unlock()
				fail(w, 400, "Position target must be an active session folder.")
				return
			}
		}
	}
	if (body.Archived != nil && isArchived != *body.Archived) || body.Position != nil {
		id := p.ID
		if err := a.commitLocked(func(d *diskState) {
			project := d.project(id)
			if body.Archived != nil {
				if *body.Archived {
					project.ArchivedFolders = append(project.ArchivedFolders, name)
				} else {
					folders := project.ArchivedFolders[:0]
					for _, folder := range project.ArchivedFolders {
						if folder != name {
							folders = append(folders, folder)
						}
					}
					project.ArchivedFolders = folders
				}
			}
			if body.Position != nil && body.Position.Before != name {
				project.Folders = moveStringBefore(project.Folders, name, body.Position.Before)
			}
		}); err != nil {
			a.mu.Unlock()
			fail(w, 503, err.Error())
			return
		}
	}
	response := *a.state.project(p.ID)
	a.mu.Unlock()
	respond(w, 200, response)
}

func folderArchived(p *Project, name string) bool {
	for _, folder := range p.ArchivedFolders {
		if folder == name {
			return true
		}
	}
	return false
}

func workspace(p *Project, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name == "Ungrouped" {
		return "Ungrouped", true
	}
	for _, f := range p.Folders {
		if f == name && !folderArchived(p, f) {
			return f, true
		}
	}
	return "", false
}

func (a *app) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID string `json:"projectId"`
		Title     string `json:"title"`
		Workspace string `json:"workspace"`
		Harness   string `json:"harness"`
		Model     string `json:"model"`
	}
	if !decode(w, r, &body) {
		return
	}
	title, ok := cleanLabel(body.Title, 200)
	if !ok {
		fail(w, 400, "Session title must contain 1 to 200 characters.")
		return
	}
	model := strings.TrimSpace(body.Model)
	if model != "" {
		if _, ok := cleanLabel(model, 200); !ok || strings.HasPrefix(model, "-") {
			fail(w, 400, "Invalid model identifier.")
			return
		}
	}
	a.mu.Lock()
	p := a.state.project(body.ProjectID)
	if p == nil || p.Removed {
		a.mu.Unlock()
		fail(w, 404, "Project not found.")
		return
	}
	group, ok := workspace(p, body.Workspace)
	if !ok {
		a.mu.Unlock()
		fail(w, 400, "Workspace must be Ungrouped or an existing project folder name.")
		return
	}
	if _, ok := a.binaries[body.Harness]; !ok {
		a.mu.Unlock()
		fail(w, 400, "Harness is unknown or not installed.")
		return
	}
	s := newTopLevelSession(p.ID, title, group, body.Harness, model, "")
	if err := a.commitLocked(func(d *diskState) {
		a.appendTopLevelSession(d, s)
	}); err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	response := a.currentSessionUpdateLocked(s.ID)
	a.mu.Unlock()
	respond(w, 201, response)
}

func (a *app) patchSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title     *string `json:"title"`
		Workspace *string `json:"workspace"`
		Archived  *bool   `json:"archived"`
		Position  *struct {
			Workspace string `json:"workspace"`
			BeforeID  string `json:"beforeId"`
		} `json:"position"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Title == nil && body.Workspace == nil && body.Archived == nil && body.Position == nil {
		fail(w, 400, "Provide title, workspace, archived, or position.")
		return
	}
	a.mu.Lock()
	s := a.state.session(r.PathValue("id"))
	if s == nil {
		a.mu.Unlock()
		fail(w, 404, "Session not found.")
		return
	}
	archiveFolder := body.Workspace
	if archiveFolder == nil && body.Position != nil {
		archiveFolder = &body.Position.Workspace
	}
	if err := sessionArchiveLabels(&a.state, s, body.Archived, archiveFolder); err != nil {
		a.mu.Unlock()
		failSessionControl(w, err)
		return
	}
	if s.Role == sessionRoleGeneralAgent {
		a.mu.Unlock()
		fail(w, 400, "The general conversation cannot be renamed, moved, reordered or archived.")
		return
	}
	if body.Position != nil && (s.ParentID != "" || s.Role == "graph_node" || s.Archived) {
		a.mu.Unlock()
		fail(w, 400, "Only active top-level sessions can be reordered.")
		return
	}
	title, group, err := sessionLabels(&a.state, s, body.Title, body.Workspace)
	if err != nil {
		a.mu.Unlock()
		failSessionControl(w, err)
		return
	}
	var ok bool
	if body.Position != nil {
		group, ok = workspace(a.state.project(s.ProjectID), body.Position.Workspace)
		if !ok {
			a.mu.Unlock()
			fail(w, 400, "Position workspace must be Ungrouped or an existing project folder name.")
			return
		}
		if body.Workspace != nil && group != strings.TrimSpace(*body.Workspace) {
			a.mu.Unlock()
			fail(w, 400, "Workspace and position workspace must match.")
			return
		}
		if body.Position.BeforeID != "" {
			before := a.state.session(body.Position.BeforeID)
			if before == nil || before.ID == s.ID || before.ProjectID != s.ProjectID || before.ParentID != "" || before.Role == "graph_node" || before.Archived || before.Workspace != group {
				a.mu.Unlock()
				fail(w, 400, "Position target must be another active session in the destination folder.")
				return
			}
		}
	}
	id := s.ID
	if err := a.commitLocked(func(d *diskState) {
		applySessionLabels(d, id, title, group, body.Archived, nil)
		if body.Position != nil {
			d.Sessions = moveSessionBefore(d.Sessions, id, body.Position.BeforeID)
		}
	}); err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	response := a.currentSessionUpdateLocked(id)
	a.mu.Unlock()
	respond(w, 200, response)
}

func (a *app) patchEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Favorite *bool `json:"favorite"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Favorite == nil {
		fail(w, 400, "Provide favorite to update the message.")
		return
	}
	a.mu.Lock()
	s := a.state.session(r.PathValue("id"))
	if s == nil {
		a.mu.Unlock()
		fail(w, 404, "Session not found.")
		return
	}
	eventID := r.PathValue("eventID")
	index := -1
	for i := range s.Events {
		if s.Events[i].ID == eventID {
			index = i
			break
		}
	}
	if index < 0 || s.Events[index].Type != "assistant" {
		a.mu.Unlock()
		fail(w, 404, "Assistant message not found.")
		return
	}
	if status := s.Events[index].Status; status != "" && status != "completed" {
		a.mu.Unlock()
		fail(w, 409, "Only completed responses can be favorited.")
		return
	}
	id := s.ID
	if err := a.commitLocked(func(d *diskState) {
		session := d.session(id)
		e := session.Events[index]
		e.Favorite = *body.Favorite
		d.setEvent(session, index, e)
		session.UpdatedAt = now()
	}); err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	response := a.state.session(id).Events[index]
	a.mu.Unlock()
	respond(w, 200, response)
}

func (a *app) message(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	initial := a.state.session(r.PathValue("id"))
	if initial == nil {
		a.mu.Unlock()
		fail(w, 404, "Session not found.")
		return
	}
	submissionStopVersion := a.stopVersions[initial.ID]
	a.mu.Unlock()
	var body messageBody
	files, uploadDir, decoded := a.decodeMessage(w, r, &body)
	if !decoded {
		return
	}
	retainFiles := false
	defer func() {
		if !retainFiles && uploadDir != "" {
			_ = os.RemoveAll(uploadDir)
		}
	}()
	if body.Mode != "" && body.Mode != "queue" && body.Mode != "steer" {
		fail(w, 400, "Message mode must be queue or steer.")
		return
	}
	if body.ClientID != "" && (len(body.ClientID) > 128 || strings.IndexFunc(body.ClientID, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-')
	}) != -1) {
		fail(w, 400, "Invalid message identity.")
		return
	}
	if (strings.TrimSpace(body.Text) == "" && len(files) == 0 && len(body.Images) == 0) || len(body.Text) > 128<<10 || !utf8.ValidString(body.Text) || strings.ContainsRune(body.Text, 0) {
		fail(w, 400, "Provide text or files; text must be UTF-8, at most 128 KiB, without NUL characters.")
		return
	}
	a.mu.Lock()
	s := a.state.session(r.PathValue("id"))
	if s == nil {
		a.mu.Unlock()
		fail(w, 404, "Session not found.")
		return
	}
	// The fingerprint covers user intent (including uploaded bytes), not mutable
	// project reference contents or generated upload paths.
	// This check precedes expensive preparation and repeats under the accept lock.
	identity := body.ClientID
	if identity == "" {
		identity = newID()
	}
	fingerprint := chatFileFingerprint(messageFingerprint(body.Text, body.Mode, body.Sources, body.Mentions, body.Images...), files)
	if previous, ok := a.state.AcceptedMessages[s.ID+"/"+identity]; ok {
		if previous != fingerprint {
			a.mu.Unlock()
			fail(w, 409, "Message identity already belongs to different content.")
			return
		}
		response := a.currentSessionUpdateLocked(s.ID)
		a.mu.Unlock()
		respond(w, 202, response)
		return
	}
	if body.ClientID != "" && a.messageIdentityInUseLocked(identity) {
		a.mu.Unlock()
		fail(w, 409, "Message identity is already in use.")
		return
	}
	if _, available := a.state.conversationDirectory(s); !available {
		a.mu.Unlock()
		fail(w, 404, "Project not found.")
		return
	}
	if a.closing {
		a.mu.Unlock()
		fail(w, 503, "Engine is shutting down.")
		return
	}

	_, ok := a.binaries[s.Harness]
	if !ok {
		a.mu.Unlock()
		fail(w, 400, "Harness is not installed.")
		return
	}
	id, projectID, harness := s.ID, s.ProjectID, s.Harness
	stopVersion := submissionStopVersion
	folder, _ := a.state.conversationDirectory(s)
	if s.Role == sessionRoleGeneralAgent && len(body.Mentions) > 0 {
		a.mu.Unlock()
		fail(w, 400, "Project file references are unavailable in the general conversation.")
		return
	}
	a.mu.Unlock()
	// Filesystem I/O never holds the application mutex. Recheck ownership and
	// turn state after preparation, before accepting a durable user message.
	cwd, err := existingDirectory(folder)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	payload, err := prepareMentions(r.Context(), cwd, harness, body.Text, body.Mentions)
	if err == nil {
		err = prepareChatFiles(&payload, files)
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.mu.Lock()
	s = a.state.session(id)
	if previous, ok := a.state.AcceptedMessages[id+"/"+identity]; ok {
		if previous != fingerprint {
			a.mu.Unlock()
			fail(w, 409, "Message identity already belongs to different content.")
			return
		}
		response := a.currentSessionUpdateLocked(id)
		a.mu.Unlock()
		respond(w, 202, response)
		return
	}
	if body.ClientID != "" && a.messageIdentityInUseLocked(identity) {
		a.mu.Unlock()
		fail(w, 409, "Message identity is already in use.")
		return
	}
	if a.stopVersions[id] != stopVersion {
		a.mu.Unlock()
		fail(w, 409, "Message preparation interrupted by Stop. Send it again when ready.")
		return
	}
	currentFolder, available := a.state.conversationDirectory(s)
	if s == nil || !available || s.ProjectID != projectID || currentFolder != folder || s.Harness != harness {
		a.mu.Unlock()
		fail(w, 409, "Conversation or project changed. Retry the message.")
		return
	}
	if a.closing || a.storageErr != nil || r.Context().Err() != nil {
		a.mu.Unlock()
		fail(w, 503, "Submission interrupted or engine unavailable. Retry the message.")
		return
	}
	legacyImages, imageMetadata, err := a.prepareStoredImagesLocked(id, body.Images, files)
	if err != nil {
		a.mu.Unlock()
		fail(w, 400, err.Error())
		return
	}
	payload.Images = append(payload.Images, legacyImages...)

	prompt, err := a.focusedPrompt(s, body.Text, body.Sources)
	if err != nil {
		a.mu.Unlock()
		fail(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(prompt) == "" && (len(files) > 0 || len(body.Images) > 0) {
		prompt = "User attached files."
	}
	guidance, err := conversationGuidance(s)
	if err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	payload.Text = guidance + "\n\n" + prompt
	if err := validateSubmissionSize(payload, harness); err != nil {
		a.mu.Unlock()
		fail(w, 400, err.Error())
		return
	}
	payload.Text = prompt
	if len(s.Queue) >= 32 {
		a.mu.Unlock()
		fail(w, 409, "Message queue is full. Remove a pending message first.")
		return
	}
	mode, status := body.Mode, "queued"
	if mode == "" {
		mode = "queue"
	}
	if mode == "steer" {
		status = "steering"
	}
	q := QueuedMessage{ID: identity, Text: body.Text, Mode: mode, Status: status, Sources: body.Sources, Mentions: body.Mentions, Files: files, Images: imageMetadata}
	if err := a.commitLocked(func(d *diskState) {
		for _, image := range imageMetadata {
			stored := d.Images[image.ID]
			stored.Accepted = true
			d.Images[image.ID] = stored
		}
		if d.AcceptedMessages == nil {
			d.AcceptedMessages = map[string]string{}
		}
		d.AcceptedMessages[id+"/"+identity] = fingerprint
		next := d.session(id)
		if next.Role == sessionRoleGeneralAgent {
			next.HarnessLocked = true
		}
		next.Queue = append(next.Queue, q)
		next.UpdatedAt = now()
		if d.QueuePayloads == nil {
			d.QueuePayloads = map[string]queuedPayload{}
		}
		d.QueuePayloads[q.ID] = queuedPayload{Submission: payload, Harness: harness, Directory: folder}
	}); err != nil {
		// A journal sync failure can still leave a recoverable accepted frame.
		// Keep its files rather than destroy data referenced after restart.
		retainFiles = a.storageErr != nil
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	retainFiles = true
	a.wakeSteeringLocked(id)
	a.scheduleMessagesLocked()
	response := a.currentSessionUpdateLocked(id)
	a.mu.Unlock()
	respond(w, 202, response)
}

func (a *app) stop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.mu.Lock()
	t, err := a.stopSessionLocked(id, nil)
	a.mu.Unlock()
	if err == nil {
		err = waitSessionStop(r.Context(), t)
	}
	if err != nil {
		failSessionControl(w, err)
		return
	}
	a.mu.Lock()
	s := a.currentSessionUpdateLocked(id)
	err = a.storageErr
	a.mu.Unlock()
	if err != nil {
		failSessionControl(w, err)
		return
	}
	respond(w, 200, s)
}

// Caller holds app.mu. Cancellation precedes persistence, even on write failure.
func (a *app) stopSessionLocked(id string, origin *SpawnOrigin) (*turn, error) {
	if a.state.session(id) == nil {
		return nil, controlError(404, "Session not found.")
	}
	if a.stopVersions == nil {
		a.stopVersions = map[string]uint64{}
	}
	a.stopVersions[id]++
	t := a.runs[id]
	// Cancellation must not depend on a successful history/queue write.
	if t != nil {
		t.cancel()
	}
	if err := a.commitLocked(func(d *diskState) {
		s := d.session(id)
		for i := range s.Queue {
			if s.Queue[i].Status == "queued" || s.Queue[i].Status == "steering" {
				s.Queue[i].Status, s.Queue[i].Error = "paused", "Execution stopped. Send queued messages when ready."
			}
		}
		appendSessionControl(s, "Session stop requested", origin, nil)
	}); err != nil {
		return t, err
	}
	if err := a.cancelLinkedLocked(id); err != nil {
		return t, err
	}
	return t, nil
}

func waitSessionStop(ctx context.Context, t *turn) error {
	if t != nil {
		select {
		case <-t.done:
		case <-ctx.Done():
			return controlError(504, "Cancellation was requested; its final state is uncertain. Read session state before retrying.")
		case <-time.After(20 * time.Second):
			return controlError(504, "Cancellation is still in progress. Read session state before retrying.")
		}
		if t.stopErr != nil {
			return controlError(503, "Could not confirm all session processes stopped: "+t.stopErr.Error())
		}
	}
	return nil
}

func (a *app) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ch := make(chan struct{}, 1)
	since, resumable := parseEventRevision(r)
	a.mu.Lock()
	update := a.sessionUpdateLocked(id, since, !resumable)
	if update == nil {
		a.mu.Unlock()
		fail(w, 404, "Session not found.")
		return
	}
	if a.listeners[id] == nil {
		a.listeners[id] = map[chan struct{}]bool{}
	}
	a.listeners[id][ch] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.listeners[id], ch)
		if len(a.listeners[id]) == 0 {
			delete(a.listeners, id)
		}
		a.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func(update *SessionUpdate) error {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		b, err := json.Marshal(update)
		if err != nil {
			return err
		}
		prefix := []byte("id: " + strconv.FormatUint(update.Revision, 10) + "\ndata: ")
		if _, err := w.Write(append(append(prefix, b...), '\n', '\n')); err != nil {
			return err
		}
		return controller.Flush()
	}
	if send(update) != nil {
		return
	}
	since = update.Revision
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			a.mu.Lock()
			update = a.sessionUpdateLocked(id, since, false)
			a.mu.Unlock()
			if update == nil || send(update) != nil {
				return
			}
			since = update.Revision
		case <-heartbeat.C:
			_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
		}
	}
}

// Node sessions expose their request reply routes, but are not independent chats.
func (a *app) chatSessionHandler(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		s := a.state.session(r.PathValue("id"))
		private := s != nil && s.GraphRunID != ""
		readOnly := s != nil && s.Role == sessionRoleSubagent && r.Method != http.MethodGet
		a.mu.Unlock()
		if private || readOnly {
			fail(w, 404, "Conversation not found.")
			return
		}
		handler(w, r)
	}
}

func (a *app) getConversationGraph(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	s := a.state.session(r.PathValue("id"))
	if s == nil || s.Role == sessionRoleGeneralAgent || s.ParentID != "" || s.GraphRunID != "" {
		a.mu.Unlock()
		fail(w, 404, "Main conversation not found.")
		return
	}
	projection := a.state.graphProjection(s.ID)
	a.mu.Unlock()
	respond(w, 200, projection)
}

func (a *app) selectConversationGraph(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SelectedGraphID *string `json:"selectedGraphId"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.SelectedGraphID == nil {
		fail(w, 400, "Provide selectedGraphId; use an empty string for None.")
		return
	}
	id := r.PathValue("id")
	if err := a.changeConversationGraph(r.Context(), id, *body.SelectedGraphID); err != nil {
		failSessionControl(w, err)
		return
	}
	a.mu.Lock()
	projection := a.state.graphProjection(id)
	a.mu.Unlock()
	respond(w, 200, projection)
}

type GraphRunSummary struct {
	ID             string          `json:"id"`
	ActivityID     string          `json:"activityId"`
	ConversationID string          `json:"conversationId"`
	GraphID        string          `json:"graphId"`
	Status         string          `json:"status"`
	Active         bool            `json:"active"`
	Revision       uint64          `json:"revision"`
	Result         *GraphRunResult `json:"result"`
	CreatedAt      string          `json:"createdAt"`
	EndedAt        string          `json:"endedAt,omitempty"`
}

func (a *app) getGraphRun(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	run := a.state.graphRun(r.PathValue("runID"))
	if run == nil {
		a.mu.Unlock()
		fail(w, 404, "Graph run not found.")
		return
	}
	response := GraphRunSummary{ID: run.ID, ActivityID: run.ActivityID, ConversationID: run.ConversationID, GraphID: run.GraphID, Status: run.Status, Active: graphRunActive(run.Status), Revision: a.state.GraphRevision, Result: run.Result, CreatedAt: run.CreatedAt, EndedAt: run.EndedAt}
	a.mu.Unlock()
	respond(w, 200, response)
}

func (a *app) directory(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5*time.Minute + 10*time.Second))
	select {
	case a.picker <- struct{}{}:
		defer func() { <-a.picker }()
	default:
		fail(w, 409, "A directory picker is already open on the engine computer. Complete or cancel it, then retry.")
		return
	}
	cmd, err := pickerCommand(ctx)
	if err != nil {
		fail(w, 501, err.Error())
		return
	}
	var output cappedBuffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			fail(w, 408, "Directory picker timed out or was cancelled.")
		} else {
			fail(w, 500, "Could not open the native directory picker.")
		}
		return
	}
	path := strings.TrimPrefix(output.String(), "\ufeff")
	if path == "" {
		respond(w, 200, map[string]any{"path": nil})
		return
	}
	path, err = existingDirectory(path)
	if err != nil || !utf8.ValidString(path) || output.truncated {
		fail(w, 500, "Directory picker returned an invalid path.")
		return
	}
	respond(w, 200, map[string]string{"path": path})
}
