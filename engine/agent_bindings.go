package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
)

func (st *sqliteSessionTx) saveAgentConfiguration(id string, binding SessionAgentBinding, res AgentResolution, forceSelectionRevision bool) error {
	s, err := st.sessionMeta(id)
	if err != nil {
		return err
	}
	binding.Desired = res.Config
	binding.SourceStatus = res.SourceStatus
	binding.Blocked = res.Blocked
	if res.Blocked == nil && res.SourceStatus == "linked" {
		binding.LastCaptured = res.Capture
		binding.Source = res.Capture.Source
	}
	if err := st.putAgentBinding(id, &binding); err != nil {
		return err
	}
	s.AgentConfig = agentSummary(binding, res)
	if s.Model != res.Config.Model {
		s.ResolvedModel = ""
		var usage *SessionUsage
		if err := decodeSQLiteRecord(st.ctx, st.tx, "session_usage", sqliteOwnedKey(id, ""), 64<<10, &usage); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if usage != nil {
			usage.Context = nil
			if err := st.putSingleChild("session_usage", id, usage); err != nil {
				return err
			}
		}
	}
	s.ResolvedEffort = ""
	s.Model, s.Effort = res.Config.Model, res.Config.Effort
	s.UpdatedAt = now()
	if res.Blocked == nil || forceSelectionRevision {
		key := sqliteOwnedKey(id, "")
		var caps ConversationCapabilities
		var desired CapabilitySelection
		if err := decodeSQLiteRecord(st.ctx, st.tx, "session_capabilities", key, 64<<10, &caps); err != nil {
			return err
		}
		if err := decodeSQLiteRecord(st.ctx, st.tx, "capability_selections", sqliteOwnedKey(key, ""), 8<<20, &desired); err != nil {
			return err
		}
		if forceSelectionRevision || !reflect.DeepEqual(copySelection(desired), copySelection(res.Config.Selection)) {
			if caps.Revision == ^uint64(0) {
				return errors.New("Selection revision exhausted.")
			}
			caps.Revision++
			if err := st.putExistingRecord("session_capabilities", key, caps); err != nil {
				return err
			}
			if err := st.putSingleChild("capability_selections", key, copySelection(res.Config.Selection)); err != nil {
				return err
			}
		}
	}
	return st.putSessionMeta(s)
}

func (a *app) sessionAgentState(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == http.MethodPatch {
		var body struct {
			Revision  string             `json:"revision"`
			Overrides AgentOverridePatch `json:"overrides"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body.Revision == "" {
			agentHTTPError(w, agentProblem(400, "revision_required", "revision", "Supply the current binding revision."))
			return
		}
		if err := a.patchAgentBinding(r.Context(), id, body.Revision, "", body.Overrides, nil); err != nil {
			agentHTTPError(w, err)
			return
		}
	}
	s, binding, cwd, err := a.readBoundAgent(r.Context(), id)
	if err != nil {
		agentHTTPError(w, err)
		return
	}
	if binding == nil {
		respond(w, 200, map[string]any{"agent": nil})
		return
	}
	res, err := a.resolveBoundAgent(r.Context(), BoundAgentRequest{Binding: *binding, FixedHarness: s.Harness, ProjectID: s.ProjectID, ExecutionCWD: cwd})
	if err != nil {
		agentHTTPError(w, err)
		return
	}
	summary := agentSummary(*binding, res)
	// External capture inventory/credential references are engine-owned; detail
	// exposes configuration and provenance, not gateway/private snapshot payloads.
	var applied *AgentExecutionCapture
	if binding.Applied != nil {
		copy := *binding.Applied
		copy.ExternalCapabilities = nil
		applied = &copy
	}
	respond(w, 200, map[string]any{"agent": struct {
		*AgentConfigSummary
		Revision  string                 `json:"revision"`
		Overrides AgentConfigOverrides   `json:"overrides"`
		Inherited CapabilitySelection    `json:"inheritedCapabilities"`
		Desired   ResolvedAgentConfig    `json:"desired"`
		Applied   *AgentExecutionCapture `json:"applied,omitempty"`
	}{summary, binding.Revision, binding.Overrides, binding.InheritedCapabilities, res.Config, applied}})
}

func (a *app) patchAgentBinding(ctx context.Context, id, expected, selectionRevision string, patch AgentOverridePatch, yolo *bool) error {
	s, before, cwd, err := a.readBoundAgent(ctx, id)
	if err != nil {
		return err
	}
	if before == nil {
		return agentProblem(404, "agent_not_bound", "agent", "This session does not use a saved agent.")
	}
	if expected != "" && before.Revision != expected {
		return agentProblem(409, "binding_changed", "revision", "Agent settings changed. Reload before saving.")
	}
	updated := cloneAgentBinding(*before)
	updated.Overrides, err = applyAgentOverridePatch(updated.Overrides, patch)
	if err != nil {
		return agentProblem(400, "invalid_overrides", "overrides", err.Error())
	}
	res, err := a.resolveBoundAgent(ctx, BoundAgentRequest{Binding: updated, FixedHarness: s.Harness, ProjectID: s.ProjectID, ExecutionCWD: cwd})
	if err != nil {
		return err
	}
	if res.Blocked != nil && (res.Blocked.Code == "invalid_selection" || (patch.Model.Present || patch.Effort.Present) && (res.Blocked.Code == "incompatible_model" || res.Blocked.Code == "incompatible_effort")) {
		return res.Blocked
	}
	updated.Revision, err = nextAgentRevision(before.Revision)
	if err != nil {
		return err
	}
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		current, err := st.agentBinding(id)
		if err != nil {
			return err
		}
		if current == nil || current.Revision != before.Revision {
			return agentProblem(409, "binding_changed", "revision", "Agent settings changed. Reload before saving.")
		}
		meta, err := st.sessionMeta(id)
		if err != nil {
			return err
		}
		if meta.Harness != s.Harness {
			return agentProblem(409, "settings_changed", "harness", "Session settings changed.")
		}
		if (patch.Model.Present || patch.Effort.Present) && (a.runs[id] != nil || meta.Status == "running") {
			return controlError(409, "Wait for the current turn before changing model or effort.")
		}
		if run, err := st.activeAutomation(id); err != nil {
			return err
		} else if run != nil {
			return controlError(409, "Automation settings are read-only until the run ends.")
		}
		var pending bool
		if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return controlError(409, "Finish Library recovery before editing agent settings.")
		}
		if selectionRevision != "" {
			var caps ConversationCapabilities
			if err := decodeSQLiteRecord(ctx, st.tx, "session_capabilities", sqliteOwnedKey(id, ""), 64<<10, &caps); err != nil {
				return err
			}
			if sqliteRevision(caps.Revision) != selectionRevision {
				return controlError(409, "Selection changed. Reload before saving.")
			}
		}
		// Preserve an application captured while this patch was validating.
		updated.Applied = current.Applied
		if err := st.saveAgentConfiguration(id, updated, res, patch.Skills.Present || patch.MCPConnections.Present); err != nil {
			return err
		}
		if yolo != nil {
			meta, err = st.sessionMeta(id)
			if err != nil {
				return err
			}
			meta.YOLO = *yolo
			return st.putSessionMeta(meta)
		}
		return nil
	})
}

func mergeAgentOverrides(overrides *AgentConfigOverrides, model, effort string) (AgentConfigOverrides, error) {
	result := AgentConfigOverrides{}
	if overrides != nil {
		raw, _ := json.Marshal(overrides)
		_ = json.Unmarshal(raw, &result)
	}
	if model != "" {
		if result.Model != nil {
			return result, agentProblem(400, "duplicate_override", "model", "Specify model in only one location.")
		}
		result.Model = &model
	}
	if effort != "" {
		if result.Effort != nil {
			return result, agentProblem(400, "duplicate_override", "effort", "Specify effort in only one location.")
		}
		result.Effort = &effort
	}
	return result, nil
}

func (a *app) createManualAgentSession(ctx context.Context, projectID, title, topic, harness, model, effort, agent, uid, sourceRevision string, overrides *AgentConfigOverrides) (string, error) {
	if a.store == nil {
		return "", agentProblem(503, "storage_unavailable", "agent", "Saved agents require SQLite storage.")
	}
	project, err := a.store.readProjectMeta(ctx, projectID)
	if err != nil {
		return "", err
	}
	if project.Removed {
		return "", sql.ErrNoRows
	}
	settings, err := mergeAgentOverrides(overrides, model, effort)
	if err != nil {
		return "", err
	}
	res, err := a.resolveNewAgent(ctx, NewAgentRequest{ProjectID: projectID, Agent: agent, SourceUID: uid, Revision: sourceRevision, Harness: harness, Overrides: settings, ExecutionCWD: project.Folder})
	if err != nil {
		return "", err
	}
	binding := bindingForResolution(res, settings, CapabilitySelection{})
	s := newTopLevelSession(projectID, title, "Ungrouped", res.Config.Harness, res.Config.Model, res.Config.Effort)
	s.TopicID = topic
	s.Capabilities.Desired = copySelection(res.Config.Selection)
	s.AgentConfig = agentSummary(*binding, res)
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	if err := a.recheckNewAgentLocked(ctx, res, projectID); err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		if err := st.createSession(s, a.dir); err != nil {
			return err
		}
		return st.putAgentBinding(s.ID, binding)
	})
	return s.ID, err
}
