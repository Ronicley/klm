package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const (
	agentCatalogSourceLimit = 2000
	agentCatalogByteLimit   = 16 << 20
	agentRegistryLimit      = 20000
)

type agentGraphSource struct {
	Project    Project
	ID         string
	Revision   string
	Definition GraphDefinition
}

type agentCatalogScan struct {
	AgentCatalog
	all                []CatalogAgentRecord
	sources            map[string]AgentSourceRecord
	previous           map[string]AgentSourceRecord
	projects           []Project
	graphs             []agentGraphSource
	referencesComplete bool
	bytes              int
	recoveryPending    bool
}

func agentSourceRecordID(source AgentSourceRef, path string) string {
	return revision([]byte(source.Scope + "/" + source.ProjectID + "/" + path + "/" + source.SourceUID))
}

func readAgentDocument(data []byte) (agentDocument, error) {
	doc := agentDocument{AgentDefinition: AgentDefinition{Enabled: true}}
	err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&doc)
	if err == nil && doc.SourceUID != "" && !validAgentSourceUID(doc.SourceUID) {
		err = errors.New("Invalid source_uid metadata.")
	}
	return doc, err
}

func marshalExistingAgentDocument(p Project, id string, definition AgentDefinition) ([]byte, error) {
	raw, err := readConfig(p, agentFile(id))
	if err != nil {
		return nil, err
	}
	document, err := readAgentDocument(raw)
	if err != nil {
		return nil, err
	}
	return toml.Marshal(agentDocument{AgentDefinition: definition, SourceUID: document.SourceUID})
}

func agentDirectoryFiles(ctx context.Context, p Project, kind, extension string, visit func(string, []byte) error) error {
	probe, err := configPath(p, ".klm/"+kind+"/.probe", false)
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Dir(probe))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := f.ReadDir(100)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for _, entry := range entries {
			count++
			if count > agentCatalogSourceLimit {
				return errors.New("Configuration directory exceeds the catalog entry limit.")
			}
			if !strings.HasSuffix(entry.Name(), extension) {
				continue
			}
			path := ".klm/" + kind + "/" + entry.Name()
			data, readErr := readConfig(p, path)
			if readErr != nil {
				return fmt.Errorf("%s: %w", path, readErr)
			}
			if err := visit(strings.TrimSuffix(entry.Name(), extension), data); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func (scan *agentCatalogScan) diagnostic(code, message string, source *AgentSourceRef) {
	if len(scan.Diagnostics) < 200 {
		scan.Diagnostics = append(scan.Diagnostics, AgentDiagnostic{Code: code, Message: message, Source: source})
	}
}

func (scan *agentCatalogScan) budget(data []byte) error {
	scan.bytes += len(data)
	if scan.bytes > agentCatalogByteLimit || len(scan.all) >= agentCatalogSourceLimit {
		return errors.New("Agent catalog exceeds its bounded source/content limit.")
	}
	return nil
}

func (scan *agentCatalogScan) previousAt(projectID, path string) *AgentSourceRecord {
	var found *AgentSourceRecord
	for _, key := range sortedGraphKeys(scan.previous) {
		source := scan.previous[key]
		if source.Source.ProjectID == projectID && source.Path == path && source.State != "deleted" {
			copy := source
			found = &copy
			break // Stable display locator; ambiguous lifetimes are blocked below.
		}
	}
	return found
}

func (scan *agentCatalogScan) addLocal(p Project, id string, data []byte) error {
	if err := scan.budget(data); err != nil {
		return err
	}
	if !validAuthoringID(id) {
		scan.Complete = false
		scan.diagnostic("invalid_agent_file", "Invalid agent filename in project "+p.Name+".", nil)
		return nil
	}
	path := agentFile(id)
	old := scan.previousAt(p.ID, path)
	doc, parseErr := readAgentDocument(data)
	uid := doc.SourceUID
	if uid == "" || !validAgentSourceUID(uid) {
		if old != nil {
			uid = old.Source.SourceUID
		} else {
			uid = newID()
		}
	}
	source := AgentSourceRef{SourceUID: uid, ID: id, Scope: "project", ProjectID: p.ID, ProjectName: p.Name}
	state := "linked"
	ambiguousPath := false
	if doc.SourceUID == "" && old != nil {
		for _, prior := range scan.previous {
			if prior.Source.ProjectID == p.ID && prior.Path == path && prior.State != "deleted" && prior.Source.SourceUID != old.Source.SourceUID {
				ambiguousPath = true
			}
		}
	}
	if parseErr == nil && authoringSlug(doc.Name) != id {
		parseErr = errors.New("Agent name and filename identifier differ.")
		scan.Complete = false
	}
	if parseErr == nil {
		parseErr = validateAgentDefinition(&doc.AgentDefinition)
	}
	if parseErr != nil {
		state = "source_invalid"
		scan.diagnostic("source_invalid", fmt.Sprintf("Agent %s in project %s: %s", id, p.Name, parseErr), &source)
	} else if !doc.Enabled {
		state = "disabled"
	}
	if ambiguousPath {
		state = "conflict"
		scan.diagnostic("source_identity_ambiguous", "Multiple source lifetimes occupy this path; restore an explicit source_uid before editing or executing it.", &source)
	}
	if old != nil && old.Adopted && doc.SourceUID == "" {
		state = "source_invalid"
		scan.diagnostic("source_identity_missing", "The adopted agent marker was removed. Restore its source_uid before using the bound source.", &source)
	}
	// An observed tombstone cannot be undone by restoring its marker.
	tombstonedUID := false
	if doc.SourceUID != "" {
		tombstoned, alive := false, false
		for _, prior := range scan.previous {
			if prior.Source.SourceUID == uid {
				if prior.State == "deleted" {
					tombstoned = true
				} else {
					alive = true
				}
			}
		}
		if tombstoned && !alive {
			tombstonedUID = true
			state = "conflict"
			scan.diagnostic("source_identity_reused", "A deleted source UID cannot be reused; use a new source identity.", &source)
		}
	}
	row := CatalogAgentRecord{AgentRecord: AgentRecord{AgentDefinition: doc.AgentDefinition, ID: id, Revision: revision(data), GraphReferences: []string{}}, Source: source, Status: state, References: []AgentReference{}}
	if p.Removed {
		row.Status = "project_removed"
	}
	scan.all = append(scan.all, row)
	record := AgentSourceRecord{ID: agentSourceRecordID(source, path), Source: source, Path: path, Revision: revision(data), State: state}
	record.Adopted = doc.SourceUID != "" || old != nil && old.Adopted
	if tombstonedUID {
		record.State = "deleted"
	}
	scan.sources[record.ID] = record
	if doc.SourceUID != "" && parseErr == nil && !tombstonedUID {
		// A valid different marker proves replacement of this locator's former
		// generation. An invalid/unreadable document never supplies that evidence.
		for _, prior := range scan.previous {
			if prior.Source.ProjectID == p.ID && prior.Path == path && prior.Source.SourceUID != uid {
				prior.State = "deleted"
				scan.sources[prior.ID] = prior
			}
		}
	}
	return nil
}

func (scan *agentCatalogScan) addGraphs(ctx context.Context, p Project) {
	err := agentDirectoryFiles(ctx, p, "graphs", ".yaml", func(id string, data []byte) error {
		if len(scan.graphs) >= agentCatalogSourceLimit {
			return errors.New("Graph reference catalog exceeds its entry limit.")
		}
		if err := scan.budget(data); err != nil {
			return err
		}
		var graph GraphDefinition
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&graph); err != nil {
			return err
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return errors.New("Expected one graph YAML document.")
		}
		if !validAuthoringID(id) || graph.Nodes == nil {
			return errors.New("Invalid graph source.")
		}
		scan.graphs = append(scan.graphs, agentGraphSource{Project: p, ID: id, Revision: revision(data), Definition: graph})
		return nil
	})
	if err != nil {
		scan.referencesComplete = false
		scan.diagnostic("graph_references_unavailable", "Cannot inspect graph references in project "+p.Name+": "+err.Error(), nil)
	}
}

func readAgentRows[T any](ctx context.Context, q sqliteQuery, table string, limit int) ([]T, error) {
	result := []T{}
	after := ""
	for {
		ids, err := sqliteQueryIDs(ctx, q, "SELECT id FROM data_"+table+" WHERE id>? ORDER BY id LIMIT 100", after)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return result, nil
		}
		for _, id := range ids {
			if len(result) >= limit {
				return nil, errors.New("Catalog registry exceeds its bounded record limit.")
			}
			var row T
			if err := decodeSQLiteRecord(ctx, q, table, id, 8<<20, &row); err != nil {
				return nil, err
			}
			result = append(result, row)
			after = id
		}
	}
}

// authoringMu is held. Reconciliation only records observed registry metadata;
// GET never adopts a marker or writes an authoring file.
func (a *app) scanAgentCatalogLocked(ctx context.Context, projectID string) (*agentCatalogScan, error) {
	if a.store == nil {
		return nil, agentProblem(503, "storage_unavailable", "agent", "Agent catalogs require SQLite storage.")
	}
	scan := &agentCatalogScan{AgentCatalog: AgentCatalog{Agents: []CatalogAgentRecord{}, Diagnostics: []AgentDiagnostic{}, Complete: true}, sources: map[string]AgentSourceRecord{}, previous: map[string]AgentSourceRecord{}, referencesComplete: true}
	var globals []GlobalAgentRecord
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		var err error
		scan.projects, err = readAgentRows[Project](ctx, tx, "projects", agentCatalogSourceLimit)
		if err != nil {
			return err
		}
		globals, err = readAgentRows[GlobalAgentRecord](ctx, tx, "global_agents", agentCatalogSourceLimit)
		if err != nil {
			return err
		}
		previous, err := readAgentRows[AgentSourceRecord](ctx, tx, "agent_sources", agentRegistryLimit)
		if err != nil {
			return err
		}
		for _, row := range previous {
			if err := validateAgentSourceRecord(row); err != nil {
				return err
			}
			scan.previous[row.ID] = row
			scan.sources[row.ID] = row
		}
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_agent_catalog_changes)`).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return agentProblem(409, "recovery_pending", "agent", "An agent catalog change needs recovery before reading current configuration.")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	foundProject := projectID == ""
	for _, p := range scan.projects {
		if p.Removed {
			continue
		}
		if _, journalErr := readConfig(p, authoringJournal); journalErr == nil {
			scan.recoveryPending = true
			scan.Complete, scan.referencesComplete = false, false
			scan.diagnostic("authoring_recovery_pending", "A project authoring transaction needs recovery before editing the agent catalog.", nil)
		}
		if p.ID == projectID && !p.Removed {
			foundProject = true
		}
	}
	if !foundProject {
		return nil, agentProblem(404, "project_unavailable", "projectId", "Project not found.")
	}
	for _, global := range globals {
		if !validAgentSourceUID(global.ID) || !validAuthoringID(global.Slug) || authoringSlug(global.Definition.Name) != global.Slug {
			return nil, errors.New("Invalid persisted global agent identity.")
		}
		encoded, _ := json.Marshal(global)
		if err := scan.budget(encoded); err != nil {
			return nil, err
		}
		source := AgentSourceRef{SourceUID: global.ID, ID: global.Slug, Scope: "global"}
		state := "linked"
		definition := global.Definition
		if err := validateAgentDefinition(&definition); err != nil {
			state = "source_invalid"
			scan.diagnostic("source_invalid", err.Error(), &source)
		} else if !definition.Enabled {
			state = "disabled"
		}
		scan.all = append(scan.all, CatalogAgentRecord{AgentRecord: AgentRecord{AgentDefinition: definition, ID: global.Slug, Revision: global.Revision, GraphReferences: []string{}}, Source: source, Status: state, References: []AgentReference{}})
		record := AgentSourceRecord{ID: agentSourceRecordID(source, ""), Source: source, State: state, Revision: global.Revision}
		scan.sources[record.ID] = record
	}
	for _, p := range scan.projects {
		if p.Removed {
			// Removed projects are dormant, including their authoring files. Keep
			// known slug/identity reservations without requiring the old directory
			// or treating its absence as deletion of those source lifetimes.
			for key, prior := range scan.previous {
				if prior.Source.ProjectID == p.ID && prior.State != "deleted" {
					prior.State = "source_unavailable"
					scan.sources[key] = prior
				}
			}
			continue
		}
		seen := map[string]bool{}
		err := agentDirectoryFiles(ctx, p, "agents", ".toml", func(id string, data []byte) error {
			seen[agentFile(id)] = true
			return scan.addLocal(p, id, data)
		})
		if err != nil {
			scan.Complete = false
			scan.diagnostic("catalog_incomplete", "Cannot inspect agents in project "+p.Name+": "+err.Error(), nil)
		}
		for key, prior := range scan.previous {
			if prior.Source.ProjectID != p.ID || prior.State == "deleted" {
				continue
			}
			if err != nil {
				// Unknown directory state retains every reservation; do not infer absence.
				if !seen[prior.Path] {
					prior.State = "source_unavailable"
					scan.sources[key] = prior
				}
			} else if !seen[prior.Path] {
				prior.State = "deleted"
				scan.sources[key] = prior
			}
		}
		scan.addGraphs(ctx, p)
	}
	sort.Slice(scan.all, func(i, j int) bool {
		left, right := scan.all[i], scan.all[j]
		return left.Source.Scope+left.Source.ProjectID+left.ID+left.Source.SourceUID < right.Source.Scope+right.Source.ProjectID+right.ID+right.Source.SourceUID
	})
	sort.Slice(scan.graphs, func(i, j int) bool {
		return scan.graphs[i].Project.ID+"/"+scan.graphs[i].ID < scan.graphs[j].Project.ID+"/"+scan.graphs[j].ID
	})
	claims := map[string][]int{}
	uids := map[string][]int{}
	for i, row := range scan.all {
		claims[row.ID] = append(claims[row.ID], i)
		uids[row.Source.SourceUID] = append(uids[row.Source.SourceUID], i)
	}
	for _, groups := range []map[string][]int{claims, uids} {
		for _, key := range sortedGraphKeys(groups) {
			indices := groups[key]
			if len(indices) < 2 {
				continue
			}
			conflicts := []AgentSourceRef{}
			for _, i := range indices {
				scan.all[i].Status = "conflict"
				conflicts = append(conflicts, scan.all[i].Source)
			}
			if len(scan.Diagnostics) < 200 {
				scan.Diagnostics = append(scan.Diagnostics, AgentDiagnostic{Code: "agent_conflict", Message: "Conflicting agent slug or source identity; no definition was selected.", Conflicts: conflicts})
			}
		}
	}
	for _, source := range scan.sources {
		if source.State == "deleted" {
			continue
		}
		// Reserve unavailable roots even when their files cannot be enumerated now.
		if source.State == "source_unavailable" {
			for i := range scan.all {
				if scan.all[i].ID == source.Source.ID && scan.all[i].Source.SourceUID != source.Source.SourceUID {
					scan.all[i].Status = "conflict"
				}
			}
		}
	}
	for i := range scan.all {
		row := &scan.all[i]
		for _, graph := range scan.graphs {
			if row.Source.Scope != "global" && graph.Project.ID != row.Source.ProjectID {
				continue
			}
			for _, node := range graph.Definition.Nodes {
				if node.Agent == row.ID {
					row.References = append(row.References, AgentReference{Kind: "graph", ID: graph.ID, Name: graph.Definition.Name, ProjectID: graph.Project.ID, ProjectName: graph.Project.Name})
					row.GraphReferences = append(row.GraphReferences, graph.Definition.Name)
					break
				}
			}
		}
		row.Available = row.Status == "linked" && scan.Complete
		if !row.Available {
			row.UnavailableReason = row.Status
			if row.Status == "linked" {
				row.UnavailableReason = "catalog_incomplete"
			}
		}
		key := agentSourceRecordID(row.Source, "")
		if row.Source.Scope == "project" {
			key = agentSourceRecordID(row.Source, agentFile(row.ID))
		}
		if record, ok := scan.sources[key]; ok && row.Status == "conflict" && record.State != "deleted" {
			record.State = "conflict"
			scan.sources[key] = record
		}
		if row.Source.Scope == "global" || row.Source.ProjectID == projectID && row.Status != "project_removed" {
			scan.Agents = append(scan.Agents, *row)
		}
	}
	sort.Slice(scan.Agents, func(i, j int) bool {
		left, right := scan.Agents[i], scan.Agents[j]
		return left.ID+left.Source.Scope+left.Source.ProjectID+left.Source.SourceUID < right.ID+right.Source.Scope+right.Source.ProjectID+right.Source.SourceUID
	})
	// Registry writes are small related records. No source definition or historical
	// transcript is copied into this index, and unchanged refreshes do not advance it.
	a.mu.Lock()
	err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		for _, key := range sortedGraphKeys(scan.sources) {
			source := scan.sources[key]
			if previous, exists := scan.previous[key]; exists && reflect.DeepEqual(previous, source) {
				continue
			}
			if err := st.putRoot("agent_sources", key, source); err != nil {
				return err
			}
			if source.Source.ProjectID != "" {
				st.changes.Projects[source.Source.ProjectID] = true
			}
		}
		return nil
	})
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	sort.Slice(scan.Diagnostics, func(i, j int) bool {
		left, _ := json.Marshal(scan.Diagnostics[i])
		right, _ := json.Marshal(scan.Diagnostics[j])
		return string(left) < string(right)
	})
	graphRevisions := []string{}
	for _, graph := range scan.graphs {
		graphRevisions = append(graphRevisions, graph.Project.ID+"/"+graph.ID+"/"+graph.Revision)
	}
	encoded, _ := json.Marshal(struct {
		Sources     map[string]AgentSourceRecord
		Diagnostics []AgentDiagnostic
		Complete    bool
		Graphs      []string
	}{scan.sources, scan.Diagnostics, scan.Complete, graphRevisions})
	scan.Revision = revision(encoded)
	return scan, nil
}

func (a *app) agentCatalog(ctx context.Context, projectID string) (AgentCatalog, error) {
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	scan, err := a.scanAgentCatalogLocked(ctx, projectID)
	if err != nil {
		return AgentCatalog{}, err
	}
	return scan.AgentCatalog, nil
}
