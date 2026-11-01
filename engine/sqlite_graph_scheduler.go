package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (st *sqliteSessionTx) firstReadyGraph(owner string) (string, error) {
	var allowed []string
	limited := false
	if run, err := st.activeAutomation(owner); err != nil {
		return "", err
	} else if run != nil {
		limited = true
		allowed = run.Snapshot.Graphs
	}
	query := `SELECT a.id FROM data_graph_activities a WHERE a.x_conversationId=? AND a.status IN ('ready','scheduled') AND NOT EXISTS(SELECT 1 FROM data_graph_dependencies d LEFT JOIN data_graph_activities p ON p.id=d.scalar_value WHERE d.owner_id=a.id AND (p.id IS NULL OR p.status<>'succeeded'))`
	args := []any{owner}
	if limited {
		if len(allowed) == 0 {
			return "", nil
		}
		marks := []string{}
		for _, id := range allowed {
			marks = append(marks, "?")
			args = append(args, id)
		}
		query += ` AND a.x_graphId IN (` + strings.Join(marks, ",") + `)`
	}
	query += ` ORDER BY (a.x_correction<>'') DESC,CAST(a.x_priority AS INTEGER) DESC,length(a.x_order),a.x_order,a.position LIMIT 1`
	var id string
	err := st.tx.QueryRowContext(st.ctx, query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
func (st *sqliteSessionTx) graphStoredAuthorization(activity GraphActivity) error {
	var count int
	if err := st.tx.QueryRowContext(st.ctx, `SELECT count(*) FROM data_graph_authorization WHERE owner_id=?`, activity.ID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return errors.New("Graph activity has no user authorization.")
	}
	return st.eachMatching("graph_authorization", `owner_id=?`, []any{activity.ID}, func(id string) error {
		var auth GraphAuthorization
		if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_authorization", id, 256<<10, &auth); err != nil {
			return err
		}
		return st.graphAuthorization(activity.ConversationID, []GraphAuthorization{auth})
	})
}
func (a *app) scheduleSQLGraphActivitiesLocked() error {
	if a.closing || a.storageErr != nil || a.harnessInstalling {
		return nil
	}
	if a.graphStarting == nil {
		a.graphStarting = map[string]bool{}
	}
	after := ""
	for {
		owners, err := sqliteQueryIDs(a.ctx, a.store.readers, `SELECT DISTINCT a.x_conversationId FROM data_graph_activities a JOIN data_projects p ON p.id=a.project_id WHERE a.status IN ('ready','scheduled') AND p.x_removed<>'true' AND a.x_conversationId>? AND NOT EXISTS(SELECT 1 FROM data_graph_runs r WHERE r.x_conversationId=a.x_conversationId AND r.status IN ('starting','running','ending')) ORDER BY a.x_conversationId LIMIT 100`, after)
		if err != nil {
			return err
		}
		if len(owners) == 0 {
			return nil
		}
		for _, owner := range owners {
			after = owner
			if a.graphStarting[owner] {
				continue
			}
			id := ""
			if err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
				st := &sqliteSessionTx{ctx: a.ctx, tx: tx}
				var err error
				id, err = st.firstReadyGraph(owner)
				return err
			}); err != nil {
				return err
			}
			if id == "" {
				continue
			}
			a.graphStarting[owner] = true
			a.wg.Add(1)
			go func(owner, id string) {
				defer a.wg.Done()
				defer func() { a.mu.Lock(); delete(a.graphStarting, owner); a.mu.Unlock() }()
				_ = a.startReservedSQLGraphActivity(a.ctx, owner, id)
			}(owner, id)
		}
	}
}
func (a *app) startReservedSQLGraphActivity(ctx context.Context, owner, id string) error {
	var activity GraphActivity
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		activity, err = st.graphActivity(owner, id)
		return err
	})
	if err != nil {
		return err
	}
	normalizeGraphWorkspaceDecision(&activity.Workspace)
	fail := func(cause error) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		_ = a.persistSQLGraphLocked(func(st *sqliteSessionTx) error {
			current, err := st.graphActivity(owner, id)
			if err != nil {
				return err
			}
			if current.Version != activity.Version || current.Status == "running" {
				return nil
			}
			if current.Version == ^uint64(0) {
				return errors.New("Activity version exhausted.")
			}
			current.Status, current.Error, current.UpdatedAt = "awaiting_user", cause.Error(), now()
			current.Version++
			if err := st.putRoot("graph_activities", id, current); err != nil {
				return err
			}
			n := RunNotification{ID: "graph-activity:" + id + ":" + fmt.Sprint(current.Version), Kind: "activity_error", ActivityID: id, ConversationID: owner, Status: "pending", Error: cause.Error(), CreatedAt: now()}
			return st.putRoot("graph_notifications", n.ID, n)
		})
		return cause
	}
	err = a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		if err := st.graphStoredAuthorization(activity); err != nil {
			return err
		}
		if err := st.graphAllowed(owner, activity.GraphID); err != nil {
			return err
		}
		latest, err := st.latestGraphRun(id)
		if err != nil {
			return err
		}
		return st.graphWorkspaceDecision(owner, activity.ProjectID, activity.Workspace, latest != nil)
	})
	if err != nil {
		return fail(err)
	}
	snapshot, compiled, err := a.captureGraphSnapshot(ctx, activity.ProjectID, activity.GraphID)
	if err != nil {
		return fail(err)
	}
	for _, agent := range compiled.Agents {
		if err := CheckGraphAdapter(agent.Harness); err != nil {
			return fail(err)
		}
		a.mu.Lock()
		_, installed := a.binaries[agent.Harness]
		a.mu.Unlock()
		if !installed {
			return fail(errors.New("Graph harness is not installed: " + agent.Harness))
		}
	}
	for _, node := range compiled.Definition.Nodes {
		if node.Type == "terminal" {
			if _, err := powerShellExecutable(); err != nil {
				return fail(err)
			}
			if err := CheckGraphAdapter("pi"); err != nil {
				return fail(err)
			}
		}
	}
	if err := validateGraphReuse(snapshot, activity.Workspace); err != nil {
		return fail(err)
	}
	if err := a.preflightSQLGraphReuse(ctx, activity); err != nil {
		return fail(err)
	}
	a.authoringMu.Lock()
	defer a.authoringMu.Unlock()
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	if err := a.revalidateGraphCapabilities(snapshot); err != nil {
		return fail(err)
	}
	project, err := a.store.readProjectMeta(ctx, activity.ProjectID)
	if err != nil {
		return fail(err)
	}
	if project.Removed {
		return fail(errors.New("Graph project is unavailable."))
	}
	for path, source := range snapshot.Sources {
		current, err := readConfig(project, path)
		if err != nil || revision(current) != source.Revision {
			return fail(errors.New("Graph definitions changed before execution could be reserved; retry with current definitions."))
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	run := GraphRun{ID: newID(), ActivityID: id, ProjectID: activity.ProjectID, ConversationID: owner, GraphID: activity.GraphID, Input: GraphRunInput{Task: activity.Task}, Workspace: activity.Workspace, Status: "starting", Revision: 1, CreatedAt: now(), UpdatedAt: now()}
	if run.Input.Task == "" {
		run.Input.Task = activity.Objective
	}
	err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		current, err := st.graphActivity(owner, id)
		if err != nil {
			return err
		}
		first, err := st.firstReadyGraph(owner)
		if err != nil {
			return err
		}
		if current.Version != activity.Version || first != id {
			return errors.New("Activity or scheduling priority changed during preparation.")
		}
		s, err := st.sessionMeta(owner)
		if err != nil {
			return err
		}
		if s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
			return errors.New("Graph runs require a main conversation.")
		}
		p, err := st.project(activity.ProjectID)
		if err != nil {
			return err
		}
		if p.Removed || p.Folder != project.Folder {
			return errors.New("Graph project changed.")
		}
		var active bool
		if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_runs WHERE x_conversationId=? AND status IN ('starting','running','ending'))`, owner).Scan(&active); err != nil {
			return err
		}
		if active {
			return errors.New("Conversation already has an active graph run.")
		}
		latest, err := st.latestGraphRun(id)
		if err != nil {
			return err
		}
		if err := st.graphWorkspaceDecision(owner, activity.ProjectID, activity.Workspace, latest != nil); err != nil {
			return err
		}
		if current.Version == ^uint64(0) {
			return errors.New("Activity version exhausted.")
		}
		current.Workspace, current.Status, current.UpdatedAt = activity.Workspace, "running", now()
		current.Version++
		if err := st.putRoot("graph_activities", id, current); err != nil {
			return err
		}
		if err := st.putRoot("graph_runs", run.ID, run); err != nil {
			return err
		}
		if err := st.putSingleChild("graph_snapshots", run.ID, snapshot); err != nil {
			return err
		}
		s.SelectedGraphID, s.UpdatedAt = run.GraphID, now()
		if err := st.putSessionMeta(s); err != nil {
			return err
		}
		if err := st.initializeAnalytics(); err != nil {
			return err
		}
		return st.putRecord("analytics_graphs", sqliteOwnedKey("settings", run.ID), "settings", run.ID, -1, AnalyticsGraphTiming{Last: run.CreatedAt})
	})
	if err != nil {
		return err
	}
	if a.analyticsGraphClocks == nil {
		a.analyticsGraphClocks = map[string]time.Time{}
	}
	a.analyticsGraphClocks[run.ID] = time.Now()
	runCtx, cancel := context.WithCancel(a.ctx)
	g := &graphExecution{ctx: runCtx, cancel: cancel, done: make(chan struct{}), completed: make(chan graphCompletion), wake: make(chan struct{}, 1)}
	if a.graphRuns == nil {
		a.graphRuns = map[string]*graphExecution{}
	}
	a.graphRuns[run.ID] = g
	a.wg.Add(1)
	go a.runGraph(run.ID, g)
	return nil
}
