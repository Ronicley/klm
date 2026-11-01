package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func (a *app) registerAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{id}/agent", a.chatSessionHandler(a.sessionAgentState))
	mux.HandleFunc("PATCH /api/sessions/{id}/agent", a.chatSessionHandler(a.sessionAgentState))
	mux.HandleFunc("GET /api/agents", a.listAgents)
	mux.HandleFunc("GET /api/agents/{item}", a.getAgent)
	mux.HandleFunc("POST /api/agents", a.saveCatalogAgent)
	mux.HandleFunc("PATCH /api/agents/{item}", a.saveCatalogAgent)
	mux.HandleFunc("DELETE /api/agents/{item}", a.deleteCatalogAgent)
	mux.HandleFunc("GET /api/models/{harness}", a.globalAgentModels)
}

func agentHTTPError(w http.ResponseWriter, err error) {
	var problem *AgentConfigProblem
	if errors.As(err, &problem) {
		respond(w, problem.status, problem)
		return
	}
	sqlHTTPError(w, err)
}

func agentPageBounds(r *http.Request, prefix string) (int, int, error) {
	offset, limit := 0, 50
	for _, item := range []struct {
		name   string
		target *int
	}{{prefix + "offset", &offset}, {prefix + "limit", &limit}} {
		if value := r.URL.Query().Get(item.name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return 0, 0, agentProblem(400, "invalid_pagination", item.name, "Enter a valid page offset and limit.")
			}
			*item.target = n
		}
	}
	if limit < 1 || limit > 100 {
		return 0, 0, agentProblem(400, "invalid_pagination", prefix+"limit", "Page limit must be between 1 and 100.")
	}
	return offset, limit, nil
}

func boundedAgentReferences(row CatalogAgentRecord, offset, limit int) CatalogAgentRecord {
	start := min(offset, len(row.References))
	end := min(start+limit, len(row.References))
	row.ReferencesHasMore = end < len(row.References)
	if row.ReferencesHasMore {
		row.ReferencesNextOffset = end
	}
	row.References = row.References[start:end]
	if len(row.GraphReferences) >= end {
		row.GraphReferences = row.GraphReferences[start:end]
	}
	return row
}

func (a *app) listAgents(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := agentPageBounds(r, "")
	if err != nil {
		agentHTTPError(w, err)
		return
	}
	catalog, err := a.agentCatalog(r.Context(), r.URL.Query().Get("projectId"))
	if err != nil {
		agentHTTPError(w, err)
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	items := []map[string]any{}
	total := 0
	for _, row := range catalog.Agents {
		if query != "" && !strings.Contains(strings.ToLower(row.ID+" "+row.Name+" "+row.Description+" "+row.Source.ProjectName), query) {
			continue
		}
		if total >= offset && len(items) < limit {
			items = append(items, map[string]any{"id": row.ID, "name": row.Name, "description": row.Description, "enabled": row.Enabled,
				"defaultHarness": row.DefaultHarness, "model": row.Model, "effort": row.Effort, "revision": row.Revision, "source": row.Source, "status": row.Status,
				"available": row.Available, "unavailableReason": row.UnavailableReason, "referenceCount": len(row.References)})
		}
		total++
	}
	respond(w, 200, map[string]any{"agents": items, "diagnostics": catalog.Diagnostics, "complete": catalog.Complete, "revision": catalog.Revision, "hasMore": offset+len(items) < total, "nextOffset": offset + len(items)})
}

func (a *app) getAgent(w http.ResponseWriter, r *http.Request) {
	// Detail references use the same bounded offset/limit parameters as list pages.
	offset, limit, err := agentPageBounds(r, "")
	if err != nil {
		agentHTTPError(w, err)
		return
	}
	catalog, err := a.agentCatalog(r.Context(), r.URL.Query().Get("projectId"))
	if err != nil {
		agentHTTPError(w, err)
		return
	}
	items := []CatalogAgentRecord{}
	uid := r.URL.Query().Get("sourceUid")
	for _, row := range catalog.Agents {
		if row.ID == r.PathValue("item") && (uid == "" || row.Source.SourceUID == uid) {
			items = append(items, row)
		}
	}
	if len(items) == 0 {
		agentHTTPError(w, agentProblem(404, "agent_not_found", "agent", "Agent source not found. Reload the catalog."))
		return
	}
	if len(items) > 1 {
		problem := agentProblem(409, "agent_conflict", "agent", "More than one source has this agent identifier.")
		for _, row := range items {
			problem.Conflicts = append(problem.Conflicts, row.Source)
		}
		agentHTTPError(w, problem)
		return
	}
	respond(w, 200, boundedAgentReferences(items[0], offset, limit))
}

func (a *app) agentModelDirectory() (string, error) {
	path, err := filepath.Abs(filepath.Join(a.dir, "workspaces", "agent-catalog"))
	if err == nil {
		err = os.MkdirAll(path, 0700)
	}
	return path, err
}

func (a *app) agentAuthoringModels(ctx context.Context, projectID, harness, cwd string, refresh bool) (*ModelCatalog, error) {
	return a.catalog(ctx, Session{ProjectID: projectID, Harness: harness}, cwd, refresh)
}

func (a *app) globalAgentModels(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(55 * time.Second))
	cwd, err := a.agentModelDirectory()
	if err != nil {
		agentHTTPError(w, err)
		return
	}
	catalog, err := a.agentAuthoringModels(r.Context(), "", r.PathValue("harness"), cwd, r.URL.Query().Get("refresh") == "true")
	if err != nil {
		agentHTTPError(w, agentProblem(502, "catalog_unavailable", "model", modelCatalogLoadError))
		return
	}
	respond(w, 200, catalog)
}

type saveCatalogAgentBody struct {
	Agent     AgentDefinition `json:"agent"`
	Revision  string          `json:"revision"`
	SourceUID string          `json:"sourceUid"`
}

func (a *app) saveCatalogAgent(w http.ResponseWriter, r *http.Request) {
	var body saveCatalogAgentBody
	if !decode(w, r, &body) {
		return
	}
	if err := validateAgentDefinition(&body.Agent); err != nil {
		agentHTTPError(w, agentProblem(400, "invalid_agent", "agent", err.Error()))
		return
	}
	projectID := r.PathValue("id")
	var p Project
	var cwd string
	var err error
	if projectID != "" {
		if a.store == nil {
			agentHTTPError(w, agentProblem(503, "storage_unavailable", "agent", "Agent catalogs require SQLite storage."))
			return
		}
		p, err = a.store.readProjectMeta(r.Context(), projectID)
		if err == nil && p.Removed {
			err = agentProblem(404, "project_unavailable", "projectId", "Project not found.")
		}
		cwd = p.Folder
	} else {
		cwd, err = a.agentModelDirectory()
	}
	if err != nil {
		agentHTTPError(w, err)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(55 * time.Second))
	catalog, err := a.agentAuthoringModels(r.Context(), projectID, body.Agent.DefaultHarness, cwd, false)
	if err != nil {
		agentHTTPError(w, agentProblem(502, "catalog_unavailable", "model", modelCatalogLoadError))
		return
	}
	option := catalog.find(body.Agent.Model)
	if option == nil || len(option.Efforts) > 0 && !slices.Contains(option.Efforts, body.Agent.Effort) || len(option.Efforts) == 0 && body.Agent.Effort != "" {
		agentHTTPError(w, agentProblem(400, "incompatible_model", "model", "Select a model and effort available in the selected harness."))
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	row, err := a.mutateCatalogAgentLocked(r.Context(), p, r.PathValue("item"), body, false)
	if err != nil {
		agentHTTPError(w, err)
		return
	}
	respond(w, 200, boundedAgentReferences(row, 0, 50))
}

func (a *app) deleteCatalogAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Revision  string `json:"revision"`
		SourceUID string `json:"sourceUid"`
	}
	if !decode(w, r, &body) {
		return
	}
	var p Project
	var err error
	if id := r.PathValue("id"); id != "" {
		if a.store == nil {
			agentHTTPError(w, agentProblem(503, "storage_unavailable", "agent", "Agent catalogs require SQLite storage."))
			return
		}
		p, err = a.store.readProjectMeta(r.Context(), id)
		if err == nil && p.Removed {
			err = agentProblem(404, "project_unavailable", "projectId", "Project not found.")
		}
		if err != nil {
			agentHTTPError(w, err)
			return
		}
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	_, err = a.mutateCatalogAgentLocked(r.Context(), p, r.PathValue("item"), saveCatalogAgentBody{Revision: body.Revision, SourceUID: body.SourceUID}, true)
	if err != nil {
		agentHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"removed": true})
}

// Caller owns authoringMu. Catalog and graph preflight happen before any durable
// intent or source-byte mutation. This does not create/adopt session bindings.
func (a *app) mutateCatalogAgentLocked(ctx context.Context, p Project, oldID string, body saveCatalogAgentBody, remove bool) (CatalogAgentRecord, error) {
	if oldID != "" && p.ID == "" && body.SourceUID == "" {
		return CatalogAgentRecord{}, agentProblem(400, "source_identity_required", "sourceUid", "Supply the exact global source UID from the current catalog.")
	}
	if oldID == "" && (body.SourceUID != "" || body.Revision != "") {
		return CatalogAgentRecord{}, agentProblem(400, "invalid_creation_identity", "sourceUid", "New agents receive a new source UID; omit revision and sourceUid.")
	}
	scan, err := a.scanAgentCatalogLocked(ctx, p.ID)
	if err != nil {
		return CatalogAgentRecord{}, err
	}
	if scan.recoveryPending {
		return CatalogAgentRecord{}, agentProblem(409, "recovery_pending", "agent", "Finish the pending project authoring transaction before editing agents.")
	}
	var recovering bool
	if err := a.store.readers.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&recovering); err != nil {
		return CatalogAgentRecord{}, err
	}
	if recovering {
		return CatalogAgentRecord{}, agentProblem(409, "recovery_pending", "agent", "Finish Library recovery before editing agents.")
	}
	var old *CatalogAgentRecord
	for i := range scan.all {
		row := &scan.all[i]
		if row.ID == oldID && row.Source.ProjectID == p.ID && (p.ID != "" || row.Source.Scope == "global") {
			old = row
			break
		}
	}
	if oldID != "" && (old == nil || old.Revision != body.Revision || body.SourceUID != "" && body.SourceUID != old.Source.SourceUID) {
		problem := agentProblem(409, "source_changed", "revision", "Agent changed or was removed. Reload before saving.")
		if old != nil {
			problem.CurrentRevision = old.Revision
		}
		return CatalogAgentRecord{}, problem
	}
	if remove && old == nil {
		return CatalogAgentRecord{}, agentProblem(404, "agent_not_found", "agent", "Agent not found.")
	}
	if old != nil {
		// Slug conflicts with distinct exact UIDs can be repaired by rename, but
		// a copied lifetime has no authoritative owner in any scope. Check the
		// complete effective scan, not the bounded/display-only diagnostics.
		ownerSources := map[string]AgentSourceRef{}
		for _, row := range scan.all {
			if row.Source.SourceUID == old.Source.SourceUID {
				path := ""
				if row.Source.Scope == "project" {
					path = agentFile(row.ID)
				}
				ownerSources[agentSourceRecordID(row.Source, path)] = row.Source
			}
		}
		// An unreadable known locator keeps its lifetime reservation too; an I/O
		// failure cannot silently resolve an already-observed copied UID.
		for key, source := range scan.sources {
			if source.State != "deleted" && source.Source.SourceUID == old.Source.SourceUID {
				ownerSources[key] = source.Source
			}
		}
		if len(ownerSources) > 1 {
			problem := agentProblem(409, "source_identity_conflict", "sourceUid", "This source UID belongs to multiple definitions. Resolve the copied identity before editing, deleting or renaming it.")
			for _, key := range sortedGraphKeys(ownerSources) {
				if len(problem.Conflicts) >= 100 {
					break
				}
				problem.Conflicts = append(problem.Conflicts, ownerSources[key])
			}
			return CatalogAgentRecord{}, problem
		}
		oldPath := ""
		if p.ID != "" {
			oldPath = agentFile(old.ID)
		}
		if scan.sources[agentSourceRecordID(old.Source, oldPath)].State == "deleted" {
			return CatalogAgentRecord{}, agentProblem(409, "source_identity_reused", "sourceUid", "A deleted source marker cannot be reused. Replace its source identity before editing.")
		}
		for _, diagnostic := range scan.Diagnostics {
			if diagnostic.Code == "source_identity_ambiguous" && diagnostic.Source != nil && diagnostic.Source.SourceUID == old.Source.SourceUID {
				return CatalogAgentRecord{}, agentProblem(409, "source_identity_ambiguous", "sourceUid", diagnostic.Message)
			}
		}
	}
	id := authoringSlug(body.Agent.Name)
	if remove {
		id = old.ID
	}
	if (old == nil || oldID != id) && !scan.Complete {
		return CatalogAgentRecord{}, agentProblem(409, "catalog_incomplete", "agent", "Restore access to all registered project catalogs before creating or renaming an agent.")
	}
	if old == nil && (len(scan.all) >= agentCatalogSourceLimit || len(scan.sources) >= agentRegistryLimit) {
		return CatalogAgentRecord{}, agentProblem(409, "catalog_capacity", "agent", "The bounded agent catalog or source registry is full.")
	}
	if !remove {
		problem := agentProblem(409, "agent_conflict", "name", "An agent with this identifier already exists in another source.")
		for _, row := range scan.all {
			if row.ID == id && (old == nil || row.Source.SourceUID != old.Source.SourceUID || row.Source.ProjectID != old.Source.ProjectID) {
				problem.Conflicts = append(problem.Conflicts, row.Source)
			}
		}
		for _, source := range scan.sources {
			if source.State == "source_unavailable" && source.Source.ID == id && (old == nil || source.Source.SourceUID != old.Source.SourceUID) {
				problem.Conflicts = append(problem.Conflicts, source.Source)
			}
		}
		if len(problem.Conflicts) > 0 {
			return CatalogAgentRecord{}, problem
		}
	}
	if old != nil && (remove || !body.Agent.Enabled || id != old.ID) && !scan.referencesComplete {
		return CatalogAgentRecord{}, agentProblem(409, "references_unavailable", "agent", "Restore access to graph references before disabling, deleting or renaming this agent.")
	}
	if old != nil && (remove || !body.Agent.Enabled) && len(old.References) > 0 {
		problem := agentProblem(409, "agent_in_use", "agent", "This agent is referenced by a graph and cannot be disabled or deleted.")
		problem.BlockingReferences = old.References[:min(len(old.References), 100)]
		return CatalogAgentRecord{}, problem
	}
	uid := newID()
	if old != nil {
		uid = old.Source.SourceUID
	}
	source := AgentSourceRef{SourceUID: uid, ID: id, Scope: "global"}
	if p.ID != "" {
		source.Scope = "project"
		source.ProjectID = p.ID
		source.ProjectName = p.Name
	}
	change := AgentCatalogChange{ID: newID(), Sources: []AgentSourceRecord{}}
	roots := map[string]*AgentAuthoringWrite{}
	write := func(project Project, path string, data []byte, expected string) {
		root := roots[project.ID]
		if root == nil {
			root = &AgentAuthoringWrite{Project: Project{ID: project.ID, Name: project.Name, Folder: project.Folder}, Expected: map[string]string{}, Writes: map[string][]byte{}}
			roots[project.ID] = root
		}
		root.Expected[path], root.Writes[path] = expected, data
	}
	data, err := toml.Marshal(body.Agent)
	if err != nil {
		return CatalogAgentRecord{}, err
	}
	newRevision := revision(data)
	path := ""
	if p.ID != "" {
		path = agentFile(id)
		if !remove {
			data, err = toml.Marshal(agentDocument{AgentDefinition: body.Agent, SourceUID: uid})
			if err != nil {
				return CatalogAgentRecord{}, err
			}
		}
		if remove {
			write(p, path, nil, old.Revision)
		} else {
			// Local readers, offline graph validation and Library serializers now
			// understand the wrapper. Preserve the source's existing marker too.
			if old != nil {
				previous, readErr := readConfig(p, agentFile(old.ID))
				if readErr != nil {
					return CatalogAgentRecord{}, readErr
				}
				if revision(previous) != old.Revision {
					return CatalogAgentRecord{}, agentProblem(409, "source_changed", "revision", "Agent changed while preparing its save.")
				}
				doc, parseErr := readAgentDocument(previous)
				if parseErr == nil && doc.SourceUID != "" {
					data, err = toml.Marshal(agentDocument{AgentDefinition: body.Agent, SourceUID: doc.SourceUID})
					if err != nil {
						return CatalogAgentRecord{}, err
					}
				}
			}
			expected := ""
			if old != nil && old.ID == id {
				expected = old.Revision
			}
			write(p, path, data, expected)
			if old != nil && old.ID != id {
				write(p, agentFile(old.ID), nil, old.Revision)
			}
			newRevision = revision(data)
		}
	} else if remove {
		change.RemoveGlobal = uid
	} else {
		change.Global = &GlobalAgentRecord{ID: uid, Slug: id, Definition: body.Agent, Revision: newRevision}
	}
	if old != nil && id != old.ID && !remove {
		for _, graph := range scan.graphs {
			if p.ID != "" && graph.Project.ID != p.ID {
				continue
			}
			changed := false
			for key, node := range graph.Definition.Nodes {
				if node.Agent == old.ID {
					node.Agent = id
					graph.Definition.Nodes[key] = node
					changed = true
				}
			}
			if changed {
				raw, err := yaml.Marshal(graph.Definition)
				if err != nil {
					return CatalogAgentRecord{}, err
				}
				write(graph.Project, graphFile(graph.ID), raw, graph.Revision)
			}
		}
	}
	state := "linked"
	if !body.Agent.Enabled {
		state = "disabled"
	}
	if remove {
		state = "deleted"
	}
	if old != nil && old.ID != id {
		oldPath := ""
		if p.ID != "" {
			oldPath = agentFile(old.ID)
		}
		record := scan.sources[agentSourceRecordID(old.Source, oldPath)]
		record.State = "deleted"
		change.Sources = append(change.Sources, record)
	}
	record := AgentSourceRecord{ID: agentSourceRecordID(source, path), Source: source, Path: path, Revision: newRevision, State: state}
	record.Adopted = p.ID != ""
	if remove {
		record.Revision = old.Revision
	}
	change.Sources = append(change.Sources, record)
	for _, key := range sortedGraphKeys(roots) {
		root := roots[key]
		raw, _ := json.Marshal(root.Writes)
		if len(raw) > 2<<20 {
			return CatalogAgentRecord{}, agentProblem(409, "change_too_large", "agent", "A project authoring write set exceeds 2 MiB.")
		}
		change.Files = append(change.Files, *root)
	}
	if err := a.commitAgentCatalogChange(ctx, change); err != nil {
		return CatalogAgentRecord{}, err
	}
	refs := []AgentReference{}
	names := []string{}
	if old != nil {
		refs = old.References
		names = old.GraphReferences
	}
	return CatalogAgentRecord{AgentRecord: AgentRecord{AgentDefinition: body.Agent, ID: id, Revision: newRevision, GraphReferences: names}, Source: source, Status: state, Available: state == "linked" && scan.Complete, References: refs}, nil
}
