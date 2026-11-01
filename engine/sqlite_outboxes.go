package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (a *app) commitSQLGraphAuthoring(ctx context.Context, p Project, writes map[string][]byte, oldID, newGraphID string) error {
	change := GraphCatalogChange{ID: newID(), ProjectID: p.ID, OldID: oldID, NewID: newGraphID}
	a.mu.Lock()
	err := a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("graph_catalog_changes", change.ID, change); err != nil {
			return err
		}
		for _, path := range sortedGraphKeys(writes) {
			if err := st.putRecord("graph_catalog_writes", sqliteOwnedKey(change.ID, path), change.ID, path, -1, writes[path]); err != nil {
				return err
			}
		}
		return nil
	})
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if err := commitAuthoring(p, writes); err != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.failStorageLocked(fmt.Errorf("graph catalog roll-forward requires restart: %w", err))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error { return st.finishGraphCatalog(change) })
}

func (st *sqliteSessionTx) finishGraphCatalog(c GraphCatalogChange) error {
	stamp := now()
	if err := st.eachMatching("sessions", `project_id=? AND x_selectedGraphId=?`, []any{c.ProjectID, c.OldID}, func(id string) error {
		s, err := st.sessionMeta(id)
		if err != nil {
			return err
		}
		s.SelectedGraphID, s.UpdatedAt = c.NewID, stamp
		return st.putSessionMeta(s)
	}); err != nil {
		return err
	}
	if err := st.eachMatching("graph_runs", `project_id=? AND (x_graphId=? OR x_catalogGraphId=?)`, []any{c.ProjectID, c.OldID, c.OldID}, func(id string) error {
		var r GraphRun
		if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_runs", id, 4<<20, &r); err != nil {
			return err
		}
		current := r.GraphID
		if r.CatalogGraphID != nil {
			current = *r.CatalogGraphID
		}
		if current != c.OldID {
			return nil
		}
		alias := c.NewID
		r.CatalogGraphID = &alias
		return st.putRoot("graph_runs", id, r)
	}); err != nil {
		return err
	}
	if c.NewID != "" {
		if err := st.eachMatching("graph_activities", `project_id=? AND x_graphId=?`, []any{c.ProjectID, c.OldID}, func(id string) error {
			var activity GraphActivity
			if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_activities", id, 4<<20, &activity); err != nil {
				return err
			}
			if activity.Version == ^uint64(0) {
				return errors.New("activity revision exhausted")
			}
			activity.GraphID, activity.UpdatedAt = c.NewID, stamp
			activity.Version++
			return st.putRoot("graph_activities", id, activity)
		}); err != nil {
			return err
		}
	}
	return st.deleteRecord("graph_catalog_changes", c.ID)
}

func (s *sqliteStore) recoverSQLGraphCatalog(ctx context.Context) error {
	for {
		var id string
		err := s.readers.QueryRowContext(ctx, `SELECT id FROM data_graph_catalog_changes ORDER BY position LIMIT 1`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var c GraphCatalogChange
		var p Project
		if err := s.readSnapshot(ctx, func(tx *sql.Tx) error {
			if err := decodeSQLiteRecord(ctx, tx, "graph_catalog_changes", id, 1<<20, &c); err != nil {
				return err
			}
			if err := decodeSQLiteRecord(ctx, tx, "projects", c.ProjectID, 1<<20, &p); err != nil {
				return err
			}
			c.Writes = map[string][]byte{}
			v := sqliteValidation{ctx: ctx, tx: tx}
			return v.each("graph_catalog_writes", &id, func(key string) error {
				var path string
				if err := tx.QueryRowContext(ctx, `SELECT map_key FROM data_graph_catalog_writes WHERE id=?`, key).Scan(&path); err != nil {
					return err
				}
				var body []byte
				if err := decodeSQLiteRecord(ctx, tx, "graph_catalog_writes", key, maxStreamRecordBytes, &body); err != nil {
					return err
				}
				c.Writes[path] = body
				return nil
			})
		}); err != nil {
			return err
		}
		// A graph change owns at most the four validated old/new graph/layout paths.
		if err := commitAuthoring(p, c.Writes); err != nil {
			return err
		}
		if _, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.finishGraphCatalog(c) }); err != nil {
			return err
		}
	}
}

func (s *sqliteStore) readOutboxChild(ctx context.Context, table, id string, target any) error {
	return s.readSnapshot(ctx, func(tx *sql.Tx) error { return decodeSQLiteRecord(ctx, tx, table, id, maxStreamRecordBytes, target) })
}

func (s *sqliteStore) applySQLLibraryFiles(ctx context.Context, change LibraryDeletion) error {
	after := ""
	for {
		ids, err := sqliteQueryIDs(ctx, s.readers, `SELECT id FROM data_library_authoring_changes WHERE owner_id=? AND id>? ORDER BY id LIMIT 100`, change.ID, after)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			var project Project
			if err := s.readOutboxChild(ctx, "library_project_snapshot", sqliteOwnedKey(id, ""), &project); err != nil {
				return err
			}
			if err := recoverAuthoring(project); err != nil {
				return err
			}
			for _, table := range []string{"library_authoring_preconditions", "library_authoring_writes"} {
				cursor := ""
				for {
					keys, err := sqliteQueryIDs(ctx, s.readers, `SELECT id FROM data_`+table+` WHERE owner_id=? AND id>? ORDER BY id LIMIT 100`, id, cursor)
					if err != nil {
						return err
					}
					if len(keys) == 0 {
						break
					}
					for _, key := range keys {
						var path string
						if err := s.readers.QueryRowContext(ctx, `SELECT map_key FROM data_`+table+` WHERE id=?`, key).Scan(&path); err != nil {
							return err
						}
						var intended []byte
						writeKey := sqliteOwnedKey(id, path)
						err := s.readOutboxChild(ctx, "library_authoring_writes", writeKey, &intended)
						if err != nil && !errors.Is(err, sql.ErrNoRows) {
							return err
						}
						if table == "library_authoring_preconditions" {
							var expected string
							if err := s.readOutboxChild(ctx, table, key, &expected); err != nil {
								return err
							}
							current, err := readConfig(project, path)
							if err != nil || revision(current) != expected && revision(current) != revision(intended) {
								return fmt.Errorf("Agent file %s changed externally; restore the expected revision before recovery.", path)
							}
						} else if err := commitAuthoring(project, map[string][]byte{path: intended}); err != nil {
							return err
						}
						cursor = key
					}
				}
			}
			after = id
		}
	}
	// Reuse the existing idempotent skill-root/revision/trash safeguards only after
	// authoring files have rolled forward. No SQL transaction spans filesystem I/O.
	change.Projects = nil
	return applyLibraryDeletionFiles(change)
}

func (st *sqliteSessionTx) finishLibraryDeletion(c LibraryDeletion) error {
	if err := st.removeAgentLibraryReferences(c.Kind, c.ItemID); err != nil {
		return err
	}
	for {
		ids, err := sqliteQueryIDs(st.ctx, st.tx, `SELECT record_key FROM selection_references WHERE domain='automations' AND kind=? AND item=? LIMIT 100`, c.Kind, c.ItemID)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			var a Automation
			if err := decodeSQLiteRecord(st.ctx, st.tx, "automations", id, 8<<20, &a); err != nil {
				return err
			}
			if a.Revision == ^uint64(0) {
				return errors.New("automation revision exhausted")
			}
			a.Selection = removeLibraryReference(a.Selection, c.Kind, c.ItemID)
			a.Revision++
			if err := st.putRoot("automations", id, a); err != nil {
				return err
			}
		}
	}
	for {
		ids, err := sqliteQueryIDs(st.ctx, st.tx, `SELECT record_key FROM selection_references WHERE domain='capability_selections' AND kind=? AND item=? LIMIT 100`, c.Kind, c.ItemID)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			var selection CapabilitySelection
			if err := decodeSQLiteRecord(st.ctx, st.tx, "capability_selections", id, 8<<20, &selection); err != nil {
				return err
			}
			selection = removeLibraryReference(selection, c.Kind, c.ItemID)
			if err := st.putExistingRecord("capability_selections", id, selection); err != nil {
				return err
			}
			var capID, sessionID string
			if err := st.tx.QueryRowContext(st.ctx, `SELECT c.id,c.owner_id FROM data_session_capabilities c JOIN data_capability_selections d ON d.owner_id=c.id WHERE d.id=?`, id).Scan(&capID, &sessionID); err != nil {
				return err
			}
			var caps ConversationCapabilities
			if err := decodeSQLiteRecord(st.ctx, st.tx, "session_capabilities", capID, 64<<10, &caps); err != nil {
				return err
			}
			if caps.Revision == ^uint64(0) {
				return errors.New("capability revision exhausted")
			}
			caps.Revision++
			if err := st.putExistingRecord("session_capabilities", capID, caps); err != nil {
				return err
			}
			s, err := st.sessionMeta(sessionID)
			if err != nil {
				return err
			}
			s.UpdatedAt = now()
			if err := st.putSessionMeta(s); err != nil {
				return err
			}
		}
	}
	if c.Kind == "mcp" {
		if err := st.deleteRecord("mcp_connections", c.ItemID); err != nil {
			return err
		}
	}
	if err := st.deleteRecord("library_deletions", c.ID); err != nil {
		return err
	}
	return st.pruneMCPCredentials()
}

func (st *sqliteSessionTx) pruneMCPCredentials() error {
	return st.eachMatching("mcp_credentials", `NOT EXISTS(SELECT 1 FROM credential_references r WHERE r.credential=data_mcp_credentials.id AND (
 r.domain='mcp_connections'
 OR r.domain='automation_runs' AND EXISTS(SELECT 1 FROM data_automation_runs a WHERE a.id=r.record_key AND a.status='running')
 OR r.domain='graph_snapshots' AND EXISTS(SELECT 1 FROM data_graph_snapshots p JOIN data_graph_runs g ON g.id=p.owner_id WHERE p.id=r.record_key AND g.status IN ('starting','running','ending'))
 OR r.domain='capability_applied' AND EXISTS(SELECT 1 FROM data_capability_applied p JOIN data_session_capabilities c ON c.id=p.owner_id JOIN data_sessions s ON s.id=c.owner_id WHERE p.id=r.record_key AND s.status='running')
	 OR r.domain='capability_preparing' AND EXISTS(SELECT 1 FROM data_capability_preparing p JOIN data_session_capabilities c ON c.id=p.owner_id JOIN data_sessions s ON s.id=c.owner_id WHERE p.id=r.record_key AND s.status='running')
	 OR r.domain='delegation_invocations' AND EXISTS(SELECT 1 FROM data_delegation_invocations i WHERE i.id=r.record_key AND (i.x_state<>'terminal' OR EXISTS(SELECT 1 FROM data_delegation_children c WHERE c.x_activeInvocationId=i.id AND c.x_quarantined='true')))
))`, nil, func(id string) error { return st.deleteRecord("mcp_credentials", id) })
}

func (s *sqliteStore) recoverSQLLibrary(ctx context.Context, publish ...func(sqliteSessionChanges)) error {
	for {
		var id string
		err := s.readers.QueryRowContext(ctx, `SELECT id FROM data_library_deletions ORDER BY position LIMIT 1`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var c LibraryDeletion
		if err := s.readOutboxChild(ctx, "library_deletions", id, &c); err != nil {
			return err
		}
		if err := s.applySQLLibraryFiles(ctx, c); err != nil {
			return err
		}
		changes, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.finishLibraryDeletion(c) })
		if err != nil {
			return err
		}
		if len(publish) > 0 && publish[0] != nil {
			publish[0](changes)
		}
	}
}
