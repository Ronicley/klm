package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
)

type sessionControlError struct {
	code int
	text string
}

func (e *sessionControlError) Error() string   { return e.text }
func controlError(code int, text string) error { return &sessionControlError{code, text} }
func failSessionControl(w http.ResponseWriter, err error) {
	var problem *sessionControlError
	if errors.As(err, &problem) {
		fail(w, problem.code, problem.text)
	} else {
		fail(w, 503, err.Error())
	}
}

func sessionLabels(d *diskState, s *Session, title, folder *string) (string, string, error) {
	if s == nil || s.GraphRunID != "" || s.Role == sessionRoleSubagent {
		return "", "", controlError(404, "Conversation not found.")
	}
	if s.Role == sessionRoleGeneralAgent {
		return "", "", controlError(400, "The general conversation cannot be renamed or moved.")
	}
	name, group := s.Title, s.Workspace
	var ok bool
	if title != nil {
		name, ok = cleanLabel(*title, 200)
		if !ok {
			return "", "", controlError(400, "Session title must contain 1 to 200 characters.")
		}
	}
	if folder != nil {
		group, ok = workspace(d.project(s.ProjectID), *folder)
		if !ok {
			return "", "", controlError(400, "Workspace must be Ungrouped or an active project folder.")
		}
	}
	return name, group, nil
}

func (a *app) renameMoveSessionLocked(id string, title, folder *string) error {
	name, group, err := sessionLabels(&a.state, a.state.session(id), title, folder)
	if err != nil {
		return err
	}
	return a.commitLocked(func(d *diskState) { s := d.session(id); s.Title, s.Workspace, s.UpdatedAt = name, group, now() })
}

func appendSessionControl(s *Session, title string, origin *SpawnOrigin, data map[string]any) {
	if origin == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["origin"] = origin
	e := event("status", title)
	e.Title, e.Status, e.Data = title, "completed", data
	s.Events = append(s.Events, e)
	s.UpdatedAt = now()
}

func sessionArchiveLabels(d *diskState, s *Session, archived *bool, folder *string) error {
	if archived == nil {
		return nil
	}
	if s == nil || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
		return controlError(400, "Only normal top-level sessions can be archived or restored.")
	}
	if !*archived && folder == nil && folderArchived(d.project(s.ProjectID), s.Workspace) {
		return controlError(409, "Choose an active folder or Ungrouped to restore a conversation in an archived folder.")
	}
	return nil
}

func applySessionLabels(d *diskState, id, title, group string, archived *bool, origin *SpawnOrigin) {
	s := d.session(id)
	s.Title, s.Workspace, s.UpdatedAt = title, group, now()
	if archived != nil {
		s.Archived = *archived
		appendSessionControl(s, "Session archive state updated", origin, map[string]any{"archived": *archived, "folder": group})
	}
}

func (a *app) archiveSessionLocked(id string, archived bool, folder *string, origin *SpawnOrigin) error {
	s := a.state.session(id)
	if err := sessionArchiveLabels(&a.state, s, &archived, folder); err != nil {
		return err
	}
	title, group, err := sessionLabels(&a.state, s, nil, folder)
	if err != nil {
		return err
	}
	if s.Archived == archived && s.Workspace == group {
		return nil
	}
	return a.commitLocked(func(d *diskState) { applySessionLabels(d, id, title, group, &archived, origin) })
}

// Catalog discovery never holds app.mu; validate the complete resultant pair,
// then recheck all inputs under the lock before a single durable mutation.
func (a *app) changeSessionSettings(ctx context.Context, id string, model, effort *string, yolo *bool) error {
	if model == nil && effort == nil && yolo == nil {
		return controlError(400, "Supply at least one setting.")
	}
	if model == nil && effort == nil {
		return a.changeSessionYOLO(ctx, id, *yolo)
	}
	a.mu.Lock()
	s := a.state.session(id)
	if s == nil || s.GraphRunID != "" || s.Role == sessionRoleSubagent {
		a.mu.Unlock()
		return controlError(404, "Conversation not found.")
	}
	if a.runs[id] != nil || s.Status == "running" {
		a.mu.Unlock()
		return controlError(409, "Wait for the current turn before changing settings.")
	}
	before := *s
	cwd, available := a.state.conversationDirectory(s)
	a.mu.Unlock()
	if !available {
		return controlError(404, "Conversation directory is unavailable.")
	}
	newModel, newEffort := before.Model, before.Effort
	if model != nil {
		newModel = *model
	}
	if effort != nil {
		newEffort = *effort
	}
	if model != nil || effort != nil {
		catalog, err := a.catalog(ctx, before, cwd, false)
		if err != nil {
			return controlError(502, "Could not verify available models. Retry loading the destination model list.")
		}
		resolved := newModel
		if resolved == "" {
			resolved = catalog.DefaultModel
			if resolved == "" {
				resolved = before.ResolvedModel
			}
		}
		option := catalog.find(resolved)
		if newModel != "" && option == nil {
			return controlError(400, "Model is not available from a connected provider.")
		}
		if newEffort != "" && (option == nil || !slices.Contains(option.Efforts, newEffort)) {
			return controlError(409, "The resulting model/effort pair is invalid. Supply a compatible effort or an empty effort for defaults.")
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s = a.state.session(id)
	currentCwd, available := a.state.conversationDirectory(s)
	if a.closing || a.storageErr != nil || s == nil || !available || currentCwd != cwd || s.Harness != before.Harness || s.Model != before.Model || s.Effort != before.Effort || s.YOLO != before.YOLO {
		return controlError(409, "Conversation settings changed; retry the update.")
	}
	if a.runs[id] != nil || s.Status == "running" {
		return controlError(409, "Wait for the current turn before changing settings.")
	}
	return a.commitLocked(func(d *diskState) {
		s := d.session(id)
		if model != nil || effort != nil {
			changed := s.Model != newModel
			s.Model, s.Effort, s.ResolvedEffort = newModel, newEffort, ""
			if changed {
				s.ResolvedModel = ""
				if s.Usage != nil {
					s.Usage.Context = nil
				}
			}
		}
		if yolo != nil {
			s.YOLO = *yolo
		}
		s.UpdatedAt = now()
	})
}

func (a *app) changeConversationGraph(ctx context.Context, id, graphID string) error {
	if graphID != "" && !validAuthoringID(graphID) {
		return controlError(400, "Invalid graph identifier.")
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	a.mu.Lock()
	s := a.state.session(id)
	if s == nil || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
		a.mu.Unlock()
		return controlError(404, "Main conversation not found.")
	}
	p := a.state.project(s.ProjectID)
	if p == nil || p.Removed {
		a.mu.Unlock()
		return controlError(404, "Project not found.")
	}
	project := *p
	a.mu.Unlock()
	if graphID != "" {
		catalog, err := loadAuthoring(project)
		if err != nil {
			return controlError(500, "Cannot read graph catalog: "+err.Error())
		}
		found := false
		for _, graph := range catalog.Graphs {
			if graph.ID == graphID {
				if !graph.Definition.Enabled {
					return controlError(409, "Graph is disabled.")
				}
				found = true
				break
			}
		}
		if !found {
			if len(catalog.Errors) > 0 {
				return controlError(409, "Graph is unavailable; resolve catalog errors: "+strings.Join(catalog.Errors, "; "))
			}
			return controlError(404, "Graph not found.")
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s, p = a.state.session(id), a.state.project(project.ID)
	if a.closing || a.storageErr != nil || s == nil || p == nil || p.Removed || p.Folder != project.Folder {
		return controlError(409, "Conversation or project changed; retry selection.")
	}
	if s.SelectedGraphID == graphID {
		return nil
	}
	return a.commitLocked(func(d *diskState) { s := d.session(id); s.SelectedGraphID, s.UpdatedAt = graphID, now() })
}
