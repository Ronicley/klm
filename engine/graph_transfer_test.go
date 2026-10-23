package main

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func transferFixture() GraphTransfer {
	shared := false
	limit := GraphTraversalLimit(3)
	return GraphTransfer{
		Format: "klm.graph", Version: 1,
		Definition: GraphDefinition{Name: "Delivery", Enabled: true, InitialNode: "start", Nodes: map[string]GraphNode{
			"start":   {Type: "agent", Name: "Plan", Agent: "worker", Choices: []string{"next"}},
			"command": {Type: "terminal", Name: "Check", Command: "echo ready", To: "fork"},
			"fork":    {Type: "fork", Name: "Parallel", Branches: map[string]GraphBranch{"main": {Name: "Main", To: "join", SeparateWorktree: &shared}}},
			"join":    {Type: "join", Agent: "worker", Prompt: "Integrate", Choices: []string{"done"}},
		}, Choices: map[string]GraphChoice{
			"next": {Name: "next", To: "command", MaxTraversals: &limit},
			"done": {Name: "done", Terminal: true},
		}},
		Layout: GraphLayout{Positions: map[string]Point{"start": {X: -100, Y: 20}, "choice:done": {X: 900, Y: 300}}},
		Agents: map[string]AgentDefinition{"worker": {Name: "Worker", Enabled: true, DefaultHarness: "codex", Model: "model-id", Prompt: "Implement <task> & verify."}},
	}
}

func TestGraphTransferRoundTrip(t *testing.T) {
	want := transferFixture()
	if err := want.validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got GraphTransfer
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := got.validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("transfer lost graph, layout or agent configuration")
	}
	if got.Definition.Nodes["fork"].Branches["main"].Isolated() {
		t.Fatal("shared branch changed to isolated")
	}
}

func TestGraphTransferConflicts(t *testing.T) {
	b := transferFixture()
	existing := b.Agents["worker"]
	existing.Prompt = "Existing project prompt"
	c := AuthoringCatalog{Agents: []AgentRecord{{ID: "worker", AgentDefinition: existing}}, Graphs: []GraphRecord{{ID: "delivery"}}}
	got := prepareGraphImport(b, c)
	if got.Definition.Name != "Delivery (2)" || got.Definition.Nodes["start"].Agent != "worker-2" || got.Definition.Nodes["join"].Agent != "worker-2" {
		t.Fatalf("conflicts not remapped: %+v", got)
	}
	if got.Agents["worker-2"].Prompt != "Implement <task> & verify." || c.Agents[0].Prompt != existing.Prompt {
		t.Fatal("agent prompt was overwritten")
	}
	if err := got.validate(); err != nil {
		t.Fatal(err)
	}
	b = transferFixture()
	c.Agents[0].AgentDefinition = b.Agents["worker"]
	got = prepareGraphImport(b, c)
	if _, ok := got.Agents["worker"]; !ok {
		t.Fatal("identical agent was not reused")
	}
}

func TestGraphTransferRejectsInvalid(t *testing.T) {
	for _, change := range []func(*GraphTransfer){
		func(b *GraphTransfer) { b.Version = 2 },
		func(b *GraphTransfer) { delete(b.Agents, "worker") },
		func(b *GraphTransfer) { b.Definition.Choices["next"] = GraphChoice{Name: "next", To: "missing"} },
		func(b *GraphTransfer) { b.Layout.Positions["missing"] = Point{} },
		func(b *GraphTransfer) { b.Layout.Positions["start"] = Point{X: 1e10} },
	} {
		b := transferFixture()
		change(&b)
		if err := b.validate(); err == nil {
			t.Fatal("invalid transfer accepted")
		}
	}
	data, _ := json.Marshal(transferFixture())
	for _, body := range []string{strings.Replace(string(data), `"version":1`, `"version":"1"`, 1), strings.TrimSuffix(string(data), "}") + `,"unknown":true}`, string(data) + `{}`} {
		var b GraphTransfer
		if decode(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(body)), &b) {
			t.Fatal("invalid JSON accepted")
		}
	}
}
