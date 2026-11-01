package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func sqliteRunningGraph(t *testing.T, status string) (*app, Session, GraphRun) {
	t.Helper()
	a, s := sqliteAutomationApp(t)
	snapshot := sqliteSemanticFixture(t).GraphRuns[0].Snapshot
	r := GraphRun{ID: "run", ActivityID: "activity", ProjectID: s.ProjectID, ConversationID: s.ID, GraphID: snapshot.GraphID, Input: GraphRunInput{Task: "Isolated runtime fixture"}, Workspace: GraphWorkspaceDecision{Mode: "original"}, Status: status, Revision: 1, InitialWorkspaceID: "workspace", CreatedAt: now(), UpdatedAt: now()}
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		v := GraphActivity{ID: r.ActivityID, ConversationID: s.ID, ProjectID: s.ProjectID, GraphID: r.GraphID, Task: r.Input.Task, Objective: "Fixture", Workspace: r.Workspace, Status: "running", Version: 1, CreatedAt: now(), UpdatedAt: now()}
		if err := st.putRoot("graph_activities", v.ID, v); err != nil {
			return err
		}
		if err := st.putRoot("graph_runs", r.ID, r); err != nil {
			return err
		}
		if err := st.putSingleChild("graph_snapshots", r.ID, snapshot); err != nil {
			return err
		}
		w := WorkspaceRecord{ID: r.InitialWorkspaceID, ProjectID: s.ProjectID, Directory: t.TempDir(), Status: "ready", CreatedByRunID: r.ID, OperationID: "workspace", CreatedAt: now()}
		return st.putRoot("graph_workspaces", w.ID, w)
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, s, r
}
func TestSQLiteGraphChoiceSettlement(t *testing.T) {
	a, _, r := sqliteRunningGraph(t, "running")
	ctx := a.ctx
	x := GraphActivation{ID: "activation", RunID: r.ID, NodeID: "agent", Occurrence: 1, Status: "running", CurrentTurnID: "turn", WorkspaceID: r.InitialWorkspaceID, CreatedAt: now(), UpdatedAt: now()}
	_, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.putRoot("graph_activations", x.ID, x) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.reserveGraphChoice(ctx, r.ID, x.ID, "wrong-turn", "choice", json.RawMessage(`{"choice":{"origin":"graph","id":"done"},"payload":{}}`)); err == nil {
		t.Fatal("stale native turn reserved Choice")
	}
	if _, err := a.reserveGraphChoice(ctx, r.ID, x.ID, "turn", "choice", json.RawMessage(`{"choice":{"origin":"graph","id":"done"},"payload":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := a.transitionGraphChoice(ctx, r.ID, x.ID, "drained"); err == nil {
		t.Fatal("out-of-order Choice drainage accepted")
	}
	for _, stage := range []string{"sealed", "drained", "accepted"} {
		if err := a.transitionGraphChoice(ctx, r.ID, x.ID, stage); err != nil {
			t.Fatal(stage, err)
		}
	}
	a.mu.Lock()
	a.completeGraphActivationLocked(r.ID, graphCompletion{activationID: x.ID, turnID: "turn", drained: true, native: &GraphAdapterResult{Outcome: "choice", ChoiceState: "accepted", ProcessesDrained: true}})
	err = a.finishGraphRunLocked(r.ID)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var saved GraphRun
	if err := decodeSQLiteRecord(ctx, a.store.readers, "graph_runs", r.ID, 4<<20, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Status != "completed" || saved.Result == nil || saved.Result.Kind != "completed" {
		t.Fatalf("graph outcome: %+v", saved)
	}
	var notices int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_graph_notifications WHERE x_runId=?`, r.ID).Scan(&notices); err != nil || notices != 1 {
		t.Fatal("terminal notification", err)
	}
	if len(a.state.GraphRuns) != 0 || a.storageErr != nil {
		t.Fatal("legacy state fallback", a.storageErr)
	}
}
func TestSQLiteGraphRuntimeFailureSettlement(t *testing.T) {
	a, s, r := sqliteRunningGraph(t, "starting")
	// Keep the unrelated owning chat occupied; this case inspects the queued
	// graph result without dispatching a second, synthetic conversation turn.
	a.runs[s.ID] = &turn{ctx: a.ctx}
	ctx, cancel := context.WithCancel(a.ctx)
	g := &graphExecution{ctx: ctx, cancel: cancel, done: make(chan struct{}), completed: make(chan graphCompletion), wake: make(chan struct{}, 1)}
	a.graphRuns = map[string]*graphExecution{r.ID: g}
	a.wg.Add(1)
	go a.runGraph(r.ID, g)
	select {
	case <-g.done:
	case <-time.After(10 * time.Second):
		t.Fatal("SQL graph runtime did not settle")
	}
	a.wg.Wait()
	var saved GraphRun
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "graph_runs", r.ID, 4<<20, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Status != "failed" || saved.Result == nil || saved.Result.Error == "" {
		t.Fatalf("missing-harness graph outcome: %+v", saved)
	}
	var active int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_graph_activations WHERE status IN ('reserved','running','waiting_user','sealing')`).Scan(&active); err != nil || active != 0 {
		t.Fatal("graph left live activations", err)
	}
	if len(a.state.GraphRuns) != 0 || len(a.state.Sessions) != 0 || a.storageErr != nil {
		t.Fatal("legacy state fallback", a.storageErr)
	}
}

func TestSQLiteGraphHTTPPages(t *testing.T) {
	if !developmentBuild {
		t.Skip("dev-only auth bypass")
	}
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "1")
	a, s, r := sqliteRunningGraph(t, "running")
	var err error
	a.clientAuth, err = newClientAuthStorage(a.dir, a.store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		node := Session{ID: "node", ProjectID: s.ProjectID, Role: "graph_node", GraphRunID: r.ID, GraphNodeID: "agent", Harness: "pi", Status: "running", ExecutionCWD: t.TempDir()}
		if err := st.createSession(node, a.dir); err != nil {
			return err
		}
		x := GraphActivation{ID: "activation", RunID: r.ID, NodeID: "agent", Occurrence: 1, SessionID: node.ID, Status: "running", WorkspaceID: r.InitialWorkspaceID, CreatedAt: now(), UpdatedAt: now()}
		if err := st.putRoot("graph_activations", x.ID, x); err != nil {
			return err
		}
		if _, err := st.appendEvent(node.ID, Event{ID: "node-event", Type: "assistant", Text: "Fixture output"}); err != nil {
			return err
		}
		return st.putRecord("pending_permissions", sqliteOwnedKey(node.ID, "permission"), node.ID, "permission", -1, Permission{ID: "permission", Title: "Fixture permission", Decisions: []string{"once", "reject"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := a.routes()
	get := func(path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", "http://localhost"+path, nil)
		req.Header.Set("X-KLM-Read-Contract", sqliteReadContract)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, res.Code, res.Body.String())
		}
		var value map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	view := get("/api/sessions/" + s.ID + "/graph")
	if len(view["requests"].([]any)) != 1 || view["revision"] == nil {
		t.Fatal("active graph projection missing request", view)
	}
	run := get("/api/graph-runs/" + r.ID)
	if run["revision"] != "1" {
		t.Fatal("run revision is not decimal", run)
	}
	activity := get("/api/sessions/" + s.ID + "/graph/runs/" + r.ID + "/nodes/agent/activity")
	if len(activity["activations"].([]any)) != 1 || len(activity["activation"].(map[string]any)["events"].([]any)) != 1 {
		t.Fatal("node activity page missing content", activity)
	}
}
