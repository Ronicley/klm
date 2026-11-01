package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func TestGraphFrozenGlobalSaveCaptureAndLookup(t *testing.T) {
	a, _, project, local := agentBindingFixture(t)
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	connection := MCPConnection{ID: newID(), Name: "Frozen graph fixture", Revision: "before", Config: MCPConnectionConfig{Transport: "http", Auth: "none", URL: "http://127.0.0.1:1"}}
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.putRoot("mcp_connections", connection.ID, connection) }); err != nil {
		t.Fatal(err)
	}
	global := local
	global.Name = "Global specialist"
	global.Description = "Global role"
	global.Prompt = "Captured global instructions."
	global.MCPConnections = []MCPSelection{{ConnectionID: connection.ID, Tools: []string{"fixture"}}}
	a.authoringMu.Lock()
	globalRow, err := a.mutateCatalogAgentLocked(ctx, Project{}, "", saveCatalogAgentBody{Agent: global}, false)
	a.authoringMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	extra := local
	extra.Name = "Extra specialist"
	extra.Prompt = "Captured non-node instructions."
	extraBytes, _ := toml.Marshal(extra)
	extraBytes = append([]byte("# Graph reads must not adopt this legacy source\n"), extraBytes...)
	if err := writeConfig(project, agentFile(authoringSlug(extra.Name)), extraBytes); err != nil {
		t.Fatal(err)
	}
	bad := local
	bad.Name = "Unavailable candidate"
	bad.Model = "not/in/catalog"
	badBytes, _ := toml.Marshal(bad)
	if err := writeConfig(project, agentFile(authoringSlug(bad.Name)), badBytes); err != nil {
		t.Fatal(err)
	}
	graph := GraphDefinition{Name: "Global graph", Enabled: true, InitialNode: "node", Nodes: map[string]GraphNode{"node": {Type: "agent", Name: "Global node", Agent: globalRow.ID, Choices: []string{"done"}}}, Choices: map[string]GraphChoice{"done": {Name: "done", Terminal: true, Input: map[string]ChoiceField{}, Output: map[string]string{}}}}
	body, _ := json.Marshal(map[string]any{"definition": graph})
	request := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	request.SetPathValue("id", project.ID)
	request.SetPathValue("item", "global-graph")
	response := httptest.NewRecorder()
	a.saveGraph(response, request)
	if response.Code != 200 {
		t.Fatal("Save rejected visible global node", response.Code, response.Body.String())
	}
	snapshot, compiled, err := a.captureGraphSnapshot(ctx, project.ID, "global-graph")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 2 || compiled.Agents["node"].Prompt != global.Prompt || snapshot.AgentOrigins[globalRow.ID].Scope != "global" || len(snapshot.GlobalSources) != 1 {
		t.Fatal("global graph source/config not captured")
	}
	if _, exists := snapshot.Sources[agentFile(globalRow.ID)]; exists {
		t.Fatal("global source was disguised as a project-relative file")
	}
	if len(snapshot.DelegationCatalog.Agents) != 3 || len(snapshot.DelegationCatalog.Diagnostics) == 0 {
		t.Fatal("complete eligible non-node catalog/diagnostics were not frozen")
	}
	if err := validateGraphSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := readConfig(project, agentFile(authoringSlug(extra.Name)))
	if !bytes.Equal(unchanged, extraBytes) {
		t.Fatal("graph capture adopted/rewrote legacy markerless source")
	}
	frozen, err := lookupFrozenGraphAgent(snapshot, extra.Name, "")
	if err != nil || frozen.Execution.Config.Prompt != extra.Prompt {
		t.Fatal("non-node specialist missing", err)
	}
	connection.Revision = "after"
	connection.Config.URL = "http://127.0.0.1:2"
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.putRoot("mcp_connections", connection.ID, connection) }); err != nil {
		t.Fatal(err)
	}
	execution, err := composeFrozenGraphAgent(snapshot, extra.Name, frozen.Source.SourceUID, "node", "opencode", AgentConfigOverrides{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.Config.Selection.MCPConnections) != 1 || execution.ExternalCapabilities.Connections[0].Config.URL != "http://127.0.0.1:1" {
		t.Fatal("inherited graph contribution came from live Library instead of parent snapshot")
	}
	if _, err := lookupFrozenGraphAgent(snapshot, bad.Name, ""); err == nil {
		t.Fatal("unusable candidate silently fell back to live catalog")
	}
	if _, err := lookupFrozenGraphAgent(snapshot, extra.Name, newID()); err == nil {
		t.Fatal("frozen source UID mismatch was ignored")
	}
	extra.Prompt = "Live source edited after start"
	changed, _ := toml.Marshal(extra)
	if err := writeConfig(project, agentFile(authoringSlug(extra.Name)), changed); err != nil {
		t.Fatal(err)
	}
	stillFrozen, err := lookupFrozenGraphAgent(snapshot, extra.Name, frozen.Source.SourceUID)
	if err != nil || stillFrozen.Execution.Config.Prompt != "Captured non-node instructions." {
		t.Fatal("captured run followed live source edits", err)
	}
	a.authoringMu.Lock()
	a.libraryMu.Lock()
	revalidateErr := a.revalidateGraphCapabilities(snapshot)
	a.libraryMu.Unlock()
	a.authoringMu.Unlock()
	if revalidateErr == nil {
		t.Fatal("reservation accepted definitions changed after capture")
	}
	before, _ := json.Marshal(snapshot)
	if _, err := compileGraphSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(snapshot)
	if !bytes.Equal(before, after) {
		t.Fatal("offline recompile rewrote sealed run artifact")
	}
}
