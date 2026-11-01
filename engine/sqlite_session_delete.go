package main

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
)

// This inventory contains identities and bounded metadata only. Histories and
// native content never enter diskState, including during cleanup recovery.
type sqliteSessionDeletionPlan struct {
	root     string
	sessions map[string]string
	runs     map[string]bool
	records  map[string]map[string]bool
	images   map[string][]string
}

func (p *sqliteSessionDeletionPlan) add(table, id string) {
	if p.records[table] == nil {
		p.records[table] = map[string]bool{}
	}
	p.records[table][id] = true
}

func (st *sqliteSessionTx) sessionDeleted(id string) (bool, error) {
	var exists bool
	err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_deleted_sessions WHERE id=?)`, id).Scan(&exists)
	return exists, err
}

func (st *sqliteSessionTx) operationDeleted(kind, from, operation string) (bool, error) {
	var exists bool
	err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_deleted_operations WHERE id=?)`, kind+"/"+from+"/"+operation).Scan(&exists)
	return exists, err
}

func (st *sqliteSessionTx) sessionDeletionPlan(root string) (*sqliteSessionDeletionPlan, error) {
	s, err := st.sessionMeta(root)
	if err != nil {
		return nil, err
	}
	if s.Role != "" || s.ParentID != "" || s.GraphRunID != "" || s.ProjectID == "" {
		return nil, controlError(400, "Only top-level project sessions can be deleted.")
	}
	p := &sqliteSessionDeletionPlan{root: root, sessions: map[string]string{root: s.ProjectID}, runs: map[string]bool{}, records: map[string]map[string]bool{}, images: map[string][]string{}}
	pending := []string{root}
	for len(pending) > 0 {
		id := pending[0]
		pending = pending[1:]
		if err := st.eachMatching("graph_runs", `x_conversationId=?`, []any{id}, func(run string) error {
			p.runs[run] = true
			p.add("graph_runs", run)
			return nil
		}); err != nil {
			return nil, err
		}
		// Independent spawn receipts are NOT ownership edges.
		if err := st.eachMatching("sessions", `parent_id=? OR x_graphRunId IN (SELECT id FROM data_graph_runs WHERE x_conversationId=?)`, []any{id, id}, func(child string) error {
			if _, exists := p.sessions[child]; exists {
				return nil
			}
			meta, err := st.sessionMeta(child)
			if err != nil {
				return err
			}
			if meta.ProjectID != s.ProjectID || meta.Role == sessionRoleGeneralAgent {
				return controlError(409, "Session ownership is inconsistent; deletion was not applied.")
			}
			p.sessions[child] = meta.ProjectID
			pending = append(pending, child)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (a *app) validateSQLSessionDeletion(st *sqliteSessionTx, p *sqliteSessionDeletionPlan) error {
	blocked := func() error {
		return controlError(409, "Stop running chats and finish active graphs or delegations before deleting this session. Paused graphs are still active.")
	}
	for id := range p.sessions {
		meta, err := st.sessionMeta(id)
		if err != nil {
			return err
		}
		if meta.Status == "running" || a.runs[id] != nil || a.graphStarting[id] {
			return blocked()
		}
		if runtime := a.runtimes[id]; runtime != nil {
			runtime.mu.Lock()
			group, active := runtime.group, runtime.turnActive
			runtime.mu.Unlock()
			if active || group != nil && group.HasWorkload() {
				return blocked()
			}
		}
		var active bool
		if err := st.tx.QueryRowContext(st.ctx, `SELECT
		 EXISTS(SELECT 1 FROM data_consultations WHERE (x_from=? OR x_to=?) AND (status IN ('queued','answering') OR x_delivery='delivering')) OR
		 EXISTS(SELECT 1 FROM data_automation_runs WHERE session_id=? AND status='running') OR
		 EXISTS(SELECT 1 FROM data_delegation_batches b WHERE (x_parentSessionId=? OR x_ownerConversationId=?) AND (x_state<>'settled' OR x_delivery IN ('pending','sending','uncertain') OR x_outcome='finality_unconfirmed' OR EXISTS(SELECT 1 FROM data_delegation_calls c WHERE c.owner_id=b.id AND c.x_delivery IN ('pending','sending','uncertain')))) OR
		 EXISTS(SELECT 1 FROM data_delegation_invocations WHERE (x_parentSessionId=? OR x_childSessionId=?) AND x_state<>'terminal') OR
		 EXISTS(SELECT 1 FROM data_delegation_children WHERE id=? AND (x_activeInvocationId<>'' OR x_quarantined='true'))`, id, id, id, id, id, id, id, id).Scan(&active); err != nil {
			return err
		}
		if active {
			return blocked()
		}
	}
	for id := range p.runs {
		var status string
		if err := st.tx.QueryRowContext(st.ctx, `SELECT status FROM data_graph_runs WHERE id=?`, id).Scan(&status); err != nil {
			return err
		}
		if graphRunActive(status) || status == "paused" || status == "pausing" || a.graphRuns[id] != nil {
			return blocked()
		}
		var active bool
		if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_activations WHERE x_runId=? AND status IN ('reserved','running','waiting_user','sealing'))`, id).Scan(&active); err != nil {
			return err
		}
		if active {
			return blocked()
		}
	}
	return st.protectSQLDeletionWorkspaces(p)
}

func (st *sqliteSessionTx) inventorySQLSessionDeletion(p *sqliteSessionDeletionPlan) error {
	collect := func(table, where string, args ...any) error {
		return st.eachMatching(table, where, args, func(id string) error { p.add(table, id); return nil })
	}
	for id := range p.sessions {
		for _, item := range []struct{ table, where string }{
			{"native_sessions", "id=?"}, {"permission_grants", "session_id=?"}, {"accepted_messages", "id IN (SELECT receipt_key FROM accepted_message_ids WHERE session_id=?)"},
			{"graph_activities", "x_conversationId=?"}, {"graph_notifications", "x_conversationId=?"},
			{"automation_runs", "session_id=?"}, {"automation_recoveries", "session_id=?"},
			{"analytics_executions", "session_id=? OR x_ownerId=?"},
			{"delegation_children", "id=? OR x_parentSessionId=?"},
			{"delegation_batches", "x_parentSessionId=? OR x_ownerConversationId=?"},
			{"delegation_invocations", "x_parentSessionId=? OR x_childSessionId=?"},
			{"consultations", "x_from=? OR x_to=?"}, {"session_spawns", "x_from=? OR session_id=?"},
			{"session_commands", "x_from=? OR x_request_sessionId=?"},
		} {
			args := []any{id}
			if slices.Contains([]string{"analytics_executions", "delegation_children", "delegation_batches", "delegation_invocations", "consultations", "session_spawns", "session_commands"}, item.table) {
				args = append(args, id)
			}
			if err := collect(item.table, item.where, args...); err != nil {
				return err
			}
		}
		if err := st.eachMatching("queued_messages", "owner_id=?", []any{id}, func(key string) error {
			var message string
			if err := st.tx.QueryRowContext(st.ctx, `SELECT record_id FROM data_queued_messages WHERE id=?`, key).Scan(&message); err != nil {
				return err
			}
			p.add("queue_payloads", message)
			return nil
		}); err != nil {
			return err
		}
	}
	// Legacy storedImage has exported fields without JSON tags. Its SessionID
	// therefore is not the generic sessionId routing projection.
	if err := st.eachMatching("chat_images", "1=1", nil, func(key string) error {
		var image storedImage
		if err := decodeSQLiteRecord(st.ctx, st.tx, "chat_images", key, 64<<10, &image); err != nil {
			return err
		}
		if _, owned := p.sessions[image.SessionID]; owned {
			p.images[image.SessionID] = append(p.images[image.SessionID], key)
			p.add("chat_images", key)
		}
		return nil
	}); err != nil {
		return err
	}
	for run := range p.runs {
		for _, item := range []struct{ table, where string }{
			{"graph_activations", "x_runId=?"}, {"graph_deliveries", "x_runId=?"}, {"graph_join_rounds", "x_runId=?"},
			{"graph_workspace_uses", "x_runId=?"}, {"graph_workspaces", "x_createdByRunId=?"}, {"analytics_graphs", "map_key=?"},
		} {
			if err := collect(item.table, item.where, run); err != nil {
				return err
			}
		}
	}
	for execution := range p.records["analytics_executions"] {
		var identity string
		if err := st.tx.QueryRowContext(st.ctx, `SELECT record_id FROM data_analytics_executions WHERE id=?`, execution).Scan(&identity); err != nil {
			return err
		}
		if err := collect("analytics_counters", "x_executionId=?", identity); err != nil {
			return err
		}
	}
	for run := range p.records["automation_runs"] {
		if err := collect("automation_recoveries", "x_runId=?", run); err != nil {
			return err
		}
	}
	return nil
}

func (st *sqliteSessionTx) applySQLSessionDeletion(p *sqliteSessionDeletionPlan) error {
	for id := range p.sessions {
		if err := st.putRoot("deleted_sessions", id, deletedSession{RootID: p.root, Images: p.images[id], CleanupPending: true}); err != nil {
			return err
		}
		// Existing routing columns keep retry/recovery scoped without a schema change.
		if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_deleted_sessions SET parent_id=?,status='cleanup_pending' WHERE id=?`, p.root, id); err != nil {
			return err
		}
	}
	if st.deletedSessions == nil {
		st.deletedSessions = map[string]string{}
	}
	for id, project := range p.sessions {
		st.deletedSessions[id] = project
	}
	for key := range p.records["session_spawns"] {
		var receipt SessionSpawn
		if err := decodeSQLiteRecord(st.ctx, st.tx, "session_spawns", key, maxStreamRecordBytes, &receipt); err != nil {
			return err
		}
		if _, deleted := p.sessions[receipt.SessionID]; deleted {
			if err := st.putRoot("deleted_operations", "spawn/"+receipt.From+"/"+receipt.OperationID, true); err != nil {
				return err
			}
		}
	}
	for key := range p.records["session_commands"] {
		var receipt SessionCommand
		if err := decodeSQLiteRecord(st.ctx, st.tx, "session_commands", key, 1<<20, &receipt); err != nil {
			return err
		}
		if _, deleted := p.sessions[receipt.Request.SessionID]; deleted {
			if err := st.putRoot("deleted_operations", "send/"+receipt.From+"/"+receipt.Request.OperationID, true); err != nil {
				return err
			}
		}
	}
	// Keep fixed automation destinations linked to their tombstone. Clearing the
	// destination would turn reuse_created into permission to create a replacement.
	for id := range p.sessions {
		if err := st.eachMatching("automations", "session_id=?", []any{id}, func(key string) error {
			var value Automation
			if err := decodeSQLiteRecord(st.ctx, st.tx, "automations", key, 8<<20, &value); err != nil {
				return err
			}
			if value.Revision == ^uint64(0) {
				return errors.New("Automation revision exhausted.")
			}
			value.Enabled, value.NextScheduledAt = false, ""
			value.Revision++
			return st.putRoot("automations", key, value)
		}); err != nil {
			return err
		}
	}
	// Remove non-session roots first, while ordinary deletion can still resolve
	// related owners. The explicit purge gate alone can remove instrumented data.
	for _, table := range []string{"consultations", "session_commands", "session_spawns", "queue_payloads", "chat_images", "permission_grants", "accepted_messages", "automation_recoveries", "automation_runs", "analytics_counters", "analytics_executions", "analytics_graphs", "graph_notifications", "graph_workspace_uses", "graph_workspaces", "graph_deliveries", "graph_join_rounds", "graph_activations", "graph_runs", "graph_activities"} {
		for key := range p.records[table] {
			if table == "consultations" {
				if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM recovery_consultations WHERE consultation_id=?`, key); err != nil {
					return err
				}
			}
			if table == "accepted_messages" {
				if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM accepted_message_ids WHERE receipt_key=?`, key); err != nil {
					return err
				}
			}
			if err := st.deleteRecord(table, key); err != nil {
				return err
			}
		}
	}
	for _, table := range []string{"delegation_invocations", "delegation_batches", "delegation_children", "native_sessions"} {
		for key := range p.records[table] {
			if err := st.deleteRecordForSessionDeletion(table, key); err != nil {
				return err
			}
		}
	}
	for id := range p.sessions {
		for _, table := range []string{"accepted_message_ids", "recovery_consultations", "runtime_activity", "permission_reply_intents"} {
			if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM `+table+` WHERE session_id=?`, id); err != nil {
				return err
			}
		}
		// A generation base can have descendants; drop publications via the session
		// delete first, then unlink only this owner's immutable generation chain.
		if err := st.deleteRecordForSessionDeletion("sessions", id); err != nil {
			return err
		}
		if _, err := st.tx.ExecContext(st.ctx, `UPDATE event_content_generations SET base_id=NULL WHERE owner_id=?`, id); err != nil {
			return err
		}
		if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM event_content_generations WHERE owner_id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) deleteSQLSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var plan *sqliteSessionDeletionPlan
	var deletedIDs []string
	var runtimes []*sessionRuntime
	a.mu.Lock()
	err := a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		var previous deletedSession
		err := decodeSQLiteRecord(st.ctx, st.tx, "deleted_sessions", id, maxStreamRecordBytes, &previous)
		if err == nil {
			if previous.RootID != id {
				return controlError(400, "Only top-level project sessions can be deleted.")
			}
			return st.eachMatching("deleted_sessions", "id=? OR parent_id=? OR parent_id=''", []any{id, id}, func(key string) error {
				var record deletedSession
				if err := decodeSQLiteRecord(st.ctx, st.tx, "deleted_sessions", key, maxStreamRecordBytes, &record); err != nil {
					return err
				}
				if record.RootID == id {
					deletedIDs = append(deletedIDs, key)
				}
				return nil
			})
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		plan, err = st.sessionDeletionPlan(id)
		if err != nil {
			return err
		}
		if err := a.validateSQLSessionDeletion(st, plan); err != nil {
			return err
		}
		if err := st.inventorySQLSessionDeletion(plan); err != nil {
			return err
		}
		if err := st.applySQLSessionDeletion(plan); err != nil {
			return err
		}
		for key := range plan.sessions {
			deletedIDs = append(deletedIDs, key)
		}
		return nil
	})
	if err == nil && plan != nil {
		for child := range plan.sessions {
			if runtime := a.runtimes[child]; runtime != nil {
				runtimes = append(runtimes, runtime)
				delete(a.runtimes, child)
			}
			delete(a.historyJournal, child)
			delete(a.streamIndexes, child)
			delete(a.stopVersions, child)
			delete(a.harnessConsultationDeadlines, child)
		}
		for run := range plan.runs {
			delete(a.graphValidation, run)
			delete(a.analyticsGraphClocks, run)
		}
	}
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	var cleanupErr error
	for _, runtime := range runtimes {
		cleanupErr = errors.Join(cleanupErr, runtime.shutdown())
	}
	if cleanupErr == nil {
		cleanupErr = a.cleanupSQLDeletedSessions(id)
	}
	slices.Sort(deletedIDs)
	response := map[string]any{"deletedSessionIds": deletedIDs}
	if cleanupErr != nil {
		response["cleanupError"] = "Session deleted, but file cleanup is incomplete. Retry Delete or restart the engine. " + cleanupErr.Error()
	}
	respond(w, http.StatusOK, response)
}
