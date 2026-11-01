package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (st *sqliteSessionTx) graphWorkspace(id string) (WorkspaceRecord, error) {
	var w WorkspaceRecord
	err := decodeSQLiteRecord(st.ctx, st.tx, "graph_workspaces", id, 1<<20, &w)
	return w, err
}
func (a *app) readSQLGraphWorkspace(ctx context.Context, id string) (WorkspaceRecord, error) {
	var w WorkspaceRecord
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		w, err = st.graphWorkspace(id)
		return err
	})
	return w, err
}
func (st *sqliteSessionTx) graphReuseSource(owner, source, workspace, project string) (WorkspaceRecord, error) {
	r, err := st.graphRun(owner, source)
	if err != nil {
		return WorkspaceRecord{}, err
	}
	if graphRunActive(r.Status) {
		return WorkspaceRecord{}, errors.New("Reuse requires a terminal source run of this conversation.")
	}
	w, err := st.graphWorkspace(workspace)
	if err != nil {
		return w, err
	}
	var associated bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_workspace_uses WHERE x_runId=? AND x_workspaceId=?)`, source, workspace).Scan(&associated); err != nil {
		return w, err
	}
	if !associated || w.Status != "ready" || w.ProjectID != project {
		return w, errors.New("Reuse requires a ready workspace explicitly used by the selected source run.")
	}
	return w, nil
}
func (a *app) preflightSQLGraphReuse(ctx context.Context, activity GraphActivity) error {
	if activity.Workspace.Attempt != "reuse" {
		return nil
	}
	folder := ""
	resources := map[string]WorkspaceRecord{}
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		project, err := st.project(activity.ProjectID)
		if err != nil {
			return err
		}
		if project.Removed {
			return errors.New("Reuse project is unavailable.")
		}
		folder = project.Folder
		for key, id := range activity.Workspace.Reuse {
			w, err := st.graphReuseSource(activity.ConversationID, activity.Workspace.SourceRunID, id, activity.ProjectID)
			if err != nil {
				return fmt.Errorf("workspace.reuse.%s: %w", key, err)
			}
			resources[id] = w
		}
		return nil
	})
	if err != nil {
		return err
	}
	origin, err := readGraphGit(ctx, folder, false)
	if err != nil {
		return err
	}
	for _, w := range resources {
		cwd, err := CanonicalGraphDirectory(w.Directory)
		if err != nil {
			return err
		}
		metadata, err := readGraphGit(ctx, cwd, w.Repository != "")
		if err != nil {
			return err
		}
		if metadata.repository != origin.repository || w.Repository != "" && !sameGraphDirectory(metadata.repository, w.Repository) || w.Owned && metadata.branch != w.GitBranch {
			return errors.New("A reused workspace's repository or managed branch no longer matches its explicit provenance.")
		}
	}
	return nil
}
func (a *app) sqlGraphUseWorkspace(runID, activationID, key, workspaceID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
		r, err := st.graphRunRecord(runID)
		if err != nil {
			return err
		}
		w, err := st.graphWorkspace(workspaceID)
		if err != nil {
			return err
		}
		if !graphRunActive(r.Status) || r.Status == "ending" || w.Status != "ready" || w.ProjectID != r.ProjectID {
			return errors.New("Workspace use is no longer available.")
		}
		var existing string
		err = st.tx.QueryRowContext(st.ctx, `SELECT x_workspaceId FROM data_graph_workspace_uses WHERE x_runId=? AND x_association=? LIMIT 1`, runID, key).Scan(&existing)
		if err == nil {
			if existing == workspaceID {
				return nil
			}
			return errors.New("Workspace association was already consumed differently.")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if r.Workspace.Attempt == "reuse" && r.Workspace.Reuse[key] == workspaceID {
			if _, err := st.graphReuseSource(r.ConversationID, r.Workspace.SourceRunID, workspaceID, r.ProjectID); err != nil {
				return err
			}
		}
		use := WorkspaceUse{ID: newID(), WorkspaceID: workspaceID, RunID: runID, ActivationID: activationID, Association: key, Reused: w.CreatedByRunID != runID, CreatedAt: now()}
		return st.putRoot("graph_workspace_uses", use.ID, use)
	})
}
func (a *app) reuseSQLGraphWorkspace(ctx context.Context, run GraphRun, activationID, key, sourceCWD string) (string, error) {
	id := run.Workspace.Reuse[key]
	if id == "" {
		return "", nil
	}
	var saved WorkspaceRecord
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		saved, err = st.graphReuseSource(run.ConversationID, run.Workspace.SourceRunID, id, run.ProjectID)
		return err
	})
	if err != nil {
		return "", err
	}
	cwd, err := CanonicalGraphDirectory(saved.Directory)
	if err != nil {
		return "", err
	}
	metadata, err := readGraphGit(ctx, cwd, saved.Repository != "")
	if err != nil {
		return "", err
	}
	if saved.Repository != "" && !sameGraphDirectory(metadata.repository, saved.Repository) {
		return "", errors.New("Reused workspace belongs to a different repository.")
	}
	if saved.Owned && metadata.branch != saved.GitBranch {
		return "", errors.New("The reused managed workspace has changed Git branch.")
	}
	if sourceCWD != "" {
		origin, err := readGraphGit(ctx, sourceCWD, saved.Repository != "")
		if err != nil {
			return "", err
		}
		if metadata.repository != origin.repository {
			return "", errors.New("Reused workspace is incompatible with the current project repository.")
		}
	}
	if err := a.sqlGraphUseWorkspace(run.ID, activationID, key, id); err != nil {
		return "", err
	}
	return id, nil
}
func (a *app) prepareSQLInitialGraphWorkspace(g *graphExecution, run GraphRun) (string, error) {
	normalizeGraphWorkspaceDecision(&run.Workspace)
	folder := ""
	err := a.store.readSnapshot(g.ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: g.ctx, tx: tx}
		if err := st.graphWorkspaceDecision(run.ConversationID, run.ProjectID, run.Workspace, false); err != nil {
			return err
		}
		if run.InitialWorkspaceID != "" {
			w, err := st.graphWorkspace(run.InitialWorkspaceID)
			if err != nil {
				return err
			}
			if w.ProjectID != run.ProjectID || w.Status != "ready" {
				return errors.New("The run's existing initial workspace is unavailable.")
			}
			return nil
		}
		p, err := st.project(run.ProjectID)
		if err != nil {
			return err
		}
		if p.Removed {
			return errors.New("Graph project directory is unavailable.")
		}
		folder = p.Folder
		return nil
	})
	if err != nil {
		return "", err
	}
	if run.InitialWorkspaceID != "" {
		return run.InitialWorkspaceID, nil
	}
	return a.createGraphWorkspace(g.ctx, run, "", "", "initial", folder, run.Workspace.BaseRevision, "graph-"+run.GraphID, run.Workspace.Mode == "new_worktree")
}
func (a *app) prepareSQLGraphJoin(g *graphExecution, run GraphRun, x GraphActivation) (GraphActivation, any, error) {
	c, err := compileGraphSnapshot(run.Snapshot)
	if err != nil {
		return x, nil, err
	}
	var round JoinRound
	var origin WorkspaceRecord
	inputs := []map[string]any{}
	workspaces := []WorkspaceRecord{}
	err = a.store.readSnapshot(g.ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: g.ctx, tx: tx}
		if err := decodeSQLiteRecord(g.ctx, tx, "graph_join_rounds", x.JoinRoundID, 4<<20, &round); err != nil {
			return err
		}
		if round.RunID != run.ID || round.ActivationID != x.ID {
			return errors.New("Join activation does not own this round.")
		}
		var err error
		origin, err = st.graphWorkspace(round.OriginWorkspaceID)
		if err != nil {
			return err
		}
		arrivals, more, err := readSQLChildren[JoinArrival](g.ctx, tx, "graph_join_arrivals", round.ID, len(c.Incoming[x.NodeID])+1, 4<<20)
		if err != nil {
			return err
		}
		if more || len(arrivals) != len(c.Incoming[x.NodeID]) {
			return errors.New("Join arrivals do not match the captured frontier.")
		}
		for _, arrival := range arrivals {
			var d GraphDelivery
			if err := decodeSQLiteRecord(g.ctx, tx, "graph_deliveries", arrival.DeliveryID, 4<<20, &d); err != nil {
				return err
			}
			if d.RunID != run.ID || d.JoinRoundID != round.ID {
				return errors.New("Join arrival belongs to another causal round.")
			}
			inputs = append(inputs, map[string]any{"connectionId": d.ConnectionID, "payload": d.Payload})
			w, err := st.graphWorkspace(d.WorkspaceID)
			if err != nil {
				return err
			}
			workspaces = append(workspaces, w)
		}
		return nil
	})
	if err != nil {
		return x, nil, err
	}
	workspace := round.OriginWorkspaceID
	node := run.Snapshot.Graph.Definition.Nodes[x.NodeID]
	if node.OutputBranch != "" {
		workspace, err = a.createGraphWorkspace(g.ctx, run, x.ID, "", graphWorkspaceAssociation(x.NodeID, x.Occurrence, ""), origin.Directory, "HEAD", node.OutputBranch, true)
	} else {
		association := graphWorkspaceAssociation(x.NodeID, x.Occurrence, "")
		if requested := run.Workspace.Reuse[association]; requested != "" && requested != workspace {
			return x, nil, errors.New("Join without output branch must return to its parallel origin.")
		}
		err = a.sqlGraphUseWorkspace(run.ID, x.ID, association, workspace)
	}
	if err != nil {
		return x, nil, err
	}
	for i := range workspaces {
		metadata, err := readGraphGit(g.ctx, workspaces[i].Directory, workspaces[i].Repository != "")
		if err != nil {
			return x, nil, err
		}
		workspaces[i].BaseCommit, workspaces[i].GitBranch = metadata.commit, metadata.branch
	}
	var integration WorkspaceRecord
	a.mu.Lock()
	err = a.writeSQLLocked(g.ctx, func(st *sqliteSessionTx) error {
		r, err := st.graphRunRecord(run.ID)
		if err != nil {
			return err
		}
		if r.Status != "running" {
			return errors.New("Run is ending.")
		}
		x, err = st.graphActivation(x.ID)
		if err != nil {
			return err
		}
		x.WorkspaceID, x.OriginWorkspaceID = workspace, workspace
		if err := st.putRoot("graph_activations", x.ID, x); err != nil {
			return err
		}
		if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_join_rounds", round.ID, 4<<20, &round); err != nil {
			return err
		}
		round.IntegrationWorkspaceID = workspace
		if err := st.putRoot("graph_join_rounds", round.ID, round); err != nil {
			return err
		}
		integration, err = st.graphWorkspace(workspace)
		return err
	})
	a.mu.Unlock()
	return x, map[string]any{"inputs": inputs, "workspaces": workspaces, "integrationWorkspace": integration, "additionalPrompt": node.Prompt}, err
}
