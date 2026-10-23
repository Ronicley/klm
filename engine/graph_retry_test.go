package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func retryFixture(t *testing.T, parallel bool) diskState {
	t.Helper()
	g := GraphDefinition{Name: "Retry", Enabled: true, InitialNode: "a", Nodes: map[string]GraphNode{
		"a": {Type: "agent", Agent: "worker", Choices: []string{"next"}},
		"b": {Type: "agent", Agent: "worker", Choices: []string{"done"}},
	}, Choices: map[string]GraphChoice{
		"next": {Name: "Next", Input: map[string]ChoiceField{}, Output: map[string]string{}, To: "b"},
		"done": {Name: "Done", Input: map[string]ChoiceField{}, Output: map[string]string{}, Terminal: true},
	}}
	if parallel {
		choice := g.Choices["next"]
		choice.To = "fork"
		g.Choices["next"] = choice
		shared := false
		g.Nodes["fork"] = GraphNode{Type: "fork", Branches: map[string]GraphBranch{
			"left":  {Name: "Left", SeparateWorktree: &shared, To: "left", Output: map[string]string{}},
			"right": {Name: "Right", SeparateWorktree: &shared, To: "b", Output: map[string]string{}},
		}}
		g.Nodes["left"] = GraphNode{Type: "agent", Agent: "worker", Choices: []string{"left-done"}}
		g.Nodes["b"] = GraphNode{Type: "agent", Agent: "worker", Choices: []string{"right-done"}}
		g.Nodes["join"] = GraphNode{Type: "join", Agent: "worker", Choices: []string{"done"}}
		for _, name := range []string{"left-done", "right-done"} {
			g.Choices[name] = GraphChoice{Name: name, Input: map[string]ChoiceField{}, Output: map[string]string{}, To: "join"}
		}
	}
	for id, node := range g.Nodes {
		node.Name = id
		g.Nodes[id] = node
	}
	for id, choice := range g.Choices {
		choice.Name = strings.ReplaceAll(id, "-", "_")
		g.Choices[id] = choice
	}
	agent := AgentDefinition{Name: "Worker", Enabled: true, DefaultHarness: "pi", Model: "model", Prompt: "Perform the task."}
	agentText, _ := toml.Marshal(agent)
	agents := []AgentRecord{{AgentDefinition: agent, ID: "worker", Revision: revision(agentText)}}
	c, err := compileGraph(g, agents, func(EffectiveGraphAgent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	text, _ := yaml.Marshal(c.Definition)
	sources := map[string]GraphSourceSnapshot{graphFile("retry"): {Text: string(text), Revision: revision(text)}, agentFile("worker"): {Text: string(agentText), Revision: revision(agentText)}}
	encoded, _ := json.Marshal(sources)
	snapshot := GraphSnapshot{GraphID: "retry", Revision: revision(encoded), CapturedAt: now(), Graph: GraphRecord{ID: "retry", Revision: revision(text), Definition: c.Definition}, Agents: agents, EffectiveAgents: c.Agents, Sources: sources}
	stamp := now()
	d := diskState{Version: 2, Native: map[string]nativeSession{}, Projects: []Project{{ID: "project", Folder: t.TempDir()}},
		Sessions:           []Session{{ID: "chat", ProjectID: "project", SelectedGraphID: "retry", Events: []Event{{ID: "user", Type: "user", Text: "Run this task"}}}},
		GraphActivities:    []GraphActivity{{ID: "activity", ProjectID: "project", ConversationID: "chat", GraphID: "retry", Objective: "Task", Status: "awaiting_user", Version: 1, Authorization: []GraphAuthorization{{EventID: "user", Text: "Run this task"}}, Workspace: GraphWorkspaceDecision{Mode: "original"}, CreatedAt: stamp, UpdatedAt: stamp}},
		GraphRuns:          []GraphRun{{ID: "source", ProjectID: "project", ConversationID: "chat", ActivityID: "activity", GraphID: "retry", Input: GraphRunInput{Task: "Task"}, Snapshot: snapshot, Workspace: GraphWorkspaceDecision{Mode: "original"}, InitialWorkspaceID: "workspace", Status: "failed", Result: &GraphRunResult{Kind: "failed"}, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp, EndedAt: stamp}},
		GraphWorkspaces:    []WorkspaceRecord{{ID: "workspace", ProjectID: "project", Directory: t.TempDir(), Status: "ready", OperationID: "workspace-op", CreatedByRunID: "source", CreatedAt: stamp}},
		GraphWorkspaceUses: []WorkspaceUse{{ID: "use", RunID: "source", WorkspaceID: "workspace", Association: "initial", CreatedAt: stamp}},
		GraphNotifications: []RunNotification{{ID: "notice", RunID: "source", ConversationID: "chat", Status: "pending", CreatedAt: stamp}},
	}
	makeActivation := func(id, node, status, choice string) GraphActivation {
		x := GraphActivation{ID: id, RunID: "source", NodeID: node, Occurrence: 1, Status: status, Input: map[string]string{"task": node}, WorkspaceID: "workspace", OriginWorkspaceID: "workspace", CausalID: "run:source", CreatedAt: stamp, UpdatedAt: stamp, EndedAt: stamp}
		if choice != "" {
			x.Submission = &GraphChoiceSubmission{OperationID: id + "-choice", TurnID: id + "-turn", Choice: ChoiceIdentity{Origin: "graph", ID: choice}, Payload: map[string]string{}, ReceivedAt: stamp, AcceptedAt: stamp}
		}
		return x
	}
	d.GraphActivations = []GraphActivation{makeActivation("a1", "a", "completed", "next"), makeActivation("b1", "b", "failed", "")}
	addDelivery := func(id, producer, edge, node, target, round string) {
		d.GraphDeliveries = append(d.GraphDeliveries, GraphDelivery{ID: id, RunID: "source", ProducerActivationID: producer, ConnectionID: edge, TargetNodeID: node, TargetActivationID: target, JoinRoundID: round, Payload: map[string]string{}, WorkspaceID: "workspace", OriginWorkspaceID: "workspace", CausalID: "run:source", Status: "consumed", CreatedAt: stamp})
	}
	if parallel {
		fork, left := makeActivation("fork1", "fork", "completed", ""), makeActivation("left1", "left", "completed", "left-done")
		left.ForkActivationID, left.BranchID = fork.ID, "left"
		d.GraphActivations[1].ForkActivationID, d.GraphActivations[1].BranchID = fork.ID, "right"
		d.GraphActivations = append(d.GraphActivations, fork, left)
		addDelivery("a-fork", "a1", "choice:next", "fork", "fork1", "")
		addDelivery("fork-left", "fork1", "fork:fork:left", "left", "left1", "")
		addDelivery("fork-right", "fork1", "fork:fork:right", "b", "b1", "")
		addDelivery("left-join", "left1", "choice:left-done", "join", "", "round1")
		d.GraphJoinRounds = []JoinRound{{ID: "round1", RunID: "source", NodeID: "join", Occurrence: 1, CausalID: "run:source", Expected: c.Incoming["join"], Arrivals: []JoinArrival{{ConnectionID: "choice:left-done", DeliveryID: "left-join"}}, Status: "interrupted", OriginWorkspaceID: "workspace", CreatedAt: stamp, EndedAt: stamp}}
	} else {
		addDelivery("a-b", "a1", "choice:next", "b", "b1", "")
	}
	if err := validateGraphRecords(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestGraphRetryRetainsPrefixAndRejoinsParallelResults(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		d := retryFixture(t, parallel)
		before, _ := json.Marshal(d.GraphRuns[0])
		run, err := retryGraphCheckpoint(&d, d.GraphRuns[0], "b1", "click")
		if err != nil {
			t.Fatal(err)
		}
		var retry *GraphActivation
		for i := range d.GraphActivations {
			x := &d.GraphActivations[i]
			if x.RunID != run.ID {
				continue
			}
			if x.NodeID == "b" {
				retry = x
			} else if x.Status != "completed" {
				t.Fatalf("replayed completed node %s", x.NodeID)
			}
		}
		if retry == nil || retry.Status != "reserved" || !reflect.DeepEqual(retry.Input, d.graphActivation("b1").Input) {
			t.Fatal("retry lost the selected input")
		}
		if err = validateGraphRecords(&d); err != nil {
			t.Fatal(err)
		}
		if parallel {
			c, _ := compileGraphSnapshot(run.Snapshot)
			retry.Status, retry.EndedAt = "completed", now()
			retry.Submission = &GraphChoiceSubmission{OperationID: "retry-choice", TurnID: "retry-turn", Choice: ChoiceIdentity{Origin: "graph", ID: "right-done"}, Payload: map[string]string{}, ReceivedAt: now(), AcceptedAt: now()}
			addGraphDelivery(&d, d.graphRun(run.ID), *retry, c.Connections["choice:right-done"], map[string]string{}, retry.WorkspaceID, retry.OriginWorkspaceID, retry.CausalID, retry.ForkActivationID, retry.BranchID)
			if err = consumeGraphDeliveries(&d, d.graphRun(run.ID), c); err != nil {
				t.Fatal(err)
			}
			joins := 0
			for _, x := range d.GraphActivations {
				if x.RunID == run.ID && x.NodeID == "join" && x.Status == "reserved" {
					joins++
				}
			}
			if joins != 1 {
				t.Fatal("retry did not combine the retained branch and new result exactly once")
			}
		}
		after, _ := json.Marshal(d.GraphRuns[0])
		if string(before) != string(after) {
			t.Fatal("retry rewrote source run")
		}
		encoded, _ := json.Marshal(d)
		var restored diskState
		if err = json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if err = validateGraphRecords(&restored); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGraphStopPersistsAndPreventsDispatch(t *testing.T) {
	d := retryFixture(t, false)
	d.GraphNotifications = nil
	d.GraphRuns[0].Status, d.GraphRuns[0].EndedAt, d.GraphRuns[0].Result = "running", "", nil
	d.GraphActivities[0].Status = "running"
	d.GraphActivations[1].Status, d.GraphActivations[1].EndedAt = "reserved", ""
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{dir: t.TempDir(), state: d, graphRuns: map[string]*graphExecution{"source": {ctx: ctx, cancel: cancel}}}
	req := httptest.NewRequest("POST", "/", nil)
	req.SetPathValue("id", "chat")
	req.SetPathValue("runId", "source")
	req.SetPathValue("control", "stop")
	for range 2 {
		w := httptest.NewRecorder()
		a.controlGraphRun(w, req)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if ctx.Err() == nil || !a.state.GraphRuns[0].StopRequested {
		t.Fatal("stop failed to persist intent and cancel owned work")
	}
	if len(reserveGraphWorkers(&a.state, a.state.graphRun("source"), map[string]bool{})) != 0 {
		t.Fatal("stop dispatched pending work")
	}
	if err := a.finishGraphRunLocked("source"); err != nil {
		t.Fatal(err)
	}
	if a.state.GraphActivities[0].Status != "awaiting_user" || a.state.GraphRuns[0].Status != "interrupted" {
		t.Fatal("stop lost human recovery boundary")
	}
	if projection := a.state.graphProjection("chat"); projection.Run != nil || projection.LastRun == nil {
		t.Fatal("ended run is unavailable for reading/retry")
	}
}

func TestGraphRetryInvalidatesOnlyCausalSuccessors(t *testing.T) {
	d := retryFixture(t, true)
	run, err := retryGraphCheckpoint(&d, d.GraphRuns[0], "a1", "retry-start")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, x := range d.GraphActivations {
		if x.RunID == run.ID {
			count++
			if x.NodeID != "a" || x.Status != "reserved" {
				t.Fatal("stale descendant survived retry")
			}
		}
	}
	if count != 1 {
		t.Fatal("entry retry did not start at selected activation")
	}
	if err := validateGraphRecords(&d); err != nil {
		t.Fatal(err)
	}
}

func TestAuthoringCatalogCheck(t *testing.T) {
	root := os.Getenv("KLM_CHECK_AUTHORING_DIR")
	if root == "" {
		t.Skip("optional local catalog syntax/preflight check")
	}
	catalog, err := loadAuthoring(Project{Folder: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Errors) > 0 {
		t.Fatal(catalog.Errors)
	}
	graphID := os.Getenv("KLM_CHECK_GRAPH_ID")
	if graphID == "" {
		graphID = "ciclo-desenvolvimento-pr"
	}
	for _, graph := range catalog.Graphs {
		if graph.ID != graphID {
			continue
		}
		if _, err := compileGraph(graph.Definition, catalog.Agents, func(EffectiveGraphAgent) error { return nil }); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("graph %s missing", graphID)
}
