package main

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Explicit persistence inventory. Each root collection and each append-growing
// nested collection has a separate table. Static configuration/snapshot payloads
// may remain per-record JSON, but are chunked independently of metadata queries.
// The positional importer is not one of these tables and is never read at runtime.
type sqliteDomain struct {
	field       string
	table       string
	single      bool
	children    []sqliteDomain
	typ         reflect.Type // Resolved from the existing serialization contract.
	parentTable string
}

func sqliteDomains() ([]sqliteDomain, error) {
	roots := []sqliteDomain{
		{field: "delegationBatches", table: "delegation_batches", children: []sqliteDomain{{field: "calls", table: "delegation_calls"}}},
		{field: "delegationInvocations", table: "delegation_invocations"},
		{field: "delegationChildren", table: "delegation_children"},
		{field: "globalAgents", table: "global_agents"},
		{field: "agentSources", table: "agent_sources"},
		{field: "agentCatalogChanges", table: "agent_catalog_changes"},
		{field: "engineSettings", table: "engine_settings", single: true},
		{field: "automations", table: "automations"},
		{field: "automationTriggers", table: "automation_triggers"},
		{field: "automationRuns", table: "automation_runs"},
		{field: "automationRecoveries", table: "automation_recoveries", children: []sqliteDomain{
			{field: "occurrenceDecisions", table: "automation_occurrence_decisions"},
			{field: "resolutions", table: "automation_resolutions", children: []sqliteDomain{{field: "occurrenceIds", table: "automation_resolution_occurrences"}}},
		}},
		{field: "analytics", table: "analytics_settings", single: true, children: []sqliteDomain{
			{field: "executions", table: "analytics_executions", children: []sqliteDomain{
				{field: "spans", table: "analytics_spans"}, {field: "usage", table: "analytics_usage"},
				{field: "waits", table: "analytics_waits"}, {field: "unknownWaits", table: "analytics_unknown_waits"},
				{field: "reasons", table: "analytics_reasons"},
			}},
			{field: "counters", table: "analytics_counters"}, {field: "graphs", table: "analytics_graphs"},
		}},
		{field: "activityBoardConfig", table: "board_config", single: true, children: []sqliteDomain{{field: "phases", table: "board_phases"}}},
		{field: "activityBoardConnections", table: "board_connections"},
		{field: "activityBoardCredentials", table: "board_credentials"},
		{field: "activityBoardItems", table: "board_items"},
		{field: "activityBoardSync", table: "board_sync"},
		{field: "activityBoardMoves", table: "board_moves"},
		{field: "mcpConnections", table: "mcp_connections"},
		{field: "mcpCredentials", table: "mcp_credentials"},
		{field: "libraryDeletions", table: "library_deletions", children: []sqliteDomain{
			{field: "projects", table: "library_authoring_changes", children: []sqliteDomain{
				{field: "project", table: "library_project_snapshot", single: true, children: []sqliteDomain{
					{field: "topics", table: "library_project_topics"}, {field: "topicOrder", table: "library_project_order"}, {field: "folders", table: "library_project_folders"}, {field: "archivedFolders", table: "library_project_archives"},
				}},
				{field: "expected", table: "library_authoring_preconditions"}, {field: "writes", table: "library_authoring_writes"},
			}},
		}},
		{field: "webhookConnections", table: "webhook_connections"},
		{field: "webhookEvents", table: "webhook_events", children: []sqliteDomain{{field: "payload", table: "webhook_payloads", single: true}}},
		{field: "webhookPending", table: "webhook_pending"},
		{field: "webhookReleases", table: "webhook_releases"},
		{field: "acceptedMessages", table: "accepted_messages"},
		{field: "sessionSpawns", table: "session_spawns"},
		{field: "sessionCommands", table: "session_commands"},
		{field: "queuePayloads", table: "queue_payloads"},
		{field: "graphActivities", table: "graph_activities", children: []sqliteDomain{
			{field: "authorization", table: "graph_authorization"}, {field: "dependencies", table: "graph_dependencies"},
			{field: "assessments", table: "graph_assessments"}, {field: "operationIds", table: "graph_operations"},
		}},
		{field: "graphRuns", table: "graph_runs", children: []sqliteDomain{{field: "snapshot", table: "graph_snapshots", single: true}}},
		{field: "graphActivations", table: "graph_activations", children: []sqliteDomain{
			{field: "events", table: "activation_events"}, {field: "violationIds", table: "activation_violations"},
			{field: "violationFeedback", table: "activation_feedback"},
		}},
		{field: "graphDeliveries", table: "graph_deliveries"},
		{field: "graphJoinRounds", table: "graph_join_rounds", children: []sqliteDomain{
			{field: "expected", table: "graph_join_expected"}, {field: "arrivals", table: "graph_join_arrivals"},
		}},
		{field: "graphWorkspaces", table: "graph_workspaces"},
		{field: "graphWorkspaceUses", table: "graph_workspace_uses"},
		{field: "graphNotifications", table: "graph_notifications"},
		{field: "graphCatalogChanges", table: "graph_catalog_changes", children: []sqliteDomain{{field: "writes", table: "graph_catalog_writes"}}},
		{field: "consultations", table: "consultations"},
		{field: "projects", table: "projects", children: []sqliteDomain{
			{field: "topics", table: "topics"}, {field: "topicOrder", table: "project_topic_order"},
			{field: "folders", table: "legacy_project_folders"}, {field: "archivedFolders", table: "legacy_archived_folders"},
		}},
		{field: "sessions", table: "sessions", children: []sqliteDomain{
			{field: "agentBinding", table: "agent_bindings", single: true},
			{field: "events", table: "session_events"}, {field: "queue", table: "queued_messages"},
			{field: "sources", table: "session_sources"}, {field: "permissions", table: "pending_permissions"},
			{field: "questions", table: "pending_questions"}, {field: "usage", table: "session_usage", single: true},
			{field: "capabilities", table: "session_capabilities", single: true, children: []sqliteDomain{
				{field: "desired", table: "capability_selections", single: true},
				{field: "applied", table: "capability_applied", single: true},
				{field: "preparing", table: "capability_preparing", single: true},
			}},
		}},
		{field: "native", table: "native_sessions"},
		{field: "permissionGrants", table: "permission_grants"},
		{field: "transcription", table: "transcription_settings", single: true, children: []sqliteDomain{{field: "vocabulary", table: "transcription_vocabulary"}}},
	}
	fields := map[string]bool{}
	for name := range sqliteScalarFields {
		fields[name] = true
	}
	tables := map[string]bool{}
	var resolve func(*sqliteDomain, reflect.Type) error
	resolve = func(spec *sqliteDomain, parent reflect.Type) error {
		typ, ok := legacyField(parent, spec.field)
		if !ok || tables[spec.table] || !sqliteIdentifier(spec.table) {
			return fmt.Errorf("invalid SQLite domain mapping %s", spec.field)
		}
		tables[spec.table] = true
		spec.typ = typ
		if !spec.single {
			if typ.Kind() != reflect.Map && typ.Kind() != reflect.Slice {
				return fmt.Errorf("SQLite collection mapping is not a collection: %s", spec.field)
			}
			typ = typ.Elem()
		}
		for i := range spec.children {
			spec.children[i].parentTable = spec.table
			if err := resolve(&spec.children[i], typ); err != nil {
				return err
			}
		}
		return nil
	}
	for i := range roots {
		if fields[roots[i].field] {
			return nil, errors.New("duplicate SQLite root mapping")
		}
		fields[roots[i].field] = true
		if err := resolve(&roots[i], reflect.TypeOf(diskState{})); err != nil {
			return nil, err
		}
	}
	// A newly added diskState field blocks schema initialization until it has an
	// explicit destination. Reflection here is schema checking, never runtime diff.
	t := reflect.TypeOf(diskState{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if !fields[name] {
			return nil, fmt.Errorf("unmapped SQLite root field %s", name)
		}
	}
	return roots, nil
}

var sqliteScalarFields = map[string]bool{
	"version": true, "topicSchema": true, "journalFormat": true,
	"graphRevision": true, "graphViewRevision": true,
	"webhookReceivedSequence": true, "webhookReleaseSequence": true,
}

func sqliteIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c != '_' && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

func sqliteDomainTables() ([]sqliteDomain, error) {
	roots, err := sqliteDomains()
	if err != nil {
		return nil, err
	}
	var all []sqliteDomain
	var walk func(sqliteDomain)
	walk = func(d sqliteDomain) {
		all = append(all, d)
		for _, child := range d.children {
			walk(child)
		}
	}
	for _, d := range roots {
		walk(d)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].table < all[j].table })
	return all, nil
}

// Data rows have typed/indexed routing columns and independently chunked content.
// Children reference their containing record. The same JSON byte encoding can be
// streamed for verification without constructing its domain or collection in Go.
func sqliteDomainSchema() (string, error) {
	return sqliteDomainSchemaSelection(nil)
}

// The same authoritative DDL is used for fresh stores and explicitly versioned
// additive upgrades. A nonnil selection excludes shared prelude/auxiliary tables.
func sqliteDomainSchemaSelection(selected map[string]bool) (string, error) {
	tables, err := sqliteDomainTables()
	if err != nil {
		return "", err
	}
	var ddl strings.Builder
	if selected == nil {
		ddl.WriteString(`CREATE TABLE record_collections(
 parent_table TEXT NOT NULL, parent_id TEXT NOT NULL, field TEXT NOT NULL,
 child_table TEXT NOT NULL, kind TEXT NOT NULL, size INTEGER NOT NULL,
 PRIMARY KEY(parent_table,parent_id,field)
) STRICT;`)
	}
	for _, spec := range tables {
		if selected != nil && !selected[spec.table] {
			continue
		}
		table := "data_" + spec.table
		ownerReference := ""
		if spec.parentTable != "" {
			ownerReference = " REFERENCES data_" + spec.parentTable + "(id) DEFERRABLE INITIALLY DEFERRED"
		}
		fmt.Fprintf(&ddl, `CREATE TABLE %s(
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL%s, map_key TEXT NOT NULL,
 position INTEGER NOT NULL CHECK(position>=0), record_id TEXT NOT NULL,
 project_id TEXT NOT NULL, session_id TEXT NOT NULL, parent_id TEXT NOT NULL,
 status TEXT NOT NULL, role TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 kind TEXT NOT NULL DEFAULT '', correlation_id TEXT NOT NULL DEFAULT '', revision TEXT NOT NULL DEFAULT '0',
 scalar_value TEXT NOT NULL DEFAULT '',
 payload_bytes INTEGER NOT NULL DEFAULT 0, digest TEXT NOT NULL DEFAULT '',
 UNIQUE(owner_id,position)
) STRICT;
CREATE INDEX %s_owner ON %s(owner_id,map_key);
CREATE INDEX %s_identity ON %s(record_id);
CREATE INDEX %s_project ON %s(project_id,role,created_at DESC,id);
CREATE INDEX %s_status ON %s(status,owner_id,id);
CREATE INDEX %s_session ON %s(session_id,created_at,id);
CREATE TABLE %s_chunks(
 record_key TEXT NOT NULL REFERENCES %s(id) ON DELETE CASCADE,
 chunk INTEGER NOT NULL CHECK(chunk>=0), body BLOB NOT NULL CHECK(length(body)<=65536),
 PRIMARY KEY(record_key,chunk)
) STRICT;`, table, ownerReference, table, table, table, table, table, table, table, table, table, table, table, table)
		ddl.WriteString(sqliteEligibilitySchema(spec.table))
		if spec.table == "project_topic_order" {
			ddl.WriteString(`CREATE INDEX data_project_topic_order_value ON data_project_topic_order(owner_id,scalar_value);`)
		}
		if spec.table == "session_events" {
			ddl.WriteString(`ALTER TABLE data_session_events ADD COLUMN message_at TEXT NOT NULL DEFAULT ''; CREATE INDEX data_session_events_recency ON data_session_events(owner_id,position DESC) WHERE message_at<>'';`)
			ddl.WriteString(`CREATE INDEX data_session_events_owner_identity ON data_session_events(owner_id,record_id,position);
CREATE INDEX data_session_events_kind ON data_session_events(owner_id,kind,position);
CREATE INDEX data_session_events_correlation ON data_session_events(owner_id,correlation_id,position);`)
		}
	}
	if selected != nil {
		return ddl.String(), nil
	}
	ddl.WriteString(`CREATE TABLE accepted_message_ids(
 session_id TEXT NOT NULL REFERENCES data_sessions(id), message_id TEXT NOT NULL,
 receipt_key TEXT NOT NULL REFERENCES data_accepted_messages(id),
 PRIMARY KEY(session_id,message_id)
) STRICT;
CREATE INDEX accepted_message_ids_collision ON accepted_message_ids(message_id);`)
	ddl.WriteString(`CREATE TABLE recovery_consultations(session_id TEXT PRIMARY KEY REFERENCES data_sessions(id),consultation_id TEXT NOT NULL REFERENCES data_consultations(id)) STRICT;`)
	ddl.WriteString(`CREATE TABLE selection_references(domain TEXT NOT NULL,record_key TEXT NOT NULL,kind TEXT NOT NULL,item TEXT NOT NULL,PRIMARY KEY(domain,record_key,kind,item)) STRICT;
CREATE INDEX selection_references_item ON selection_references(kind,item,domain,record_key);
CREATE TABLE credential_references(domain TEXT NOT NULL,record_key TEXT NOT NULL,credential TEXT NOT NULL,PRIMARY KEY(domain,record_key,credential)) STRICT;
CREATE INDEX credential_references_credential ON credential_references(credential,domain,record_key);`)
	// Positive selections are bounded, but JSON escaping can expand the allowed
	// 32*512*200-byte tool names sixfold. Do not reject a supported old selection.
	ddl.WriteString(`CREATE TABLE capability_views(domain TEXT NOT NULL,record_key TEXT NOT NULL,payload BLOB NOT NULL CHECK(length(payload)<=33554432),PRIMARY KEY(domain,record_key)) STRICT;
CREATE TABLE credential_connections(domain TEXT NOT NULL,record_key TEXT NOT NULL,connection TEXT NOT NULL,credential TEXT NOT NULL,PRIMARY KEY(domain,record_key,connection,credential)) STRICT;
CREATE INDEX credential_connections_lookup ON credential_connections(connection,credential);
CREATE TABLE capture_references(domain TEXT NOT NULL,record_key TEXT NOT NULL,kind TEXT NOT NULL,item TEXT NOT NULL,PRIMARY KEY(domain,record_key,kind,item)) STRICT;
CREATE INDEX capture_references_lookup ON capture_references(kind,item,domain,record_key);`)
	ddl.WriteString(`CREATE TABLE github_activity_summaries(event_id TEXT PRIMARY KEY REFERENCES data_webhook_events(id) ON DELETE CASCADE, payload BLOB NOT NULL, scope TEXT NOT NULL,run_key TEXT NOT NULL,run_card TEXT NOT NULL,deploy_key TEXT NOT NULL,deploy_card TEXT NOT NULL,kind TEXT NOT NULL,provider_id TEXT NOT NULL,attempt TEXT NOT NULL,check_url TEXT NOT NULL,suite_id TEXT NOT NULL,deployment_id TEXT NOT NULL,state_rank INTEGER NOT NULL,state_at TEXT NOT NULL,category TEXT NOT NULL,catalog TEXT NOT NULL,search TEXT NOT NULL) STRICT;
CREATE INDEX github_activity_summary_run ON github_activity_summaries(scope,run_key);
CREATE INDEX github_activity_summary_check ON github_activity_summaries(scope,check_url);
CREATE INDEX github_activity_summary_deploy ON github_activity_summaries(scope,deployment_id);`)
	ddl.WriteString(`CREATE TABLE board_item_views(item_id TEXT PRIMARY KEY REFERENCES data_board_items(id) ON DELETE CASCADE,phase TEXT NOT NULL,sprint TEXT NOT NULL) STRICT;
CREATE INDEX board_item_views_phase ON board_item_views(phase,sprint,item_id);`)
	ddl.WriteString(`CREATE TABLE event_stream_state(event_id TEXT PRIMARY KEY REFERENCES data_session_events(id) ON DELETE CASCADE,header BLOB NOT NULL,prefix_bytes INTEGER NOT NULL,text_bytes INTEGER NOT NULL,hash_state BLOB NOT NULL,nonblank INTEGER NOT NULL,preview TEXT NOT NULL) STRICT;
CREATE TABLE data_session_events_rewrite_chunks(record_key TEXT NOT NULL REFERENCES data_session_events(id) ON DELETE CASCADE,chunk INTEGER NOT NULL,body BLOB NOT NULL,PRIMARY KEY(record_key,chunk)) STRICT;`)
	return ddl.String(), nil
}
