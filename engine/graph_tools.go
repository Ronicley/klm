package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

func init() { graphConversationHooks = graphConversationFactory }

func decodeGraphTool(raw json.RawMessage, target any) error {
	if len(raw) > 256<<10 {
		return errors.New("Graph tool arguments exceed 256 KiB.")
	}
	switch target.(type) {
	case *graphInvokeArgs, *graphActivityUpdateArgs:
		_, invoke := target.(*graphInvokeArgs)
		if err := checkGraphActivityArgumentShape(raw, invoke); err != nil {
			return fmt.Errorf("Invalid graph tool arguments: %w", err)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("Invalid graph tool arguments: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("Expected one graph tool argument object.")
	}
	return nil
}
func graphTool(name, description string, properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}
}
func graphConversationFactory(a *app, t *turn, s Session) (*GraphAdapterHooks, error) {
	if s.GraphRunID != "" {
		return nil, errors.New("Private graph nodes require their activation binding.")
	}
	owner := s.ID
	mutable := s.ParentID == ""
	if !mutable {
		owner = s.ParentID
	}
	textField := map[string]any{"type": "string"}
	integer := map[string]any{"type": "integer"}
	tools := []map[string]any{
		graphTool("graph_catalog", "List project graphs, their initial node and agent descriptions for preparing the input task, and execution capabilities.", map[string]any{}),
		graphTool("graph_activities", "Inspect authorized activities, ordering, assessments and pending decisions.", map[string]any{}),
		graphTool("graph_get_run", "Read a real run's progress, result, activations and workspace associations.", map[string]any{"runId": textField}, "runId"),
		graphTool("graph_recent_events", "Read bounded recent events of one node in a run.", map[string]any{"runId": textField, "nodeId": textField, "limit": integer}, "runId", "nodeId"),
	}
	if mutable {
		tools = append(tools,
			graphTool("graph_invoke", "Invoke or schedule an expressly authorized graph activity. Selection alone is not authorization. Omitted workspace uses the project folder (original) without confirmation.", map[string]any{"operationId": textField, "graphId": textField, "objective": textField, "task": textField, "authorization": graphAuthorizationSchema(), "workspace": graphWorkspaceSchema(), "dependencies": map[string]any{"type": "array", "items": textField}, "priority": integer}, "operationId", "graphId", "objective", "task", "authorization"),
			graphTool("graph_assess", "Assess whether a completed run met its authorized objective; normal completion is not semantic success.", map[string]any{"activityId": textField, "runId": textField, "operationId": textField, "expectedVersion": integer, "satisfied": map[string]any{"type": "boolean"}, "reason": textField}, "activityId", "runId", "operationId", "expectedVersion", "satisfied", "reason"),
			graphTool("graph_update_activity", "Versioned activity update. Retry retains the activity authorization and needs concrete correction, a self-contained task and an explicit fresh/reuse decision after a prior run.", map[string]any{"activityId": textField, "operationId": textField, "expectedVersion": integer, "action": map[string]any{"type": "string", "enum": []string{"retry", "await_user", "abandon", "reprioritize", "change_dependencies"}}, "task": textField, "correction": textField, "workspace": graphWorkspaceSchema(), "userEventId": map[string]any{"type": "string", "description": "Real user event required for abandon, reprioritize or change_dependencies; not a workspace default justification or replacement activity authorization."}, "priority": integer, "dependencies": map[string]any{"type": "array", "items": textField}}, "activityId", "operationId", "expectedVersion", "action"))
	}
	a.mu.Lock()
	parent := a.state.session(owner)
	if parent == nil {
		a.mu.Unlock()
		return nil, errors.New("Graph conversation owner is unavailable.")
	}
	projection := a.state.graphProjection(owner)
	events := []map[string]string{}
	for _, e := range parent.Events {
		if e.Type == "user" {
			events = append(events, map[string]string{"eventId": e.ID, "text": boundedText(e.Text, 4096)})
		}
	}
	if len(events) > 12 {
		events = events[len(events)-12:]
	}
	a.mu.Unlock()
	catalog, err := a.graphCatalog(owner)
	if err != nil {
		catalog = map[string]any{"error": err.Error()}
	}
	prompt, err := graphPrompt("orchestrator.md", map[string]any{"mainConversationId": owner, "canInvoke": mutable, "selectedGraphId": projection.SelectedGraphID, "activeRun": projection.Run, "catalog": catalog, "recentUserMessages": events})
	if err != nil {
		return nil, err
	}
	t.prompt = prompt + "\n" + t.prompt
	hooks := &GraphAdapterHooks{Node: false, Tools: tools, Call: func(ctx context.Context, name string, raw json.RawMessage) (any, error) {
		return a.graphConversationCall(ctx, owner, mutable, name, raw)
	}}
	if t.graphNotificationID != "" {
		hooks.Finished = func(result GraphAdapterResult) { a.graphNotificationFinished(t.graphNotificationID, result) }
	}
	return hooks, nil
}

func (a *app) graphCatalog(conversationID string) (any, error) {
	a.mu.Lock()
	s := a.state.session(conversationID)
	if s == nil {
		a.mu.Unlock()
		return nil, errors.New("Conversation is unavailable.")
	}
	p := *a.state.project(s.ProjectID)
	a.mu.Unlock()
	a.authoringMu.Lock()
	catalog, err := loadAuthoring(p)
	a.authoringMu.Unlock()
	if err != nil {
		return nil, err
	}
	agents := make(map[string]AgentRecord, len(catalog.Agents))
	for _, agent := range catalog.Agents {
		agents[agent.ID] = agent
	}
	graphs := []map[string]any{}
	for _, g := range catalog.Graphs {
		entry := map[string]any{"id": g.ID, "name": g.Definition.Name, "description": g.Definition.Description, "enabled": g.Definition.Enabled, "revision": g.Revision}
		if node, ok := g.Definition.Nodes[g.Definition.InitialNode]; ok {
			initial := map[string]any{"id": g.Definition.InitialNode, "type": node.Type, "name": node.Name, "description": ""}
			if agent, ok := agents[node.Agent]; ok && node.Type == "agent" {
				initial["agentId"] = agent.ID
				initial["agentName"] = agent.Name
				initial["description"] = agent.Description
				if node.Name == "" {
					initial["name"] = agent.Name
				}
			}
			entry["initialNode"] = initial
		}
		graphs = append(graphs, entry)
	}
	return map[string]any{"graphs": graphs, "errors": catalog.Errors, "capabilities": GraphAdapterCapabilities()}, nil
}

func (a *app) graphInspectRunLocked(owner, runID string) (any, error) {
	r := a.state.graphRun(runID)
	if r == nil || r.ConversationID != owner {
		return nil, errors.New("Run does not belong to this conversation.")
	}
	activations := []map[string]any{}
	workspaces := []WorkspaceRecord{}
	uses := []WorkspaceUse{}
	rounds := []JoinRound{}
	deliveries := []GraphDelivery{}
	for _, x := range a.state.GraphActivations {
		if x.RunID == runID {
			activations = append(activations, map[string]any{"id": x.ID, "nodeId": x.NodeID, "occurrence": x.Occurrence, "status": x.Status, "workspaceId": x.WorkspaceID, "choice": x.Submission, "output": x.Output, "violations": x.Violations, "error": x.Error})
		}
	}
	seen := map[string]bool{}
	for _, use := range a.state.GraphWorkspaceUses {
		if use.RunID == runID {
			uses = append(uses, use)
			if !seen[use.WorkspaceID] {
				if w := a.state.graphWorkspace(use.WorkspaceID); w != nil {
					workspaces = append(workspaces, *w)
				}
				seen[use.WorkspaceID] = true
			}
		}
	}
	for _, w := range a.state.GraphWorkspaces {
		if w.CreatedByRunID == runID && !seen[w.ID] {
			workspaces = append(workspaces, w)
		}
	}
	for _, round := range a.state.GraphJoinRounds {
		if round.RunID == runID {
			rounds = append(rounds, round)
		}
	}
	for _, delivery := range a.state.GraphDeliveries {
		if delivery.RunID == runID {
			deliveries = append(deliveries, delivery)
		}
	}
	return map[string]any{"runId": r.ID, "activityId": r.ActivityID, "graphId": r.GraphID, "status": r.Status, "active": graphRunActive(r.Status), "task": r.Input.Task, "result": r.Result, "finalityError": r.FinalityError, "activations": activations, "joinRounds": rounds, "deliveries": deliveries, "workspaces": workspaces, "workspaceUses": uses}, nil
}

func (a *app) graphConversationCall(ctx context.Context, owner string, mutable bool, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "graph_catalog":
		var args struct{}
		if err := decodeGraphTool(raw, &args); err != nil {
			return nil, err
		}
		return a.graphCatalog(owner)
	case "graph_activities":
		var args struct{}
		if err := decodeGraphTool(raw, &args); err != nil {
			return nil, err
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		activities := []GraphActivity{}
		for _, activity := range a.state.GraphActivities {
			if activity.ConversationID == owner {
				activities = append(activities, activity)
			}
		}
		return activities, nil
	case "graph_get_run":
		var args struct {
			RunID string `json:"runId"`
		}
		if err := decodeGraphTool(raw, &args); err != nil {
			return nil, err
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.graphInspectRunLocked(owner, args.RunID)
	case "graph_recent_events":
		var args struct {
			RunID  string `json:"runId"`
			NodeID string `json:"nodeId"`
			Limit  int    `json:"limit"`
		}
		if err := decodeGraphTool(raw, &args); err != nil {
			return nil, err
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		r := a.state.graphRun(args.RunID)
		if r == nil || r.ConversationID != owner {
			return nil, errors.New("Run not found in this conversation.")
		}
		if _, ok := r.Snapshot.Graph.Definition.Nodes[args.NodeID]; !ok {
			return nil, errors.New("Node not found in this run.")
		}
		events := []Event{}
		for _, x := range a.state.GraphActivations {
			if x.RunID == r.ID && x.NodeID == args.NodeID {
				events = append(events, x.Events...)
				if s := a.state.session(x.SessionID); s != nil {
					end := x.NativeEventEnd
					if graphActivationActive(x.Status) {
						end = len(s.Events)
					}
					start := min(x.NativeEventStart, len(s.Events))
					end = min(max(start, end), len(s.Events))
					events = append(events, s.Events[start:end]...)
				}
			}
		}
		sort.SliceStable(events, func(i, j int) bool { return events[i].CreatedAt < events[j].CreatedAt })
		limit := args.Limit
		if limit <= 0 {
			limit = 15
		}
		limit = min(limit, 50)
		if len(events) > limit {
			events = events[len(events)-limit:]
		}
		for i := range events {
			events[i].Text = boundedText(events[i].Text, 4096)
			events[i].Data = nil
		}
		return events, nil
	}
	if !mutable {
		return nil, errors.New("Only the main conversation orchestrator may mutate graph activities.")
	}
	switch name {
	case "graph_invoke":
		return a.invokeGraphActivity(ctx, owner, raw)
	case "graph_assess":
		return a.assessGraphActivity(owner, raw)
	case "graph_update_activity":
		return a.updateGraphActivity(owner, raw)
	}
	return nil, fmt.Errorf("Unknown graph capability %s.", name)
}

func graphUserEvent(d *diskState, owner, eventID string) bool {
	s := d.session(owner)
	if s == nil || eventID == "" {
		return false
	}
	start := 0
	if d.tx != nil && d.tx.indices[owner] != nil {
		if i, ok := d.tx.indices[owner][eventID]; ok {
			e := s.Events[i]
			if staged, ok := d.tx.events[owner][i]; ok {
				e = staged
			}
			return e.ID == eventID && e.Type == "user"
		}
		start = d.tx.baseEvents[owner]
	}
	for _, event := range s.Events[start:] {
		if event.ID == eventID && event.Type == "user" {
			return true
		}
	}
	return false
}
func validateGraphWorkspaceDecision(d *diskState, owner string, decision GraphWorkspaceDecision, retry bool) error {
	normalizeGraphWorkspaceDecision(&decision)
	if decision.Mode != "original" && decision.Mode != "new_worktree" {
		return errors.New("workspace.mode: expected original or new_worktree; omission defaults to original.")
	}
	if decision.AuthorizationEventID != "" && !graphUserEvent(d, owner, decision.AuthorizationEventID) {
		return errors.New("workspace.authorizationEventId: optional legacy reference must identify a real user message in this conversation; omit it when no reference is needed.")
	}
	if decision.Attempt != "" && decision.Attempt != "fresh" && decision.Attempt != "reuse" {
		return errors.New("workspace.attempt: expected fresh or reuse.")
	}
	if retry && decision.Attempt != "fresh" && decision.Attempt != "reuse" {
		return errors.New("workspace.attempt: retry after a prior run requires the user's explicit fresh or reuse decision; defaulting mode to original does not choose the retry policy.")
	}
	if decision.Attempt == "reuse" && (decision.SourceRunID == "" || decision.Reuse["initial"] == "") {
		return errors.New("workspace.sourceRunId and workspace.reuse.initial: reuse requires a terminal source run ID and explicit association-to-workspace-ID mappings including initial.")
	}
	if decision.Attempt != "reuse" && len(decision.Reuse) > 0 {
		return errors.New("workspace.reuse: associations are accepted only with workspace.attempt=reuse.")
	}
	return nil
}

func (a *app) invokeGraphActivity(ctx context.Context, owner string, raw json.RawMessage) (any, error) {
	var args graphInvokeArgs
	if err := decodeGraphTool(raw, &args); err != nil {
		return nil, err
	}
	normalizeGraphWorkspaceDecision(&args.Workspace)
	if !validLinkedText(args.OperationID, 200) || !validAuthoringID(args.GraphID) || !validLinkedText(args.Task, 128<<10) || !validLinkedText(args.Objective, 16<<10) {
		return nil, errors.New("Provide valid operationId, graphId, objective and a self-contained task.")
	}
	a.mu.Lock()
	for _, activity := range a.state.GraphActivities {
		if activity.ConversationID == owner && slicesContainsString(activity.OperationIDs, args.OperationID) {
			result := a.graphActivityResultLocked(activity.ID)
			a.mu.Unlock()
			return result, nil
		}
	}
	s := a.state.session(owner)
	if s == nil || s.ParentID != "" || s.GraphRunID != "" {
		a.mu.Unlock()
		return nil, errors.New("Main conversation is unavailable.")
	}
	if err := validateGraphAuthorization(&a.state, owner, args.Authorization); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if err := validateGraphWorkspaceDecision(&a.state, owner, args.Workspace, false); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	order := uint64(1)
	for _, activity := range a.state.GraphActivities {
		if activity.ConversationID == owner && activity.Order >= order {
			order = activity.Order + 1
		}
	}
	activity := GraphActivity{ID: newID(), ConversationID: owner, ProjectID: s.ProjectID, GraphID: args.GraphID, Objective: args.Objective, Task: args.Task, Authorization: args.Authorization, Workspace: args.Workspace, Dependencies: args.Dependencies, Priority: args.Priority, Order: order, Status: "ready", Version: 1, OperationIDs: []string{args.OperationID}, CreatedAt: now(), UpdatedAt: now()}
	err := a.graphChangeLocked(func(d *diskState) error { d.GraphActivities = append(d.GraphActivities, activity); return nil })
	canStart := err == nil && a.state.activeGraphRun(owner) == nil && a.graphActivityReadyLocked(activity.ID) && a.graphFirstReadyLocked(owner) == activity.ID && !a.graphStarting[owner]
	if err == nil && !canStart {
		a.scheduleGraphActivitiesLocked()
	}
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if canStart {
		if err := a.startGraphActivity(ctx, activity.ID); err != nil {
			return nil, err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.graphActivityResultLocked(activity.ID), nil
}
func slicesContainsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (a *app) graphActivityResultLocked(activityID string) any {
	activity := a.state.graphActivity(activityID)
	var latest *GraphRun
	for i := range a.state.GraphRuns {
		run := &a.state.GraphRuns[i]
		if run.ActivityID == activityID {
			latest = run
		}
	}
	result := map[string]any{"activity": activity, "started": false}
	if latest != nil {
		result["runId"], result["runStatus"] = latest.ID, latest.Status
		result["started"] = graphRunActive(latest.Status)
	}
	return result
}

func (a *app) assessGraphActivity(owner string, raw json.RawMessage) (any, error) {
	var args struct {
		ActivityID      string `json:"activityId"`
		RunID           string `json:"runId"`
		OperationID     string `json:"operationId"`
		ExpectedVersion uint64 `json:"expectedVersion"`
		Satisfied       *bool  `json:"satisfied"`
		Reason          string `json:"reason"`
	}
	if err := decodeGraphTool(raw, &args); err != nil {
		return nil, err
	}
	if args.Satisfied == nil || !validLinkedText(args.OperationID, 200) || !validLinkedText(args.Reason, 16<<10) {
		return nil, errors.New("Provide an assessment, reason and stable operationId.")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.graphChangeLocked(func(d *diskState) error {
		activity := d.graphActivity(args.ActivityID)
		if activity == nil || activity.ConversationID != owner {
			return errors.New("Activity not found.")
		}
		if slicesContainsString(activity.OperationIDs, args.OperationID) {
			return nil
		}
		if activity.Version != args.ExpectedVersion {
			return errors.New("Activity version changed; read current state before updating.")
		}
		run := d.graphRun(args.RunID)
		if run == nil || run.ActivityID != activity.ID || graphRunActive(run.Status) {
			return errors.New("Assess a terminal run of this activity.")
		}
		for _, assessment := range activity.Assessments {
			if assessment.RunID == run.ID {
				return errors.New("This run already has an assessment.")
			}
		}
		if *args.Satisfied && run.Status != "completed" {
			return errors.New("Failure, built-in blocked and interruption do not satisfy a success prerequisite.")
		}
		if activity.Status == "running" || activity.Status == "abandoned" {
			return errors.New("Cannot change the objective state while running or abandoned.")
		}
		activity.Assessments = append(activity.Assessments, GraphAssessment{RunID: run.ID, Satisfied: *args.Satisfied, Reason: args.Reason, OperationID: args.OperationID, CreatedAt: now()})
		activity.Status = "awaiting_user"
		if *args.Satisfied {
			activity.Status = "succeeded"
		}
		activity.Version++
		activity.OperationIDs = append(activity.OperationIDs, args.OperationID)
		activity.UpdatedAt = now()
		return nil
	})
	if err != nil {
		return nil, err
	}
	a.scheduleGraphActivitiesLocked()
	return a.graphActivityResultLocked(args.ActivityID), nil
}

func (a *app) updateGraphActivity(owner string, raw json.RawMessage) (any, error) {
	var args graphActivityUpdateArgs
	if err := decodeGraphTool(raw, &args); err != nil {
		return nil, err
	}
	if !validLinkedText(args.OperationID, 200) {
		return nil, errors.New("A stable operationId is required.")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.graphChangeLocked(func(d *diskState) error {
		activity := d.graphActivity(args.ActivityID)
		if activity == nil || activity.ConversationID != owner {
			return errors.New("Activity not found.")
		}
		if slicesContainsString(activity.OperationIDs, args.OperationID) {
			return nil
		}
		if activity.Version != args.ExpectedVersion {
			return errors.New("Activity version changed; read current state before updating.")
		}
		if activity.Status == "running" {
			return errors.New("An active run cannot be stopped or replaced by the orchestrator.")
		}
		switch args.Action {
		case "retry":
			if !validLinkedText(args.Correction, 16<<10) || !validLinkedText(args.Task, 128<<10) {
				return errors.New("Retry requires correction (concrete corrective action) and task (self-contained instructions).")
			}
			if args.Workspace == nil {
				args.Workspace = &GraphWorkspaceDecision{}
			}
			normalizeGraphWorkspaceDecision(args.Workspace)
			if args.Workspace.Mode == "new_worktree" && args.Workspace.BaseRevision == "" {
				args.Workspace.BaseRevision = activity.Workspace.BaseRevision
			}
			if activity.Status == "abandoned" {
				return errors.New("An abandoned activity needs a new user-authorized invocation.")
			}
			var latest *GraphRun
			for i := range d.GraphRuns {
				if d.GraphRuns[i].ActivityID == activity.ID {
					latest = &d.GraphRuns[i]
				}
			}
			if err := validateGraphWorkspaceDecision(d, owner, *args.Workspace, latest != nil); err != nil {
				return err
			}
			if latest != nil && latest.Status == "completed" {
				assessed := false
				for _, assessment := range activity.Assessments {
					if assessment.RunID == latest.ID {
						if assessment.Satisfied {
							return errors.New("The objective is already assessed as satisfied; a new request needs its own authorized activity.")
						}
						assessed = true
					}
				}
				if !assessed {
					return errors.New("Assess the normally completed run before preparing its corrective attempt.")
				}
			}
			activity.Task, activity.Correction, activity.Workspace, activity.Status, activity.Error = args.Task, args.Correction, *args.Workspace, "ready", ""
		case "await_user":
			activity.Status = "awaiting_user"
		case "abandon", "reprioritize", "change_dependencies":
			if !graphUserEvent(d, owner, args.UserEventID) {
				return errors.New("Changing the user's sequence or priority requires their express event reference.")
			}
			if args.Action == "abandon" {
				activity.Status = "abandoned"
			}
			if args.Action == "reprioritize" {
				activity.Priority = args.Priority
			}
			if args.Action == "change_dependencies" {
				activity.Dependencies = args.Dependencies
			}
		default:
			return errors.New("action: expected retry, await_user, abandon, reprioritize or change_dependencies.")
		}
		activity.Version++
		activity.OperationIDs = append(activity.OperationIDs, args.OperationID)
		activity.UpdatedAt = now()
		return nil
	})
	if err != nil {
		return nil, err
	}
	a.scheduleGraphActivitiesLocked()
	return a.graphActivityResultLocked(args.ActivityID), nil
}
