package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Compiled structures are rebuilt from the immutable snapshot, never from layout.
type ChoiceIdentity struct {
	Origin string `json:"origin"`
	ID     string `json:"id"`
}

type EffectiveGraphAgent struct {
	AgentID string `json:"agentId"`
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort,omitempty"`
	Prompt  string `json:"prompt"`
}

type GraphConnection struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Sources  []string `json:"sources"`
	ChoiceID string   `json:"choiceId,omitempty"`
	BranchID string   `json:"branchId,omitempty"`
	To       string   `json:"to"`
	Session  string   `json:"session,omitempty"`
}

type CompiledGraph struct {
	Definition  GraphDefinition
	Agents      map[string]EffectiveGraphAgent // keyed by node ID, overrides already resolved
	Connections map[string]GraphConnection
	Incoming    map[string][]string // stable, sorted connection IDs; Join waits on all
	Outgoing    map[string][]string
	Outputs     map[string]CompiledOutput // choice:<id>, terminal:<node>, fork:<node>:<branch>
}

type templatePart struct{ text, reference string }
type CompiledOutput map[string][]templatePart
type GraphOutputContext struct {
	Task          string
	Choice        map[string]string
	Payload       map[string]string
	CommandResult *string // nil distinguishes absent command result from empty output
	ChoiceInput   map[string]ChoiceField
}

func compileOutput(output map[string]string, kind string, input map[string]ChoiceField) (CompiledOutput, error) {
	if kind != "choice" && kind != "terminal" && kind != "fork" {
		return nil, errors.New("Unknown output context.")
	}
	result := CompiledOutput{}
	for _, field := range sortedGraphKeys(output) {
		value := output[field]
		if !fieldNamePattern.MatchString(field) {
			return nil, fmt.Errorf("Invalid output field %q.", field)
		}
		parts := []templatePart{}
		for len(value) > 0 {
			start := strings.Index(value, "{{")
			if start < 0 {
				if strings.Contains(value, "}}") {
					return nil, fmt.Errorf("Malformed template in %s.", field)
				}
				parts = append(parts, templatePart{text: value})
				break
			}
			if strings.Contains(value[:start], "}}") {
				return nil, fmt.Errorf("Malformed template in %s.", field)
			}
			parts = append(parts, templatePart{text: value[:start]})
			end := strings.Index(value[start+2:], "}}")
			if end < 0 {
				return nil, fmt.Errorf("Unclosed template in %s.", field)
			}
			ref := strings.TrimSpace(value[start+2 : start+2+end])
			valid := ref == "run.input.task"
			if kind == "choice" && strings.HasPrefix(ref, "choice.") {
				f, ok := input[strings.TrimPrefix(ref, "choice.")]
				valid = ok && f.Type == "string"
			}
			if (kind == "terminal" || kind == "fork") && strings.HasPrefix(ref, "payload.") {
				valid = fieldNamePattern.MatchString(strings.TrimPrefix(ref, "payload."))
			}
			if kind == "terminal" && ref == "command.result" {
				valid = true
			}
			if !valid {
				return nil, fmt.Errorf("Unknown %s output reference %q.", kind, ref)
			}
			parts = append(parts, templatePart{reference: ref})
			value = value[start+2+end+2:]
		}
		result[field] = parts
	}
	return result, nil
}

func resolveGraphOutput(output CompiledOutput, ctx GraphOutputContext) (map[string]string, error) {
	result := map[string]string{}
	for _, field := range sortedGraphKeys(output) {
		var value strings.Builder
		for _, part := range output[field] {
			if part.reference == "" {
				value.WriteString(part.text)
				continue
			}
			ref, text, found := part.reference, "", false
			switch {
			case ref == "run.input.task":
				text, found = ctx.Task, true
			case ref == "command.result":
				if ctx.CommandResult != nil {
					text, found = *ctx.CommandResult, true
				}
			case strings.HasPrefix(ref, "payload."):
				text, found = ctx.Payload[strings.TrimPrefix(ref, "payload.")]
			case strings.HasPrefix(ref, "choice."):
				name := strings.TrimPrefix(ref, "choice.")
				text, found = ctx.Choice[name]
				if f, declared := ctx.ChoiceInput[name]; !found && declared && !f.Required {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("Cannot resolve %s for output %s: local field is absent.", ref, field)
			}
			value.WriteString(text)
		}
		result[field] = value.String()
	}
	return result, nil
}

func sortedGraphKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// validateModel is mandatory for graphs with agents. It must use a real harness
// catalog (captureGraphSnapshot supplies it); compilation itself performs no I/O.
func compileGraph(g GraphDefinition, agents []AgentRecord, validateModel func(EffectiveGraphAgent) error) (*CompiledGraph, error) {
	if err := validateGraph(g, agents); err != nil {
		return nil, err
	}
	if !g.Enabled {
		return nil, errors.New("Graph is disabled.")
	}
	// Own all maps/pointers so subsequent caller mutations cannot alter execution.
	b, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	g = GraphDefinition{}
	if err = json.Unmarshal(b, &g); err != nil {
		return nil, err
	}
	c := &CompiledGraph{Definition: g, Agents: map[string]EffectiveGraphAgent{}, Connections: map[string]GraphConnection{}, Incoming: map[string][]string{}, Outgoing: map[string][]string{}, Outputs: map[string]CompiledOutput{}}
	agentByID := map[string]AgentRecord{}
	for _, a := range agents {
		if _, duplicate := agentByID[a.ID]; duplicate {
			return nil, fmt.Errorf("Conflicting graph agent identifier %s.", a.ID)
		}
		agentByID[a.ID] = a
	}
	choiceSources := map[string][]string{}
	for _, id := range sortedGraphKeys(g.Nodes) {
		n := g.Nodes[id]
		if n.Type == "agent" || n.Type == "join" {
			a := agentByID[n.Agent]
			if strings.TrimSpace(a.Prompt) == "" {
				return nil, fmt.Errorf("Agent %s has no prompt.", n.Agent)
			}
			e := EffectiveGraphAgent{AgentID: n.Agent, Harness: a.DefaultHarness, Model: a.Model, Effort: a.Effort, Prompt: a.Prompt}
			if o := n.Overrides; o != nil {
				if o.Harness != "" {
					e.Harness = o.Harness
				}
				if o.Model != "" {
					e.Model = o.Model
				}
				if o.Effort != nil {
					e.Effort = *o.Effort
				}
			}
			if (e.Harness != "pi" && e.Harness != "opencode" && e.Harness != "codex") || e.Model == "" || validateModel == nil {
				return nil, fmt.Errorf("Node %s requires a valid harness model catalog.", id)
			}
			if err := validateModel(e); err != nil {
				return nil, fmt.Errorf("Node %s: %w", id, err)
			}
			c.Agents[id] = e
			for _, choice := range n.Choices {
				choiceSources[choice] = append(choiceSources[choice], id)
			}
		}
		if n.Type == "terminal" {
			if n.To == "" {
				return nil, fmt.Errorf("Terminal %s needs a destination.", id)
			}
			key := "terminal:" + id
			c.Outputs[key], err = compileOutput(n.Output, "terminal", nil)
			if err != nil {
				return nil, err
			}
			c.addConnection(GraphConnection{ID: key, Kind: "terminal", Sources: []string{id}, To: n.To})
		}
		if n.Type == "fork" {
			if len(n.Branches) < 2 {
				return nil, fmt.Errorf("Fork %s needs at least two branches.", id)
			}
			for _, branchID := range sortedGraphKeys(n.Branches) {
				branch := n.Branches[branchID]
				if branch.To == "" {
					return nil, fmt.Errorf("Fork %s branch %s needs a destination.", id, branchID)
				}
				key := "fork:" + id + ":" + branchID
				c.Outputs[key], err = compileOutput(branch.Output, "fork", nil)
				if err != nil {
					return nil, err
				}
				c.addConnection(GraphConnection{ID: key, Kind: "fork", Sources: []string{id}, BranchID: branchID, To: branch.To})
			}
		}
	}
	for _, id := range sortedGraphKeys(g.Choices) {
		choice := g.Choices[id]
		key := "choice:" + id
		if !choice.Terminal && choice.To == "" {
			return nil, fmt.Errorf("Choice %s needs a destination or terminal outcome.", id)
		}
		c.Outputs[key], err = compileOutput(choice.Output, "choice", choice.Input)
		if err != nil {
			return nil, err
		}
		if !choice.Terminal && len(choiceSources[id]) > 0 {
			policy := choice.Session
			if g.Nodes[choice.To].Type == "agent" || g.Nodes[choice.To].Type == "join" {
				if policy == "" {
					policy = "new"
				}
			}
			c.addConnection(GraphConnection{ID: key, Kind: "choice", Sources: choiceSources[id], ChoiceID: id, To: choice.To, Session: policy})
		}
	}
	for id := range c.Incoming {
		sort.Strings(c.Incoming[id])
	}
	for id := range c.Outgoing {
		sort.Strings(c.Outgoing[id])
	}
	if err := c.validateTopology(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *CompiledGraph) addConnection(edge GraphConnection) {
	c.Connections[edge.ID] = edge
	c.Incoming[edge.To] = append(c.Incoming[edge.To], edge.ID)
	for _, source := range edge.Sources {
		c.Outgoing[source] = append(c.Outgoing[source], edge.ID)
	}
}

// Scan until the next Join, preserving physical connection identity. A visited
// node breaks cycles, not compilation: we do not require a DAG or prove termination.
func (c *CompiledGraph) joinFrontier(edgeID string, parallel bool, seen map[string]bool, arrivals map[string]map[string]bool) error {
	e := c.Connections[edgeID]
	n := c.Definition.Nodes[e.To]
	if n.Type == "join" {
		if arrivals[e.To] == nil {
			arrivals[e.To] = map[string]bool{}
		}
		arrivals[e.To][edgeID] = true
		return nil
	}
	if seen[e.To] {
		return nil
	}
	seen[e.To] = true
	if n.Type == "fork" {
		if parallel {
			return fmt.Errorf("Nested Fork at %s is not supported.", e.To)
		}
	}
	for _, id := range n.Choices {
		if parallel && c.Definition.Choices[id].Terminal {
			return fmt.Errorf("Parallel path reaches terminal Choice %s before Join.", id)
		}
	}
	for _, next := range c.Outgoing[e.To] {
		if err := c.joinFrontier(next, parallel, seen, arrivals); err != nil {
			return err
		}
	}
	return nil
}

func (c *CompiledGraph) validateTopology() error {
	for _, id := range sortedGraphKeys(c.Definition.Nodes) {
		n := c.Definition.Nodes[id]
		if n.Type == "join" {
			if len(c.Incoming[id]) == 0 {
				return fmt.Errorf("Join %s has no incoming connections.", id)
			}
			policy := ""
			for _, edgeID := range c.Incoming[id] {
				e := c.Connections[edgeID]
				if e.Kind != "choice" {
					continue
				}
				if policy != "" && policy != e.Session {
					return fmt.Errorf("Join %s has conflicting session policies.", id)
				}
				policy = e.Session
			}
		}
		if n.Type == "agent" || n.Type == "join" {
			// Distinct mutually exclusive choices cannot populate distinct required
			// connections of the same next Join, including indirect Terminal paths.
			previous := map[string]map[string]bool{}
			for _, edgeID := range c.Outgoing[id] {
				frontier := map[string]map[string]bool{}
				if err := c.joinFrontier(edgeID, false, map[string]bool{}, frontier); err != nil {
					return err
				}
				for join, edges := range frontier {
					if old, exists := previous[join]; exists && !slices.Equal(sortedGraphKeys(old), sortedGraphKeys(edges)) {
						return fmt.Errorf("Mutually exclusive paths from %s supply distinct required inputs of Join %s.", id, join)
					}
					if previous[join] == nil {
						previous[join] = map[string]bool{}
					}
					for edge := range edges {
						previous[join][edge] = true
					}
				}
			}
		}
		if n.Type != "fork" {
			continue
		}
		joinID := ""
		used := map[string]bool{}
		for _, edgeID := range c.Outgoing[id] {
			frontier := map[string]map[string]bool{}
			if err := c.joinFrontier(edgeID, true, map[string]bool{}, frontier); err != nil {
				return err
			}
			if len(frontier) != 1 {
				return fmt.Errorf("Fork %s has a path without an unambiguous reconverging Join.", id)
			}
			for join, edges := range frontier {
				if joinID != "" && joinID != join {
					return fmt.Errorf("Fork %s has ambiguous reconvergence origins (%s, %s).", id, joinID, join)
				}
				joinID = join
				for edge := range edges {
					if used[edge] {
						return fmt.Errorf("Fork %s collapses independent arrivals onto %s.", id, edge)
					}
					used[edge] = true
				}
			}
		}
	}
	return nil
}

func (c *CompiledGraph) availableChoices(nodeID string) map[ChoiceIdentity]GraphChoice {
	result := map[ChoiceIdentity]GraphChoice{}
	n := c.Definition.Nodes[nodeID]
	if n.Type != "agent" && n.Type != "join" {
		return result
	}
	for _, id := range n.Choices {
		result[ChoiceIdentity{Origin: "graph", ID: id}] = c.Definition.Choices[id]
	}
	result[ChoiceIdentity{Origin: "engine", ID: "blocked"}] = GraphChoice{Name: "blocked", Input: map[string]ChoiceField{"reason": {Type: "string", Required: true}}, Output: map[string]string{"reason": "{{choice.reason}}"}, Terminal: true}
	return result
}

// raw stays permissive at transport boundaries; only this engine validator
// decides whether a submission is valid. Empty required strings are valid.
func (c *CompiledGraph) validateChoice(nodeID string, identity ChoiceIdentity, raw map[string]any) (GraphChoice, map[string]string, error) {
	choice, ok := c.availableChoices(nodeID)[identity]
	if !ok {
		return GraphChoice{}, nil, errors.New("Choice is not available to this node.")
	}
	payload := map[string]string{}
	for _, key := range sortedGraphKeys(raw) {
		if _, ok := choice.Input[key]; !ok {
			return choice, nil, fmt.Errorf("Undeclared Choice field %s.", key)
		}
		value, ok := raw[key].(string)
		if !ok {
			return choice, nil, fmt.Errorf("Choice field %s must be a string.", key)
		}
		payload[key] = value
	}
	for _, key := range sortedGraphKeys(choice.Input) {
		if _, ok := payload[key]; choice.Input[key].Required && !ok {
			return choice, nil, fmt.Errorf("Required Choice field %s is missing.", key)
		}
	}
	return choice, payload, nil
}
