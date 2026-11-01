package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Isolated CP1a storage proof: no real engine data, providers, models or servers.
func TestAgentCatalogCP1aStorageProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, upgrade := range []bool{false, true} {
		name := "fresh"
		if upgrade {
			name = "upgrade_v1"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "fixture.sqlite3")
			store, err := createSQLiteStore(ctx, path, "agent-catalog-fixture")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if store != nil {
					_ = store.close()
				}
			}()
			_, err = store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
				p := Project{ID: "fixture-project", Name: "Fixture", Folder: dir}
				if _, _, err := st.registerProject(p, false, ""); err != nil {
					return err
				}
				s := newTopLevelSession(p.ID, "Retained", "Ungrouped", "opencode", "fixture/model", "")
				s.ID = "fixture-session"
				if err := st.createSession(s, dir); err != nil {
					return err
				}
				e := event("user", "preserve this history exactly")
				if _, err := st.appendEvent(s.ID, e); err != nil {
					return err
				}
				return st.putRoot("session_spawns", "fixture-receipt", SessionSpawn{OperationID: "historical-operation", SessionID: s.ID, Title: "Historical receipt", Request: spawnArgs{Effort: "", Model: "fixture/model"}})
			})
			if err != nil {
				t.Fatal(err)
			}
			before := map[string]string{}
			for _, table := range []string{"sessions", "session_events", "session_spawns"} {
				var digest string
				if err := store.writer.QueryRowContext(ctx, "SELECT digest FROM data_"+table+" LIMIT 1").Scan(&digest); err != nil {
					t.Fatal(err)
				}
				before[table] = digest
			}
			if upgrade {
				// Reconstruct the v1 schema by removing exactly the four additive A
				// domains from an otherwise unchanged fixture. No old row is rebuilt.
				err = store.write(ctx, func(tx *sql.Tx) error {
					for _, table := range []string{"delegation_calls", "delegation_invocations", "delegation_children", "delegation_batches", "agent_bindings", "agent_catalog_changes", "agent_sources", "global_agents"} {
						if _, err := tx.ExecContext(ctx, "DROP TABLE data_"+table+"_chunks; DROP TABLE data_"+table); err != nil {
							return err
						}
					}
					if _, err := tx.ExecContext(ctx, `DROP TABLE event_content_appends; DROP TABLE event_content_publications; DROP TABLE event_content_generation_chunks; DROP TABLE event_content_generations`); err != nil {
						return err
					}
					_, err := tx.ExecContext(ctx, `PRAGMA user_version=1; UPDATE store_meta SET value='committed' WHERE key='cutover'`)
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.close(); err != nil {
					t.Fatal(err)
				}
				store = nil
				store, err = openSQLiteStore(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
			} else if _, err := store.writer.ExecContext(ctx, `UPDATE store_meta SET value='committed' WHERE key='cutover'`); err != nil {
				t.Fatal(err)
			}
			if err := store.enableRuntime(ctx); err != nil {
				t.Fatal(err)
			}
			if err := store.openReaders(ctx, path); err != nil {
				t.Fatal(err)
			}
			var version int
			if err := store.writer.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != sqliteSchemaVersion {
				t.Fatalf("schema=%d err=%v", version, err)
			}
			for table, wanted := range before {
				var digest string
				if err := store.writer.QueryRowContext(ctx, "SELECT digest FROM data_"+table+" LIMIT 1").Scan(&digest); err != nil || digest != wanted {
					t.Fatalf("%s changed across upgrade: %v", table, err)
				}
			}
			if err := store.readSnapshot(ctx, func(tx *sql.Tx) error {
				binding, err := (&sqliteSessionTx{ctx: ctx, tx: tx}).agentBinding("fixture-session")
				if binding != nil {
					t.Fatal("upgrade invented a binding")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			a := &app{dir: dir, ctx: ctx, store: store, sqlChanges: &sqliteChangeBus{}}
			definition := AgentDefinition{Name: "Storage specialist", Enabled: true, DefaultHarness: "opencode", Model: "fixture/model", Prompt: "A captured specialist."}
			if err := validateAgentDefinition(&definition); err != nil {
				t.Fatal(err)
			}
			a.authoringMu.Lock()
			created, err := a.mutateCatalogAgentLocked(ctx, Project{}, "", saveCatalogAgentBody{Agent: definition}, false)
			a.authoringMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			uid := created.Source.SourceUID
			definition.Name = "Renamed storage specialist"
			a.authoringMu.Lock()
			renamed, err := a.mutateCatalogAgentLocked(ctx, Project{}, created.ID, saveCatalogAgentBody{Agent: definition, Revision: created.Revision, SourceUID: uid}, false)
			a.authoringMu.Unlock()
			if err != nil || renamed.Source.SourceUID != uid {
				t.Fatalf("rename lost identity: %v", err)
			}
			// Persist an intent without finishing it, then exercise real recovery.
			change := AgentCatalogChange{ID: newID(), RemoveGlobal: uid, Sources: []AgentSourceRecord{{ID: agentSourceRecordID(renamed.Source, ""), Source: renamed.Source, Revision: renamed.Revision, State: "deleted"}}}
			if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.putRoot("agent_catalog_changes", change.ID, change) }); err != nil {
				t.Fatal(err)
			}
			if err := store.recoverAgentCatalogChanges(ctx); err != nil {
				t.Fatal(err)
			}
			catalog, err := a.agentCatalog(ctx, "")
			if err != nil || len(catalog.Agents) != 0 {
				t.Fatalf("deletion did not recover: %v", err)
			}
			a.authoringMu.Lock()
			recreated, err := a.mutateCatalogAgentLocked(ctx, Project{}, "", saveCatalogAgentBody{Agent: definition}, false)
			a.authoringMu.Unlock()
			if err != nil || recreated.Source.SourceUID == uid {
				t.Fatalf("recreated slug rebound old source: %v", err)
			}
			// Exercise the optional document wrapper and an observed local tombstone.
			// Restoring a deleted marker must stay conflicted across repeated reads.
			local := definition
			local.Name = "Local specialist"
			localBytes, err := toml.Marshal(agentDocument{AgentDefinition: local, SourceUID: newID()})
			if err != nil {
				t.Fatal(err)
			}
			localPath := filepath.Join(dir, ".klm", "agents", "local-specialist.toml")
			if err := os.MkdirAll(filepath.Dir(localPath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(localPath, localBytes, 0600); err != nil {
				t.Fatal(err)
			}
			if catalog, err := a.agentCatalog(ctx, "fixture-project"); err != nil || len(catalog.Agents) != 2 {
				t.Fatalf("local wrapper: %v", err)
			}
			if err := os.Remove(localPath); err != nil {
				t.Fatal(err)
			}
			if _, err := a.agentCatalog(ctx, "fixture-project"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(localPath, localBytes, 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				catalog, err := a.agentCatalog(ctx, "fixture-project")
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range catalog.Agents {
					if row.ID == "local-specialist" && (row.Available || row.Status != "conflict") {
						t.Fatal("restored marker resurrected a tombstoned source")
					}
				}
			}
			if err := store.check(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
	var patch AgentOverridePatch
	if err := json.Unmarshal([]byte(`{"skills":[],"mcpConnections":null,"model":""}`), &patch); err != nil {
		t.Fatal(err)
	}
	if !patch.Skills.Present || patch.Skills.Value == nil || !patch.MCPConnections.Present || patch.MCPConnections.Value != nil || patch.Effort.Present || patch.Model.Value == nil {
		t.Fatal("override decoder collapsed absence, reset or empty value")
	}
}

func TestAgentCatalogModelCacheScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := Session{Harness: "pi"} // Same empty project scope as global/general inventory.
	first, second, third := t.TempDir(), t.TempDir(), t.TempDir()
	keyA, err := modelCatalogCacheKey(s, first)
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := modelCatalogCacheKey(s, second)
	if err != nil {
		t.Fatal(err)
	}
	keyC, err := modelCatalogCacheKey(s, third)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{catalogs: map[string]catalogCache{
		keyA: {catalog: &ModelCatalog{DefaultModel: "first"}, expires: time.Now().Add(time.Minute)},
		keyB: {catalog: &ModelCatalog{DefaultModel: "second"}, expires: time.Now().Add(time.Minute)},
	}}
	done := make(chan struct{})
	close(done)
	a.catalogFlights = map[string]*catalogFlight{keyC: {done: done, catalog: &ModelCatalog{DefaultModel: "third"}}}
	for _, item := range []struct{ cwd, want string }{{first, "first"}, {second + string(os.PathSeparator) + ".", "second"}, {third, "third"}} {
		catalog, err := a.agentAuthoringModels(ctx, "", s.Harness, item.cwd, false)
		if err != nil || catalog.DefaultModel != item.want {
			t.Fatalf("CWD catalog mixed cache/flight: got %v, err %v", catalog, err)
		}
	}
}
