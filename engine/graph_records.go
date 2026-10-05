package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
)

// Graph contexts are owned by app.ctx, never by an invoking chat turn. Runtime
// reserves its disk record first, then creates this handle and starts I/O.
type graphExecution struct {
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	completed chan graphCompletion
	wake      chan struct{}
}

type GraphAuthorization struct {
	EventID string `json:"eventId"` // express user message in the owning conversation
	Text    string `json:"text"`    // authorized scope, not a semantic proof of consent
}
type GraphWorkspaceDecision struct {
	Mode                 string            `json:"mode"`                           // original (default) | new_worktree
	Attempt              string            `json:"attempt,omitempty"`              // fresh | reuse
	AuthorizationEventID string            `json:"authorizationEventId,omitempty"` // optional legacy user-event reference
	BaseRevision         string            `json:"baseRevision,omitempty"`
	SourceRunID          string            `json:"sourceRunId,omitempty"`
	Reuse                map[string]string `json:"reuse,omitempty"` // initial or node:<id>:<occurrence>[:branch:<id>] -> workspace ID
}
type GraphAssessment struct {
	RunID       string `json:"runId"`
	Satisfied   bool   `json:"satisfied"`
	Reason      string `json:"reason"`
	OperationID string `json:"operationId"`
	CreatedAt   string `json:"createdAt"`
}
type GraphActivity struct {
	Task           string                 `json:"task,omitempty"`
	Error          string                 `json:"error,omitempty"`
	ID             string                 `json:"id"`
	ConversationID string                 `json:"conversationId"`
	ProjectID      string                 `json:"projectId"`
	GraphID        string                 `json:"graphId"`
	Objective      string                 `json:"objective"`
	Authorization  []GraphAuthorization   `json:"authorization"`
	Status         string                 `json:"status"` // scheduled | ready | running | awaiting_user | awaiting_assessment | succeeded | abandoned
	Order          uint64                 `json:"order"`
	Priority       int                    `json:"priority"`
	Dependencies   []string               `json:"dependencies"` // activity IDs whose objective must be satisfied
	Assessments    []GraphAssessment      `json:"assessments,omitempty"`
	Correction     string                 `json:"correction,omitempty"`
	Workspace      GraphWorkspaceDecision `json:"workspace"`
	Version        uint64                 `json:"version"`
	OperationIDs   []string               `json:"operationIds,omitempty"`
	CreatedAt      string                 `json:"createdAt"`
	UpdatedAt      string                 `json:"updatedAt"`
}
type GraphRunInput struct {
	Task string `json:"task"`
}
type GraphRunResult struct {
	Kind   string            `json:"kind"` // completed | blocked | failed | interrupted
	Choice *ChoiceIdentity   `json:"choice,omitempty"`
	Output map[string]string `json:"output,omitempty"`
	Error  string            `json:"error,omitempty"`
}
type GraphRun struct {
	FinalityError      string                 `json:"finalityError,omitempty"`
	ID                 string                 `json:"id"`
	ActivityID         string                 `json:"activityId"`
	ProjectID          string                 `json:"projectId"`
	ConversationID     string                 `json:"conversationId"`
	GraphID            string                 `json:"graphId"`                  // captured identity; catalog rename never rewrites this
	CatalogGraphID     *string                `json:"catalogGraphId,omitempty"` // current catalog alias; empty after deletion
	Input              GraphRunInput          `json:"input"`
	Workspace          GraphWorkspaceDecision `json:"workspace"` // invocation-time decision retained across later attempts
	Snapshot           GraphSnapshot          `json:"snapshot"`
	Status             string                 `json:"status"` // starting | running | ending | completed | blocked | failed | interrupted
	Revision           uint64                 `json:"revision"`
	InitialWorkspaceID string                 `json:"initialWorkspaceId,omitempty"`
	Result             *GraphRunResult        `json:"result,omitempty"`
	CreatedAt          string                 `json:"createdAt"`
	UpdatedAt          string                 `json:"updatedAt"`
	EndedAt            string                 `json:"endedAt,omitempty"`
}
type GraphChoiceSubmission struct {
	OperationID string            `json:"operationId"`
	TurnID      string            `json:"turnId"`
	Choice      ChoiceIdentity    `json:"choice"`
	Payload     map[string]string `json:"payload"`
	ReceivedAt  string            `json:"receivedAt"`
	AcceptedAt  string            `json:"acceptedAt,omitempty"` // only after confirmed settlement
}
type GraphActivation struct {
	CurrentTurnID     string                 `json:"currentTurnId,omitempty"`
	FinishedTurnID    string                 `json:"finishedTurnId,omitempty"`
	NativeEventStart  int                    `json:"nativeEventStart,omitempty"`
	NativeEventEnd    int                    `json:"nativeEventEnd,omitempty"`
	OriginWorkspaceID string                 `json:"originWorkspaceId,omitempty"`
	SessionPolicy     string                 `json:"sessionPolicy,omitempty"`
	Correction        string                 `json:"correction,omitempty"`
	AdapterState      string                 `json:"adapterState,omitempty"`
	Continuable       bool                   `json:"continuable,omitempty"`
	IncomingCommit    string                 `json:"incomingCommit,omitempty"`
	ViolationFeedback map[string]string      `json:"violationFeedback,omitempty"`
	ID                string                 `json:"id"`
	RunID             string                 `json:"runId"`
	NodeID            string                 `json:"nodeId"`
	Occurrence        uint64                 `json:"occurrence"`
	Status            string                 `json:"status"` // reserved | running | waiting_user | sealing | completed | failed | interrupted
	Input             map[string]string      `json:"input"`
	WorkspaceID       string                 `json:"workspaceId,omitempty"`
	SessionID         string                 `json:"sessionId,omitempty"`
	CausalID          string                 `json:"causalId"`
	ForkActivationID  string                 `json:"forkActivationId,omitempty"`
	BranchID          string                 `json:"branchId,omitempty"`
	JoinRoundID       string                 `json:"joinRoundId,omitempty"`
	Violations        int                    `json:"violations"`
	ViolationIDs      []string               `json:"violationIds,omitempty"` // call:<id> and omission:<turnID>
	Submission        *GraphChoiceSubmission `json:"submission,omitempty"`
	Output            map[string]string      `json:"output"`
	CommandExitCode   *int                   `json:"commandExitCode,omitempty"`
	CommandResult     string                 `json:"commandResult,omitempty"`
	CommandTruncated  bool                   `json:"commandTruncated,omitempty"`
	Events            []Event                `json:"events,omitempty"` // bounded normalized node/Terminal events, not chat turns
	Error             string                 `json:"error,omitempty"`
	CreatedAt         string                 `json:"createdAt"`
	UpdatedAt         string                 `json:"updatedAt"`
	EndedAt           string                 `json:"endedAt,omitempty"`
}
type GraphDelivery struct {
	ID                   string            `json:"id"`
	RunID                string            `json:"runId"`
	ConnectionID         string            `json:"connectionId"`
	ProducerActivationID string            `json:"producerActivationId"`
	TargetNodeID         string            `json:"targetNodeId"`
	TargetActivationID   string            `json:"targetActivationId,omitempty"`
	Payload              map[string]string `json:"payload"`
	WorkspaceID          string            `json:"workspaceId"`
	OriginWorkspaceID    string            `json:"originWorkspaceId"`
	CausalID             string            `json:"causalId"`
	ForkActivationID     string            `json:"forkActivationId,omitempty"`
	BranchID             string            `json:"branchId,omitempty"`
	JoinRoundID          string            `json:"joinRoundId,omitempty"`
	Status               string            `json:"status"` // pending | consumed | interrupted
	CreatedAt            string            `json:"createdAt"`
}
type JoinArrival struct {
	ConnectionID string `json:"connectionId"`
	DeliveryID   string `json:"deliveryId"`
}
type JoinRound struct {
	ID                     string        `json:"id"`
	RunID                  string        `json:"runId"`
	NodeID                 string        `json:"nodeId"`
	Occurrence             uint64        `json:"occurrence"`
	CausalID               string        `json:"causalId"`
	Expected               []string      `json:"expected"`
	Arrivals               []JoinArrival `json:"arrivals"`
	Status                 string        `json:"status"` // collecting | running | completed | interrupted
	ActivationID           string        `json:"activationId,omitempty"`
	OriginWorkspaceID      string        `json:"originWorkspaceId"`
	IntegrationWorkspaceID string        `json:"integrationWorkspaceId,omitempty"`
	SessionPolicy          string        `json:"sessionPolicy,omitempty"`
	CreatedAt              string        `json:"createdAt"`
	EndedAt                string        `json:"endedAt,omitempty"`
}
type WorkspaceRecord struct {
	ID                    string `json:"id"`
	ProjectID             string `json:"projectId"`
	Directory             string `json:"directory"` // physical cwd, never Session.Workspace
	Repository            string `json:"repository,omitempty"`
	GitBranch             string `json:"gitBranch,omitempty"`
	BaseCommit            string `json:"baseCommit,omitempty"`
	Owned                 bool   `json:"owned"`
	Status                string `json:"status"` // reserved | ready | failed | interrupted
	OperationID           string `json:"operationId"`
	CreatedByRunID        string `json:"createdByRunId"`
	CreatedByActivationID string `json:"createdByActivationId,omitempty"`
	BranchID              string `json:"branchId,omitempty"`
	Error                 string `json:"error,omitempty"`
	CreatedAt             string `json:"createdAt"`
}
type WorkspaceUse struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspaceId"`
	RunID        string `json:"runId"`
	ActivationID string `json:"activationId,omitempty"`
	Association  string `json:"association"` // explicit key of GraphWorkspaceDecision.Reuse
	Reused       bool   `json:"reused"`
	CreatedAt    string `json:"createdAt"`
}
type RunNotification struct {
	Kind           string `json:"kind,omitempty"` // run_result (default) or activity_error
	ActivityID     string `json:"activityId,omitempty"`
	Error          string `json:"error,omitempty"`
	RetryAfter     string `json:"retryAfter,omitempty"`
	ID             string `json:"id"` // stable across uncertain redelivery, graph-result:<runID>
	RunID          string `json:"runId"`
	ConversationID string `json:"conversationId"`
	Status         string `json:"status"` // pending | delivering | delivered
	TurnID         string `json:"turnId,omitempty"`
	Attempts       uint64 `json:"attempts"`
	CreatedAt      string `json:"createdAt"`
	DeliveredAt    string `json:"deliveredAt,omitempty"`
}

// Public projections contain neither native histories nor node event payloads.
type GraphRunProjection struct {
	ID                 string      `json:"id"`
	GraphID            string      `json:"graphId"`
	Active             bool        `json:"active"`
	Status             string      `json:"status"`
	Revision           uint64      `json:"revision"`
	Snapshot           GraphRecord `json:"snapshot"`
	ActiveNodeIDs      []string    `json:"activeNodeIds"`
	CompletedNodeIDs   []string    `json:"completedNodeIds"`
	CompletedChoiceIDs []string    `json:"completedChoiceIds"` // raw graph IDs, no canvas prefix
	CollectingJoinIDs  []string    `json:"collectingJoinIds"`
}
type GraphRequestProjection struct {
	RunID        string           `json:"runId"`
	GraphID      string           `json:"graphId"`
	NodeID       string           `json:"nodeId"`
	NodeName     string           `json:"nodeName"`
	ActivationID string           `json:"activationId"`
	SessionID    string           `json:"sessionId"`
	RequestID    string           `json:"requestId"`
	Kind         string           `json:"kind"` // permission | question
	Permission   *Permission      `json:"permission,omitempty"`
	Question     *QuestionRequest `json:"question,omitempty"`
}
type ConversationGraphState struct {
	SessionID       string                   `json:"sessionId"`
	SelectedGraphID string                   `json:"selectedGraphId"` // "" means None
	Revision        uint64                   `json:"revision"`
	Run             *GraphRunProjection      `json:"run"` // active run only; null after terminal settlement
	Requests        []GraphRequestProjection `json:"requests"`
}

func graphRunActive(status string) bool {
	return status == "starting" || status == "running" || status == "ending"
}
func graphActivationActive(status string) bool {
	return status == "reserved" || status == "running" || status == "waiting_user" || status == "sealing"
}
func sameGraphDirectory(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
func (d *diskState) graphRun(id string) *GraphRun {
	for i := range d.GraphRuns {
		if d.GraphRuns[i].ID == id {
			return &d.GraphRuns[i]
		}
	}
	return nil
}
func (d *diskState) graphActivation(id string) *GraphActivation {
	for i := range d.GraphActivations {
		if d.GraphActivations[i].ID == id {
			return &d.GraphActivations[i]
		}
	}
	return nil
}
func (d *diskState) graphActivity(id string) *GraphActivity {
	for i := range d.GraphActivities {
		if d.GraphActivities[i].ID == id {
			return &d.GraphActivities[i]
		}
	}
	return nil
}
func (d *diskState) graphWorkspace(id string) *WorkspaceRecord {
	for i := range d.GraphWorkspaces {
		if d.GraphWorkspaces[i].ID == id {
			return &d.GraphWorkspaces[i]
		}
	}
	return nil
}
func (d *diskState) activeGraphRun(conversationID string) *GraphRun {
	for i := range d.GraphRuns {
		r := &d.GraphRuns[i]
		if r.ConversationID == conversationID && graphRunActive(r.Status) {
			return r
		}
	}
	return nil
}

func (d *diskState) graphProjection(conversationID string) ConversationGraphState {
	p := ConversationGraphState{SessionID: conversationID, Revision: d.GraphViewRevision, Requests: []GraphRequestProjection{}}
	if s := d.session(conversationID); s != nil {
		p.SelectedGraphID = s.SelectedGraphID
	}
	r := d.activeGraphRun(conversationID)
	if r == nil {
		return p
	}
	view := &GraphRunProjection{ID: r.ID, GraphID: r.GraphID, Active: true, Status: r.Status, Revision: d.GraphViewRevision, Snapshot: r.Snapshot.Graph, ActiveNodeIDs: []string{}, CompletedNodeIDs: []string{}, CompletedChoiceIDs: []string{}, CollectingJoinIDs: []string{}}
	if r.CatalogGraphID != nil {
		view.GraphID = *r.CatalogGraphID
	}
	active, completed, choices := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, activation := range d.GraphActivations {
		if activation.RunID != r.ID {
			continue
		}
		if graphActivationActive(activation.Status) {
			active[activation.NodeID] = true
		}
		if activation.Status == "completed" {
			completed[activation.NodeID] = true
			if sub := activation.Submission; sub != nil && sub.AcceptedAt != "" && sub.Choice.Origin == "graph" {
				choices[sub.Choice.ID] = true
			}
		}
		if !graphActivationActive(activation.Status) || activation.SessionID == "" {
			continue
		}
		s := d.session(activation.SessionID)
		if s == nil {
			continue
		}
		base := GraphRequestProjection{RunID: r.ID, GraphID: r.GraphID, NodeID: activation.NodeID, NodeName: r.Snapshot.Graph.Definition.Nodes[activation.NodeID].Name, ActivationID: activation.ID, SessionID: s.ID}
		if base.NodeName == "" {
			base.NodeName = activation.NodeID
		}
		for _, permission := range s.Permissions {
			request := base
			copy := permission
			request.Kind, request.RequestID, request.Permission = "permission", permission.ID, &copy
			p.Requests = append(p.Requests, request)
		}
		for _, question := range s.Questions {
			request := base
			copy := question
			request.Kind, request.RequestID, request.Question = "question", question.ID, &copy
			p.Requests = append(p.Requests, request)
		}
	}
	for _, round := range d.GraphJoinRounds {
		if round.RunID == r.ID && round.Status == "collecting" {
			view.CollectingJoinIDs = append(view.CollectingJoinIDs, round.NodeID)
			delete(completed, round.NodeID)
		}
	}
	for id := range active {
		delete(completed, id)
	}
	view.ActiveNodeIDs, view.CompletedNodeIDs, view.CompletedChoiceIDs = sortedGraphKeys(active), sortedGraphKeys(completed), sortedGraphKeys(choices)
	sort.Strings(view.CollectingJoinIDs)
	p.Run = view
	return p
}

// This is a view-only copy; projection data must not be stored on Session.
func (d *diskState) sessionView(id string) *Session {
	s := d.session(id)
	if s == nil {
		return nil
	}
	view := *s
	if s.ParentID == "" && s.GraphRunID == "" && s.Role != sessionRoleGeneralAgent {
		projection := d.graphProjection(id)
		view.Graph = &projection
	}
	return &view
}

// No executor/Git/script/native-session replay is performed here. Old native IDs
// and workspace artifacts are retained. Delivery retries retain notification IDs.
func interruptGraphState(d *diskState, cause string) bool {
	changed := false
	timestamp := now()
	for i := range d.GraphRuns {
		r := &d.GraphRuns[i]
		if !graphRunActive(r.Status) {
			continue
		}
		r.Status, r.UpdatedAt, r.EndedAt = "interrupted", timestamp, timestamp
		r.Revision++
		r.Result = &GraphRunResult{Kind: "interrupted", Error: cause}
		if activity := d.graphActivity(r.ActivityID); activity != nil {
			activity.Status, activity.UpdatedAt = "awaiting_assessment", timestamp
			activity.Version++
		}
		found := false
		for _, n := range d.GraphNotifications {
			if n.RunID == r.ID {
				found = true
			}
		}
		if !found {
			d.GraphNotifications = append(d.GraphNotifications, RunNotification{ID: "graph-result:" + r.ID, RunID: r.ID, ConversationID: r.ConversationID, Status: "pending", CreatedAt: timestamp})
		}
		changed = true
	}
	for i := range d.GraphActivations {
		x := &d.GraphActivations[i]
		if graphActivationActive(x.Status) {
			x.Status, x.Error, x.UpdatedAt, x.EndedAt = "interrupted", cause, timestamp, timestamp
			changed = true
		}
	}
	for i := range d.GraphDeliveries {
		x := &d.GraphDeliveries[i]
		if x.Status == "pending" {
			x.Status = "interrupted"
			changed = true
		}
	}
	for i := range d.GraphJoinRounds {
		x := &d.GraphJoinRounds[i]
		if x.Status == "collecting" || x.Status == "running" {
			x.Status, x.EndedAt = "interrupted", timestamp
			changed = true
		}
	}
	for i := range d.GraphWorkspaces {
		x := &d.GraphWorkspaces[i]
		if x.Status == "reserved" {
			x.Status, x.Error = "interrupted", cause
			changed = true
		}
	}
	for i := range d.GraphNotifications {
		x := &d.GraphNotifications[i]
		if x.Status == "delivering" {
			x.Status = "pending"
			changed = true
		}
	}
	if changed {
		d.GraphRevision++
		d.GraphViewRevision = d.GraphRevision
	}
	return changed
}

func validGraphTime(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

// Called before writing and during load (before restart reconciliation). Invalid
// data is refused, never "repaired" by dropping records or reusing active slots.
func validateGraphRecords(d *diskState) error {
	return validateGraphRecordsCached(d, nil)
}

type validatedGraphSnapshot struct {
	snapshot GraphSnapshot
	compiled *CompiledGraph
}

// Captured definitions are immutable across transitions. Validate/compile a new
// definition once, while still checking relational/lifecycle invariants each time.
func validateGraphRecordsCached(d *diskState, cache map[string]validatedGraphSnapshot) error {
	bad := func(kind, id string) error {
		return fmt.Errorf("Invalid graph %s %s in state.json; refusing to overwrite.", kind, id)
	}
	if (d.Version != 1 && d.Version != 2) || d.Projects == nil || d.Sessions == nil || d.Native == nil {
		return errors.New("Unsupported or incomplete state.json; refusing to overwrite.")
	}
	ids := map[string]bool{}
	claim := func(id string) bool {
		if id == "" || ids[id] {
			return false
		}
		ids[id] = true
		return true
	}
	for _, p := range d.Projects {
		if !claim(p.ID) {
			return bad("project identity", p.ID)
		}
	}
	generalCount := 0
	for _, s := range d.Sessions {
		engineOwned := s.Role == sessionRoleGeneralAgent || s.Role == sessionRoleSubagent && s.ProjectID == "" && d.session(s.ParentID) != nil && d.session(s.ParentID).Role == sessionRoleGeneralAgent
		if s.Role == sessionRoleGeneralAgent {
			generalCount++
			if generalCount > 1 || s.ProjectID != "" || s.ParentID != "" || s.GraphRunID != "" || s.SelectedGraphID != "" || s.Archived || !filepath.IsAbs(s.ExecutionCWD) {
				return bad("general conversation", s.ID)
			}
		}
		if !claim(s.ID) || !engineOwned && d.project(s.ProjectID) == nil || s.Events == nil {
			return bad("session identity", s.ID)
		}
	}
	for _, c := range d.Consultations {
		if !claim(c.ID) {
			return bad("consultation identity", c.ID)
		}
	}
	for key := range d.Native {
		if d.session(key) == nil {
			return bad("native session reference", key)
		}
	}
	for _, grant := range d.Grants {
		// Empty project means a global rule; empty harness means a normalized
		// policy scope, shared across harnesses. Both are persisted intentionally.
		if !claim(grant.ID) || grant.ProjectID != "" && d.project(grant.ProjectID) == nil {
			return bad("permission grant", grant.ID)
		}
		if grant.SessionID != "" {
			s := d.session(grant.SessionID)
			if s == nil || grant.ProjectID == "" && s.Role != sessionRoleGeneralAgent || s.ProjectID != grant.ProjectID || grant.Harness != "" && s.Harness != grant.Harness {
				return bad("permission grant owner", grant.ID)
			}
		}
	}
	for _, change := range d.GraphCatalogChanges {
		if !claim(change.ID) || d.project(change.ProjectID) == nil || !validAuthoringID(change.OldID) || change.NewID != "" && !validAuthoringID(change.NewID) || len(change.Writes) == 0 {
			return bad("catalog change", change.ID)
		}
		for path := range change.Writes {
			if path != graphFile(change.OldID) && path != layoutFile(change.OldID) && (change.NewID == "" || path != graphFile(change.NewID) && path != layoutFile(change.NewID)) {
				return bad("catalog change path", change.ID)
			}
		}
	}
	for _, activity := range d.GraphActivities {
		s := d.session(activity.ConversationID)
		if !claim(activity.ID) || s == nil || s.ParentID != "" || s.GraphRunID != "" || s.ProjectID != activity.ProjectID || !validAuthoringID(activity.GraphID) || strings.TrimSpace(activity.Objective) == "" || activity.Version == 0 || !validGraphTime(activity.CreatedAt) || !validGraphTime(activity.UpdatedAt) || !slices.Contains([]string{"scheduled", "ready", "running", "awaiting_user", "awaiting_assessment", "succeeded", "abandoned"}, activity.Status) {
			return bad("activity", activity.ID)
		}
		if err := validateGraphAuthorization(d, activity.ConversationID, activity.Authorization); err != nil {
			return fmt.Errorf("Graph activity %s: %w", activity.ID, err)
		}
		dependencies := map[string]bool{}
		for _, dependency := range activity.Dependencies {
			other := d.graphActivity(dependency)
			if other == nil || other.ID == activity.ID || other.ConversationID != activity.ConversationID || dependencies[dependency] {
				return bad("activity dependency", activity.ID)
			}
			dependencies[dependency] = true
		}
		operations := map[string]bool{}
		for _, op := range activity.OperationIDs {
			if op == "" || operations[op] {
				return bad("activity operation", activity.ID)
			}
			operations[op] = true
		}
		assessed := map[string]bool{}
		for _, assessment := range activity.Assessments {
			run := d.graphRun(assessment.RunID)
			if run == nil || run.ActivityID != activity.ID || graphRunActive(run.Status) || assessment.OperationID == "" || assessed[assessment.RunID] || !validGraphTime(assessment.CreatedAt) || assessment.Satisfied && (run.Status == "failed" || run.Status == "blocked" || run.Status == "interrupted") {
				return bad("assessment", activity.ID)
			}
			assessed[assessment.RunID] = true
		}
		if activity.Workspace.Mode != "original" && activity.Workspace.Mode != "new_worktree" {
			return bad("workspace mode", activity.ID)
		}
		if activity.Workspace.Attempt != "" && activity.Workspace.Attempt != "fresh" && activity.Workspace.Attempt != "reuse" {
			return bad("workspace attempt", activity.ID)
		}
		if activity.Workspace.SourceRunID != "" {
			source := d.graphRun(activity.Workspace.SourceRunID)
			if source == nil || source.ConversationID != activity.ConversationID {
				return fmt.Errorf("Graph activity %s workspace.sourceRunId: must identify a run in the owning conversation.", activity.ID)
			}
		}
		for key, id := range activity.Workspace.Reuse {
			w := d.graphWorkspace(id)
			if key == "" || w == nil || w.ProjectID != activity.ProjectID || activity.Workspace.Attempt != "reuse" {
				return fmt.Errorf("Graph activity %s workspace.reuse.%s: must map a nonempty association to a recorded workspace ID in this project, with workspace.attempt=reuse.", activity.ID, key)
			}
		}
	}
	// Dependency cycles can never become ready; reject without starting work.
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return false
		}
		if visited[id] {
			return true
		}
		visiting[id] = true
		for _, dep := range d.graphActivity(id).Dependencies {
			if !visit(dep) {
				return false
			}
		}
		delete(visiting, id)
		visited[id] = true
		return true
	}
	for _, activity := range d.GraphActivities {
		if !visit(activity.ID) {
			return bad("dependency cycle", activity.ID)
		}
	}
	activeRuns, activeActivities := map[string]bool{}, map[string]bool{}
	compiled := map[string]*CompiledGraph{}
	for _, r := range d.GraphRuns {
		activity := d.graphActivity(r.ActivityID)
		if !claim(r.ID) || activity == nil || r.ProjectID != activity.ProjectID || r.ConversationID != activity.ConversationID || r.GraphID != r.Snapshot.GraphID || strings.TrimSpace(r.Input.Task) == "" || r.Revision == 0 || !validGraphTime(r.CreatedAt) || !validGraphTime(r.UpdatedAt) {
			return bad("run", r.ID)
		}
		entry, valid := cache[r.ID]
		var c *CompiledGraph
		if valid && reflect.DeepEqual(entry.snapshot, r.Snapshot) {
			c = entry.compiled
		} else {
			if err := validateGraphSnapshot(r.Snapshot); err != nil {
				return fmt.Errorf("Run %s: %w", r.ID, err)
			}
			var err error
			c, err = compileGraphSnapshot(r.Snapshot)
			if err != nil {
				return err
			}
			if cache != nil {
				cache[r.ID] = validatedGraphSnapshot{snapshot: r.Snapshot, compiled: c}
			}
		}
		compiled[r.ID] = c
		if graphRunActive(r.Status) {
			if activeRuns[r.ConversationID] || r.EndedAt != "" || activity.Status != "running" {
				return bad("active slot", r.ID)
			}
			activeRuns[r.ConversationID], activeActivities[activity.ID] = true, true
			if r.Status != "ending" && r.Result != nil {
				return bad("premature result", r.ID)
			}
		} else if !slices.Contains([]string{"completed", "blocked", "failed", "interrupted"}, r.Status) || !validGraphTime(r.EndedAt) || r.Result == nil || r.Result.Kind != r.Status {
			return bad("run termination", r.ID)
		}
		if r.InitialWorkspaceID != "" {
			w := d.graphWorkspace(r.InitialWorkspaceID)
			if w == nil || w.ProjectID != r.ProjectID {
				return bad("initial workspace", r.ID)
			}
		}
		if r.Workspace.Mode != "original" && r.Workspace.Mode != "new_worktree" {
			return bad("run workspace decision", r.ID)
		}
		if r.Workspace.Attempt != "" && r.Workspace.Attempt != "fresh" && r.Workspace.Attempt != "reuse" {
			return bad("run attempt decision", r.ID)
		}
		for key, workspaceID := range r.Workspace.Reuse {
			w := d.graphWorkspace(workspaceID)
			if key == "" || w == nil || w.ProjectID != r.ProjectID || r.Workspace.Attempt != "reuse" {
				return bad("run workspace reuse", r.ID)
			}
		}
		if r.Status == "running" {
			w := d.graphWorkspace(r.InitialWorkspaceID)
			if w == nil || w.Status != "ready" {
				return bad("run workspace readiness", r.ID)
			}
		}
		if r.Result != nil {
			if r.Result.Kind == "blocked" && (r.Result.Choice == nil || *r.Result.Choice != (ChoiceIdentity{Origin: "engine", ID: "blocked"})) {
				return bad("blocked result", r.ID)
			}
			if r.Result.Kind == "completed" {
				if r.Result.Choice == nil || r.Result.Choice.Origin != "graph" || !c.Definition.Choices[r.Result.Choice.ID].Terminal {
					return bad("terminal result", r.ID)
				}
			}
		}
	}
	for _, activity := range d.GraphActivities {
		if activity.Status == "running" && !activeActivities[activity.ID] {
			return bad("running activity slot", activity.ID)
		}
	}
	occurrences, activeSessions := map[string]bool{}, map[string]bool{}
	for _, x := range d.GraphActivations {
		r := d.graphRun(x.RunID)
		if !claim(x.ID) || r == nil || x.Occurrence == 0 || x.Input == nil || x.CausalID == "" || x.Violations < 0 || x.Violations > 3 || !validGraphTime(x.CreatedAt) || !validGraphTime(x.UpdatedAt) {
			return bad("activation", x.ID)
		}
		n, exists := r.Snapshot.Graph.Definition.Nodes[x.NodeID]
		if !exists {
			return bad("activation node", x.ID)
		}
		key := fmt.Sprintf("%s:%s:%d", x.RunID, x.NodeID, x.Occurrence)
		if occurrences[key] {
			return bad("activation occurrence", x.ID)
		}
		occurrences[key] = true
		if graphActivationActive(x.Status) {
			if !graphRunActive(r.Status) || x.EndedAt != "" {
				return bad("active activation", x.ID)
			}
		} else if !slices.Contains([]string{"completed", "failed", "interrupted"}, x.Status) || !validGraphTime(x.EndedAt) {
			return bad("activation status", x.ID)
		}
		if x.WorkspaceID != "" {
			w := d.graphWorkspace(x.WorkspaceID)
			if w == nil || w.ProjectID != r.ProjectID {
				return bad("activation workspace", x.ID)
			}
		}
		if x.Status == "running" || x.Status == "waiting_user" || x.Status == "sealing" {
			w := d.graphWorkspace(x.WorkspaceID)
			if w == nil || w.Status != "ready" {
				return bad("activation workspace readiness", x.ID)
			}
		}
		if x.SessionID != "" {
			s := d.session(x.SessionID)
			if s == nil || s.GraphRunID != x.RunID || s.GraphNodeID != x.NodeID || n.Type != "agent" && n.Type != "join" {
				return bad("activation session", x.ID)
			}
			if w := d.graphWorkspace(x.WorkspaceID); w == nil || !sameGraphDirectory(w.Directory, s.ExecutionCWD) {
				return bad("native session directory", x.ID)
			}
			if graphActivationActive(x.Status) {
				if activeSessions[s.ID] {
					return bad("concurrent native session", x.ID)
				}
				activeSessions[s.ID] = true
			}
		}
		violations := map[string]bool{}
		for _, id := range x.ViolationIDs {
			if id == "" || violations[id] {
				return bad("violation deduplication", x.ID)
			}
			violations[id] = true
		}
		if len(x.ViolationIDs) != x.Violations {
			return bad("violation count", x.ID)
		}
		if x.ForkActivationID != "" {
			fork := d.graphActivation(x.ForkActivationID)
			if fork == nil || fork.RunID != x.RunID || r.Snapshot.Graph.Definition.Nodes[fork.NodeID].Type != "fork" {
				return bad("fork provenance", x.ID)
			}
			if _, ok := r.Snapshot.Graph.Definition.Nodes[fork.NodeID].Branches[x.BranchID]; !ok {
				return bad("branch provenance", x.ID)
			}
		}
		if sub := x.Submission; sub != nil {
			raw := map[string]any{}
			for k, v := range sub.Payload {
				raw[k] = v
			}
			if _, _, err := compiled[x.RunID].validateChoice(x.NodeID, sub.Choice, raw); err != nil || sub.OperationID == "" || sub.TurnID == "" || !validGraphTime(sub.ReceivedAt) {
				return bad("reserved Choice", x.ID)
			}
			if sub.AcceptedAt != "" && (!validGraphTime(sub.AcceptedAt) || x.Status != "completed") {
				return bad("Choice acceptance", x.ID)
			}
			if sub.AcceptedAt == "" && x.Status != "sealing" && x.Status != "failed" && x.Status != "interrupted" {
				return bad("Choice reservation state", x.ID)
			}
		}
		if x.Status == "completed" && (n.Type == "agent" || n.Type == "join") && (x.Submission == nil || x.Submission.AcceptedAt == "") {
			return bad("agent completion without Choice", x.ID)
		}
	}
	for _, s := range d.Sessions {
		if s.Graph != nil {
			return bad("persisted public projection", s.ID)
		}
		if s.GraphRunID == "" {
			if s.GraphNodeID != "" || s.ExecutionCWD != "" && s.Role != sessionRoleGeneralAgent || s.Role == "graph_node" {
				return bad("session ownership", s.ID)
			}
			continue
		}
		r := d.graphRun(s.GraphRunID)
		if r == nil || s.Role != "graph_node" || s.ParentID != "" || s.SelectedGraphID != "" || s.ProjectID != r.ProjectID || !filepath.IsAbs(s.ExecutionCWD) {
			return bad("private session ownership", s.ID)
		}
		n := r.Snapshot.Graph.Definition.Nodes[s.GraphNodeID]
		effective := r.Snapshot.EffectiveAgents[s.GraphNodeID]
		if n.Type != "agent" && n.Type != "join" || s.Harness != effective.Harness || s.Model != effective.Model || s.Effort != effective.Effort {
			return bad("private session settings", s.ID)
		}
	}
	deliveries := map[string]GraphDelivery{}
	produced := map[string]bool{}
	for _, x := range d.GraphDeliveries {
		producer := d.graphActivation(x.ProducerActivationID)
		c := compiled[x.RunID]
		if !claim(x.ID) || producer == nil || producer.RunID != x.RunID || producer.Status != "completed" || c == nil || x.Payload == nil || x.CausalID == "" || !validGraphTime(x.CreatedAt) || !slices.Contains([]string{"pending", "consumed", "interrupted"}, x.Status) {
			return bad("delivery", x.ID)
		}
		e, ok := c.Connections[x.ConnectionID]
		if !ok || e.To != x.TargetNodeID || !slices.Contains(e.Sources, producer.NodeID) {
			return bad("delivery connection", x.ID)
		}
		if e.Kind == "choice" && (producer.Submission == nil || producer.Submission.AcceptedAt == "" || producer.Submission.Choice != (ChoiceIdentity{Origin: "graph", ID: e.ChoiceID})) {
			return bad("delivery without accepted Choice", x.ID)
		}
		if x.Status == "pending" && !graphRunActive(d.graphRun(x.RunID).Status) {
			return bad("pending delivery in ended run", x.ID)
		}
		key := producer.ID + ":" + e.ID
		if produced[key] {
			return bad("duplicate produced delivery", x.ID)
		}
		produced[key] = true
		if x.TargetActivationID != "" {
			target := d.graphActivation(x.TargetActivationID)
			if target == nil || target.RunID != x.RunID || target.NodeID != x.TargetNodeID {
				return bad("delivery target", x.ID)
			}
		}
		for _, id := range []string{x.WorkspaceID, x.OriginWorkspaceID} {
			w := d.graphWorkspace(id)
			if w == nil || w.ProjectID != d.graphRun(x.RunID).ProjectID {
				return bad("delivery workspace", x.ID)
			}
		}
		deliveries[x.ID] = x
	}
	openRounds, roundOccurrences, arrived := map[string]bool{}, map[string]bool{}, map[string]bool{}
	rounds := map[string]JoinRound{}
	for _, round := range d.GraphJoinRounds {
		c := compiled[round.RunID]
		if !claim(round.ID) || c == nil || c.Definition.Nodes[round.NodeID].Type != "join" || round.Occurrence == 0 || round.CausalID == "" || !validGraphTime(round.CreatedAt) {
			return bad("Join round", round.ID)
		}
		key := round.RunID + ":" + round.NodeID
		occurrence := fmt.Sprintf("%s:%d", key, round.Occurrence)
		if roundOccurrences[occurrence] {
			return bad("Join occurrence", round.ID)
		}
		roundOccurrences[occurrence] = true
		if round.Status == "collecting" || round.Status == "running" {
			if openRounds[key] || !graphRunActive(d.graphRun(round.RunID).Status) || round.EndedAt != "" {
				return bad("overlapping Join rounds", round.ID)
			}
			openRounds[key] = true
		} else if !slices.Contains([]string{"completed", "interrupted"}, round.Status) || !validGraphTime(round.EndedAt) {
			return bad("Join status", round.ID)
		}
		expected := slices.Clone(round.Expected)
		sort.Strings(expected)
		if !slices.Equal(expected, c.Incoming[round.NodeID]) {
			return bad("Join expected inputs", round.ID)
		}
		connections := map[string]bool{}
		for _, arrival := range round.Arrivals {
			delivery, exists := deliveries[arrival.DeliveryID]
			if !exists || arrived[arrival.DeliveryID] || connections[arrival.ConnectionID] || delivery.ConnectionID != arrival.ConnectionID || delivery.RunID != round.RunID || delivery.TargetNodeID != round.NodeID || delivery.JoinRoundID != round.ID || delivery.CausalID != round.CausalID || !slices.Contains(round.Expected, arrival.ConnectionID) {
				return bad("Join arrival", round.ID)
			}
			connections[arrival.ConnectionID], arrived[arrival.DeliveryID] = true, true
		}
		if round.Status == "running" || round.Status == "completed" {
			activation := d.graphActivation(round.ActivationID)
			if len(round.Arrivals) != len(round.Expected) || activation == nil || activation.RunID != round.RunID || activation.NodeID != round.NodeID || activation.JoinRoundID != round.ID {
				return bad("Join activation before readiness", round.ID)
			}
		}
		if round.OriginWorkspaceID == "" || d.graphWorkspace(round.OriginWorkspaceID) == nil {
			return bad("Join origin", round.ID)
		}
		if round.IntegrationWorkspaceID != "" && d.graphWorkspace(round.IntegrationWorkspaceID) == nil {
			return bad("Join integration", round.ID)
		}
		if d.graphWorkspace(round.OriginWorkspaceID).ProjectID != d.graphRun(round.RunID).ProjectID || round.IntegrationWorkspaceID != "" && d.graphWorkspace(round.IntegrationWorkspaceID).ProjectID != d.graphRun(round.RunID).ProjectID {
			return bad("Join workspace project", round.ID)
		}
		if round.SessionPolicy != "" && round.SessionPolicy != "new" && round.SessionPolicy != "continue_target" {
			return bad("Join session policy", round.ID)
		}
		rounds[round.ID] = round
	}
	for _, x := range d.GraphActivations {
		if x.JoinRoundID != "" {
			round, ok := rounds[x.JoinRoundID]
			if !ok || round.RunID != x.RunID || round.ActivationID != x.ID {
				return bad("activation Join reference", x.ID)
			}
		}
	}
	for _, x := range d.GraphDeliveries {
		if x.JoinRoundID != "" {
			round, ok := rounds[x.JoinRoundID]
			if !ok || round.RunID != x.RunID || round.NodeID != x.TargetNodeID {
				return bad("delivery Join reference", x.ID)
			}
		}
	}
	workspaceOps := map[string]bool{}
	for _, w := range d.GraphWorkspaces {
		r := d.graphRun(w.CreatedByRunID)
		if !claim(w.ID) || r == nil || r.ProjectID != w.ProjectID || !filepath.IsAbs(w.Directory) || w.OperationID == "" || workspaceOps[w.OperationID] || !validGraphTime(w.CreatedAt) || !slices.Contains([]string{"reserved", "ready", "failed", "interrupted"}, w.Status) {
			return bad("workspace", w.ID)
		}
		workspaceOps[w.OperationID] = true
		if w.CreatedByActivationID != "" {
			x := d.graphActivation(w.CreatedByActivationID)
			if x == nil || x.RunID != w.CreatedByRunID {
				return bad("workspace creator", w.ID)
			}
		}
		if w.Status == "reserved" && !graphRunActive(r.Status) {
			return bad("workspace intent in ended run", w.ID)
		}
	}
	associations := map[string]bool{}
	for _, use := range d.GraphWorkspaceUses {
		w, r := d.graphWorkspace(use.WorkspaceID), d.graphRun(use.RunID)
		if !claim(use.ID) || w == nil || r == nil || w.ProjectID != r.ProjectID || use.Association == "" || !validGraphTime(use.CreatedAt) {
			return bad("workspace use", use.ID)
		}
		key := use.RunID + ":" + use.Association
		if associations[key] {
			return bad("duplicate workspace association", use.ID)
		}
		associations[key] = true
		if use.ActivationID != "" {
			x := d.graphActivation(use.ActivationID)
			if x == nil || x.RunID != r.ID {
				return bad("workspace use activation", use.ID)
			}
		}
		if use.Reused && w.CreatedByRunID == r.ID || !use.Reused && w.CreatedByRunID != r.ID {
			return bad("workspace reuse provenance", use.ID)
		}
	}
	notified := map[string]bool{}
	for _, notification := range d.GraphNotifications {
		if !claim(notification.ID) || !validGraphTime(notification.CreatedAt) || !slices.Contains([]string{"pending", "delivering", "delivered"}, notification.Status) {
			return bad("notification", notification.ID)
		}
		if notification.Kind == "activity_error" {
			activity := d.graphActivity(notification.ActivityID)
			if activity == nil || notification.RunID != "" || activity.ConversationID != notification.ConversationID {
				return bad("activity notification", notification.ID)
			}
		} else {
			r := d.graphRun(notification.RunID)
			if (notification.Kind != "" && notification.Kind != "run_result") || r == nil || graphRunActive(r.Status) || r.ConversationID != notification.ConversationID || notified[r.ID] {
				return bad("run notification", notification.ID)
			}
			notified[r.ID] = true
		}
		if notification.RetryAfter != "" && !validGraphTime(notification.RetryAfter) {
			return bad("notification retry", notification.ID)
		}
		if notification.Status == "delivering" && notification.TurnID == "" || notification.Status == "delivered" && (!validGraphTime(notification.DeliveredAt) || notification.TurnID == "") {
			return bad("notification delivery", notification.ID)
		}
	}
	for _, r := range d.GraphRuns {
		if !graphRunActive(r.Status) && !notified[r.ID] {
			return bad("missing terminal notification", r.ID)
		}
	}
	return nil
}

// Called under app.mu by future runtime before reserving a new run. Availability
// comes from durable state, including starting/ending, not just process handles.
func (a *app) graphSlotAvailableLocked(conversationID string) error {
	if a.closing || a.storageErr != nil {
		return errors.New("Engine is unavailable.")
	}
	s := a.state.session(conversationID)
	if s == nil || s.ParentID != "" || s.GraphRunID != "" {
		return errors.New("Graph runs require a main conversation.")
	}
	if p := a.state.project(s.ProjectID); p == nil || p.Removed {
		return errors.New("Graph project is unavailable.")
	}
	if a.state.activeGraphRun(conversationID) != nil {
		return errors.New("Conversation already has an active graph run.")
	}
	return nil
}
