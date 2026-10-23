package main

import (
	"errors"
	"net/http"
	"sort"
)

func (a *app) controlGraphRun(w http.ResponseWriter, req *http.Request) {
	control := req.PathValue("control")
	if control != "pause" && control != "resume" && control != "stop" {
		fail(w, 404, "Unknown graph control.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.state.session(req.PathValue("id"))
	r := a.state.graphRun(req.PathValue("runId"))
	if s == nil || s.ParentID != "" || s.GraphRunID != "" || r == nil || r.ConversationID != s.ID {
		fail(w, 404, "Run not found in this conversation.")
		return
	}
	if control == "stop" {
		if r.StopRequested || r.Status == "ending" || !graphRunActive(r.Status) {
			respond(w, 200, a.state.graphProjection(s.ID))
			return
		}
		if err := a.endGraphLocked(r.ID, GraphRunResult{Kind: "interrupted", Error: "Graph stopped by the user."}, ""); err != nil {
			fail(w, 409, err.Error())
			return
		}
		respond(w, 200, a.state.graphProjection(s.ID))
		return
	}
	if (r.Status != "starting" && r.Status != "running") || a.graphRuns[r.ID] == nil || a.closing {
		fail(w, 409, "Run is not available for pause or resume.")
		return
	}
	if err := a.setGraphPausedLocked(r.ID, control == "pause"); err != nil {
		fail(w, 409, err.Error())
		return
	}
	respond(w, 200, a.state.graphProjection(s.ID))
}

func (a *app) setGraphPausedLocked(runID string, pause bool) error {
	r := a.state.graphRun(runID)
	if r == nil || (r.Status != "starting" && r.Status != "running") {
		return errors.New("Run is not available for pause or resume.")
	}
	if r.PauseRequested == pause {
		return nil
	}
	err := a.graphChangeLocked(func(d *diskState) error {
		run := d.graphRun(runID)
		run.PauseRequested, run.Paused, run.UpdatedAt = pause, false, now()
		run.Revision++
		if !pause && run.Status == "running" {
			compiled, err := compileGraphSnapshot(run.Snapshot)
			if err != nil {
				return err
			}
			return consumeGraphDeliveries(d, run, compiled)
		}
		return nil
	})
	if err == nil {
		a.signalGraphLocked(runID)
	}
	return err
}

type graphActivitySummary struct {
	ID          string `json:"id"`
	Occurrence  uint64 `json:"occurrence"`
	Status      string `json:"status"`
	RetryReason string `json:"retryReason,omitempty"`
}

type graphActivityDetail struct {
	graphActivitySummary
	Input      map[string]string      `json:"input"`
	Output     map[string]string      `json:"output,omitempty"`
	Submission *GraphChoiceSubmission `json:"submission,omitempty"`
	Events     []Event                `json:"events"`
	Error      string                 `json:"error,omitempty"`
	CreatedAt  string                 `json:"createdAt"`
	UpdatedAt  string                 `json:"updatedAt"`
}

// Activity is fetched on demand, scoped to the owning conversation and captured run.
func (a *app) getGraphNodeActivity(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	s := a.state.session(r.PathValue("id"))
	run := a.state.graphRun(r.PathValue("runId"))
	if s == nil || s.ParentID != "" || s.GraphRunID != "" || run == nil || run.ConversationID != s.ID {
		a.mu.Unlock()
		fail(w, 404, "Run not found in this conversation.")
		return
	}
	nodeID := r.PathValue("nodeId")
	node, ok := run.Snapshot.Graph.Definition.Nodes[nodeID]
	if !ok || node.Type == "" {
		a.mu.Unlock()
		fail(w, 404, "Agent node not found in this run.")
		return
	}
	summaries := []graphActivitySummary{}
	var selected *GraphActivation
	requested := r.URL.Query().Get("activationId")
	for i := range a.state.GraphActivations {
		x := &a.state.GraphActivations[i]
		if x.RunID != run.ID || x.NodeID != nodeID {
			continue
		}
		summaries = append(summaries, graphActivitySummary{ID: x.ID, Occurrence: x.Occurrence, Status: x.Status, RetryReason: a.state.graphRetryReason(run, x)})
		if requested != "" {
			if x.ID == requested {
				selected = x
			}
		} else if selected == nil || x.Occurrence > selected.Occurrence {
			selected = x
		}
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Occurrence < summaries[j].Occurrence })
	if requested != "" && selected == nil {
		a.mu.Unlock()
		fail(w, 404, "Activation not found in this node.")
		return
	}
	var detail *graphActivityDetail
	if x := selected; x != nil {
		events := a.state.graphActivationEvents(x)
		updatedAt := x.UpdatedAt
		if native := a.state.session(x.SessionID); native != nil {
			if graphActivationActive(x.Status) {
				updatedAt = native.UpdatedAt
			}
		}
		sort.SliceStable(events, func(i, j int) bool { return events[i].CreatedAt < events[j].CreatedAt })
		detail = &graphActivityDetail{graphActivitySummary: graphActivitySummary{ID: x.ID, Occurrence: x.Occurrence, Status: x.Status, RetryReason: a.state.graphRetryReason(run, x)}, Input: x.Input, Output: x.Output, Submission: x.Submission, Events: events, Error: x.Error, CreatedAt: x.CreatedAt, UpdatedAt: updatedAt}
	}
	response := map[string]any{"runActive": graphRunActive(run.Status), "activations": summaries, "activation": detail, "capabilities": run.Snapshot.Capabilities[nodeID]}
	if selected != nil && run.RetrySourceRunID != "" {
		for _, previous := range a.state.GraphActivations {
			if previous.RunID == run.RetrySourceRunID && previous.NodeID == selected.NodeID && previous.Occurrence == selected.Occurrence {
				response["previousAttempt"] = map[string]string{"runId": previous.RunID, "activationId": previous.ID}
				break
			}
		}
	}
	a.mu.Unlock()
	respond(w, 200, response)
}

func (d *diskState) graphActivationEvents(x *GraphActivation) []Event {
	for x.ReusedFromActivationID != "" {
		source := d.graphActivation(x.ReusedFromActivationID)
		if source == nil {
			break
		}
		x = source
	}
	events := append([]Event{}, x.Events...)
	if native := d.session(x.SessionID); native != nil {
		start := min(max(0, x.NativeEventStart), len(native.Events))
		end := x.NativeEventEnd
		if graphActivationActive(x.Status) {
			end = len(native.Events)
		}
		end = min(max(start, end), len(native.Events))
		events = append(events, native.Events[start:end]...)
	}
	return events
}
