package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

type GraphSourceSnapshot struct {
	Revision string `json:"revision"`
	Text     string `json:"text"`
}

type GraphSnapshot struct {
	GraphID         string                         `json:"graphId"`
	Revision        string                         `json:"revision"` // content hash of executable sources, not layout
	CapturedAt      string                         `json:"capturedAt"`
	Graph           GraphRecord                    `json:"graph"`
	Agents          []AgentRecord                  `json:"agents"`
	EffectiveAgents map[string]EffectiveGraphAgent `json:"effectiveAgents"`
	Sources         map[string]GraphSourceSnapshot `json:"sources"` // project-relative YAML/TOML
}

// Caller must not hold app.mu. authoringMu prevents our CRUD from interleaving;
// a second complete read detects observed external edits. This is not an external
// multi-file filesystem transaction. Layout errors never invalidate execution.
func (a *app) captureGraphSnapshot(ctx context.Context, projectID, graphID string) (GraphSnapshot, *CompiledGraph, error) {
	var snapshot GraphSnapshot
	if !validAuthoringID(graphID) {
		return snapshot, nil, errors.New("Invalid graph identifier.")
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	a.mu.Lock()
	project := a.state.project(projectID)
	if project == nil || project.Removed {
		a.mu.Unlock()
		return snapshot, nil, errors.New("Project not found.")
	}
	p := *project
	a.mu.Unlock()
	if err := recoverAuthoring(p); err != nil {
		return snapshot, nil, err
	}
	snapshot = GraphSnapshot{GraphID: graphID, CapturedAt: now(), Agents: []AgentRecord{}, Sources: map[string]GraphSourceSnapshot{}}
	readSource := func(path string) ([]byte, error) {
		data, err := readConfig(p, path)
		if err == nil {
			snapshot.Sources[path] = GraphSourceSnapshot{Revision: revision(data), Text: string(data)}
		}
		return data, err
	}
	data, err := readSource(graphFile(graphID))
	if err != nil {
		return snapshot, nil, err
	}
	g := GraphDefinition{Enabled: true}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&g); err != nil {
		return snapshot, nil, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return snapshot, nil, errors.New("Expected one YAML document.")
	}
	if authoringSlug(g.Name) != graphID {
		return snapshot, nil, errors.New("Graph name and filename identifier differ.")
	}
	agentIDs := map[string]bool{}
	for _, node := range g.Nodes {
		if node.Agent != "" {
			agentIDs[node.Agent] = true
		}
	}
	for _, id := range sortedGraphKeys(agentIDs) {
		if !validAuthoringID(id) {
			return snapshot, nil, errors.New("Invalid agent identifier.")
		}
		data, err := readSource(agentFile(id))
		if err != nil {
			return snapshot, nil, fmt.Errorf("Agent %s: %w", id, err)
		}
		agent := AgentDefinition{Enabled: true}
		if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&agent); err != nil {
			return snapshot, nil, err
		}
		if authoringSlug(agent.Name) != id {
			return snapshot, nil, fmt.Errorf("Agent %s name and filename differ.", id)
		}
		snapshot.Agents = append(snapshot.Agents, AgentRecord{AgentDefinition: agent, ID: id, Revision: revision(data), GraphReferences: []string{}})
	}
	validated := map[string]bool{}
	compiled, err := compileGraph(g, snapshot.Agents, func(agent EffectiveGraphAgent) error {
		key := agent.Harness + "\x00" + agent.Model + "\x00" + agent.Effort
		if validated[key] {
			return nil
		}
		catalog, err := a.catalog(ctx, Session{ProjectID: p.ID, Harness: agent.Harness}, p.Folder, false)
		if err != nil {
			return errors.New("Could not validate the effective model against its harness catalog.")
		}
		model := catalog.find(agent.Model)
		if model == nil {
			return errors.New("Effective model is not available in its harness catalog.")
		}
		if len(model.Efforts) > 0 && !slices.Contains(model.Efforts, agent.Effort) || len(model.Efforts) == 0 && agent.Effort != "" {
			return errors.New("Invalid effective effort for the selected model.")
		}
		validated[key] = true
		return nil
	})
	if err != nil {
		return snapshot, nil, err
	}
	if err := ctx.Err(); err != nil {
		return snapshot, nil, err
	}
	for _, path := range sortedGraphKeys(snapshot.Sources) {
		current, err := readConfig(p, path)
		if err != nil || revision(current) != snapshot.Sources[path].Revision {
			return snapshot, nil, fmt.Errorf("Configuration changed during snapshot capture (%s); retry with stable files.", path)
		}
	}
	layout := GraphLayout{Positions: map[string]Point{}}
	if data, err := readConfig(p, layoutFile(graphID)); err == nil {
		var candidate GraphLayout
		if json.Unmarshal(data, &candidate) == nil && validateLayout(candidate) == nil && candidate.Positions != nil {
			layout = candidate
		}
	}
	snapshot.Graph = GraphRecord{ID: graphID, Revision: snapshot.Sources[graphFile(graphID)].Revision, Definition: compiled.Definition, Layout: layout}
	snapshot.EffectiveAgents = compiled.Agents
	encoded, err := json.Marshal(snapshot.Sources)
	if err != nil {
		return snapshot, nil, err
	}
	snapshot.Revision = revision(encoded)
	return snapshot, compiled, nil
}

// Recompilation uses only the run's captured and already catalog-validated model
// settings. Catalog changes and internal prompt edits do not rewrite snapshots.
func compileGraphSnapshot(snapshot GraphSnapshot) (*CompiledGraph, error) {
	return compileGraph(snapshot.Graph.Definition, snapshot.Agents, func(agent EffectiveGraphAgent) error {
		for _, captured := range snapshot.EffectiveAgents {
			if captured == agent {
				return nil
			}
		}
		return errors.New("Effective agent does not match the captured snapshot.")
	})
}

func validateGraphSnapshot(snapshot GraphSnapshot) error {
	if !validAuthoringID(snapshot.GraphID) || snapshot.Graph.ID != snapshot.GraphID || snapshot.Revision == "" || len(snapshot.Sources) == 0 || !validGraphTime(snapshot.CapturedAt) {
		return errors.New("Incomplete graph snapshot.")
	}
	for path, source := range snapshot.Sources {
		if !(strings.HasPrefix(path, ".klm/agents/") && strings.HasSuffix(path, ".toml") || path == graphFile(snapshot.GraphID)) || revision([]byte(source.Text)) != source.Revision {
			return errors.New("Invalid graph snapshot source.")
		}
	}
	encoded, err := json.Marshal(snapshot.Sources)
	if err != nil {
		return err
	}
	if revision(encoded) != snapshot.Revision || snapshot.Sources[graphFile(snapshot.GraphID)].Revision != snapshot.Graph.Revision {
		return errors.New("Graph snapshot revision mismatch.")
	}
	// Check serialized definitions against raw source data, not just their hashes.
	var graph GraphDefinition
	graph.Enabled = true
	if yaml.Unmarshal([]byte(snapshot.Sources[graphFile(snapshot.GraphID)].Text), &graph) != nil {
		return errors.New("Invalid captured graph YAML.")
	}
	left, _ := json.Marshal(graph)
	right, _ := json.Marshal(snapshot.Graph.Definition)
	if !bytes.Equal(left, right) {
		return errors.New("Captured graph definition differs from its source.")
	}
	seen := map[string]bool{}
	for _, record := range snapshot.Agents {
		source, ok := snapshot.Sources[agentFile(record.ID)]
		if !ok || seen[record.ID] || source.Revision != record.Revision {
			return errors.New("Invalid captured agent reference.")
		}
		seen[record.ID] = true
		agent := AgentDefinition{Enabled: true}
		if toml.Unmarshal([]byte(source.Text), &agent) != nil || agent != record.AgentDefinition {
			return errors.New("Captured agent differs from its source.")
		}
	}
	compiled, err := compileGraphSnapshot(snapshot)
	if err != nil {
		return err
	}
	if len(compiled.Agents) != len(snapshot.EffectiveAgents) {
		return errors.New("Invalid captured effective agents.")
	}
	for id, agent := range compiled.Agents {
		if snapshot.EffectiveAgents[id] != agent {
			return errors.New("Invalid captured effective agent ownership.")
		}
	}
	return nil
}

// An outbox coordinates the existing authoring journal with state.json selection
// references. Crash recovery rolls forward only authoring writes, never run work.
type GraphCatalogChange struct {
	ID        string            `json:"id"`
	ProjectID string            `json:"projectId"`
	OldID     string            `json:"oldId"`
	NewID     string            `json:"newId"`
	Writes    map[string][]byte `json:"writes"`
}

func applyGraphCatalogChange(d *diskState, change GraphCatalogChange) {
	for i := range d.Sessions {
		s := &d.Sessions[i]
		if s.ProjectID == change.ProjectID && s.SelectedGraphID == change.OldID {
			s.SelectedGraphID, s.UpdatedAt = change.NewID, now()
		}
	}
	for i := range d.GraphRuns {
		r := &d.GraphRuns[i]
		current := r.GraphID
		if r.CatalogGraphID != nil {
			current = *r.CatalogGraphID
		}
		if r.ProjectID == change.ProjectID && current == change.OldID {
			alias := change.NewID
			r.CatalogGraphID = &alias
		}
	}
	// Scheduled activities resolve the renamed graph at their actual start; deletion
	// leaves their identity intact so orchestration can report it is unavailable.
	if change.NewID != "" {
		for i := range d.GraphActivities {
			activity := &d.GraphActivities[i]
			if activity.ProjectID == change.ProjectID && activity.GraphID == change.OldID {
				activity.GraphID, activity.UpdatedAt = change.NewID, now()
				activity.Version++
			}
		}
	}
	d.GraphCatalogChanges = slices.DeleteFunc(d.GraphCatalogChanges, func(item GraphCatalogChange) bool { return item.ID == change.ID })
}

// Caller holds authoringMu, not app.mu. Persist intent before files are changed.
func (a *app) commitGraphAuthoring(p Project, writes map[string][]byte, oldID, newGraphID string) error {
	if oldID == newGraphID {
		return commitAuthoring(p, writes)
	}
	change := GraphCatalogChange{ID: newID(), ProjectID: p.ID, OldID: oldID, NewID: newGraphID, Writes: writes}
	a.mu.Lock()
	err := a.commitLocked(func(d *diskState) { d.GraphCatalogChanges = append(d.GraphCatalogChanges, change) })
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if err := commitAuthoring(p, writes); err != nil {
		// The durable intent owns these paths until restart recovery. Prevent a
		// later mutation from racing the pending roll-forward operation.
		a.mu.Lock()
		a.storageErr = errors.New("Graph catalog persistence failed; restart to recover the pending change.")
		for _, turn := range a.runs {
			turn.cancel()
		}
		for _, run := range a.graphRuns {
			run.cancel()
		}
		a.mu.Unlock()
		return err
	}
	a.mu.Lock()
	err = a.commitLocked(func(d *diskState) { applyGraphCatalogChange(d, change) })
	a.mu.Unlock()
	return err
}

func recoverGraphCatalogChanges(dir string, d *diskState) error {
	for len(d.GraphCatalogChanges) > 0 {
		change := d.GraphCatalogChanges[0]
		project := d.project(change.ProjectID)
		if project == nil {
			return errors.New("Catalog recovery project is missing.")
		}
		if err := commitAuthoring(*project, change.Writes); err != nil {
			return fmt.Errorf("Cannot recover graph catalog change: %w", err)
		}
		applyGraphCatalogChange(d, change)
		d.GraphRevision++
		d.GraphViewRevision = d.GraphRevision
		if err := saveState(dir, d); err != nil {
			return err
		}
	}
	return nil
}
