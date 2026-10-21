package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
)

type MCPSelection struct {
	ConnectionID string   `json:"connectionId" toml:"connection_id"`
	Tools        []string `json:"tools" toml:"tools"`
}

type CapabilitySelection struct {
	Skills         []string       `json:"skills"`
	MCPConnections []MCPSelection `json:"mcpConnections"`
}

type CapabilitySnapshot struct {
	Version     int                     `json:"version"`
	Revision    string                  `json:"revision"`
	Selection   CapabilitySelection     `json:"selection"`
	Skills      []SkillSnapshot         `json:"skills"`
	Connections []MCPConnectionSnapshot `json:"connections"`
	Diagnostics []string                `json:"diagnostics"`
	Adapter     string                  `json:"adapter"`
}

type ConversationCapabilities struct {
	Preparing *CapabilitySnapshot `json:"preparing,omitempty"`
	Revision  uint64              `json:"revision"`
	Desired   CapabilitySelection `json:"desired"`
	Applied   *CapabilitySnapshot `json:"applied,omitempty"`
}

func copySelection(selection CapabilitySelection) CapabilitySelection {
	data, _ := json.Marshal(selection)
	var result CapabilitySelection
	_ = json.Unmarshal(data, &result)
	if result.Skills == nil {
		result.Skills = []string{}
	}
	if result.MCPConnections == nil {
		result.MCPConnections = []MCPSelection{}
	}
	return result
}

func inheritedCapabilities(s *Session) ConversationCapabilities {
	selection := s.Capabilities.Desired
	if s.Capabilities.Applied != nil {
		selection = s.Capabilities.Applied.Selection
	}
	return ConversationCapabilities{Desired: copySelection(selection)}
}

func selectionEmpty(s CapabilitySelection) bool {
	return len(s.Skills) == 0 && len(s.MCPConnections) == 0
}

func normalizeSelection(s *CapabilitySelection) error {
	if len(s.Skills) > 100 || len(s.MCPConnections) > 32 {
		return errors.New("Select at most 100 skills and 32 MCP connections.")
	}
	seen := map[string]bool{}
	for _, id := range s.Skills {
		if !validSkillID(id) || seen[id] {
			return errors.New("Invalid or duplicate skill ID.")
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for i := range s.MCPConnections {
		c := &s.MCPConnections[i]
		if !validLibraryConnectionID(c.ConnectionID) || seen[c.ConnectionID] || len(c.Tools) > 512 {
			return errors.New("Invalid or duplicate MCP connection selection.")
		}
		seen[c.ConnectionID] = true
		tools := map[string]bool{}
		for _, name := range c.Tools {
			if name == "" || len(name) > 200 || tools[name] {
				return errors.New("Invalid or duplicate MCP tool selection.")
			}
			tools[name] = true
		}
		sort.Strings(c.Tools)
		if c.Tools == nil {
			c.Tools = []string{}
		}
	}
	sort.Strings(s.Skills)
	sort.Slice(s.MCPConnections, func(i, j int) bool { return s.MCPConnections[i].ConnectionID < s.MCPConnections[j].ConnectionID })
	*s = copySelection(*s)
	return nil
}

func managedLibraryHarness(harness string) bool {
	return harness == "pi" || harness == "opencode" || harness == "codex"
}

func libraryAdapterID(harness string) string { return harness + "-library-v1" }

func (a *app) captureCapabilities(ctx context.Context, selection CapabilitySelection, harness string) (*CapabilitySnapshot, error) {
	selection = copySelection(selection)
	if err := normalizeSelection(&selection); err != nil {
		return nil, err
	}
	if !managedLibraryHarness(harness) {
		if !selectionEmpty(selection) {
			return nil, fmt.Errorf("Managed library capabilities are unavailable for %s; exclusivity has not been validated. Selection remains saved.", harness)
		}
		return nil, nil // Explicit legacy execution, never advertised as an empty effective catalog.
	}
	if b, installed := a.binaries["pi"]; installed {
		if harness == "pi" {
			if err := validatePiLibraryBinary(b); err != nil {
				return nil, err
			}
		}
	}
	if harness == "opencode" {
		if b, installed := a.binaries[harness]; installed {
			if err := validateOpenCodeLibraryBinary(b); err != nil {
				return nil, err
			}
		}
	}
	if harness == "codex" {
		if b, installed := a.binaries[harness]; installed {
			if err := validateCodexLibraryBinary(b); err != nil {
				return nil, err
			}
		}
	}
	s := &CapabilitySnapshot{Version: 1, Selection: selection, Adapter: libraryAdapterID(harness), Skills: []SkillSnapshot{}, Connections: []MCPConnectionSnapshot{}, Diagnostics: []string{}}
	root, entries, err := scanSkillLibrary()
	if err != nil {
		return nil, err
	}
	for _, id := range selection.Skills {
		index := slices.IndexFunc(entries, func(e SkillLibraryEntry) bool { return e.ID == id })
		if index < 0 || !entries[index].Valid {
			return nil, fmt.Errorf("Selected skill %s is missing or invalid; update the library selection.", id)
		}
		p, err := stableSkillPackage(root, id, false)
		if err != nil {
			return nil, err
		}
		captured, err := a.materializeSkill(p, false)
		if err != nil {
			return nil, err
		}
		s.Skills = append(s.Skills, captured)
	}
	for _, selected := range selection.MCPConnections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a.mu.Lock()
		connection, exists := a.state.MCPConnections[selected.ConnectionID]
		a.mu.Unlock()
		if !exists {
			return nil, fmt.Errorf("Selected MCP connection %s is missing.", selected.ConnectionID)
		}
		s.Connections = append(s.Connections, snapshotMCPConnection(connection, selected))
	}
	sealCapabilitySnapshot(s)
	return s, nil
}

func sealCapabilitySnapshot(s *CapabilitySnapshot) {
	s.Revision = ""
	data, _ := json.Marshal(s)
	s.Revision = revision(data)
}

func (p *adapter) prepareCapabilities(s Session) error {
	p.app.libraryMu.Lock()
	locked := true
	defer func() {
		if locked {
			p.app.libraryMu.Unlock()
		}
	}()
	p.app.mu.Lock()
	recovering := len(p.app.state.LibraryDeletions) > 0
	p.app.mu.Unlock()
	if recovering {
		return errors.New("Library deletion requires recovery before starting another turn.")
	}
	var snapshot *CapabilitySnapshot
	var err error
	if s.Role == "graph_node" {
		p.app.mu.Lock()
		run := p.app.state.graphRun(s.GraphRunID)
		if run != nil {
			snapshot = run.Snapshot.Capabilities[s.GraphNodeID]
		}
		p.app.mu.Unlock()
		if snapshot == nil && !selectionEmpty(s.Capabilities.Desired) {
			return errors.New("Graph has no captured capability snapshot.")
		}
	} else {
		// Resolve outside the state lock, then reject an interleaved selection edit.
		for attempt := 0; attempt < 2; attempt++ {
			p.app.mu.Lock()
			current := p.app.state.session(p.id)
			if current == nil {
				p.app.mu.Unlock()
				return errors.New("Conversation no longer exists.")
			}
			rev, desired := current.Capabilities.Revision, copySelection(current.Capabilities.Desired)
			p.app.mu.Unlock()
			snapshot, err = p.app.captureCapabilities(p.turn.ctx, desired, s.Harness)
			if err != nil {
				return err
			}
			p.app.mu.Lock()
			stable := p.app.state.session(p.id).Capabilities.Revision == rev
			p.app.mu.Unlock()
			if stable {
				break
			}
			if attempt == 1 {
				return errors.New("Capability selection changed during preparation; retry.")
			}
		}
	}
	if snapshot != nil {
		// Clone before runtime availability diagnostics: graph snapshots never mutate.
		data, _ := json.Marshal(snapshot)
		snapshot = &CapabilitySnapshot{}
		_ = json.Unmarshal(data, snapshot)
		for _, skill := range snapshot.Skills {
			if err := p.app.validateSkillSnapshot(skill); err != nil {
				return err
			}
		}
	}
	p.internalSkills, err = p.app.captureInternalSkills(s.Role)
	if err != nil {
		return err
	}
	p.internalSkillPrompt, err = p.app.internalSkillGuidance(s.Role, p.internalSkills)
	if err != nil {
		return err
	}
	if snapshot != nil {
		snapshot.Skills = append(snapshot.Skills, p.internalSkills...)
		sealCapabilitySnapshot(snapshot)
	}
	p.capabilities = snapshot
	if snapshot != nil {
		encoded, _ := json.Marshal(snapshot)
		preparing := &CapabilitySnapshot{}
		_ = json.Unmarshal(encoded, preparing)
		p.app.mu.Lock()
		err = p.app.commitLocked(func(d *diskState) { d.session(p.id).Capabilities.Preparing = preparing })
		p.app.mu.Unlock()
		if err != nil {
			return err
		}
	}
	p.app.libraryMu.Unlock()
	locked = false
	if err := p.prepareMCP(); err != nil {
		return err
	}
	return nil
}

// Applied only after the adapter has confirmed its effective inventory, before
// the first native prompt. Edits made during startup remain desired/pending.
func (p *adapter) markCapabilitiesApplied() error {
	if p.capabilities == nil {
		return nil
	}
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	return p.app.commitLocked(func(d *diskState) {
		s := d.session(p.id)
		s.Capabilities.Applied = p.capabilities
		s.Capabilities.Preparing = nil
		s.UpdatedAt = now()
	})
}

func (a *app) conversationCapabilities(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	s := a.state.session(r.PathValue("id"))
	if s == nil || s.Role == sessionRoleSubagent || s.Role == "graph_node" {
		a.mu.Unlock()
		fail(w, 404, "Conversation not found.")
		return
	}
	if r.Method == http.MethodGet {
		result := s.Capabilities
		a.mu.Unlock()
		respond(w, 200, result)
		return
	}
	a.mu.Unlock()
	var body struct {
		Revision  uint64              `json:"revision"`
		Selection CapabilitySelection `json:"selection"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := normalizeSelection(&body.Selection); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	s = a.state.session(r.PathValue("id"))
	if len(a.state.LibraryDeletions) > 0 {
		fail(w, 409, "A library deletion needs recovery before editing selections.")
		return
	}
	if s.Capabilities.Revision != body.Revision {
		fail(w, 409, "Selection changed. Reload before saving.")
		return
	}
	err := a.commitLocked(func(d *diskState) {
		c := &d.session(s.ID).Capabilities
		c.Desired = body.Selection
		c.Revision++
		d.session(s.ID).UpdatedAt = now()
	})
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, a.state.session(s.ID).Capabilities)
}
