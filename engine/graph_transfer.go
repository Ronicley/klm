package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// GraphTransfer is an interchange document, not an execution snapshot.
type GraphTransfer struct {
	Format     string                     `json:"format"`
	Version    int                        `json:"version"`
	Definition GraphDefinition            `json:"definition"`
	Layout     GraphLayout                `json:"layout"`
	Agents     map[string]AgentDefinition `json:"agents"`
}

func (b GraphTransfer) validate() error {
	if b.Format != "klm.graph" || b.Version != 1 {
		return errors.New("Expected klm.graph format, version 1.")
	}
	if b.Definition.Nodes == nil || b.Definition.Choices == nil || b.Layout.Positions == nil || b.Agents == nil {
		return errors.New("Definition nodes/choices, layout positions and agents must be objects.")
	}
	used := map[string]bool{}
	for _, n := range b.Definition.Nodes {
		if n.Type == "agent" || n.Type == "join" {
			used[n.Agent] = true
		}
	}
	agents := make([]AgentRecord, 0, len(b.Agents))
	for id, d := range b.Agents {
		if !validAuthoringID(id) || id != authoringSlug(d.Name) || !used[id] {
			return fmt.Errorf("Agent %q must have a name-derived identifier and be referenced by the graph.", id)
		}
		if err := validateAgentDefinition(d); err != nil {
			return fmt.Errorf("Agent %s: %w", id, err)
		}
		if d.DefaultHarness != "pi" && d.DefaultHarness != "opencode" && d.DefaultHarness != "codex" || strings.TrimSpace(d.Model) == "" {
			return fmt.Errorf("Agent %s needs a supported harness and model.", id)
		}
		agents = append(agents, AgentRecord{AgentDefinition: d, ID: id})
	}
	if err := validateGraph(b.Definition, agents); err != nil {
		return err
	}
	for id := range b.Layout.Positions {
		_, node := b.Definition.Nodes[id]
		_, choice := b.Definition.Choices[strings.TrimPrefix(id, "choice:")]
		if !node && !(strings.HasPrefix(id, "choice:") && choice) {
			return fmt.Errorf("Layout references unknown node %q.", id)
		}
	}
	return validateLayout(b.Layout)
}

func transferName(name string, taken map[string]bool) string {
	if !taken[authoringSlug(name)] {
		return name
	}
	// Leave room for the numeric suffix within the existing 80-character limit.
	base := []rune(strings.TrimSpace(name))
	if len(base) > 60 {
		base = base[:60]
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)", string(base), i)
		if !taken[authoringSlug(candidate)] {
			return candidate
		}
	}
}

// Preview is read-only. The resulting identities are checked again on Save.
func prepareGraphImport(b GraphTransfer, c AuthoringCatalog) GraphTransfer {
	graphNames, agentNames := map[string]bool{}, map[string]bool{}
	existing := map[string]AgentDefinition{}
	for _, g := range c.Graphs {
		graphNames[g.ID] = true
	}
	for _, a := range c.Agents {
		agentNames[a.ID] = true
		existing[a.ID] = a.AgentDefinition
	}
	ids := make([]string, 0, len(b.Agents))
	for id := range b.Agents {
		ids = append(ids, id)
		agentNames[id] = true // Do not consume another incoming agent's identifier.
	}
	sort.Strings(ids)
	remap := map[string]string{}
	agents := map[string]AgentDefinition{}
	for _, id := range ids {
		d := b.Agents[id]
		next := id
		if old, found := existing[id]; found && !reflect.DeepEqual(old, d) {
			d.Name = transferName(d.Name, agentNames)
			next = authoringSlug(d.Name)
		}
		agentNames[next] = true
		agents[next] = d
		remap[id] = next
	}
	for id, n := range b.Definition.Nodes {
		if n.Agent != "" {
			n.Agent = remap[n.Agent]
			b.Definition.Nodes[id] = n
		}
	}
	b.Agents = agents
	b.Definition.Name = transferName(b.Definition.Name, graphNames)
	return b
}

func (a *app) validateGraphModels(r *http.Request, p Project, g GraphDefinition, agents []AgentRecord) error {
	for id, n := range g.Nodes {
		if n.Overrides == nil {
			continue
		}
		for _, agent := range agents {
			if agent.ID != n.Agent {
				continue
			}
			h, m, e := agent.DefaultHarness, agent.Model, agent.Effort
			if n.Overrides.Harness != "" {
				h = n.Overrides.Harness
			}
			if n.Overrides.Model != "" {
				m = n.Overrides.Model
			}
			if n.Overrides.Effort != nil {
				e = *n.Overrides.Effort
			}
			if err := a.validateAgentModel(r, p, h, m, e); err != nil {
				return fmt.Errorf("Node %s: %w", id, err)
			}
		}
	}
	return nil
}

func (a *app) previewGraphImport(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authoringProject(w, r)
	if !ok {
		return
	}
	var b GraphTransfer
	if !decode(w, r, &b) {
		return
	}
	if err := b.validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.authoringMu.Lock()
	c, err := catalogForMutation(p)
	a.authoringMu.Unlock()
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	respond(w, 200, prepareGraphImport(b, c))
}

func (a *app) importGraph(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authoringProject(w, r)
	if !ok {
		return
	}
	var b GraphTransfer
	if !decode(w, r, &b) {
		return
	}
	if err := b.validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(55 * time.Second))
	agents := make([]AgentRecord, 0, len(b.Agents))
	for id, d := range b.Agents {
		if err := a.validateAgentModel(r, p, d.DefaultHarness, d.Model, d.Effort); err != nil {
			fail(w, 400, fmt.Sprintf("Agent %s: %s Choose an available agent in Import agents.", d.Name, err))
			return
		}
		agents = append(agents, AgentRecord{AgentDefinition: d, ID: id})
	}
	if err := a.validateGraphModels(r, p, b.Definition, agents); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	c, err := catalogForMutation(p)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	id := authoringSlug(b.Definition.Name)
	for _, g := range c.Graphs {
		if g.ID == id {
			fail(w, 409, "Graph name is already in use. Choose another name in Graph details.")
			return
		}
	}
	writes := map[string][]byte{}
	for agentID, d := range b.Agents {
		found := false
		for _, existing := range c.Agents {
			if existing.ID != agentID {
				continue
			}
			if !reflect.DeepEqual(existing.AgentDefinition, d) {
				fail(w, 409, "Agent "+d.Name+" changed or already exists. Review Import agents before saving.")
				return
			}
			found = true
		}
		if !found {
			data, err := toml.Marshal(d)
			if err != nil {
				fail(w, 400, err.Error())
				return
			}
			writes[agentFile(agentID)] = data
		}
	}
	graphData, err := yaml.Marshal(b.Definition)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	layoutData, err := json.Marshal(b.Layout)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writes[graphFile(id)], writes[layoutFile(id)] = graphData, layoutData
	for name := range writes {
		if _, err := readConfig(p, name); !errors.Is(err, os.ErrNotExist) {
			fail(w, 409, "An import destination already exists or is unavailable. Reload the catalog before importing: "+name)
			return
		}
	}
	if err := commitAuthoring(p, writes); err != nil {
		fail(w, 500, "Could not finish importing graph: "+err.Error())
		return
	}
	respond(w, 201, GraphRecord{ID: id, Revision: revision(graphData), Definition: b.Definition, Layout: b.Layout})
}
