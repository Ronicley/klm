package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (a *app) runSQLGraph(runID string, g *graphExecution) {
	defer a.wg.Done()
	defer func() {
		g.cancel()
		a.mu.Lock()
		delete(a.graphRuns, runID)
		delete(a.analyticsGraphClocks, runID)
		close(g.done)
		a.scheduleLinkedLocked()
		a.mu.Unlock()
	}()
	var run GraphRun
	var captured GraphSnapshot
	err := a.store.readSnapshot(g.ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: g.ctx, tx: tx}
		var err error
		run, err = st.graphRunRecord(runID)
		if err != nil {
			return err
		}
		captured, _, err = st.capturedGraph(runID)
		return err
	})
	if err != nil {
		a.mu.Lock()
		_ = a.endSQLGraphLocked(runID, GraphRunResult{Kind: "failed", Error: err.Error()}, "")
		_ = a.finishSQLGraphRunLocked(runID)
		a.mu.Unlock()
		return
	}
	run.Snapshot = captured
	workspace, err := a.prepareInitialGraphWorkspace(g, run)
	a.mu.Lock()
	if err == nil {
		err = a.writeSQLLocked(g.ctx, func(st *sqliteSessionTx) error {
			r, err := st.graphRunRecord(runID)
			if err != nil {
				return err
			}
			if r.Status != "starting" || r.Revision == ^uint64(0) {
				return errors.New("Graph startup interrupted.")
			}
			r.InitialWorkspaceID, r.Status, r.UpdatedAt = workspace, "running", now()
			r.Revision++
			if err := st.putRoot("graph_runs", runID, r); err != nil {
				return err
			}
			_, err = st.nextGraphActivation(r, captured.Graph.Definition.InitialNode, map[string]string{"task": r.Input.Task}, workspace, workspace, "run:"+r.ID, "new", "", "", "")
			return err
		})
	}
	if err != nil {
		kind := "failed"
		if a.ctx.Err() != nil {
			kind = "interrupted"
		}
		uncertain := ""
		var unconfirmed *graphUnconfirmedError
		if errors.As(err, &unconfirmed) {
			uncertain = err.Error()
		}
		_ = a.endSQLGraphLocked(runID, GraphRunResult{Kind: kind, Error: err.Error()}, uncertain)
	}
	a.mu.Unlock()
	workers := map[string]bool{}
	for {
		a.mu.Lock()
		var current GraphRun
		err = a.store.readSnapshot(context.Background(), func(tx *sql.Tx) error {
			st := &sqliteSessionTx{ctx: context.Background(), tx: tx}
			var err error
			current, err = st.graphRunRecord(runID)
			return err
		})
		if err != nil || a.storageErr != nil {
			a.mu.Unlock()
			g.cancel()
			for len(workers) > 0 {
				result := <-g.completed
				delete(workers, result.activationID)
			}
			return
		}
		if a.ctx.Err() != nil && current.Status != "ending" {
			_ = a.endSQLGraphLocked(runID, GraphRunResult{Kind: "interrupted", Error: "Engine shutdown interrupted graph execution."}, "")
			current.Status = "ending"
		}
		if current.Status == "ending" && len(workers) == 0 {
			err = a.finishSQLGraphRunLocked(runID)
			if err != nil {
				a.failStorageLocked(err)
			}
			a.mu.Unlock()
			return
		}
		launch := []GraphActivation{}
		if current.Status == "running" {
			err = a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
				st := &sqliteSessionTx{ctx: a.ctx, tx: tx}
				ids, err := sqliteQueryIDs(a.ctx, tx, `SELECT id FROM data_graph_activations WHERE x_runId=? AND status='reserved' ORDER BY position,id LIMIT 100`, runID)
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
				_ = a.endSQLGraphLocked(runID, GraphRunResult{Kind: "failed", Error: err.Error()}, "")
				a.mu.Unlock()
				continue
			}
			for _, x := range launch {
				workers[x.ID] = true
			}
			if len(workers) == 0 {
				_ = a.endSQLGraphLocked(runID, GraphRunResult{Kind: "failed", Error: "Graph has no runnable producer or terminal outcome; inspect collecting Join rounds for missing dependencies."}, "")
				a.mu.Unlock()
				continue
			}
		}
		current.Snapshot = captured
		a.mu.Unlock()
		for _, x := range launch {
			go a.launchGraphActivation(g, current, x)
		}
		select {
		case result := <-g.completed:
			delete(workers, result.activationID)
			a.mu.Lock()
			a.completeSQLGraphActivationLocked(runID, result)
			a.mu.Unlock()
		case <-g.wake:
		case <-a.ctx.Done():
			if len(workers) > 0 {
				result := <-g.completed
				delete(workers, result.activationID)
				a.mu.Lock()
				a.completeSQLGraphActivationLocked(runID, result)
				a.mu.Unlock()
			}
		}
	}
}

func (a *app) launchSQLGraphAgent(g *graphExecution, run GraphRun, x GraphActivation, send func(graphCompletion)) {
	failure := func(err error) { send(graphCompletion{activationID: x.ID, drained: true, err: err}) }
	compiled, err := compileGraphSnapshot(run.Snapshot)
	if err != nil {
		failure(err)
		return
	}
	agent := compiled.Agents[x.NodeID]
	if err := CheckGraphAdapter(agent.Harness); err != nil {
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
		extra, err := graphPrompt("join-node.md", joinData)
		if err != nil {
			failure(err)
			return
		}
		prompt += "\n" + extra
	}
	w, err := a.readSQLGraphWorkspace(g.ctx, x.WorkspaceID)
	if err != nil {
		failure(err)
		return
	}
	cwd, err := CanonicalGraphDirectory(w.Directory)
	if err != nil {
		failure(err)
		return
	}
	ctx, cancel := context.WithCancel(g.ctx)
	t := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	turnID := newID()
	if run.Snapshot.Version == 2 {
		t.graphDelegation = &graphDelegationBinding{Snapshot: run.Snapshot, OwnerID: run.ConversationID, Scope: DelegationGraphScope{RunID: run.ID, NodeID: x.NodeID, ActivationID: x.ID, TurnID: turnID, WorkspaceID: x.WorkspaceID, SnapshotDigest: run.Snapshot.Revision}}
	}
	hooks := GraphAdapterHooks{Node: true, ReserveChoice: func(ctx context.Context, callID string, raw json.RawMessage) (any, error) {
		return a.reserveSQLGraphChoice(ctx, run.ID, x.ID, turnID, callID, raw)
	}, Transition: func(ctx context.Context, stage string) error {
		return a.transitionSQLGraphChoice(ctx, run.ID, x.ID, stage)
	}, Finished: func(result GraphAdapterResult) {
		send(graphCompletion{activationID: x.ID, turnID: turnID, drained: result.ProcessesDrained, native: &result, err: result.Error})
	}}
	if err := BindGraphAdapter(t, hooks); err != nil {
		cancel()
		failure(err)
		return
	}
	a.mu.Lock()
	b, installed := a.binaries[agent.Harness]
	if !installed {
		a.mu.Unlock()
		UnbindGraphAdapter(t)
		cancel()
		failure(errors.New("Graph harness executable is unavailable."))
		return
	}
	var s Session
	var native nativeSession
	err = a.writeSQLLocked(g.ctx, func(st *sqliteSessionTx) error {
		r, err := st.graphRunRecord(run.ID)
		if err != nil {
			return err
		}
		if r.Status != "running" {
			return errors.New("Graph activation was cancelled before dispatch.")
		}
		current, err := st.graphActivation(x.ID)
		if err != nil {
			return err
		}
		if current.Status != "reserved" || current.RunID != run.ID {
			return errors.New("Activation is no longer reserved.")
		}
		id := ""
		if current.Correction != "" {
			if !current.Continuable {
				return errors.New("The native session cannot safely continue result correction.")
			}
			id = current.SessionID
		} else if current.SessionPolicy == "continue_target" {
			var previous string
			err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_graph_activations WHERE x_runId=? AND x_nodeId=? AND id<>? AND session_id<>'' ORDER BY length(x_occurrence) DESC,x_occurrence DESC LIMIT 1`, run.ID, x.NodeID, x.ID).Scan(&previous)
			if err == nil {
				old, err := st.graphActivation(previous)
				if err != nil {
					return err
				}
				if old.Continuable {
					candidate, err := st.sessionMeta(old.SessionID)
					if err != nil {
						return err
					}
					if candidate.Harness == agent.Harness && sameGraphDirectory(candidate.ExecutionCWD, cwd) {
						id = candidate.ID
					}
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if id != "" && a.runs[id] != nil {
			return errors.New("Target native session already has an active turn.")
		}
		if id == "" {
			id = newID()
			s = Session{ID: id, ProjectID: run.ProjectID, Title: compiled.Definition.Nodes[x.NodeID].Name, Workspace: "Ungrouped", Role: "graph_node", GraphRunID: run.ID, GraphNodeID: x.NodeID, ExecutionCWD: cwd, Harness: agent.Harness, Model: agent.Model, Effort: agent.Effort, YOLO: true, Status: "idle", CreatedAt: now(), UpdatedAt: now()}
			if err := st.createSession(s, a.dir); err != nil {
				return err
			}
		}
		s, err = st.sessionMeta(id)
		if err != nil {
			return err
		}
		if captured := run.Snapshot.Capabilities[x.NodeID]; captured != nil {
			if err := st.putSingleChild("capability_selections", sqliteOwnedKey(id, ""), copySelection(captured.Selection)); err != nil {
				return err
			}
		}
		s.Status, s.UpdatedAt = "running", now()
		if err := st.putSessionMeta(s); err != nil {
			return err
		}
		for _, table := range []string{"pending_permissions", "pending_questions"} {
			if err := st.clearChildren(table, id); err != nil {
				return err
			}
		}
		current.SessionID, current.Status, current.AdapterState, current.UpdatedAt = id, "running", "open", now()
		if current.CurrentTurnID == "" {
			if err := st.tx.QueryRowContext(st.ctx, `SELECT size FROM record_collections WHERE parent_table='sessions' AND parent_id=? AND field='events'`, id).Scan(&current.NativeEventStart); err != nil {
				return err
			}
		}
		current.CurrentTurnID = turnID
		if err := st.putRoot("graph_activations", current.ID, current); err != nil {
			return err
		}
		e := event("status", "Node activation started.")
		e.Data = map[string]any{"activationId": current.ID, "turnId": turnID}
		if err := st.appendScalarChild("activation_events", current.ID, e); err != nil {
			return err
		}
		native, err = st.native(id)
		return err
	})
	if err != nil {
		a.mu.Unlock()
		UnbindGraphAdapter(t)
		cancel()
		failure(err)
		return
	}
	a.runs[s.ID] = t
	a.wg.Add(1)
	a.mu.Unlock()
	go a.execute(t, s, native, b, cwd, submission{Text: prompt})
}
