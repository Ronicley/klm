package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func agentBindingFixture(t *testing.T) (*app, Session, Project, AgentDefinition) {
	t.Helper()
	a, parent := sqliteAutomationApp(t)
	project, err := a.store.readProjectMeta(a.ctx, parent.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	recipe, _ := harnessRecipeFor("opencode")
	metadata, _ := json.Marshal(map[string]string{"name": recipe.Package, "version": recipe.Version})
	if err := os.WriteFile(filepath.Join(root, "package.json"), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	a.binaries["opencode"] = binary{path: filepath.Join(root, "bin", "not-executed.exe")}
	key, err := modelCatalogCacheKey(Session{ProjectID: project.ID, Harness: "opencode"}, project.Folder)
	if err != nil {
		t.Fatal(err)
	}
	a.catalogs = map[string]catalogCache{key: {catalog: &ModelCatalog{DefaultModel: "fixture/base", Models: []ModelOption{{ID: "fixture/base", Efforts: []string{"high", "low"}, DefaultEffort: "high"}, {ID: "fixture/next", Efforts: []string{"high", "low"}, DefaultEffort: "high"}}}, expires: time.Now().Add(time.Hour)}}
	definition := AgentDefinition{Name: "Bound specialist", Enabled: true, DefaultHarness: "opencode", Model: "fixture/base", Effort: "high", Prompt: "Specialist original prompt."}
	raw, err := toml.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	raw = append([]byte("# Preserve this external comment\n"), raw...)
	if err := writeConfig(project, agentFile(authoringSlug(definition.Name)), raw); err != nil {
		t.Fatal(err)
	}
	return a, parent, project, definition
}

func TestAgentBindingCreationOverridesAndLifecycle(t *testing.T) {
	a, parent, project, definition := agentBindingFixture(t)
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	original, _ := readConfig(project, agentFile(authoringSlug(definition.Name)))
	id, err := a.createManualAgentSession(ctx, project.ID, "Bound fixture", parent.TopicID, "", "", "", definition.Name, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	s, binding, cwd, err := a.readBoundAgent(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	marked, _ := readConfig(project, agentFile(authoringSlug(definition.Name)))
	document, err := readAgentDocument(marked)
	if err != nil || document.SourceUID != binding.Source.SourceUID || !bytes.HasSuffix(marked, original) {
		t.Fatal("adoption did not preserve source identity/comments/bytes", err)
	}
	low := "low"
	empty := []string{}
	err = a.patchAgentBinding(ctx, id, binding.Revision, "", AgentOverridePatch{Effort: agentOverrideValue[string]{Present: true, Value: &low}, Skills: agentOverrideValue[[]string]{Present: true, Value: &empty}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, binding, _, _ = a.readBoundAgent(ctx, id)
	definition.Model, definition.Prompt = "fixture/next", "Updated source prompt."
	raw, err := marshalExistingAgentDocument(project, authoringSlug(definition.Name), definition)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeConfig(project, agentFile(authoringSlug(definition.Name)), raw); err != nil {
		t.Fatal(err)
	}
	res, err := a.resolveBoundAgent(ctx, BoundAgentRequest{Binding: *binding, FixedHarness: s.Harness, ProjectID: project.ID, ExecutionCWD: cwd})
	if err != nil || res.Blocked != nil || res.Config.Model != "fixture/next" || res.Config.Effort != "low" {
		t.Fatalf("source edit lost explicit effort or froze model: %+v %v", res, err)
	}
	if err := a.patchAgentBinding(ctx, id, binding.Revision, "", AgentOverridePatch{Effort: agentOverrideValue[string]{Present: true}}, nil); err != nil {
		t.Fatal(err)
	}
	_, binding, _, _ = a.readBoundAgent(ctx, id)
	if binding.Overrides.Effort != nil || binding.Overrides.Skills == nil || binding.Overrides.MCPConnections != nil {
		t.Fatal("reset/presence coupled separate override families")
	}
	preparation, err := a.prepareAgentTurn(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if preparation.Resolution.Config.Model != "fixture/next" || preparation.Resolution.Config.Effort != "high" || preparation.ExternalCapabilities == nil {
		t.Fatal("turn did not capture one effective model/prompt/Library")
	}
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.markAgentApplied(id, preparation) }); err != nil {
		t.Fatal(err)
	}
	_, binding, _, _ = a.readBoundAgent(ctx, id)
	if binding.Applied == nil || agentSummary(*binding, preparation.Resolution).Pending {
		t.Fatal("applied projection did not settle")
	}
	stableSource, _ := readConfig(project, agentFile(authoringSlug(definition.Name)))
	markerless, _ := toml.Marshal(definition)
	if err := writeConfig(project, agentFile(authoringSlug(definition.Name)), markerless); err != nil {
		t.Fatal(err)
	}
	res, err = a.resolveBoundAgent(ctx, BoundAgentRequest{Binding: *binding, FixedHarness: s.Harness, ProjectID: project.ID, ExecutionCWD: cwd})
	if err != nil || res.Blocked == nil || res.SourceStatus != "source_invalid" {
		t.Fatal("removed source marker silently fell back/rebound", err)
	}
	if err := writeConfig(project, agentFile(authoringSlug(definition.Name)), stableSource); err != nil {
		t.Fatal(err)
	}
	copied := definition
	copied.Name = "Copied bound source"
	copiedBytes, _ := toml.Marshal(agentDocument{AgentDefinition: copied, SourceUID: binding.Source.SourceUID})
	if err := writeConfig(project, agentFile(authoringSlug(copied.Name)), copiedBytes); err != nil {
		t.Fatal(err)
	}
	res, err = a.resolveBoundAgent(ctx, BoundAgentRequest{Binding: *binding, FixedHarness: s.Harness, ProjectID: project.ID, ExecutionCWD: cwd})
	if err != nil || res.Blocked == nil || res.SourceStatus != "conflict" {
		t.Fatal("copied bound UID was eligible for stale execution", err)
	}
	if err := os.Remove(filepath.Join(project.Folder, filepath.FromSlash(agentFile(authoringSlug(copied.Name))))); err != nil {
		t.Fatal(err)
	}
	offline := project.Folder + ".offline"
	if err := os.Rename(project.Folder, offline); err != nil {
		t.Fatal(err)
	}
	res, err = a.resolveBoundAgent(ctx, BoundAgentRequest{Binding: *binding, FixedHarness: s.Harness, ProjectID: project.ID, ExecutionCWD: cwd})
	if restoreErr := os.Rename(offline, project.Folder); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if err != nil || res.Blocked == nil || res.SourceStatus == "deleted" {
		t.Fatal("unknown root I/O was treated as source deletion", err)
	}
	definition.DefaultHarness, definition.Model = "pi", "pi-only/model"
	raw, _ = marshalExistingAgentDocument(project, authoringSlug(definition.Name), definition)
	if err := writeConfig(project, agentFile(authoringSlug(definition.Name)), raw); err != nil {
		t.Fatal(err)
	}
	res, err = a.resolveBoundAgent(ctx, BoundAgentRequest{Binding: *binding, FixedHarness: s.Harness, ProjectID: project.ID, ExecutionCWD: cwd})
	if err != nil || res.Blocked == nil || res.Config.Harness != "opencode" {
		t.Fatal("source harness edit silently changed history harness or allowed incompatible model", err)
	}
	q := QueuedMessage{ID: newID(), Text: "Keep this queued task", Mode: "queue", Status: "queued"}
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if err := st.putRecord("queued_messages", sqliteOwnedKey(id, q.ID), id, q.ID, -1, q); err != nil {
			return err
		}
		return st.putRoot("queue_payloads", q.ID, queuedPayload{Submission: submission{Text: q.Text}, Harness: s.Harness, Directory: cwd})
	}); err != nil {
		t.Fatal(err)
	}
	turnCtx, stop := context.WithCancel(ctx)
	owned := &turn{ctx: turnCtx, cancel: stop, done: make(chan struct{})}
	a.runs[id] = owned
	a.wg.Add(1)
	a.executeBoundQueued(owned, s, nativeSession{}, a.binaries[s.Harness], cwd, submission{Text: q.Text}, []QueuedMessage{q})
	if err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM data_queued_messages WHERE owner_id=?`, id).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatal("blocked preparation consumed queue")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cancelledCtx, cancelledStop := context.WithCancel(ctx)
	cancelled := &turn{ctx: cancelledCtx, cancel: cancelledStop, done: make(chan struct{})}
	a.runs[id] = cancelled
	a.wg.Add(1)
	cancelledStop()
	a.executeBoundQueued(cancelled, s, nativeSession{}, a.binaries[s.Harness], cwd, submission{Text: q.Text}, []QueuedMessage{q})
	if stopped, err := a.store.readSessionMeta(ctx, id); err != nil || stopped.Status != "idle" {
		t.Fatal("Stop during specialist preparation left a false execution failure", err)
	}
	if err := os.Remove(filepath.Join(project.Folder, filepath.FromSlash(agentFile(authoringSlug(definition.Name))))); err != nil {
		t.Fatal(err)
	}
	_, binding, _, _ = a.readBoundAgent(ctx, id)
	res, err = a.resolveBoundAgent(ctx, BoundAgentRequest{Binding: *binding, FixedHarness: s.Harness, ProjectID: project.ID, ExecutionCWD: cwd})
	if err != nil || res.Blocked != nil || res.SourceStatus != "deleted" || res.Config.Model != "fixture/next" {
		t.Fatalf("confirmed deletion did not retain latest valid capture: %+v %v", res, err)
	}
}

func TestAgentBindingSpawnReceiptAndLibraryMarker(t *testing.T) {
	a, parent, project, definition := agentBindingFixture(t)
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	connection := MCPConnection{ID: newID(), Name: "Fixture", Revision: "fixture", Config: MCPConnectionConfig{Transport: "http", Auth: "none", URL: "http://127.0.0.1:1"}}
	definition.MCPConnections = []MCPSelection{{ConnectionID: connection.ID, Tools: []string{"fixture"}}}
	raw, _ := toml.Marshal(definition)
	if err := writeConfig(project, agentFile(authoringSlug(definition.Name)), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if _, err := st.appendEvent(parent.ID, Event{ID: "human-agent-request", Type: "user", Text: "Create a session using the saved specialist."}); err != nil {
			return err
		}
		if err := st.putRoot("mcp_connections", connection.ID, connection); err != nil {
			return err
		}
		return st.putSingleChild("capability_selections", sqliteOwnedKey(parent.ID, ""), CapabilitySelection{Skills: []string{"parent-external"}, MCPConnections: definition.MCPConnections})
	}); err != nil {
		t.Fatal(err)
	}
	owned := &turn{ctx: ctx}
	a.runs[parent.ID] = owned
	a.harnessInstalling = true // Hold native scheduling; no executor/provider is started.
	p := &adapter{app: a, id: parent.ID, turn: owned, role: "", harness: parent.Harness}
	args := spawnArgs{Title: "Saved specialist", Prompt: "Self-contained fixture task", OperationID: "agent-spawn", SourceUserEventID: "human-agent-request", Agent: definition.Name}
	accepted, err := p.spawnSession(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	childID := accepted.(map[string]any)["sessionId"].(string)
	_, binding, _, err := a.readBoundAgent(ctx, childID)
	if err != nil || binding == nil {
		t.Fatal("spawn accepted without binding", err)
	}
	if len(binding.InheritedCapabilities.Skills) != 0 || len(binding.Desired.Selection.Skills) != 0 {
		t.Fatal("saved agent silently inherited parent external selection")
	}
	inherit := true
	inheritedArgs := args
	inheritedArgs.OperationID = "agent-spawn-inherited"
	inheritedArgs.InheritParentCapabilities = &inherit
	inheritedResult, err := p.spawnSession(ctx, inheritedArgs)
	if err != nil {
		t.Fatal(err)
	}
	inheritedID := inheritedResult.(map[string]any)["sessionId"].(string)
	_, inheritedBinding, _, _ := a.readBoundAgent(ctx, inheritedID)
	if len(inheritedBinding.InheritedCapabilities.Skills) != 1 || inheritedBinding.InheritedCapabilities.Skills[0] != "parent-external" {
		t.Fatal("explicit external inheritance was not captured")
	}
	general := newTopLevelSession("", "General fixture", "", "pi", "", "")
	general.Role = sessionRoleGeneralAgent
	general.ExecutionCWD = t.TempDir()
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if err := st.createSession(general, a.dir); err != nil {
			return err
		}
		_, err := st.appendEvent(general.ID, Event{ID: "general-human", Type: "user", Text: "Create a saved-specialist session in the explicit project."})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	generalTurn := &turn{ctx: ctx}
	a.runs[general.ID] = generalTurn
	generalAdapter := &adapter{app: a, id: general.ID, turn: generalTurn, role: sessionRoleGeneralAgent, harness: "pi"}
	generalArgs, _ := json.Marshal(map[string]any{"projectId": project.ID, "title": "General saved specialist", "prompt": "Explicit general fixture task", "agent": definition.Name, "operationId": "general-agent-create", "sourceUserEventId": "general-human"})
	generalResult, err := generalAdapter.callGeneralTool(ctx, "session_create", generalArgs)
	if err != nil {
		t.Fatal("general-create agent fields were not dispatched", err)
	}
	generalChild := generalResult.(map[string]any)["sessionId"].(string)
	generalMeta, generalBinding, _, err := a.readBoundAgent(ctx, generalChild)
	if err != nil || generalBinding == nil || generalMeta.TopicID != "" || generalMeta.ProjectID != project.ID {
		t.Fatal("general saved-agent creation lost explicit project/no-topic rules", err)
	}
	selection := definition.MCPConnections
	if err := a.patchAgentBinding(ctx, childID, binding.Revision, "", AgentOverridePatch{MCPConnections: agentOverrideValue[[]MCPSelection]{Present: true, Value: &selection}}, nil); err != nil {
		t.Fatal(err)
	}
	_, binding, _, _ = a.readBoundAgent(ctx, childID)
	binding.Applied = &AgentExecutionCapture{Config: binding.Desired, Revision: agentConfigDigest(binding.Desired)}
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.putAgentBinding(childID, binding) }); err != nil {
		t.Fatal(err)
	}
	markerBefore, _ := readConfig(project, agentFile(authoringSlug(definition.Name)))
	docBefore, _ := readAgentDocument(markerBefore)
	// Confirmed deletion goes through the real Library outbox/authoring path.
	a.authoringMu.Lock()
	a.libraryMu.Lock()
	plan, err := a.prepareSQLLibraryDeletion(ctx, "mcp", connection.ID, 0, 50)
	a.libraryMu.Unlock()
	a.authoringMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"revision": plan.revision})
	request := httptest.NewRequest("DELETE", "/", bytes.NewReader(body))
	request.SetPathValue("kind", "mcp")
	request.SetPathValue("itemID", connection.ID)
	response := httptest.NewRecorder()
	a.deleteSQLLibraryItem(response, request)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	markerAfter, _ := readConfig(project, agentFile(authoringSlug(definition.Name)))
	docAfter, err := readAgentDocument(markerAfter)
	if err != nil || docAfter.SourceUID != docBefore.SourceUID || len(docAfter.MCPConnections) != 0 {
		t.Fatal("Library writer lost marker or selected deleted MCP", err)
	}
	_, binding, _, _ = a.readBoundAgent(ctx, childID)
	if len(binding.LastCaptured.Config.Selection.MCPConnections) != 0 || len(binding.Desired.Selection.MCPConnections) != 0 {
		t.Fatal("hidden fallback reintroduced removed Library selection")
	}
	if binding.Overrides.MCPConnections == nil || len(*binding.Overrides.MCPConnections) != 0 || binding.Applied == nil || len(binding.Applied.Config.Selection.MCPConnections) != 1 {
		t.Fatal("Library removal lost override presence or mutated applied capture")
	}
	_, inheritedBinding, _, _ = a.readBoundAgent(ctx, inheritedID)
	if len(inheritedBinding.InheritedCapabilities.MCPConnections) != 0 {
		t.Fatal("Library removal left hidden parent contribution")
	}
	if err := a.patchAgentBinding(ctx, childID, binding.Revision, "", AgentOverridePatch{MCPConnections: agentOverrideValue[[]MCPSelection]{Present: true}}, nil); err != nil {
		t.Fatal(err)
	}
	_, binding, _, _ = a.readBoundAgent(ctx, childID)
	if binding.Overrides.MCPConnections != nil || len(binding.Desired.Selection.MCPConnections) != 0 {
		t.Fatal("reset resurrected a removed Library connection")
	}
	if err := os.Remove(filepath.Join(project.Folder, filepath.FromSlash(agentFile(authoringSlug(definition.Name))))); err != nil {
		t.Fatal(err)
	}
	replayed, err := p.spawnSession(ctx, args)
	if err != nil || replayed.(map[string]any)["sessionId"] != childID {
		t.Fatal("accepted receipt consulted changed/deleted source instead of replaying", err)
	}
}

func TestAgentBindingAutomationKeepsAdmittedCapture(t *testing.T) {
	a, parent, project, definition := agentBindingFixture(t)
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	id, err := a.createManualAgentSession(ctx, project.ID, "Automation specialist", parent.TopicID, "", "", "", definition.Name, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := a.prepareAgentTurn(ctx, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.prepareAgentTurn(ctx, id, &AgentExecutionCapture{Config: prepared.Resolution.Config}); err == nil {
		t.Fatal("frozen empty Library selection lost its explicit managed-isolation capture")
	}
	s, binding, _, _ := a.readBoundAgent(ctx, id)
	source := binding.LastCaptured
	config := Automation{ID: "agent-automation", ProjectID: project.ID, Name: "Agent fixture", Mode: "webhook", SessionMode: "existing", SessionID: id, FollowUpPrompt: "Fixture follow up", Harness: s.Harness, Model: s.Model, Effort: s.Effort, TopicID: parent.TopicID}
	snapshot := config
	snapshot.AgentConfiguration = &AgentExecutionCapture{Config: prepared.Resolution.Config, Source: &source, Revision: agentConfigDigest(prepared.Resolution.Config)}
	run := AutomationRun{ID: "agent-run", AutomationID: config.ID, ProjectID: project.ID, SessionID: id, Snapshot: snapshot, Status: "running", Capabilities: prepared.ExternalCapabilities}
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("automations", config.ID, config); err != nil {
			return err
		}
		return st.putRoot("automation_runs", run.ID, run)
	}); err != nil {
		t.Fatal(err)
	}
	definition.Model, definition.Prompt = "not-in-fixed-harness", "Changed during automation."
	raw, _ := marshalExistingAgentDocument(project, authoringSlug(definition.Name), definition)
	if err := writeConfig(project, agentFile(authoringSlug(definition.Name)), raw); err != nil {
		t.Fatal(err)
	}
	frozen, err := a.prepareAgentTurn(ctx, id, nil)
	if err != nil || frozen.Resolution.Config.Model != "fixture/base" || frozen.Resolution.Config.Prompt != prepared.Resolution.Config.Prompt {
		t.Fatal("active automation followed a live specialist edit", err)
	}
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		run.Status = "execution_failed"
		return st.putRoot("automation_runs", run.ID, run)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.prepareAgentTurn(ctx, id, nil); err == nil {
		t.Fatal("ordinary turn after frozen run failed to pick up incompatible source edit")
	}
}

func TestAgentBindingHTTPAndReferenceBackfill(t *testing.T) {
	a, parent, project, definition := agentBindingFixture(t)
	createBody, _ := json.Marshal(map[string]any{"projectId": project.ID, "topicId": parent.TopicID, "title": "HTTP specialist", "agent": definition.Name})
	request := httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(createBody))
	response := httptest.NewRecorder()
	a.createSession(response, request)
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	var created struct {
		Session struct {
			ID          string              `json:"id"`
			Harness     string              `json:"harness"`
			AgentConfig *AgentConfigSummary `json:"agentConfig"`
		} `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Session.AgentConfig == nil || created.Session.Harness != "opencode" {
		t.Fatal("creation omitted compact binding summary")
	}
	id := created.Session.ID
	_, binding, _, _ := a.readBoundAgent(a.ctx, id)
	patch := func(revision string, overrides map[string]any, want int) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"revision": revision, "overrides": overrides})
		r := httptest.NewRequest("PATCH", "/", bytes.NewReader(body))
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		a.sessionAgentState(w, r)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := patch(binding.Revision, map[string]any{"effort": "low", "skills": []string{}}, 200)
	state := result["agent"].(map[string]any)
	saved := state["overrides"].(map[string]any)
	if saved["effort"] != "low" || saved["skills"] == nil || saved["mcpConnections"] != nil {
		t.Fatal("wire shape lost independent override presence")
	}
	patch(binding.Revision, map[string]any{"prompt": "stale"}, 409)
	result = patch(state["revision"].(string), map[string]any{"effort": nil}, 200)
	if _, exists := result["agent"].(map[string]any)["overrides"].(map[string]any)["effort"]; exists {
		t.Fatal("null reset did not restore following agent")
	}
	cacheKey, _ := modelCatalogCacheKey(Session{ProjectID: project.ID, Harness: "opencode"}, project.Folder)
	a.catalogs[cacheKey].catalog.Models[0].DefaultEffort = ""
	result = patch(result["agent"].(map[string]any)["revision"].(string), map[string]any{"effort": ""}, 200)
	if result["agent"].(map[string]any)["overrides"].(map[string]any)["effort"] != "" {
		t.Fatal("explicit default effort was confused with follow-agent or invented a replacement")
	}
	global := definition
	global.Name = "Backfill specialist"
	global.Skills = []string{"selected-before-cp1b"}
	data, _ := toml.Marshal(global)
	record := GlobalAgentRecord{ID: newID(), Slug: authoringSlug(global.Name), Definition: global, Revision: revision(data)}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("global_agents", record.ID, record); err != nil {
			return err
		}
		_, err := st.tx.ExecContext(st.ctx, `DELETE FROM selection_references WHERE domain='global_agents'; DELETE FROM store_meta WHERE key='agent_binding_reference_version'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.upgradeAgentReferenceIndexes(a.ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.store.readers.QueryRowContext(a.ctx, `SELECT count(*) FROM selection_references WHERE domain='global_agents' AND item='selected-before-cp1b'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("v2 global reference backfill did not recover old selection", err)
	}
}

func TestAgentBindingLocalMarkerSnapshotValidation(t *testing.T) {
	a, parent, project, definition := agentBindingFixture(t)
	if _, err := a.createManualAgentSession(a.ctx, project.ID, "Mark source", parent.TopicID, "", "", "", definition.Name, "", "", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := readConfig(project, agentFile(authoringSlug(definition.Name)))
	if err != nil {
		t.Fatal(err)
	}
	graph := GraphDefinition{Name: "Marker graph", Enabled: true, InitialNode: "node", Nodes: map[string]GraphNode{"node": {Type: "agent", Name: "Specialist", Agent: authoringSlug(definition.Name), Choices: []string{"done"}}}, Choices: map[string]GraphChoice{"done": {Name: "done", Terminal: true, Input: map[string]ChoiceField{}, Output: map[string]string{}}}}
	record := AgentRecord{AgentDefinition: definition, ID: authoringSlug(definition.Name), Revision: revision(raw), GraphReferences: []string{}}
	compiled, err := compileGraph(graph, []AgentRecord{record}, func(EffectiveGraphAgent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	graphBytes, err := yaml.Marshal(compiled.Definition)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := GraphSnapshot{GraphID: "marker-graph", CapturedAt: now(), Graph: GraphRecord{ID: "marker-graph", Revision: revision(graphBytes), Definition: compiled.Definition}, Agents: []AgentRecord{record}, EffectiveAgents: compiled.Agents, Sources: map[string]GraphSourceSnapshot{graphFile("marker-graph"): {Revision: revision(graphBytes), Text: string(graphBytes)}, agentFile(record.ID): {Revision: revision(raw), Text: string(raw)}}}
	sources, _ := json.Marshal(snapshot.Sources)
	snapshot.Revision = revision(sources)
	if err := validateGraphSnapshot(snapshot); err != nil {
		t.Fatal("local marked v1 snapshot rejected", err)
	}
	after, _ := json.Marshal(snapshot.Sources)
	if !bytes.Equal(sources, after) {
		t.Fatal("offline validation rewrote captured source bytes/hash")
	}
}
