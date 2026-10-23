package main

import (
	"context"
	"errors"
	"net/http"
)

func (d *diskState) graphRetryReason(r *GraphRun, x *GraphActivation) string {
	if r == nil || x == nil || x.RunID != r.ID {
		return "Node activation is unavailable."
	}
	if d.activeGraphRun(r.ConversationID) != nil {
		return "Stop the active graph and wait for it to finish first."
	}
	activity := d.graphActivity(r.ActivityID)
	if activity == nil || activity.Status == "abandoned" || activity.Status == "succeeded" {
		return "This activity is closed. Start a new activity."
	}
	for i := len(d.GraphRuns) - 1; i >= 0; i-- {
		if d.GraphRuns[i].ActivityID == r.ActivityID {
			if d.GraphRuns[i].ID != r.ID {
				return "Retry from the latest attempt of this activity."
			}
			break
		}
	}
	if x.Input == nil || d.graphWorkspace(x.WorkspaceID) == nil {
		return "This activation has no recorded input or workspace."
	}
	return ""
}

// Clone the causal prefix, not the graph topology: loops may visit a node repeatedly.
// Only completed producers survive. Interrupted independent branches restart too,
// so a downstream Join can still receive every required result.
func retryGraphCheckpoint(d *diskState, source GraphRun, selectedID, operationID string) (GraphRun, error) {
	selected := d.graphActivation(selectedID)
	if reason := d.graphRetryReason(&source, selected); reason != "" {
		return GraphRun{}, errors.New(reason)
	}
	invalid := map[string]bool{selectedID: true}
	for changed := true; changed; {
		changed = false
		for _, delivery := range d.GraphDeliveries {
			if delivery.RunID != source.ID || !invalid[delivery.ProducerActivationID] {
				continue
			}
			target := delivery.TargetActivationID
			if delivery.JoinRoundID != "" {
				for _, round := range d.GraphJoinRounds {
					if round.ID == delivery.JoinRoundID {
						target = round.ActivationID
						break
					}
				}
			}
			if target != "" && !invalid[target] {
				invalid[target], changed = true, true
			}
		}
	}
	run := source
	run.ID, run.Status, run.Revision = newID(), "running", 1
	run.RetrySourceRunID, run.RetryActivationID, run.RetryOperationID = source.ID, selectedID, operationID
	run.StopRequested, run.PauseRequested, run.Paused = false, false, false
	run.Result, run.FinalityError, run.EndedAt = nil, "", ""
	run.CreatedAt, run.UpdatedAt = now(), now()
	run.Workspace.Attempt, run.Workspace.SourceRunID = "reuse", source.ID
	run.Workspace.Reuse = map[string]string{}
	for _, use := range d.GraphWorkspaceUses {
		if use.RunID == source.ID {
			if w := d.graphWorkspace(use.WorkspaceID); w != nil && w.Status == "ready" {
				run.Workspace.Reuse[use.Association] = w.ID
			}
		}
	}
	run.Workspace.Reuse["initial"] = source.InitialWorkspaceID
	ids := map[string]string{"run:" + source.ID: "run:" + run.ID}
	restart := map[string]bool{}
	for _, x := range d.GraphActivations {
		if x.RunID != source.ID || invalid[x.ID] && x.ID != selectedID {
			continue
		}
		ids[x.ID] = newID()
		restart[x.ID] = x.ID == selectedID || x.Status != "completed"
	}
	for _, round := range d.GraphJoinRounds {
		if round.RunID == source.ID {
			ids[round.ID] = newID()
		}
	}
	for _, delivery := range d.GraphDeliveries {
		if delivery.RunID == source.ID && ids[delivery.ProducerActivationID] != "" && !restart[delivery.ProducerActivationID] {
			ids[delivery.ID] = newID()
		}
	}
	causal := func(id string) string {
		if next := ids[id]; next != "" {
			return next
		}
		return id
	}
	activations := []GraphActivation{}
	for _, old := range d.GraphActivations {
		if old.RunID != source.ID || ids[old.ID] == "" {
			continue
		}
		x := old
		x.ID, x.RunID, x.CausalID = ids[old.ID], run.ID, causal(old.CausalID)
		x.ForkActivationID, x.JoinRoundID = ids[old.ForkActivationID], ids[old.JoinRoundID]
		x.SessionID, x.CurrentTurnID, x.FinishedTurnID = "", "", ""
		x.Continuable, x.SessionPolicy = false, "new"
		x.NativeEventStart, x.NativeEventEnd = 0, 0
		x.Events = nil
		if restart[old.ID] {
			x.ReusedFromActivationID = ""
			x.Status, x.Error, x.EndedAt, x.Correction, x.AdapterState = "reserved", "", "", "", ""
			x.Submission, x.Output, x.CommandExitCode = nil, nil, nil
			x.CommandResult, x.CommandTruncated = "", false
			x.Violations, x.ViolationIDs, x.ViolationFeedback = 0, nil, nil
			x.CreatedAt, x.UpdatedAt = now(), now()
		} else {
			x.ReusedFromActivationID = old.ID
		}
		activations = append(activations, x)
	}
	rounds := []JoinRound{}
	keptRounds := map[string]bool{}
	for _, old := range d.GraphJoinRounds {
		if old.RunID != source.ID || ids[old.ActivationID] == "" {
			continue
		}
		round := old
		round.ID, round.RunID, round.CausalID = ids[old.ID], run.ID, causal(old.CausalID)
		round.ActivationID = ids[old.ActivationID]
		round.Arrivals = []JoinArrival{}
		for _, arrival := range old.Arrivals {
			if ids[arrival.DeliveryID] == "" {
				return GraphRun{}, errors.New("Retry checkpoint is missing a Join input.")
			}
			round.Arrivals = append(round.Arrivals, JoinArrival{ConnectionID: arrival.ConnectionID, DeliveryID: ids[arrival.DeliveryID]})
		}
		if restart[old.ActivationID] {
			round.Status, round.EndedAt = "running", ""
		}
		rounds = append(rounds, round)
		keptRounds[old.ID] = true
	}
	deliveries := []GraphDelivery{}
	for _, old := range d.GraphDeliveries {
		if old.RunID != source.ID || ids[old.ID] == "" {
			continue
		}
		delivery := old
		delivery.ID, delivery.RunID, delivery.ProducerActivationID = ids[old.ID], run.ID, ids[old.ProducerActivationID]
		delivery.TargetActivationID, delivery.ForkActivationID, delivery.CausalID = ids[old.TargetActivationID], ids[old.ForkActivationID], causal(old.CausalID)
		delivery.JoinRoundID = ""
		if keptRounds[old.JoinRoundID] {
			delivery.JoinRoundID = ids[old.JoinRoundID]
		}
		if delivery.TargetActivationID == "" && delivery.JoinRoundID == "" {
			delivery.Status = "pending"
		} else {
			delivery.Status = "consumed"
		}
		deliveries = append(deliveries, delivery)
	}
	uses := []WorkspaceUse{}
	for _, old := range d.GraphWorkspaceUses {
		if old.RunID != source.ID || old.ActivationID != "" && ids[old.ActivationID] == "" {
			continue
		}
		use := old
		use.ID, use.RunID, use.ActivationID, use.Reused, use.CreatedAt = newID(), run.ID, ids[old.ActivationID], true, now()
		uses = append(uses, use)
	}
	d.GraphRuns = append(d.GraphRuns, run)
	d.GraphActivations = append(d.GraphActivations, activations...)
	d.GraphDeliveries = append(d.GraphDeliveries, deliveries...)
	d.GraphJoinRounds = append(d.GraphJoinRounds, rounds...)
	d.GraphWorkspaceUses = append(d.GraphWorkspaceUses, uses...)
	activity := d.graphActivity(run.ActivityID)
	activity.Status, activity.Workspace, activity.UpdatedAt = "running", run.Workspace, now()
	activity.Version++
	if session := d.session(run.ConversationID); session != nil {
		session.SelectedGraphID, session.UpdatedAt = run.GraphID, now()
		if run.CatalogGraphID != nil {
			session.SelectedGraphID = *run.CatalogGraphID
		}
	}
	compiled, err := compileGraphSnapshot(run.Snapshot)
	if err != nil {
		return GraphRun{}, err
	}
	if err = consumeGraphDeliveries(d, d.graphRun(run.ID), compiled); err != nil {
		return GraphRun{}, err
	}
	return run, nil
}

func (a *app) retryGraphNode(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ActivationID string `json:"activationId"`
		OperationID  string `json:"operationId"`
	}
	if !decode(w, req, &body) {
		return
	}
	if !validLinkedText(body.OperationID, 200) {
		fail(w, 400, "A retry operation ID is required.")
		return
	}
	a.mu.Lock()
	source := a.state.graphRun(req.PathValue("runId"))
	x := a.state.graphActivation(body.ActivationID)
	if source == nil || source.ConversationID != req.PathValue("id") || x == nil || x.RunID != source.ID || x.NodeID != req.PathValue("nodeId") {
		a.mu.Unlock()
		fail(w, 404, "Node activation not found in this conversation.")
		return
	}
	for _, previous := range a.state.GraphRuns {
		if previous.ConversationID == source.ConversationID && previous.RetryOperationID == body.OperationID {
			if previous.RetrySourceRunID != source.ID || previous.RetryActivationID != x.ID {
				a.mu.Unlock()
				fail(w, 409, "Retry identity belongs to another activation.")
				return
			}
			projection := a.state.graphProjection(source.ConversationID)
			a.mu.Unlock()
			respond(w, 200, projection)
			return
		}
	}
	if reason := a.state.graphRetryReason(source, x); reason != "" {
		a.mu.Unlock()
		fail(w, 409, reason)
		return
	}
	if a.closing || a.graphRuns[source.ID] != nil || a.graphStarting[source.ConversationID] {
		a.mu.Unlock()
		fail(w, 409, "Graph is still settling. Retry shortly.")
		return
	}
	project := a.state.project(source.ProjectID)
	if project == nil || project.Removed {
		a.mu.Unlock()
		fail(w, 404, "Project is unavailable.")
		return
	}
	saved, revision := *source, a.state.GraphRevision
	workspaces := map[string]WorkspaceRecord{}
	for _, use := range a.state.GraphWorkspaceUses {
		if use.RunID == source.ID {
			if workspace := a.state.graphWorkspace(use.WorkspaceID); workspace != nil && workspace.Status == "ready" {
				workspaces[workspace.ID] = *workspace
			}
		}
	}
	if initial := a.state.graphWorkspace(source.InitialWorkspaceID); initial != nil {
		workspaces[initial.ID] = *initial
	}
	a.mu.Unlock()
	// Validate physical artifacts outside the state lock, then recheck the revision.
	for _, workspace := range workspaces {
		cwd, err := CanonicalGraphDirectory(workspace.Directory)
		if err == nil {
			var metadata graphGitMetadata
			metadata, err = readGraphGit(req.Context(), cwd, workspace.Repository != "")
			if err == nil && (workspace.Status != "ready" || workspace.Repository != "" && !sameGraphDirectory(metadata.repository, workspace.Repository) || workspace.Owned && metadata.branch != workspace.GitBranch) {
				err = errors.New("Workspace repository or managed branch changed.")
			}
		}
		if err != nil {
			fail(w, 409, "Cannot reuse workspace: "+err.Error())
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.GraphRevision != revision || a.closing || a.graphStarting[saved.ConversationID] || req.Context().Err() != nil {
		fail(w, 409, "Graph changed while preparing retry. Retry with current state.")
		return
	}
	var next GraphRun
	err := a.graphChangeLocked(func(d *diskState) error {
		var err error
		next, err = retryGraphCheckpoint(d, saved, body.ActivationID, body.OperationID)
		return err
	})
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	g := &graphExecution{ctx: ctx, cancel: cancel, done: make(chan struct{}), completed: make(chan graphCompletion), wake: make(chan struct{}, 1)}
	if a.graphRuns == nil {
		a.graphRuns = map[string]*graphExecution{}
	}
	a.graphRuns[next.ID] = g
	a.wg.Add(1)
	go a.runGraph(next.ID, g)
	respond(w, 200, a.state.graphProjection(next.ConversationID))
}
