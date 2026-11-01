package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func sqliteSemanticFixture(t *testing.T) diskState {
	t.Helper()
	d := sqliteLegacyFixture()
	stamp := now()
	root := t.TempDir()
	d.TopicSchema = 1
	d.EngineSettings = EngineSettings{TimeZone: defaultEngineTimeZone}
	d.Projects[0].Folder = root
	d.Projects[0].Topics = []Topic{{ID: "topic", Name: "Fixture", Color: "neutral", CreatedAt: stamp}}
	for i := range d.Sessions {
		d.Sessions[i].Harness = "pi"
		d.Sessions[i].TopicID = "topic"
		d.Sessions[i].CreatedAt = stamp
		d.Sessions[i].UpdatedAt = stamp
	}
	d.SessionSpawns = []SessionSpawn{{From: "a", SessionID: "b", SourceUserEventID: "a-user", OperationID: "spawn-op", MessageID: "spawn-message", Request: spawnArgs{Title: "B", Prompt: "Fixture", OperationID: "spawn-op", SourceUserEventID: "a-user"}}}
	d.SessionCommands = []SessionCommand{{From: "a", Request: sendArgs{SessionID: "b", Instruction: "Fixture instruction", OperationID: "send-op", SourceUserEventID: "a-user"}, MessageID: "send-message", CreatedAt: stamp}}
	d.Consultations = []Consultation{{ID: "consultation", From: "a", To: "b", Question: "Fixture question", Status: "completed", Delivery: "delivered", CreatedAt: stamp}}
	agent := AgentDefinition{Name: "Fixture agent", Enabled: true, DefaultHarness: "pi", Model: "fixture-model", Prompt: "Storage fixture; never execute."}
	graph := GraphDefinition{Name: "Fixture graph", Enabled: true, InitialNode: "agent", Nodes: map[string]GraphNode{"agent": {Type: "agent", Name: "Fixture agent", Agent: "fixture-agent", Choices: []string{"done"}}}, Choices: map[string]GraphChoice{"done": {Name: "done", Terminal: true, Input: map[string]ChoiceField{}, Output: map[string]string{}}}}
	agentSource, err := toml.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	graphSource, err := yaml.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := GraphSnapshot{GraphID: "fixture-graph", CapturedAt: stamp, Graph: GraphRecord{ID: "fixture-graph", Revision: revision(graphSource), Definition: graph, Layout: GraphLayout{Positions: map[string]Point{}}}, Agents: []AgentRecord{{ID: "fixture-agent", AgentDefinition: agent, Revision: revision(agentSource)}}, EffectiveAgents: map[string]EffectiveGraphAgent{"agent": {AgentID: "fixture-agent", Harness: "pi", Model: "fixture-model", Prompt: agent.Prompt}}, Sources: map[string]GraphSourceSnapshot{graphFile("fixture-graph"): {Revision: revision(graphSource), Text: string(graphSource)}, agentFile("fixture-agent"): {Revision: revision(agentSource), Text: string(agentSource)}}}
	encoded, _ := json.Marshal(snapshot.Sources)
	snapshot.Revision = revision(encoded)
	d.GraphActivities = []GraphActivity{{ID: "activity", ConversationID: "a", ProjectID: "p", GraphID: "fixture-graph", Objective: "Fixture objective", Status: "awaiting_assessment", Version: 1, Workspace: GraphWorkspaceDecision{Mode: "original"}, Authorization: []GraphAuthorization{{EventID: "a-user", Text: "Fixture authorization"}}, CreatedAt: stamp, UpdatedAt: stamp}}
	d.GraphRuns = []GraphRun{{ID: "run", ActivityID: "activity", ProjectID: "p", ConversationID: "a", GraphID: "fixture-graph", Input: GraphRunInput{Task: "Fixture task"}, Snapshot: snapshot, Workspace: GraphWorkspaceDecision{Mode: "original"}, Status: "interrupted", Revision: 1, InitialWorkspaceID: "workspace", Result: &GraphRunResult{Kind: "interrupted"}, CreatedAt: stamp, UpdatedAt: stamp, EndedAt: stamp}}
	d.GraphWorkspaces = []WorkspaceRecord{{ID: "workspace", ProjectID: "p", Directory: root, Status: "ready", OperationID: "workspace-op", CreatedByRunID: "run", CreatedAt: stamp}}
	d.GraphWorkspaceUses = []WorkspaceUse{{ID: "workspace-use", WorkspaceID: "workspace", RunID: "run", Association: "initial", CreatedAt: stamp}}
	d.GraphActivations = []GraphActivation{{ID: "activation", RunID: "run", NodeID: "agent", Occurrence: 1, Status: "interrupted", Input: map[string]string{}, WorkspaceID: "workspace", SessionID: "node-session", CausalID: "causal", CreatedAt: stamp, UpdatedAt: stamp, EndedAt: stamp, Events: []Event{{ID: "activation-event", Type: "status", Text: "retained node event"}}}}
	d.Sessions = append(d.Sessions, Session{ID: "node-session", ProjectID: "p", Role: "graph_node", GraphRunID: "run", GraphNodeID: "agent", ExecutionCWD: root, Harness: "pi", Model: "fixture-model", Status: "error", Events: []Event{{ID: "node-event", Type: "assistant", Text: "retained native node history"}}})
	d.GraphNotifications = []RunNotification{{ID: "graph-result:run", RunID: "run", ConversationID: "a", Status: "pending", CreatedAt: stamp}}
	n := int64(3)
	d.Analytics = &AnalyticsState{Version: 1, Started: stamp, Executions: map[string]AnalyticsExecution{"execution": {ID: "execution", SessionID: "a", OwnerID: "a", ProjectID: "p", Role: "main", Harness: "pi", Reserved: stamp, Last: stamp, Spans: []AnalyticsSpan{{Start: stamp, End: stamp, Kind: "working"}}, Usage: []AnalyticsUsageDelta{{Sequence: 1, At: stamp, Input: &n}}, Sequence: 1}}, Counters: map[string]AnalyticsCounter{"counter": {Input: &n, ExecutionID: "execution"}}, Graphs: map[string]AnalyticsGraphTiming{"run": {Last: stamp, End: stamp, Interrupted: true}}}
	a := Automation{ID: "automation", ProjectID: "p", Revision: 1, OperationID: "automation-op", Name: "Fixture automation", Prompt: "Fixture task", Mode: "schedule", Harness: "pi", TopicID: "topic", SessionMode: "new_per_run", Schedule: AutomationSchedule{Time: "12:00", Recurrence: "daily", EveryDays: 1, Weekdays: []int{}}}
	d.Automations = []Automation{a}
	trigger := AutomationTrigger{ID: "trigger", AutomationID: a.ID, Key: "fixture-trigger", Source: "manual", At: stamp, Consumed: true}
	d.AutomationTriggers = []AutomationTrigger{trigger}
	d.AutomationRuns = []AutomationRun{{ID: "automation-run", AutomationID: a.ID, ProjectID: "p", SessionID: "b", Snapshot: a, Trigger: trigger, Status: "execution_failed", StartedAt: stamp, FinishedAt: stamp}}
	d.Sessions[1].Automation = &AutomationSession{RunID: "automation-run", AutomationID: a.ID, Status: "execution_failed"}
	d.AutomationRecoveries = []AutomationRecovery{{ID: "recovery", RunID: "automation-run", Snapshot: a, Source: "interrupted", From: stamp, Until: stamp, SessionID: "b", Delivery: "pending", OccurrenceDecisions: []AutomationRecoveryOccurrence{{ID: "occurrence", ScheduledAt: stamp, Action: "discard"}}, Resolutions: []AutomationRecoveryResolution{{OperationID: "resolution-op", Action: "discard", UserEventID: "a-user", OccurrenceIDs: []string{"occurrence"}}}}}
	connectionID, eventID := newID(), newID()
	payload := `{"action":"opened","number":1}`
	d.WebhookConnections = map[string]WebhookConnection{connectionID: {ID: connectionID, ProjectID: "p", Provider: "github", Name: "Fixture webhook", Enabled: true, Secret: strings.Repeat("fixture-", 4), Retention: "forever", CreatedAt: stamp, UpdatedAt: stamp, LastReceivedAt: stamp}}
	d.WebhookEvents = map[string]WebhookStoredEvent{eventID: {ID: eventID, ProjectID: "p", WebhookID: connectionID, Provider: "github", DeliveryID: "fixture-delivery", EventType: "issues", Action: "opened", ReceivedAt: stamp, ReceivedSequence: 1, Payload: payload, PayloadBytes: len(payload)}}
	d.WebhookPending = map[string]WebhookPending{eventID: {EventID: eventID, ReceivedSequence: 1}}
	d.WebhookReleases = map[string]WebhookRelease{}
	d.WebhookReceivedSequence = 1
	d.ActivityBoardConfig = activityBoardConfig{Schema: 1, Phases: []activityBoardPhase{{ID: "phase", Name: "Todo"}}}
	d.ActivityBoardConnections = map[string]activityBoardConnection{"board": {ID: "board", Name: "Fixture board", Provider: "github", ProjectID: "p", Scope: activityBoardScope{Repository: "fixture/repo", RepositoryID: "1"}, Mappings: map[string][]activityBoardValue{"phase": {{ID: "todo", Name: "Todo"}}}}}
	d.ActivityBoardCredentials = map[string][]byte{"board": {1, 2, 3}}
	d.ActivityBoardItems = map[string]activityBoardItem{"item": {Key: "item", ConnectionID: "board", ExternalID: "1", Version: "fixture-version"}}
	d.ActivityBoardMoves = map[string]activityBoardMove{"move": {OperationID: "move", Key: "item", ConnectionID: "board", Status: "unconfirmed"}}
	d.Transcription = TranscriptionSettings{APIKey: "synthetic-not-a-real-key", Model: defaultTranscriptionModel, Vocabulary: []TranscriptionVocabularyEntry{{Term: "Fixture", Note: "retained"}}}
	for _, validate := range []func(*diskState) error{validateGraphRecords, validateAutomationRecords, validateWebhookRecords, validateActivityBoardRecords, validateTopicRecords, validateAnalytics} {
		if err := validate(&d); err != nil {
			t.Fatal("invalid test oracle fixture:", err)
		}
	}
	return d
}

func TestSQLiteMigrationProductionImport(t *testing.T) {
	ctx := context.Background()
	state := sqliteSemanticFixture(t)
	dir := t.TempDir()
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := fingerprintLegacyFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	m, authority, err := sqliteManifestFromStore(ctx, store)
	if err != nil || authority != "committed" {
		t.Fatalf("authority: %s %v", authority, err)
	}
	backup := filepath.Join(dir, "legacy-backups", m.Backup.ID)
	after, err := fingerprintLegacyFile(filepath.Join(backup, "state.json"))
	if err != nil || before != after {
		t.Fatal("original checkpoint changed", err)
	}
	if err := verifyLegacyFiles(backup, m.Backup); err != nil {
		t.Fatal(err)
	}
	var report string
	if err := store.writer.QueryRow(`SELECT value FROM store_meta WHERE key='import_verification'`).Scan(&report); err != nil {
		t.Fatal(err)
	}
	var domains map[string]sqliteImportDomainReport
	if err := json.Unmarshal([]byte(report), &domains); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"sessions", "session_events", "graph_snapshots", "graph_activations", "activation_events", "analytics_spans", "analytics_usage", "automation_recoveries", "automation_resolution_occurrences", "webhook_payloads", "board_credentials"} {
		if domains[table].Records == 0 {
			t.Fatalf("missing imported domain %s", table)
		}
	}
	page, err := store.readHistory(ctx, "node-session", "", "", 50)
	if err != nil || len(page.Events) != 1 || page.Events[0].Event.Text != "retained native node history" {
		t.Fatalf("node history lost: %+v %v", page, err)
	}
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	store, err = openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	if err := verifyLegacyFiles(backup, m.Backup); err != nil {
		t.Fatal(err)
	}
	t.Logf("verified %d normalized domain tables; real importer committed and reopened", len(domains))
}

func TestSQLiteMigrationProductionRejectsInvalidOwnership(t *testing.T) {
	for _, name := range []string{"orphan-session", "spoofed-authorization", "webhook-pending", "analytics-order", "automation-destination", "graph-snapshot"} {
		t.Run(name, func(t *testing.T) {
			state := sqliteSemanticFixture(t)
			switch name {
			case "orphan-session":
				state.Sessions[0].ProjectID = "missing"
			case "spoofed-authorization":
				state.GraphActivities[0].Authorization[0].EventID = "reply"
			case "webhook-pending":
				state.WebhookPending = map[string]WebhookPending{}
			case "analytics-order":
				e := state.Analytics.Executions["execution"]
				e.Spans[0].End = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
				state.Analytics.Executions["execution"] = e
			case "automation-destination":
				state.AutomationRuns[0].SessionID = "node-session"
			case "graph-snapshot":
				state.GraphRuns[0].Snapshot.Revision = "incorrect"
			}
			dir := t.TempDir()
			body, _ := json.Marshal(state)
			if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := openOrMigrateSQLite(context.Background(), dir, prepareSQLiteImport, nil); err == nil {
				t.Fatal("invalid legacy ownership accepted")
			}
			if _, err := os.Stat(filepath.Join(dir, "engine.sqlite3")); !os.IsNotExist(err) {
				t.Fatal("invalid import published database")
			}
			current, err := os.ReadFile(filepath.Join(dir, "state.json"))
			if err != nil || string(current) != string(body) {
				t.Fatal("rejected import changed source")
			}
		})
	}
}

func TestSQLiteMigrationProductionFreshStore(t *testing.T) {
	store, err := openOrMigrateSQLite(context.Background(), t.TempDir(), prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	var count int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("fresh store invented sessions", err)
	}
}

func TestSQLiteMigrationVerificationRejectsCorruption(t *testing.T) {
	for _, change := range []string{
		`UPDATE data_session_events_chunks SET body=CAST('corrupted' AS BLOB) WHERE record_key=(SELECT id FROM data_session_events LIMIT 1)`,
		`UPDATE data_sessions SET project_id='wrong-owner' WHERE id='a'`,
		`UPDATE data_session_events SET owner_id='b' WHERE owner_id='a' AND position=1`,
		`DELETE FROM migration_collection_refs WHERE parent_table='sessions' AND parent_id='a' AND field='events'`,
	} {
		t.Run(strconv.Itoa(len(change)), func(t *testing.T) {
			store := sqliteTestStage(t, sqliteLegacyFixture())
			ctx := context.Background()
			if err := importStagedDomains(ctx, store); err != nil {
				t.Fatal(err)
			}
			if _, err := store.writer.Exec(change); err != nil {
				t.Fatal(err)
			}
			if err := store.write(ctx, func(tx *sql.Tx) error { _, err := verifySQLiteImport(ctx, tx); return err }); err == nil {
				t.Fatal("corrupt imported record passed verification")
			}
		})
	}
}
