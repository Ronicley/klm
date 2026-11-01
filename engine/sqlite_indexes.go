package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func sqliteScalarProjection(table string) bool {
	switch table {
	case "project_topic_order", "legacy_project_folders", "legacy_archived_folders", "graph_dependencies", "graph_operations", "activation_violations", "graph_join_expected", "automation_resolution_occurrences", "analytics_waits", "analytics_unknown_waits", "analytics_reasons":
		return true
	}
	return false
}

// Only routing/eligibility/reference fields are projected. Prompt/tool/output,
// raw webhook bodies, credentials and immutable snapshot content stay in their
// separate record chunks. Revisions/sequences remain canonical decimal TEXT.
var sqliteIndexFields = map[string][]string{
	"delegation_batches":              {"parentSessionId", "ownerConversationId", "parentTurnId", "operationId", "requestHash", "state", "delivery", "outcome", "revision", "endedAt", "graph.runId", "graph.activationId", "graph.turnId", "graph.workspaceId", "graph.snapshotDigest"},
	"delegation_calls":                {"parentTurnId", "sessionId", "turnId", "callId", "delivery"},
	"delegation_invocations":          {"batchId", "key", "ordinal", "childSessionId", "parentSessionId", "parentTurnId", "state", "outcome", "dispatchAttempted", "nativeAccepted", "nativeSessionId", "nativeTurnId", "resultDigest", "capture.config.harness", "graph.runId", "graph.activationId", "graph.turnId", "graph.workspaceId"},
	"delegation_children":             {"parentSessionId", "origin", "harness", "executionCwd", "graphRunId", "workspaceId", "activeInvocationId", "quarantined"},
	"global_agents":                   {"slug"},
	"agent_sources":                   {"source.sourceUid", "source.id", "source.projectId", "state"},
	"session_events":                  {"data.childSessionId", "data.automationRecoveryNotice"},
	"capability_preparing":            {"version"},
	"projects":                        {"name", "folder", "icon", "removed"},
	"topics":                          {"name", "color", "archived"},
	"sessions":                        {"title", "workspace", "topicId", "harness", "harnessLocked", "archived", "graphRunId", "graphNodeId", "selectedGraphId", "executionCwd", "automation.runId", "automation.automationId", "automation.status"},
	"session_spawns":                  {"from", "operationId", "sourceUserEventId", "messageId", "request.projectId"},
	"session_commands":                {"from", "request.sessionId", "request.operationId", "request.sourceUserEventId", "messageId"},
	"queued_messages":                 {"mode"},
	"permission_grants":               {"harness", "kind", "key", "decision"},
	"consultations":                   {"from", "to", "delivery"},
	"graph_activities":                {"conversationId", "graphId", "order", "priority", "version", "correction"},
	"graph_runs":                      {"activityId", "conversationId", "graphId", "catalogGraphId", "initialWorkspaceId", "endedAt"},
	"graph_snapshots":                 {"graph.definition.name"},
	"graph_activations":               {"runId", "nodeId", "occurrence", "workspaceId", "originWorkspaceId", "causalId", "forkActivationId", "branchId", "joinRoundId", "endedAt", "violations", "nativeEventStart", "nativeEventEnd", "submission.choice.origin", "submission.choice.id", "submission.acceptedAt"},
	"graph_assessments":               {"runId", "operationId"},
	"graph_authorization":             {"eventId"},
	"graph_deliveries":                {"runId", "connectionId", "producerActivationId", "targetNodeId", "targetActivationId", "workspaceId", "originWorkspaceId", "causalId", "joinRoundId"},
	"graph_join_rounds":               {"runId", "nodeId", "occurrence", "causalId", "activationId", "originWorkspaceId", "integrationWorkspaceId", "endedAt"},
	"graph_join_arrivals":             {"connectionId", "deliveryId"},
	"graph_workspaces":                {"createdByRunId", "createdByActivationId", "operationId"},
	"graph_workspace_uses":            {"workspaceId", "runId", "activationId", "association"},
	"graph_notifications":             {"kind", "activityId", "runId", "conversationId", "retryAfter"},
	"graph_catalog_changes":           {"oldId", "newId"},
	"webhook_connections":             {"enabled", "provider"},
	"webhook_events":                  {"webhookId", "deliveryId", "eventType", "isPing", "receivedAt", "expiresAt", "receivedSequence", "payloadBytes"},
	"webhook_pending":                 {"eventId", "receivedSequence"},
	"webhook_releases":                {"eventId", "releaseSequence", "emittedAt"},
	"board_connections":               {"provider", "scope.repositoryId", "scope.organization", "scope.projectId", "scope.workItemType", "onlyAssignedToMe"},
	"board_sync":                      {"stale", "snapshotPending"},
	"board_items":                     {"connectionId", "key", "externalId", "iterationPath"},
	"board_moves":                     {"connectionId", "operationId", "key"},
	"automations":                     {"enabled", "deleted", "mode", "nextScheduledAt", "sessionMode", "topicId", "operationId", "name"},
	"automation_triggers":             {"automationId", "key", "consumed", "at"},
	"automation_runs":                 {"automationId", "startedAt", "finishedAt", "needsContinuation", "snapshot.mode", "snapshot.sessionMode", "snapshot.name"},
	"automation_recoveries":           {"runId", "snapshot.id", "delivery", "resolved", "noticeEventId", "nextOccurrenceAt", "source", "action", "from"},
	"automation_occurrence_decisions": {"scheduledAt", "action"},
	"automation_resolutions":          {"operationId", "userEventId", "action"},
	"analytics_executions":            {"ownerId", "graphRunId", "activationId", "reserved", "dispatched", "accepted", "last", "ended", "sequence", "unknownUntil", "harness"},
	"analytics_spans":                 {"start", "end", "kind"},
	"analytics_usage":                 {"sequence", "at"},
	"analytics_counters":              {"executionId"},
	"analytics_graphs":                {"last", "end", "interrupted"},
}

func sqliteIndexColumn(path string) string { return `"x_` + strings.ReplaceAll(path, ".", "_") + `"` }

func sqliteTimeKey(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func sqliteIndexedValue(field, value string) string {
	if strings.HasSuffix(field, "At") || field == "unknownUntil" || field == "reserved" || field == "dispatched" || field == "accepted" || field == "last" || field == "ended" || field == "start" || field == "end" || field == "at" {
		return sqliteTimeKey(value)
	}
	return value
}

func sqliteEligibilitySchema(table string) string {
	var ddl strings.Builder
	for i, field := range sqliteIndexFields[table] {
		column := sqliteIndexColumn(field)
		fmt.Fprintf(&ddl, "ALTER TABLE data_%s ADD COLUMN %s TEXT NOT NULL DEFAULT ''; CREATE INDEX data_%s_lookup_%d ON data_%s(%s,id);", table, column, table, i, table, column)
	}
	switch table {
	case "delegation_batches":
		ddl.WriteString(`CREATE UNIQUE INDEX data_delegation_batches_record_unique ON data_delegation_batches(record_id);
CREATE UNIQUE INDEX data_delegation_batches_operation ON data_delegation_batches(x_parentSessionId,x_operationId);
CREATE UNIQUE INDEX data_delegation_batches_active_parent ON data_delegation_batches(x_parentSessionId) WHERE x_state<>'settled' OR x_delivery IN ('pending','sending','uncertain') OR x_outcome='finality_unconfirmed';
CREATE INDEX data_delegation_batches_owner_page ON data_delegation_batches(x_ownerConversationId,created_at DESC,id);
CREATE INDEX data_delegation_batches_graph_epoch ON data_delegation_batches(x_graph_runId,x_graph_activationId,x_graph_turnId,x_state,id);
CREATE INDEX data_delegation_batches_recovery ON data_delegation_batches(x_state,x_delivery,id);`)
		ddl.WriteString(`CREATE INDEX data_delegation_batches_pending_recovery ON data_delegation_batches(id) WHERE x_state<>'settled' OR x_delivery IN ('pending','sending');`)
	case "delegation_calls":
		ddl.WriteString(`CREATE UNIQUE INDEX data_delegation_calls_native_alias ON data_delegation_calls(x_parentTurnId,x_sessionId,x_turnId,x_callId);
CREATE INDEX data_delegation_calls_delivery ON data_delegation_calls(owner_id,x_parentTurnId,x_delivery,position);`)
	case "delegation_invocations":
		ddl.WriteString(`CREATE UNIQUE INDEX data_delegation_invocations_record_unique ON data_delegation_invocations(record_id);
CREATE UNIQUE INDEX data_delegation_invocations_member_key ON data_delegation_invocations(x_batchId,x_key);
CREATE UNIQUE INDEX data_delegation_invocations_member_order ON data_delegation_invocations(x_batchId,x_ordinal);
CREATE UNIQUE INDEX data_delegation_invocations_active_child ON data_delegation_invocations(x_childSessionId) WHERE x_state<>'terminal';
CREATE INDEX data_delegation_invocations_batch_order ON data_delegation_invocations(x_batchId,CAST(x_ordinal AS INTEGER),id);
CREATE INDEX data_delegation_invocations_graph_epoch ON data_delegation_invocations(x_graph_runId,x_graph_activationId,x_graph_turnId,x_state,id);
CREATE INDEX data_delegation_invocations_recovery ON data_delegation_invocations(x_state,id);`)
		ddl.WriteString(`CREATE INDEX data_delegation_invocations_pending_recovery ON data_delegation_invocations(id) WHERE x_state<>'terminal';`)
	case "delegation_children":
		ddl.WriteString(`CREATE UNIQUE INDEX data_delegation_children_record_unique ON data_delegation_children(record_id);
CREATE UNIQUE INDEX data_delegation_children_lease ON data_delegation_children(x_activeInvocationId) WHERE x_activeInvocationId<>'';
CREATE INDEX data_delegation_children_parent ON data_delegation_children(x_parentSessionId,created_at,id);
CREATE INDEX data_delegation_children_graph_workspace ON data_delegation_children(x_graphRunId,x_workspaceId,id);`)
	case "global_agents":
		ddl.WriteString(`CREATE UNIQUE INDEX data_global_agents_slug ON data_global_agents(x_slug);`)
	case "sessions":
		ddl.WriteString(`CREATE INDEX data_sessions_project_page ON data_sessions(project_id,role,x_archived,created_at DESC,id);
CREATE INDEX data_sessions_topic ON data_sessions(project_id,x_topicId,created_at DESC,id);
CREATE INDEX data_sessions_parent ON data_sessions(parent_id,role,id);`)
	case "webhook_events":
		ddl.WriteString(`CREATE INDEX data_webhook_events_dedupe ON data_webhook_events(x_webhookId,x_deliveryId);
CREATE INDEX data_webhook_events_expiry ON data_webhook_events(x_expiresAt,id);`)
	case "webhook_releases":
		ddl.WriteString(`CREATE INDEX data_webhook_releases_sequence ON data_webhook_releases(length(x_releaseSequence),x_releaseSequence);`)
	case "automations":
		ddl.WriteString(`CREATE INDEX data_automations_due ON data_automations(x_enabled,x_deleted,x_mode,x_nextScheduledAt,id);`)
	case "automation_triggers":
		ddl.WriteString(`CREATE INDEX data_automation_triggers_fifo ON data_automation_triggers(x_automationId,x_consumed,position);`)
	case "graph_runs":
		ddl.WriteString(`CREATE INDEX data_graph_runs_owner_status ON data_graph_runs(x_conversationId,status,id);`)
	}
	return ddl.String()
}

func (s legacyStage) projectedScalar(node int64, path string) (string, error) {
	for _, field := range strings.Split(path, ".") {
		child, err := s.child(node, field)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		node = child
	}
	n, err := s.node(node)
	if err != nil {
		return "", err
	}
	if path == "data.automationRecoveryNotice" {
		if string(n.scalar) == "true" {
			return "true", nil
		}
		return "", nil
	}
	if strings.HasPrefix(path, "data.") && (n.kind != "scalar" || len(n.scalar) == 0 || n.scalar[0] != '"') {
		return "", nil
	}
	if n.kind != "scalar" {
		return "", fmt.Errorf("index field %s is not a scalar", path)
	}
	return sqliteScalarText(n.scalar)
}

func sqliteScalarText(raw []byte) (string, error) {
	if string(raw) == "null" {
		return "", nil
	}
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		err := json.Unmarshal(raw, &text)
		return text, err
	}
	return string(raw), nil
}

func (s legacyStage) importIndexFields(table, id string, node int64) error {
	fields := sqliteIndexFields[table]
	if len(fields) == 0 {
		return nil
	}
	set := make([]string, 0, len(fields))
	args := make([]any, 0, len(fields)+1)
	for _, field := range fields {
		value, err := s.projectedScalar(node, field)
		if err != nil {
			return err
		}
		set = append(set, sqliteIndexColumn(field)+"=?")
		args = append(args, sqliteIndexedValue(field, value))
	}
	args = append(args, id)
	_, err := s.tx.ExecContext(s.ctx, `UPDATE data_`+table+` SET `+strings.Join(set, ",")+` WHERE id=?`, args...)
	return err
}

func updateSQLiteIndexFields(ctx context.Context, tx *sql.Tx, table, id string, raw []byte) error {
	fields := sqliteIndexFields[table]
	if len(fields) == 0 {
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	set := make([]string, 0, len(fields))
	args := make([]any, 0, len(fields)+1)
	for _, field := range fields {
		current := object
		var value json.RawMessage
		parts := strings.Split(field, ".")
		for i, part := range parts {
			value = current[part]
			if i == len(parts)-1 || len(value) == 0 || string(value) == "null" {
				break
			}
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(value, &nested); err != nil {
				return err
			}
			current = nested
		}
		text, err := sqliteScalarText(value)
		if err != nil {
			return err
		}
		if strings.HasPrefix(field, "data.") && (len(value) == 0 || value[0] != '"') {
			text = ""
		}
		if field == "data.automationRecoveryNotice" {
			text = ""
			if string(value) == "true" {
				text = "true"
			}
		}
		set = append(set, sqliteIndexColumn(field)+"=?")
		args = append(args, sqliteIndexedValue(field, text))
	}
	args = append(args, id)
	_, err := tx.ExecContext(ctx, `UPDATE data_`+table+` SET `+strings.Join(set, ",")+` WHERE id=?`, args...)
	return err
}
