package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// Graphs remain project files. Only the agent view is unified; ordinary authoring
// diagnostics do not silently pick a conflicting source. Caller owns authoringMu.
func (a *app) graphAuthoringLocked(ctx context.Context, p Project) (AuthoringCatalog, error) {
	catalog, err := loadAuthoring(p)
	if err != nil || a.store == nil {
		return catalog, err
	}
	scan, err := a.scanAgentCatalogLocked(ctx, p.ID)
	if err != nil {
		return catalog, err
	}
	catalog.Agents = []AgentRecord{}
	for _, row := range scan.Agents {
		if row.Status == "linked" || row.Status == "disabled" {
			catalog.Agents = append(catalog.Agents, row.AgentRecord)
		}
	}
	for _, diagnostic := range scan.Diagnostics {
		catalog.Errors = append(catalog.Errors, diagnostic.Message)
	}
	return catalog, nil
}

func (a *app) frozenSourceBytes(ctx context.Context, p Project, row CatalogAgentRecord) ([]byte, error) {
	if row.Source.Scope == "project" {
		data, err := readConfig(p, agentFile(row.ID))
		if err != nil {
			return nil, err
		}
		if revision(data) != row.Revision {
			return nil, errors.New("Local agent changed during graph capture.")
		}
		return data, nil
	}
	if row.Source.Scope != "global" || row.Source.ProjectID != "" {
		return nil, errors.New("Invalid graph agent source scope.")
	}
	var global GlobalAgentRecord
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		return decodeSQLiteRecord(ctx, tx, "global_agents", row.Source.SourceUID, 8<<20, &global)
	})
	if err != nil {
		return nil, err
	}
	data, err := toml.Marshal(global.Definition)
	if err != nil {
		return nil, err
	}
	if global.ID != row.Source.SourceUID || global.Slug != row.ID || global.Revision != row.Revision || revision(data) != row.Revision {
		return nil, errors.New("Global agent changed or its canonical source is inconsistent.")
	}
	return data, nil
}

func (a *app) validateGraphCaptureModel(ctx context.Context, p Project, agent EffectiveGraphAgent) error {
	if _, ok := a.binarySnapshot()[agent.Harness]; !ok {
		return errors.New("Graph agent harness is not installed.")
	}
	catalog, err := a.catalog(ctx, Session{ProjectID: p.ID, Harness: agent.Harness}, p.Folder, false)
	if err != nil {
		return errors.New("Could not validate graph model catalog.")
	}
	model := catalog.find(agent.Model)
	if model == nil {
		return errors.New("Graph model is unavailable in its harness.")
	}
	if len(model.Efforts) > 0 && !slices.Contains(model.Efforts, agent.Effort) || len(model.Efforts) == 0 && agent.Effort != "" {
		return errors.New("Invalid graph model/effort pair.")
	}
	return nil
}

func (a *app) captureGraphSnapshotV2(ctx context.Context, projectID, graphID string) (GraphSnapshot, *CompiledGraph, error) {
	var snapshot GraphSnapshot
	release, err := a.acquireHarnessConsumer()
	if err != nil {
		return snapshot, nil, err
	}
	defer release()
	if !validAuthoringID(graphID) {
		return snapshot, nil, errors.New("Invalid graph identifier.")
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	p, err := a.store.readProjectMeta(ctx, projectID)
	if err != nil {
		return snapshot, nil, err
	}
	if p.Removed {
		return snapshot, nil, errors.New("Graph project is unavailable.")
	}
	var pending bool
	if err := a.store.readers.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&pending); err != nil {
		return snapshot, nil, err
	}
	if pending {
		return snapshot, nil, errors.New("Finish Library recovery before graph capture.")
	}
	if err := recoverAuthoring(p); err != nil {
		return snapshot, nil, err
	}
	scan, err := a.scanAgentCatalogLocked(ctx, p.ID)
	if err != nil {
		return snapshot, nil, err
	}
	graphBytes, err := readConfig(p, graphFile(graphID))
	if err != nil {
		return snapshot, nil, err
	}
	graph := GraphDefinition{Enabled: true}
	decoder := yaml.NewDecoder(bytes.NewReader(graphBytes))
	decoder.KnownFields(true)
	if err := decoder.Decode(&graph); err != nil {
		return snapshot, nil, err
	}
	if decoder.Decode(new(any)) != io.EOF || authoringSlug(graph.Name) != graphID {
		return snapshot, nil, errors.New("Invalid graph source identity/document.")
	}
	snapshot = GraphSnapshot{Version: 2, ProjectID: p.ID, GraphID: graphID, CapturedAt: now(), Agents: []AgentRecord{}, Sources: map[string]GraphSourceSnapshot{graphFile(graphID): {Revision: revision(graphBytes), Text: string(graphBytes)}}, GlobalSources: map[string]GraphSourceSnapshot{}, AgentOrigins: map[string]AgentSourceRef{}, Capabilities: map[string]*CapabilitySnapshot{}}
	byID := map[string]CatalogAgentRecord{}
	conflicts := map[string]bool{}
	for _, row := range scan.Agents {
		_, exists := byID[row.ID]
		if exists || conflicts[row.ID] {
			delete(byID, row.ID)
			conflicts[row.ID] = true
			continue
		}
		byID[row.ID] = row
	}
	references := map[string]bool{}
	for _, node := range graph.Nodes {
		if node.Agent != "" {
			references[node.Agent] = true
		}
	}
	for _, id := range sortedGraphKeys(references) {
		row, ok := byID[id]
		if !ok || !row.Available {
			return snapshot, nil, fmt.Errorf("Graph node needs an enabled, unconflicted source %s.", id)
		}
		data, err := a.frozenSourceBytes(ctx, p, row)
		if err != nil {
			return snapshot, nil, err
		}
		snapshot.Agents = append(snapshot.Agents, row.AgentRecord)
		snapshot.AgentOrigins[id] = row.Source
		source := GraphSourceSnapshot{Revision: revision(data), Text: string(data)}
		if row.Source.Scope == "global" {
			snapshot.GlobalSources[id] = source
		} else {
			snapshot.Sources[agentFile(id)] = source
		}
	}
	compiled, err := compileGraph(graph, snapshot.Agents, func(agent EffectiveGraphAgent) error { return a.validateGraphCaptureModel(ctx, p, agent) })
	if err != nil {
		return snapshot, nil, err
	}
	for nodeID, effective := range compiled.Agents {
		row := byID[effective.AgentID]
		captured, err := a.captureCapabilities(ctx, agentConfig(row.AgentDefinition).Selection, effective.Harness)
		if err != nil {
			return snapshot, nil, fmt.Errorf("Node %s: %w", nodeID, err)
		}
		snapshot.Capabilities[nodeID] = captured
	}
	frozen := &GraphFrozenAgentCatalog{Agents: map[string]GraphFrozenAgent{}, Complete: scan.Complete, Diagnostics: []AgentDiagnostic{}, CatalogRevision: scan.Revision}
	addDiagnostic := func(diagnostic AgentDiagnostic) {
		if len(frozen.Diagnostics) < 100 {
			frozen.Diagnostics = append(frozen.Diagnostics, diagnostic)
		} else {
			frozen.DiagnosticsTruncated = true
		}
	}
	for _, diagnostic := range scan.Diagnostics {
		addDiagnostic(diagnostic)
	}
	totalBytes := 0
	for _, row := range scan.Agents {
		if !row.Available {
			source := row.Source
			addDiagnostic(AgentDiagnostic{Code: "agent_not_eligible", Message: "Specialist was not eligible at run capture: " + row.UnavailableReason, Source: &source})
			continue
		}
		data, err := a.frozenSourceBytes(ctx, p, row)
		if err != nil {
			return snapshot, nil, err
		}
		config, err := a.validateResolvedAgent(ctx, agentConfig(row.AgentDefinition), p.ID, p.Folder)
		var external *CapabilitySnapshot
		if err == nil {
			external, err = a.captureCapabilities(ctx, config.Selection, config.Harness)
		}
		if err != nil {
			source := row.Source
			addDiagnostic(AgentDiagnostic{Code: "agent_capture_unavailable", Message: err.Error(), Source: &source})
			continue
		}
		capture := AgentCapture{Name: row.Name, Source: row.Source, Revision: row.Revision, Config: agentConfig(row.AgentDefinition)}
		entry := GraphFrozenAgent{Definition: row.AgentDefinition, Source: row.Source, SourceRevision: row.Revision, SourceLocator: frozenAgentLocator(row.Source), SourceText: string(data), Execution: AgentExecutionCapture{Config: config, Source: &capture, Revision: agentConfigDigest(config), ExternalCapabilities: external}}
		raw, _ := json.Marshal(entry)
		totalBytes += len(raw)
		if len(frozen.Agents) >= graphFrozenAgentLimit || totalBytes > graphFrozenCatalogByteLimit {
			return snapshot, nil, agentProblem(409, "delegation_catalog_too_large", "agent", "Frozen graph specialist catalog exceeds 256 eligible definitions or 16 MiB; no eligible entries were truncated.")
		}
		frozen.Agents[row.ID] = entry
	}
	// Public model metadata only, frozen at the same run-start boundary. This
	// validates child overrides without provider/catalog discovery inside a node.
	frozen.ModelCatalogs = map[string]ModelCatalog{}
	harnesses := map[string]bool{}
	for _, agent := range compiled.Agents {
		harnesses[agent.Harness] = true
	}
	for _, agent := range frozen.Agents {
		harnesses[agent.Execution.Config.Harness] = true
	}
	for _, harness := range sortedGraphKeys(harnesses) {
		catalog, err := a.catalog(ctx, Session{ProjectID: p.ID, Harness: harness}, p.Folder, false)
		if err != nil {
			return snapshot, nil, err
		}
		raw, _ := json.Marshal(catalog)
		var public ModelCatalog
		if err := json.Unmarshal(raw, &public); err != nil {
			return snapshot, nil, err
		}
		totalBytes += len(raw)
		if totalBytes > graphFrozenCatalogByteLimit {
			return snapshot, nil, errors.New("Frozen specialist/model catalog exceeds 16 MiB.")
		}
		frozen.ModelCatalogs[harness] = public
	}
	sealGraphFrozenCatalog(frozen)
	snapshot.DelegationCatalog = frozen
	layout := GraphLayout{Positions: map[string]Point{}}
	if raw, err := readConfig(p, layoutFile(graphID)); err == nil {
		var candidate GraphLayout
		if json.Unmarshal(raw, &candidate) == nil {
			discardInvalidGraphHandles(&candidate)
			if validateLayout(candidate) == nil && candidate.Positions != nil {
				layout = candidate
			}
		}
	}
	snapshot.Graph = GraphRecord{ID: graphID, Revision: revision(graphBytes), Definition: compiled.Definition, Layout: layout}
	snapshot.EffectiveAgents = compiled.Agents
	library, _ := json.Marshal(snapshot.Capabilities)
	snapshot.LibraryRevision = revision(library)
	snapshot.Revision = graphSnapshotV2Revision(snapshot)
	if err := a.revalidateFrozenGraphCatalog(snapshot); err != nil {
		return snapshot, nil, err
	}
	current, err := readConfig(p, graphFile(graphID))
	if err != nil || revision(current) != snapshot.Graph.Revision {
		return snapshot, nil, errors.New("Graph source changed during snapshot capture.")
	}
	if err := validateGraphSnapshot(snapshot); err != nil {
		return snapshot, nil, err
	}
	return snapshot, compiled, nil
}

// Caller owns authoringMu/libraryMu, no app lock. Existing scheduler can retain
// its local Sources recheck: globals use the explicit manifest here, not a fake
// project-relative path passed to configPath.
func (a *app) revalidateFrozenGraphCatalog(snapshot GraphSnapshot) error {
	if snapshot.Version != 2 || snapshot.DelegationCatalog == nil {
		return nil
	}
	scan, err := a.scanAgentCatalogLocked(a.ctx, snapshot.ProjectID)
	if err != nil {
		return err
	}
	if scan.Revision != snapshot.DelegationCatalog.CatalogRevision {
		return errors.New("Visible specialist catalog changed before graph reservation; capture a fresh run snapshot.")
	}
	byUID := map[string]CatalogAgentRecord{}
	for _, row := range scan.Agents {
		byUID[row.Source.SourceUID] = row
	}
	for id, origin := range snapshot.AgentOrigins {
		row, ok := byUID[origin.SourceUID]
		if !ok || !row.Available || row.ID != id {
			return errors.New("Graph node origin changed before reservation.")
		}
		data, err := a.frozenSourceBytes(a.ctx, Project{ID: snapshot.ProjectID, Folder: scanProjectDirectory(scan, snapshot.ProjectID)}, row)
		if err != nil {
			return err
		}
		if origin.Scope == "global" && revision(data) != snapshot.GlobalSources[id].Revision || origin.Scope == "project" && revision(data) != snapshot.Sources[agentFile(id)].Revision {
			return errors.New("Graph node source changed before reservation.")
		}
	}
	return nil
}

func scanProjectDirectory(scan *agentCatalogScan, id string) string {
	for _, project := range scan.projects {
		if project.ID == id {
			return project.Folder
		}
	}
	return ""
}
