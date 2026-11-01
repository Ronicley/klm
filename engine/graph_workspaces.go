package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type graphGitMetadata struct{ repository, branch, commit string }

func readGraphGit(ctx context.Context, cwd string, required bool) (graphGitMetadata, error) {
	var metadata graphGitMetadata
	repository, err := graphGit(ctx, cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		var uncertain *graphUnconfirmedError
		if !required && ctx.Err() == nil && !errors.As(err, &uncertain) {
			return metadata, nil
		}
		return metadata, err
	}
	metadata.repository, err = CanonicalGraphDirectory(repository)
	if err != nil {
		return metadata, err
	}
	metadata.commit, err = graphGit(ctx, cwd, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		var uncertain *graphUnconfirmedError
		if required || ctx.Err() != nil || errors.As(err, &uncertain) {
			return metadata, err
		}
	}
	metadata.branch, err = graphGit(ctx, cwd, "symbolic-ref", "--quiet", "--short", "HEAD") // Detached HEAD is valid.
	if err != nil {
		var uncertain *graphUnconfirmedError
		if ctx.Err() != nil || errors.As(err, &uncertain) {
			return metadata, err
		}
	}
	return metadata, nil
}
func graphWorkspaceAssociation(nodeID string, occurrence uint64, branch string) string {
	if nodeID == "" {
		return "initial"
	}
	key := fmt.Sprintf("node:%s:%d", nodeID, occurrence)
	if branch != "" {
		key += ":branch:" + branch
	}
	return key
}
func validateGraphReuse(snapshot GraphSnapshot, decision GraphWorkspaceDecision) error {
	if decision.Attempt != "reuse" {
		if len(decision.Reuse) > 0 {
			return errors.New("workspace.reuse: mappings require workspace.attempt=reuse.")
		}
		return nil
	}
	if decision.SourceRunID == "" || decision.Reuse["initial"] == "" {
		return errors.New("workspace.sourceRunId and workspace.reuse.initial: reuse requires a source run ID and an explicit initial workspace ID.")
	}
	for key := range decision.Reuse {
		if key == "initial" {
			continue
		}
		parts := strings.Split(key, ":")
		if len(parts) != 3 && len(parts) != 5 || parts[0] != "node" {
			return fmt.Errorf("workspace.reuse.%s: expected association key initial, node:<nodeId>:<occurrence> or node:<nodeId>:<occurrence>:branch:<branchId>.", key)
		}
		node, ok := snapshot.Graph.Definition.Nodes[parts[1]]
		occurrence, err := strconv.ParseUint(parts[2], 10, 64)
		if !ok || err != nil || occurrence == 0 {
			return fmt.Errorf("workspace.reuse.%s: use a node in the captured graph and a positive occurrence number.", key)
		}
		if len(parts) == 5 {
			if parts[3] != "branch" || node.Type != "fork" {
				return fmt.Errorf("workspace.reuse.%s: branch associations require a Fork node and the branch:<branchId> suffix.", key)
			}
			if _, ok := node.Branches[parts[4]]; !ok {
				return fmt.Errorf("workspace.reuse.%s: branch ID is not in the captured Fork definition.", key)
			}
		} else if node.Type != "join" {
			return fmt.Errorf("workspace.reuse.%s: only Join-created workspaces use node-only associations.", key)
		}
	}
	return nil
}

// Check every explicit association before reserving a run, including associations
// on paths that might not be visited. Never silently discard an edited-graph map.
func (a *app) preflightGraphReuse(ctx context.Context, activity GraphActivity) error {
	if a.store != nil {
		return a.preflightSQLGraphReuse(ctx, activity)
	}
	if activity.Workspace.Attempt != "reuse" {
		return nil
	}
	a.mu.Lock()
	source := a.state.graphRun(activity.Workspace.SourceRunID)
	if source == nil || graphRunActive(source.Status) || source.ConversationID != activity.ConversationID {
		a.mu.Unlock()
		return errors.New("workspace.sourceRunId: reuse requires a terminal run of this conversation.")
	}
	project := a.state.project(activity.ProjectID)
	if project == nil {
		a.mu.Unlock()
		return errors.New("Reuse project is unavailable.")
	}
	folder := project.Folder
	used := map[string]bool{}
	for _, use := range a.state.GraphWorkspaceUses {
		if use.RunID == source.ID {
			used[use.WorkspaceID] = true
		}
	}
	resources := map[string]WorkspaceRecord{}
	for key, id := range activity.Workspace.Reuse {
		w := a.state.graphWorkspace(id)
		if w == nil || !used[id] || w.Status != "ready" || w.ProjectID != activity.ProjectID {
			a.mu.Unlock()
			return fmt.Errorf("workspace.reuse.%s: must identify a ready workspace used by workspace.sourceRunId; read its workspace associations with graph_get_run.", key)
		}
		resources[id] = *w
	}
	a.mu.Unlock()
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

func (a *app) graphUseWorkspace(runID, activationID, key, workspaceID string) error {
	if a.store != nil {
		return a.sqlGraphUseWorkspace(runID, activationID, key, workspaceID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.graphChangeLocked(func(d *diskState) error {
		r, w := d.graphRun(runID), d.graphWorkspace(workspaceID)
		if r == nil || w == nil || !graphRunActive(r.Status) || r.Status == "ending" || w.Status != "ready" || w.ProjectID != r.ProjectID {
			return errors.New("Workspace use is no longer available.")
		}
		for _, use := range d.GraphWorkspaceUses {
			if use.RunID == runID && use.Association == key {
				if use.WorkspaceID == workspaceID {
					return nil
				}
				return errors.New("Workspace association was already consumed differently.")
			}
		}
		d.GraphWorkspaceUses = append(d.GraphWorkspaceUses, WorkspaceUse{ID: newID(), WorkspaceID: workspaceID, RunID: runID, ActivationID: activationID, Association: key, Reused: w.CreatedByRunID != runID, CreatedAt: now()})
		return nil
	})
}

func (a *app) reuseGraphWorkspace(ctx context.Context, run GraphRun, activationID, key, sourceCWD string) (string, error) {
	if a.store != nil {
		return a.reuseSQLGraphWorkspace(ctx, run, activationID, key, sourceCWD)
	}
	id := run.Workspace.Reuse[key]
	if id == "" {
		return "", nil
	}
	a.mu.Lock()
	w := a.state.graphWorkspace(id)
	var saved WorkspaceRecord
	if w != nil {
		saved = *w
	}
	source := a.state.graphRun(run.Workspace.SourceRunID)
	sourceOK := source != nil && source.ConversationID == run.ConversationID && !graphRunActive(source.Status)
	associated := false
	for _, use := range a.state.GraphWorkspaceUses {
		if use.RunID == run.Workspace.SourceRunID && use.WorkspaceID == id {
			associated = true
		}
	}
	a.mu.Unlock()
	if w == nil || saved.Status != "ready" || saved.ProjectID != run.ProjectID || !sourceOK || !associated {
		return "", errors.New("Reuse must identify a ready workspace explicitly used by the specified completed prior run.")
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
		return "", errors.New("The reused managed workspace has changed Git branch; resolve its provenance before reuse.")
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
	if err = a.graphUseWorkspace(run.ID, activationID, key, id); err != nil {
		return "", err
	}
	return id, nil
}

func (a *app) createGraphWorkspace(ctx context.Context, run GraphRun, activationID, branchID, key, sourceCWD, base, name string, isolated bool) (string, error) {
	if id, err := a.reuseGraphWorkspace(ctx, run, activationID, key, sourceCWD); id != "" || err != nil {
		return id, err
	}
	cwd, err := CanonicalGraphDirectory(sourceCWD)
	if err != nil {
		return "", err
	}
	metadata, err := readGraphGit(ctx, cwd, isolated)
	if err != nil {
		return "", err
	}
	if isolated {
		if base == "" {
			base = "HEAD"
		}
		base, err = graphGit(ctx, cwd, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
		if err != nil {
			return "", err
		}
	}
	w := WorkspaceRecord{ID: newID(), ProjectID: run.ProjectID, Directory: cwd, Repository: metadata.repository, GitBranch: metadata.branch, BaseCommit: metadata.commit, Owned: isolated, Status: "reserved", OperationID: newID(), CreatedByRunID: run.ID, CreatedByActivationID: activationID, BranchID: branchID, CreatedAt: now()}
	if isolated {
		if name == "" {
			name = "graph-work"
		}
		if !validGitBranch(name) {
			return "", errors.New("Invalid workspace branch name base.")
		}
		activity := activationID
		if activity == "" {
			activity = "initial"
		}
		w.GitBranch = name + "-r" + run.ID + "-a" + activity
		if branchID != "" {
			w.GitBranch += "-" + branchID
		}
		w.Directory = filepath.Join(a.dir, "worktrees", run.ProjectID, w.ID)
		w.BaseCommit = base
		if _, err := os.Lstat(w.Directory); !os.IsNotExist(err) {
			return "", errors.New("Workspace destination already exists or cannot be inspected.")
		}
		if err = os.MkdirAll(filepath.Dir(w.Directory), 0700); err != nil {
			return "", err
		}
	}
	a.mu.Lock()
	if a.store != nil {
		err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
			r, err := st.graphRunRecord(run.ID)
			if err != nil {
				return err
			}
			if !graphRunActive(r.Status) || r.Status == "ending" {
				return errors.New("Run is ending.")
			}
			return st.putRoot("graph_workspaces", w.ID, w)
		})
	} else {
		err = a.graphChangeLocked(func(d *diskState) error {
			r := d.graphRun(run.ID)
			if r == nil || r.Status == "ending" {
				return errors.New("Run is ending.")
			}
			d.GraphWorkspaces = append(d.GraphWorkspaces, w)
			return nil
		})
	}
	a.mu.Unlock()
	if err != nil {
		return "", err
	}
	if isolated {
		_, err = graphGit(ctx, cwd, "worktree", "add", "-b", w.GitBranch, w.Directory, base)
	}
	if err == nil {
		w.Directory, err = CanonicalGraphDirectory(w.Directory)
	}
	a.mu.Lock()
	var persistErr error
	if a.store != nil {
		persistErr = a.persistSQLGraphLocked(func(st *sqliteSessionTx) error {
			record, readErr := st.graphWorkspace(w.ID)
			if readErr != nil {
				return readErr
			}
			if err != nil {
				record.Status, record.Error = "failed", err.Error()
			} else {
				record.Status, record.Directory = "ready", w.Directory
			}
			return st.putRoot("graph_workspaces", record.ID, record)
		})
	} else {
		persistErr = a.graphChangeLocked(func(d *diskState) error {
			record := d.graphWorkspace(w.ID)
			if err != nil {
				record.Status, record.Error = "failed", err.Error()
			} else {
				record.Status, record.Directory = "ready", w.Directory
			}
			return nil
		})
	}
	a.mu.Unlock()
	if err != nil || persistErr != nil {
		return "", errors.Join(err, persistErr)
	}
	if err = a.graphUseWorkspace(run.ID, activationID, key, w.ID); err != nil {
		return "", err
	}
	return w.ID, nil
}

func (a *app) prepareInitialGraphWorkspace(g *graphExecution, run GraphRun) (string, error) {
	if a.store != nil {
		return a.prepareSQLInitialGraphWorkspace(g, run)
	}
	normalizeGraphWorkspaceDecision(&run.Workspace)
	a.mu.Lock()
	if err := validateGraphWorkspaceDecision(&a.state, run.ConversationID, run.Workspace, false); err != nil {
		a.mu.Unlock()
		return "", err
	}
	if run.InitialWorkspaceID != "" {
		w := a.state.graphWorkspace(run.InitialWorkspaceID)
		ready := w != nil && w.ProjectID == run.ProjectID && w.Status == "ready"
		a.mu.Unlock()
		if !ready {
			return "", errors.New("The run's existing initial workspace is unavailable.")
		}
		return run.InitialWorkspaceID, nil
	}
	p := a.state.project(run.ProjectID)
	folder := ""
	if p != nil {
		folder = p.Folder
	}
	a.mu.Unlock()
	if folder == "" {
		return "", errors.New("Graph project directory is unavailable.")
	}
	return a.createGraphWorkspace(g.ctx, run, "", "", "initial", folder, run.Workspace.BaseRevision, "graph-"+run.GraphID, run.Workspace.Mode == "new_worktree")
}

type graphBranchResult struct {
	connectionID, workspaceID, branchID string
	output                              map[string]string
}

func (a *app) executeGraphFork(g *graphExecution, run GraphRun, x GraphActivation) graphCompletion {
	result := graphCompletion{activationID: x.ID, drained: true}
	node := run.Snapshot.Graph.Definition.Nodes[x.NodeID]
	a.mu.Lock()
	var origin WorkspaceRecord
	if a.store != nil {
		var err error
		origin, err = a.readSQLGraphWorkspace(g.ctx, x.WorkspaceID)
		if err != nil {
			a.mu.Unlock()
			result.err = err
			return result
		}
	} else {
		workspace := a.state.graphWorkspace(x.WorkspaceID)
		if workspace != nil {
			origin = *workspace
		}
	}
	a.mu.Unlock()
	compiled, err := compileGraphSnapshot(run.Snapshot)
	if err != nil {
		result.err = err
		return result
	}
	base := ""
	for _, branch := range node.Branches {
		if branch.Isolated() {
			base, err = graphGit(g.ctx, origin.Directory, "rev-parse", "--verify", "HEAD^{commit}")
			break
		}
	}
	if err != nil {
		result.err = err
		return result
	}
	a.mu.Lock()
	if a.store != nil {
		err = a.writeSQLLocked(g.ctx, func(st *sqliteSessionTx) error {
			current, err := st.graphActivation(x.ID)
			if err != nil {
				return err
			}
			current.IncomingCommit = base
			return st.putRoot("graph_activations", current.ID, current)
		})
	} else {
		err = a.graphChangeLocked(func(d *diskState) error { d.graphActivation(x.ID).IncomingCommit = base; return nil })
	}
	a.mu.Unlock()
	if err != nil {
		result.err = err
		return result
	}
	for _, branchID := range sortedGraphKeys(node.Branches) {
		branch := node.Branches[branchID]
		key := "fork:" + x.NodeID + ":" + branchID
		payload, err := resolveGraphOutput(compiled.Outputs[key], GraphOutputContext{Task: run.Input.Task, Payload: x.Input})
		if err != nil {
			result.err = err
			return result
		}
		id := origin.ID
		if branch.Isolated() {
			id, err = a.createGraphWorkspace(g.ctx, run, x.ID, branchID, graphWorkspaceAssociation(x.NodeID, x.Occurrence, branchID), origin.Directory, base, branch.GitBranch, true)
		} else {
			association := graphWorkspaceAssociation(x.NodeID, x.Occurrence, branchID)
			if requested := run.Workspace.Reuse[association]; requested != "" && requested != id {
				result.err = errors.New("A shared Fork branch cannot switch away from its inherited workspace.")
				return result
			}
			err = a.graphUseWorkspace(run.ID, x.ID, association, id)
		}
		if err != nil {
			result.err = err
			return result
		}
		result.branches = append(result.branches, graphBranchResult{connectionID: key, workspaceID: id, branchID: branchID, output: payload})
	}
	return result
}

func (a *app) prepareGraphJoin(g *graphExecution, run GraphRun, x GraphActivation) (GraphActivation, any, error) {
	if a.store != nil {
		return a.prepareSQLGraphJoin(g, run, x)
	}
	a.mu.Lock()
	var round JoinRound
	for _, saved := range a.state.GraphJoinRounds {
		if saved.ID == x.JoinRoundID {
			round = saved
			break
		}
	}
	origin := a.state.graphWorkspace(round.OriginWorkspaceID)
	cwd := ""
	if origin != nil {
		cwd = origin.Directory
	}
	inputs := []map[string]any{}
	workspaces := []WorkspaceRecord{}
	for _, arrival := range round.Arrivals {
		for _, delivery := range a.state.GraphDeliveries {
			if delivery.ID == arrival.DeliveryID {
				inputs = append(inputs, map[string]any{"connectionId": delivery.ConnectionID, "payload": delivery.Payload})
				if w := a.state.graphWorkspace(delivery.WorkspaceID); w != nil {
					workspaces = append(workspaces, *w)
				}
			}
		}
	}
	a.mu.Unlock()
	if cwd == "" {
		return x, nil, errors.New("Join origin workspace is unavailable.")
	}
	workspaceID := round.OriginWorkspaceID
	node := run.Snapshot.Graph.Definition.Nodes[x.NodeID]
	if node.OutputBranch != "" {
		var err error
		workspaceID, err = a.createGraphWorkspace(g.ctx, run, x.ID, "", graphWorkspaceAssociation(x.NodeID, x.Occurrence, ""), cwd, "HEAD", node.OutputBranch, true)
		if err != nil {
			return x, nil, err
		}
	} else {
		association := graphWorkspaceAssociation(x.NodeID, x.Occurrence, "")
		if requested := run.Workspace.Reuse[association]; requested != "" && requested != workspaceID {
			return x, nil, errors.New("Join without output branch must return to its parallel origin.")
		}
		if err := a.graphUseWorkspace(run.ID, x.ID, association, workspaceID); err != nil {
			return x, nil, err
		}
	}
	for i := range workspaces {
		metadata, err := readGraphGit(g.ctx, workspaces[i].Directory, workspaces[i].Repository != "")
		if err != nil {
			return x, nil, err
		}
		workspaces[i].BaseCommit, workspaces[i].GitBranch = metadata.commit, metadata.branch
	}
	a.mu.Lock()
	err := a.graphChangeLocked(func(d *diskState) error {
		activation := d.graphActivation(x.ID)
		activation.WorkspaceID, activation.OriginWorkspaceID = workspaceID, workspaceID
		for i := range d.GraphJoinRounds {
			if d.GraphJoinRounds[i].ID == round.ID {
				d.GraphJoinRounds[i].IntegrationWorkspaceID = workspaceID
			}
		}
		return nil
	})
	if err == nil {
		x = *a.state.graphActivation(x.ID)
	}
	integration := a.state.graphWorkspace(workspaceID)
	a.mu.Unlock()
	return x, map[string]any{"inputs": inputs, "workspaces": workspaces, "integrationWorkspace": integration, "additionalPrompt": node.Prompt}, err
}
