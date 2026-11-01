package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sqliteRetryFixture(t *testing.T, parallel bool) (*app, Session, GraphRun) {
	t.Helper()
	a, s := sqliteAutomationApp(t)
	d := retryFixture(t, parallel)
	r := d.GraphRuns[0]
	r.ProjectID, r.ConversationID = s.ProjectID, s.ID
	snapshot := r.Snapshot
	r.Snapshot = GraphSnapshot{}
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if _, err := st.appendEvent(s.ID, Event{ID: "user", Type: "user", Text: "Run this task"}); err != nil {
			return err
		}
		activity := d.GraphActivities[0]
		activity.ProjectID, activity.ConversationID, activity.Authorization = s.ProjectID, s.ID, nil
		if err := st.putRoot("graph_activities", activity.ID, activity); err != nil {
			return err
		}
		if err := st.appendScalarChild("graph_authorization", activity.ID, d.GraphActivities[0].Authorization[0]); err != nil {
			return err
		}
		if err := st.putRoot("graph_runs", r.ID, r); err != nil {
			return err
		}
		if err := st.putSingleChild("graph_snapshots", r.ID, snapshot); err != nil {
			return err
		}
		workspace := d.GraphWorkspaces[0]
		workspace.ProjectID = s.ProjectID
		if err := st.putRoot("graph_workspaces", workspace.ID, workspace); err != nil {
			return err
		}
		for _, x := range d.GraphActivations {
			if err := st.putRoot("graph_activations", x.ID, x); err != nil {
				return err
			}
		}
		for _, delivery := range d.GraphDeliveries {
			if err := st.putRoot("graph_deliveries", delivery.ID, delivery); err != nil {
				return err
			}
		}
		for _, round := range d.GraphJoinRounds {
			expected, arrivals := round.Expected, round.Arrivals
			round.Expected, round.Arrivals = nil, nil
			if err := st.putRoot("graph_join_rounds", round.ID, round); err != nil {
				return err
			}
			for _, expected := range expected {
				if err := st.appendScalarChild("graph_join_expected", round.ID, expected); err != nil {
					return err
				}
			}
			for _, arrival := range arrivals {
				if err := st.appendScalarChild("graph_join_arrivals", round.ID, arrival); err != nil {
					return err
				}
			}
		}
		for _, use := range d.GraphWorkspaceUses {
			if err := st.putRoot("graph_workspace_uses", use.ID, use); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	project, err := a.store.readProjectMeta(a.ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range snapshot.Sources {
		path := filepath.Join(project.Folder, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source.Text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return a, s, r
}

func sqlRetryCheckpoint(t *testing.T, a *app, source GraphRun, activation, operation string) GraphRun {
	t.Helper()
	var next GraphRun
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		snapshot, _, err := st.capturedGraph(source.ID)
		if err != nil {
			return err
		}
		next, err = st.retryGraphCheckpoint(source, snapshot, activation, operation)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestSQLiteGraphRetryCausalCheckpoint(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprint(parallel), func(t *testing.T) {
			a, _, source := sqliteRetryFixture(t, parallel)
			next := sqlRetryCheckpoint(t, a, source, "b1", "retry")
			var selected GraphActivation
			err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
				st := &sqliteSessionTx{ctx: a.ctx, tx: tx}
				return st.eachMatching("graph_activations", `x_runId=?`, []any{next.ID}, func(id string) error {
					x, err := st.graphActivation(id)
					if err != nil {
						return err
					}
					if x.SessionID != "" || x.SessionPolicy != "new" || x.CurrentTurnID != "" {
						t.Fatal("retry reused a native session", x)
					}
					if x.NodeID == "b" {
						selected = x
					} else if x.Status != "completed" || x.ReusedFromActivationID == "" {
						t.Fatal("completed causal prefix was replayed", x)
					}
					return nil
				})
			})
			if err != nil || selected.Status != "reserved" || selected.Input["task"] != "b" {
				t.Fatal("retry input/state", selected, err)
			}
			if parallel {
				_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
					_, c, err := st.capturedGraph(next.ID)
					if err != nil {
						return err
					}
					selected.Status, selected.EndedAt = "completed", now()
					selected.Submission = &GraphChoiceSubmission{OperationID: "choice", TurnID: "turn", Choice: ChoiceIdentity{Origin: "graph", ID: "right-done"}, Payload: map[string]string{}, ReceivedAt: now(), AcceptedAt: now()}
					if err := st.putRoot("graph_activations", selected.ID, selected); err != nil {
						return err
					}
					if err := st.addGraphDelivery(next, selected, c.Connections["choice:right-done"], map[string]string{}, selected.WorkspaceID, selected.OriginWorkspaceID, selected.CausalID, selected.ForkActivationID, selected.BranchID); err != nil {
						return err
					}
					for range 2 {
						if err := st.consumeGraphDeliveries(next, c); err != nil {
							return err
						}
					}
					var count, expected, arrivals int
					if err := txGraphCounts(st, next.ID, &count, &expected, &arrivals); err != nil {
						return err
					}
					if count != 1 || expected != 2 || arrivals != 2 {
						t.Fatalf("Join frontier lost or expanded twice: %d/%d/%d", count, expected, arrivals)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			var original GraphRun
			if err := decodeSQLiteRecord(a.ctx, a.store.readers, "graph_runs", source.ID, 4<<20, &original); err != nil || original.Status != "failed" || original.RetrySourceRunID != "" {
				t.Fatal("source run mutated", err)
			}
			if len(a.state.GraphRuns) != 0 || len(a.state.GraphActivations) != 0 {
				t.Fatal("legacy state hydration")
			}
		})
	}
}

func txGraphCounts(st *sqliteSessionTx, run string, activations, expected, arrivals *int) error {
	return st.tx.QueryRowContext(st.ctx, `SELECT (SELECT count(*) FROM data_graph_activations WHERE x_runId=? AND x_nodeId='join'),(SELECT count(*) FROM data_graph_join_expected e JOIN data_graph_join_rounds j ON j.id=e.owner_id WHERE j.x_runId=?),(SELECT count(*) FROM data_graph_join_arrivals a JOIN data_graph_join_rounds j ON j.id=a.owner_id WHERE j.x_runId=?)`, run, run, run).Scan(activations, expected, arrivals)
}

func TestSQLiteGraphRetryLoopsAndIndependentBranches(t *testing.T) {
	a, _, source := sqliteRetryFixture(t, true)
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		x, err := st.graphActivation("left1")
		if err != nil {
			return err
		}
		x.Status, x.Submission = "interrupted", nil
		if err := st.putRoot("graph_activations", x.ID, x); err != nil {
			return err
		}
		// A later visit to the same node is a different causal occurrence.
		later := x
		later.ID, later.NodeID, later.Occurrence = "b2", "b", 2
		if err := st.putRoot("graph_activations", later.ID, later); err != nil {
			return err
		}
		d := GraphDelivery{ID: "loop", RunID: source.ID, ProducerActivationID: "b1", TargetActivationID: "b2", ConnectionID: "loop", TargetNodeID: "b", Payload: map[string]string{}, Status: "consumed"}
		return st.putRoot("graph_deliveries", d.ID, d)
	})
	if err != nil {
		t.Fatal(err)
	}
	next := sqlRetryCheckpoint(t, a, source, "b1", "loop-retry")
	var descendants, independent int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_graph_activations WHERE x_runId=? AND x_nodeId='b' AND x_occurrence='2'`, next.ID).Scan(&descendants); err != nil {
		t.Fatal(err)
	}
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_graph_activations WHERE x_runId=? AND x_nodeId='left' AND status='reserved' AND session_id=''`, next.ID).Scan(&independent); err != nil {
		t.Fatal(err)
	}
	if descendants != 0 || independent != 1 {
		t.Fatal("retry used topology instead of causality or dropped independent branch", descendants, independent)
	}
}

func TestSQLiteGraphRetryReceiptOwnershipAndRace(t *testing.T) {
	a, s, source := sqliteRetryFixture(t, false)
	next := sqlRetryCheckpoint(t, a, source, "b1", "operation")
	request := func(owner, activation, op string, want int) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"activationId": activation, "operationId": op})
		req := httptest.NewRequest("POST", "/", strings.NewReader(string(raw)))
		req.SetPathValue("id", owner)
		req.SetPathValue("runId", source.ID)
		req.SetPathValue("nodeId", map[string]string{"b1": "b", "a1": "a"}[activation])
		w := httptest.NewRecorder()
		a.retryGraphNode(w, req)
		if w.Code != want {
			t.Fatalf("retry %s/%s/%s: %d %s", owner, activation, op, w.Code, w.Body.String())
		}
	}
	request(s.ID, "b1", "operation", 200)
	request(s.ID, "a1", "operation", 409)
	request("other", "b1", "operation", 404)
	request(s.ID, "b1", "new-operation", 409)
	var count int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_graph_runs WHERE x_conversationId=?`, s.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("receipt replay changed run inventory", count, err)
	}
	var stamp string
	if err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: a.ctx, tx: tx}
		var err error
		stamp, err = st.graphRetryRevision(source)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		_, err := st.appendEvent(s.ID, Event{ID: "chat-stream", Type: "assistant", Text: "Independent chat continues."})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
		return (&sqliteSessionTx{ctx: a.ctx, tx: tx}).graphRetryUnchanged(source, stamp)
	}); err != nil {
		t.Fatal("independent conversation events invalidated graph preparation", err)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		workspace, err := st.graphWorkspace(source.InitialWorkspaceID)
		if err != nil {
			return err
		}
		workspace.Directory = t.TempDir()
		return st.putRoot("graph_workspaces", workspace.ID, workspace)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		return st.graphRetryUnchanged(source, stamp)
	})
	var conflict *sessionControlError
	if !errors.As(err, &conflict) || conflict.code != 409 {
		t.Fatal("workspace changed outside lock without invalidating preparation", err)
	}
	if next.ID != sqlGraphRetryID(s.ID, "operation") {
		t.Fatal("receipt identity is not deterministic")
	}
	if len(next.ID) != 32 || strings.ContainsAny(next.ID, `<>:"/\|?*`) {
		t.Fatal("retry identity is not safe for managed artifact paths", next.ID)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		imported := next
		imported.ID, imported.RetryOperationID = "legacy-random-run", "legacy-operation"
		if err := st.putRoot("graph_runs", imported.ID, imported); err != nil {
			return err
		}
		receipt, err := st.graphRetryReceipt(s.ID, source.ID, "b1", "legacy-operation")
		if err != nil || receipt == nil || receipt.ID != imported.ID {
			t.Fatal("imported retry receipt was not retained", receipt, err)
		}
		if _, err := st.graphRetryReceipt(s.ID, source.ID, "a1", "legacy-operation"); err == nil {
			t.Fatal("imported operation conflict accepted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteGraphRetryRollback(t *testing.T) {
	a, _, source := sqliteRetryFixture(t, true)
	injected := errors.New("abort checkpoint publication")
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		snapshot, _, err := st.capturedGraph(source.ID)
		if err != nil {
			return err
		}
		if _, err := st.retryGraphCheckpoint(source, snapshot, "b1", "rollback"); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}
	for _, table := range []string{"graph_runs", "graph_snapshots", "graph_activations", "graph_deliveries", "graph_join_rounds", "graph_workspace_uses"} {
		var count int
		where := `x_runId=?`
		if table == "graph_runs" {
			where = `id=?`
		}
		if table == "graph_snapshots" {
			where = `owner_id=?`
		}
		if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_`+table+` WHERE `+where, sqlGraphRetryID(source.ConversationID, "rollback")).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial checkpoint or durable retry identity survived rollback", table, count, err)
		}
	}
}

func TestSQLiteGraphRetryReusedEvidencePages(t *testing.T) {
	a, s, source := sqliteRetryFixture(t, false)
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		x, err := st.graphActivation("a1")
		if err != nil {
			return err
		}
		x.Output = map[string]string{"result": "retained"}
		if err := st.putRoot("graph_activations", x.ID, x); err != nil {
			return err
		}
		for i := range 57 {
			if err := st.appendScalarChild("activation_events", x.ID, Event{ID: fmt.Sprintf("event-%03d", i), Type: "command", Text: fmt.Sprint(i), CreatedAt: now()}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first := sqlRetryCheckpoint(t, a, source, "b1", "first")
	var selected string
	if err := a.store.readers.QueryRow(`SELECT id FROM data_graph_activations WHERE x_runId=? AND x_nodeId='b'`, first.ID).Scan(&selected); err != nil {
		t.Fatal(err)
	}
	// A second retry adds a reference, not a second copy of the first prefix.
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		first.Status, first.EndedAt, first.Result = "failed", now(), &GraphRunResult{Kind: "failed"}
		return st.putRoot("graph_runs", first.ID, first)
	})
	if err != nil {
		t.Fatal(err)
	}
	second := sqlRetryCheckpoint(t, a, first, selected, "second")
	var retained string
	if err := a.store.readers.QueryRow(`SELECT id FROM data_graph_activations WHERE x_runId=? AND x_nodeId='a'`, second.ID).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, offset := range []int{0, 50} {
		req := httptest.NewRequest("GET", fmt.Sprintf("/?activationId=%s&eventOffset=%d", retained, offset), nil)
		req.SetPathValue("id", s.ID)
		req.SetPathValue("runId", second.ID)
		req.SetPathValue("nodeId", "a")
		w := httptest.NewRecorder()
		a.getSQLGraphNodeActivity(w, req)
		var value struct {
			Activation struct {
				Occurrence string            `json:"occurrence"`
				Output     map[string]string `json:"output"`
				Events     []Event           `json:"events"`
				Total      int               `json:"total"`
			} `json:"activation"`
			Previous map[string]string `json:"previousAttempt"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if value.Activation.Total != 57 || value.Activation.Occurrence != "1" || value.Activation.Output["result"] != "retained" || value.Previous["runId"] != first.ID {
			t.Fatal("reused output/previous attempt/decimal projection lost", w.Body.String())
		}
		for _, event := range value.Activation.Events {
			if seen[event.ID] {
				t.Fatal("reused event expanded twice", event.ID)
			}
			seen[event.ID] = true
		}
	}
	if len(seen) != 57 {
		t.Fatal("reused evidence truncated", len(seen))
	}
	raw, _ := json.Marshal(map[string]any{"runId": second.ID, "nodeId": "a", "offset": 50, "limit": 10})
	value, err := a.readSQLGraphTool(context.Background(), s.ID, "graph_recent_events", raw)
	if err != nil || len(value.(map[string]any)["items"].([]any)) != 7 {
		t.Fatal("tool evidence pagination lost history", value, err)
	}
}

func TestSQLiteGraphRetryHTTPAdmission(t *testing.T) {
	if CheckGraphAdapter("pi") != nil {
		t.Skip("graph execution requires Windows containment")
	}
	a, s, source := sqliteRetryFixture(t, false)
	// No installed/native harness is launched; reservation must survive dispatch failure.
	a.binaries = map[string]binary{}
	a.runs[s.ID] = &turn{ctx: a.ctx}
	a.sqlChanges = &sqliteChangeBus{}
	sub, err := a.sqlChanges.subscribe(sqliteChangeScope{Kind: "conversation", ID: s.ID, ProjectID: s.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	defer a.sqlChanges.unsubscribe(sub)
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"activationId":"b1","operationId":"http"}`))
	req.SetPathValue("id", s.ID)
	req.SetPathValue("runId", source.ID)
	req.SetPathValue("nodeId", "b")
	w := httptest.NewRecorder()
	a.retryGraphNode(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	select {
	case <-sub.notices:
	default:
		t.Fatal("retry did not invalidate owning conversation")
	}
	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("retry runtime did not settle its missing-harness failure")
	}
	var run GraphRun
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "graph_runs", sqlGraphRetryID(s.ID, "http"), 4<<20, &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != "failed" || run.RetrySourceRunID != source.ID || run.Result == nil || !strings.Contains(run.Result.Error, "harness executable is unavailable") {
		t.Fatal("retry runtime re-entered graph startup or lost durable identity", run)
	}
	var starts, prefix int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_graph_activations WHERE x_runId=? AND x_nodeId='a'`, run.ID).Scan(&starts); err != nil {
		t.Fatal(err)
	}
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_graph_activations WHERE x_runId=? AND status='completed'`, run.ID).Scan(&prefix); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || prefix != 1 || a.storageErr != nil || len(a.state.GraphRuns) != 0 {
		t.Fatal("retry replayed prefix or fell back to legacy storage", starts, prefix, a.storageErr)
	}
}
