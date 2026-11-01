package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// References are streamed from maintained indexes, not retained captures. Project
// fingerprints bind filesystem preparation to the same registered directories.
func (st *sqliteSessionTx) libraryReferenceHash(kind, id string, visit func(LibraryReference) error) (string, error) {
	h := sha256.New()
	encoder := json.NewEncoder(h)
	rows, err := st.tx.QueryContext(st.ctx, `SELECT kind,id,title,project,revision FROM (
	 SELECT 'session' AS kind,s.id,s.x_title AS title,s.project_id AS project,d.digest AS revision FROM selection_references r JOIN data_capability_selections d ON d.id=r.record_key JOIN data_session_capabilities c ON c.id=d.owner_id JOIN data_sessions s ON s.id=c.owner_id WHERE r.domain='capability_selections' AND r.kind=? AND r.item=?
	 UNION ALL SELECT 'automation',a.id,a.x_name,a.project_id,a.digest FROM selection_references r JOIN data_automations a ON a.id=r.record_key WHERE r.domain='automations' AND r.kind=? AND r.item=? AND a.x_deleted<>'true'
	 UNION ALL SELECT 'captured_automation_run',a.id,a.x_snapshot_name,a.project_id,'' FROM capture_references r JOIN data_automation_runs a ON a.id=r.record_key WHERE r.domain='automation_runs' AND r.kind=? AND r.item=?
	 UNION ALL SELECT 'captured_run',g.id,g.x_graphId,g.project_id,'' FROM capture_references r JOIN data_graph_snapshots p ON p.id=r.record_key JOIN data_graph_runs g ON g.id=p.owner_id WHERE r.domain='graph_snapshots' AND r.kind=? AND r.item=?
	 UNION ALL SELECT 'global_agent',a.x_slug,a.x_slug,'',a.digest FROM selection_references r JOIN data_global_agents a ON a.id=r.record_key WHERE r.domain='global_agents' AND r.kind=? AND r.item=?
	 UNION ALL SELECT 'agent_binding',s.id,s.x_title,s.project_id,b.digest FROM selection_references r JOIN data_agent_bindings b ON b.id=r.record_key JOIN data_sessions s ON s.id=b.owner_id WHERE r.domain='agent_bindings' AND r.kind=? AND r.item=?
	) ORDER BY kind,project,id`, kind, id, kind, id, kind, id, kind, id, kind, id, kind, id)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var ref LibraryReference
		if err := rows.Scan(&ref.Kind, &ref.ID, &ref.Title, &ref.ProjectID, &ref.Revision); err != nil {
			rows.Close()
			return "", err
		}
		if err := encoder.Encode(ref); err != nil {
			rows.Close()
			return "", err
		}
		if visit != nil {
			if err := visit(ref); err != nil {
				rows.Close()
				return "", err
			}
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", err
	}
	rows, err = st.tx.QueryContext(st.ctx, `SELECT id,digest FROM data_projects ORDER BY id`)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var id, digest string
		if err := rows.Scan(&id, &digest); err != nil {
			rows.Close()
			return "", err
		}
		if err := encoder.Encode([]string{id, digest}); err != nil {
			rows.Close()
			return "", err
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type sqlLibraryDeletionPlan struct {
	change                  LibraryDeletion
	stateRevision, revision string
	references              []LibraryReference
	total                   int
}

// authoringMu and libraryMu are held. Only the displayed reference window is
// retained. The temporary write set contains changed authoring files, not history.
func (a *app) prepareSQLLibraryDeletion(ctx context.Context, kind, id string, offset, limit int) (sqlLibraryDeletionPlan, error) {
	plan := sqlLibraryDeletionPlan{change: LibraryDeletion{Kind: kind, ItemID: id}, references: []LibraryReference{}}
	if kind == "skills" {
		if !validSkillID(id) {
			return plan, errors.New("Invalid skill ID.")
		}
		root, err := skillLibraryRoot()
		if err != nil {
			return plan, err
		}
		p, err := stableSkillPackage(root, id, false)
		if err != nil && p.Entry.Revision == "" {
			return plan, err
		}
		plan.change.Root, plan.change.ItemRevision = root, p.Entry.Revision
	} else if kind == "mcp" {
		a.mu.Lock()
		c, exists, err := a.mcpConnectionLocked(ctx, id)
		a.mu.Unlock()
		if err != nil {
			return plan, err
		}
		if !exists {
			return plan, errors.New("Connection not found.")
		}
		plan.change.ItemRevision = c.Revision
	} else {
		return plan, errors.New("Unknown library item kind.")
	}
	h := sha256.New()
	encoder := json.NewEncoder(h)
	_ = encoder.Encode(plan.change.ItemRevision)
	emit := func(ref LibraryReference) error {
		if plan.total >= offset && len(plan.references) < limit {
			plan.references = append(plan.references, ref)
		}
		plan.total++
		return encoder.Encode(ref)
	}
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		plan.stateRevision, err = st.libraryReferenceHash(kind, id, emit)
		return err
	})
	if err != nil {
		return plan, err
	}
	_ = encoder.Encode(plan.stateRevision)
	after := ""
	for {
		ids, err := sqliteQueryIDs(ctx, a.store.readers, `SELECT id FROM data_projects WHERE id>? ORDER BY id LIMIT 100`, after)
		if err != nil {
			return plan, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			p, err := a.store.readProjectMeta(ctx, id)
			if err != nil {
				return plan, err
			}
			catalog, err := loadAuthoring(p)
			if err != nil || len(catalog.Errors) > 0 {
				return plan, fmt.Errorf("Cannot inspect agent references in project %s; restore access and resolve authoring errors before deleting.", p.Name)
			}
			change := LibraryAuthoringChange{Project: p, Expected: map[string]string{}, Writes: map[string][]byte{}}
			for _, agent := range catalog.Agents {
				selection := CapabilitySelection{Skills: agent.Skills, MCPConnections: agent.MCPConnections}
				if !selectionReferences(selection, kind, plan.change.ItemID) {
					continue
				}
				if err := emit(LibraryReference{Kind: "agent", ID: agent.ID, Title: agent.Name, ProjectID: p.ID, Revision: agent.Revision}); err != nil {
					return plan, err
				}
				selection = removeLibraryReference(selection, kind, plan.change.ItemID)
				definition := agent.AgentDefinition
				definition.Skills, definition.MCPConnections = selection.Skills, selection.MCPConnections
				body, err := marshalExistingAgentDocument(p, agent.ID, definition)
				if err != nil {
					return plan, err
				}
				path := agentFile(agent.ID)
				change.Expected[path], change.Writes[path] = agent.Revision, body
			}
			if len(change.Writes) > 0 {
				plan.change.Projects = append(plan.change.Projects, change)
			}
			after = id
		}
	}
	plan.revision = hex.EncodeToString(h.Sum(nil))
	return plan, nil
}
func (a *app) previewSQLLibraryDeletion(w http.ResponseWriter, r *http.Request) {
	offset, err := sqlOffset(r)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	limit, err := sqlPageLimit(r, 50)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	plan, err := a.prepareSQLLibraryDeletion(r.Context(), r.PathValue("kind"), r.PathValue("itemID"), offset, limit)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	respond(w, 200, map[string]any{"revision": plan.revision, "itemRevision": plan.change.ItemRevision, "references": plan.references, "hasMore": offset+len(plan.references) < plan.total, "nextOffset": offset + len(plan.references), "total": plan.total})
}
func (a *app) deleteSQLLibraryItem(w http.ResponseWriter, r *http.Request) {
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
	plan, err := a.prepareSQLLibraryDeletion(r.Context(), r.PathValue("kind"), r.PathValue("itemID"), 0, 0)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	if plan.revision != body.Revision {
		fail(w, 409, "Item or references changed. Review the deletion preview again.")
		return
	}
	change := plan.change
	change.ID = newID()
	projects := change.Projects
	change.Projects = nil
	a.mu.Lock()
	err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		current, err := st.libraryReferenceHash(change.Kind, change.ItemID, nil)
		if err != nil {
			return err
		}
		if current != plan.stateRevision {
			return controlError(409, "Library references changed during preview. Review the deletion again.")
		}
		var pending bool
		if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return controlError(409, "A library deletion needs recovery before another deletion.")
		}
		if err := st.putRoot("library_deletions", change.ID, change); err != nil {
			return err
		}
		for _, project := range projects {
			key := sqliteOwnedKey(change.ID, project.Project.ID)
			if err := st.putRecord("library_authoring_changes", key, change.ID, project.Project.ID, -1, LibraryAuthoringChange{}); err != nil {
				return err
			}
			if err := st.putSingleChild("library_project_snapshot", key, project.Project); err != nil {
				return err
			}
			for _, path := range sortedGraphKeys(project.Writes) {
				if err := st.putRecord("library_authoring_preconditions", sqliteOwnedKey(key, path), key, path, -1, project.Expected[path]); err != nil {
					return err
				}
				if err := st.putRecord("library_authoring_writes", sqliteOwnedKey(key, path), key, path, -1, project.Writes[path]); err != nil {
					return err
				}
			}
		}
		return nil
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	if err := a.store.applySQLLibraryFiles(r.Context(), change); err != nil {
		fail(w, 409, "Deletion is pending recovery; no snapshots were removed. "+err.Error())
		return
	}
	a.mu.Lock()
	err = a.persistSQLGraphLocked(func(st *sqliteSessionTx) error { return st.finishLibraryDeletion(change) })
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"removed": true})
}
