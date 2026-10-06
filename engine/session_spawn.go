package main

import (
	"context"
	"encoding/json"
	"slices"
)

type spawnError struct {
	Accepted    bool     `json:"accepted"`
	Code        string   `json:"code"`
	Field       string   `json:"field"`
	Message     string   `json:"message"`
	Hint        string   `json:"hint"`
	ValidValues []string `json:"validValues,omitempty"`
}

func (e *spawnError) Error() string { b, _ := json.Marshal(e); return string(b) }
func spawnProblem(field, code, message, hint string) *spawnError {
	return &spawnError{Code: code, Field: field, Message: message, Hint: hint}
}

func validateSpawnModel(selection Session, resolvedModel string, catalog *ModelCatalog) error {
	id := selection.Model
	if id == "" {
		id = catalog.DefaultModel
		if id == "" {
			id = resolvedModel
		}
	}
	option := catalog.find(id)
	if id != "" && option == nil || len(catalog.Models) == 0 {
		problem := spawnProblem("model", "model_unavailable", "Model is not available from a connected provider for this harness.", "Call session_spawn_options for this harness, then retry with an exact model ID. No session was created; the operationId can be reused after correcting rejected arguments.")
		for _, model := range catalog.Models[:min(10, len(catalog.Models))] {
			problem.ValidValues = append(problem.ValidValues, model.ID)
		}
		return problem
	}
	if selection.Effort != "" && (option == nil || !slices.Contains(option.Efforts, selection.Effort)) {
		problem := spawnProblem("effort", "unsupported_effort", "The selected model does not support this effort/variant.", "Choose a supported effort from session_spawn_options. If the default model is unknown, supply an explicit model ID as well. No session was created.")
		if option != nil {
			problem.ValidValues = option.Efforts
		}
		return problem
	}
	return nil
}

// Validate catalogs outside app.mu, then recheck the authenticated turn and
// sender configuration before one atomic creation. Rejected arguments never
// create a session, enqueue native work, or reserve an operationId.
func (p *adapter) spawnSession(ctx context.Context, args spawnArgs) (any, error) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.turn.ctx, cancel)
	defer cancel()
	defer stop()
	a := p.app
	a.mu.Lock()
	if !p.activeToolLocked() {
		a.mu.Unlock()
		return nil, spawnProblem("turn", "turn_inactive", "The spawning turn is no longer active.", "Do not retry uncertain native delivery automatically.")
	}
	from := a.state.session(p.id)
	if args.SourceUserEventID != "" {
		if existing, err := a.existingSpawnLocked(from, args); err != nil {
			a.mu.Unlock()
			return nil, err
		} else if existing != nil {
			result := spawnReceiptResult(*existing)
			a.mu.Unlock()
			return result, nil
		}
	}
	selection, err := a.spawnSelectionLocked(from, args)
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	before := *from
	cwd := a.state.project(from.ProjectID).Folder
	a.mu.Unlock()
	catalog, err := a.catalog(ctx, selection, cwd, false)
	if err != nil {
		return nil, spawnProblem("harness", "catalog_unavailable", "Could not verify the requested harness model catalog.", "Retry session_spawn_options for that harness. Check its installed/connected provider configuration if the catalog remains unavailable. No session was created.")
	}
	resolved := ""
	if selection.Harness == before.Harness {
		resolved = before.ResolvedModel
	}
	if err := validateSpawnModel(selection, resolved, catalog); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	from = a.state.session(p.id)
	project := a.state.project(before.ProjectID)
	if ctx.Err() != nil || !p.activeToolLocked() || from == nil || project == nil || project.Removed || project.Folder != cwd || from.Harness != before.Harness || from.Model != before.Model || from.Effort != before.Effort || from.YOLO != before.YOLO || from.Workspace != before.Workspace {
		return nil, spawnProblem("session", "settings_changed", "The sender, turn or project changed while validating spawn settings.", "Read session_spawn_options again and retry. No session was created.")
	}
	return a.spawnSessionLocked(from, args)
}

func (p *adapter) spawnOptions(ctx context.Context, harness, cursor string, limit int) (any, error) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.turn.ctx, cancel)
	defer cancel()
	defer stop()
	a := p.app
	a.mu.Lock()
	from := a.state.session(p.id)
	if !p.activeToolLocked() || !consultationEndpoint(from) {
		a.mu.Unlock()
		return nil, spawnProblem("turn", "turn_inactive", "Session spawn options are unavailable to this turn.", "Use these tools only from a normal main/side conversation.")
	}
	s := *from
	if harness == "" {
		harness = s.Harness
	}
	if _, installed := a.binaries[harness]; !installed {
		a.mu.Unlock()
		return nil, spawnProblem("harness", "harness_unavailable", "Harness is unknown or not installed.", "Choose an installed harness from the returned capability information, or omit harness to use the sender's.")
	}
	cwd, available := a.state.conversationDirectory(from)
	if !available {
		a.mu.Unlock()
		return nil, spawnProblem("project", "project_unavailable", "Project is unavailable.", "The sender must belong to an available project.")
	}
	userMessages := []map[string]string{}
	for i := len(s.Events) - 1; i >= 0 && len(userMessages) < 5; i-- {
		e := s.Events[i]
		if e.Type == "user" {
			userMessages = append(userMessages, map[string]string{"id": e.ID, "text": boundedText(e.Text, 1024)})
		}
	}
	harnesses := []Harness{}
	for _, option := range a.harnesses {
		harnesses = append(harnesses, option)
	}
	folders := append([]string{"Ungrouped"}, a.state.project(s.ProjectID).Folders...)
	activeFolders := []string{}
	for _, folder := range folders {
		if !folderArchived(a.state.project(s.ProjectID), folder) {
			activeFolders = append(activeFolders, folder)
		}
	}
	a.mu.Unlock()
	senderModel, senderEffort := s.Model, s.Effort
	s.Harness = harness
	catalog, err := a.catalog(ctx, s, cwd, false)
	if err != nil {
		return nil, spawnProblem("harness", "catalog_unavailable", "Could not load spawn model options.", "Check the selected harness/provider configuration and retry. No session was created.")
	}
	items := []any{}
	for _, model := range catalog.Models {
		items = append(items, model)
	}
	return agentItemsPage("session_spawn_options", generalToolArgs{SessionID: p.id, Query: harness, Cursor: cursor, Limit: limit}, items, map[string]any{"harness": harness, "harnesses": harnesses, "folders": activeFolders, "senderYolo": s.YOLO, "senderModel": senderModel, "senderEffort": senderEffort, "defaultModel": catalog.DefaultModel, "defaultEffort": catalog.DefaultEffort, "sourceUserMessages": userMessages, "requiredFields": []string{"title", "prompt", "operationId", "sourceUserEventId"}}, revisionForItems(items))
}
