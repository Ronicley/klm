package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type AgentSourceRef struct {
	SourceUID   string `json:"sourceUid"`
	ID          string `json:"id"`
	Scope       string `json:"scope"`
	ProjectID   string `json:"projectId,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
}

// Identity metadata belongs to the authoring document, not specialist settings.
type agentDocument struct {
	AgentDefinition
	SourceUID string `toml:"source_uid,omitempty"`
}

type AgentConfigOverrides struct {
	Model          *string         `json:"model,omitempty"`
	Effort         *string         `json:"effort,omitempty"`
	Prompt         *string         `json:"prompt,omitempty"`
	Skills         *[]string       `json:"skills,omitempty"`
	MCPConnections *[]MCPSelection `json:"mcpConnections,omitempty"`
}

// Present distinguishes omission (unchanged), null (follow source), and a value
// including an empty string/list (explicit override).
type agentOverrideValue[T any] struct {
	Present bool
	Value   *T
}

func (v *agentOverrideValue[T]) UnmarshalJSON(raw []byte) error {
	v.Present = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		v.Value = nil
		return nil
	}
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	v.Value = &value
	return nil
}

type AgentOverridePatch struct {
	Model          agentOverrideValue[string]         `json:"model"`
	Effort         agentOverrideValue[string]         `json:"effort"`
	Prompt         agentOverrideValue[string]         `json:"prompt"`
	Skills         agentOverrideValue[[]string]       `json:"skills"`
	MCPConnections agentOverrideValue[[]MCPSelection] `json:"mcpConnections"`
}

func applyAgentOverridePatch(current AgentConfigOverrides, patch AgentOverridePatch) (AgentConfigOverrides, error) {
	if !patch.Model.Present && !patch.Effort.Present && !patch.Prompt.Present && !patch.Skills.Present && !patch.MCPConnections.Present {
		return current, errors.New("Supply at least one override or reset.")
	}
	if patch.Model.Present {
		current.Model = patch.Model.Value
	}
	if patch.Effort.Present {
		current.Effort = patch.Effort.Value
	}
	if patch.Prompt.Present {
		current.Prompt = patch.Prompt.Value
	}
	if patch.Skills.Present {
		current.Skills = patch.Skills.Value
	}
	if patch.MCPConnections.Present {
		current.MCPConnections = patch.MCPConnections.Value
	}
	return current, nil
}

type ResolvedAgentConfig struct {
	Harness   string              `json:"harness"`
	Model     string              `json:"model"`
	Effort    string              `json:"effort"`
	Prompt    string              `json:"prompt"`
	Selection CapabilitySelection `json:"selection"`
}

type AgentCapture struct {
	Name     string              `json:"name,omitempty"`
	Source   AgentSourceRef      `json:"source"`
	Revision string              `json:"revision"`
	Config   ResolvedAgentConfig `json:"config"`
}

type SessionAgentBinding struct {
	Source                AgentSourceRef         `json:"source"`
	Revision              string                 `json:"revision"`
	LastCaptured          AgentCapture           `json:"lastCaptured"`
	Overrides             AgentConfigOverrides   `json:"overrides"`
	InheritedCapabilities CapabilitySelection    `json:"inheritedCapabilities"`
	Desired               ResolvedAgentConfig    `json:"desired"`
	Applied               *AgentExecutionCapture `json:"applied,omitempty"`
	SourceStatus          string                 `json:"sourceStatus"`
	Blocked               *AgentConfigProblem    `json:"blocked,omitempty"`
}

type AgentExecutionCapture struct {
	Config               ResolvedAgentConfig `json:"config"`
	Source               *AgentCapture       `json:"source,omitempty"`
	Revision             string              `json:"revision"`
	ExternalCapabilities *CapabilitySnapshot `json:"externalCapabilities,omitempty"`
}

type AgentResolution struct {
	Config       ResolvedAgentConfig `json:"config"`
	Capture      AgentCapture        `json:"capture"`
	SourceStatus string              `json:"sourceStatus"`
	Blocked      *AgentConfigProblem `json:"blocked,omitempty"`
	Token        string              `json:"token"`
}

type AgentTurnPreparation struct {
	Resolution           AgentResolution
	BindingRevision      string
	SelectionRevision    string
	ExternalCapabilities *CapabilitySnapshot
}

type AgentExecutionEligibility struct {
	Allowed bool     `json:"allowed"`
	Code    string   `json:"code,omitempty"`
	Message string   `json:"message,omitempty"`
	Fields  []string `json:"fields,omitempty"`
	Token   string   `json:"token"`
}

type AgentConfigSummary struct {
	Source           AgentSourceRef            `json:"source"`
	Name             string                    `json:"name"`
	SourceStatus     string                    `json:"sourceStatus"`
	CapturedRevision string                    `json:"capturedRevision"`
	PendingRevision  string                    `json:"pendingRevision,omitempty"`
	OverriddenFields []string                  `json:"overriddenFields"`
	Pending          bool                      `json:"pending"`
	Execution        AgentExecutionEligibility `json:"execution"`
}

type GlobalAgentRecord struct {
	ID         string          `json:"id"` // internal source lifetime; public ID is Slug
	Slug       string          `json:"slug"`
	Definition AgentDefinition `json:"definition"`
	Revision   string          `json:"revision"`
}

type AgentSourceRecord struct {
	Adopted  bool           `json:"adopted,omitempty"`
	ID       string         `json:"id"`
	Source   AgentSourceRef `json:"source"`
	Path     string         `json:"path,omitempty"`
	Revision string         `json:"revision"`
	State    string         `json:"state"`
}

type AgentReference struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
}

type AgentDiagnostic struct {
	Code      string           `json:"code"`
	Message   string           `json:"message"`
	Source    *AgentSourceRef  `json:"source,omitempty"`
	Conflicts []AgentSourceRef `json:"conflicts,omitempty"`
}

type CatalogAgentRecord struct {
	AgentRecord
	Source               AgentSourceRef   `json:"source"`
	Status               string           `json:"status"`
	Available            bool             `json:"available"`
	UnavailableReason    string           `json:"unavailableReason,omitempty"`
	References           []AgentReference `json:"references"`
	ReferencesHasMore    bool             `json:"referencesHasMore"`
	ReferencesNextOffset int              `json:"referencesNextOffset,omitempty"`
}

type AgentCatalog struct {
	Agents      []CatalogAgentRecord `json:"agents"`
	Diagnostics []AgentDiagnostic    `json:"diagnostics"`
	Complete    bool                 `json:"complete"`
	Revision    string               `json:"revision"`
}

type AgentConfigProblem struct {
	Code               string           `json:"code"`
	Field              string           `json:"field,omitempty"`
	Message            string           `json:"error"`
	Hint               string           `json:"hint,omitempty"`
	Conflicts          []AgentSourceRef `json:"conflicts,omitempty"`
	CurrentRevision    string           `json:"currentRevision,omitempty"`
	BlockingReferences []AgentReference `json:"blockingReferences,omitempty"`
	status             int
}

func (e *AgentConfigProblem) Error() string { return e.Message }
func agentProblem(status int, code, field, message string) *AgentConfigProblem {
	return &AgentConfigProblem{status: status, Code: code, Field: field, Message: message}
}

func agentConfig(d AgentDefinition) ResolvedAgentConfig {
	return ResolvedAgentConfig{Harness: d.DefaultHarness, Model: d.Model, Effort: d.Effort, Prompt: d.Prompt,
		Selection: copySelection(CapabilitySelection{Skills: d.Skills, MCPConnections: d.MCPConnections})}
}

// Compose positive lists without applying intermediate limits: a later explicit
// family override may replace this union. Validate the final effective selection.
func unionAgentSelection(left, right CapabilitySelection) CapabilitySelection {
	result := copySelection(left)
	skills := map[string]bool{}
	for _, id := range result.Skills {
		skills[id] = true
	}
	for _, id := range right.Skills {
		if !skills[id] {
			result.Skills = append(result.Skills, id)
			skills[id] = true
		}
	}
	connections := map[string]int{}
	for i, c := range result.MCPConnections {
		connections[c.ConnectionID] = i
	}
	for _, c := range right.MCPConnections {
		i, exists := connections[c.ConnectionID]
		if !exists {
			connections[c.ConnectionID] = len(result.MCPConnections)
			result.MCPConnections = append(result.MCPConnections, MCPSelection{ConnectionID: c.ConnectionID, Tools: append([]string{}, c.Tools...)})
			continue
		}
		seen := map[string]bool{}
		for _, tool := range result.MCPConnections[i].Tools {
			seen[tool] = true
		}
		for _, tool := range c.Tools {
			if !seen[tool] {
				result.MCPConnections[i].Tools = append(result.MCPConnections[i].Tools, tool)
				seen[tool] = true
			}
		}
	}
	return result
}

// Pure composition only. Runtime/catalog validation must use the fixed harness
// and actual execution directory; this function grants no role or execution rights.
func resolveAgentConfig(base ResolvedAgentConfig, fixedHarness string, overrides AgentConfigOverrides, inherited CapabilitySelection) (ResolvedAgentConfig, error) {
	result := base
	if fixedHarness != "" {
		result.Harness = fixedHarness
	}
	selection := unionAgentSelection(base.Selection, inherited)
	if overrides.Model != nil {
		result.Model = *overrides.Model
	}
	if overrides.Effort != nil {
		result.Effort = *overrides.Effort
	}
	if overrides.Prompt != nil {
		result.Prompt = *overrides.Prompt
	}
	if len(result.Prompt) > 128<<10 {
		return result, errors.New("Specialist prompt must be at most 128 KiB.")
	}
	if overrides.Skills != nil {
		selection.Skills = append([]string{}, (*overrides.Skills)...)
	}
	if overrides.MCPConnections != nil {
		selection.MCPConnections = copySelection(CapabilitySelection{MCPConnections: *overrides.MCPConnections}).MCPConnections
	}
	if err := normalizeSelection(&selection); err != nil {
		return result, err
	}
	result.Selection = selection
	return result, nil
}

func validateAgentDefinition(d *AgentDefinition) error {
	d.Name, d.Description = strings.TrimSpace(d.Name), strings.TrimSpace(d.Description)
	if _, ok := cleanLabel(d.Name, 80); !ok || !validAuthoringID(authoringSlug(d.Name)) || len(d.Description) > 960 || strings.TrimSpace(d.Prompt) == "" || len(d.Prompt) > 128<<10 {
		return errors.New("Enter a valid name and prompt (at most 128 KiB).")
	}
	if d.DefaultHarness != "pi" && d.DefaultHarness != "opencode" && d.DefaultHarness != "codex" {
		return errors.New("Select a supported harness.")
	}
	if _, ok := cleanLabel(d.Model, 200); !ok || strings.HasPrefix(d.Model, "-") {
		return errors.New("Select an exact model identifier.")
	}
	selection := CapabilitySelection{Skills: d.Skills, MCPConnections: d.MCPConnections}
	if err := normalizeSelection(&selection); err != nil {
		return err
	}
	d.Skills, d.MCPConnections = selection.Skills, selection.MCPConnections
	return nil
}

func validAgentSourceUID(uid string) bool {
	if len(uid) < 16 || len(uid) > 128 {
		return false
	}
	for _, r := range uid {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func (st *sqliteSessionTx) agentBinding(id string) (*SessionAgentBinding, error) {
	var binding *SessionAgentBinding
	err := decodeSQLiteRecord(st.ctx, st.tx, "agent_bindings", sqliteOwnedKey(id, ""), 8<<20, &binding)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return binding, err
}

// Validation is deliberately not a binding creation path. CP1a exposes no route
// which creates a session/child with a partially integrated agent configuration.
func validateAgentBinding(binding *SessionAgentBinding) error {
	if binding == nil {
		return nil
	}
	if !validAgentSourceUID(binding.Source.SourceUID) || binding.Source.SourceUID != binding.LastCaptured.Source.SourceUID || !validAuthoringID(binding.Source.ID) {
		return fmt.Errorf("Invalid saved agent binding identity.")
	}
	return nil
}
