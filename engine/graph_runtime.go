package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

type graphCompletion struct {
	turnID       string
	activationID string
	err          error
	drained      bool
	native       *GraphAdapterResult
	output       map[string]string
	branches     []graphBranchResult
	command      *graphProcessResult
}

// Domain rejections must not poison storage. Prepare and validate a private next
// state before delegating persistence/publication to the existing commit gate.
func (a *app) graphChangeLocked(change func(*diskState) error) error {
	if a.storageErr != nil {
		return a.storageErr
	}
	data, err := json.Marshal(a.state)
	if err != nil {
		return err
	}
	var next diskState
	if err = json.Unmarshal(data, &next); err != nil {
		return err
	}
	if err = change(&next); err != nil {
		return err
	}
	normalizeGraphWorkspaceRecords(&next)
	if err = validateGraphRecords(&next); err != nil {
		return err
	}
	return a.commitLocked(func(d *diskState) { *d = next })
}
func (a *app) signalGraphLocked(runID string) {
	if g := a.graphRuns[runID]; g != nil {
		select {
		case g.wake <- struct{}{}:
		default:
		}
	}
}
func (a *app) endGraphLocked(runID string, result GraphRunResult, uncertain string) error {
	err := a.graphChangeLocked(func(d *diskState) error {
		r := d.graphRun(runID)
		if r == nil || !graphRunActive(r.Status) {
			return errors.New("Run has already ended.")
		}
		if r.Status != "ending" {
			r.Status, r.Result, r.UpdatedAt = "ending", &result, now()
			r.Revision++
		}
		if uncertain != "" {
			r.FinalityError = uncertain
		}
		return nil
	})
	if err == nil {
		if g := a.graphRuns[runID]; g != nil {
			g.cancel()
		}
		a.signalGraphLocked(runID)
	}
	return err
}

func nextGraphActivation(d *diskState, r *GraphRun, nodeID string, input map[string]string, workspaceID, originID, causalID, policy, forkID, branchID, roundID string) GraphActivation {
	occurrence := uint64(1)
	for _, x := range d.GraphActivations {
		if x.RunID == r.ID && x.NodeID == nodeID && x.Occurrence >= occurrence {
			occurrence = x.Occurrence + 1
		}
	}
	return GraphActivation{ID: newID(), RunID: r.ID, NodeID: nodeID, Occurrence: occurrence, Status: "reserved", Input: input, WorkspaceID: workspaceID, OriginWorkspaceID: originID, CausalID: causalID, SessionPolicy: policy, ForkActivationID: forkID, BranchID: branchID, JoinRoundID: roundID, CreatedAt: now(), UpdatedAt: now()}
}

func consumeGraphDeliveries(d *diskState, r *GraphRun, c *CompiledGraph) error {
	if r.Status != "running" {
		return nil
	}
	for i := range d.GraphDeliveries {
		delivery := &d.GraphDeliveries[i]
		if delivery.RunID != r.ID || delivery.Status != "pending" {
			continue
		}
		node := c.Definition.Nodes[delivery.TargetNodeID]
		edge := c.Connections[delivery.ConnectionID]
		if node.Type != "join" {
			x := nextGraphActivation(d, r, delivery.TargetNodeID, delivery.Payload, delivery.WorkspaceID, delivery.OriginWorkspaceID, delivery.CausalID, edge.Session, delivery.ForkActivationID, delivery.BranchID, "")
			d.GraphActivations = append(d.GraphActivations, x)
			delivery.TargetActivationID, delivery.Status = x.ID, "consumed"
			continue
		}
		index := -1
		occurrence := uint64(1)
		for j := range d.GraphJoinRounds {
			round := &d.GraphJoinRounds[j]
			if round.RunID != r.ID || round.NodeID != delivery.TargetNodeID {
				continue
			}
			if round.Occurrence >= occurrence {
				occurrence = round.Occurrence + 1
			}
			if round.Status == "collecting" || round.Status == "running" {
				index = j
				break
			}
			if round.CausalID == delivery.CausalID {
				return errors.New("A fresh Join round cannot reuse a previous round's causal deliveries.")
			}
		}
		if index < 0 {
			round := JoinRound{ID: newID(), RunID: r.ID, NodeID: delivery.TargetNodeID, Occurrence: occurrence, CausalID: delivery.CausalID, Expected: slices.Clone(c.Incoming[delivery.TargetNodeID]), Arrivals: []JoinArrival{}, Status: "collecting", OriginWorkspaceID: delivery.OriginWorkspaceID, CreatedAt: now()}
			d.GraphJoinRounds = append(d.GraphJoinRounds, round)
			index = len(d.GraphJoinRounds) - 1
		}
		round := &d.GraphJoinRounds[index]
		if round.Status != "collecting" || round.CausalID != delivery.CausalID {
			return errors.New("Overlapping or causally unrelated Join rounds are invalid.")
		}
		if round.OriginWorkspaceID != delivery.OriginWorkspaceID {
			return errors.New("Join arrivals have ambiguous integration origins.")
		}
		for _, arrival := range round.Arrivals {
			if arrival.ConnectionID == delivery.ConnectionID {
				return fmt.Errorf("Independent deliveries collapsed onto Join connection %s.", delivery.ConnectionID)
			}
		}
		if edge.Kind == "choice" {
			if round.SessionPolicy != "" && round.SessionPolicy != edge.Session {
				return errors.New("Join session policies disagree.")
			}
			round.SessionPolicy = edge.Session
		}
		delivery.JoinRoundID, delivery.Status = round.ID, "consumed"
		round.Arrivals = append(round.Arrivals, JoinArrival{ConnectionID: delivery.ConnectionID, DeliveryID: delivery.ID})
		if len(round.Arrivals) == len(round.Expected) {
			if round.SessionPolicy == "" {
				round.SessionPolicy = "new"
			}
			x := nextGraphActivation(d, r, round.NodeID, map[string]string{}, round.OriginWorkspaceID, round.OriginWorkspaceID, round.ID, round.SessionPolicy, "", "", round.ID)
			round.ActivationID, round.Status = x.ID, "running"
			d.GraphActivations = append(d.GraphActivations, x)
		}
	}
	return nil
}

func addGraphDelivery(d *diskState, r *GraphRun, x GraphActivation, edge GraphConnection, payload map[string]string, workspaceID, originID, causalID, forkID, branchID string) {
	d.GraphDeliveries = append(d.GraphDeliveries, GraphDelivery{ID: newID(), RunID: r.ID, ConnectionID: edge.ID, ProducerActivationID: x.ID, TargetNodeID: edge.To, Payload: payload, WorkspaceID: workspaceID, OriginWorkspaceID: originID, CausalID: causalID, ForkActivationID: forkID, BranchID: branchID, Status: "pending", CreatedAt: now()})
}

func (a *app) finishGraphRunLocked(runID string) error {
	return a.graphChangeLocked(func(d *diskState) error {
		r := d.graphRun(runID)
		if r == nil || r.Status != "ending" || r.Result == nil {
			return errors.New("Run cannot publish an unsettled result.")
		}
		// Workers have returned. Unconfirmed external effects are a failure
		// diagnostic, not a reason to keep the graph executing indefinitely.
		// Retain FinalityError without claiming cancellation or accepting Choice.
		if r.FinalityError != "" {
			r.Result.Kind = "failed"
			if r.Result.Error == "" {
				r.Result.Error = r.FinalityError
			} else if !strings.Contains(r.Result.Error, r.FinalityError) {
				r.Result.Error += "\n" + r.FinalityError
			}
		}
		stamp := now()
		for i := range d.GraphActivations {
			x := &d.GraphActivations[i]
			if x.RunID == runID && graphActivationActive(x.Status) {
				x.Status, x.EndedAt, x.UpdatedAt = "interrupted", stamp, stamp
			}
		}
		for i := range d.GraphDeliveries {
			x := &d.GraphDeliveries[i]
			if x.RunID == runID && x.Status == "pending" {
				x.Status = "interrupted"
			}
		}
		for i := range d.GraphJoinRounds {
			x := &d.GraphJoinRounds[i]
			if x.RunID == runID && (x.Status == "collecting" || x.Status == "running") {
				x.Status, x.EndedAt = "interrupted", stamp
			}
		}
		for i := range d.GraphWorkspaces {
			x := &d.GraphWorkspaces[i]
			if x.CreatedByRunID == runID && x.Status == "reserved" {
				x.Status, x.Error = "interrupted", "Run ended before workspace creation settled."
			}
		}
		r.Status, r.EndedAt, r.UpdatedAt = r.Result.Kind, stamp, stamp
		r.Revision++
		activity := d.graphActivity(r.ActivityID)
		activity.Status, activity.UpdatedAt = "awaiting_assessment", stamp
		activity.Version++
		d.GraphNotifications = append(d.GraphNotifications, RunNotification{ID: "graph-result:" + r.ID, RunID: r.ID, ConversationID: r.ConversationID, Status: "pending", CreatedAt: stamp})
		return nil
	})
}

func (a *app) runGraph(runID string, g *graphExecution) {
	defer a.wg.Done()
	defer func() {
		g.cancel()
		a.mu.Lock()
		delete(a.graphRuns, runID)
		close(g.done)
		a.scheduleLinkedLocked()
		a.mu.Unlock()
	}()
	a.mu.Lock()
	run := *a.state.graphRun(runID)
	a.mu.Unlock()
	workspaceID, err := a.prepareInitialGraphWorkspace(g, run)
	a.mu.Lock()
	if err == nil {
		err = a.graphChangeLocked(func(d *diskState) error {
			r := d.graphRun(runID)
			if r.Status != "starting" || g.ctx.Err() != nil {
				return errors.New("Graph startup interrupted.")
			}
			r.InitialWorkspaceID, r.Status, r.UpdatedAt = workspaceID, "running", now()
			r.Revision++
			x := nextGraphActivation(d, r, r.Snapshot.Graph.Definition.InitialNode, map[string]string{"task": r.Input.Task}, workspaceID, workspaceID, "run:"+r.ID, "new", "", "", "")
			d.GraphActivations = append(d.GraphActivations, x)
			return nil
		})
	}
	if err != nil {
		kind := "failed"
		if a.ctx.Err() != nil {
			kind = "interrupted"
		}
		uncertainty := ""
		var unconfirmed *graphUnconfirmedError
		if errors.As(err, &unconfirmed) {
			uncertainty = err.Error()
		}
		_ = a.endGraphLocked(runID, GraphRunResult{Kind: kind, Error: err.Error()}, uncertainty)
	}
	a.mu.Unlock()
	workers := map[string]bool{}
	for {
		a.mu.Lock()
		r := a.state.graphRun(runID)
		if r == nil || a.storageErr != nil {
			a.mu.Unlock()
			g.cancel()
			for len(workers) > 0 {
				result := <-g.completed
				delete(workers, result.activationID)
			}
			return
		}
		if a.ctx.Err() != nil && r.Status != "ending" {
			_ = a.endGraphLocked(runID, GraphRunResult{Kind: "interrupted", Error: "Engine shutdown interrupted graph execution."}, "")
			r = a.state.graphRun(runID)
		}
		if r.Status == "ending" && len(workers) == 0 {
			if err := a.finishGraphRunLocked(runID); err == nil || a.storageErr != nil {
				a.mu.Unlock()
				return
			} else {
				_ = a.graphChangeLocked(func(d *diskState) error {
					d.graphRun(runID).FinalityError = "Cannot settle graph result: " + err.Error()
					return nil
				})
				r = a.state.graphRun(runID)
			}
			if a.ctx.Err() != nil {
				a.mu.Unlock()
				return
			}
		}
		launch := []GraphActivation{}
		if r.Status == "running" {
			for _, x := range a.state.GraphActivations {
				if x.RunID == runID && x.Status == "reserved" && !workers[x.ID] {
					launch = append(launch, x)
					workers[x.ID] = true
				}
			}
			if len(workers) == 0 {
				missing := []string{}
				for _, round := range a.state.GraphJoinRounds {
					if round.RunID == runID && round.Status == "collecting" {
						arrived := map[string]bool{}
						for _, x := range round.Arrivals {
							arrived[x.ConnectionID] = true
						}
						for _, edge := range round.Expected {
							if !arrived[edge] {
								missing = append(missing, round.NodeID+"/"+edge)
							}
						}
					}
				}
				diagnostic := "Graph has no runnable producer or terminal outcome."
				if len(missing) > 0 {
					diagnostic = "Unsatisfied Join dependencies with no remaining producer: " + strings.Join(missing, ", ")
				}
				_ = a.endGraphLocked(runID, GraphRunResult{Kind: "failed", Error: diagnostic}, "")
				a.mu.Unlock()
				continue
			}
		}
		run = *r
		a.mu.Unlock()
		for _, x := range launch {
			go a.launchGraphActivation(g, run, x)
		}
		select {
		case result := <-g.completed:
			delete(workers, result.activationID)
			a.mu.Lock()
			a.completeGraphActivationLocked(runID, result)
			a.mu.Unlock()
		case <-g.wake:
		case <-a.ctx.Done():
			// A canceled graph context still needs every worker's settlement; do
			// not let cancellation drop a completion or publish early.
			if len(workers) > 0 {
				result := <-g.completed
				delete(workers, result.activationID)
				a.mu.Lock()
				a.completeGraphActivationLocked(runID, result)
				a.mu.Unlock()
			}
		}
	}
}

func (a *app) launchGraphActivation(g *graphExecution, run GraphRun, x GraphActivation) {
	send := func(result graphCompletion) {
		select {
		case g.completed <- result:
		case <-g.done:
		}
	}
	if g.ctx.Err() != nil {
		send(graphCompletion{activationID: x.ID, drained: true, err: g.ctx.Err()})
		return
	}
	node := run.Snapshot.Graph.Definition.Nodes[x.NodeID]
	if node.Type == "agent" || node.Type == "join" {
		a.launchGraphAgent(g, run, x, send)
		return
	}
	a.mu.Lock()
	err := a.graphChangeLocked(func(d *diskState) error {
		r := d.graphRun(run.ID)
		if r.Status != "running" {
			return errors.New("Run is ending.")
		}
		activation := d.graphActivation(x.ID)
		activation.Status, activation.UpdatedAt = "running", now()
		return nil
	})
	a.mu.Unlock()
	if err != nil {
		send(graphCompletion{activationID: x.ID, drained: true, err: err})
		return
	}
	if node.Type == "terminal" {
		send(a.executeGraphTerminal(g, run, x))
		return
	}
	if node.Type == "fork" {
		send(a.executeGraphFork(g, run, x))
		return
	}
	send(graphCompletion{activationID: x.ID, drained: true, err: errors.New("Unknown executable graph node.")})
}

func (a *app) launchGraphAgent(g *graphExecution, run GraphRun, x GraphActivation, send func(graphCompletion)) {
	failure := func(err error) { send(graphCompletion{activationID: x.ID, drained: true, err: err}) }
	compiled, err := compileGraphSnapshot(run.Snapshot)
	if err != nil {
		failure(err)
		return
	}
	agent := compiled.Agents[x.NodeID]
	if err = CheckGraphAdapter(agent.Harness); err != nil {
		failure(err)
		return
	}
	var joinData any
	if compiled.Definition.Nodes[x.NodeID].Type == "join" && x.Correction == "" {
		x, joinData, err = a.prepareGraphJoin(g, run, x)
		if err != nil {
			failure(err)
			return
		}
	}
	data := map[string]any{"runId": run.ID, "nodeId": x.NodeID, "activationId": x.ID, "runTask": run.Input.Task, "payload": x.Input, "agentInstructions": agent.Prompt, "availableChoices": graphChoiceContracts(compiled, x.NodeID)}
	promptName := "agent-node.md"
	if x.Correction != "" {
		promptName = x.Correction
		data["violationCount"] = x.Violations
	}
	prompt, err := graphPrompt(promptName, data)
	if err != nil {
		failure(err)
		return
	}
	if joinData != nil {
		extra, e := graphPrompt("join-node.md", joinData)
		if e != nil {
			failure(e)
			return
		}
		prompt += "\n" + extra
	}
	a.mu.Lock()
	workspace := a.state.graphWorkspace(x.WorkspaceID)
	cwd := ""
	if workspace != nil {
		cwd = workspace.Directory
	}
	a.mu.Unlock()
	cwd, err = CanonicalGraphDirectory(cwd)
	if err != nil {
		failure(err)
		return
	}
	a.mu.Lock()
	if a.closing || a.state.graphRun(run.ID).Status != "running" || g.ctx.Err() != nil {
		a.mu.Unlock()
		failure(errors.New("Graph activation was cancelled before dispatch."))
		return
	}
	b, installed := a.binaries[agent.Harness]
	if !installed {
		a.mu.Unlock()
		failure(errors.New("Graph harness executable is unavailable."))
		return
	}
	sessionID := ""
	if x.Correction != "" {
		if !x.Continuable {
			a.mu.Unlock()
			failure(errors.New("The native session cannot safely continue result correction."))
			return
		}
		sessionID = x.SessionID
	} else if x.SessionPolicy == "continue_target" {
		var latest GraphActivation
		for _, previous := range a.state.GraphActivations {
			if previous.RunID == run.ID && previous.NodeID == x.NodeID && previous.ID != x.ID && previous.SessionID != "" && previous.Occurrence > latest.Occurrence {
				latest = previous
			}
		}
		if latest.SessionID != "" && latest.Continuable {
			s := a.state.session(latest.SessionID)
			if s != nil && s.Harness == agent.Harness && sameGraphDirectory(s.ExecutionCWD, cwd) {
				sessionID = s.ID
			}
		}
	}
	if sessionID != "" && a.runs[sessionID] != nil {
		a.mu.Unlock()
		failure(errors.New("Target native session already has an active turn."))
		return
	}
	newSession := sessionID == ""
	if newSession {
		sessionID = newID()
	}
	ctx, cancel := context.WithCancel(g.ctx)
	t := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	turnID := newID()
	hooks := GraphAdapterHooks{Node: true, ReserveChoice: func(ctx context.Context, callID string, raw json.RawMessage) (any, error) {
		return a.reserveGraphChoice(ctx, run.ID, x.ID, turnID, callID, raw)
	}, Transition: func(ctx context.Context, stage string) error {
		return a.transitionGraphChoice(ctx, run.ID, x.ID, stage)
	}, Finished: func(result GraphAdapterResult) {
		send(graphCompletion{activationID: x.ID, turnID: turnID, drained: result.ProcessesDrained, native: &result, err: result.Error})
	}}
	if err = BindGraphAdapter(t, hooks); err != nil {
		cancel()
		a.mu.Unlock()
		failure(err)
		return
	}
	err = a.graphChangeLocked(func(d *diskState) error {
		if newSession {
			d.Sessions = append(d.Sessions, Session{ID: sessionID, ProjectID: run.ProjectID, Title: compiled.Definition.Nodes[x.NodeID].Name, Workspace: "Ungrouped", Role: "graph_node", GraphRunID: run.ID, GraphNodeID: x.NodeID, ExecutionCWD: cwd, Harness: agent.Harness, Model: agent.Model, Effort: agent.Effort, Status: "idle", Events: []Event{}, CreatedAt: now(), UpdatedAt: now()})
			if agent.Harness == "pi" {
				d.Native[sessionID] = nativeSession{Path: filepath.Join(a.dir, "sessions", sessionID+".jsonl")}
			}
		}
		s := d.session(sessionID)
		s.Status, s.UpdatedAt = "running", now()
		s.Permissions, s.Questions = nil, nil
		activation := d.graphActivation(x.ID)
		activation.SessionID, activation.Status, activation.AdapterState, activation.UpdatedAt = sessionID, "running", "open", now()
		if activation.CurrentTurnID == "" {
			activation.NativeEventStart = len(s.Events)
		}
		activation.CurrentTurnID = turnID
		entry := event("status", "Node activation started.")
		entry.Data = map[string]any{"activationId": x.ID, "turnId": turnID}
		activation.Events = append(activation.Events, entry)
		return nil
	})
	if err != nil {
		UnbindGraphAdapter(t)
		cancel()
		a.mu.Unlock()
		failure(err)
		return
	}
	snapshot, native := *a.state.session(sessionID), a.state.Native[sessionID]
	a.runs[sessionID] = t
	a.wg.Add(1)
	a.mu.Unlock()
	go a.execute(t, snapshot, native, b, cwd, submission{Text: prompt})
}

func (a *app) reserveGraphChoice(ctx context.Context, runID, activationID, turnID, callID string, raw json.RawMessage) (any, error) {
	a.mu.Lock()
	r := a.state.graphRun(runID)
	x := a.state.graphActivation(activationID)
	if r == nil || x == nil || r.Status != "running" || x.Status != "running" || ctx.Err() != nil {
		a.mu.Unlock()
		return nil, errors.New("Activation is not accepting a Choice.")
	}
	compiled, err := compileGraphSnapshot(r.Snapshot)
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if callID == "" {
		callID = newID()
	}
	operationID := "call:" + callID
	if text := x.ViolationFeedback[operationID]; text != "" {
		a.mu.Unlock()
		return nil, errors.New(text)
	}
	var envelope struct {
		Choice  ChoiceIdentity `json:"choice"`
		Payload map[string]any `json:"payload"`
	}
	err = decodeGraphTool(raw, &envelope)
	var payload map[string]string
	if err == nil && envelope.Payload == nil {
		err = errors.New("Choice payload must be a JSON object.")
	}
	if err == nil {
		_, payload, err = compiled.validateChoice(x.NodeID, envelope.Choice, envelope.Payload)
	}
	if err != nil {
		nodeID, count := x.NodeID, x.Violations+1
		a.mu.Unlock()
		feedback, loadErr := graphPrompt("invalid-choice.md", map[string]any{"error": err.Error(), "violationCount": count, "availableChoices": graphChoiceContracts(compiled, nodeID)})
		a.mu.Lock()
		defer a.mu.Unlock()
		if loadErr != nil {
			_ = a.endGraphLocked(runID, GraphRunResult{Kind: "failed", Error: loadErr.Error()}, "")
			return nil, loadErr
		}
		persistErr := a.graphChangeLocked(func(d *diskState) error {
			activation := d.graphActivation(activationID)
			if activation.Status != "running" {
				return errors.New("Activation already settled.")
			}
			if slices.Contains(activation.ViolationIDs, operationID) {
				return nil
			}
			activation.Violations++
			activation.ViolationIDs = append(activation.ViolationIDs, operationID)
			if activation.ViolationFeedback == nil {
				activation.ViolationFeedback = map[string]string{}
			}
			activation.ViolationFeedback[operationID] = feedback
			activation.UpdatedAt = now()
			return nil
		})
		if persistErr != nil {
			return nil, persistErr
		}
		if a.state.graphActivation(activationID).Violations >= 3 {
			_ = a.endGraphLocked(runID, GraphRunResult{Kind: "failed", Error: "Three Choice-contract violations in activation " + activationID + "."}, "")
		}
		return nil, errors.New(feedback)
	}
	defer a.mu.Unlock()
	err = a.graphChangeLocked(func(d *diskState) error {
		activation := d.graphActivation(activationID)
		if activation.Submission != nil {
			return errors.New("Choice already reserved.")
		}
		activation.Submission = &GraphChoiceSubmission{OperationID: operationID, TurnID: turnID, Choice: envelope.Choice, Payload: payload, ReceivedAt: now()}
		activation.Status, activation.AdapterState, activation.UpdatedAt = "sealing", "reserved", now()
		return nil
	})
	return map[string]any{"activationId": activationID, "reserved": err == nil, "accepted": false}, err
}

func (a *app) transitionGraphChoice(ctx context.Context, runID, activationID, stage string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.graphChangeLocked(func(d *diskState) error {
		r, x := d.graphRun(runID), d.graphActivation(activationID)
		if x != nil && x.Submission != nil && x.AdapterState == stage && (stage == "sealed" || stage == "drained" || stage == "accepted") {
			return nil
		}
		if ctx.Err() != nil || r.Status != "running" || x.Submission == nil || x.Status != "sealing" {
			return errors.New("Choice reservation is no longer active.")
		}
		previous := map[string]string{"sealed": "reserved", "drained": "sealed", "accepted": "drained"}
		if previous[stage] == "" || x.AdapterState != previous[stage] {
			return errors.New("Out-of-order Choice settlement transition.")
		}
		if stage == "accepted" {
			c, err := compileGraphSnapshot(r.Snapshot)
			if err != nil {
				return err
			}
			choice := c.availableChoices(x.NodeID)[x.Submission.Choice]
			mapping, err := compileOutput(choice.Output, "choice", choice.Input)
			if err != nil {
				return err
			}
			output, err := resolveGraphOutput(mapping, GraphOutputContext{Task: r.Input.Task, Choice: x.Submission.Payload, ChoiceInput: choice.Input})
			if err != nil {
				return err
			}
			x.Output, x.Status, x.EndedAt, x.Submission.AcceptedAt = output, "completed", now(), now()
		}
		x.AdapterState, x.UpdatedAt = stage, now()
		return nil
	})
}

func (a *app) completeGraphActivationLocked(runID string, result graphCompletion) {
	r := a.state.graphRun(runID)
	x := a.state.graphActivation(result.activationID)
	if r == nil || x == nil {
		return
	}
	if result.turnID != "" {
		if x.FinishedTurnID == result.turnID || x.CurrentTurnID != result.turnID {
			return
		}
		if err := a.graphChangeLocked(func(d *diskState) error {
			activation := d.graphActivation(x.ID)
			activation.FinishedTurnID = result.turnID
			if s := d.session(activation.SessionID); s != nil {
				activation.NativeEventEnd = len(s.Events)
			}
			return nil
		}); err != nil {
			_ = a.endGraphLocked(runID, GraphRunResult{Kind: "failed", Error: err.Error()}, "")
			return
		}
		x = a.state.graphActivation(result.activationID)
	}
	var unconfirmed *graphUnconfirmedError
	if errors.As(result.err, &unconfirmed) {
		result.drained = false
	}
	if !result.drained {
		detail := "Activation " + x.ID + " has unconfirmed owned-work drainage."
		if unconfirmed != nil {
			detail = "Activation " + x.ID + ": " + unconfirmed.Error()
		}
		_ = a.endGraphLocked(runID, GraphRunResult{Kind: "failed", Error: detail}, detail)
		_ = a.graphChangeLocked(func(d *diskState) error {
			activation := d.graphActivation(x.ID)
			activation.Error = detail
			activation.Status, activation.EndedAt, activation.UpdatedAt = "failed", now(), now()
			e := event("error", detail)
			if result.native != nil {
				e.Data = map[string]any{"toolCallsSettled": result.native.ToolCallsSettled, "uncertainToolCalls": result.native.UncertainToolCalls}
			}
			activation.Events = append(activation.Events, e)
			return nil
		})
	}
	r = a.state.graphRun(runID)
	if r.Status == "ending" {
		_ = a.graphChangeLocked(func(d *diskState) error {
			activation := d.graphActivation(x.ID)
			if graphActivationActive(activation.Status) {
				activation.Status, activation.EndedAt, activation.UpdatedAt = "interrupted", now(), now()
			}
			return nil
		})
		return
	}
	if result.err != nil {
		kind := "failed"
		if a.ctx.Err() != nil {
			kind = "interrupted"
		}
		_ = a.endGraphLocked(runID, GraphRunResult{Kind: kind, Error: result.err.Error()}, "")
		return
	}
	if native := result.native; native != nil && native.Outcome == "missing_choice" {
		err := a.graphChangeLocked(func(d *diskState) error {
			activation := d.graphActivation(x.ID)
			violationID := "omission:" + result.turnID
			if slices.Contains(activation.ViolationIDs, violationID) {
				return nil
			}
			activation.Violations++
			activation.ViolationIDs = append(activation.ViolationIDs, violationID)
			activation.Continuable = native.Continuable
			activation.UpdatedAt = now()
			if activation.Violations < 3 && native.Continuable {
				activation.Status, activation.Correction = "reserved", "missing-choice.md"
			}
			return nil
		})
		if err != nil {
			_ = a.endGraphLocked(runID, GraphRunResult{Kind: "failed", Error: err.Error()}, "")
			return
		}
		if a.state.graphActivation(x.ID).Violations >= 3 || !native.Continuable {
			_ = a.endGraphLocked(runID, GraphRunResult{Kind: "failed", Error: "Missing Choice exhausted the activation's correction contract or its session is not continuable."}, "")
		}
		return
	}
	if result.native != nil && (result.native.Outcome != "choice" || result.native.ChoiceState != "accepted") {
		_ = a.endGraphLocked(runID, GraphRunResult{Kind: "failed", Error: "Harness finished without a confirmed accepted Choice."}, "")
		return
	}
	var terminal *GraphRunResult
	err := a.graphChangeLocked(func(d *diskState) error {
		run, activation := d.graphRun(runID), d.graphActivation(x.ID)
		c, err := compileGraphSnapshot(run.Snapshot)
		if err != nil {
			return err
		}
		if result.native != nil {
			activation.Continuable = result.native.Continuable
		} else {
			activation.Status, activation.Output, activation.EndedAt, activation.UpdatedAt = "completed", result.output, now(), now()
		}
		if result.command != nil {
			code := result.command.ExitCode
			activation.CommandExitCode, activation.CommandResult, activation.CommandTruncated = &code, result.command.Output, result.command.Truncated
			entry := event("command", boundedText(result.command.Output, 64<<10))
			entry.Status = "completed"
			activation.Events = append(activation.Events, entry)
		}
		node := c.Definition.Nodes[x.NodeID]
		if node.Type == "fork" {
			for _, branch := range result.branches {
				// The causal round belongs to the sequential segment, not to a
				// required common Fork identity. Keep Fork provenance separately.
				addGraphDelivery(d, run, *activation, c.Connections[branch.connectionID], branch.output, branch.workspaceID, activation.WorkspaceID, activation.CausalID, activation.ID, branch.branchID)
			}
		} else if node.Type == "terminal" {
			addGraphDelivery(d, run, *activation, c.Connections["terminal:"+x.NodeID], activation.Output, activation.WorkspaceID, activation.OriginWorkspaceID, activation.CausalID, activation.ForkActivationID, activation.BranchID)
		} else {
			sub := activation.Submission
			if sub == nil || sub.AcceptedAt == "" {
				return errors.New("No accepted Choice is available for dispatch.")
			}
			if activation.JoinRoundID != "" {
				for i := range d.GraphJoinRounds {
					if d.GraphJoinRounds[i].ID == activation.JoinRoundID {
						d.GraphJoinRounds[i].Status, d.GraphJoinRounds[i].EndedAt = "completed", now()
					}
				}
			}
			if sub.Choice.Origin == "engine" {
				terminal = &GraphRunResult{Kind: "blocked", Choice: &sub.Choice, Output: activation.Output}
			} else {
				choice := c.Definition.Choices[sub.Choice.ID]
				if choice.Terminal {
					if activation.ForkActivationID != "" {
						return errors.New("An author terminal Choice cannot end an unreconverged parallel path.")
					}
					for _, other := range d.GraphActivations {
						if other.RunID == runID && other.ID != x.ID && graphActivationActive(other.Status) {
							return errors.New("Normal completion reached while other work remains active.")
						}
					}
					for _, round := range d.GraphJoinRounds {
						if round.RunID == runID && (round.Status == "collecting" || round.Status == "running") {
							return errors.New("Normal completion reached before all Join rounds reconverged.")
						}
					}
					for _, delivery := range d.GraphDeliveries {
						if delivery.RunID == runID && delivery.Status == "pending" {
							return errors.New("Normal completion reached with undelivered work.")
						}
					}
					terminal = &GraphRunResult{Kind: "completed", Choice: &sub.Choice, Output: activation.Output}
				} else {
					addGraphDelivery(d, run, *activation, c.Connections["choice:"+sub.Choice.ID], activation.Output, activation.WorkspaceID, activation.OriginWorkspaceID, activation.CausalID, activation.ForkActivationID, activation.BranchID)
				}
			}
		}
		if terminal != nil {
			return nil
		}
		return consumeGraphDeliveries(d, run, c)
	})
	if err != nil {
		_ = a.endGraphLocked(runID, GraphRunResult{Kind: "failed", Error: err.Error()}, "")
		return
	}
	if terminal != nil {
		_ = a.endGraphLocked(runID, *terminal, "")
	}
}
