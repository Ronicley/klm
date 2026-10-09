package main

import (
	"errors"
	"path/filepath"
	"strings"
)

// A spawn is provenance and an idempotency receipt, never an ownership link.
type SessionSpawn struct {
	Request           spawnArgs `json:"request"`
	From              string    `json:"from"`
	SessionID         string    `json:"sessionId"`
	MessageID         string    `json:"messageId"`
	SourceUserEventID string    `json:"sourceUserEventId"`
	OperationID       string    `json:"operationId"`
	Title             string    `json:"title"`
	Prompt            string    `json:"prompt"`
	Workspace         string    `json:"workspace"`
	Harness           string    `json:"harness"`
	Model             string    `json:"model"`
	Effort            string    `json:"effort"`
	YOLO              bool      `json:"yolo,omitempty"`
}

type SpawnOrigin struct {
	SessionID         string `json:"sessionId"`
	Title             string `json:"title"`
	Kind              string `json:"kind,omitempty"`
	SourceUserEventID string `json:"sourceUserEventId,omitempty"`
}

type spawnArgs struct {
	ProjectID         string `json:"projectId,omitempty"`
	Title             string `json:"title"`
	Prompt            string `json:"prompt"`
	OperationID       string `json:"operationId"`
	SourceUserEventID string `json:"sourceUserEventId"`
	Harness           string `json:"harness"`
	Model             string `json:"model"`
	Effort            string `json:"effort"`
	Workspace         string `json:"workspace"`
	YOLO              *bool  `json:"yolo,omitempty"`
}

func spawnSender(from *Session, args spawnArgs) bool {
	return from != nil && (consultationEndpoint(from) && args.ProjectID == "" || from.Role == sessionRoleGeneralAgent && from.ParentID == "" && from.GraphRunID == "" && args.ProjectID != "")
}

// Also used by checkpoint and journal validation; old same-project receipts keep
// their original request shape and general receipts explicitly bind the project.
func validateSessionSpawns(d *diskState) error {
	keys := map[string]bool{}
	for _, receipt := range d.SessionSpawns {
		from, to := d.session(receipt.From), d.session(receipt.SessionID)
		key := receipt.From + "/" + receipt.OperationID
		if receipt.OperationID == "" || keys[key] || !spawnSender(from, receipt.Request) || to == nil || to.Role != "" || to.ParentID != "" || to.GraphRunID != "" || !graphUserEvent(d, receipt.From, receipt.SourceUserEventID) {
			return errors.New("Invalid session spawn in state.json; refusing to overwrite.")
		}
		projectID := from.ProjectID
		if from.Role == sessionRoleGeneralAgent {
			projectID = receipt.Request.ProjectID
		}
		if to.ProjectID != projectID || d.project(projectID) == nil {
			return errors.New("Invalid session spawn project in state.json; refusing to overwrite.")
		}
		keys[key] = true
	}
	return nil
}

func sameSpawnRequest(left, right spawnArgs) bool {
	leftYOLO, rightYOLO := left.YOLO, right.YOLO
	left.YOLO, right.YOLO = nil, nil
	return left == right && (leftYOLO == nil && rightYOLO == nil || leftYOLO != nil && rightYOLO != nil && *leftYOLO == *rightYOLO)
}

func newTopLevelSession(projectID, title, group, harness, model, effort string) Session {
	stamp := now()
	return Session{ID: newID(), ProjectID: projectID, Title: title, Workspace: group, Harness: harness, Model: model, Effort: effort,
		Status: "idle", Events: []Event{}, CreatedAt: stamp, UpdatedAt: stamp}
}

func (a *app) appendTopLevelSession(d *diskState, s Session) {
	d.Sessions = append(d.Sessions, s)
	if s.Harness == "pi" {
		d.Native[s.ID] = nativeSession{Path: filepath.Join(a.dir, "sessions", s.ID+".jsonl")}
	}
}

func consultationEndpoint(s *Session) bool {
	return s != nil && s.GraphRunID == "" && (s.Role == "" && s.ParentID == "" || s.Role == sessionRoleSideAgent && s.ParentID != "")
}

// Direction matters: only the engine-owned general agent can initiate a
// cross-project consultation. Replies use the existing request, not new access.
func consultationPair(from, to *Session) bool {
	return from != nil && to != nil && from.ID != to.ID && consultationEndpoint(to) &&
		(from.Role == sessionRoleGeneralAgent && from.ParentID == "" && from.GraphRunID == "" || consultationEndpoint(from) && from.ProjectID == to.ProjectID)
}

func (d *diskState) conversationArchived(s *Session) bool {
	if s == nil {
		return true
	}
	p := d.project(s.ProjectID)
	return s.Archived || p != nil && folderArchived(p, s.Workspace) || s.Role == sessionRoleSideAgent && (d.session(s.ParentID) == nil || d.session(s.ParentID).Archived)
}

func (a *app) existingSpawnLocked(from *Session, args spawnArgs) (*SessionSpawn, error) {
	if from != nil && a.state.DeletedOperations["spawn/"+from.ID+"/"+args.OperationID] {
		return nil, spawnProblem("operationId", "session_deleted", "The session created by this operation was deleted.", "Do not retry this creation. A new session requires a new user request and operationId.")
	}
	if !spawnSender(from, args) || !graphUserEvent(&a.state, from.ID, args.SourceUserEventID) {
		return nil, spawnProblem("sourceUserEventId", "invalid_user_event", "sourceUserEventId must identify a real user message in this conversation.", "Use session_spawn_options to read recent user message IDs. Reference the user's request to create sessions, not a session ID, invented ID or agent-authored prompt.")
	}
	if from.Role == sessionRoleGeneralAgent {
		if project := a.state.project(args.ProjectID); project == nil || project.Removed {
			return nil, spawnProblem("projectId", "project_unavailable", "Target project is unavailable.", "Choose a registered nonremoved project.")
		}
	}
	for i := range a.state.SessionSpawns {
		existing := &a.state.SessionSpawns[i]
		if existing.From != from.ID || existing.OperationID != args.OperationID {
			continue
		}
		if !sameSpawnRequest(existing.Request, args) {
			return nil, spawnProblem("operationId", "operation_conflict", "operationId already belongs to a different accepted spawn request.", "Retry identical arguments for the original receipt. Use a distinct operationId only for a separately requested session.")
		}
		return existing, nil
	}
	return nil, nil
}

func (a *app) spawnSelectionLocked(from *Session, args spawnArgs) (Session, error) {
	missing := []string{}
	for _, item := range []struct{ key, value string }{{"title", args.Title}, {"prompt", args.Prompt}, {"operationId", args.OperationID}, {"sourceUserEventId", args.SourceUserEventID}} {
		if strings.TrimSpace(item.value) == "" {
			missing = append(missing, item.key)
		}
	}
	if len(missing) > 0 {
		return Session{}, spawnProblem("arguments", "missing_fields", "Missing required fields: "+strings.Join(missing, ", ")+".", "Supply title, self-contained prompt, stable operationId and the real sourceUserEventId. Call session_spawn_options for available settings and recent user message IDs.")
	}
	if !spawnSender(from, args) || !graphUserEvent(&a.state, from.ID, args.SourceUserEventID) {
		return Session{}, spawnProblem("sourceUserEventId", "invalid_user_event", "sourceUserEventId must identify a real user message in this conversation.", "Call session_spawn_options and choose the actual user request to create sessions.")
	}
	title, ok := cleanLabel(args.Title, 200)
	if !ok {
		return Session{}, spawnProblem("title", "invalid_title", "Title must contain 1 to 200 characters.", "Supply a short nonempty session title.")
	}
	if !validLinkedText(args.Prompt, 128<<10) {
		return Session{}, spawnProblem("prompt", "invalid_prompt", "Prompt must be nonempty UTF-8 text of at most 128 KiB.", "Supply a self-contained initial task with fewer than 128 KiB of text.")
	}
	if !validLinkedText(args.OperationID, 200) {
		return Session{}, spawnProblem("operationId", "invalid_operation_id", "operationId must be nonempty UTF-8 text of at most 200 bytes.", "Use a stable short identifier unique to this requested session.")
	}
	projectID := from.ProjectID
	if from.Role == sessionRoleGeneralAgent {
		projectID = args.ProjectID
	}
	project := a.state.project(projectID)
	if project == nil || project.Removed {
		return Session{}, spawnProblem("project", "project_unavailable", "Project is unavailable.", "Create sessions only in this sender's available project.")
	}
	group := args.Workspace
	if group == "" {
		if from.Role == sessionRoleGeneralAgent {
			group = "Ungrouped"
		} else {
			group = from.Workspace
		}
	}
	group, ok = workspace(project, group)
	if !ok && args.Workspace == "" {
		group, ok = "Ungrouped", true
	}
	if !ok {
		return Session{}, spawnProblem("workspace", "invalid_workspace", "Workspace must be Ungrouped or an active project folder.", "Omit workspace to inherit an active folder, use Ungrouped, or choose an active folder from session_spawn_options.")
	}
	harness := args.Harness
	if harness == "" {
		harness = from.Harness
	}
	if _, ok := a.binaries[harness]; !ok {
		return Session{}, spawnProblem("harness", "harness_unavailable", "Harness is unknown or not installed.", "Omit harness to inherit the sender, or choose an installed harness from session_spawn_options.")
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
		if label, valid := cleanLabel(model, 200); !valid || label != model || strings.HasPrefix(model, "-") {
			return Session{}, spawnProblem("model", "invalid_model_id", "Invalid model identifier.", "Use an exact model ID from session_spawn_options, or omit model to inherit/default.")
		}
		if harness == "opencode" {
			provider, id, full := strings.Cut(model, "/")
			if !full || provider == "" || id == "" {
				return Session{}, spawnProblem("model", "invalid_model_format", "OpenCode model must use provider/model format.", "Use a full ID such as openai/gpt-6-luna, not gpt-6-luna or Luna. Call session_spawn_options for exact model IDs, or omit model to inherit/default.")
			}
		}
	}
	if effort != "" {
		if label, valid := cleanLabel(effort, 100); !valid || label != effort {
			return Session{}, spawnProblem("effort", "invalid_effort", "Invalid effort.", "Choose a supported effort/variant from session_spawn_options, or omit it to inherit/default.")
		}
	}
	selection := Session{ProjectID: projectID, Title: title, Workspace: group, Harness: harness, Model: model, Effort: effort, YOLO: from.YOLO}
	if args.YOLO != nil {
		selection.YOLO = *args.YOLO
	}
	return selection, nil
}

func spawnReceiptResult(receipt SessionSpawn) map[string]any {
	result := map[string]any{"sessionId": receipt.SessionID, "title": receipt.Title, "messageId": receipt.MessageID, "yolo": receipt.YOLO, "accepted": true}
	if receipt.Request.ProjectID != "" {
		result["projectId"], result["folder"] = receipt.Request.ProjectID, receipt.Workspace
		result["harness"], result["model"], result["effort"] = receipt.Harness, receipt.Model, receipt.Effort
	}
	return result
}

func (a *app) spawnSessionLocked(from *Session, args spawnArgs) (any, error) {
	if args.SourceUserEventID != "" {
		if existing, err := a.existingSpawnLocked(from, args); err != nil {
			return nil, err
		} else if existing != nil {
			return spawnReceiptResult(*existing), nil
		}
	}
	selection, err := a.spawnSelectionLocked(from, args)
	if err != nil {
		return nil, err
	}
	s := newTopLevelSession(selection.ProjectID, selection.Title, selection.Workspace, selection.Harness, selection.Model, selection.Effort)
	s.YOLO = selection.YOLO
	request := args
	if args.YOLO != nil {
		selected := *args.YOLO
		request.YOLO = &selected
	}
	q := QueuedMessage{ID: newID(), Text: args.Prompt, Status: "queued", Mode: "queue", Origin: &SpawnOrigin{SessionID: from.ID, Title: from.Title, SourceUserEventID: args.SourceUserEventID}}
	receipt := SessionSpawn{Request: request, From: from.ID, SessionID: s.ID, MessageID: q.ID, SourceUserEventID: args.SourceUserEventID, OperationID: args.OperationID,
		Title: s.Title, Prompt: args.Prompt, Workspace: s.Workspace, Harness: s.Harness, Model: s.Model, Effort: s.Effort, YOLO: s.YOLO}
	if err := a.commitLocked(func(d *diskState) {
		a.appendTopLevelSession(d, s)
		d.session(s.ID).Queue = []QueuedMessage{q}
		if d.QueuePayloads == nil {
			d.QueuePayloads = map[string]queuedPayload{}
		}
		d.QueuePayloads[q.ID] = queuedPayload{Submission: submission{Text: args.Prompt}, Harness: s.Harness, Directory: d.project(s.ProjectID).Folder}
		d.SessionSpawns = append(d.SessionSpawns, receipt)
		origin := d.session(from.ID)
		e := event("session_spawn", "")
		e.Title = "Created " + s.Title
		e.Data = map[string]any{"sessionId": s.ID, "title": s.Title, "messageId": q.ID, "yolo": s.YOLO}
		origin.Events = append(origin.Events, e)
		origin.UpdatedAt = now()
	}); err != nil {
		return nil, err
	}
	a.scheduleMessagesLocked()
	return spawnReceiptResult(receipt), nil
}
