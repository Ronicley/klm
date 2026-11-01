package main

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func (v sqliteValidation) eachField(table, field, value string, visit func(string) error) error {
	if !sqliteKnownTable(table) || !slices.Contains(sqliteIndexFields[table], field) {
		return errors.New("invalid validation query")
	}
	last := ""
	first := true
	for {
		query := `SELECT id FROM data_` + table + ` WHERE ` + sqliteIndexColumn(field) + `=? AND id>? ORDER BY id LIMIT 1`
		args := []any{value, last}
		if first {
			query = `SELECT id FROM data_` + table + ` WHERE ` + sqliteIndexColumn(field) + `=? ORDER BY id LIMIT 1`
			args = []any{value}
		}
		var id string
		err := v.tx.QueryRowContext(v.ctx, query, args...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := visit(id); err != nil {
			return err
		}
		last, first = id, false
	}
}

func (v sqliteValidation) workspace(id string) (WorkspaceRecord, error) {
	var w WorkspaceRecord
	err := v.get("graph_workspaces", id, &w)
	return w, err
}
func (v sqliteValidation) run(id string) (GraphRun, error) {
	var r GraphRun
	err := v.get("graph_runs", id, &r)
	return r, err
}
func (v sqliteValidation) activation(id string) (GraphActivation, error) {
	var x GraphActivation
	err := v.get("graph_activations", id, &x)
	return x, err
}

func (v sqliteValidation) workspaceDecision(project string, d GraphWorkspaceDecision) error {
	if d.Mode != "original" && d.Mode != "new_worktree" || d.Attempt != "" && d.Attempt != "fresh" && d.Attempt != "reuse" {
		return errors.New("invalid graph workspace decision")
	}
	for key, id := range d.Reuse {
		w, err := v.workspace(id)
		if err != nil {
			return err
		}
		if key == "" || w.ProjectID != project || d.Attempt != "reuse" {
			return errors.New("invalid graph workspace reuse")
		}
	}
	return nil
}

func (v sqliteValidation) validateGraphs() error {
	checks := []struct{ label, query string }{
		{"graph dependency", `SELECT 1 FROM data_graph_dependencies d JOIN data_graph_activities a ON a.id=d.owner_id LEFT JOIN data_graph_activities dependency ON dependency.id=d.scalar_value WHERE dependency.id IS NULL OR dependency.id=a.id OR dependency.x_conversationId<>a.x_conversationId`},
		{"duplicate graph dependency", `SELECT owner_id,scalar_value FROM data_graph_dependencies GROUP BY owner_id,scalar_value HAVING count(*)>1`},
		{"graph dependency cycle", `WITH RECURSIVE reach(origin,target) AS (SELECT owner_id,scalar_value FROM data_graph_dependencies UNION SELECT r.origin,d.scalar_value FROM reach r JOIN data_graph_dependencies d ON d.owner_id=r.target) SELECT 1 FROM reach WHERE origin=target`},
		{"graph operations", `SELECT owner_id,scalar_value FROM data_graph_operations GROUP BY owner_id,scalar_value HAVING scalar_value='' OR count(*)>1`},
		{"graph assessment dedupe", `SELECT owner_id,x_runId FROM data_graph_assessments GROUP BY owner_id,x_runId HAVING count(*)>1`},
		{"graph active slots", `SELECT x_conversationId FROM data_graph_runs WHERE status IN ('starting','running','ending') GROUP BY x_conversationId HAVING count(*)>1`},
		{"running graph activity", `SELECT 1 FROM data_graph_activities a WHERE a.status='running' AND NOT EXISTS(SELECT 1 FROM data_graph_runs r WHERE r.x_activityId=a.id AND r.status IN ('starting','running','ending'))`},
		{"activation run", `SELECT 1 FROM data_graph_activations a LEFT JOIN data_graph_runs r ON r.id=a.x_runId WHERE r.id IS NULL`},
		{"activation occurrence", `SELECT x_runId,x_nodeId,x_occurrence FROM data_graph_activations GROUP BY x_runId,x_nodeId,x_occurrence HAVING count(*)>1`},
		{"concurrent native session", `SELECT session_id FROM data_graph_activations WHERE session_id<>'' AND status IN ('reserved','running','waiting_user','sealing') GROUP BY session_id HAVING count(*)>1`},
		{"activation violation dedupe", `SELECT owner_id,scalar_value FROM data_activation_violations GROUP BY owner_id,scalar_value HAVING scalar_value='' OR count(*)>1`},
		{"graph session run", `SELECT 1 FROM data_sessions s LEFT JOIN data_graph_runs r ON r.id=s.x_graphRunId WHERE s.x_graphRunId<>'' AND r.id IS NULL`},
		{"delivery run", `SELECT 1 FROM data_graph_deliveries d LEFT JOIN data_graph_runs r ON r.id=d.x_runId WHERE r.id IS NULL`},
		{"duplicate graph production", `SELECT x_producerActivationId,x_connectionId FROM data_graph_deliveries GROUP BY x_producerActivationId,x_connectionId HAVING count(*)>1`},
		{"join run", `SELECT 1 FROM data_graph_join_rounds j LEFT JOIN data_graph_runs r ON r.id=j.x_runId WHERE r.id IS NULL`},
		{"join occurrence", `SELECT x_runId,x_nodeId,x_occurrence FROM data_graph_join_rounds GROUP BY x_runId,x_nodeId,x_occurrence HAVING count(*)>1`},
		{"open join overlap", `SELECT x_runId,x_nodeId FROM data_graph_join_rounds WHERE status IN ('collecting','running') GROUP BY x_runId,x_nodeId HAVING count(*)>1`},
		{"join arrival dedupe", `SELECT x_deliveryId FROM data_graph_join_arrivals GROUP BY x_deliveryId HAVING count(*)>1`},
		{"join connection dedupe", `SELECT owner_id,x_connectionId FROM data_graph_join_arrivals GROUP BY owner_id,x_connectionId HAVING count(*)>1`},
		{"activation join reference", `SELECT 1 FROM data_graph_activations a LEFT JOIN data_graph_join_rounds j ON j.id=a.x_joinRoundId WHERE a.x_joinRoundId<>'' AND (j.id IS NULL OR j.x_runId<>a.x_runId OR j.x_activationId<>a.id)`},
		{"delivery join reference", `SELECT 1 FROM data_graph_deliveries d LEFT JOIN data_graph_join_rounds j ON j.id=d.x_joinRoundId WHERE d.x_joinRoundId<>'' AND (j.id IS NULL OR j.x_runId<>d.x_runId OR j.x_nodeId<>d.x_targetNodeId)`},
		{"workspace operation dedupe", `SELECT x_operationId FROM data_graph_workspaces GROUP BY x_operationId HAVING x_operationId='' OR count(*)>1`},
		{"workspace association dedupe", `SELECT x_runId,x_association FROM data_graph_workspace_uses GROUP BY x_runId,x_association HAVING count(*)>1`},
		{"terminal notification dedupe", `SELECT x_runId FROM data_graph_notifications WHERE x_kind<>'activity_error' GROUP BY x_runId HAVING count(*)>1`},
		{"missing terminal notification", `SELECT 1 FROM data_graph_runs r WHERE r.status NOT IN ('starting','running','ending') AND NOT EXISTS(SELECT 1 FROM data_graph_notifications n WHERE n.x_runId=r.id AND n.x_kind<>'activity_error')`},
	}
	for _, c := range checks {
		if err := v.rejectRows(c.label, c.query); err != nil {
			return err
		}
	}
	if err := v.validateGraphActivities(); err != nil {
		return err
	}
	if err := v.each("graph_runs", nil, func(id string) error {
		r, err := v.run(id)
		if err != nil {
			return err
		}
		var a GraphActivity
		if err := v.get("graph_activities", r.ActivityID, &a); err != nil {
			return err
		}
		if err := v.get("graph_snapshots", sqliteOwnedKey(id, ""), &r.Snapshot); err != nil {
			return err
		}
		if r.ProjectID != a.ProjectID || r.ConversationID != a.ConversationID || r.GraphID != r.Snapshot.GraphID || strings.TrimSpace(r.Input.Task) == "" || r.Revision == 0 || !validGraphTime(r.CreatedAt) || !validGraphTime(r.UpdatedAt) {
			return errors.New("invalid graph run")
		}
		if err := validateGraphSnapshot(r.Snapshot); err != nil {
			return err
		}
		compiled, err := compileGraphSnapshot(r.Snapshot)
		if err != nil {
			return err
		}
		if graphRunActive(r.Status) {
			if r.EndedAt != "" || a.Status != "running" || r.Status != "ending" && r.Result != nil {
				return errors.New("invalid active graph run")
			}
		} else if !slices.Contains([]string{"completed", "blocked", "failed", "interrupted"}, r.Status) || !validGraphTime(r.EndedAt) || r.Result == nil || r.Result.Kind != r.Status {
			return errors.New("invalid graph run termination")
		}
		if r.InitialWorkspaceID != "" {
			w, err := v.workspace(r.InitialWorkspaceID)
			if err != nil {
				return err
			}
			if w.ProjectID != r.ProjectID {
				return errors.New("graph initial workspace project mismatch")
			}
		}
		if err := v.workspaceDecision(r.ProjectID, r.Workspace); err != nil {
			return err
		}
		if r.Status == "running" {
			w, err := v.workspace(r.InitialWorkspaceID)
			if err != nil {
				return err
			}
			if w.Status != "ready" {
				return errors.New("graph workspace not ready")
			}
		}
		if r.Result != nil {
			if r.Result.Kind == "blocked" && (r.Result.Choice == nil || *r.Result.Choice != (ChoiceIdentity{Origin: "engine", ID: "blocked"})) {
				return errors.New("invalid blocked graph result")
			}
			if r.Result.Kind == "completed" && (r.Result.Choice == nil || r.Result.Choice.Origin != "graph" || !compiled.Definition.Choices[r.Result.Choice.ID].Terminal) {
				return errors.New("invalid terminal graph result")
			}
		}
		// One captured definition is resident at a time, even when validating a
		// run with millions of historical activations/deliveries/rounds.
		if err := v.eachField("sessions", "graphRunId", r.ID, func(sid string) error {
			s, err := v.session(sid)
			if err != nil {
				return err
			}
			node := r.Snapshot.Graph.Definition.Nodes[s.GraphNodeID]
			effective := r.Snapshot.EffectiveAgents[s.GraphNodeID]
			if s.ProjectID != r.ProjectID || node.Type != "agent" && node.Type != "join" || s.Harness != effective.Harness || s.Model != effective.Model || s.Effort != effective.Effort {
				return errors.New("graph private session settings mismatch")
			}
			return nil
		}); err != nil {
			return err
		}
		if err := v.eachField("graph_activations", "runId", r.ID, func(aid string) error { return v.validateGraphActivation(r, compiled, aid) }); err != nil {
			return err
		}
		if err := v.eachField("graph_deliveries", "runId", r.ID, func(did string) error { return v.validateGraphDelivery(r, compiled, did) }); err != nil {
			return err
		}
		return v.eachField("graph_join_rounds", "runId", r.ID, func(jid string) error { return v.validateGraphJoin(r, compiled, jid) })
	}); err != nil {
		return err
	}
	return v.validateGraphWorkspacesAndNotifications()
}

func (v sqliteValidation) validateGraphActivities() error {
	if err := v.each("graph_catalog_changes", nil, func(id string) error {
		var c GraphCatalogChange
		if err := v.get("graph_catalog_changes", id, &c); err != nil {
			return err
		}
		exists, err := v.exists("projects", c.ProjectID)
		if err != nil {
			return err
		}
		n, err := v.collectionCount("graph_catalog_changes", id, "writes")
		if err != nil {
			return err
		}
		if !exists || !validAuthoringID(c.OldID) || c.NewID != "" && !validAuthoringID(c.NewID) || n == 0 {
			return errors.New("invalid catalog outbox")
		}
		return v.each("graph_catalog_writes", &id, func(key string) error {
			var path string
			if err := v.tx.QueryRowContext(v.ctx, `SELECT map_key FROM data_graph_catalog_writes WHERE id=?`, key).Scan(&path); err != nil {
				return err
			}
			if path != graphFile(c.OldID) && path != layoutFile(c.OldID) && (c.NewID == "" || path != graphFile(c.NewID) && path != layoutFile(c.NewID)) {
				return errors.New("invalid graph catalog write path")
			}
			return nil
		})
	}); err != nil {
		return err
	}
	return v.each("graph_activities", nil, func(id string) error {
		var a GraphActivity
		if err := v.get("graph_activities", id, &a); err != nil {
			return err
		}
		s, err := v.session(a.ConversationID)
		if err != nil {
			return err
		}
		if s.ParentID != "" || s.GraphRunID != "" || s.ProjectID != a.ProjectID || !validAuthoringID(a.GraphID) || strings.TrimSpace(a.Objective) == "" || a.Version == 0 || !validGraphTime(a.CreatedAt) || !validGraphTime(a.UpdatedAt) || !slices.Contains([]string{"scheduled", "ready", "running", "awaiting_user", "awaiting_assessment", "succeeded", "abandoned"}, a.Status) {
			return errors.New("invalid graph activity")
		}
		n, err := v.collectionCount("graph_activities", id, "authorization")
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("missing graph authorization")
		}
		if err := v.each("graph_authorization", &id, func(key string) error {
			var ref GraphAuthorization
			if err := v.get("graph_authorization", key, &ref); err != nil {
				return err
			}
			human, err := v.userEvent(a.ConversationID, ref.EventID)
			if err != nil {
				return err
			}
			if !human || strings.TrimSpace(ref.Text) == "" {
				return errors.New("invalid graph authorization reference")
			}
			return nil
		}); err != nil {
			return err
		}
		if err := v.each("graph_assessments", &id, func(key string) error {
			var assessment GraphAssessment
			if err := v.get("graph_assessments", key, &assessment); err != nil {
				return err
			}
			r, err := v.run(assessment.RunID)
			if err != nil {
				return err
			}
			if r.ActivityID != id || graphRunActive(r.Status) || assessment.OperationID == "" || !validGraphTime(assessment.CreatedAt) || assessment.Satisfied && (r.Status == "failed" || r.Status == "blocked" || r.Status == "interrupted") {
				return errors.New("invalid graph assessment")
			}
			return nil
		}); err != nil {
			return err
		}
		if err := v.workspaceDecision(a.ProjectID, a.Workspace); err != nil {
			return err
		}
		if a.Workspace.SourceRunID != "" {
			r, err := v.run(a.Workspace.SourceRunID)
			if err != nil {
				return err
			}
			if r.ConversationID != a.ConversationID {
				return errors.New("graph retry source owner mismatch")
			}
		}
		return nil
	})
}

func (v sqliteValidation) validateGraphActivation(r GraphRun, c *CompiledGraph, id string) error {
	x, err := v.activation(id)
	if err != nil {
		return err
	}
	node, exists := r.Snapshot.Graph.Definition.Nodes[x.NodeID]
	if x.Occurrence == 0 || x.Input == nil || x.CausalID == "" || x.Violations < 0 || x.Violations > 3 || !validGraphTime(x.CreatedAt) || !validGraphTime(x.UpdatedAt) || !exists {
		return errors.New("invalid graph activation")
	}
	if graphActivationActive(x.Status) {
		if !graphRunActive(r.Status) || x.EndedAt != "" {
			return errors.New("active activation in ended run")
		}
	} else if !slices.Contains([]string{"completed", "failed", "interrupted"}, x.Status) || !validGraphTime(x.EndedAt) {
		return errors.New("invalid activation termination")
	}
	if x.WorkspaceID != "" {
		w, err := v.workspace(x.WorkspaceID)
		if err != nil {
			return err
		}
		if w.ProjectID != r.ProjectID {
			return errors.New("activation workspace project mismatch")
		}
	}
	if x.Status == "running" || x.Status == "waiting_user" || x.Status == "sealing" {
		w, err := v.workspace(x.WorkspaceID)
		if err != nil {
			return err
		}
		if w.Status != "ready" {
			return errors.New("activation workspace not ready")
		}
	}
	if x.SessionID != "" {
		s, err := v.session(x.SessionID)
		if err != nil {
			return err
		}
		w, err := v.workspace(x.WorkspaceID)
		if err != nil {
			return err
		}
		if s.GraphRunID != x.RunID || s.GraphNodeID != x.NodeID || node.Type != "agent" && node.Type != "join" || !sameGraphDirectory(w.Directory, s.ExecutionCWD) {
			return errors.New("invalid activation native session")
		}
	}
	n, err := v.collectionCount("graph_activations", id, "violationIds")
	if err != nil {
		return err
	}
	if n != int64(x.Violations) {
		return errors.New("activation violation count mismatch")
	}
	if x.ForkActivationID != "" {
		fork, err := v.activation(x.ForkActivationID)
		if err != nil {
			return err
		}
		f := r.Snapshot.Graph.Definition.Nodes[fork.NodeID]
		if fork.RunID != x.RunID || f.Type != "fork" {
			return errors.New("invalid fork provenance")
		}
		if _, ok := f.Branches[x.BranchID]; !ok {
			return errors.New("invalid branch provenance")
		}
	}
	if sub := x.Submission; sub != nil {
		raw := map[string]any{}
		for k, value := range sub.Payload {
			raw[k] = value
		}
		if _, _, err := c.validateChoice(x.NodeID, sub.Choice, raw); err != nil {
			return err
		}
		if sub.OperationID == "" || sub.TurnID == "" || !validGraphTime(sub.ReceivedAt) {
			return errors.New("invalid reserved Choice")
		}
		if sub.AcceptedAt != "" && (!validGraphTime(sub.AcceptedAt) || x.Status != "completed") {
			return errors.New("invalid Choice acceptance")
		}
		if sub.AcceptedAt == "" && x.Status != "sealing" && x.Status != "failed" && x.Status != "interrupted" {
			return errors.New("invalid Choice reservation state")
		}
	}
	if x.Status == "completed" && (node.Type == "agent" || node.Type == "join") && (x.Submission == nil || x.Submission.AcceptedAt == "") {
		return errors.New("agent completed without Choice")
	}
	return nil
}

func (v sqliteValidation) validateGraphDelivery(r GraphRun, c *CompiledGraph, id string) error {
	var x GraphDelivery
	if err := v.get("graph_deliveries", id, &x); err != nil {
		return err
	}
	producer, err := v.activation(x.ProducerActivationID)
	if err != nil {
		return err
	}
	if producer.RunID != x.RunID || producer.Status != "completed" || x.Payload == nil || x.CausalID == "" || !validGraphTime(x.CreatedAt) || !slices.Contains([]string{"pending", "consumed", "interrupted"}, x.Status) {
		return errors.New("invalid graph delivery")
	}
	edge, ok := c.Connections[x.ConnectionID]
	if !ok || edge.To != x.TargetNodeID || !slices.Contains(edge.Sources, producer.NodeID) {
		return errors.New("invalid graph delivery connection")
	}
	if edge.Kind == "choice" && (producer.Submission == nil || producer.Submission.AcceptedAt == "" || producer.Submission.Choice != (ChoiceIdentity{Origin: "graph", ID: edge.ChoiceID})) {
		return errors.New("delivery without accepted Choice")
	}
	if x.Status == "pending" && !graphRunActive(r.Status) {
		return errors.New("pending delivery in ended run")
	}
	if x.TargetActivationID != "" {
		target, err := v.activation(x.TargetActivationID)
		if err != nil {
			return err
		}
		if target.RunID != x.RunID || target.NodeID != x.TargetNodeID {
			return errors.New("invalid delivery target")
		}
	}
	for _, wid := range []string{x.WorkspaceID, x.OriginWorkspaceID} {
		w, err := v.workspace(wid)
		if err != nil {
			return err
		}
		if w.ProjectID != r.ProjectID {
			return errors.New("delivery workspace mismatch")
		}
	}
	return nil
}

func (v sqliteValidation) validateGraphJoin(r GraphRun, c *CompiledGraph, id string) error {
	var j JoinRound
	if err := v.get("graph_join_rounds", id, &j); err != nil {
		return err
	}
	if c.Definition.Nodes[j.NodeID].Type != "join" || j.Occurrence == 0 || j.CausalID == "" || !validGraphTime(j.CreatedAt) {
		return errors.New("invalid Join round")
	}
	if j.Status == "collecting" || j.Status == "running" {
		if !graphRunActive(r.Status) || j.EndedAt != "" {
			return errors.New("open Join in ended run")
		}
	} else if !slices.Contains([]string{"completed", "interrupted"}, j.Status) || !validGraphTime(j.EndedAt) {
		return errors.New("invalid Join status")
	}
	expected := slices.Clone(c.Incoming[j.NodeID])
	sort.Strings(expected)
	n, err := v.collectionCount("graph_join_rounds", id, "expected")
	if err != nil {
		return err
	}
	if n != int64(len(expected)) {
		return errors.New("Join expected inputs mismatch")
	}
	// The expected set is bounded by the one captured graph, not by run history.
	values := []string{}
	if err := v.each("graph_join_expected", &id, func(key string) error {
		var value string
		if err := v.get("graph_join_expected", key, &value); err != nil {
			return err
		}
		values = append(values, value)
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(values)
	if !slices.Equal(values, expected) {
		return errors.New("Join expected inputs mismatch")
	}
	if err := v.each("graph_join_arrivals", &id, func(key string) error {
		var a JoinArrival
		if err := v.get("graph_join_arrivals", key, &a); err != nil {
			return err
		}
		var d GraphDelivery
		if err := v.get("graph_deliveries", a.DeliveryID, &d); err != nil {
			return err
		}
		if d.ConnectionID != a.ConnectionID || d.RunID != j.RunID || d.TargetNodeID != j.NodeID || d.JoinRoundID != j.ID || d.CausalID != j.CausalID || !slices.Contains(expected, a.ConnectionID) {
			return errors.New("invalid Join arrival")
		}
		return nil
	}); err != nil {
		return err
	}
	if j.Status == "running" || j.Status == "completed" {
		arrivals, err := v.collectionCount("graph_join_rounds", id, "arrivals")
		if err != nil {
			return err
		}
		a, err := v.activation(j.ActivationID)
		if err != nil {
			return err
		}
		if arrivals != n || a.RunID != j.RunID || a.NodeID != j.NodeID || a.JoinRoundID != j.ID {
			return errors.New("Join activation before readiness")
		}
	}
	w, err := v.workspace(j.OriginWorkspaceID)
	if err != nil {
		return err
	}
	if w.ProjectID != r.ProjectID {
		return errors.New("Join origin project mismatch")
	}
	if j.IntegrationWorkspaceID != "" {
		w, err := v.workspace(j.IntegrationWorkspaceID)
		if err != nil {
			return err
		}
		if w.ProjectID != r.ProjectID {
			return errors.New("Join integration project mismatch")
		}
	}
	if !slices.Contains([]string{"", "new", "continue_target"}, j.SessionPolicy) {
		return errors.New("invalid Join session policy")
	}
	return nil
}

func (v sqliteValidation) validateGraphWorkspacesAndNotifications() error {
	if err := v.each("graph_workspaces", nil, func(id string) error {
		w, err := v.workspace(id)
		if err != nil {
			return err
		}
		r, err := v.run(w.CreatedByRunID)
		if err != nil {
			return err
		}
		if r.ProjectID != w.ProjectID || !filepath.IsAbs(w.Directory) || w.OperationID == "" || !validGraphTime(w.CreatedAt) || !slices.Contains([]string{"reserved", "ready", "failed", "interrupted"}, w.Status) {
			return errors.New("invalid workspace")
		}
		if w.CreatedByActivationID != "" {
			a, err := v.activation(w.CreatedByActivationID)
			if err != nil {
				return err
			}
			if a.RunID != w.CreatedByRunID {
				return errors.New("workspace creator mismatch")
			}
		}
		if w.Status == "reserved" && !graphRunActive(r.Status) {
			return errors.New("workspace intent in ended run")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := v.each("graph_workspace_uses", nil, func(id string) error {
		var use WorkspaceUse
		if err := v.get("graph_workspace_uses", id, &use); err != nil {
			return err
		}
		w, err := v.workspace(use.WorkspaceID)
		if err != nil {
			return err
		}
		r, err := v.run(use.RunID)
		if err != nil {
			return err
		}
		if w.ProjectID != r.ProjectID || use.Association == "" || !validGraphTime(use.CreatedAt) {
			return errors.New("invalid workspace use")
		}
		if use.ActivationID != "" {
			a, err := v.activation(use.ActivationID)
			if err != nil {
				return err
			}
			if a.RunID != r.ID {
				return errors.New("workspace activation mismatch")
			}
		}
		if use.Reused && w.CreatedByRunID == r.ID || !use.Reused && w.CreatedByRunID != r.ID {
			return errors.New("workspace reuse provenance mismatch")
		}
		return nil
	}); err != nil {
		return err
	}
	return v.each("graph_notifications", nil, func(id string) error {
		var n RunNotification
		if err := v.get("graph_notifications", id, &n); err != nil {
			return err
		}
		if !validGraphTime(n.CreatedAt) || !slices.Contains([]string{"pending", "delivering", "delivered"}, n.Status) {
			return errors.New("invalid graph notification")
		}
		if n.Kind == "activity_error" {
			var a GraphActivity
			if err := v.get("graph_activities", n.ActivityID, &a); err != nil {
				return err
			}
			if n.RunID != "" || a.ConversationID != n.ConversationID {
				return errors.New("invalid activity notification")
			}
		} else {
			r, err := v.run(n.RunID)
			if err != nil {
				return err
			}
			if n.Kind != "" && n.Kind != "run_result" || graphRunActive(r.Status) || r.ConversationID != n.ConversationID {
				return errors.New("invalid run notification")
			}
		}
		if n.RetryAfter != "" && !validGraphTime(n.RetryAfter) {
			return errors.New("invalid notification retry time")
		}
		if n.Status == "delivering" && n.TurnID == "" || n.Status == "delivered" && (!validGraphTime(n.DeliveredAt) || n.TurnID == "") {
			return errors.New("invalid notification delivery")
		}
		return nil
	})
}
