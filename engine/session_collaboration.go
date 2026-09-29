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
}

type SpawnOrigin struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
}

type spawnArgs struct {
	Title             string `json:"title"`
	Prompt            string `json:"prompt"`
	OperationID       string `json:"operationId"`
	SourceUserEventID string `json:"sourceUserEventId"`
	Harness           string `json:"harness"`
	Model             string `json:"model"`
	Effort            string `json:"effort"`
	Workspace         string `json:"workspace"`
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

func (a *app) spawnSessionLocked(from *Session, args spawnArgs) (any, error) {
	if !consultationEndpoint(from) || !graphUserEvent(&a.state, from.ID, args.SourceUserEventID) {
		return nil, errors.New("sourceUserEventId must identify a real user message in this conversation.")
	}
	title, ok := cleanLabel(args.Title, 200)
	if !ok || !validLinkedText(args.Prompt, 128<<10) || len(args.OperationID) > 200 || strings.TrimSpace(args.OperationID) == "" || strings.ContainsRune(args.OperationID, 0) {
		return nil, errors.New("Supply a title (at most 200 characters), prompt (at most 128 KiB), and operationId (at most 200 characters).")
	}
	for _, existing := range a.state.SessionSpawns {
		if existing.From != from.ID || existing.OperationID != args.OperationID {
			continue
		}
		if existing.Request != args {
			return nil, errors.New("operationId already belongs to a different spawn request.")
		}
		return map[string]any{"sessionId": existing.SessionID, "title": existing.Title, "messageId": existing.MessageID, "accepted": true}, nil
	}
	project := a.state.project(from.ProjectID)
	if project == nil || project.Removed {
		return nil, errors.New("Project is unavailable.")
	}
	group := args.Workspace
	if group == "" {
		group = from.Workspace
	}
	group, ok = workspace(project, group)
	if !ok && args.Workspace == "" {
		group, ok = "Ungrouped", true
	}
	if !ok {
		return nil, errors.New("Workspace must be Ungrouped or an active project folder.")
	}
	harness := args.Harness
	if harness == "" {
		harness = from.Harness
	}
	if _, ok := a.binaries[harness]; !ok {
		return nil, errors.New("Harness is unknown or not installed.")
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
			return nil, errors.New("Invalid model identifier.")
		}
	}
	if effort != "" {
		if label, valid := cleanLabel(effort, 100); !valid || label != effort {
			return nil, errors.New("Invalid effort.")
		}
	}
	s := newTopLevelSession(from.ProjectID, title, group, harness, model, effort)
	q := QueuedMessage{ID: newID(), Text: args.Prompt, Status: "queued", Mode: "queue", Origin: &SpawnOrigin{SessionID: from.ID, Title: from.Title}}
	receipt := SessionSpawn{Request: args, From: from.ID, SessionID: s.ID, MessageID: q.ID, SourceUserEventID: args.SourceUserEventID, OperationID: args.OperationID,
		Title: title, Prompt: args.Prompt, Workspace: group, Harness: harness, Model: model, Effort: effort}
	if err := a.commitLocked(func(d *diskState) {
		a.appendTopLevelSession(d, s)
		d.session(s.ID).Queue = []QueuedMessage{q}
		if d.QueuePayloads == nil {
			d.QueuePayloads = map[string]queuedPayload{}
		}
		d.QueuePayloads[q.ID] = queuedPayload{Submission: submission{Text: args.Prompt}, Harness: harness, Directory: project.Folder}
		d.SessionSpawns = append(d.SessionSpawns, receipt)
		origin := d.session(from.ID)
		e := event("session_spawn", "")
		e.Title = "Created " + title
		e.Data = map[string]any{"sessionId": s.ID, "title": title, "messageId": q.ID}
		origin.Events = append(origin.Events, e)
		origin.UpdatedAt = now()
	}); err != nil {
		return nil, err
	}
	a.scheduleMessagesLocked()
	return map[string]any{"sessionId": s.ID, "title": title, "messageId": q.ID, "accepted": true}, nil
}
