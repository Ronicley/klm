package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func delegationStorageFixture(t *testing.T) (*sqliteStore, context.Context, Project, Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.sqlite3")
	store, err := createSQLiteStore(ctx, path, "delegation-storage-fixture")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.close() })
	if _, err := store.writer.ExecContext(ctx, `UPDATE store_meta SET value='committed' WHERE key='cutover'`); err != nil {
		t.Fatal(err)
	}
	if err := store.enableRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.openReaders(ctx, path); err != nil {
		t.Fatal(err)
	}
	project := Project{ID: "project", Name: "Fixture", Folder: t.TempDir()}
	parent := newTopLevelSession(project.ID, "Parent", "Ungrouped", "opencode", "fixture/model", "")
	parent.ID = "parent"
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if _, _, err := st.registerProject(project, false, ""); err != nil {
			return err
		}
		return st.createSession(parent, dir)
	}); err != nil {
		t.Fatal(err)
	}
	return store, ctx, project, parent
}

func delegationStorageRecords(parent Session, cwd, suffix string) (Session, DelegationChildRecord, DelegationBatchRecord, DelegationInvocationRecord) {
	child := newTopLevelSession(parent.ProjectID, "Child", "Ungrouped", parent.Harness, "fixture/model", "")
	child.ID = "child-" + suffix
	child.ParentID = parent.ID
	child.Role = sessionRoleSubagent
	child.ExecutionCWD = cwd
	item := DelegationInvocationRecord{Version: 1, ID: "inv-" + suffix, BatchID: "batch-" + suffix, Key: "member", Ordinal: 0, ChildSessionID: child.ID, ParentSessionID: parent.ID, ParentTurnID: "epoch-" + suffix, State: DelegationInvocationAdmitted, ExecutionCWD: cwd, Task: "Fixture task", CreatedAt: now(), Capture: AgentExecutionCapture{Config: ResolvedAgentConfig{Harness: child.Harness, Model: child.Model, Selection: copySelection(CapabilitySelection{})}}}
	external := &CapabilitySnapshot{Version: 1, Adapter: libraryAdapterID(child.Harness), Selection: copySelection(CapabilitySelection{}), Skills: []SkillSnapshot{}, Connections: []MCPConnectionSnapshot{}, Diagnostics: []string{}}
	sealCapabilitySnapshot(external)
	item.Capture.ExternalCapabilities = external
	owned := DelegationChildRecord{Version: 1, ID: child.ID, ParentSessionID: parent.ID, Origin: "engine", Harness: child.Harness, ExecutionCWD: cwd, ActiveInvocationID: item.ID, CreatedAt: now()}
	batch := DelegationBatchRecord{Version: 1, ID: item.BatchID, ParentSessionID: parent.ID, OwnerConversationID: parent.ID, ParentTurnID: item.ParentTurnID, OperationID: "op-" + suffix, RequestHash: strings.Repeat("a", 64), Revision: "0", NativeCall: DelegationNativeCall{ParentTurnID: item.ParentTurnID, SessionID: "native-parent", TurnID: "native-turn", CallID: "0"}, State: DelegationAdmitted, ExpectedChildren: 1, CreatedAt: now(), UpdatedAt: now()}
	return child, owned, batch, item
}

func persistDelegationStorageFixture(st *sqliteSessionTx, dir string, child Session, owned DelegationChildRecord, batch DelegationBatchRecord, item DelegationInvocationRecord) error {
	if err := st.createSession(child, dir); err != nil {
		return err
	}
	if err := st.putDelegationChild(owned); err != nil {
		return err
	}
	if _, err := st.saveDelegationBatch(batch, ""); err != nil {
		return err
	}
	return st.putDelegationInvocation(item)
}

func TestDelegationStorageSchemaUpgradeAndManifest(t *testing.T) {
	for _, version := range []int{1, 2, 3, 4} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			store, ctx, project, parent := delegationStorageFixture(t)
			// Seed genuine retained binding/native metadata before reconstructing the
			// requested old schema. No legacy child is inferred into managed records.
			legacy := newTopLevelSession(project.ID, "Legacy child", "Ungrouped", "opencode", "fixture/model", "")
			legacy.ID = "legacy-child"
			legacy.Role = sessionRoleSubagent
			legacy.ParentID = parent.ID
			oldCodex := newTopLevelSession(project.ID, "Legacy Codex", "Ungrouped", "codex", "fixture/model", "")
			oldCodex.ID = "codex-legacy"
			if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
				if err := st.createSession(legacy, project.Folder); err != nil {
					return err
				}
				if err := st.createSession(oldCodex, project.Folder); err != nil {
					return err
				}
				if err := st.putRoot("native_sessions", oldCodex.ID, nativeSession{ID: "old-codex-thread"}); err != nil {
					return err
				}
				if err := st.putRoot("native_sessions", parent.ID, nativeSession{ID: "retained-native"}); err != nil {
					return err
				}
				if version >= 3 {
					source := AgentSourceRef{SourceUID: newID(), ID: "deleted-specialist", Scope: "global"}
					config := ResolvedAgentConfig{Harness: parent.Harness, Model: parent.Model, Prompt: "Retained capture", Selection: copySelection(CapabilitySelection{})}
					binding := &SessionAgentBinding{Source: source, Revision: "1", LastCaptured: AgentCapture{Source: source, Revision: strings.Repeat("a", 64), Config: config}, Desired: config, SourceStatus: "deleted", InheritedCapabilities: copySelection(CapabilitySelection{})}
					if err := st.putAgentBinding(parent.ID, binding); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before := map[string]string{}
			tables := []string{"sessions", "native_sessions"}
			if version >= 3 {
				tables = append(tables, "agent_bindings")
			}
			for _, table := range tables {
				var value string
				if err := store.writer.QueryRowContext(ctx, "SELECT digest FROM data_"+table+" ORDER BY id LIMIT 1").Scan(&value); err != nil {
					t.Fatal(err)
				}
				before[table] = value
			}
			if version < 4 {
				if err := store.write(ctx, func(tx *sql.Tx) error {
					for _, table := range []string{"delegation_calls", "delegation_invocations", "delegation_children", "delegation_batches"} {
						if _, err := tx.ExecContext(ctx, "DROP TABLE data_"+table+"_chunks; DROP TABLE data_"+table); err != nil {
							return err
						}
					}
					if version < 3 {
						for _, table := range []string{"agent_bindings", "agent_catalog_changes", "agent_sources", "global_agents"} {
							if _, err := tx.ExecContext(ctx, "DROP TABLE data_"+table+"_chunks; DROP TABLE data_"+table); err != nil {
								return err
							}
						}
					}
					if version == 1 {
						if _, err := tx.ExecContext(ctx, `DROP TABLE event_content_appends; DROP TABLE event_content_publications; DROP TABLE event_content_generation_chunks; DROP TABLE event_content_generations`); err != nil {
							return err
						}
					}
					_, err := tx.ExecContext(ctx, "PRAGMA user_version="+string(rune('0'+version)))
					return err
				}); err != nil {
					t.Fatal(err)
				}
				var sequence int
				var databaseName, path string
				if err := store.writer.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&sequence, &databaseName, &path); err != nil {
					t.Fatal(err)
				}
				if err := store.close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := openSQLiteStore(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = reopened.close() })
				store = reopened
				if err := store.enableRuntime(ctx); err != nil {
					t.Fatal(err)
				}
				if err := store.openReaders(ctx, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.upgradeAgentSchema(ctx); err != nil {
				t.Fatal(err)
			}
			if err := store.upgradeDelegationSchema(ctx); err != nil {
				t.Fatal(err)
			}
			var current int
			if err := store.writer.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil || current != sqliteSchemaVersion {
				t.Fatal("upgrade failed", current, err)
			}
			for table, digest := range before {
				var actual string
				if err := store.writer.QueryRowContext(ctx, "SELECT digest FROM data_"+table+" ORDER BY id LIMIT 1").Scan(&actual); err != nil || actual != digest {
					t.Fatal("upgrade rewrote retained metadata", table, err)
				}
			}
			for _, table := range []string{"delegation_batches", "delegation_calls", "delegation_invocations", "delegation_children"} {
				var n int
				if err := store.writer.QueryRowContext(ctx, "SELECT count(*) FROM data_"+table).Scan(&n); err != nil || n != 0 {
					t.Fatal("upgrade backfilled managed children/work", err)
				}
			}
			if err := store.readSnapshot(ctx, func(tx *sql.Tx) error {
				var native nativeSession
				if err := decodeSQLiteRecord(ctx, tx, "native_sessions", parent.ID, 64<<10, &native); err != nil {
					return err
				}
				if native.Delegation != nil {
					t.Fatal("legacy native history gained a manifest")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			codex := newTopLevelSession(project.ID, "Codex parent", "Ungrouped", "codex", "fixture/model", "")
			codex.ID = "codex-parent"
			manifest, err := delegationCodexManifestV1()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
				if err := st.createSession(codex, project.Folder); err != nil {
					return err
				}
				return st.putNativeDelegationMetadata(codex.ID, nativeSession{ID: "new-thread", Delegation: &manifest})
			}); err != nil {
				t.Fatal(err)
			}
			if err := store.readSnapshot(ctx, func(tx *sql.Tx) error {
				var value nativeSession
				if err := decodeSQLiteRecord(ctx, tx, "native_sessions", codex.ID, 64<<10, &value); err != nil {
					return err
				}
				if value.Delegation == nil || *value.Delegation != manifest {
					t.Fatal("optional manifest did not roundtrip")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
				return st.putNativeDelegationMetadata(codex.ID, nativeSession{ID: "new-thread"})
			}); err == nil {
				t.Fatal("existing native writer dropped instrumented manifest")
			}
			if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
				return st.putNativeDelegationMetadata(oldCodex.ID, nativeSession{ID: "old-codex-thread", Delegation: &manifest})
			}); err == nil {
				t.Fatal("existing legacy Codex thread adopted a manifest")
			}
			if err := store.check(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDelegationStorageOwnershipLeasesAndAliases(t *testing.T) {
	store, ctx, project, parent := delegationStorageFixture(t)
	child, owned, batch, item := delegationStorageRecords(parent, project.Folder, "one")
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		return persistDelegationStorageFixture(st, project.Folder, child, owned, batch, item)
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.readSnapshot(ctx, func(tx *sql.Tx) error { v := sqliteValidation{ctx: ctx, tx: tx}; return v.validateDelegationRecords() }); err != nil {
		t.Fatal(err)
	}
	var state, status string
	if err := store.readers.QueryRowContext(ctx, `SELECT x_state,status FROM data_delegation_batches WHERE id=?`, batch.ID).Scan(&state, &status); err != nil || state != "admitted" || status != "" {
		t.Fatal("delegation state used the generic status projection", err)
	}
	var callKey string
	if err := store.readers.QueryRowContext(ctx, `SELECT id FROM data_delegation_calls WHERE owner_id=? AND x_callId='0'`, batch.ID).Scan(&callKey); err != nil {
		t.Fatal("numeric zero native alias lost", err)
	}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		raw, _ := json.Marshal(batch.NativeCall)
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		value["delivery"] = "pending"
		if err := st.putExistingRecord("delegation_calls", callKey, value); err != nil {
			return err
		}
		value["delivery"] = "sending"
		if err := st.putExistingRecord("delegation_calls", callKey, value); err != nil {
			return err
		}
		value["delivery"] = "delivered"
		return st.putExistingRecord("delegation_calls", callKey, value)
	}); err != nil {
		t.Fatal("mutable alias Delivery was treated as identity", err)
	}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		call := batch.NativeCall
		call.CallID = "changed"
		return st.putExistingRecord("delegation_calls", callKey, call)
	}); err == nil {
		t.Fatal("native alias identity mutated")
	}
	for _, test := range []string{"active-parent", "child-lease", "gap", "foreign-cwd", "foreign-new-cwd", "leaf-parent", "own-selector"} {
		t.Run(test, func(t *testing.T) {
			_, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
				nextChild, nextOwned, nextBatch, nextItem := delegationStorageRecords(parent, project.Folder, "bad-"+test)
				switch test {
				case "active-parent":
					return persistDelegationStorageFixture(st, project.Folder, nextChild, nextOwned, nextBatch, nextItem)
				case "child-lease":
					nextItem.ID = "second-active"
					nextItem.BatchID = batch.ID
					nextItem.Key = "second"
					nextItem.Ordinal = 1
					nextItem.ChildSessionID = child.ID
					return st.putDelegationInvocation(nextItem)
				case "gap", "foreign-new-cwd":
					other := parent
					other.ID = "other-" + test
					if err := st.createSession(other, project.Folder); err != nil {
						return err
					}
					nextChild.ParentID = other.ID
					nextOwned.ParentSessionID = other.ID
					nextBatch.ParentSessionID = other.ID
					nextBatch.OwnerConversationID = other.ID
					nextItem.ParentSessionID = other.ID
					if test == "gap" {
						nextItem.Ordinal = 2
					} else {
						directory := t.TempDir()
						nextChild.ExecutionCWD = directory
						nextOwned.ExecutionCWD = directory
						nextItem.ExecutionCWD = directory
					}
					return persistDelegationStorageFixture(st, project.Folder, nextChild, nextOwned, nextBatch, nextItem)
				case "foreign-cwd":
					nextOwned = owned
					nextOwned.ExecutionCWD = t.TempDir()
					return st.putDelegationChild(nextOwned)
				case "leaf-parent":
					nextChild.ParentID = child.ID
					nextOwned.ParentSessionID = child.ID
					nextBatch.ParentSessionID = child.ID
					nextBatch.OwnerConversationID = child.ID
					nextItem.ParentSessionID = child.ID
					return persistDelegationStorageFixture(st, project.Folder, nextChild, nextOwned, nextBatch, nextItem)
				case "own-selector":
					bad, err := st.sessionMeta(child.ID)
					if err != nil {
						return err
					}
					bad.SelectedGraphID = "forbidden"
					return st.putSessionMeta(bad)
				}
				return nil
			})
			if err == nil {
				t.Fatal("malformed ownership/lease was committed")
			}
			if test == "child-lease" && !strings.Contains(err.Error(), "x_childSessionId") {
				t.Fatal("active-child SQL uniqueness did not reject conflict", err)
			}
			if test == "gap" && !strings.Contains(err.Error(), "contiguous") {
				t.Fatal("contiguous membership validator did not reject gap", err)
			}
		})
	}
	if err := store.runDelegationRecoveryBarrier(ctx, nil); err == nil {
		t.Fatal("unwired recovery silently accepted unfinished batches")
	}
	page, err := store.delegationRecoveryPage(ctx, "", 10)
	if err != nil || len(page) != 1 {
		t.Fatal("bounded recovery seam missing pending batch", err)
	}
}

func TestDelegationStorageSideGeneralAndGraphOwnership(t *testing.T) {
	for _, role := range []string{sessionRoleSideAgent, sessionRoleGeneralAgent, "graph_node"} {
		t.Run(role, func(t *testing.T) {
			store, ctx, project, main := delegationStorageFixture(t)
			parent := newTopLevelSession(project.ID, "Owning parent", "Ungrouped", "opencode", "fixture/model", "")
			parent.ID = "special-parent"
			parent.Role = role
			directory := project.Folder
			if role == sessionRoleSideAgent {
				parent.ParentID = main.ID
			}
			if role == sessionRoleGeneralAgent {
				parent.ProjectID = ""
				parent.ExecutionCWD = t.TempDir()
				directory = parent.ExecutionCWD
			}
			if role == "graph_node" {
				parent.GraphRunID = "graph-run"
				parent.GraphNodeID = "node"
				parent.ExecutionCWD = t.TempDir()
				directory = parent.ExecutionCWD
			}
			child, owned, batch, item := delegationStorageRecords(parent, directory, role)
			var scope *DelegationGraphScope
			if role == "graph_node" {
				scope = &DelegationGraphScope{RunID: parent.GraphRunID, NodeID: parent.GraphNodeID, ActivationID: "activation", TurnID: batch.ParentTurnID, WorkspaceID: "workspace", SnapshotDigest: strings.Repeat("b", 64)}
				owned.GraphRunID = scope.RunID
				owned.WorkspaceID = scope.WorkspaceID
				batch.Graph = scope
				item.Graph = scope
				batch.OwnerConversationID = main.ID
			}
			if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
				if err := st.createSession(parent, project.Folder); err != nil {
					return err
				}
				if scope != nil {
					run := GraphRun{ID: scope.RunID, ProjectID: project.ID, ConversationID: main.ID, GraphID: "fixture", Status: "running", CreatedAt: now(), UpdatedAt: now()}
					if err := st.putRoot("graph_runs", run.ID, run); err != nil {
						return err
					}
					if err := st.putSingleChild("graph_snapshots", run.ID, GraphSnapshot{GraphID: "fixture", Revision: scope.SnapshotDigest}); err != nil {
						return err
					}
					workspace := WorkspaceRecord{ID: scope.WorkspaceID, ProjectID: project.ID, Directory: directory, CreatedByRunID: run.ID, OperationID: "workspace-op", Status: "ready", CreatedAt: now()}
					if err := st.putRoot("graph_workspaces", workspace.ID, workspace); err != nil {
						return err
					}
					if err := st.putRoot("graph_workspace_uses", "workspace-use", WorkspaceUse{ID: "workspace-use", WorkspaceID: workspace.ID, RunID: run.ID, ActivationID: scope.ActivationID, Association: "initial"}); err != nil {
						return err
					}
					activation := GraphActivation{ID: scope.ActivationID, RunID: run.ID, NodeID: scope.NodeID, SessionID: parent.ID, WorkspaceID: workspace.ID, CurrentTurnID: scope.TurnID, Status: "running", CreatedAt: now(), UpdatedAt: now()}
					if err := st.putRoot("graph_activations", activation.ID, activation); err != nil {
						return err
					}
				}
				return persistDelegationStorageFixture(st, project.Folder, child, owned, batch, item)
			}); err != nil {
				t.Fatal("valid exact managed ownership rejected", err)
			}
			if err := store.write(ctx, func(tx *sql.Tx) error {
				v := sqliteValidation{ctx: ctx, tx: tx}
				if err := v.validateSessionsAndTopics(); err != nil {
					return err
				}
				return v.validateDelegationRecords()
			}); err != nil {
				t.Fatal(err)
			}
			if scope != nil {
				_, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
					second := parent
					second.ID = "wrong-node-session"
					if err := st.createSession(second, project.Folder); err != nil {
						return err
					}
					c, l, b, i := delegationStorageRecords(second, directory, "wrong-activation")
					l.GraphRunID = scope.RunID
					l.WorkspaceID = scope.WorkspaceID
					b.Graph = scope
					b.OwnerConversationID = main.ID
					i.Graph = scope
					return persistDelegationStorageFixture(st, project.Folder, c, l, b, i)
				})
				if err == nil {
					t.Fatal("graph ledger accepted another node session's activation")
				}
				_, err = store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
					legacy := child
					legacy.ID = "unmanaged-graph-child"
					if err := st.createSession(legacy, project.Folder); err != nil {
						return err
					}
					return (sqliteValidation{ctx: ctx, tx: st.tx}).validateSessionsAndTopics()
				})
				if err == nil {
					t.Fatal("legacy ParentID rules were universally relaxed for graph children")
				}
			}
		})
	}
}
