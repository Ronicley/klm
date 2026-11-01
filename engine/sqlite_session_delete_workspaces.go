package main

import "database/sql"

func (st *sqliteSessionTx) protectSQLDeletionWorkspaces(p *sqliteSessionDeletionPlan) error {
	blocked := func() error {
		return controlError(409, "Another session references a workspace created by this session's graph.")
	}
	for run := range p.runs {
		if err := st.eachMatching("graph_workspaces", "x_createdByRunId=?", []any{run}, func(key string) error {
			var workspace WorkspaceRecord
			if err := decodeSQLiteRecord(st.ctx, st.tx, "graph_workspaces", key, 1<<20, &workspace); err != nil {
				return err
			}
			for _, ref := range []struct {
				table, where string
				args         []any
			}{
				{"graph_workspace_uses", "x_workspaceId=?", []any{key}},
				{"graph_activations", "x_workspaceId=? OR x_originWorkspaceId=?", []any{key, key}},
				{"graph_deliveries", "x_workspaceId=? OR x_originWorkspaceId=?", []any{key, key}},
				{"graph_join_rounds", "x_originWorkspaceId=? OR x_integrationWorkspaceId=?", []any{key, key}},
			} {
				if err := st.eachMatching(ref.table, ref.where, ref.args, func(id string) error {
					var owner string
					if err := st.tx.QueryRowContext(st.ctx, "SELECT x_runId FROM data_"+ref.table+" WHERE id=?", id).Scan(&owner); err != nil {
						return err
					}
					if !p.runs[owner] {
						return blocked()
					}
					return nil
				}); err != nil {
					return err
				}
			}
			if err := st.eachMatching("delegation_children", "x_workspaceId=?", []any{key}, func(id string) error {
				if _, owned := p.sessions[id]; !owned {
					return blocked()
				}
				return nil
			}); err != nil {
				return err
			}
			if err := st.eachMatching("delegation_invocations", "x_graph_workspaceId=?", []any{key}, func(id string) error {
				var owner string
				if err := st.tx.QueryRowContext(st.ctx, `SELECT x_parentSessionId FROM data_delegation_invocations WHERE id=?`, id).Scan(&owner); err != nil {
					return err
				}
				if _, owned := p.sessions[owner]; !owned {
					return blocked()
				}
				return nil
			}); err != nil {
				return err
			}
			// Direct execution in a worktree need not have a workspace-use row.
			return st.eachMatching("sessions", "x_executionCwd<>'' OR project_id<>''", nil, func(id string) error {
				if _, owned := p.sessions[id]; owned {
					return nil
				}
				var cwd string
				if err := st.tx.QueryRowContext(st.ctx, `SELECT coalesce(nullif(s.x_executionCwd,''),p.x_folder,'') FROM data_sessions s LEFT JOIN data_projects p ON p.id=s.project_id WHERE s.id=?`, id).Scan(&cwd); err != nil {
					return err
				}
				if workspace.Directory != "" && sameGraphDirectory(cwd, workspace.Directory) {
					return blocked()
				}
				return nil
			})
		}); err != nil {
			return err
		}
	}
	if len(p.runs) == 0 {
		return nil
	}
	// Reuse decisions are bounded metadata, not histories or global state.
	for _, table := range []string{"graph_activities", "graph_runs"} {
		if err := st.eachMatching(table, "1=1", nil, func(id string) error {
			var owner string
			var decision GraphWorkspaceDecision
			if table == "graph_activities" {
				var activity GraphActivity
				if err := decodeSQLiteRecord(st.ctx, st.tx, table, id, maxStreamRecordBytes, &activity); err != nil {
					return err
				}
				owner, decision = activity.ConversationID, activity.Workspace
			} else {
				var run GraphRun
				if err := decodeSQLiteRecord(st.ctx, st.tx, table, id, maxStreamRecordBytes, &run); err != nil {
					return err
				}
				owner, decision = run.ConversationID, run.Workspace
				if _, owned := p.sessions[owner]; !owned && run.InitialWorkspaceID != "" {
					var creator string
					if err := st.tx.QueryRowContext(st.ctx, `SELECT x_createdByRunId FROM data_graph_workspaces WHERE id=?`, run.InitialWorkspaceID).Scan(&creator); err != nil && err != sql.ErrNoRows {
						return err
					}
					if p.runs[creator] {
						return blocked()
					}
				}
			}
			if _, owned := p.sessions[owner]; owned {
				return nil
			}
			if p.runs[decision.SourceRunID] {
				return blocked()
			}
			for _, workspace := range decision.Reuse {
				var creator string
				if err := st.tx.QueryRowContext(st.ctx, `SELECT x_createdByRunId FROM data_graph_workspaces WHERE id=?`, workspace).Scan(&creator); err != nil && err != sql.ErrNoRows {
					return err
				}
				if p.runs[creator] {
					return blocked()
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
