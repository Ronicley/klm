package main

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
)

func (a *app) changeSQLSettings(ctx context.Context, id string, model, effort *string, yolo *bool) error {
	if model != nil || effort != nil {
		bound, err := a.agentBindingExists(ctx, id)
		if err != nil {
			return err
		}
		if bound {
			return a.patchAgentBinding(ctx, id, "", "", AgentOverridePatch{Model: agentOverrideValue[string]{Present: model != nil, Value: model}, Effort: agentOverrideValue[string]{Present: effort != nil, Value: effort}}, yolo)
		}
	}
	if model == nil && effort == nil {
		if yolo == nil {
			return controlError(400, "Supply at least one setting.")
		}
		return a.changeSessionYOLO(ctx, id, *yolo)
	}
	a.mu.Lock()
	var before Session
	var cwd string
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		before, err = st.sessionMeta(id)
		if err != nil {
			return err
		}
		if before.GraphRunID != "" || before.Role == sessionRoleSubagent {
			return controlError(404, "Conversation not found.")
		}
		if a.runs[id] != nil || before.Status == "running" {
			return controlError(409, "Wait for the current turn before changing settings.")
		}
		if r, err := st.activeAutomation(id); err != nil {
			return err
		} else if r != nil {
			return controlError(409, "Automation run settings are read-only until the run ends.")
		}
		cwd, err = st.conversationDirectory(before)
		return err
	})
	a.mu.Unlock()
	if err != nil {
		return err
	}
	nextModel, nextEffort := before.Model, before.Effort
	if model != nil {
		nextModel = *model
	}
	if effort != nil {
		nextEffort = *effort
	}
	catalog, err := a.catalog(ctx, before, cwd, false)
	if err != nil {
		return controlError(502, "Could not verify available models. Reload the model list.")
	}
	resolved := nextModel
	if resolved == "" {
		resolved = catalog.DefaultModel
		if resolved == "" {
			resolved = before.ResolvedModel
		}
	}
	option := catalog.find(resolved)
	if nextModel != "" && option == nil {
		return controlError(400, "Model is not available from a connected provider.")
	}
	if nextEffort != "" && (option == nil || !slices.Contains(option.Efforts, nextEffort)) {
		return controlError(409, "The resulting model/effort pair is invalid.")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		s, err := st.sessionMeta(id)
		if err != nil {
			return err
		}
		current, err := st.conversationDirectory(s)
		if err != nil {
			return err
		}
		if current != cwd || s.Harness != before.Harness || s.Model != before.Model || s.Effort != before.Effort || s.YOLO != before.YOLO {
			return controlError(409, "Conversation settings changed; retry the update.")
		}
		if a.runs[id] != nil || s.Status == "running" {
			return controlError(409, "Wait for the current turn before changing settings.")
		}
		if r, err := st.activeAutomation(id); err != nil {
			return err
		} else if r != nil {
			return controlError(409, "Automation run settings are read-only until the run ends.")
		}
		changed := s.Model != nextModel
		s.Model, s.Effort, s.ResolvedEffort = nextModel, nextEffort, ""
		if changed {
			s.ResolvedModel = ""
			var usage *SessionUsage
			err := decodeSQLiteRecord(ctx, st.tx, "session_usage", sqliteOwnedKey(id, ""), 64<<10, &usage)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if usage != nil {
				usage.Context = nil
				if err := st.putSingleChild("session_usage", id, usage); err != nil {
					return err
				}
			}
		}
		if yolo != nil {
			s.YOLO = *yolo
		}
		s.UpdatedAt = now()
		return st.putSessionMeta(s)
	})
}

func (a *app) changeSQLGraphSelection(ctx context.Context, id, graphID string) error {
	if graphID != "" && !validAuthoringID(graphID) {
		return controlError(400, "Invalid graph identifier.")
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	var project Project
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		s, err := st.sessionMeta(id)
		if err != nil {
			return err
		}
		if s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
			return controlError(404, "Main conversation not found.")
		}
		project, err = st.project(s.ProjectID)
		if err != nil {
			return err
		}
		if project.Removed {
			return controlError(404, "Project not found.")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if graphID != "" {
		catalog, err := loadAuthoring(project)
		if err != nil {
			return err
		}
		found := false
		for _, g := range catalog.Graphs {
			if g.ID == graphID {
				if !g.Definition.Enabled {
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
	return a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		s, err := st.sessionMeta(id)
		if err != nil {
			return err
		}
		p, err := st.project(project.ID)
		if err != nil {
			return err
		}
		if p.Removed || p.Folder != project.Folder || s.ProjectID != project.ID {
			return controlError(409, "Conversation or project changed; retry selection.")
		}
		if r, err := st.activeAutomation(id); err != nil {
			return err
		} else if r != nil {
			return controlError(409, "Automation run settings are read-only until the run ends.")
		}
		if s.SelectedGraphID == graphID {
			return nil
		}
		s.SelectedGraphID, s.UpdatedAt = graphID, now()
		return st.putSessionMeta(s)
	})
}
