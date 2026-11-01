package main

import (
	"database/sql"
	"errors"
	"net/http"
)

func (a *app) controlSQLGraphRun(w http.ResponseWriter, req *http.Request) {
	control := req.PathValue("control")
	if control != "pause" && control != "resume" && control != "stop" {
		fail(w, 404, "Unknown graph control.")
		return
	}
	id, runID := req.PathValue("id"), req.PathValue("runId")
	a.mu.Lock()
	stop := false
	err := a.writeSQLLocked(req.Context(), func(st *sqliteSessionTx) error {
		if _, err := st.graphHTTPMain(id); err != nil {
			return err
		}
		r, err := st.graphRun(id, runID)
		if err != nil {
			return err
		}
		if control == "stop" {
			if r.StopRequested || r.Status == "ending" || !graphRunActive(r.Status) {
				return nil
			}
			stop = true
			return st.endGraph(&r, GraphRunResult{Kind: "interrupted", Error: "Graph stopped by the user."}, "")
		}
		if (r.Status != "starting" && r.Status != "running") || a.graphRuns[runID] == nil {
			return controlError(409, "Run is not available for pause or resume.")
		}
		pause := control == "pause"
		if r.PauseRequested == pause {
			return nil
		}
		if r.Revision == ^uint64(0) {
			return errors.New("Graph revision exhausted.")
		}
		r.PauseRequested, r.Paused, r.UpdatedAt = pause, false, now()
		r.Revision++
		if !pause && r.Status == "running" {
			_, compiled, err := st.capturedGraph(runID)
			if err != nil {
				return err
			}
			if err := st.consumeGraphDeliveries(r, compiled); err != nil {
				return err
			}
		}
		st.changes.Sessions[id] = nil
		return st.putRoot("graph_runs", runID, r)
	})
	if err == nil {
		if stop {
			a.cancelSQLGraphLocked(runID)
		} else {
			a.signalGraphLocked(runID)
		}
	}
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.getSQLConversationGraph(w, req)
}

func (st *sqliteSessionTx) acknowledgeGraphPause(r *GraphRun, workers int) error {
	if r.Status != "running" || !r.PauseRequested || r.Paused || workers != 0 {
		return nil
	}
	if r.Revision == ^uint64(0) {
		return errors.New("Graph revision exhausted.")
	}
	r.Paused, r.UpdatedAt = true, now()
	r.Revision++
	st.changes.Sessions[r.ConversationID] = nil
	return st.putRoot("graph_runs", r.ID, *r)
}

// Called under app.mu: reservations before Pause may finish, but none follow it.
func (a *app) reserveSQLGraphWorkersLocked(r GraphRun, workers map[string]bool) ([]GraphActivation, error) {
	launch := []GraphActivation{}
	if r.Status != "running" || r.PauseRequested {
		return launch, nil
	}
	err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: a.ctx, tx: tx}
		ids, err := sqliteQueryIDs(st.ctx, tx, `SELECT id FROM data_graph_activations WHERE x_runId=? AND status='reserved' ORDER BY position,id LIMIT 100`, r.ID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if workers[id] {
				continue
			}
			x, err := st.graphActivation(id)
			if err != nil {
				return err
			}
			launch = append(launch, x)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, x := range launch {
		workers[x.ID] = true
	}
	return launch, nil
}
