package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteExistingAdapterWrites(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	state := sqliteSemanticFixture(t)
	body, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	currentTurn := &turn{ctx: turnCtx, cancel: cancel, analyticsID: "execution"}
	a := &app{store: store, ctx: ctx, dir: dir, runs: map[string]*turn{"a": currentTurn}, sqlChanges: &sqliteChangeBus{}}
	p := &adapter{app: a, id: "a", turn: currentTurn, harness: "pi", keys: map[string]string{}, analyticsNative: "counter"}
	if err := p.put("native-output", "assistant", "", "A", "running", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.put("native-output", "assistant", "", "B", "running", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.put("native-output", "assistant", "", "AB", "completed", false, nil); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := store.readers.QueryRow(`SELECT value FROM store_meta WHERE key='revision'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := p.put("native-output", "assistant", "", "AB", "completed", false, nil); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := store.readers.QueryRow(`SELECT value FROM store_meta WHERE key='revision'`).Scan(&after); err != nil || before != after {
		t.Fatal("identical stream wrote another revision", err)
	}
	if err := p.nativeID("continued-native"); err != nil {
		t.Fatal(err)
	}
	baseline, final := int64(10), int64(15)
	if err := p.setUsageBaseline(&SessionUsage{InputTokens: &baseline}); err != nil {
		t.Fatal(err)
	}
	if err := p.setUsageFinal(&SessionUsage{InputTokens: &final}); err != nil {
		t.Fatal(err)
	}
	var usage SessionUsage
	if err := decodeSQLiteRecord(ctx, store.readers, "session_usage", sqliteOwnedKey("a", ""), 64<<10, &usage); err != nil || usage.InputTokens == nil || *usage.InputTokens != 15 {
		t.Fatal("adapter usage not persisted", err)
	}
	var count int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_analytics_usage`).Scan(&count); err != nil || count != 2 {
		t.Fatal("usage and accounting were not committed together", err)
	}
	var native nativeSession
	if err := decodeSQLiteRecord(ctx, store.readers, "native_sessions", "a", 64<<10, &native); err != nil || native.ID != "continued-native" || native.Path != state.Native["a"].Path {
		t.Fatal("native mapping lost path/identity", err)
	}
	a.closing = true
	if err := p.put("final-diagnostic", "status", "", "Drained after admission closed", "completed", false, nil); err != nil {
		t.Fatal(err)
	}
	if len(a.state.Sessions) != 0 || a.state.Native != nil || a.streamIndexes != nil || a.messageIDs != nil {
		t.Fatal("adapter hydrated global state/history indexes")
	}
}

func TestSQLiteOutboxIntegration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	state := sqliteSemanticFixture(t)
	project := state.Projects[0]
	state.Sessions[0].SelectedGraphID = "fixture-graph"
	state.Sessions[0].Status = "running"
	connection := MCPConnection{ID: newID(), CredentialID: "keep"}
	state.MCPConnections = map[string]MCPConnection{connection.ID: connection}
	state.MCPCredentials = map[string][]byte{"keep": {1, 2, 3}, "unused": {4, 5, 6}}
	state.Sessions[0].Capabilities.Desired = CapabilitySelection{MCPConnections: []MCPSelection{{ConnectionID: connection.ID, Tools: []string{}}}}
	state.Sessions[0].Capabilities.Applied = &CapabilitySnapshot{Version: 1, Connections: []MCPConnectionSnapshot{{ID: connection.ID, CredentialID: "keep"}}}
	path := agentFile("fixture-agent")
	original := []byte("name = \"Fixture agent\"\n")
	intended := []byte("name = \"Fixture agent\"\nskills = []\n")
	if err := commitAuthoring(project, map[string][]byte{path: original}); err != nil {
		t.Fatal(err)
	}
	state.LibraryDeletions = []LibraryDeletion{{ID: "delete-connection", Kind: "mcp", ItemID: connection.ID, Projects: []LibraryAuthoringChange{{Project: project, Expected: map[string]string{path: revision(original)}, Writes: map[string][]byte{path: intended}}}}}
	body, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	if err := commitAuthoring(project, map[string][]byte{path: []byte("external change")}); err != nil {
		t.Fatal(err)
	}
	if err := store.recoverSQLLibrary(ctx); err == nil {
		t.Fatal("external authoring conflict ignored")
	}
	var count int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_library_deletions`).Scan(&count); err != nil || count != 1 {
		t.Fatal("conflicting outbox was lost", err)
	}
	if err := commitAuthoring(project, map[string][]byte{path: original}); err != nil {
		t.Fatal(err)
	}
	if err := store.recoverSQLLibrary(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := readConfig(project, path)
	if err != nil || string(got) != string(intended) {
		t.Fatal("authoring roll-forward failed", err)
	}
	var desired CapabilitySelection
	if err := decodeSQLiteRecord(ctx, store.readers, "capability_selections", sqliteOwnedKey(sqliteOwnedKey("a", ""), ""), 1<<20, &desired); err != nil || len(desired.MCPConnections) != 0 {
		t.Fatal("future selection was not cleared", err)
	}
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_mcp_credentials WHERE id='keep'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("active capture credential lost", err)
	}
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_mcp_credentials WHERE id='unused'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unreferenced credential retained", err)
	}
	var snapshot string
	if err := store.readers.QueryRow(`SELECT digest FROM data_graph_snapshots`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	a := &app{store: store, ctx: ctx, dir: dir, runs: map[string]*turn{}}
	if err := a.commitGraphAuthoring(project, map[string][]byte{graphFile("fixture-graph"): nil, graphFile("renamed"): []byte("name: Renamed\n")}, "fixture-graph", "renamed"); err != nil {
		t.Fatal(err)
	}
	s, err := store.readSessionMeta(ctx, "a")
	if err != nil || s.SelectedGraphID != "renamed" {
		t.Fatal("catalog selection not settled", err)
	}
	var run GraphRun
	if err := decodeSQLiteRecord(ctx, store.readers, "graph_runs", "run", 1<<20, &run); err != nil || run.GraphID != "fixture-graph" || run.CatalogGraphID == nil || *run.CatalogGraphID != "renamed" {
		t.Fatal("captured graph identity was rewritten", err)
	}
	var after string
	if err := store.readers.QueryRow(`SELECT digest FROM data_graph_snapshots`).Scan(&after); err != nil || after != snapshot {
		t.Fatal("captured graph payload changed", err)
	}
	if err := store.recoverSQLGraphCatalog(ctx); err != nil {
		t.Fatal(err)
	}
}
