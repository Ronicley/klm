package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (st *sqliteSessionTx) graphRunRecord(id string) (GraphRun, error) {
	var r GraphRun
	err := decodeSQLiteRecord(st.ctx, st.tx, "graph_runs", id, 4<<20, &r)
	return r, err
}
func (st *sqliteSessionTx) graphActivation(id string) (GraphActivation, error) {
	var x GraphActivation
	err := decodeSQLiteRecord(st.ctx, st.tx, "graph_activations", id, 4<<20, &x)
	return x, err
}
func (st *sqliteSessionTx) capturedGraph(id string) (GraphSnapshot, *CompiledGraph, error) {
	var snapshot GraphSnapshot
	err := decodeSQLiteRecord(st.ctx, st.tx, "graph_snapshots", sqliteOwnedKey(id, ""), maxStreamRecordBytes, &snapshot)
	if err != nil {
		return snapshot, nil, err
	}
	c, err := compileGraphSnapshot(snapshot)
	return snapshot, c, err
}

// Finalizers must drain after admission closes; they still use the single SQL
// writer and publish only committed records, with a bounded shutdown deadline.
func (a *app) persistSQLGraphLocked(change func(*sqliteSessionTx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.persistSQLLocked(ctx, change)
}
func (st *sqliteSessionTx) endGraph(r *GraphRun, result GraphRunResult, uncertain string) error {
	if !graphRunActive(r.Status) {
		return errors.New("Run has already ended.")
	}
	if r.Status != "ending" {
		if r.Revision == ^uint64(0) {
			return errors.New("Graph revision exhausted.")
		}
		r.Status, r.Result, r.UpdatedAt = "ending", &result, now()
		r.Revision++
	}
	if uncertain != "" {
		r.FinalityError = uncertain
	}
	return st.putRoot("graph_runs", r.ID, *r)
}
func (a *app) cancelSQLGraphLocked(id string) {
	if g := a.graphRuns[id]; g != nil {
		g.cancel()
	}
	a.signalGraphLocked(id)
}
func (a *app) endSQLGraphLocked(id string, result GraphRunResult, uncertain string) error {
	err := a.persistSQLGraphLocked(func(st *sqliteSessionTx) error {
		r, err := st.graphRunRecord(id)
		if err != nil {
			return err
		}
		return st.endGraph(&r, result, uncertain)
	})
	if err == nil {
		a.cancelSQLGraphLocked(id)
	}
	return err
}
func (a *app) finishSQLGraphRunLocked(id string) error {
	return a.persistSQLGraphLocked(func(st *sqliteSessionTx) error {
		r, err := st.graphRunRecord(id)
		if err != nil {
			return err
		}
		if r.Status != "ending" || r.Result == nil {
			return errors.New("Run cannot publish an unsettled result.")
		}
		if r.Revision == ^uint64(0) {
			return errors.New("Graph revision exhausted.")
		}
		if r.FinalityError != "" {
			r.Result.Kind = "failed"
			if r.Result.Error == "" {
				r.Result.Error = r.FinalityError
			} else if !strings.Contains(r.Result.Error, r.FinalityError) {
				r.Result.Error += "\n" + r.FinalityError
			}
		}
		stamp := now()
		if err := st.eachMatching("graph_activations", `x_runId=? AND status IN ('reserved','running','waiting_user','sealing')`, []any{id}, func(key string) error {
			x, err := st.graphActivation(key)
			if err != nil {
				return err
			}
			x.Status, x.EndedAt, x.UpdatedAt = "interrupted", stamp, stamp
			return st.putRoot("graph_activations", key, x)
		}); err != nil {
			return err
		}
		if err := st.eachMatching("graph_deliveries", `x_runId=? AND status='pending'`, []any{id}, func(key string) error {
			var d GraphDelivery
			if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_deliveries", key, 4<<20, &d); err != nil {
				return err
			}
			d.Status = "interrupted"
			return st.putRoot("graph_deliveries", key, d)
		}); err != nil {
			return err
		}
		if err := st.eachMatching("graph_join_rounds", `x_runId=? AND status IN ('collecting','running')`, []any{id}, func(key string) error {
			var j JoinRound
			if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_join_rounds", key, 4<<20, &j); err != nil {
				return err
			}
			j.Status, j.EndedAt = "interrupted", stamp
			return st.putRoot("graph_join_rounds", key, j)
		}); err != nil {
			return err
		}
		if err := st.eachMatching("graph_workspaces", `x_createdByRunId=? AND status='reserved'`, []any{id}, func(key string) error {
			var w WorkspaceRecord
			if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_workspaces", key, 1<<20, &w); err != nil {
				return err
			}
			w.Status, w.Error = "interrupted", "Run ended before workspace creation settled."
			return st.putRoot("graph_workspaces", key, w)
		}); err != nil {
			return err
		}
		r.Status, r.EndedAt, r.UpdatedAt = r.Result.Kind, stamp, stamp
		r.Revision++
		if err := st.putRoot("graph_runs", id, r); err != nil {
			return err
		}
		activity, err := st.graphActivity(r.ConversationID, r.ActivityID)
		if err != nil {
			return err
		}
		if activity.Version == ^uint64(0) {
			return errors.New("Activity version exhausted.")
		}
		activity.Status, activity.UpdatedAt = "awaiting_assessment", stamp
		activity.Version++
		if err := st.putRoot("graph_activities", activity.ID, activity); err != nil {
			return err
		}
		n := RunNotification{ID: "graph-result:" + id, RunID: id, ConversationID: r.ConversationID, Status: "pending", CreatedAt: stamp}
		if err := st.putRoot("graph_notifications", n.ID, n); err != nil {
			return err
		}
		var timing AnalyticsGraphTiming
		key := sqliteOwnedKey("settings", id)
		err = decodeSQLiteRecord(st.ctx, st.tx, "analytics_graphs", key, 64<<10, &timing)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		at, drift := a.analyticsGraphInstant(id)
		if drift || r.Status == "interrupted" {
			timing.Interrupted = true
		} else if !timing.Interrupted {
			timing.Last = analyticsStamp(at)
		}
		timing.End = timing.Last
		return st.putExistingRecord("analytics_graphs", key, timing)
	})
}

func (st *sqliteSessionTx) graphViolation(x *GraphActivation, id, feedback string) (bool, error) {
	var exists bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_activation_violations WHERE owner_id=? AND scalar_value=?)`, x.ID, id).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	x.Violations++
	x.UpdatedAt = now()
	if err := st.appendScalarChild("activation_violations", x.ID, id); err != nil {
		return false, err
	}
	if feedback != "" {
		if err := st.putRecord("activation_feedback", sqliteOwnedKey(x.ID, id), x.ID, id, -1, feedback); err != nil {
			return false, err
		}
	}
	return true, st.putRoot("graph_activations", x.ID, *x)
}
func (a *app) reserveSQLGraphChoice(ctx context.Context, runID, activationID, turnID, callID string, raw json.RawMessage) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if callID == "" {
		callID = newID()
	}
	operation := "call:" + callID
	var rejection error
	ending := false
	err := a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		r, err := st.graphRunRecord(runID)
		if err != nil {
			return err
		}
		x, err := st.graphActivation(activationID)
		if err != nil {
			return err
		}
		if r.Status != "running" || x.RunID != runID || x.Status != "running" || x.CurrentTurnID != turnID {
			return errors.New("Activation is not accepting a Choice.")
		}
		if err := a.checkGraphChildrenBeforeChoice(st, x); err != nil {
			return err
		}
		var feedback string
		err = decodeSQLiteRecord(ctx, st.tx, "activation_feedback", sqliteOwnedKey(x.ID, operation), 128<<10, &feedback)
		if err == nil {
			return errors.New(feedback)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, compiled, err := st.capturedGraph(runID)
		if err != nil {
			return err
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
			// graphPrompt may read disk; load it after this transaction closes.
			rejection = err
			return nil
		}
		if x.Submission != nil {
			return errors.New("Choice already reserved.")
		}
		x.Submission = &GraphChoiceSubmission{OperationID: operation, TurnID: turnID, Choice: envelope.Choice, Payload: payload, ReceivedAt: now()}
		x.Status, x.AdapterState, x.UpdatedAt = "sealing", "reserved", now()
		return st.putRoot("graph_activations", x.ID, x)
	})
	if err != nil {
		return nil, err
	}
	if rejection != nil {
		var node string
		var count int
		var choices any
		err = a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
			st := &sqliteSessionTx{ctx: ctx, tx: tx}
			x, err := st.graphActivation(activationID)
			if err != nil {
				return err
			}
			_, c, err := st.capturedGraph(runID)
			if err != nil {
				return err
			}
			node, count = x.NodeID, x.Violations+1
			choices = graphChoiceContracts(c, node)
			return nil
		})
		if err != nil {
			return nil, err
		}
		feedback, err := graphPrompt("invalid-choice.md", map[string]any{"error": rejection.Error(), "violationCount": count, "availableChoices": choices})
		if err != nil {
			_ = a.endSQLGraphLocked(runID, GraphRunResult{Kind: "failed", Error: err.Error()}, "")
			return nil, err
		}
		err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
			r, err := st.graphRunRecord(runID)
			if err != nil {
				return err
			}
			x, err := st.graphActivation(activationID)
			if err != nil {
				return err
			}
			if r.Status != "running" || x.Status != "running" || x.CurrentTurnID != turnID {
				return errors.New("Activation already settled.")
			}
			if _, err := st.graphViolation(&x, operation, feedback); err != nil {
				return err
			}
			if x.Violations >= 3 {
				ending = true
				return st.endGraph(&r, GraphRunResult{Kind: "failed", Error: "Three Choice-contract violations in activation " + activationID + "."}, "")
			}
			return nil
		})
		if ending && err == nil {
			a.cancelSQLGraphLocked(runID)
		}
		if err != nil {
			return nil, err
		}
		return nil, errors.New(feedback)
	}
	return map[string]any{"activationId": activationID, "reserved": true, "accepted": false}, nil
}
func (a *app) transitionSQLGraphChoice(ctx context.Context, runID, activationID, stage string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		r, err := st.graphRunRecord(runID)
		if err != nil {
			return err
		}
		x, err := st.graphActivation(activationID)
		if err != nil {
			return err
		}
		if x.RunID != r.ID {
			return errors.New("Activation belongs to another run.")
		}
		if x.Submission != nil && x.AdapterState == stage && (stage == "sealed" || stage == "drained" || stage == "accepted") {
			return nil
		}
		if r.Status != "running" || x.Submission == nil || x.Status != "sealing" {
			return errors.New("Choice reservation is no longer active.")
		}
		previous := map[string]string{"sealed": "reserved", "drained": "sealed", "accepted": "drained"}
		if previous[stage] == "" || x.AdapterState != previous[stage] {
			return errors.New("Out-of-order Choice settlement transition.")
		}
		if stage == "accepted" {
			if err := a.checkGraphChildrenBeforeChoice(st, x); err != nil {
				return err
			}
			_, c, err := st.capturedGraph(runID)
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
		return st.putRoot("graph_activations", x.ID, x)
	})
}

func (st *sqliteSessionTx) nextGraphOccurrence(table, run, node string) (uint64, error) {
	var value string
	err := st.tx.QueryRowContext(st.ctx, `SELECT x_occurrence FROM data_`+table+` WHERE x_runId=? AND x_nodeId=? ORDER BY length(x_occurrence) DESC,x_occurrence DESC LIMIT 1`, run, node).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n == ^uint64(0) {
		return 0, errors.New("Graph occurrence exhausted or invalid.")
	}
	return n + 1, nil
}
func (st *sqliteSessionTx) nextGraphActivation(r GraphRun, node string, input map[string]string, workspace, origin, causal, policy, fork, branch, round string) (GraphActivation, error) {
	n, err := st.nextGraphOccurrence("graph_activations", r.ID, node)
	if err != nil {
		return GraphActivation{}, err
	}
	x := GraphActivation{ID: newID(), RunID: r.ID, NodeID: node, Occurrence: n, Status: "reserved", Input: input, WorkspaceID: workspace, OriginWorkspaceID: origin, CausalID: causal, SessionPolicy: policy, ForkActivationID: fork, BranchID: branch, JoinRoundID: round, CreatedAt: now(), UpdatedAt: now()}
	return x, st.putRoot("graph_activations", x.ID, x)
}
func (st *sqliteSessionTx) addGraphDelivery(r GraphRun, x GraphActivation, edge GraphConnection, payload map[string]string, workspace, origin, causal, fork, branch string) error {
	var exists bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_deliveries WHERE x_producerActivationId=? AND x_connectionId=?)`, x.ID, edge.ID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return errors.New("Graph production already dispatched.")
	}
	d := GraphDelivery{ID: newID(), RunID: r.ID, ConnectionID: edge.ID, ProducerActivationID: x.ID, TargetNodeID: edge.To, Payload: payload, WorkspaceID: workspace, OriginWorkspaceID: origin, CausalID: causal, ForkActivationID: fork, BranchID: branch, Status: "pending", CreatedAt: now()}
	return st.putRoot("graph_deliveries", d.ID, d)
}
func (st *sqliteSessionTx) consumeGraphDeliveries(r GraphRun, c *CompiledGraph) error {
	if r.Status != "running" {
		return nil
	}
	return st.eachMatching("graph_deliveries", `x_runId=? AND status='pending'`, []any{r.ID}, func(id string) error {
		var d GraphDelivery
		if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_deliveries", id, 4<<20, &d); err != nil {
			return err
		}
		node := c.Definition.Nodes[d.TargetNodeID]
		edge := c.Connections[d.ConnectionID]
		if node.Type != "join" {
			x, err := st.nextGraphActivation(r, d.TargetNodeID, d.Payload, d.WorkspaceID, d.OriginWorkspaceID, d.CausalID, edge.Session, d.ForkActivationID, d.BranchID, "")
			if err != nil {
				return err
			}
			d.TargetActivationID, d.Status = x.ID, "consumed"
			return st.putRoot("graph_deliveries", id, d)
		}
		var round JoinRound
		var key string
		err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_graph_join_rounds WHERE x_runId=? AND x_nodeId=? AND status IN ('collecting','running') LIMIT 1`, r.ID, d.TargetNodeID).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			var used bool
			if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_join_rounds WHERE x_runId=? AND x_nodeId=? AND x_causalId=?)`, r.ID, d.TargetNodeID, d.CausalID).Scan(&used); err != nil {
				return err
			}
			if used {
				return errors.New("A fresh Join round cannot reuse previous causal deliveries.")
			}
			n, err := st.nextGraphOccurrence("graph_join_rounds", r.ID, d.TargetNodeID)
			if err != nil {
				return err
			}
			round = JoinRound{ID: newID(), RunID: r.ID, NodeID: d.TargetNodeID, Occurrence: n, CausalID: d.CausalID, Status: "collecting", OriginWorkspaceID: d.OriginWorkspaceID, CreatedAt: now()}
			if err := st.putRoot("graph_join_rounds", round.ID, round); err != nil {
				return err
			}
			for _, expected := range c.Incoming[d.TargetNodeID] {
				if err := st.appendScalarChild("graph_join_expected", round.ID, expected); err != nil {
					return err
				}
			}
		} else if err != nil {
			return err
		} else if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_join_rounds", key, 4<<20, &round); err != nil {
			return err
		}
		if round.Status != "collecting" || round.CausalID != d.CausalID {
			return errors.New("Overlapping or causally unrelated Join rounds are invalid.")
		}
		if round.OriginWorkspaceID != d.OriginWorkspaceID {
			return errors.New("Join arrivals have ambiguous integration origins.")
		}
		var exists bool
		if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_join_arrivals WHERE owner_id=? AND x_connectionId=?)`, round.ID, d.ConnectionID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("Independent deliveries collapsed onto Join connection %s.", d.ConnectionID)
		}
		if edge.Kind == "choice" {
			if round.SessionPolicy != "" && round.SessionPolicy != edge.Session {
				return errors.New("Join session policies disagree.")
			}
			round.SessionPolicy = edge.Session
		}
		if err := st.appendScalarChild("graph_join_arrivals", round.ID, JoinArrival{ConnectionID: d.ConnectionID, DeliveryID: d.ID}); err != nil {
			return err
		}
		var expected, arrived int
		if err := st.tx.QueryRowContext(st.ctx, `SELECT (SELECT count(*) FROM data_graph_join_expected WHERE owner_id=?),(SELECT count(*) FROM data_graph_join_arrivals WHERE owner_id=?)`, round.ID, round.ID).Scan(&expected, &arrived); err != nil {
			return err
		}
		if expected == arrived {
			if round.SessionPolicy == "" {
				round.SessionPolicy = "new"
			}
			x, err := st.nextGraphActivation(r, round.NodeID, map[string]string{}, round.OriginWorkspaceID, round.OriginWorkspaceID, round.ID, round.SessionPolicy, "", "", round.ID)
			if err != nil {
				return err
			}
			round.ActivationID, round.Status = x.ID, "running"
		}
		if err := st.putRoot("graph_join_rounds", round.ID, round); err != nil {
			return err
		}
		d.JoinRoundID, d.Status = round.ID, "consumed"
		return st.putRoot("graph_deliveries", id, d)
	})
}

func (a *app) completeSQLGraphActivationLocked(runID string, result graphCompletion) {
	ending := false
	err := a.persistSQLGraphLocked(func(st *sqliteSessionTx) error {
		r, err := st.graphRunRecord(runID)
		if err != nil {
			return err
		}
		x, err := st.graphActivation(result.activationID)
		if err != nil {
			return err
		}
		if x.RunID != r.ID {
			return errors.New("Activation belongs to another run.")
		}
		if result.turnID != "" {
			if x.FinishedTurnID == result.turnID || x.CurrentTurnID != result.turnID {
				return nil
			}
			x.FinishedTurnID = result.turnID
			if x.SessionID != "" {
				if err := st.tx.QueryRowContext(st.ctx, `SELECT size FROM record_collections WHERE parent_table='sessions' AND parent_id=? AND field='events'`, x.SessionID).Scan(&x.NativeEventEnd); err != nil {
					return err
				}
			}
		} else if !graphActivationActive(x.Status) {
			return nil
		}
		end := func(outcome GraphRunResult, uncertain string) error {
			ending = true
			if err := st.putRoot("graph_activations", x.ID, x); err != nil {
				return err
			}
			return st.endGraph(&r, outcome, uncertain)
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
			x.Status, x.EndedAt, x.UpdatedAt, x.Error = "failed", now(), now(), detail
			e := event("error", detail)
			if result.native != nil {
				e.Data = map[string]any{"toolCallsSettled": result.native.ToolCallsSettled, "uncertainToolCalls": result.native.UncertainToolCalls}
			}
			if err := st.appendScalarChild("activation_events", x.ID, e); err != nil {
				return err
			}
			return end(GraphRunResult{Kind: "failed", Error: detail}, detail)
		}
		if r.Status == "ending" {
			if graphActivationActive(x.Status) {
				x.Status, x.EndedAt, x.UpdatedAt = "interrupted", now(), now()
			}
			return st.putRoot("graph_activations", x.ID, x)
		}
		if result.err != nil {
			kind := "failed"
			if a.ctx.Err() != nil {
				kind = "interrupted"
			}
			return end(GraphRunResult{Kind: kind, Error: result.err.Error()}, "")
		}
		if native := result.native; native != nil && native.Outcome == "missing_choice" {
			added, err := st.graphViolation(&x, "omission:"+result.turnID, "")
			if err != nil {
				return err
			}
			if !added {
				return nil
			}
			x.Continuable = native.Continuable
			if x.Violations >= 3 || !native.Continuable {
				return end(GraphRunResult{Kind: "failed", Error: "Missing Choice exhausted the correction contract or its session is not continuable."}, "")
			}
			x.Status, x.Correction = "reserved", "missing-choice.md"
			return st.putRoot("graph_activations", x.ID, x)
		}
		if result.native != nil && (result.native.Outcome != "choice" || result.native.ChoiceState != "accepted") {
			return end(GraphRunResult{Kind: "failed", Error: "Harness finished without a confirmed accepted Choice."}, "")
		}
		_, c, err := st.capturedGraph(runID)
		if err != nil {
			return err
		}
		if result.native != nil {
			x.Continuable = result.native.Continuable
		} else {
			x.Status, x.Output, x.EndedAt, x.UpdatedAt = "completed", result.output, now(), now()
		}
		if result.command != nil {
			code := result.command.ExitCode
			x.CommandExitCode, x.CommandResult, x.CommandTruncated = &code, result.command.Output, result.command.Truncated
			e := event("command", boundedText(result.command.Output, 64<<10))
			e.Status = "completed"
			if err := st.appendScalarChild("activation_events", x.ID, e); err != nil {
				return err
			}
		}
		if err := st.putRoot("graph_activations", x.ID, x); err != nil {
			return err
		}
		node := c.Definition.Nodes[x.NodeID]
		switch node.Type {
		case "fork":
			for _, branch := range result.branches {
				if err := st.addGraphDelivery(r, x, c.Connections[branch.connectionID], branch.output, branch.workspaceID, x.WorkspaceID, x.CausalID, x.ID, branch.branchID); err != nil {
					return err
				}
			}
		case "terminal":
			if err := st.addGraphDelivery(r, x, c.Connections["terminal:"+x.NodeID], x.Output, x.WorkspaceID, x.OriginWorkspaceID, x.CausalID, x.ForkActivationID, x.BranchID); err != nil {
				return err
			}
		default:
			sub := x.Submission
			if sub == nil || sub.AcceptedAt == "" {
				return errors.New("No accepted Choice is available for dispatch.")
			}
			if x.JoinRoundID != "" {
				var j JoinRound
				if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_join_rounds", x.JoinRoundID, 4<<20, &j); err != nil {
					return err
				}
				j.Status, j.EndedAt = "completed", now()
				if err := st.putRoot("graph_join_rounds", j.ID, j); err != nil {
					return err
				}
			}
			if sub.Choice.Origin == "engine" {
				return end(GraphRunResult{Kind: "blocked", Choice: &sub.Choice, Output: x.Output}, "")
			}
			if c.Definition.Choices[sub.Choice.ID].Terminal {
				if x.ForkActivationID != "" {
					return errors.New("An author terminal Choice cannot end an unreconverged parallel path.")
				}
				var pending bool
				if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_activations WHERE x_runId=? AND id<>? AND status IN ('reserved','running','waiting_user','sealing')) OR EXISTS(SELECT 1 FROM data_graph_join_rounds WHERE x_runId=? AND status IN ('collecting','running')) OR EXISTS(SELECT 1 FROM data_graph_deliveries WHERE x_runId=? AND status='pending')`, runID, x.ID, runID, runID).Scan(&pending); err != nil {
					return err
				}
				if pending {
					return errors.New("Normal completion reached while other work or Join dependencies remain active.")
				}
				return end(GraphRunResult{Kind: "completed", Choice: &sub.Choice, Output: x.Output}, "")
			}
			if err := st.addGraphDelivery(r, x, c.Connections["choice:"+sub.Choice.ID], x.Output, x.WorkspaceID, x.OriginWorkspaceID, x.CausalID, x.ForkActivationID, x.BranchID); err != nil {
				return err
			}
		}
		return st.consumeGraphDeliveries(r, c)
	})
	if err != nil {
		_ = a.endSQLGraphLocked(runID, GraphRunResult{Kind: "failed", Error: err.Error()}, "")
		return
	}
	if ending {
		a.cancelSQLGraphLocked(runID)
	}
}
