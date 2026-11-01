package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

// Local coordinator/storage fixture only: no native process/provider, scheduler,
// registered graph invocation or worktree. Native finality flags are fixture data.
func TestDelegationGraphJoinChoice(t *testing.T) {
	a, visible, project, definition := agentBindingFixture(t)
	graph := GraphDefinition{Name: "Delegation fixture", Enabled: true, InitialNode: "node", Nodes: map[string]GraphNode{"node": {Type: "agent", Name: "Parent node", Agent: authoringSlug(definition.Name), Choices: []string{"done"}}}, Choices: map[string]GraphChoice{"done": {Name: "done", Terminal: true, Input: map[string]ChoiceField{}, Output: map[string]string{}}}}
	raw, _ := json.Marshal(map[string]any{"definition": graph})
	r := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
	r.SetPathValue("id", project.ID)
	r.SetPathValue("item", "delegation-fixture")
	w := httptest.NewRecorder()
	a.saveGraph(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	snapshot, _, err := a.captureGraphSnapshot(a.ctx, project.ID, "delegation-fixture")
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := CanonicalGraphDirectory(project.Folder)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	scope := DelegationGraphScope{RunID: "fixture-run", NodeID: "node", ActivationID: "fixture-activation", TurnID: "fixture-turn", WorkspaceID: "fixture-workspace", SnapshotDigest: snapshot.Revision}
	parent := Session{ID: "fixture-node", ProjectID: project.ID, Role: "graph_node", GraphRunID: scope.RunID, GraphNodeID: scope.NodeID, ExecutionCWD: cwd, Harness: "opencode", Model: definition.Model, Effort: definition.Effort, Title: "Parent node", Status: "running", CreatedAt: now(), UpdatedAt: now()}
	_, err = a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if err := st.createSession(parent, a.dir); err != nil {
			return err
		}
		if err := st.putRoot("graph_runs", scope.RunID, GraphRun{ID: scope.RunID, ConversationID: visible.ID, ProjectID: project.ID, GraphID: "delegation-fixture", Status: "running", CreatedAt: now(), UpdatedAt: now()}); err != nil {
			return err
		}
		if err := st.putSingleChild("graph_snapshots", scope.RunID, snapshot); err != nil {
			return err
		}
		if err := st.putRoot("graph_workspaces", scope.WorkspaceID, WorkspaceRecord{ID: scope.WorkspaceID, ProjectID: project.ID, Directory: cwd, CreatedByRunID: scope.RunID, OperationID: "fixture-workspace", Status: "ready", CreatedAt: now()}); err != nil {
			return err
		}
		if err := st.putRoot("graph_workspace_uses", "fixture-use", WorkspaceUse{ID: "fixture-use", WorkspaceID: scope.WorkspaceID, RunID: scope.RunID, ActivationID: scope.ActivationID, Association: "initial"}); err != nil {
			return err
		}
		if err := st.putRoot("graph_activations", scope.ActivationID, GraphActivation{ID: scope.ActivationID, RunID: scope.RunID, NodeID: scope.NodeID, SessionID: parent.ID, WorkspaceID: scope.WorkspaceID, CurrentTurnID: scope.TurnID, Status: "running", AdapterState: "open", CreatedAt: now(), UpdatedAt: now()}); err != nil {
			return err
		}
		return st.putRoot("native_sessions", parent.ID, nativeSession{ID: "fixture-native-parent"})
	})
	if err != nil {
		t.Fatal(err)
	}
	tur := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{}), graphDelegation: &graphDelegationBinding{Snapshot: snapshot, Scope: scope, OwnerID: visible.ID}}
	if err := BindGraphAdapter(tur, GraphAdapterHooks{Node: true, ReserveChoice: func(ctx context.Context, id string, raw json.RawMessage) (any, error) {
		return a.reserveSQLGraphChoice(ctx, scope.RunID, scope.ActivationID, scope.TurnID, id, raw)
	}, Transition: func(ctx context.Context, stage string) error {
		return a.transitionSQLGraphChoice(ctx, scope.RunID, scope.ActivationID, stage)
	}, Finished: func(GraphAdapterResult) {}}); err != nil {
		t.Fatal(err)
	}
	defer UnbindGraphAdapter(tur)
	p := &adapter{app: a, id: parent.ID, role: "graph_node", harness: parent.Harness, model: parent.Model, effort: parent.Effort, cwd: cwd, turn: tur, graph: graphBinding(tur), completed: true, nativeSettled: true, processesDrained: true}
	a.runs[parent.ID] = tur
	release := make(chan struct{})
	arrived := make(chan struct{}, 2)
	controller, err := newDelegationExecution(p, func(ctx context.Context, item DelegationInvocationRecord) delegationChildOutcome {
		a.mu.Lock()
		err := a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
			current, err := st.delegationInvocation(item.ID)
			if err != nil {
				return err
			}
			current.NativeAccepted = true
			current.NativeSessionID = "fixture-child-" + item.Key
			current.State = DelegationInvocationRunning
			current.StartedAt = now()
			return st.putDelegationInvocation(current)
		})
		a.mu.Unlock()
		if err != nil {
			return delegationChildOutcome{Outcome: DelegationFailed, Error: err.Error()}
		}
		arrived <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return delegationChildOutcome{Outcome: DelegationCompleted, Result: "fixture-output", Finality: DelegationFinality{NativeSettled: true, ProcessesDrained: true, ToolCallsSettled: true}}
	})
	if err != nil {
		t.Fatal(err)
	}
	p.delegation, tur.delegation = controller, controller
	t.Cleanup(func() { cancel(); _ = controller.close() })
	// Remove live discovery access after capture. Preparation must still work.
	a.catalogs = nil
	low := "low"
	prepared, err := controller.prepare(DelegationRunRequest{OperationID: "graph-children", Children: []DelegationChildRequest{{Key: "specialist", AgentID: authoringSlug(definition.Name), Effort: &low, Task: "Captured specialist task"}, {Key: "unnamed", Task: "Captured parent settings, empty Library"}}}, "fixture-call", "")
	if err != nil {
		t.Fatal(err)
	}
	for prepared.Preparing && err == nil {
		prepared, err = controller.prepared(ctx, prepared.PreparationID, "fixture-call", "")
	}
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	choice := json.RawMessage(`{"choice":{"origin":"graph","id":"done"},"payload":{}}`)
	if _, err := p.callGraphTool(ctx, "graph_submit_choice", "early", choice); err == nil {
		t.Fatal("Choice accepted before child join")
	}
	close(release)
	revision := ""
	for {
		reply, err := controller.wait(ctx, prepared.Receipt.BatchID, revision, "fixture-call", "")
		if err != nil {
			t.Fatal(err)
		}
		revision = reply.Revision
		if reply.State == "settled" {
			break
		}
		if reply.StopRequired {
			t.Fatal("fixture join unconfirmed")
		}
	}
	if _, err := p.callGraphTool(ctx, "graph_submit_choice", "before-delivery", choice); err == nil {
		t.Fatal("Choice accepted before native delivery")
	}
	if err := controller.nativeResultSeen("fixture-call", ""); err != nil {
		t.Fatal(err)
	}
	p.graph.choiceAdmitted, p.graph.gateSeen = true, true
	if _, err := p.callGraphTool(ctx, "graph_submit_choice", "after-delivery", choice); err != nil {
		t.Fatal(err)
	}
	p.choiceToolSettled = true
	if result := p.finishGraphAdapter(nil); result.Outcome != "choice" {
		t.Fatalf("Choice did not join/settle: %+v", result)
	}
	if _, err := controller.prepare(DelegationRunRequest{OperationID: "after-choice", Children: []DelegationChildRequest{{Key: "no", Task: "Not admitted"}}}, "later-call", ""); err == nil {
		t.Fatal("Children admitted after Choice")
	}
	if err := controller.close(); err != nil {
		t.Fatal(err)
	}
	err = a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		items, err := st.delegationMembers(prepared.Receipt.BatchID)
		if err != nil {
			return err
		}
		if len(items) != 2 {
			return errors.New("partial membership")
		}
		for _, item := range items {
			if item.Key == "specialist" && item.Capture.Config.Effort != "low" {
				return errors.New("Frozen model effort override was not applied")
			}
			if item.Graph == nil || item.Graph.ActivationID != scope.ActivationID || item.Graph.WorkspaceID != scope.WorkspaceID || item.ParentTurnID != scope.TurnID {
				return errors.New("graph ownership lost")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("id", visible.ID)
	r.SetPathValue("runId", scope.RunID)
	r.SetPathValue("activationId", scope.ActivationID)
	a.graphDelegationHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
