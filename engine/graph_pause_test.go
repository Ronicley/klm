package main

import (
	"net/http/httptest"
	"testing"
)

func TestGraphPauseBlocksWorkerDispatchAndCorrections(t *testing.T) {
	r := &GraphRun{ID: "run", Status: "running", PauseRequested: true}
	d := &diskState{GraphActivations: []GraphActivation{
		{ID: "in-flight", RunID: "run", Status: "running"},
		{ID: "successor", RunID: "run", Status: "reserved"},
		{ID: "correction", RunID: "run", Status: "reserved", Correction: "missing-choice.md"},
	}}
	workers := map[string]bool{"in-flight": true}
	if launch := reserveGraphWorkers(d, r, workers); len(launch) != 0 || len(workers) != 1 {
		t.Fatal("pause admitted new work or cancelled an in-flight worker")
	}
	delete(workers, "in-flight")
	if launch := reserveGraphWorkers(d, r, workers); len(launch) != 0 {
		t.Fatal("pause dispatched work after current worker settled")
	}
	r.PauseRequested = false
	if launch := reserveGraphWorkers(d, r, workers); len(launch) != 2 {
		t.Fatal("resume did not admit successor and correction")
	}
	if launch := reserveGraphWorkers(d, r, workers); len(launch) != 0 {
		t.Fatal("resume duplicated already admitted work")
	}
}

func TestGraphPausePreservesDeliveryUntilResume(t *testing.T) {
	d := diskState{
		Sessions:         []Session{{ID: "chat"}},
		GraphRuns:        []GraphRun{{ID: "run", ConversationID: "chat", Status: "running", PauseRequested: true}},
		GraphActivations: []GraphActivation{{ID: "producer", RunID: "run", NodeID: "first", Status: "completed"}},
		GraphDeliveries:  []GraphDelivery{{ID: "delivery", RunID: "run", ConnectionID: "edge", TargetNodeID: "next", Status: "pending", Payload: map[string]string{"task": "retained result"}}},
	}
	c := &CompiledGraph{
		Definition:  GraphDefinition{Nodes: map[string]GraphNode{"next": {Type: "agent"}}},
		Connections: map[string]GraphConnection{"edge": {ID: "edge", To: "next", Session: "new"}},
	}
	r := d.graphRun("run")
	if err := consumeGraphDeliveries(&d, r, c); err != nil {
		t.Fatal(err)
	}
	if len(d.GraphActivations) != 1 || d.GraphDeliveries[0].Status != "pending" {
		t.Fatal("pause dispatched successor or consumed its delivery")
	}
	if p := d.graphProjection("chat"); p.Run.Status != "pausing" || len(p.Run.CompletedNodeIDs) != 1 {
		t.Fatal("pause lost completed work or pausing projection")
	}
	r.Paused = true
	if p := d.graphProjection("chat"); p.Run.Status != "paused" || !p.Run.Active {
		t.Fatal("paused run lost its active ownership")
	}
	r.PauseRequested, r.Paused = false, false
	for range 2 {
		if err := consumeGraphDeliveries(&d, r, c); err != nil {
			t.Fatal(err)
		}
	}
	if len(d.GraphActivations) != 2 || d.GraphActivations[1].Input["task"] != "retained result" || d.GraphDeliveries[0].Status != "consumed" {
		t.Fatal("resume did not deliver exactly once with retained payload")
	}
}

func TestGraphPauseControlOwnershipAndIdempotence(t *testing.T) {
	a := &app{
		state:     diskState{Sessions: []Session{{ID: "owner"}, {ID: "other"}}, GraphRuns: []GraphRun{{ID: "run", ConversationID: "owner", Status: "running", PauseRequested: true}}},
		graphRuns: map[string]*graphExecution{"run": {}},
	}
	for _, tc := range []struct {
		session, control string
		want             int
	}{{"other", "pause", 404}, {"owner", "pause", 200}, {"owner", "pause", 200}, {"owner", "unknown", 404}} {
		req := httptest.NewRequest("POST", "/", nil)
		req.SetPathValue("id", tc.session)
		req.SetPathValue("runId", "run")
		req.SetPathValue("control", tc.control)
		w := httptest.NewRecorder()
		a.controlGraphRun(w, req)
		if w.Code != tc.want {
			t.Fatalf("%s %s: got %d, want %d", tc.session, tc.control, w.Code, tc.want)
		}
	}
	a.state.GraphRuns[0].Status = "ending"
	if err := a.setGraphPausedLocked("run", false); err == nil {
		t.Fatal("ending run resumed")
	}
}
