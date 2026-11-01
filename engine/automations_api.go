package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"time"
)

func (a *app) automationRoutes(mux *http.ServeMux) {
	if a.store != nil {
		a.sqlAutomationRoutes(mux)
		return
	}
	mux.HandleFunc("GET /api/projects/{id}/automations", a.listAutomations)
	mux.HandleFunc("POST /api/projects/{id}/automations", a.saveAutomation)
	mux.HandleFunc("PATCH /api/projects/{id}/automations/{automationId}", a.saveAutomation)
	mux.HandleFunc("DELETE /api/projects/{id}/automations/{automationId}", a.deleteAutomation)
	mux.HandleFunc("GET /api/projects/{id}/automations/{automationId}/runs", a.listAutomationRuns)
	mux.HandleFunc("POST /api/projects/{id}/automations/{automationId}/run-now", a.runAutomationNow)
	mux.HandleFunc("POST /api/projects/{id}/automations/preview", a.previewAutomation)
	mux.HandleFunc("GET /api/settings/engine", a.engineSettings)
	mux.HandleFunc("PATCH /api/settings/engine", a.engineSettings)
}

func (a *app) listAutomations(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.state.project(r.PathValue("id"))
	if p == nil || p.Removed {
		fail(w, 404, "Project not found.")
		return
	}
	items := []any{}
	for _, v := range a.state.Automations {
		if v.ProjectID != p.ID || v.Deleted {
			continue
		}
		queued, active := 0, 0
		reason := ""
		for _, t := range a.state.AutomationTriggers {
			if t.AutomationID == v.ID && !t.Consumed {
				queued++
				if reason == "" {
					reason = t.BlockedReason
				}
			}
		}
		for _, run := range a.state.AutomationRuns {
			if run.AutomationID == v.ID && run.Status == "running" {
				active++
			}
		}
		if reason == "" && queued > 0 && automationUsesSession(v) {
			reason = a.automationSessionBlocked(v.ProjectID, v.SessionID)
		}
		items = append(items, map[string]any{"automation": v, "queued": queued, "active": active, "blockedReason": reason})
	}
	respond(w, 200, map[string]any{"items": items, "timeZone": a.state.engineZone(), "topics": p.Topics})
}

func (a *app) validateAutomationResources(v Automation, p Project) error {
	if !automationUsesSession(v) {
		if _, ok := a.binaries[v.Harness]; !ok {
			return errors.New("Selected harness is unavailable.")
		}
		selection := Session{ProjectID: p.ID, Harness: v.Harness, Model: v.Model, Effort: v.Effort}
		catalog, err := a.catalog(a.ctx, selection, p.Folder, false)
		if err != nil {
			return err
		}
		if err = validateSpawnModel(selection, "", catalog); err != nil {
			return err
		}
	}
	a.authoringMu.Lock()
	authoring, err := loadAuthoring(p)
	a.authoringMu.Unlock()
	if err != nil {
		return err
	}
	for _, id := range v.Graphs {
		if !slices.ContainsFunc(authoring.Graphs, func(g GraphRecord) bool { return g.ID == id && g.Definition.Enabled }) {
			return errors.New("A selected graph is unavailable or disabled.")
		}
	}
	if automationUsesSession(v) {
		return nil
	}
	_, skills, err := scanSkillLibrary()
	if err != nil {
		return err
	}
	for _, id := range v.Selection.Skills {
		if !slices.ContainsFunc(skills, func(s SkillLibraryEntry) bool { return s.ID == id && s.Valid }) {
			return errors.New("A selected skill is missing or invalid.")
		}
	}
	return nil
}

func (a *app) saveAutomation(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(modelCatalogTimeout + 15*time.Second))
	var body struct {
		Automation Automation `json:"automation"`
		Enabled    *bool      `json:"enabled,omitempty"`
		Revision   uint64     `json:"revision,omitempty"`
		NewTopic   *struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"newTopic,omitempty"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Enabled != nil {
		a.toggleAutomation(w, r, *body.Enabled, body.Revision)
		return
	}
	v := body.Automation
	if v.AgentConfiguration != nil {
		fail(w, 400, "Captured specialist configuration is engine-owned.")
		return
	}
	requestData, _ := json.Marshal(body)
	requestRevision := revision(requestData)
	if r.PathValue("automationId") == "" {
		a.mu.Lock()
		for _, existing := range a.state.Automations {
			if existing.ProjectID == r.PathValue("id") && existing.OperationID == v.OperationID {
				a.mu.Unlock()
				if existing.CreationRequest != requestRevision {
					fail(w, 409, "operationId already belongs to a different automation request.")
				} else {
					respond(w, 200, existing)
				}
				return
			}
		}
		a.mu.Unlock()
	}
	v.SessionMode = automationSessionMode(v)
	a.mu.Lock()
	previous := a.state.automation(r.PathValue("automationId"))
	if previous != nil && previous.ProjectID == r.PathValue("id") {
		if previous.Revision != v.Revision {
			a.mu.Unlock()
			fail(w, 409, "Automation changed. Reload before saving.")
			return
		}
		if automationSessionMode(*previous) != "new_per_run" && previous.SessionID != "" && (v.SessionMode != automationSessionMode(*previous) || v.SessionID != previous.SessionID) {
			a.mu.Unlock()
			fail(w, 409, "The linked session is fixed. Create another automation to use a different session.")
			return
		}
		if v.SessionMode != automationSessionMode(*previous) || v.SessionID != previous.SessionID {
			for _, run := range a.state.AutomationRuns {
				if run.AutomationID == previous.ID && run.Status == "running" {
					a.mu.Unlock()
					fail(w, 409, "Wait for the active run before changing its session mode.")
					return
				}
			}
		}
	}
	if v.SessionMode == "reuse_created" && v.SessionID != "" && (previous == nil || previous.SessionID != v.SessionID) {
		a.mu.Unlock()
		fail(w, 400, "The reusable session is linked automatically on its first run.")
		return
	}
	if v.SessionMode == "new_per_run" {
		v.SessionID = ""
	}
	if automationUsesSession(v) {
		if err := a.state.automationSessionEligible(r.PathValue("id"), v.SessionID); err != nil {
			a.mu.Unlock()
			fail(w, 400, err.Error())
			return
		}
		if body.NewTopic != nil {
			a.mu.Unlock()
			fail(w, 400, "An existing session keeps its own topic and settings.")
			return
		}
		captureAutomationSessionSettings(&v, a.state.session(v.SessionID))
	}
	a.mu.Unlock()
	if err := validateAutomationConfig(&v); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !validLinkedText(v.OperationID, 200) {
		fail(w, 400, "Provide an operationId.")
		return
	}
	var topic *Topic
	if body.NewTopic != nil {
		name, ok := cleanLabel(body.NewTopic.Name, 60)
		if !ok || !validTopicColor(body.NewTopic.Color) {
			fail(w, 400, "Invalid topic name or color.")
			return
		}
		topic = &Topic{ID: newID(), Name: name, Color: body.NewTopic.Color, CreatedAt: now()}
	}
	a.mu.Lock()
	p := a.state.project(r.PathValue("id"))
	if p == nil || p.Removed {
		a.mu.Unlock()
		fail(w, 404, "Project not found.")
		return
	}
	project := *p
	a.mu.Unlock()
	if err := a.validateAutomationResources(v, project); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !automationUsesSession(v) {
		catalog, err := a.catalog(r.Context(), Session{ProjectID: project.ID, Harness: v.Harness}, project.Folder, false)
		if err != nil {
			fail(w, 502, "Could not resolve the model configuration.")
			return
		}
		if v.Model == "" {
			v.Model = catalog.DefaultModel
		}
		if v.Model == "" {
			fail(w, 400, "Choose an explicit model; this harness has no known default.")
			return
		}
		if v.Effort == "" {
			if option := catalog.find(v.Model); option != nil {
				v.Effort = option.DefaultEffort
			}
			if v.Model == catalog.DefaultModel && catalog.DefaultEffort != "" {
				v.Effort = catalog.DefaultEffort
			}
		}
	}
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	p = a.state.project(project.ID)
	if p == nil || p.Removed || p.Folder != project.Folder {
		fail(w, 409, "Project changed.")
		return
	}
	if len(a.state.LibraryDeletions) > 0 {
		fail(w, 409, "Finish Library recovery before saving.")
		return
	}
	if automationUsesSession(v) {
		if err := a.state.automationSessionEligible(p.ID, v.SessionID); err != nil {
			fail(w, 409, err.Error())
			return
		}
		captureAutomationSessionSettings(&v, a.state.session(v.SessionID))
	} else {
		for _, selected := range v.Selection.MCPConnections {
			c, ok := a.state.MCPConnections[selected.ConnectionID]
			if !ok {
				fail(w, 400, "Selected MCP connection is missing.")
				return
			}
			for _, name := range selected.Tools {
				found := false
				if c.Catalog != nil {
					for _, tool := range c.Catalog.Tools {
						if tool != nil && tool.Name == name {
							found = true
						}
					}
				}
				if !found {
					fail(w, 400, "Select tools from the known MCP catalog.")
					return
				}
			}
		}
	}
	id := r.PathValue("automationId")
	old := a.state.automation(id)
	if id != "" && (old == nil || old.Deleted || old.ProjectID != p.ID) {
		fail(w, 404, "Automation not found.")
		return
	}
	if old == nil {
		for _, existing := range a.state.Automations {
			if existing.ProjectID == p.ID && existing.OperationID == v.OperationID {
				if existing.CreationRequest != requestRevision {
					fail(w, 409, "operationId already belongs to a different automation request.")
				} else {
					respond(w, 200, existing)
				}
				return
			}
		}
		v.ID, v.ProjectID, v.Revision = newID(), p.ID, 1
		v.CreationRequest = requestRevision
	} else {
		if old.Revision != v.Revision {
			fail(w, 409, "Automation changed. Reload before saving.")
			return
		}
		v.ID, v.ProjectID, v.Revision, v.OperationID = old.ID, old.ProjectID, old.Revision+1, old.OperationID
		v.CreationRequest = old.CreationRequest
	}
	v.Deleted = false
	if topic != nil {
		v.TopicID = topic.ID
	} else if t := p.topic(v.TopicID); !automationUsesSession(v) && (t == nil || t.Archived) {
		fail(w, 400, "Select an active topic or create a new one.")
		return
	}
	v.AfterSequence = a.state.WebhookReceivedSequence
	if old != nil && old.Enabled && v.Enabled {
		v.AfterSequence = old.AfterSequence
	}
	reset := old == nil || !reflect.DeepEqual(old.Schedule, v.Schedule) || old.Mode != v.Mode
	if reset {
		v.OnceTriggeredAt = ""
	} else {
		v.OnceTriggeredAt = old.OnceTriggeredAt
	}
	if reset && v.Mode == "schedule" && v.Schedule.Recurrence == "once" && nextAutomationOccurrence(v.Schedule, a.state.engineZone(), time.Now()).IsZero() {
		fail(w, 400, "Choose a future date and valid local time for the one-time schedule.")
		return
	}
	resetAutomationSchedule(&v, a.state.engineZone(), time.Now(), reset)
	if old != nil && old.Enabled && v.Enabled && !reset {
		v.NextScheduledAt = old.NextScheduledAt
	}
	if err := a.commitLocked(func(d *diskState) {
		if topic != nil {
			d.project(p.ID).Topics = append(d.project(p.ID).Topics, *topic)
		}
		if old == nil {
			d.Automations = append(d.Automations, v)
		} else {
			*d.automation(v.ID) = v
		}
	}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, v)
}

func (a *app) toggleAutomation(w http.ResponseWriter, r *http.Request, enabled bool, revision uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := a.state.automation(r.PathValue("automationId"))
	p := a.state.project(r.PathValue("id"))
	if v == nil || v.Deleted || p == nil || p.Removed || v.ProjectID != p.ID {
		fail(w, 404, "Automation not found.")
		return
	}
	if v.Revision != revision {
		fail(w, 409, "Automation changed. Reload before saving.")
		return
	}
	if err := a.commitLocked(func(d *diskState) {
		next := d.automation(v.ID)
		if !next.Enabled && enabled {
			next.AfterSequence = d.WebhookReceivedSequence
		}
		next.Enabled = enabled
		next.Revision++
		resetAutomationSchedule(next, d.engineZone(), time.Now(), false)
	}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, a.state.automation(v.ID))
}

func (a *app) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision uint64 `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if p := a.state.project(r.PathValue("id")); p == nil || p.Removed {
		fail(w, 404, "Project not found.")
		return
	}
	v := a.state.automation(r.PathValue("automationId"))
	if v == nil || v.ProjectID != r.PathValue("id") || v.Deleted {
		fail(w, 404, "Automation not found.")
		return
	}
	if v.Revision != body.Revision {
		fail(w, 409, "Automation changed.")
		return
	}
	if err := a.commitLocked(func(d *diskState) {
		next := d.automation(v.ID)
		next.Deleted, next.Enabled, next.NextScheduledAt = true, false, ""
		next.Revision++
		for i := range d.AutomationTriggers {
			if d.AutomationTriggers[i].AutomationID == v.ID {
				d.AutomationTriggers[i].Consumed = true
			}
		}
	}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, map[string]bool{"deleted": true})
}

func (a *app) listAutomationRuns(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := a.state.automation(r.PathValue("automationId"))
	if v == nil || v.ProjectID != r.PathValue("id") {
		fail(w, 404, "Automation not found.")
		return
	}
	if p := a.state.project(v.ProjectID); p == nil || p.Removed {
		fail(w, 404, "Project not found.")
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		fail(w, 400, "Invalid offset.")
		return
	}
	items := []any{}
	count := 0
	for i := len(a.state.AutomationRuns) - 1; i >= 0; i-- {
		run := a.state.AutomationRuns[i]
		if run.AutomationID != v.ID {
			continue
		}
		if count >= offset && len(items) < 50 {
			if session := a.state.session(run.SessionID); session != nil {
				items = append(items, map[string]any{"runId": run.ID, "sessionId": session.ID, "title": session.Title})
			}
		}
		count++
	}
	respond(w, 200, map[string]any{"items": items, "hasMore": count > offset+len(items), "nextOffset": offset + len(items)})
}

func (a *app) runAutomationNow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision    uint64 `json:"revision"`
		OperationID string `json:"operationId"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !validLinkedText(body.OperationID, 200) {
		fail(w, 400, "Provide an operationId.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	v := a.state.automation(r.PathValue("automationId"))
	if v == nil || v.Deleted || v.ProjectID != r.PathValue("id") || v.Mode != "schedule" {
		fail(w, 404, "Scheduled automation not found.")
		return
	}
	for _, t := range a.state.AutomationTriggers {
		if t.AutomationID == v.ID && t.Key == "manual/"+body.OperationID {
			respond(w, 202, map[string]any{"accepted": true, "triggerId": t.ID})
			return
		}
	}
	if v.Revision != body.Revision {
		fail(w, 409, "Automation changed.")
		return
	}
	p := a.state.project(v.ProjectID)
	if p == nil || p.Removed {
		fail(w, 409, "Project unavailable.")
		return
	}
	var id string
	if err := a.commitLocked(func(d *diskState) {
		id = enqueueAutomation(d, *v, "manual/"+body.OperationID, "manual", now(), nil, nil)
	}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 202, map[string]any{"accepted": true, "triggerId": id})
}

func (a *app) previewAutomation(w http.ResponseWriter, r *http.Request) {
	var schedule AutomationSchedule
	if !decode(w, r, &schedule) {
		return
	}
	if err := validateAutomationSchedule(schedule); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.mu.Lock()
	zone := a.state.engineZone()
	p := a.state.project(r.PathValue("id"))
	available := p != nil && !p.Removed
	a.mu.Unlock()
	if !available {
		fail(w, 404, "Project not found.")
		return
	}
	v := Automation{Mode: "schedule", Enabled: true, Schedule: schedule}
	resetAutomationSchedule(&v, zone, time.Now(), false)
	if schedule.Recurrence == "once" && v.NextScheduledAt == "" {
		fail(w, 400, "Choose a future date and valid local time.")
		return
	}
	respond(w, 200, map[string]string{"nextScheduledAt": v.NextScheduledAt, "timeZone": zone})
}

func (a *app) engineSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		a.mu.Lock()
		defer a.mu.Unlock()
		settings := a.state.EngineSettings
		settings.TimeZone = a.state.engineZone()
		respond(w, 200, settings)
		return
	}
	var body EngineSettings
	if !decode(w, r, &body) {
		return
	}
	if body.TimeZone == "" || body.TimeZone == "Local" || len(body.TimeZone) > 100 {
		fail(w, 400, "Choose an IANA time zone.")
		return
	}
	if _, err := time.LoadLocation(body.TimeZone); err != nil {
		fail(w, 400, "Choose an IANA time zone.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if body.Revision != a.state.EngineSettings.Revision {
		fail(w, 409, "Engine settings changed.")
		return
	}
	body.Revision++
	if err := a.commitLocked(func(d *diskState) {
		d.EngineSettings = body
		for i := range d.Automations {
			resetAutomationSchedule(&d.Automations[i], body.TimeZone, time.Now(), false)
		}
	}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, body)
}

// Stable copies keep queued recovery snapshots independent of authoring edits.
func copyAutomation(v Automation) Automation {
	data, _ := json.Marshal(v)
	var copy Automation
	_ = json.Unmarshal(data, &copy)
	return copy
}
