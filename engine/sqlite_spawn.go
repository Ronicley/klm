package main

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
)

func (st *sqliteSessionTx) spawnSource(id string) (Session, error) {
	s, err := st.sessionMeta(id)
	if err != nil {
		return s, err
	}
	if run, err := st.activeAutomation(id); err != nil {
		return s, err
	} else if run != nil {
		s.Harness, s.Model, s.Effort, s.YOLO = run.Snapshot.Harness, run.Snapshot.Model, run.Snapshot.Effort, run.Snapshot.YOLO
	}
	return s, nil
}

func (st *sqliteSessionTx) sourceTopic(s Session) (*Topic, error) {
	if s.Role == sessionRoleSideAgent {
		parent, err := st.sessionMeta(s.ParentID)
		if err != nil {
			return nil, err
		}
		s = parent
	}
	if s.TopicID == "" {
		return nil, nil
	}
	t, _, err := st.topic(s.ProjectID, s.TopicID)
	return &t, err
}

func (st *sqliteSessionTx) inheritedSelection(id string) (CapabilitySelection, error) {
	key := sqliteOwnedKey(id, "")
	var applied *CapabilitySnapshot
	if err := readSQLCapabilityView(st.ctx, st.tx, "capability_applied", sqliteOwnedKey(key, ""), &applied); err != nil {
		return CapabilitySelection{}, err
	}
	if applied != nil {
		return copySelection(applied.Selection), nil
	}
	var desired CapabilitySelection
	err := decodeSQLiteRecord(st.ctx, st.tx, "capability_selections", sqliteOwnedKey(key, ""), 8<<20, &desired)
	return desired, err
}

func (st *sqliteSessionTx) existingSpawn(from Session, args spawnArgs) (*SessionSpawn, error) {
	if !spawnSender(&from, args) {
		return nil, spawnProblem("sourceUserEventId", "invalid_user_event", "Source conversation cannot create this session.", "Use an explicitly authorized main, side or general conversation.")
	}
	human, err := (sqliteValidation{ctx: st.ctx, tx: st.tx}).userEvent(from.ID, args.SourceUserEventID)
	if err != nil {
		return nil, err
	}
	if !human {
		return nil, spawnProblem("sourceUserEventId", "invalid_user_event", "sourceUserEventId must identify a real user message in this conversation.", "Reference the user's actual creation request.")
	}
	if from.Role == sessionRoleGeneralAgent {
		project, err := st.project(args.ProjectID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err != nil || project.Removed {
			return nil, spawnProblem("projectId", "project_unavailable", "Target project is unavailable.", "Choose a registered nonremoved project.")
		}
	}
	var key string
	err = st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_session_spawns WHERE x_from=? AND x_operationId=? LIMIT 1`, from.ID, args.OperationID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var receipt SessionSpawn
	if err := decodeSQLiteRecord(st.ctx, st.tx, "session_spawns", key, 1<<20, &receipt); err != nil {
		return nil, err
	}
	if !sameSpawnRequest(receipt.Request, args) {
		return nil, spawnProblem("operationId", "operation_conflict", "operationId belongs to another accepted request.", "Retry the identical request, or use a distinct ID for a separately requested session.")
	}
	return &receipt, nil
}

func (a *app) sqlSpawnSelection(st *sqliteSessionTx, from Session, args spawnArgs) (Session, Project, error) {
	bad := func(field, code, message string) (Session, Project, error) {
		return Session{}, Project{}, spawnProblem(field, code, message, "Read session_spawn_options and retry the same rejected operation after correcting its arguments.")
	}
	if args.SourceUserEventID == "" || !spawnSender(&from, args) {
		return bad("sourceUserEventId", "invalid_user_event", "Supply the actual human creation request in this conversation.")
	}
	human, err := (sqliteValidation{ctx: st.ctx, tx: st.tx}).userEvent(from.ID, args.SourceUserEventID)
	if err != nil {
		return Session{}, Project{}, err
	}
	if !human {
		return bad("sourceUserEventId", "invalid_user_event", "The authorization reference is not a real human message.")
	}
	title, ok := cleanLabel(args.Title, 200)
	if !ok {
		return bad("title", "invalid_title", "Title must contain 1 to 200 characters.")
	}
	if !validLinkedText(args.Prompt, 128<<10) {
		return bad("prompt", "invalid_prompt", "Supply a self-contained prompt of at most 128 KiB.")
	}
	if !validLinkedText(args.OperationID, 200) {
		return bad("operationId", "invalid_operation_id", "Supply a stable operationId of at most 200 bytes.")
	}
	if args.Workspace != "" {
		return bad("workspace", "topic_override", "Folder overrides are not supported; the engine inherits topic identity.")
	}
	projectID := from.ProjectID
	if from.Role == sessionRoleGeneralAgent {
		projectID = args.ProjectID
	}
	project, err := st.project(projectID)
	if err != nil || project.Removed {
		return bad("project", "project_unavailable", "Project is unavailable.")
	}
	topicID := ""
	if from.Role != sessionRoleGeneralAgent {
		archived, err := st.effectivelyArchived(from)
		if err != nil {
			return Session{}, Project{}, err
		}
		if archived {
			return bad("topic", "source_archived", "Restore the source conversation and topic first.")
		}
		topic, err := st.sourceTopic(from)
		if err != nil {
			return Session{}, Project{}, err
		}
		if topic == nil {
			return bad("topic", "topic_pending", "The main conversation must assign its initial topic before spawning.")
		}
		topicID = topic.ID
	}
	if args.Agent != "" {
		selection := Session{ProjectID: projectID, Title: title, Workspace: "Ungrouped", TopicID: topicID, Harness: args.Harness, Model: args.Model, Effort: args.Effort, YOLO: from.YOLO}
		if args.YOLO != nil {
			selection.YOLO = *args.YOLO
		}
		return selection, project, nil
	}
	if args.InheritParentCapabilities != nil || args.AgentOverrides != nil {
		return bad("agent", "agent_required", "Agent overrides and inheritance require a saved agent.")
	}
	harness := args.Harness
	if harness == "" {
		harness = from.Harness
	}
	if _, ok := a.binaries[harness]; !ok {
		return bad("harness", "harness_unavailable", "Harness is unknown or not installed.")
	}
	model, effort := args.Model, args.Effort
	if harness == from.Harness {
		if model == "" {
			model = from.Model
		}
		if effort == "" && (args.Model == "" || args.Model == from.Model) {
			effort = from.Effort
		}
	}
	if model != "" {
		label, ok := cleanLabel(model, 200)
		if !ok || label != model || strings.HasPrefix(model, "-") {
			return bad("model", "invalid_model_id", "Use an exact model identifier.")
		}
		if harness == "opencode" {
			provider, id, ok := strings.Cut(model, "/")
			if !ok || provider == "" || id == "" {
				return bad("model", "invalid_model_format", "OpenCode model identifiers require provider/model format.")
			}
		}
	}
	if effort != "" {
		label, ok := cleanLabel(effort, 100)
		if !ok || label != effort {
			return bad("effort", "invalid_effort", "Use a supported effort identifier.")
		}
	}
	selection := Session{ProjectID: projectID, Title: title, Workspace: "Ungrouped", TopicID: topicID, Harness: harness, Model: model, Effort: effort, YOLO: from.YOLO}
	if args.YOLO != nil {
		selection.YOLO = *args.YOLO
	}
	return selection, project, nil
}

func (p *adapter) spawnSQLSession(ctx context.Context, args spawnArgs) (any, error) {
	a := p.app
	var from, selection Session
	var project Project
	var receipt *SessionSpawn
	var inheritedForAgent CapabilitySelection
	var agentResolution *AgentResolution
	var binding *SessionAgentBinding
	a.mu.Lock()
	if !p.activeToolLocked() {
		a.mu.Unlock()
		return nil, errors.New("Spawning turn is no longer active.")
	}
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		from, err = st.spawnSource(p.id)
		if err != nil {
			return err
		}
		if args.SourceUserEventID != "" {
			receipt, err = st.existingSpawn(from, args)
			if err != nil || receipt != nil {
				return err
			}
		}
		selection, project, err = a.sqlSpawnSelection(st, from, args)
		if err == nil && args.Agent != "" && args.InheritParentCapabilities != nil && *args.InheritParentCapabilities {
			inheritedForAgent, err = st.inheritedSelection(from.ID)
		}
		return err
	})
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if receipt != nil {
		return spawnReceiptResult(*receipt), nil
	}
	if args.Agent != "" {
		overrides, err := mergeAgentOverrides(args.AgentOverrides, args.Model, args.Effort)
		if err != nil {
			return nil, err
		}
		res, err := a.resolveNewAgent(ctx, NewAgentRequest{ProjectID: project.ID, Agent: args.Agent, SourceUID: args.AgentSourceUID, Revision: args.AgentRevision, Harness: args.Harness, Overrides: overrides, InheritedCapabilities: inheritedForAgent, ExecutionCWD: project.Folder})
		if err != nil {
			return nil, err
		}
		agentResolution = &res
		binding = bindingForResolution(res, overrides, inheritedForAgent)
		selection.Harness, selection.Model, selection.Effort = res.Config.Harness, res.Config.Model, res.Config.Effort
	}
	if agentResolution == nil {
		catalog, err := a.catalog(ctx, selection, project.Folder, false)
		if err != nil {
			return nil, spawnProblem("harness", "catalog_unavailable", "Could not verify the requested model catalog.", "Retry spawn options for this harness; no session was created.")
		}
		resolved := ""
		if selection.Harness == from.Harness {
			resolved = from.ResolvedModel
		}
		if err := validateSpawnModel(selection, resolved, catalog); err != nil {
			return nil, err
		}
	}
	if agentResolution != nil {
		a.authoringMu.Lock()
		defer a.authoringMu.Unlock()
		if err := a.recheckNewAgentLocked(ctx, *agentResolution, project.ID); err != nil {
			return nil, err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !p.activeToolLocked() || ctx.Err() != nil {
		return nil, errors.New("Spawning turn is no longer active.")
	}
	err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		current, err := st.spawnSource(p.id)
		if err != nil {
			return err
		}
		receipt, err = st.existingSpawn(current, args)
		if err != nil || receipt != nil {
			return err
		}
		next, target, err := a.sqlSpawnSelection(st, current, args)
		if err != nil {
			return err
		}
		if target.Folder != project.Folder || agentResolution == nil && (next.Harness != selection.Harness || next.Model != selection.Model || next.Effort != selection.Effort) || next.YOLO != selection.YOLO || next.TopicID != selection.TopicID || current.Harness != from.Harness || current.Model != from.Model || current.Effort != from.Effort || current.YOLO != from.YOLO {
			return spawnProblem("session", "settings_changed", "Sender or project changed during validation.", "Read current options and retry; no session was created.")
		}
		var recovering bool
		if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&recovering); err != nil {
			return err
		}
		if recovering {
			return errors.New("Finish Library recovery before inheriting capabilities.")
		}
		inherited, err := st.inheritedSelection(from.ID)
		if err != nil {
			return err
		}
		if agentResolution != nil && args.InheritParentCapabilities != nil && *args.InheritParentCapabilities && !reflect.DeepEqual(copySelection(inherited), copySelection(inheritedForAgent)) {
			return spawnProblem("inheritParentCapabilities", "settings_changed", "Parent external selection changed during creation.", "Retry with the current applied external selection.")
		}
		s := newTopLevelSession(next.ProjectID, next.Title, next.Workspace, next.Harness, next.Model, next.Effort)
		if agentResolution != nil {
			s.Harness, s.Model, s.Effort = selection.Harness, selection.Model, selection.Effort
			s.AgentConfig = agentSummary(*binding, *agentResolution)
			inherited = copySelection(agentResolution.Config.Selection)
		}
		s.TopicID, s.YOLO = next.TopicID, next.YOLO
		s.Capabilities.Desired = inherited
		if err := st.createSession(s, a.dir); err != nil {
			return err
		}
		if binding != nil {
			if err := st.putAgentBinding(s.ID, binding); err != nil {
				return err
			}
		}
		q := QueuedMessage{ID: newID(), Text: args.Prompt, Mode: "queue", Status: "queued", Origin: &SpawnOrigin{SessionID: from.ID, Title: from.Title, SourceUserEventID: args.SourceUserEventID}}
		if err := st.putRecord("queued_messages", sqliteOwnedKey(s.ID, q.ID), s.ID, q.ID, -1, q); err != nil {
			return err
		}
		if err := st.putRoot("queue_payloads", q.ID, queuedPayload{Submission: submission{Text: args.Prompt}, Harness: s.Harness, Directory: project.Folder}); err != nil {
			return err
		}
		receipt = &SessionSpawn{Request: args, From: from.ID, SessionID: s.ID, MessageID: q.ID, SourceUserEventID: args.SourceUserEventID, OperationID: args.OperationID, Title: s.Title, Prompt: args.Prompt, Workspace: s.Workspace, TopicID: s.TopicID, Harness: s.Harness, Model: s.Model, Effort: s.Effort, YOLO: s.YOLO}
		if agentResolution != nil {
			receipt.Agent = &agentResolution.Capture
			receipt.Configuration = &agentResolution.Config
		}
		if err := st.putRoot("session_spawns", sqliteOwnedKey(from.ID, args.OperationID), *receipt); err != nil {
			return err
		}
		e := event("session_spawn", "")
		e.Title = "Created " + s.Title
		e.Data = map[string]any{"sessionId": s.ID, "title": s.Title, "messageId": q.ID, "harness": s.Harness, "model": s.Model, "effort": s.Effort, "yolo": s.YOLO}
		if _, err := st.appendEvent(from.ID, e); err != nil {
			return err
		}
		// spawnSource overlays the active automation's frozen settings. Keep those
		// execution preferences out of the source conversation's saved settings.
		meta, err := st.sessionMeta(from.ID)
		if err != nil {
			return err
		}
		meta.UpdatedAt = now()
		return st.putSessionMeta(meta)
	})
	if err != nil {
		return nil, err
	}
	a.scheduleMessagesLocked()
	return spawnReceiptResult(*receipt), nil
}

func (p *adapter) sqlSpawnOptions(ctx context.Context, projectID, harness, cursor string, limit int) (any, error) {
	a := p.app
	var from Session
	var project Project
	var topic *Topic
	messages := []map[string]string{}
	a.mu.Lock()
	if !p.activeToolLocked() {
		a.mu.Unlock()
		return nil, errors.New("Spawning turn is no longer active.")
	}
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		from, err = st.spawnSource(p.id)
		if err != nil {
			return err
		}
		if !spawnSender(&from, spawnArgs{ProjectID: projectID}) {
			return errors.New("Creation scope is unavailable to this conversation.")
		}
		if projectID == "" {
			projectID = from.ProjectID
		}
		project, err = st.project(projectID)
		if err != nil {
			return err
		}
		if project.Removed {
			return errors.New("Project is unavailable.")
		}
		topic, err = st.sourceTopic(from)
		if err != nil {
			return err
		}
		ids, err := sqliteQueryIDs(ctx, tx, `SELECT id FROM data_session_events WHERE owner_id=? AND kind='user' ORDER BY position DESC LIMIT 5`, p.id)
		if err != nil {
			return err
		}
		for _, id := range ids {
			e, err := readSQLiteEventMetadata(ctx, tx, id)
			if err != nil {
				return err
			}
			text, _, err := sqliteEventTextWindow(ctx, tx, id, 0, 1024)
			if err != nil {
				return err
			}
			messages = append(messages, map[string]string{"id": e.ID, "text": text})
		}
		return nil
	})
	if harness == "" {
		harness = from.Harness
	}
	_, installed := a.binaries[harness]
	harnesses := append([]Harness(nil), a.harnesses...)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !installed {
		return nil, errors.New("Harness is not installed.")
	}
	selected := from
	selected.ProjectID, selected.Harness = projectID, harness
	catalog, err := a.catalog(ctx, selected, project.Folder, false)
	if err != nil {
		return nil, err
	}
	items := []any{}
	for _, model := range catalog.Models {
		items = append(items, model)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !p.activeToolLocked() {
		return nil, errors.New("Spawning turn is no longer active.")
	}
	current, err := a.store.readProjectMeta(ctx, project.ID)
	if err != nil || current.Removed || current.Folder != project.Folder {
		return nil, errors.New("Project changed while loading options.")
	}
	name := "session_spawn_options"
	required := []string{"title", "prompt", "operationId", "sourceUserEventId"}
	args := generalToolArgs{SessionID: p.id, Query: harness, Cursor: cursor, Limit: limit}
	if from.Role == sessionRoleGeneralAgent {
		name = "session_create_options"
		args.ProjectID = projectID
		required = append(required, "projectId")
	}
	extra := map[string]any{"projectId": projectID, "executionCwd": project.Folder, "harness": harness, "harnesses": harnesses, "topic": topic, "senderYolo": from.YOLO, "senderModel": from.Model, "senderEffort": from.Effort, "defaultModel": catalog.DefaultModel, "defaultEffort": catalog.DefaultEffort, "sourceUserMessages": messages, "requiredFields": required}
	return agentItemsPage(name, args, items, extra, revisionForItems(map[string]any{"items": items, "options": extra}))
}
