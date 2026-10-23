package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func graphLimitFixture(t *testing.T, limited bool) *app {
	t.Helper()
	d := retryFixture(t, false)
	g := d.GraphRuns[0].Snapshot.Graph.Definition
	g.Nodes["b"] = GraphNode{Type: "agent", Name: "Review", Agent: "worker", Choices: []string{"request-fix", "done"}}
	limit := GraphTraversalLimit(3)
	choice := GraphChoice{Name: "request_fix", To: "a", Session: "continue_target", Input: map[string]ChoiceField{
		"repositories": {Type: "string", Required: true}, "findings": {Type: "string", Required: true},
	}, Output: map[string]string{"repositories": "{{choice.repositories}}", "findings": "{{choice.findings}}"}}
	if limited {
		choice.MaxTraversals = &limit
	}
	g.Choices["request-fix"] = choice
	snapshot := &d.GraphRuns[0].Snapshot
	c, err := compileGraph(g, snapshot.Agents, func(EffectiveGraphAgent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	text, err := yaml.Marshal(c.Definition)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Graph.Definition, snapshot.Graph.Revision, snapshot.EffectiveAgents = c.Definition, revision(text), c.Agents
	snapshot.Sources[graphFile(snapshot.GraphID)] = GraphSourceSnapshot{Text: string(text), Revision: revision(text)}
	encoded, _ := json.Marshal(snapshot.Sources)
	snapshot.Revision = revision(encoded)
	d.GraphNotifications = nil
	d.GraphActivities[0].Status = "running"
	d.GraphRuns[0].Status, d.GraphRuns[0].Result, d.GraphRuns[0].EndedAt = "running", nil, ""
	d.GraphActivations[1].Status, d.GraphActivations[1].EndedAt = "reserved", ""
	if err := validateGraphRecords(&d); err != nil {
		t.Fatal(err)
	}
	return &app{ctx: context.Background(), dir: t.TempDir(), state: d}
}

func completeLimitChoice(t *testing.T, a *app, runID, choiceID string) graphCompletion {
	t.Helper()
	activationID, turnID := "", newID()
	err := a.graphChangeLocked(func(d *diskState) error {
		for i := range d.GraphActivations {
			x := &d.GraphActivations[i]
			if x.RunID != runID || x.Status != "reserved" {
				continue
			}
			activationID = x.ID
			payload := map[string]string{}
			if choiceID == "request-fix" {
				payload = map[string]string{"repositories": `[{"pr_url":"https://github.com/example/app/pull/1"}]`, "findings": "Fix the regression."}
			}
			x.Status, x.AdapterState, x.CurrentTurnID = "sealing", "drained", turnID
			x.Submission = &GraphChoiceSubmission{OperationID: newID(), TurnID: turnID, Choice: ChoiceIdentity{Origin: "graph", ID: choiceID}, Payload: payload, ReceivedAt: now()}
			break
		}
		return nil
	})
	if err != nil || activationID == "" {
		t.Fatalf("reserve %s: activation=%s err=%v", choiceID, activationID, err)
	}
	if err := a.transitionGraphChoice(context.Background(), runID, activationID, "accepted"); err != nil {
		t.Fatal(err)
	}
	result := graphCompletion{activationID: activationID, turnID: turnID, drained: true, native: &GraphAdapterResult{Outcome: "choice", ChoiceState: "accepted"}}
	a.completeGraphActivationLocked(runID, result)
	if a.storageErr != nil {
		t.Fatal(a.storageErr)
	}
	return result
}

func TestGraphTransitionLimitDispatchRecoveryAndRetry(t *testing.T) {
	a := graphLimitFixture(t, true)
	for round := 1; round <= 3; round++ {
		completion := completeLimitChoice(t, a, "source", "request-fix")
		// A duplicate native completion must not dispatch or consume another round.
		a.completeGraphActivationLocked("source", completion)
		limit := a.state.graphTransitionLimits(a.state.graphRun("source"))["request-fix"]
		if limit.Traversals != round || limit.Remaining != 3-round || a.state.graphRun("source").Status != "running" {
			t.Fatalf("round %d: %+v, %+v", round, limit, a.state.graphRun("source").Result)
		}
		completeLimitChoice(t, a, "source", "next")
	}
	// Reload persisted records: usage is derived, never an in-memory counter.
	encoded, err := json.Marshal(a.state)
	if err != nil {
		t.Fatal(err)
	}
	var restored diskState
	if err = json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if err = validateGraphRecords(&restored); err != nil {
		t.Fatal(err)
	}
	a = &app{ctx: context.Background(), dir: t.TempDir(), state: restored}
	before := len(a.state.GraphDeliveries)
	last := completeLimitChoice(t, a, "source", "request-fix")
	run := a.state.graphRun("source")
	if run.Status != "ending" || run.Result == nil || run.Result.Kind != "blocked" || run.Result.Output["status"] != "transition_limit_reached" {
		t.Fatalf("limit did not block: %+v", run)
	}
	if len(a.state.GraphDeliveries) != before || !strings.Contains(run.Result.Output["repositories"], "/pull/1") || run.Result.Output["findings"] != "Fix the regression." {
		t.Fatal("fourth correction dispatched or final evidence lost")
	}
	if err = a.finishGraphRunLocked("source"); err != nil {
		t.Fatal(err)
	}
	var retry GraphRun
	if err = a.graphChangeLocked(func(d *diskState) error {
		var err error
		retry, err = retryGraphCheckpoint(d, *d.graphRun("source"), last.activationID, "retry-review")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if limit := a.state.graphTransitionLimits(&retry)["request-fix"]; limit.Traversals != 3 || limit.Remaining != 0 {
		t.Fatalf("retry reset or doubled usage: %+v", limit)
	}
	completeLimitChoice(t, a, retry.ID, "request-fix")
	if got := a.state.graphRun(retry.ID).Result; got == nil || got.Kind != "blocked" {
		t.Fatalf("retry bypassed limit: %+v", got)
	}
}

func TestGraphTransitionLimitDoesNotBlockSuccessOrUnlimitedGraphs(t *testing.T) {
	for _, limited := range []bool{true, false} {
		a := graphLimitFixture(t, limited)
		rounds := 4
		if limited {
			rounds = 3
		}
		for range rounds {
			completeLimitChoice(t, a, "source", "request-fix")
			completeLimitChoice(t, a, "source", "next")
		}
		completeLimitChoice(t, a, "source", "done")
		if got := a.state.graphRun("source").Result; got == nil || got.Kind != "completed" {
			t.Fatalf("limited=%v: success blocked: %+v", limited, got)
		}
	}
}

func TestGraphTransitionLimitValidation(t *testing.T) {
	for _, raw := range []string{"0", "-1", "1.5", "2147483648", `"3"`} {
		for _, format := range []string{"json", "yaml"} {
			var choice GraphChoice
			var err error
			if format == "json" {
				err = json.Unmarshal([]byte(`{"name":"next","to":"command","max_traversals":`+raw+`}`), &choice)
			} else {
				err = yaml.Unmarshal([]byte("name: next\nto: command\nmax_traversals: "+raw), &choice)
			}
			if err == nil {
				bundle := transferFixture()
				bundle.Definition.Choices["next"] = choice
				err = bundle.validate()
			}
			if err == nil {
				t.Fatalf("accepted %s limit %s", format, raw)
			}
		}
	}
	bundle := transferFixture()
	choice := bundle.Definition.Choices["next"]
	choice.Terminal, choice.To = true, ""
	bundle.Definition.Choices["next"] = choice
	if bundle.validate() == nil {
		t.Fatal("terminal Choice accepted transition limit")
	}
}
