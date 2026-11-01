package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestAgentBindingManagerSpawnReceiptIsCompact(t *testing.T) {
	config := ResolvedAgentConfig{Harness: "opencode", Model: "fixture/model", Prompt: strings.Repeat("p", 128<<10)}
	receipt := SessionSpawn{SessionID: "created", Title: "Fixture", MessageID: "queued", OperationID: "once",
		Agent: &AgentCapture{Name: "Specialist", Source: AgentSourceRef{ID: "specialist", SourceUID: "fixture-identity", Scope: "global"}, Revision: "source-revision", Config: config}, Configuration: &config}
	encoded, err := json.Marshal(spawnReceiptResult(receipt))
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > agentToolByteLimit {
		t.Fatalf("accepted spawn receipt must fit bounded tool output without echoing full specialist prompts: %d bytes", len(encoded))
	}
	for i := 0; i < 32; i++ {
		tools := []string{}
		for j := 0; j < 64; j++ {
			tools = append(tools, fmt.Sprintf("tool-%03d-%s", j, strings.Repeat("x", 180)))
		}
		config.Selection.MCPConnections = append(config.Selection.MCPConnections, MCPSelection{ConnectionID: fmt.Sprintf("%032x", i+1), Tools: tools})
	}
	receipt.Agent.Config = config
	for _, general := range []bool{false, true} {
		candidate := receipt
		if general {
			candidate.Request.ProjectID = "explicit-project"
		}
		result := spawnReceiptResult(candidate)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > agentToolByteLimit || result["accepted"] != true || result["sessionId"] != "created" {
			t.Fatal("compact receipt lost acceptance/identity or echoed large catalogs")
		}
		if _, exists := result["agent"].(map[string]any)["config"]; exists {
			t.Fatal("receipt echoed full agent capture")
		}
		if _, exists := result["configuration"].(map[string]string)["prompt"]; exists {
			t.Fatal("receipt echoed full configuration prompt")
		}
	}
}

func TestAgentBindingManagerAutomationAdmissionRetainsPreparedLibrary(t *testing.T) {
	a, parent, project, definition := agentBindingFixture(t)
	connection := MCPConnection{ID: newID(), Name: "Admitted connection", Revision: "before", Config: MCPConnectionConfig{Transport: "http", Auth: "none", URL: "http://127.0.0.1:1"}}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error { return st.putRoot("mcp_connections", connection.ID, connection) }); err != nil {
		t.Fatal(err)
	}
	definition.MCPConnections = []MCPSelection{{ConnectionID: connection.ID, Tools: []string{"fixture"}}}
	raw, err := toml.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeConfig(project, agentFile(authoringSlug(definition.Name)), raw); err != nil {
		t.Fatal(err)
	}
	id, err := a.createManualAgentSession(a.ctx, project.ID, "Bound automation", parent.TopicID, "", "", "", definition.Name, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	config := Automation{ID: "manager-automation", ProjectID: project.ID, Name: "Fixture", Enabled: true, Mode: "webhook", SessionMode: "existing", SessionID: id, FollowUpPrompt: "Fixture only", Harness: "opencode", Model: "fixture/base", Effort: "high", TopicID: parent.TopicID}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("automations", config.ID, config); err != nil {
			return err
		}
		_, err := st.enqueueAutomation(config, "fixture-trigger", "fixture", now(), nil, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	run, _, err := a.admitSQLAutomationLocked(config.ID)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if run == nil || run.Snapshot.AgentConfiguration == nil {
		t.Fatal("expected admitted saved-agent automation")
	}
	if run.Capabilities == nil && run.Snapshot.AgentConfiguration.ExternalCapabilities == nil {
		t.Fatal("admission discarded prepared external Library capture; execution will recapture mutable content")
	}
	admitted := run.Capabilities
	if admitted == nil {
		admitted = run.Snapshot.AgentConfiguration.ExternalCapabilities
	}
	if len(admitted.Connections) != 1 {
		t.Fatal("fixture did not admit its selected external connection")
	}
	oldRevision, oldURL := admitted.Revision, admitted.Connections[0].Config.URL
	connection.Revision = "after"
	connection.Config.URL = "http://127.0.0.1:2"
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error { return st.putRoot("mcp_connections", connection.ID, connection) }); err != nil {
		t.Fatal(err)
	}
	meta, err := a.store.readSessionMeta(a.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	prepared, active, err := a.prepareAutomationAgent(a.ctx, meta)
	if err != nil || !active || prepared.ExternalCapabilities == nil {
		t.Fatal("admitted capture could not prepare dispatch", err)
	}
	if prepared.ExternalCapabilities.Revision != oldRevision || prepared.ExternalCapabilities.Connections[0].Config.URL != oldURL {
		t.Fatal("dispatch recaptured connection edited after real admission")
	}
	// A missing old/partial snapshot is explicit recovery failure, not permission
	// to fill it with mutable live content under the same admitted run identity.
	run.Capabilities = nil
	run.Snapshot.AgentConfiguration.ExternalCapabilities = nil
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error { return st.putRoot("automation_runs", run.ID, *run) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.prepareAutomationAgent(a.ctx, meta); err == nil {
		t.Fatal("missing admitted capture silently used live Library")
	}
}
