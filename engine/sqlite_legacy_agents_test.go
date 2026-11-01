package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func legacyAgentSnapshotFixture() *AgentRecord {
	return &AgentRecord{ID: "planner", Revision: "original-revision", AgentDefinition: AgentDefinition{
		Name: "Planner", Enabled: true, DefaultHarness: "pi", Model: "old-model", Effort: "high", Prompt: "  Original prompt.\r\nKeep exact bytes.\n",
		Skills: []string{"old-agent-skill"},
	}}
}

func legacyAgentBindingDigest(t *testing.T, store *sqliteStore, table, id string) string {
	t.Helper()
	var digest string
	if err := store.writer.QueryRow(`SELECT digest FROM data_`+table+` WHERE id=?`, id).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestSQLiteLegacyAgentBindings(t *testing.T) {
	store, ctx, project, parent := delegationStorageFixture(t)
	parent.Events, parent.Queue, parent.Sources, parent.Permissions, parent.Questions, parent.Usage = nil, nil, nil, nil, nil, nil
	selection := CapabilitySelection{Skills: []string{"session-skill"}, MCPConnections: []MCPSelection{{ConnectionID: strings.Repeat("a", 32), Tools: []string{"read"}}}}
	parent.SelectedAgent, parent.AgentSelectionLocked, parent.Effort = legacyAgentSnapshotFixture(), true, "low"
	parent.ResolvedModel, parent.ResolvedEffort, parent.HarnessLocked, parent.YOLO = "resolved-model", "low", true, true
	parent.Capabilities = ConversationCapabilities{Revision: 7, Desired: selection}
	existing := newTopLevelSession(project.ID, "Already bound", "Ungrouped", "opencode", "fixture/model", "")
	existing.ID, existing.SelectedAgent = "already-bound", legacyAgentSnapshotFixture()
	res := AgentResolution{Capture: AgentCapture{Name: "Current", Source: AgentSourceRef{SourceUID: "existing-source-uid", ID: "current", Scope: "project", ProjectID: project.ID}, Revision: "current-revision"}, SourceStatus: "linked"}
	currentBinding := bindingForResolution(res, AgentConfigOverrides{}, CapabilitySelection{})
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		// Model an existing store that predates this data migration.
		if _, err := st.tx.ExecContext(ctx, `DELETE FROM store_meta WHERE key='legacy_agent_binding_version'`); err != nil {
			return err
		}
		caps := parent.Capabilities
		parent.Capabilities = ConversationCapabilities{}
		if err := st.putSessionMeta(parent); err != nil {
			return err
		}
		if err := st.putSingleChild("session_capabilities", parent.ID, ConversationCapabilities{Revision: caps.Revision}); err != nil {
			return err
		}
		if err := st.putSingleChild("capability_selections", sqliteOwnedKey(parent.ID, ""), selection); err != nil {
			return err
		}
		if _, err := st.appendEvent(parent.ID, Event{ID: "history", Type: "assistant", Text: "Retain history", CreatedAt: now()}); err != nil {
			return err
		}
		if err := st.putRoot("native_sessions", parent.ID, nativeSession{ID: "retained-native", OpenCodeVersion: "v1"}); err != nil {
			return err
		}
		if err := st.createSession(existing, store.dir); err != nil {
			return err
		}
		return st.putAgentBinding(existing.ID, currentBinding)
	}); err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{"native_sessions": parent.ID, "session_events": sqliteOwnedKey(parent.ID, "0"), "session_capabilities": sqliteOwnedKey(parent.ID, ""), "capability_selections": sqliteOwnedKey(sqliteOwnedKey(parent.ID, ""), ""), "agent_bindings": sqliteOwnedKey(existing.ID, ""), "sessions": existing.ID}
	digests := map[string]string{}
	for table, id := range keys {
		digests[table] = legacyAgentBindingDigest(t, store, table, id)
	}
	if err := store.enableRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	meta, err := store.readSessionMeta(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := parent
	want.AgentConfig = meta.AgentConfig
	if meta.AgentConfig == nil || !reflect.DeepEqual(meta, want) {
		t.Fatal("migration changed original session settings/snapshot", meta)
	}
	var binding *SessionAgentBinding
	if err := store.readSnapshot(ctx, func(tx *sql.Tx) error {
		var err error
		binding, err = (&sqliteSessionTx{ctx: ctx, tx: tx}).agentBinding(parent.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if binding == nil || !binding.LegacySnapshot || binding.SourceStatus != "legacy_snapshot" || binding.LastCaptured.Config.Prompt != parent.SelectedAgent.Prompt || binding.Applied != nil || binding.Desired.Model != parent.Model || binding.Desired.Effort != parent.Effort || !reflect.DeepEqual(binding.Desired.Selection, selection) {
		t.Fatalf("legacy configuration was not preserved: %+v", binding)
	}
	// Even an explicit marker claiming the synthetic UID must not replace history.
	data := []byte("source_uid = \"" + binding.Source.SourceUID + "\"\nname = \"Planner\"\nenabled = true\ndefault_harness = \"opencode\"\nmodel = \"replacement\"\nprompt = \"Replacement prompt\"\n")
	if err := writeConfig(project, agentFile("planner"), data); err != nil {
		t.Fatal(err)
	}
	key, err := modelCatalogCacheKey(parent, project.Folder)
	if err != nil {
		t.Fatal(err)
	}
	a := &app{ctx: ctx, store: store, binaries: map[string]binary{"opencode": {path: filepath.Join(t.TempDir(), "never-executed.exe")}}, catalogs: map[string]catalogCache{key: {catalog: &ModelCatalog{Models: []ModelOption{{ID: parent.Model, Efforts: []string{"low", "high"}}}}, expires: time.Now().Add(time.Hour)}}}
	if prompt, err := a.chatAgentPrompt(meta); err != nil || prompt != "" {
		t.Fatal("SQL execution duplicated legacy prompt lookup", prompt, err)
	}
	resolved, err := a.resolveBoundAgent(ctx, BoundAgentRequest{Binding: *binding, FixedHarness: parent.Harness, ProjectID: project.ID, ExecutionCWD: project.Folder})
	if err != nil || resolved.Blocked != nil || !reflect.DeepEqual(resolved.Config, binding.Desired) || resolved.SourceStatus != "legacy_snapshot" {
		t.Fatalf("snapshot followed replacement or failed resolution: %+v %v", resolved, err)
	}
	delete(a.binaries, "opencode")
	blocked, err := a.resolveBoundAgent(ctx, BoundAgentRequest{Binding: *binding, FixedHarness: parent.Harness, ProjectID: project.ID, ExecutionCWD: project.Folder})
	if err != nil || blocked.Blocked == nil || blocked.Blocked.Code != "harness_unavailable" {
		t.Fatal("snapshot bypassed normal harness validation", blocked, err)
	}
	before := legacyAgentBindingDigest(t, store, "agent_bindings", sqliteOwnedKey(parent.ID, ""))
	if err := store.enableRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	if before != legacyAgentBindingDigest(t, store, "agent_bindings", sqliteOwnedKey(parent.ID, "")) {
		t.Fatal("repeat startup changed binding")
	}
	for table, id := range keys {
		if digests[table] != legacyAgentBindingDigest(t, store, table, id) {
			t.Fatal("migration changed existing record", table, id)
		}
	}
}

func TestSQLiteLegacyAgentBindingRollback(t *testing.T) {
	store, ctx, _, parent := delegationStorageFixture(t)
	parent.Events, parent.Queue, parent.Sources, parent.Permissions, parent.Questions, parent.Usage = nil, nil, nil, nil, nil, nil
	parent.Capabilities = ConversationCapabilities{}
	parent.SelectedAgent = legacyAgentSnapshotFixture()
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if _, err := st.tx.ExecContext(ctx, `DELETE FROM store_meta WHERE key='legacy_agent_binding_version'`); err != nil {
			return err
		}
		return st.putSessionMeta(parent)
	}); err != nil {
		t.Fatal(err)
	}
	original := legacyAgentBindingDigest(t, store, "sessions", parent.ID)
	var revisionBefore string
	if err := store.writer.QueryRow(`SELECT value FROM store_meta WHERE key='revision'`).Scan(&revisionBefore); err != nil {
		t.Fatal(err)
	}
	// Fail after the binding has been written, before its session summary commits.
	if _, err := store.writer.Exec(`CREATE TRIGGER reject_legacy_agent BEFORE UPDATE ON data_sessions BEGIN SELECT RAISE(ABORT,'fixture migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrateLegacyAgentBindings(ctx); err == nil || !strings.Contains(err.Error(), "fixture migration failure") {
		t.Fatal("missing migration write error", err)
	}
	var bindings, marker int
	var revisionAfter string
	if err := store.writer.QueryRow(`SELECT (SELECT count(*) FROM data_agent_bindings),(SELECT count(*) FROM store_meta WHERE key='legacy_agent_binding_version'),(SELECT value FROM store_meta WHERE key='revision')`).Scan(&bindings, &marker, &revisionAfter); err != nil {
		t.Fatal(err)
	}
	if bindings != 0 || marker != 0 || revisionBefore != revisionAfter || original != legacyAgentBindingDigest(t, store, "sessions", parent.ID) {
		t.Fatal("failed migration left partial changes", bindings, marker, revisionAfter)
	}
	if _, err := store.writer.Exec(`DROP TRIGGER reject_legacy_agent`); err != nil {
		t.Fatal(err)
	}
	parent.SelectedAgent.Prompt = ""
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.putSessionMeta(parent) }); err != nil {
		t.Fatal(err)
	}
	if err := store.migrateLegacyAgentBindings(ctx); err == nil || !strings.Contains(err.Error(), "valid name and prompt") {
		t.Fatal("invalid snapshot was silently accepted", err)
	}
	parent.SelectedAgent = legacyAgentSnapshotFixture()
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.putSessionMeta(parent) }); err != nil {
		t.Fatal(err)
	}
	if err := store.migrateLegacyAgentBindings(ctx); err != nil {
		t.Fatal("retry failed", err)
	}
}

func TestSQLiteLegacyAgentBindingImport(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := newTopLevelSession("project", "Legacy", "Ungrouped", "pi", "session-model", "")
	s.ID, s.SelectedAgent, s.AgentSelectionLocked = "legacy", legacyAgentSnapshotFixture(), true
	state := diskState{Version: 2, Projects: []Project{{ID: "project", Name: "Fixture", Folder: t.TempDir(), Folders: []string{}, ArchivedFolders: []string{}}}, Sessions: []Session{s}, Native: map[string]nativeSession{}}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	var binding *SessionAgentBinding
	if err := decodeSQLiteRecord(ctx, store.writer, "agent_bindings", sqliteOwnedKey(s.ID, ""), 8<<20, &binding); err != nil || binding == nil || !binding.LegacySnapshot || binding.Desired.Prompt != s.SelectedAgent.Prompt || binding.Desired.Model != s.Model {
		_ = store.close()
		t.Fatal("JSON cutover did not migrate snapshot", binding, err)
	}
	m, _, err := sqliteManifestFromStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(filepath.Join(dir, "legacy-backups", m.Backup.ID, "state.json"))
	if err != nil || string(backup) != string(body) {
		t.Fatal("migration changed original JSON backup", err)
	}
	digest := legacyAgentBindingDigest(t, store, "agent_bindings", sqliteOwnedKey(s.ID, ""))
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	store, err = openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	if digest != legacyAgentBindingDigest(t, store, "agent_bindings", sqliteOwnedKey(s.ID, "")) {
		t.Fatal("reopening cutover changed snapshot binding")
	}
}
