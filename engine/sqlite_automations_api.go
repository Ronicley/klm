package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"time"
)

// Contract 3 carries revisions/sequences as decimal strings, including nested
// editable configuration. The persisted domain types retain their uint64 values.
type sqlAutomationWire struct {
	Automation
	Revision      string `json:"revision"`
	AfterSequence string `json:"afterSequence"`
}

func automationSQLView(v Automation) sqlAutomationWire {
	return sqlAutomationWire{v, sqliteRevision(v.Revision), sqliteRevision(v.AfterSequence)}
}
func sqlExpectedRevision(raw string) (uint64, error) {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || sqliteRevision(n) != raw {
		return 0, controlError(400, "Use the returned decimal revision.")
	}
	return n, nil
}
func sqlOffset(r *http.Request) (int, error) {
	if r.URL.Query().Get("offset") == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || n < 0 {
		return 0, controlError(400, "Invalid offset.")
	}
	return n, nil
}
func (st *sqliteSessionTx) engineSettings() (EngineSettings, error) {
	var v EngineSettings
	err := decodeSQLiteRecord(st.ctx, st.tx, "engine_settings", "settings", 64<<10, &v)
	if v.TimeZone == "" {
		v.TimeZone = defaultEngineTimeZone
	}
	return v, err
}
func (st *sqliteSessionTx) automation(project, id string, includeDeleted bool) (Automation, error) {
	var v Automation
	available, err := st.projectAvailable(project)
	if err != nil {
		return v, err
	}
	if !available {
		return v, sql.ErrNoRows
	}
	err = decodeSQLiteRecord(st.ctx, st.tx, "automations", id, 8<<20, &v)
	if err == nil && (v.ProjectID != project || v.Deleted && !includeDeleted) {
		err = sql.ErrNoRows
	}
	return v, err
}
func (st *sqliteSessionTx) automationSessionEligible(project, id string) (Session, error) {
	s, err := st.sessionMeta(id)
	if err != nil {
		return s, err
	}
	if s.ProjectID != project || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
		return s, controlError(400, "Choose a normal top-level session in this project.")
	}
	available, err := st.projectAvailable(project)
	if err != nil {
		return s, err
	}
	archived, err := st.effectivelyArchived(s)
	if err != nil {
		return s, err
	}
	if !available || archived {
		return s, controlError(409, "Restore the session, topic and project before using this automation.")
	}
	err = decodeSQLiteRecord(st.ctx, st.tx, "capability_selections", sqliteOwnedKey(sqliteOwnedKey(id, ""), ""), 8<<20, &s.Capabilities.Desired)
	return s, err
}

func (a *app) sqlAutomationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{id}/automations", a.listSQLAutomations)
	mux.HandleFunc("GET /api/projects/{id}/automations/{automationId}", a.getSQLAutomation)
	mux.HandleFunc("POST /api/projects/{id}/automations", a.saveSQLAutomation)
	mux.HandleFunc("PATCH /api/projects/{id}/automations/{automationId}", a.saveSQLAutomation)
	mux.HandleFunc("DELETE /api/projects/{id}/automations/{automationId}", a.deleteSQLAutomation)
	mux.HandleFunc("GET /api/projects/{id}/automations/{automationId}/runs", a.listSQLAutomationRuns)
	mux.HandleFunc("POST /api/projects/{id}/automations/{automationId}/run-now", a.runSQLAutomationNow)
	mux.HandleFunc("POST /api/projects/{id}/automations/preview", a.previewSQLAutomation)
	mux.HandleFunc("GET /api/settings/engine", a.sqlEngineSettings)
	mux.HandleFunc("PATCH /api/settings/engine", a.sqlEngineSettings)
}

func (a *app) listSQLAutomations(w http.ResponseWriter, r *http.Request) {
	offset, err := sqlOffset(r)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	limit, err := sqlPageLimit(r, 50)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	items := []any{}
	more := false
	zone := ""
	a.mu.Lock()
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		available, err := st.projectAvailable(r.PathValue("id"))
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		settings, err := st.engineSettings()
		if err != nil {
			return err
		}
		zone = settings.TimeZone
		ids, err := sqliteQueryIDs(st.ctx, tx, `SELECT id FROM data_automations WHERE project_id=? AND x_deleted<>'true' ORDER BY position,id LIMIT ? OFFSET ?`, r.PathValue("id"), limit+1, offset)
		if err != nil {
			return err
		}
		more = len(ids) > limit
		if more {
			ids = ids[:limit]
		}
		bytes := 0
		for _, id := range ids {
			v, err := st.automation(r.PathValue("id"), id, false)
			if err != nil {
				return err
			}
			var queued, active int
			if err := tx.QueryRowContext(st.ctx, `SELECT (SELECT count(*) FROM data_automation_triggers WHERE x_automationId=? AND x_consumed<>'true'),(SELECT count(*) FROM data_automation_runs WHERE x_automationId=? AND status='running')`, id, id).Scan(&queued, &active); err != nil {
				return err
			}
			reason := ""
			if queued > 0 && automationUsesSession(v) {
				_, reason, err = a.sqlAutomationSessionBlocked(st, v.ProjectID, v.SessionID)
				if err != nil {
					return err
				}
			}
			// Lists carry editable configurations but never growing run/trigger history.
			item := map[string]any{"automation": automationSQLView(v), "queued": queued, "active": active, "blockedReason": reason}
			encoded, err := json.Marshal(item)
			if err != nil {
				return err
			}
			if bytes+len(encoded) > 2<<20 && len(items) > 0 {
				more = true
				break
			}
			items = append(items, item)
			bytes += len(encoded)
		}
		return nil
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "timeZone": zone, "hasMore": more, "nextOffset": offset + len(items)})
}
func (a *app) getSQLAutomation(w http.ResponseWriter, r *http.Request) {
	var v Automation
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		var err error
		v, err = st.automation(r.PathValue("id"), r.PathValue("automationId"), false)
		return err
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, automationSQLView(v))
}

type sqlSaveAutomationBody struct {
	Automation sqlAutomationWire `json:"automation"`
	Enabled    *bool             `json:"enabled,omitempty"`
	Revision   string            `json:"revision,omitempty"`
	NewTopic   *struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	} `json:"newTopic,omitempty"`
}

// The same validation is applied before external catalog/filesystem reads and
// again in the final transaction; no optimistic settings update can break links.
func (st *sqliteSessionTx) prepareAutomationSave(project, id string, v *Automation, newTopic bool) (*Automation, error) {
	var old *Automation
	if id != "" {
		previous, err := st.automation(project, id, false)
		if err != nil {
			return nil, err
		}
		old = &previous
		if old.Revision != v.Revision || old.Revision == ^uint64(0) {
			return nil, controlError(409, "Automation changed. Reload before saving.")
		}
		if automationSessionMode(*old) != "new_per_run" && old.SessionID != "" && (v.SessionMode != automationSessionMode(*old) || v.SessionID != old.SessionID) {
			return nil, controlError(409, "The linked session is fixed. Create another automation to use a different session.")
		}
		if v.SessionMode != automationSessionMode(*old) || v.SessionID != old.SessionID {
			var active bool
			if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_automation_runs WHERE x_automationId=? AND status='running')`, id).Scan(&active); err != nil {
				return nil, err
			}
			if active {
				return nil, controlError(409, "Wait for the active run before changing its session mode.")
			}
		}
	}
	if v.SessionMode == "reuse_created" && v.SessionID != "" && (old == nil || old.SessionID != v.SessionID) {
		return nil, controlError(400, "The reusable session is linked automatically on its first run.")
	}
	if v.SessionMode == "new_per_run" {
		v.SessionID = ""
	}
	if automationUsesSession(*v) {
		if newTopic {
			return nil, controlError(400, "An existing session keeps its own topic and settings.")
		}
		s, err := st.automationSessionEligible(project, v.SessionID)
		if err != nil {
			return nil, err
		}
		captureAutomationSessionSettings(v, &s)
	}
	return old, nil
}
func (st *sqliteSessionTx) automationCreationReceipt(project, operation, fingerprint string) (*Automation, error) {
	var id string
	err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_automations WHERE project_id=? AND x_operationId=? LIMIT 1`, project, operation).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v Automation
	if err := decodeSQLiteRecord(st.ctx, st.tx, "automations", id, 8<<20, &v); err != nil {
		return nil, err
	}
	if v.CreationRequest != fingerprint {
		return nil, controlError(409, "operationId already belongs to a different automation request.")
	}
	return &v, nil
}
func (st *sqliteSessionTx) validateAutomationMCP(selection CapabilitySelection) error {
	for _, selected := range selection.MCPConnections {
		var c MCPConnection
		if err := decodeSQLiteRecord(st.ctx, st.tx, "mcp_connections", selected.ConnectionID, 8<<20, &c); errors.Is(err, sql.ErrNoRows) {
			return controlError(400, "Selected MCP connection is missing.")
		} else if err != nil {
			return err
		}
		for _, name := range selected.Tools {
			found := false
			if c.Catalog != nil {
				for _, tool := range c.Catalog.Tools {
					if tool != nil && tool.Name == name {
						found = true
						break
					}
				}
			}
			if !found {
				return controlError(400, "Select tools from the known MCP catalog.")
			}
		}
	}
	return nil
}
func (a *app) saveSQLAutomation(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(modelCatalogTimeout + 15*time.Second))
	var body sqlSaveAutomationBody
	if !decode(w, r, &body) {
		return
	}
	if body.Enabled != nil {
		a.toggleSQLAutomation(w, r, *body.Enabled, body.Revision)
		return
	}
	v := body.Automation.Automation
	if v.AgentConfiguration != nil {
		fail(w, 400, "Captured specialist configuration is engine-owned.")
		return
	}
	if body.Automation.Revision != "" {
		n, err := sqlExpectedRevision(body.Automation.Revision)
		if err != nil {
			sqlHTTPError(w, err)
			return
		}
		v.Revision = n
	}
	raw, _ := json.Marshal(body)
	fingerprint := revision(raw)
	projectID, id := r.PathValue("id"), r.PathValue("automationId")
	var project Project
	var receipt *Automation
	v.SessionMode = automationSessionMode(v)
	a.mu.Lock()
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		var err error
		project, err = st.project(projectID)
		if err != nil {
			return err
		}
		if project.Removed {
			return sql.ErrNoRows
		}
		if id == "" {
			receipt, err = st.automationCreationReceipt(projectID, v.OperationID, fingerprint)
			if err != nil || receipt != nil {
				return err
			}
		}
		_, err = st.prepareAutomationSave(projectID, id, &v, body.NewTopic != nil)
		return err
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	if receipt != nil {
		respond(w, 200, automationSQLView(*receipt))
		return
	}
	if err := validateAutomationConfig(&v); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !validLinkedText(v.OperationID, 200) {
		fail(w, 400, "Provide an operationId.")
		return
	}
	if body.NewTopic != nil {
		name, ok := cleanLabel(body.NewTopic.Name, 60)
		if !ok || !validTopicColor(body.NewTopic.Color) {
			fail(w, 400, "Invalid topic name or color.")
			return
		}
		body.NewTopic.Name = name
	}
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
	a.mu.Lock()
	err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		current, err := st.project(projectID)
		if err != nil {
			return err
		}
		if current.Removed || current.Folder != project.Folder {
			return controlError(409, "Project changed.")
		}
		if id == "" {
			receipt, err = st.automationCreationReceipt(projectID, v.OperationID, fingerprint)
			if err != nil {
				return err
			}
			if receipt != nil {
				v = *receipt
				return nil
			}
		}
		var recovering bool
		if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&recovering); err != nil {
			return err
		}
		if recovering {
			return controlError(409, "Finish Library recovery before saving.")
		}
		old, err := st.prepareAutomationSave(projectID, id, &v, body.NewTopic != nil)
		if err != nil {
			return err
		}
		if !automationUsesSession(v) {
			if err := st.validateAutomationMCP(v.Selection); err != nil {
				return err
			}
		}
		if old == nil {
			v.ID, v.ProjectID, v.Revision, v.CreationRequest = newID(), projectID, 1, fingerprint
		} else {
			v.ID, v.ProjectID, v.Revision, v.OperationID, v.CreationRequest = old.ID, old.ProjectID, old.Revision+1, old.OperationID, old.CreationRequest
		}
		v.Deleted = false
		if body.NewTopic != nil {
			topic, err := st.createTopic(projectID, body.NewTopic.Name, body.NewTopic.Color, "")
			if err != nil {
				return err
			}
			v.TopicID = topic.ID
		} else if !automationUsesSession(v) {
			topic, _, err := st.topic(projectID, v.TopicID)
			if err != nil {
				return err
			}
			if topic.Archived {
				return controlError(400, "Select an active topic or create a new one.")
			}
		}
		v.AfterSequence, err = st.sequence("webhook_received_sequence")
		if err != nil {
			return err
		}
		if old != nil && old.Enabled && v.Enabled {
			v.AfterSequence = old.AfterSequence
		}
		reset := old == nil || !reflect.DeepEqual(old.Schedule, v.Schedule) || old.Mode != v.Mode
		if reset {
			v.OnceTriggeredAt = ""
		} else {
			v.OnceTriggeredAt = old.OnceTriggeredAt
		}
		settings, err := st.engineSettings()
		if err != nil {
			return err
		}
		if reset && v.Mode == "schedule" && v.Schedule.Recurrence == "once" && nextAutomationOccurrence(v.Schedule, settings.TimeZone, time.Now()).IsZero() {
			return controlError(400, "Choose a future date and valid local time for the one-time schedule.")
		}
		resetAutomationSchedule(&v, settings.TimeZone, time.Now(), reset)
		if old != nil && old.Enabled && v.Enabled && !reset {
			v.NextScheduledAt = old.NextScheduledAt
		}
		return st.putRoot("automations", v.ID, v)
	})
	a.mu.Unlock()
	a.libraryMu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, automationSQLView(v))
}

func (a *app) toggleSQLAutomation(w http.ResponseWriter, r *http.Request, enabled bool, expected string) {
	rev, err := sqlExpectedRevision(expected)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	var v Automation
	a.mu.Lock()
	err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		var err error
		v, err = st.automation(r.PathValue("id"), r.PathValue("automationId"), false)
		if err != nil {
			return err
		}
		if v.Revision != rev || v.Revision == ^uint64(0) {
			return controlError(409, "Automation changed. Reload before saving.")
		}
		if !v.Enabled && enabled {
			v.AfterSequence, err = st.sequence("webhook_received_sequence")
			if err != nil {
				return err
			}
		}
		v.Enabled = enabled
		v.Revision++
		settings, err := st.engineSettings()
		if err != nil {
			return err
		}
		resetAutomationSchedule(&v, settings.TimeZone, time.Now(), false)
		return st.putRoot("automations", v.ID, v)
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, automationSQLView(v))
}
func (a *app) deleteSQLAutomation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision string `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	rev, err := sqlExpectedRevision(body.Revision)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.mu.Lock()
	err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		v, err := st.automation(r.PathValue("id"), r.PathValue("automationId"), false)
		if err != nil {
			return err
		}
		if v.Revision != rev || v.Revision == ^uint64(0) {
			return controlError(409, "Automation changed.")
		}
		v.Deleted, v.Enabled, v.NextScheduledAt = true, false, ""
		v.Revision++
		if err := st.putRoot("automations", v.ID, v); err != nil {
			return err
		}
		return st.eachMatching("automation_triggers", `x_automationId=? AND x_consumed<>'true'`, []any{v.ID}, func(id string) error {
			var t AutomationTrigger
			if err := decodeSQLiteRecord(st.ctx, st.tx, "automation_triggers", id, 32<<20, &t); err != nil {
				return err
			}
			t.Consumed = true
			return st.putRoot("automation_triggers", id, t)
		})
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"deleted": true})
}
func (a *app) listSQLAutomationRuns(w http.ResponseWriter, r *http.Request) {
	offset, err := sqlOffset(r)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	items := []map[string]string{}
	more := false
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		v, err := st.automation(r.PathValue("id"), r.PathValue("automationId"), true)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(st.ctx, `SELECT r.id,s.id,s.x_title FROM data_automation_runs r JOIN data_sessions s ON s.id=r.session_id WHERE r.x_automationId=? ORDER BY r.position DESC,r.id LIMIT 51 OFFSET ?`, v.ID, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, session, title string
			if err := rows.Scan(&id, &session, &title); err != nil {
				return err
			}
			if len(items) == 50 {
				more = true
				break
			}
			items = append(items, map[string]string{"runId": id, "sessionId": session, "title": title})
		}
		return rows.Err()
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "hasMore": more, "nextOffset": offset + len(items)})
}
func (a *app) runSQLAutomationNow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision    string `json:"revision"`
		OperationID string `json:"operationId"`
	}
	if !decode(w, r, &body) {
		return
	}
	rev, err := sqlExpectedRevision(body.Revision)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	if !validLinkedText(body.OperationID, 200) {
		fail(w, 400, "Provide an operationId.")
		return
	}
	id := ""
	a.mu.Lock()
	err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		v, err := st.automation(r.PathValue("id"), r.PathValue("automationId"), false)
		if err != nil {
			return err
		}
		if v.Mode != "schedule" {
			return controlError(404, "Scheduled automation not found.")
		}
		err = st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_automation_triggers WHERE x_automationId=? AND x_key=? LIMIT 1`, v.ID, "manual/"+body.OperationID).Scan(&id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if v.Revision != rev {
			return controlError(409, "Automation changed.")
		}
		id, err = st.enqueueAutomation(v, "manual/"+body.OperationID, "manual", now(), nil, nil)
		return err
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 202, map[string]any{"accepted": true, "triggerId": id})
}
func (a *app) previewSQLAutomation(w http.ResponseWriter, r *http.Request) {
	var schedule AutomationSchedule
	if !decode(w, r, &schedule) {
		return
	}
	if err := validateAutomationSchedule(schedule); err != nil {
		fail(w, 400, err.Error())
		return
	}
	zone := ""
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		available, err := st.projectAvailable(r.PathValue("id"))
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		settings, err := st.engineSettings()
		zone = settings.TimeZone
		return err
	})
	if err != nil {
		sqlHTTPError(w, err)
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
func (a *app) sqlEngineSettings(w http.ResponseWriter, r *http.Request) {
	var settings EngineSettings
	var err error
	if r.Method == http.MethodGet {
		err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
			st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
			settings, err = st.engineSettings()
			return err
		})
	} else {
		var body struct {
			TimeZone string `json:"timeZone"`
			Revision string `json:"revision"`
		}
		if !decode(w, r, &body) {
			return
		}
		rev, e := sqlExpectedRevision(body.Revision)
		if e != nil {
			sqlHTTPError(w, e)
			return
		}
		if body.TimeZone == "" || body.TimeZone == "Local" || len(body.TimeZone) > 100 {
			fail(w, 400, "Choose an IANA time zone.")
			return
		}
		if _, e := time.LoadLocation(body.TimeZone); e != nil {
			fail(w, 400, "Choose an IANA time zone.")
			return
		}
		a.mu.Lock()
		err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
			current, err := st.engineSettings()
			if err != nil {
				return err
			}
			if current.Revision != rev || rev == ^uint64(0) {
				return controlError(409, "Engine settings changed.")
			}
			settings = EngineSettings{TimeZone: body.TimeZone, Revision: rev + 1}
			if err := st.putRoot("engine_settings", "settings", settings); err != nil {
				return err
			}
			at := time.Now()
			return st.eachMatching("automations", `x_mode='schedule'`, nil, func(id string) error {
				var v Automation
				if err := decodeSQLiteRecord(st.ctx, st.tx, "automations", id, 8<<20, &v); err != nil {
					return err
				}
				resetAutomationSchedule(&v, settings.TimeZone, at, false)
				return st.putRoot("automations", id, v)
			})
		})
		a.mu.Unlock()
	}
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]string{"timeZone": settings.TimeZone, "revision": sqliteRevision(settings.Revision)})
}
