package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

type NewAgentRequest struct {
	ProjectID, Agent, SourceUID, Revision, Harness, ExecutionCWD string
	Overrides                                                    AgentConfigOverrides
	InheritedCapabilities                                        CapabilitySelection
}
type BoundAgentRequest struct {
	Binding                               SessionAgentBinding
	FixedHarness, ProjectID, ExecutionCWD string
}

func cloneAgentBinding(binding SessionAgentBinding) SessionAgentBinding {
	raw, _ := json.Marshal(binding)
	var result SessionAgentBinding
	_ = json.Unmarshal(raw, &result)
	return result
}
func agentConfigDigest(config ResolvedAgentConfig) string {
	raw, _ := json.Marshal(config)
	return revision(raw)
}
func nextAgentRevision(value string) (string, error) {
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n == ^uint64(0) {
		return "", errors.New("Invalid or exhausted agent binding revision.")
	}
	return sqliteRevision(n + 1), nil
}

func agentSummary(binding SessionAgentBinding, resolution AgentResolution) *AgentConfigSummary {
	fields := []string{}
	if binding.Overrides.Model != nil {
		fields = append(fields, "model")
	}
	if binding.Overrides.Effort != nil {
		fields = append(fields, "effort")
	}
	if binding.Overrides.Prompt != nil {
		fields = append(fields, "prompt")
	}
	if binding.Overrides.Skills != nil {
		fields = append(fields, "skills")
	}
	if binding.Overrides.MCPConnections != nil {
		fields = append(fields, "mcpConnections")
	}
	summary := &AgentConfigSummary{Source: resolution.Capture.Source, Name: resolution.Capture.Name, SourceStatus: resolution.SourceStatus, CapturedRevision: binding.LastCaptured.Revision, OverriddenFields: fields, Execution: AgentExecutionEligibility{Allowed: resolution.Blocked == nil, Token: resolution.Token}}
	if resolution.Blocked != nil {
		summary.Execution.Code = resolution.Blocked.Code
		summary.Execution.Message = resolution.Blocked.Message
		switch resolution.Blocked.Field {
		case "model", "effort", "prompt", "skills", "mcpConnections":
			summary.Execution.Fields = []string{resolution.Blocked.Field}
		case "selection":
			summary.Execution.Fields = []string{"skills", "mcpConnections"}
		}
	}
	summary.Pending = binding.Applied == nil || agentConfigDigest(binding.Applied.Config) != agentConfigDigest(resolution.Config) || binding.Applied.Source != nil && binding.Applied.Source.Revision != resolution.Capture.Revision
	if resolution.Capture.Revision != binding.LastCaptured.Revision {
		summary.PendingRevision = resolution.Capture.Revision
	}
	return summary
}

func (a *app) validateResolvedAgent(ctx context.Context, config ResolvedAgentConfig, projectID, cwd string) (ResolvedAgentConfig, error) {
	if _, installed := a.binarySnapshot()[config.Harness]; !installed {
		return config, agentProblem(409, "harness_unavailable", "harness", "The session's selected harness is not installed.")
	}
	catalog, err := a.catalog(ctx, Session{ProjectID: projectID, Harness: config.Harness}, cwd, false)
	if err != nil {
		return config, agentProblem(502, "catalog_unavailable", "model", modelCatalogLoadError)
	}
	if config.Model == "" {
		config.Model = catalog.DefaultModel
	}
	option := catalog.find(config.Model)
	if option == nil {
		return config, agentProblem(409, "incompatible_model", "model", "The agent model is unavailable in this session's fixed harness. Choose a compatible model.")
	}
	if config.Effort == "" {
		config.Effort = option.DefaultEffort
		if config.Model == catalog.DefaultModel && catalog.DefaultEffort != "" {
			config.Effort = catalog.DefaultEffort
		}
	}
	if config.Effort != "" && (len(option.Efforts) > 0 && !slices.Contains(option.Efforts, config.Effort) || len(option.Efforts) == 0) {
		return config, agentProblem(409, "incompatible_effort", "effort", "The agent model/effort pair is incompatible with this session's fixed harness. Choose an effort explicitly.")
	}
	return config, nil
}

// authoringMu held. Identity adoption changes only a top-level marker prefix;
// comments, prompt bytes and existing TOML content are preserved exactly.
func (a *app) adoptAgentSourceLocked(ctx context.Context, row CatalogAgentRecord, p Project) (CatalogAgentRecord, error) {
	if row.Source.Scope != "project" {
		return row, nil
	}
	data, err := readConfig(p, agentFile(row.ID))
	if err != nil {
		return row, err
	}
	if revision(data) != row.Revision {
		return row, agentProblem(409, "source_changed", "revision", "Agent changed during identity adoption.")
	}
	doc, err := readAgentDocument(data)
	if err != nil {
		return row, err
	}
	if doc.SourceUID != "" {
		return row, nil
	}
	prefix := []byte(fmt.Sprintf("source_uid = %q\n", row.Source.SourceUID))
	// Preserve an optional UTF-8 BOM at the very beginning.
	marked := append(prefix, data...)
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		marked = append([]byte{0xef, 0xbb, 0xbf}, append(prefix, data[3:]...)...)
	}
	newRevision := revision(marked)
	source := AgentSourceRecord{Adopted: true, ID: agentSourceRecordID(row.Source, agentFile(row.ID)), Source: row.Source, Path: agentFile(row.ID), Revision: newRevision, State: "linked"}
	change := AgentCatalogChange{ID: newID(), Sources: []AgentSourceRecord{source}, Files: []AgentAuthoringWrite{{Project: Project{ID: p.ID, Name: p.Name, Folder: p.Folder}, Expected: map[string]string{agentFile(row.ID): row.Revision}, Writes: map[string][]byte{agentFile(row.ID): marked}}}}
	if err := a.commitAgentCatalogChange(ctx, change); err != nil {
		return row, err
	}
	row.Revision = newRevision
	return row, nil
}

func (a *app) resolveNewAgent(ctx context.Context, req NewAgentRequest) (AgentResolution, error) {
	a.authoringMu.Lock()
	scan, err := a.scanAgentCatalogLocked(ctx, req.ProjectID)
	if err != nil {
		a.authoringMu.Unlock()
		return AgentResolution{}, err
	}
	var row *CatalogAgentRecord
	for i := range scan.Agents {
		candidate := &scan.Agents[i]
		if candidate.ID == authoringSlug(req.Agent) {
			if row != nil {
				a.authoringMu.Unlock()
				return AgentResolution{}, agentProblem(409, "agent_conflict", "agent", "Agent identifier has conflicting sources.")
			}
			row = candidate
		}
	}
	if row == nil {
		a.authoringMu.Unlock()
		return AgentResolution{}, agentProblem(404, "agent_not_found", "agent", "Saved agent not found in this scope.")
	}
	if !row.Available {
		a.authoringMu.Unlock()
		return AgentResolution{}, agentProblem(409, "agent_unavailable", "agent", "Select an enabled, unconflicted agent from a complete catalog.")
	}
	if req.SourceUID != "" && row.Source.SourceUID != req.SourceUID || req.Revision != "" && row.Revision != req.Revision {
		a.authoringMu.Unlock()
		return AgentResolution{}, agentProblem(409, "source_changed", "agent", "The selected agent changed. Reload before creating.")
	}
	selected := *row
	a.authoringMu.Unlock()
	if err != nil {
		return AgentResolution{}, err
	}
	capture := AgentCapture{Name: selected.Name, Source: selected.Source, Revision: selected.Revision, Config: agentConfig(selected.AgentDefinition)}
	config, err := resolveAgentConfig(capture.Config, req.Harness, req.Overrides, req.InheritedCapabilities)
	if err != nil {
		return AgentResolution{}, agentProblem(400, "invalid_selection", "selection", err.Error())
	}
	config, err = a.validateResolvedAgent(ctx, config, req.ProjectID, req.ExecutionCWD)
	if err != nil {
		return AgentResolution{}, err
	}
	if selected.Source.Scope == "project" {
		a.authoringMu.Lock()
		if err := a.recheckNewAgentLocked(ctx, AgentResolution{Capture: capture}, req.ProjectID); err != nil {
			a.authoringMu.Unlock()
			return AgentResolution{}, err
		}
		var pending bool
		if err := a.store.readers.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&pending); err != nil {
			a.authoringMu.Unlock()
			return AgentResolution{}, err
		}
		if pending {
			a.authoringMu.Unlock()
			return AgentResolution{}, agentProblem(409, "recovery_pending", "agent", "Finish Library recovery before adopting a saved agent.")
		}
		var project Project
		for _, p := range scan.projects {
			if p.ID == req.ProjectID {
				project = p
			}
		}
		selected, err = a.adoptAgentSourceLocked(ctx, selected, project)
		a.authoringMu.Unlock()
		if err != nil {
			return AgentResolution{}, err
		}
		capture.Revision = selected.Revision
	}
	return AgentResolution{Config: config, Capture: capture, SourceStatus: "linked", Token: agentConfigDigest(config) + "/" + capture.Revision}, nil
}

func (a *app) resolveBoundAgent(ctx context.Context, req BoundAgentRequest) (AgentResolution, error) {
	binding := req.Binding
	result := AgentResolution{Capture: binding.LastCaptured, SourceStatus: "deleted"}
	a.authoringMu.Lock()
	scan, err := a.scanAgentCatalogLocked(ctx, req.ProjectID)
	a.authoringMu.Unlock()
	if err != nil {
		result.SourceStatus = "source_unavailable"
		result.Blocked = agentProblem(409, "source_unavailable", "agent", err.Error())
	} else {
		owners := []CatalogAgentRecord{}
		for _, row := range scan.all {
			if row.Source.SourceUID == binding.Source.SourceUID {
				owners = append(owners, row)
			}
		}
		if len(owners) > 1 {
			result.SourceStatus = "conflict"
			result.Blocked = agentProblem(409, "source_identity_conflict", "agent", "Agent source identity has conflicting definitions.")
		} else if len(owners) == 1 {
			row := owners[0]
			if row.Source.Scope != binding.Source.Scope || row.Source.ProjectID != binding.Source.ProjectID {
				confirmed := false
				for _, record := range scan.sources {
					if record.Source.SourceUID == binding.Source.SourceUID && record.Source.Scope == binding.Source.Scope && record.Source.ProjectID == binding.Source.ProjectID && record.State == "deleted" {
						confirmed = true
					}
				}
				if confirmed {
					row.Status = "deleted"
				} else {
					row.Status = "source_replaced"
				}
			}
			result.SourceStatus = row.Status
			switch row.Status {
			case "linked":
				result.Capture = AgentCapture{Name: row.Name, Source: row.Source, Revision: row.Revision, Config: agentConfig(row.AgentDefinition)}
			case "disabled", "deleted":
			default:
				result.Blocked = agentProblem(409, row.Status, "agent", "The agent source is invalid, unavailable or conflicted. Restore its source before another turn.")
			}
		} else {
			confirmedDeleted := false
			for _, record := range scan.sources {
				if record.Source.SourceUID == binding.Source.SourceUID && record.State == "deleted" {
					confirmedDeleted = true
				}
				if record.Source.SourceUID == binding.Source.SourceUID && record.State != "deleted" {
					result.SourceStatus = record.State
					result.Blocked = agentProblem(409, "source_unavailable", "agent", "Cannot confirm agent source availability. Restore access before another turn.")
					break
				}
			}
			if !confirmedDeleted && result.Blocked == nil {
				result.SourceStatus = "source_unavailable"
				result.Blocked = agentProblem(409, "source_unavailable", "agent", "Agent source identity is unavailable; deletion was not confirmed.")
			}
		}
	}
	if err == nil && scan != nil {
		live := 0
		for _, record := range scan.sources {
			if record.Source.SourceUID == binding.Source.SourceUID && record.State != "deleted" {
				live++
			}
		}
		if live > 1 {
			result.SourceStatus = "conflict"
			result.Blocked = agentProblem(409, "source_identity_conflict", "agent", "Agent source UID is shared by multiple known source locators.")
		}
	}
	config, composeErr := resolveAgentConfig(result.Capture.Config, req.FixedHarness, binding.Overrides, binding.InheritedCapabilities)
	result.Config = config
	if composeErr != nil {
		result.Blocked = agentProblem(409, "invalid_selection", "selection", composeErr.Error())
	}
	if result.Blocked == nil {
		validated, validationErr := a.validateResolvedAgent(ctx, config, req.ProjectID, req.ExecutionCWD)
		if validationErr != nil {
			var problem *AgentConfigProblem
			if errors.As(validationErr, &problem) {
				result.Blocked = problem
			} else {
				return result, validationErr
			}
		} else {
			result.Config = validated
		}
	}
	result.Token = agentConfigDigest(result.Config) + "/" + result.Capture.Revision + "/" + binding.Revision
	return result, nil
}

func bindingForResolution(res AgentResolution, overrides AgentConfigOverrides, inherited CapabilitySelection) *SessionAgentBinding {
	return &SessionAgentBinding{Source: res.Capture.Source, Revision: "1", LastCaptured: res.Capture, Overrides: overrides, InheritedCapabilities: copySelection(inherited), Desired: res.Config, SourceStatus: res.SourceStatus}
}

func (a *app) recheckNewAgentLocked(ctx context.Context, res AgentResolution, projectID string) error {
	scan, err := a.scanAgentCatalogLocked(ctx, projectID)
	if err != nil {
		return err
	}
	for _, row := range scan.Agents {
		if row.Source.SourceUID == res.Capture.Source.SourceUID && row.ID == res.Capture.Source.ID && row.Available && row.Revision == res.Capture.Revision {
			return nil
		}
	}
	return agentProblem(409, "source_changed", "agent", "Agent changed during preparation. Retry with current configuration.")
}

func (a *app) recheckBoundAgentLocked(ctx context.Context, res AgentResolution, projectID string) error {
	scan, err := a.scanAgentCatalogLocked(ctx, projectID)
	if err != nil {
		return err
	}
	owners := 0
	for _, source := range scan.sources {
		if source.Source.SourceUID == res.Capture.Source.SourceUID && source.State != "deleted" {
			owners++
		}
	}
	if owners != 1 {
		return agentProblem(409, "source_identity_conflict", "agent", "Bound agent identity changed during preparation.")
	}
	for _, row := range scan.all {
		if row.Source.SourceUID == res.Capture.Source.SourceUID && row.Source.Scope == res.Capture.Source.Scope && row.Source.ProjectID == res.Capture.Source.ProjectID && row.Status == "linked" && row.Revision == res.Capture.Revision {
			return nil
		}
	}
	return agentProblem(409, "source_changed", "agent", "Bound agent changed during preparation. Retry with current settings.")
}

func (a *app) readBoundAgent(ctx context.Context, id string) (Session, *SessionAgentBinding, string, error) {
	var s Session
	var binding *SessionAgentBinding
	var cwd string
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		s, err = st.sessionMeta(id)
		if err != nil {
			return err
		}
		binding, err = st.agentBinding(id)
		if err != nil {
			return err
		}
		cwd, err = st.conversationDirectory(s)
		return err
	})
	return s, binding, cwd, err
}
