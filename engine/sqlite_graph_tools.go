package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func (st *sqliteSessionTx) graphActivity(owner, id string) (GraphActivity, error) {
	var v GraphActivity
	err := decodeSQLiteRecord(st.ctx, st.tx, "graph_activities", id, 1<<20, &v)
	if err == nil && v.ConversationID != owner {
		err = sql.ErrNoRows
	}
	return v, err
}
func (st *sqliteSessionTx) graphRun(owner, id string) (GraphRun, error) {
	var v GraphRun
	err := decodeSQLiteRecord(st.ctx, st.tx, "graph_runs", id, 4<<20, &v)
	if err == nil && v.ConversationID != owner {
		err = sql.ErrNoRows
	}
	return v, err
}
func (st *sqliteSessionTx) graphAllowed(owner, graph string) error {
	r, err := st.activeAutomation(owner)
	if err != nil {
		return err
	}
	if r != nil && !slicesContainsString(r.Snapshot.Graphs, graph) {
		return errors.New("Graph is not enabled for this automation run.")
	}
	return nil
}
func (st *sqliteSessionTx) graphAuthorization(owner string, authorization []GraphAuthorization) error {
	if len(authorization) == 0 {
		return errors.New("Provide at least one real user-message reference authorizing this graph/activity.")
	}
	for i, auth := range authorization {
		valid, err := (sqliteValidation{ctx: st.ctx, tx: st.tx}).userEvent(owner, auth.EventID)
		if err != nil {
			return err
		}
		if !valid || strings.TrimSpace(auth.Text) == "" {
			return fmt.Errorf("authorization[%d] must reference a real user message and describe its authorized scope", i)
		}
	}
	return nil
}
func (st *sqliteSessionTx) graphWorkspaceDecision(owner, project string, d GraphWorkspaceDecision, retry bool) error {
	ref := d.AuthorizationEventID
	d.AuthorizationEventID = ""
	// All remaining checks in the shared validator are pure workspace policy.
	if err := validateGraphWorkspaceDecision(nil, owner, d, retry); err != nil {
		return err
	}
	if ref != "" {
		valid, err := (sqliteValidation{ctx: st.ctx, tx: st.tx}).userEvent(owner, ref)
		if err != nil {
			return err
		}
		if !valid {
			return errors.New("workspace.authorizationEventId must reference a real user message.")
		}
	}
	if d.SourceRunID != "" {
		if _, err := st.graphRun(owner, d.SourceRunID); err != nil {
			return errors.New("workspace.sourceRunId must identify a run in this conversation.")
		}
	}
	return (sqliteValidation{ctx: st.ctx, tx: st.tx}).workspaceDecision(project, d)
}
func (st *sqliteSessionTx) graphOperation(owner, operation string) (string, error) {
	var id string
	err := st.tx.QueryRowContext(st.ctx, `SELECT a.id FROM data_graph_operations o JOIN data_graph_activities a ON a.id=o.owner_id WHERE a.x_conversationId=? AND o.scalar_value=? ORDER BY a.position LIMIT 1`, owner, operation).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
func (st *sqliteSessionTx) graphDependencies(activity GraphActivity, dependencies []string) error {
	seen := map[string]bool{}
	for _, id := range dependencies {
		if id == activity.ID || seen[id] {
			return errors.New("Activity dependencies must be distinct other activities.")
		}
		seen[id] = true
		if _, err := st.graphActivity(activity.ConversationID, id); err != nil {
			return errors.New("Dependency does not belong to this conversation.")
		}
	}
	if err := st.clearChildren("graph_dependencies", activity.ID); err != nil {
		return err
	}
	for _, id := range dependencies {
		if err := st.appendScalarChild("graph_dependencies", activity.ID, id); err != nil {
			return err
		}
	}
	var cycle bool
	err := st.tx.QueryRowContext(st.ctx, `WITH RECURSIVE reach(id) AS (SELECT scalar_value FROM data_graph_dependencies WHERE owner_id=? UNION SELECT d.scalar_value FROM reach r JOIN data_graph_dependencies d ON d.owner_id=r.id) SELECT EXISTS(SELECT 1 FROM reach WHERE id=?)`, activity.ID, activity.ID).Scan(&cycle)
	if err != nil {
		return err
	}
	if cycle {
		return errors.New("Activity dependency cycle.")
	}
	return nil
}
func (st *sqliteSessionTx) latestGraphRun(activity string) (*GraphRun, error) {
	var id string
	err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_graph_runs WHERE x_activityId=? ORDER BY position DESC LIMIT 1`, activity).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r GraphRun
	err = decodeSQLiteRecord(st.ctx, st.tx, "graph_runs", id, 4<<20, &r)
	return &r, err
}
func sqlGraphWire(value any) (json.RawMessage, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || body[0] != '{' {
		return body, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{"version", "order", "revision", "occurrence", "attempts"} {
		if raw, ok := fields[key]; ok {
			var n uint64
			if err := json.Unmarshal(raw, &n); err != nil {
				return nil, err
			}
			fields[key], _ = json.Marshal(sqliteRevision(n))
		}
	}
	return json.Marshal(fields)
}
func (st *sqliteSessionTx) graphActivityResult(activity GraphActivity) (any, error) {
	encoded, err := sqlGraphWire(activity)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"activity": encoded, "started": false}
	latest, err := st.latestGraphRun(activity.ID)
	if err != nil {
		return nil, err
	}
	if latest != nil {
		result["runId"], result["runStatus"], result["started"] = latest.ID, latest.Status, graphRunActive(latest.Status)
	}
	return result, nil
}

func decodeSQLGraphVersion(raw json.RawMessage, target any) error {
	if len(raw) > 256<<10 {
		return errors.New("Graph tool arguments exceed 256 KiB.")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if version, ok := fields["expectedVersion"]; ok {
		var text string
		if err := json.Unmarshal(version, &text); err != nil {
			return errors.New("Use the returned decimal activity version.")
		}
		if _, err := sqlExpectedRevision(text); err != nil {
			return err
		}
		fields["expectedVersion"] = json.RawMessage(text)
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return decodeGraphTool(encoded, target)
}

func (a *app) sqlGraphConversationCall(ctx context.Context, owner string, mutable bool, name string, raw json.RawMessage) (any, error) {
	if name == "graph_catalog" {
		var args struct{}
		if err := decodeGraphTool(raw, &args); err != nil {
			return nil, err
		}
		return a.graphCatalog(owner)
	}
	if name == "graph_activities" || name == "graph_get_run" || name == "graph_recent_events" || name == "graph_record_content" {
		return a.readSQLGraphTool(ctx, owner, name, raw)
	}
	if !mutable {
		return nil, errors.New("Only the main conversation orchestrator may mutate graph activities.")
	}
	var invoke graphInvokeArgs
	var update graphActivityUpdateArgs
	var assess struct {
		ActivityID      string `json:"activityId"`
		RunID           string `json:"runId"`
		OperationID     string `json:"operationId"`
		ExpectedVersion uint64 `json:"expectedVersion"`
		Satisfied       *bool  `json:"satisfied"`
		Reason          string `json:"reason"`
	}
	switch name {
	case "graph_invoke":
		if err := decodeGraphTool(raw, &invoke); err != nil {
			return nil, err
		}
		normalizeGraphWorkspaceDecision(&invoke.Workspace)
		if !validLinkedText(invoke.OperationID, 200) || !validAuthoringID(invoke.GraphID) || !validLinkedText(invoke.Task, 128<<10) || !validLinkedText(invoke.Objective, 16<<10) {
			return nil, errors.New("Provide valid operationId, graphId, objective and a self-contained task.")
		}
	case "graph_update_activity":
		if err := decodeSQLGraphVersion(raw, &update); err != nil {
			return nil, err
		}
		if !validLinkedText(update.OperationID, 200) {
			return nil, errors.New("A stable operationId is required.")
		}
	case "graph_assess":
		if err := decodeSQLGraphVersion(raw, &assess); err != nil {
			return nil, err
		}
		if assess.Satisfied == nil || !validLinkedText(assess.OperationID, 200) || !validLinkedText(assess.Reason, 16<<10) {
			return nil, errors.New("Provide an assessment, reason and stable operationId.")
		}
	default:
		return nil, fmt.Errorf("Unknown graph capability %s.", name)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var result any
	err := a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		s, err := st.sessionMeta(owner)
		if err != nil {
			return err
		}
		if s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
			return errors.New("Main conversation is unavailable.")
		}
		available, err := st.projectAvailable(s.ProjectID)
		if err != nil {
			return err
		}
		if !available {
			return errors.New("Graph project is unavailable.")
		}
		if name == "graph_invoke" {
			id, err := st.graphOperation(owner, invoke.OperationID)
			if err != nil {
				return err
			}
			if id != "" {
				v, err := st.graphActivity(owner, id)
				if err != nil {
					return err
				}
				result, err = st.graphActivityResult(v)
				return err
			}
			if err := st.graphAuthorization(owner, invoke.Authorization); err != nil {
				return err
			}
			if err := st.graphAllowed(owner, invoke.GraphID); err != nil {
				return err
			}
			if err := st.graphWorkspaceDecision(owner, s.ProjectID, invoke.Workspace, false); err != nil {
				return err
			}
			var highest string
			err = st.tx.QueryRowContext(ctx, `SELECT x_order FROM data_graph_activities WHERE x_conversationId=? ORDER BY length(x_order) DESC,x_order DESC LIMIT 1`, owner).Scan(&highest)
			order := uint64(1)
			if err == nil {
				n, e := strconv.ParseUint(highest, 10, 64)
				if e != nil || n == ^uint64(0) {
					return errors.New("Graph activity order exhausted.")
				}
				order = n + 1
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			v := GraphActivity{ID: newID(), ConversationID: owner, ProjectID: s.ProjectID, GraphID: invoke.GraphID, Objective: invoke.Objective, Task: invoke.Task, Workspace: invoke.Workspace, Priority: invoke.Priority, Order: order, Status: "ready", Version: 1, CreatedAt: now(), UpdatedAt: now()}
			if err := st.putRoot("graph_activities", v.ID, v); err != nil {
				return err
			}
			for _, auth := range invoke.Authorization {
				if err := st.appendScalarChild("graph_authorization", v.ID, auth); err != nil {
					return err
				}
			}
			if err := st.graphDependencies(v, invoke.Dependencies); err != nil {
				return err
			}
			if err := st.appendScalarChild("graph_operations", v.ID, invoke.OperationID); err != nil {
				return err
			}
			result, err = st.graphActivityResult(v)
			return err
		}
		id, operation, expected := update.ActivityID, update.OperationID, update.ExpectedVersion
		if name == "graph_assess" {
			id, operation, expected = assess.ActivityID, assess.OperationID, assess.ExpectedVersion
		}
		v, err := st.graphActivity(owner, id)
		if err != nil {
			return err
		}
		var repeated bool
		if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_operations WHERE owner_id=? AND scalar_value=?)`, id, operation).Scan(&repeated); err != nil {
			return err
		}
		if repeated {
			result, err = st.graphActivityResult(v)
			return err
		}
		if v.Version != expected || v.Version == ^uint64(0) {
			return errors.New("Activity version changed; read current state before updating.")
		}
		if v.Status == "running" {
			return errors.New("An active run cannot be stopped or replaced by the orchestrator.")
		}
		if name == "graph_assess" {
			r, err := st.graphRun(owner, assess.RunID)
			if err != nil {
				return err
			}
			if r.ActivityID != v.ID || graphRunActive(r.Status) {
				return errors.New("Assess a terminal run of this activity.")
			}
			var exists bool
			if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_assessments WHERE owner_id=? AND x_runId=?)`, v.ID, r.ID).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return errors.New("This run already has an assessment.")
			}
			if *assess.Satisfied && r.Status != "completed" {
				return errors.New("Failure, blocked and interruption do not satisfy a success prerequisite.")
			}
			if v.Status == "abandoned" {
				return errors.New("Cannot change an abandoned activity.")
			}
			assessment := GraphAssessment{RunID: r.ID, Satisfied: *assess.Satisfied, Reason: assess.Reason, OperationID: operation, CreatedAt: now()}
			if err := st.appendScalarChild("graph_assessments", v.ID, assessment); err != nil {
				return err
			}
			v.Status = "awaiting_user"
			if *assess.Satisfied {
				v.Status = "succeeded"
			}
		} else {
			switch update.Action {
			case "retry":
				if err := st.graphAllowed(owner, v.GraphID); err != nil {
					return err
				}
				if !validLinkedText(update.Correction, 16<<10) || !validLinkedText(update.Task, 128<<10) {
					return errors.New("Retry requires concrete correction and a self-contained task.")
				}
				if v.Status == "abandoned" {
					return errors.New("An abandoned activity needs a new user-authorized invocation.")
				}
				if update.Workspace == nil {
					update.Workspace = &GraphWorkspaceDecision{}
				}
				normalizeGraphWorkspaceDecision(update.Workspace)
				if update.Workspace.Mode == "new_worktree" && update.Workspace.BaseRevision == "" {
					update.Workspace.BaseRevision = v.Workspace.BaseRevision
				}
				latest, err := st.latestGraphRun(v.ID)
				if err != nil {
					return err
				}
				if err := st.graphWorkspaceDecision(owner, v.ProjectID, *update.Workspace, latest != nil); err != nil {
					return err
				}
				if latest != nil && latest.Status == "completed" {
					var key string
					err := st.tx.QueryRowContext(ctx, `SELECT id FROM data_graph_assessments WHERE owner_id=? AND x_runId=? LIMIT 1`, v.ID, latest.ID).Scan(&key)
					if errors.Is(err, sql.ErrNoRows) {
						return errors.New("Assess the normally completed run before preparing its corrective attempt.")
					}
					if err != nil {
						return err
					}
					var item GraphAssessment
					if err := decodeSQLiteRecord(ctx, st.tx, "graph_assessments", key, 64<<10, &item); err != nil {
						return err
					}
					if item.Satisfied {
						return errors.New("The objective is already assessed as satisfied; a new request needs its own authorized activity.")
					}
				}
				v.Task, v.Correction, v.Workspace, v.Status, v.Error = update.Task, update.Correction, *update.Workspace, "ready", ""
			case "await_user":
				v.Status = "awaiting_user"
			case "abandon", "reprioritize", "change_dependencies":
				human, err := (sqliteValidation{ctx: ctx, tx: st.tx}).userEvent(owner, update.UserEventID)
				if err != nil {
					return err
				}
				if !human {
					return errors.New("Changing the user's sequence or priority requires their express event reference.")
				}
				if update.Action == "abandon" {
					v.Status = "abandoned"
				}
				if update.Action == "reprioritize" {
					v.Priority = update.Priority
				}
				if update.Action == "change_dependencies" {
					if err := st.graphDependencies(v, update.Dependencies); err != nil {
						return err
					}
				}
			default:
				return errors.New("Unknown activity update action.")
			}
		}
		v.Version++
		v.UpdatedAt = now()
		if err := st.putRoot("graph_activities", v.ID, v); err != nil {
			return err
		}
		if err := st.appendScalarChild("graph_operations", v.ID, operation); err != nil {
			return err
		}
		result, err = st.graphActivityResult(v)
		return err
	})
	if err != nil {
		return nil, err
	}
	a.scheduleGraphActivitiesLocked()
	return result, nil
}
