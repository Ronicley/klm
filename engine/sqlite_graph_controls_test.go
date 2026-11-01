package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestSQLiteGraphControls(t *testing.T) {
	a, s, r := sqliteRunningGraph(t, "running")
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	chatCtx, cancelChat := context.WithCancel(a.ctx)
	defer cancelChat()
	a.runs[s.ID] = &turn{ctx: chatCtx, cancel: cancelChat}
	a.graphRuns = map[string]*graphExecution{r.ID: {ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}}
	a.sqlChanges = &sqliteChangeBus{}
	sub, err := a.sqlChanges.subscribe(sqliteChangeScope{Kind: "conversation", ID: s.ID, ProjectID: s.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	defer a.sqlChanges.unsubscribe(sub)
	other := newTopLevelSession(s.ProjectID, "Other", "Ungrouped", "pi", "", "")
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.createSession(other, a.dir); err != nil {
			return err
		}
		return st.putRoot("graph_activations", "reserved", GraphActivation{ID: "reserved", RunID: r.ID, NodeID: "agent", Status: "reserved"})
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(owner, control string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest("POST", "/", nil)
		req.SetPathValue("id", owner)
		req.SetPathValue("runId", r.ID)
		req.SetPathValue("control", control)
		w := httptest.NewRecorder()
		a.controlGraphRun(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", owner, control, w.Code, w.Body.String())
		}
		var value map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	load := func() GraphRun {
		t.Helper()
		var saved GraphRun
		if err := decodeSQLiteRecord(a.ctx, a.store.readers, "graph_runs", r.ID, 4<<20, &saved); err != nil {
			t.Fatal(err)
		}
		return saved
	}
	request(other.ID, "pause", 404)
	request(s.ID, "unknown", 404)
	view := request(s.ID, "pause", 200)["run"].(map[string]any)
	if view["status"] != "pausing" || len(view["activeNodeIds"].([]any)) != 0 || !load().PauseRequested || ctx.Err() != nil {
		t.Fatal("pause projection/reservation or cancellation mismatch", view)
	}
	select {
	case <-sub.notices:
	default:
		t.Fatal("owner conversation was not invalidated after commit")
	}
	revision := load().Revision
	request(s.ID, "pause", 200)
	if load().Revision != revision {
		t.Fatal("repeated pause changed revision")
	}
	for _, workers := range []int{1, 0} {
		a.mu.Lock()
		err = a.persistSQLGraphLocked(func(st *sqliteSessionTx) error {
			saved, err := st.graphRunRecord(r.ID)
			if err != nil {
				return err
			}
			return st.acknowledgeGraphPause(&saved, workers)
		})
		a.mu.Unlock()
		if err != nil || load().Paused != (workers == 0) {
			t.Fatal("pause acknowledged before drainage or not acknowledged after", err)
		}
	}
	get := httptest.NewRequest("GET", "/", nil)
	get.SetPathValue("runID", r.ID)
	w := httptest.NewRecorder()
	a.getSQLGraphRun(w, get)
	var savedView map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &savedView); err != nil || w.Code != 200 || savedView["status"] != "paused" {
		t.Fatal("paused run read projection", w.Body.String(), err)
	}
	view = request(s.ID, "resume", 200)["run"].(map[string]any)
	if view["status"] != "running" || len(view["activeNodeIds"].([]any)) != 1 || load().Paused || load().PauseRequested {
		t.Fatal("resume projection mismatch", view)
	}
	request(s.ID, "resume", 200)
	request(s.ID, "pause", 200)
	request(other.ID, "stop", 404)
	if ctx.Err() != nil {
		t.Fatal("another conversation cancelled the run")
	}
	view = request(s.ID, "stop", 200)["run"].(map[string]any)
	saved := load()
	if ctx.Err() == nil || chatCtx.Err() != nil || !saved.StopRequested || saved.Status != "ending" || saved.Result == nil || saved.Result.Kind != "interrupted" || view["status"] != "ending" {
		t.Fatal("Stop did not persist intent before cancellation", saved, view)
	}
	request(s.ID, "stop", 200)
	if load().Revision != saved.Revision {
		t.Fatal("repeated Stop changed revision")
	}
	request(s.ID, "resume", 409)
	a.mu.Lock()
	err = a.finishSQLGraphRunLocked(r.ID)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var activity GraphActivity
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "graph_activities", r.ActivityID, 1<<20, &activity); err != nil {
		t.Fatal(err)
	}
	if saved = load(); saved.Status != "interrupted" || graphRunStatus(saved) != "interrupted" || activity.Status != "awaiting_user" {
		t.Fatal("Stop settlement resumed assessment or retained paused projection", saved, activity)
	}
	request(s.ID, "stop", 200)
	if len(a.state.GraphRuns) != 0 || a.storageErr != nil {
		t.Fatal("controls used legacy state or froze SQL", a.storageErr)
	}
}

func TestSQLiteGraphPauseDispatch(t *testing.T) {
	a, _, r := sqliteRunningGraph(t, "running")
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		for _, id := range []string{"successor", "correction"} {
			x := GraphActivation{ID: id, RunID: r.ID, NodeID: "agent", Status: "reserved", Correction: "missing-choice.md"}
			if err := st.putRoot("graph_activations", id, x); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	workers := map[string]bool{"in-flight": true}
	r.PauseRequested = true
	for _, drained := range []bool{false, true} {
		if drained {
			delete(workers, "in-flight")
		}
		a.mu.Lock()
		launch, err := a.reserveSQLGraphWorkersLocked(r, workers)
		a.mu.Unlock()
		if err != nil || len(launch) != 0 || workers["in-flight"] != !drained {
			t.Fatal("pause admitted workers or cancelled in-flight work", launch, err)
		}
	}
	r.PauseRequested = false
	for _, want := range []int{2, 0} {
		a.mu.Lock()
		launch, err := a.reserveSQLGraphWorkersLocked(r, workers)
		a.mu.Unlock()
		if err != nil || len(launch) != want {
			t.Fatal("resume lost or duplicated reservations", len(launch), err)
		}
	}
}

func TestSQLiteGraphPauseDelivery(t *testing.T) {
	a, _, r := sqliteRunningGraph(t, "running")
	r.PauseRequested = true
	c := &CompiledGraph{Definition: GraphDefinition{Nodes: map[string]GraphNode{"next": {Type: "agent"}}}, Connections: map[string]GraphConnection{"edge": {ID: "edge", To: "next", Session: "new"}}}
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("graph_runs", r.ID, r); err != nil {
			return err
		}
		d := GraphDelivery{ID: "delivery", RunID: r.ID, ConnectionID: "edge", TargetNodeID: "next", Status: "pending", Payload: map[string]string{"task": "retained result"}}
		if err := st.putRoot("graph_deliveries", d.ID, d); err != nil {
			return err
		}
		return st.consumeGraphDeliveries(r, c)
	})
	if err != nil {
		t.Fatal(err)
	}
	var pending, activations int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_graph_deliveries WHERE status='pending'`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("paused delivery was consumed", err)
	}
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_graph_activations`).Scan(&activations); err != nil || activations != 0 {
		t.Fatal("paused delivery created a successor", err)
	}
	r.PauseRequested = false
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("graph_runs", r.ID, r); err != nil {
			return err
		}
		for range 2 {
			if err := st.consumeGraphDeliveries(r, c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_graph_activations`).Scan(&activations); err != nil || activations != 1 {
		t.Fatal("resume lost/duplicated delivery", activations, err)
	}
}

func TestSQLiteGraphControlRollback(t *testing.T) {
	a, s, r := sqliteRunningGraph(t, "running")
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	a.graphRuns = map[string]*graphExecution{r.ID: {ctx: ctx, cancel: cancel}}
	r.Revision = ^uint64(0)
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error { return st.putRoot("graph_runs", r.ID, r) }); err != nil {
		t.Fatal(err)
	}
	for _, control := range []string{"pause", "stop"} {
		req := httptest.NewRequest("POST", "/", nil)
		req.SetPathValue("id", s.ID)
		req.SetPathValue("runId", r.ID)
		req.SetPathValue("control", control)
		w := httptest.NewRecorder()
		a.controlGraphRun(w, req)
		var saved GraphRun
		if err := decodeSQLiteRecord(a.ctx, a.store.readers, "graph_runs", r.ID, 4<<20, &saved); err != nil {
			t.Fatal(err)
		}
		if w.Code != 503 || ctx.Err() != nil || saved.Status != "running" || saved.PauseRequested || saved.StopRequested || saved.Revision != r.Revision {
			t.Fatal("failed write exposed partial control or cancellation", w.Code, saved)
		}
	}
}
