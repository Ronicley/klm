package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

func (a *app) libraryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/library", a.getLibrary)
	mux.HandleFunc("POST /api/library/recover", a.recoverLibrary)
	mux.HandleFunc("POST /api/library/mcp", a.saveMCPConnection)
	mux.HandleFunc("PATCH /api/library/mcp/{connectionID}", a.saveMCPConnection)
	mux.HandleFunc("POST /api/library/mcp/{connectionID}/test", a.testMCPConnection)
	mux.HandleFunc("POST /api/library/mcp/{connectionID}/oauth", a.beginMCPOAuth)
	mux.HandleFunc("POST /api/library/mcp/{connectionID}/disconnect", a.disconnectMCPOAuth)
	mux.HandleFunc("GET /api/library/mcp/oauth/callback", a.completeMCPOAuth)
	mux.HandleFunc("GET /api/library/{kind}/{itemID}/deletion", a.previewLibraryDeletion)
	mux.HandleFunc("DELETE /api/library/{kind}/{itemID}", a.deleteLibraryItem)
	mux.HandleFunc("GET /api/sessions/{id}/capabilities", a.chatSessionHandler(a.conversationCapabilities))
	mux.HandleFunc("PATCH /api/sessions/{id}/capabilities", a.chatSessionHandler(a.conversationCapabilities))
	mux.HandleFunc("POST /api/sessions/{id}/questions/{questionID}/elicitation", a.answerMCPElicitation)
}

func (a *app) getLibrary(w http.ResponseWriter, r *http.Request) {
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	root, skills, err := scanSkillLibrary()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.mu.Lock()
	connections := []map[string]any{}
	for _, c := range a.state.MCPConnections {
		connections = append(connections, publicMCPConnection(c))
	}
	pending := len(a.state.LibraryDeletions) > 0
	piBinary, piInstalled := a.binaries["pi"]
	ocBinary, ocInstalled := a.binaries["opencode"]
	codexBinary, codexInstalled := a.binaries["codex"]
	a.mu.Unlock()
	sort.Slice(connections, func(i, j int) bool { return str(connections[i], "name") < str(connections[j], "name") })
	piSupported := piInstalled && validatePiLibraryBinary(piBinary) == nil
	ocSupported := ocInstalled && validateOpenCodeLibraryBinary(ocBinary) == nil
	codexSupported := codexInstalled && validateCodexLibraryBinary(codexBinary) == nil
	respond(w, 200, map[string]any{"skillsRoot": root, "skills": skills, "connections": connections, "recoveryPending": pending, "harnesses": map[string]any{"pi": map[string]any{"managed": piSupported, "status": map[bool]string{true: "supported", false: "not_validated"}[piSupported], "evidence": "Pi 0.85.1 resource-loader/RPC fixture probes and per-start effective catalog handshake"}, "opencode": map[string]any{"managed": ocSupported, "status": map[bool]string{true: "supported", false: "not_validated"}[ocSupported], "evidence": "OpenCode 1.18.35 controlled discovery profile and empty/selected/YOLO native catalog probes"}, "codex": map[string]any{"managed": codexSupported, "status": map[bool]string{true: "supported", false: "not_validated"}[codexSupported], "evidence": "Codex 0.153.4 thread projection, gateway-only registry and local model-surface fixture; native plugin/app/hook/delegation sources disabled"}}})
}

type LibraryReference struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	ProjectID string `json:"projectId,omitempty"`
	Revision  string `json:"revision,omitempty"`
}
type LibraryAuthoringChange struct {
	Project  Project           `json:"project"`
	Expected map[string]string `json:"expected"`
	Writes   map[string][]byte `json:"writes"`
}
type LibraryDeletion struct {
	ID           string                   `json:"id"`
	Kind         string                   `json:"kind"`
	ItemID       string                   `json:"itemId"`
	ItemRevision string                   `json:"itemRevision"`
	Root         string                   `json:"root,omitempty"`
	Projects     []LibraryAuthoringChange `json:"projects"`
}
type libraryDeletionPreview struct {
	stateRevision string
	Revision      string             `json:"revision"`
	ItemRevision  string             `json:"itemRevision"`
	References    []LibraryReference `json:"references"`
	change        LibraryDeletion
}

func selectionReferences(s CapabilitySelection, kind, id string) bool {
	if kind == "skills" {
		return slices.Contains(s.Skills, id)
	}
	return slices.ContainsFunc(s.MCPConnections, func(c MCPSelection) bool { return c.ConnectionID == id })
}
func removeLibraryReference(s CapabilitySelection, kind, id string) CapabilitySelection {
	s = copySelection(s)
	if kind == "skills" {
		s.Skills = slices.DeleteFunc(s.Skills, func(value string) bool { return value == id })
	} else {
		s.MCPConnections = slices.DeleteFunc(s.MCPConnections, func(c MCPSelection) bool { return c.ConnectionID == id })
	}
	return s
}

// Caller owns authoringMu then libraryMu; capture desired references and exact
// source revisions. Inaccessible known projects prevent a misleading preview.
func (a *app) libraryDeletionPreview(kind, id string) (libraryDeletionPreview, error) {
	result := libraryDeletionPreview{References: []LibraryReference{}, change: LibraryDeletion{Kind: kind, ItemID: id, Projects: []LibraryAuthoringChange{}}}
	if kind == "skills" {
		if !validSkillID(id) {
			return result, errors.New("Invalid skill ID.")
		}
		root, err := skillLibraryRoot()
		if err != nil {
			return result, err
		}
		p, err := stableSkillPackage(root, id, false)
		if err != nil && p.Entry.Revision == "" {
			return result, err
		}
		result.ItemRevision = p.Entry.Revision
		result.change.Root = root
	} else if kind == "mcp" {
		a.mu.Lock()
		c, exists := a.state.MCPConnections[id]
		a.mu.Unlock()
		if !exists {
			return result, errors.New("Connection not found.")
		}
		result.ItemRevision = c.Revision
	} else {
		return result, errors.New("Unknown library item kind.")
	}
	a.mu.Lock()
	result.stateRevision = libraryReferenceState(&a.state, kind, id)
	projects := append([]Project{}, a.state.Projects...)
	for _, s := range a.state.Sessions {
		if selectionReferences(s.Capabilities.Desired, kind, id) {
			result.References = append(result.References, LibraryReference{Kind: "session", ID: s.ID, Title: s.Title, ProjectID: s.ProjectID, Revision: fmt.Sprint(s.Capabilities.Revision)})
		}
	}
	for _, run := range a.state.GraphRuns {
		for _, snapshot := range run.Snapshot.Capabilities {
			if snapshot != nil && selectionReferences(snapshot.Selection, kind, id) {
				result.References = append(result.References, LibraryReference{Kind: "captured_run", ID: run.ID, Title: run.GraphID, ProjectID: run.ProjectID})
				break
			}
		}
	}
	a.mu.Unlock()
	for _, p := range projects {
		catalog, err := loadAuthoring(p)
		if err != nil || len(catalog.Errors) > 0 {
			return result, fmt.Errorf("Cannot inspect agent references in project %s; restore access and resolve authoring errors before deleting.", p.Name)
		}
		change := LibraryAuthoringChange{Project: p, Expected: map[string]string{}, Writes: map[string][]byte{}}
		for _, agent := range catalog.Agents {
			selection := CapabilitySelection{Skills: agent.Skills, MCPConnections: agent.MCPConnections}
			if !selectionReferences(selection, kind, id) {
				continue
			}
			result.References = append(result.References, LibraryReference{Kind: "agent", ID: agent.ID, Title: agent.Name, ProjectID: p.ID, Revision: agent.Revision})
			selection = removeLibraryReference(selection, kind, id)
			definition := agent.AgentDefinition
			definition.Skills, definition.MCPConnections = selection.Skills, selection.MCPConnections
			data, err := toml.Marshal(definition)
			if err != nil {
				return result, err
			}
			change.Expected[agentFile(agent.ID)] = agent.Revision
			change.Writes[agentFile(agent.ID)] = data
		}
		if len(change.Writes) > 0 {
			result.change.Projects = append(result.change.Projects, change)
		}
	}
	sort.Slice(result.References, func(i, j int) bool {
		l, r := result.References[i], result.References[j]
		return l.Kind+l.ProjectID+l.ID < r.Kind+r.ProjectID+r.ID
	})
	data, _ := json.Marshal(map[string]any{"item": result.ItemRevision, "references": result.References})
	result.Revision = revision(data)
	result.change.ItemRevision = result.ItemRevision
	return result, nil
}

func (a *app) previewLibraryDeletion(w http.ResponseWriter, r *http.Request) {
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	preview, err := a.libraryDeletionPreview(r.PathValue("kind"), r.PathValue("itemID"))
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	respond(w, 200, preview)
}

func (a *app) deleteLibraryItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision string `json:"revision"`
	}
	if !decode(w, r, &body) {
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	preview, err := a.libraryDeletionPreview(r.PathValue("kind"), r.PathValue("itemID"))
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	if preview.Revision != body.Revision {
		fail(w, 409, "Item or references changed. Review the deletion preview again.")
		return
	}
	change := preview.change
	change.ID = newID()
	a.mu.Lock()
	if libraryReferenceState(&a.state, change.Kind, change.ItemID) != preview.stateRevision {
		a.mu.Unlock()
		fail(w, 409, "Library references changed during preview. Review the deletion again.")
		return
	}
	if len(a.state.LibraryDeletions) > 0 {
		a.mu.Unlock()
		fail(w, 409, "A library deletion needs recovery. Retry recovery before another deletion.")
		return
	}
	err = a.commitLocked(func(d *diskState) { d.LibraryDeletions = append(d.LibraryDeletions, change) })
	a.mu.Unlock()
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	if err := applyLibraryDeletionFiles(change); err != nil {
		fail(w, 409, "Deletion is pending recovery; no snapshots were removed. "+err.Error())
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.commitLocked(func(d *diskState) { finishLibraryDeletion(d, change) }); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, map[string]bool{"removed": true})
}

func applyLibraryDeletionFiles(change LibraryDeletion) error {
	for _, project := range change.Projects {
		if err := recoverAuthoring(project.Project); err != nil {
			return err
		}
		for path, expected := range project.Expected {
			data, err := readConfig(project.Project, path)
			if err != nil || revision(data) != expected && revision(data) != revision(project.Writes[path]) {
				return fmt.Errorf("Agent file %s changed externally; restore the expected revision or complete the intended removal before recovery.", path)
			}
		}
		if err := commitAuthoring(project.Project, project.Writes); err != nil {
			return err
		}
	}
	if change.Kind == "skills" {
		root, err := skillLibraryRoot()
		if err != nil || !piSamePath(root, change.Root) {
			return errors.New("Skill library root changed during deletion.")
		}
		trash := filepath.Join(filepath.Dir(root), "skill-library-trash", change.ID)
		if _, err := os.Stat(trash); err == nil {
			return nil
		}
		p, err := stableSkillPackage(root, change.ItemID, false)
		if err != nil && p.Entry.Revision == "" || p.Entry.Revision != change.ItemRevision {
			return errors.New("Skill changed during deletion; restore its confirmed revision before recovery.")
		}
		if err := os.MkdirAll(filepath.Dir(trash), 0700); err != nil {
			return err
		}
		return os.Rename(filepath.Join(root, change.ItemID), trash)
	}
	return nil
}

func finishLibraryDeletion(d *diskState, change LibraryDeletion) {
	for i := range d.Sessions {
		s := &d.Sessions[i]
		if selectionReferences(s.Capabilities.Desired, change.Kind, change.ItemID) {
			s.Capabilities.Desired = removeLibraryReference(s.Capabilities.Desired, change.Kind, change.ItemID)
			s.Capabilities.Revision++
			s.UpdatedAt = now()
		}
	}
	if change.Kind == "mcp" {
		delete(d.MCPConnections, change.ItemID)
	}
	d.LibraryDeletions = slices.DeleteFunc(d.LibraryDeletions, func(c LibraryDeletion) bool { return c.ID == change.ID })
	pruneMCPCredentials(d)
}

func pruneMCPCredentials(d *diskState) {
	keep := map[string]bool{}
	for _, c := range d.MCPConnections {
		keep[c.CredentialID] = true
	}
	for _, s := range d.Sessions {
		if s.Status == "running" && s.Capabilities.Preparing != nil {
			for _, c := range s.Capabilities.Preparing.Connections {
				keep[c.CredentialID] = true
			}
		}
		if s.Status == "running" && s.Capabilities.Applied != nil {
			for _, c := range s.Capabilities.Applied.Connections {
				keep[c.CredentialID] = true
			}
		}
	}
	for _, run := range d.GraphRuns {
		if graphRunActive(run.Status) {
			for _, snapshot := range run.Snapshot.Capabilities {
				if snapshot != nil {
					for _, c := range snapshot.Connections {
						keep[c.CredentialID] = true
					}
				}
			}
		}
	}
	for id := range d.MCPCredentials {
		if !keep[id] {
			delete(d.MCPCredentials, id)
		}
	}
}

func libraryReferenceState(d *diskState, kind, id string) string {
	values := map[string]any{}
	for _, s := range d.Sessions {
		if selectionReferences(s.Capabilities.Desired, kind, id) {
			values["session/"+s.ID] = map[string]any{"revision": s.Capabilities.Revision, "selection": s.Capabilities.Desired}
		}
	}
	for _, r := range d.GraphRuns {
		for _, snapshot := range r.Snapshot.Capabilities {
			if snapshot != nil && selectionReferences(snapshot.Selection, kind, id) {
				values["run/"+r.ID] = r.Snapshot.LibraryRevision
				break
			}
		}
	}
	data, _ := json.Marshal(values)
	return revision(data)
}

func (a *app) recoverLibrary(w http.ResponseWriter, r *http.Request) {
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	for {
		a.mu.Lock()
		if len(a.state.LibraryDeletions) == 0 {
			a.mu.Unlock()
			respond(w, 200, map[string]bool{"recovered": true})
			return
		}
		change := a.state.LibraryDeletions[0]
		a.mu.Unlock()
		if err := applyLibraryDeletionFiles(change); err != nil {
			fail(w, 409, err.Error())
			return
		}
		a.mu.Lock()
		err := a.commitLocked(func(d *diskState) { finishLibraryDeletion(d, change) })
		a.mu.Unlock()
		if err != nil {
			fail(w, 503, err.Error())
			return
		}
	}
}

func recoverLibraryDeletions(dir string, d *diskState) error {
	for len(d.LibraryDeletions) > 0 {
		change := d.LibraryDeletions[0]
		if err := applyLibraryDeletionFiles(change); err != nil {
			return fmt.Errorf("Library deletion %s requires recovery: %w", change.ID, err)
		}
		finishLibraryDeletion(d, change)
		if err := saveState(dir, d); err != nil {
			return err
		}
	}
	return nil
}
