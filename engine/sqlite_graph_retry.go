package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// The run itself is the durable receipt; no growing conversation-wide receipt scan.
func sqlGraphRetryID(owner, operation string) string {
	raw, _ := json.Marshal([]string{owner, operation})
	// Run IDs also become Windows artifact paths and Git branch suffixes.
	return revision(raw)[:32]
}

func (st *sqliteSessionTx) graphRetryReceipt(owner, source, activation, operation string) (*GraphRun, error) {
	r, err := st.graphRunRecord(sqlGraphRetryID(owner, operation))
	if errors.Is(err, sql.ErrNoRows) {
		// Imported pre-SQL retries have random IDs. The routing projection keeps
		// their original identity without scanning or expanding conversation history.
		var id string
		err = st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_graph_runs WHERE x_conversationId=? AND x_retryOperationId=? ORDER BY position LIMIT 1`, owner, operation).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err == nil {
			r, err = st.graphRunRecord(id)
		}
	}
	if err != nil {
		return nil, err
	}
	if r.ConversationID != owner || r.RetrySourceRunID != source || r.RetryActivationID != activation || r.RetryOperationID != operation {
		return nil, controlError(409, "Retry identity belongs to another activation.")
	}
	return &r, nil
}

func (st *sqliteSessionTx) graphRetryReason(r GraphRun, x GraphActivation) (string, error) {
	if x.RunID != r.ID {
		return "Node activation is unavailable.", nil
	}
	var active bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_runs WHERE x_conversationId=? AND status IN ('starting','running','ending'))`, r.ConversationID).Scan(&active); err != nil {
		return "", err
	}
	if active {
		return "Stop the active graph and wait for it to finish first.", nil
	}
	activity, err := st.graphActivity(r.ConversationID, r.ActivityID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (activity.Status == "abandoned" || activity.Status == "succeeded") {
		return "This activity is closed. Start a new activity.", nil
	}
	if err != nil {
		return "", err
	}
	latest, err := st.latestGraphRun(r.ActivityID)
	if err != nil {
		return "", err
	}
	if latest == nil || latest.ID != r.ID {
		return "Retry from the latest attempt of this activity.", nil
	}
	_, err = st.graphWorkspace(x.WorkspaceID)
	if x.Input == nil || errors.Is(err, sql.ErrNoRows) {
		return "This activation has no recorded input or workspace.", nil
	}
	return "", err
}

func (st *sqliteSessionTx) graphRetryOwner(owner, source, activation, node string) (GraphRun, GraphActivation, error) {
	s, err := st.graphHTTPMain(owner)
	if err != nil {
		return GraphRun{}, GraphActivation{}, err
	}
	r, err := st.graphRun(owner, source)
	if err != nil {
		return r, GraphActivation{}, err
	}
	x, err := st.graphActivation(activation)
	if err != nil {
		return r, x, err
	}
	if r.ProjectID != s.ProjectID || x.RunID != r.ID || x.NodeID != node {
		return r, x, sql.ErrNoRows
	}
	return r, x, nil
}

// Fingerprint only the checkpoint's metadata revisions, not the global engine
// revision: an independent chat may keep streaming while artifacts are checked.
func (st *sqliteSessionTx) graphRetryRevision(source GraphRun) (string, error) {
	hash := sha256.New()
	encode := json.NewEncoder(hash)
	for _, scope := range []struct {
		table, where string
		args         []any
	}{
		{"graph_runs", `id=?`, []any{source.ID}},
		{"graph_activities", `id=?`, []any{source.ActivityID}},
		{"graph_snapshots", `owner_id=?`, []any{source.ID}},
		{"graph_authorization", `owner_id=?`, []any{source.ActivityID}},
		{"graph_activations", `x_runId=?`, []any{source.ID}},
		{"graph_deliveries", `x_runId=?`, []any{source.ID}},
		{"graph_join_rounds", `x_runId=?`, []any{source.ID}},
		{"graph_workspace_uses", `x_runId=?`, []any{source.ID}},
		{"graph_join_expected", `owner_id IN (SELECT id FROM data_graph_join_rounds WHERE x_runId=?)`, []any{source.ID}},
		{"graph_join_arrivals", `owner_id IN (SELECT id FROM data_graph_join_rounds WHERE x_runId=?)`, []any{source.ID}},
		{"graph_workspaces", `id=? OR id IN (SELECT x_workspaceId FROM data_graph_workspace_uses WHERE x_runId=?) OR id IN (SELECT x_workspaceId FROM data_graph_activations WHERE x_runId=?) OR id IN (SELECT x_originWorkspaceId FROM data_graph_activations WHERE x_runId=?)`, []any{source.InitialWorkspaceID, source.ID, source.ID, source.ID}},
	} {
		if err := st.eachMatching(scope.table, scope.where, scope.args, func(id string) error {
			var digest, revision string
			if err := st.tx.QueryRowContext(st.ctx, `SELECT digest,revision FROM data_`+scope.table+` WHERE id=?`, id).Scan(&digest, &revision); err != nil {
				return err
			}
			return encode.Encode([]string{scope.table, id, digest, revision})
		}); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (st *sqliteSessionTx) graphRetryUnchanged(source GraphRun, expected string) error {
	current, err := st.graphRetryRevision(source)
	if err != nil {
		return err
	}
	if current != expected {
		return controlError(409, "Graph changed while preparing retry. Retry with current state.")
	}
	return nil
}

func (a *app) graphRetrySettling(st *sqliteSessionTx, owner string) (bool, error) {
	if a.closing || a.graphStarting[owner] || a.harnessInstalling {
		return true, nil
	}
	for id := range a.graphRuns {
		r, err := st.graphRunRecord(id)
		if err != nil {
			return false, err
		}
		if r.ConversationID == owner {
			return true, nil
		}
	}
	return false, nil
}

func (st *sqliteSessionTx) graphRetryWorkspaces(r GraphRun) (map[string]WorkspaceRecord, error) {
	workspaces := map[string]WorkspaceRecord{}
	add := func(id string) error {
		if _, exists := workspaces[id]; exists {
			return nil
		}
		w, err := st.graphWorkspace(id)
		if err != nil {
			return err
		}
		if w.ProjectID != r.ProjectID || w.Status != "ready" {
			return controlError(409, "Retry requires ready workspaces in the owning project.")
		}
		workspaces[id] = w
		return nil
	}
	if err := add(r.InitialWorkspaceID); err != nil {
		return nil, err
	}
	err := st.eachMatching("graph_workspace_uses", `x_runId=?`, []any{r.ID}, func(id string) error {
		var use WorkspaceUse
		if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_workspace_uses", id, 1<<20, &use); err != nil {
			return err
		}
		return add(use.WorkspaceID)
	})
	if err == nil {
		err = st.eachMatching("graph_activations", `x_runId=?`, []any{r.ID}, func(id string) error {
			x, err := st.graphActivation(id)
			if err != nil {
				return err
			}
			for _, workspace := range []string{x.WorkspaceID, x.OriginWorkspaceID} {
				if workspace != "" {
					if err := add(workspace); err != nil {
						return err
					}
				}
			}
			return nil
		})
	}
	return workspaces, err
}

func (a *app) retrySQLGraphNode(w http.ResponseWriter, req *http.Request) {
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
	owner, sourceID, node := req.PathValue("id"), req.PathValue("runId"), req.PathValue("nodeId")
	var source GraphRun
	var snapshot GraphSnapshot
	var project Project
	var workspaces map[string]WorkspaceRecord
	var sqlRevision string
	repeated := false
	a.mu.Lock()
	err := a.store.readSnapshot(req.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: req.Context(), tx: tx}
		var x GraphActivation
		var err error
		source, x, err = st.graphRetryOwner(owner, sourceID, body.ActivationID, node)
		if err != nil {
			return err
		}
		receipt, err := st.graphRetryReceipt(owner, sourceID, x.ID, body.OperationID)
		if err != nil {
			return err
		}
		if receipt != nil {
			repeated = true
			return nil
		}
		if reason, err := st.graphRetryReason(source, x); err != nil {
			return err
		} else if reason != "" {
			return controlError(409, reason)
		}
		if settling, err := a.graphRetrySettling(st, owner); err != nil {
			return err
		} else if settling {
			return controlError(409, "Graph is still settling. Retry shortly.")
		}
		activity, err := st.graphActivity(owner, source.ActivityID)
		if err != nil {
			return err
		}
		if err := st.graphStoredAuthorization(activity); err != nil {
			return controlError(409, err.Error())
		}
		if err := st.graphAllowed(owner, source.GraphID); err != nil {
			return controlError(409, err.Error())
		}
		if err := st.graphWorkspaceDecision(owner, source.ProjectID, source.Workspace, source.Workspace.Attempt == "reuse"); err != nil {
			return controlError(409, err.Error())
		}
		snapshot, _, err = st.capturedGraph(source.ID)
		if err != nil {
			return err
		}
		project, err = st.project(source.ProjectID)
		if err != nil {
			return err
		}
		workspaces, err = st.graphRetryWorkspaces(source)
		if err != nil {
			return err
		}
		sqlRevision, err = st.graphRetryRevision(source)
		return err
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	if repeated {
		a.getSQLConversationGraph(w, req)
		return
	}
	// Keep file/catalog mutations excluded through commit, but never hold app.mu for I/O.
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	if err = validateGraphSnapshot(snapshot); err == nil {
		err = a.revalidateGraphCapabilities(snapshot)
	}
	if err == nil {
		for path, captured := range snapshot.Sources {
			current, readErr := readConfig(project, path)
			if readErr != nil || revision(current) != captured.Revision {
				err = errors.New("Graph definitions changed; retry requires the captured sources.")
				break
			}
		}
	}
	if err == nil {
		for _, workspace := range workspaces {
			var cwd string
			cwd, err = CanonicalGraphDirectory(workspace.Directory)
			if err == nil {
				var metadata graphGitMetadata
				metadata, err = readGraphGit(req.Context(), cwd, workspace.Repository != "")
				if err == nil && (workspace.Repository != "" && !sameGraphDirectory(metadata.repository, workspace.Repository) || workspace.Owned && metadata.branch != workspace.GitBranch) {
					err = errors.New("Workspace repository or managed branch changed.")
				}
			}
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		fail(w, 409, "Cannot prepare graph retry: "+err.Error())
		return
	}
	a.mu.Lock()
	var next GraphRun
	err = a.writeSQLLocked(req.Context(), func(st *sqliteSessionTx) error {
		current, x, err := st.graphRetryOwner(owner, sourceID, body.ActivationID, node)
		if err != nil {
			return err
		}
		receipt, err := st.graphRetryReceipt(owner, sourceID, x.ID, body.OperationID)
		if err != nil {
			return err
		}
		if receipt != nil {
			repeated = true
			return nil
		}
		if err := st.graphRetryUnchanged(current, sqlRevision); err != nil {
			return err
		}
		settling, err := a.graphRetrySettling(st, owner)
		if err != nil {
			return err
		}
		if current.Revision != source.Revision || settling {
			return controlError(409, "Graph changed while preparing retry. Retry with current state.")
		}
		currentProject, err := st.project(source.ProjectID)
		if err != nil {
			return err
		}
		if currentProject.Removed || currentProject.Folder != project.Folder {
			return controlError(409, "Graph project changed while preparing retry.")
		}
		next, err = st.retryGraphCheckpoint(current, snapshot, x.ID, body.OperationID)
		return err
	})
	if err == nil && !repeated {
		if a.analyticsGraphClocks == nil {
			a.analyticsGraphClocks = map[string]time.Time{}
		}
		a.analyticsGraphClocks[next.ID] = time.Now()
		ctx, cancel := context.WithCancel(a.ctx)
		g := &graphExecution{ctx: ctx, cancel: cancel, done: make(chan struct{}), completed: make(chan graphCompletion), wake: make(chan struct{}, 1)}
		if a.graphRuns == nil {
			a.graphRuns = map[string]*graphExecution{}
		}
		a.graphRuns[next.ID] = g
		a.wg.Add(1)
		go a.runGraph(next.ID, g)
	}
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.getSQLConversationGraph(w, req)
}

// Clone causal records page by page. Only IDs/flags grow with the affected run;
// histories and other runs are never hydrated into diskState.
func (st *sqliteSessionTx) retryGraphCheckpoint(source GraphRun, snapshot GraphSnapshot, selected, operation string) (GraphRun, error) {
	x, err := st.graphActivation(selected)
	if err != nil {
		return GraphRun{}, err
	}
	current, _, err := st.graphRetryOwner(source.ConversationID, source.ID, selected, x.NodeID)
	if err != nil {
		return GraphRun{}, err
	}
	if reason, err := st.graphRetryReason(current, x); err != nil {
		return GraphRun{}, err
	} else if reason != "" {
		return GraphRun{}, controlError(409, reason)
	}
	authorized, err := st.graphActivity(current.ConversationID, current.ActivityID)
	if err != nil {
		return GraphRun{}, err
	}
	if err := st.graphStoredAuthorization(authorized); err != nil {
		return GraphRun{}, controlError(409, err.Error())
	}
	if err := st.graphAllowed(current.ConversationID, current.GraphID); err != nil {
		return GraphRun{}, controlError(409, err.Error())
	}
	source = current
	invalid := map[string]bool{selected: true}
	frontier := []string{selected}
	for i := 0; i < len(frontier); i++ {
		err := st.eachMatching("graph_deliveries", `x_runId=? AND x_producerActivationId=?`, []any{source.ID, frontier[i]}, func(id string) error {
			var d GraphDelivery
			if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_deliveries", id, 4<<20, &d); err != nil {
				return err
			}
			target := d.TargetActivationID
			if d.JoinRoundID != "" {
				if err := st.tx.QueryRowContext(st.ctx, `SELECT x_activationId FROM data_graph_join_rounds WHERE id=? AND x_runId=?`, d.JoinRoundID, source.ID).Scan(&target); err != nil {
					return err
				}
			}
			if target != "" && !invalid[target] {
				invalid[target] = true
				frontier = append(frontier, target)
			}
			return nil
		})
		if err != nil {
			return GraphRun{}, err
		}
	}
	run := source
	run.ID, run.Status, run.Revision = sqlGraphRetryID(source.ConversationID, operation), "running", 1
	run.Snapshot = GraphSnapshot{}
	run.RetrySourceRunID, run.RetryActivationID, run.RetryOperationID = source.ID, selected, operation
	run.StopRequested, run.PauseRequested, run.Paused = false, false, false
	run.Result, run.FinalityError, run.EndedAt = nil, "", ""
	run.CreatedAt, run.UpdatedAt = now(), now()
	run.Workspace.Attempt, run.Workspace.SourceRunID = "reuse", source.ID
	run.Workspace.Reuse = map[string]string{"initial": source.InitialWorkspaceID}
	ids := map[string]string{"run:" + source.ID: "run:" + run.ID}
	restart := map[string]bool{}
	if err := st.eachMatching("graph_activations", `x_runId=?`, []any{source.ID}, func(id string) error {
		x, err := st.graphActivation(id)
		if err != nil {
			return err
		}
		if !invalid[id] || id == selected {
			ids[id], restart[id] = newID(), id == selected || x.Status != "completed"
		}
		return nil
	}); err != nil {
		return run, err
	}
	if ids[selected] == "" {
		return run, sql.ErrNoRows
	}
	for _, table := range []string{"graph_join_rounds", "graph_deliveries"} {
		if err := st.eachMatching(table, `x_runId=?`, []any{source.ID}, func(id string) error {
			if table == "graph_join_rounds" {
				ids[id] = newID()
				return nil
			}
			var producer string
			if err := st.tx.QueryRowContext(st.ctx, `SELECT x_producerActivationId FROM data_graph_deliveries WHERE id=?`, id).Scan(&producer); err != nil {
				return err
			}
			if ids[producer] != "" && !restart[producer] {
				ids[id] = newID()
			}
			return nil
		}); err != nil {
			return run, err
		}
	}
	causal := func(id string) string {
		if next := ids[id]; next != "" {
			return next
		}
		return id
	}
	if err := st.eachMatching("graph_activations", `x_runId=?`, []any{source.ID}, func(id string) error {
		if ids[id] == "" {
			return nil
		}
		x, err := st.graphActivation(id)
		if err != nil {
			return err
		}
		x.ID, x.RunID, x.CausalID = ids[id], run.ID, causal(x.CausalID)
		x.ForkActivationID, x.JoinRoundID = ids[x.ForkActivationID], ids[x.JoinRoundID]
		x.SessionID, x.CurrentTurnID, x.FinishedTurnID = "", "", ""
		x.Continuable, x.SessionPolicy = false, "new"
		x.NativeEventStart, x.NativeEventEnd = 0, 0
		x.Events, x.ViolationIDs, x.ViolationFeedback = nil, nil, nil
		if restart[id] {
			x.ReusedFromActivationID = ""
			x.Status, x.Error, x.EndedAt, x.Correction, x.AdapterState = "reserved", "", "", "", ""
			x.Submission, x.Output, x.CommandExitCode = nil, nil, nil
			x.CommandResult, x.CommandTruncated, x.Violations = "", false, 0
			x.CreatedAt, x.UpdatedAt = now(), now()
		} else {
			x.ReusedFromActivationID = id
		}
		if err := st.putRoot("graph_activations", x.ID, x); err != nil {
			return err
		}
		if !restart[id] {
			if err := st.eachMatching("activation_violations", `owner_id=?`, []any{id}, func(key string) error {
				var violation string
				if err := decodeSQLiteRecord(st.ctx, st.tx, "activation_violations", key, 1<<20, &violation); err != nil {
					return err
				}
				return st.appendScalarChild("activation_violations", x.ID, violation)
			}); err != nil {
				return err
			}
			return st.eachMatching("activation_feedback", `owner_id=?`, []any{id}, func(key string) error {
				var feedback, operation string
				if err := decodeSQLiteRecord(st.ctx, st.tx, "activation_feedback", key, 1<<20, &feedback); err != nil {
					return err
				}
				if err := st.tx.QueryRowContext(st.ctx, `SELECT map_key FROM data_activation_feedback WHERE id=?`, key).Scan(&operation); err != nil {
					return err
				}
				return st.putRecord("activation_feedback", sqliteOwnedKey(x.ID, operation), x.ID, operation, -1, feedback)
			})
		}
		return nil
	}); err != nil {
		return run, err
	}
	keptRounds := map[string]bool{}
	if err := st.eachMatching("graph_join_rounds", `x_runId=?`, []any{source.ID}, func(id string) error {
		var round JoinRound
		if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_join_rounds", id, 4<<20, &round); err != nil {
			return err
		}
		if ids[round.ActivationID] == "" {
			return nil
		}
		restarting := restart[round.ActivationID]
		round.ID, round.RunID, round.CausalID, round.ActivationID = ids[id], run.ID, causal(round.CausalID), ids[round.ActivationID]
		if restarting {
			round.Status, round.EndedAt, round.SessionPolicy = "running", "", "new"
		}
		if err := st.putRoot("graph_join_rounds", round.ID, round); err != nil {
			return err
		}
		if err := st.eachMatching("graph_join_expected", `owner_id=?`, []any{id}, func(key string) error {
			var expected string
			if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_join_expected", key, 1<<20, &expected); err != nil {
				return err
			}
			return st.appendScalarChild("graph_join_expected", round.ID, expected)
		}); err != nil {
			return err
		}
		if err := st.eachMatching("graph_join_arrivals", `owner_id=?`, []any{id}, func(key string) error {
			var arrival JoinArrival
			if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_join_arrivals", key, 1<<20, &arrival); err != nil {
				return err
			}
			if ids[arrival.DeliveryID] == "" {
				return controlError(409, "Retry checkpoint is missing a Join input.")
			}
			arrival.DeliveryID = ids[arrival.DeliveryID]
			return st.appendScalarChild("graph_join_arrivals", round.ID, arrival)
		}); err != nil {
			return err
		}
		keptRounds[id] = true
		return nil
	}); err != nil {
		return run, err
	}
	if err := st.eachMatching("graph_deliveries", `x_runId=?`, []any{source.ID}, func(id string) error {
		if ids[id] == "" {
			return nil
		}
		var d GraphDelivery
		if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_deliveries", id, 4<<20, &d); err != nil {
			return err
		}
		d.ID, d.RunID, d.ProducerActivationID = ids[id], run.ID, ids[d.ProducerActivationID]
		d.TargetActivationID, d.ForkActivationID, d.CausalID = ids[d.TargetActivationID], ids[d.ForkActivationID], causal(d.CausalID)
		if keptRounds[d.JoinRoundID] {
			d.JoinRoundID = ids[d.JoinRoundID]
		} else {
			d.JoinRoundID = ""
		}
		d.Status = "consumed"
		if d.TargetActivationID == "" && d.JoinRoundID == "" {
			d.Status = "pending"
		}
		return st.putRoot("graph_deliveries", d.ID, d)
	}); err != nil {
		return run, err
	}
	if err := st.eachMatching("graph_workspace_uses", `x_runId=?`, []any{source.ID}, func(id string) error {
		var use WorkspaceUse
		if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_workspace_uses", id, 1<<20, &use); err != nil {
			return err
		}
		workspace, err := st.graphWorkspace(use.WorkspaceID)
		if err != nil {
			return err
		}
		if workspace.Status == "ready" {
			run.Workspace.Reuse[use.Association] = use.WorkspaceID
		}
		if use.ActivationID != "" && ids[use.ActivationID] == "" {
			return nil
		}
		use.ID, use.RunID, use.ActivationID, use.Reused, use.CreatedAt = newID(), run.ID, ids[use.ActivationID], true, now()
		return st.putRoot("graph_workspace_uses", use.ID, use)
	}); err != nil {
		return run, err
	}
	run.Workspace.Reuse["initial"] = source.InitialWorkspaceID
	if err := st.putRoot("graph_runs", run.ID, run); err != nil {
		return run, err
	}
	if err := st.putSingleChild("graph_snapshots", run.ID, snapshot); err != nil {
		return run, err
	}
	activity, err := st.graphActivity(run.ConversationID, run.ActivityID)
	if err != nil {
		return run, err
	}
	if activity.Version == ^uint64(0) {
		return run, errors.New("Activity version exhausted.")
	}
	activity.Status, activity.Workspace, activity.UpdatedAt = "running", run.Workspace, now()
	activity.Version++
	if err := st.putRoot("graph_activities", activity.ID, activity); err != nil {
		return run, err
	}
	s, err := st.sessionMeta(run.ConversationID)
	if err != nil {
		return run, err
	}
	s.SelectedGraphID, s.UpdatedAt = run.GraphID, now()
	if run.CatalogGraphID != nil {
		s.SelectedGraphID = *run.CatalogGraphID
	}
	if err := st.putSessionMeta(s); err != nil {
		return run, err
	}
	st.changes.Sessions[s.ID] = nil
	if err := st.initializeAnalytics(); err != nil {
		return run, err
	}
	if err := st.putRecord("analytics_graphs", sqliteOwnedKey("settings", run.ID), "settings", run.ID, -1, AnalyticsGraphTiming{Last: run.CreatedAt}); err != nil {
		return run, err
	}
	compiled, err := compileGraphSnapshot(snapshot)
	if err == nil {
		err = st.consumeGraphDeliveries(run, compiled)
	}
	return run, err
}
