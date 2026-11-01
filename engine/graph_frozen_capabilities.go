package main

import (
	"encoding/json"
	"errors"
	"reflect"
)

// Compose already-captured assets only. Caller owns actual parent/activation,
// explicit human inheritance intent and target model/platform validation. This
// helper cannot discover sources, read files, connect MCPs or grant role tools.
func composeFrozenGraphAgent(snapshot GraphSnapshot, agent, sourceUID, parentNodeID, targetHarness string, overrides AgentConfigOverrides, inheritParent bool) (AgentExecutionCapture, error) {
	entry, err := lookupFrozenGraphAgent(snapshot, agent, sourceUID)
	if err != nil {
		return AgentExecutionCapture{}, err
	}
	parents := []*CapabilitySnapshot{entry.Execution.ExternalCapabilities}
	inherited := CapabilitySelection{}
	if inheritParent {
		parent, ok := snapshot.Capabilities[parentNodeID]
		if !ok || parent == nil {
			return AgentExecutionCapture{}, errors.New("Parent node has no captured external selection in this run.")
		}
		if _, ok := snapshot.EffectiveAgents[parentNodeID]; !ok {
			return AgentExecutionCapture{}, errors.New("Parent capture has no owning node in this run.")
		}
		if err := validateFrozenExternalCapture(ResolvedAgentConfig{Harness: snapshot.EffectiveAgents[parentNodeID].Harness, Selection: parent.Selection}, parent); err != nil {
			return AgentExecutionCapture{}, err
		}
		inherited = copySelection(parent.Selection)
		parents = append(parents, parent)
	}
	config, err := resolveAgentConfig(entry.Execution.Config, targetHarness, overrides, inherited)
	if err != nil {
		return AgentExecutionCapture{}, err
	}
	if !managedLibraryHarness(config.Harness) {
		return AgentExecutionCapture{}, errors.New("Frozen specialist target harness has no managed Library adapter.")
	}
	available := CapabilitySelection{}
	skills := map[string]SkillSnapshot{}
	connections := map[string]MCPConnectionSnapshot{}
	for _, captured := range parents {
		available = unionAgentSelection(available, captured.Selection)
		for _, skill := range captured.Skills {
			if skill.Internal {
				return AgentExecutionCapture{}, errors.New("Internal role skills cannot be inherited by a graph child.")
			}
			if old, exists := skills[skill.ID]; exists && !reflect.DeepEqual(old, skill) {
				return AgentExecutionCapture{}, errors.New("Captured skill contracts conflict within this run.")
			}
			skills[skill.ID] = skill
		}
		for _, connection := range captured.Connections {
			if old, exists := connections[connection.ID]; exists {
				old.Selection = MCPSelection{}
				candidate := connection
				candidate.Selection = MCPSelection{}
				if !reflect.DeepEqual(old, candidate) {
					return AgentExecutionCapture{}, errors.New("Captured MCP contracts conflict within this run.")
				}
			}
			connections[connection.ID] = connection
		}
	}
	result := &CapabilitySnapshot{Version: 1, Adapter: libraryAdapterID(config.Harness), Selection: copySelection(config.Selection), Skills: []SkillSnapshot{}, Connections: []MCPConnectionSnapshot{}, Diagnostics: []string{}}
	for _, id := range config.Selection.Skills {
		skill, exists := skills[id]
		if !exists || !selectionReferences(available, "skills", id) {
			return AgentExecutionCapture{}, agentProblem(409, "capability_not_captured", "skills", "Requested skill was not selected/captured in the specialist or inherited parent snapshot.")
		}
		result.Skills = append(result.Skills, skill)
	}
	for _, selected := range config.Selection.MCPConnections {
		connection, exists := connections[selected.ConnectionID]
		if !exists {
			return AgentExecutionCapture{}, agentProblem(409, "capability_not_captured", "mcpConnections", "Requested MCP connection was not captured for this specialist/parent.")
		}
		allowed := map[string]bool{}
		found := false
		for _, entry := range available.MCPConnections {
			if entry.ConnectionID == selected.ConnectionID {
				found = true
				for _, tool := range entry.Tools {
					allowed[tool] = true
				}
			}
		}
		if !found {
			return AgentExecutionCapture{}, errors.New("MCP connection has no captured positive selection.")
		}
		for _, tool := range selected.Tools {
			if !allowed[tool] {
				return AgentExecutionCapture{}, agentProblem(409, "capability_not_captured", "mcpConnections", "Requested MCP tool was outside the captured positive selection.")
			}
		}
		connection.Selection = selected
		result.Connections = append(result.Connections, connection)
	}
	sealCapabilitySnapshot(result)
	source := *entry.Execution.Source
	capture := AgentExecutionCapture{Config: config, Source: &source, ExternalCapabilities: result}
	raw, _ := json.Marshal(capture)
	capture.Revision = revision(raw)
	return capture, nil
}
