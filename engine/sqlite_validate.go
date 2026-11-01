package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type sqliteValidation struct {
	ctx context.Context
	tx  *sql.Tx
}

func (v sqliteValidation) rejectRows(label, query string, args ...any) error {
	var bad bool
	if err := v.tx.QueryRowContext(v.ctx, `SELECT EXISTS(`+query+`)`, args...).Scan(&bad); err != nil {
		return err
	}
	if bad {
		return fmt.Errorf("invalid imported %s", label)
	}
	return nil
}

func (v sqliteValidation) each(table string, owner *string, visit func(string) error) error {
	if !sqliteKnownTable(table) {
		return errors.New("unknown validation table")
	}
	last := ""
	first := true
	for {
		query := `SELECT id FROM data_` + table + ` WHERE id>? ORDER BY id LIMIT 1`
		args := []any{last}
		if owner != nil {
			query = `SELECT id FROM data_` + table + ` WHERE owner_id=? AND id>? ORDER BY id LIMIT 1`
			args = []any{*owner, last}
		}
		if first {
			query = `SELECT id FROM data_` + table + ` ORDER BY id LIMIT 1`
			args = nil
			if owner != nil {
				query = `SELECT id FROM data_` + table + ` WHERE owner_id=? ORDER BY id LIMIT 1`
				args = []any{*owner}
			}
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
			return fmt.Errorf("validate %s record: %w", table, err)
		}
		last, first = id, false
	}
}

func (v sqliteValidation) get(table, id string, target any) error {
	return decodeSQLiteRecord(v.ctx, v.tx, table, id, maxStreamRecordBytes, target)
}

func (v sqliteValidation) exists(table, id string) (bool, error) {
	if !sqliteKnownTable(table) {
		return false, errors.New("unknown validation table")
	}
	var exists bool
	err := v.tx.QueryRowContext(v.ctx, `SELECT EXISTS(SELECT 1 FROM data_`+table+` WHERE id=?)`, id).Scan(&exists)
	return exists, err
}

func (v sqliteValidation) session(id string) (Session, error) {
	var s Session
	err := v.get("sessions", id, &s)
	return s, err
}
func (v sqliteValidation) userEvent(owner, id string) (bool, error) {
	var found bool
	err := v.tx.QueryRowContext(v.ctx, `SELECT EXISTS(SELECT 1 FROM data_session_events WHERE owner_id=? AND record_id=? AND kind='user')`, owner, id).Scan(&found)
	return id != "" && found, err
}

func (v sqliteValidation) collectionCount(parentTable, parent, field string) (int64, error) {
	var n int64
	err := v.tx.QueryRowContext(v.ctx, `SELECT size FROM record_collections WHERE parent_table=? AND parent_id=? AND field=?`, parentTable, parent, field).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

func (v sqliteValidation) validateIdentities() error {
	tables := []string{"projects", "sessions", "consultations", "permission_grants", "graph_catalog_changes", "graph_activities", "graph_runs", "graph_activations", "graph_deliveries", "graph_join_rounds", "graph_workspaces", "graph_workspace_uses", "graph_notifications"}
	parts := []string{}
	for _, table := range tables {
		parts = append(parts, `SELECT record_id FROM data_`+table)
	}
	if err := v.rejectRows("record identities", `SELECT record_id FROM (`+strings.Join(parts, " UNION ALL ")+`) GROUP BY record_id HAVING record_id='' OR count(*)>1`); err != nil {
		return err
	}
	for _, table := range []string{"topics", "automations", "automation_triggers", "automation_runs", "automation_recoveries"} {
		if err := v.rejectRows(table+" identities", `SELECT record_id FROM data_`+table+` GROUP BY record_id HAVING record_id='' OR count(*)>1`); err != nil {
			return err
		}
	}
	for _, table := range []string{"webhook_connections", "webhook_events", "board_connections"} {
		if err := v.rejectRows(table+" map identity", `SELECT 1 FROM data_`+table+` WHERE map_key<>record_id OR record_id=''`); err != nil {
			return err
		}
	}
	return nil
}

func (v sqliteValidation) validateSessionsAndTopics() error {
	checks := []struct{ label, query string }{
		{"session status", `SELECT 1 FROM data_sessions WHERE status NOT IN ('idle','running','error')`},
		{"session history", `SELECT 1 FROM data_sessions s LEFT JOIN record_collections c ON c.parent_table='sessions' AND c.parent_id=s.id AND c.field='events' WHERE c.kind IS NULL OR c.kind<>'array'`},
		{"general singleton", `SELECT role FROM data_sessions WHERE role='general_agent' GROUP BY role HAVING count(*)>1`},
		{"session project", `SELECT 1 FROM data_sessions s LEFT JOIN data_projects p ON p.id=s.project_id LEFT JOIN data_sessions parent ON parent.id=s.parent_id WHERE p.id IS NULL AND NOT(s.role='general_agent' AND s.project_id='') AND NOT(s.role='subagent' AND s.project_id='' AND coalesce(parent.role,'')='general_agent')`},
		{"linked owner", `SELECT 1 FROM data_sessions s LEFT JOIN data_sessions p ON p.id=s.parent_id LEFT JOIN data_sessions gp ON gp.id=p.parent_id WHERE s.parent_id<>'' AND (p.id IS NULL OR p.x_graphRunId<>'' OR s.x_graphRunId<>'' OR p.project_id<>s.project_id OR s.role NOT IN ('side_agent','subagent') OR (p.parent_id<>'' AND NOT(s.role='subagent' AND p.role='side_agent' AND gp.id IS NOT NULL AND gp.parent_id=''))) AND NOT(s.role='subagent' AND EXISTS(SELECT 1 FROM data_delegation_children d WHERE d.id=s.id))`},
		{"duplicate side agent", `SELECT parent_id FROM data_sessions WHERE role='side_agent' GROUP BY parent_id HAVING count(*)>1`},
		{"native owner", `SELECT 1 FROM data_native_sessions n LEFT JOIN data_sessions s ON s.id=n.map_key WHERE s.id IS NULL`},
		{"topic membership", `SELECT 1 FROM data_sessions s LEFT JOIN data_topics t ON t.record_id=s.x_topicId AND t.owner_id=s.project_id WHERE s.x_topicId<>'' AND (t.id IS NULL OR s.role<>'' OR s.parent_id<>'' OR s.x_graphRunId<>'')`},
		{"topic order", `SELECT 1 FROM data_project_topic_order o LEFT JOIN data_topics t ON t.record_id=o.scalar_value AND t.owner_id=o.owner_id WHERE t.id IS NULL`},
		{"duplicate topic order", `SELECT owner_id,scalar_value FROM data_project_topic_order GROUP BY owner_id,scalar_value HAVING count(*)>1`},
		{"project folders", `SELECT 1 FROM data_projects p LEFT JOIN record_collections c ON c.parent_table='projects' AND c.parent_id=p.id AND c.field='folders' WHERE c.kind IS NULL OR c.kind<>'array'`},
		{"archived folder", `SELECT 1 FROM data_legacy_archived_folders a LEFT JOIN data_legacy_project_folders f ON f.owner_id=a.owner_id AND f.scalar_value=a.scalar_value WHERE f.id IS NULL`},
	}
	for _, c := range checks {
		if err := v.rejectRows(c.label, c.query); err != nil {
			return err
		}
	}
	if _, err := v.tx.ExecContext(v.ctx, `CREATE TEMP TABLE validation_archived_folders(owner TEXT NOT NULL,folded TEXT NOT NULL,PRIMARY KEY(owner,folded))`); err != nil {
		return err
	}
	if err := v.each("legacy_archived_folders", nil, func(id string) error {
		var owner, name string
		if err := v.tx.QueryRowContext(v.ctx, `SELECT owner_id,scalar_value FROM data_legacy_archived_folders WHERE id=?`, id).Scan(&owner, &name); err != nil {
			return err
		}
		_, err := v.tx.ExecContext(v.ctx, `INSERT INTO validation_archived_folders(owner,folded) VALUES(?,?)`, owner, strings.ToLower(name))
		if err != nil {
			return errors.New("duplicate archived folder")
		}
		return nil
	}); err != nil {
		return err
	}
	if _, err := v.tx.ExecContext(v.ctx, `DROP TABLE validation_archived_folders`); err != nil {
		return err
	}
	if err := v.each("topics", nil, func(id string) error {
		var t Topic
		if err := v.get("topics", id, &t); err != nil {
			return err
		}
		name, ok := cleanLabel(t.Name, 60)
		if !ok || name != t.Name || !validTopicColor(t.Color) || t.CreatedAt == "" {
			return errors.New("invalid topic")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := v.each("sessions", nil, func(id string) error {
		s, err := v.session(id)
		if err != nil {
			return err
		}
		if s.Graph != nil {
			return errors.New("persisted public graph projection")
		}
		managed, err := v.exists("delegation_children", s.ID)
		if err != nil {
			return err
		}
		if managed {
			if err := v.validateManagedDelegationChild(s.ID); err != nil {
				return err
			}
		}
		if s.Role == sessionRoleGeneralAgent && (s.ProjectID != "" || s.ParentID != "" || s.GraphRunID != "" || s.SelectedGraphID != "" || s.Archived || !filepath.IsAbs(s.ExecutionCWD)) {
			return errors.New("invalid general conversation")
		}
		if s.GraphRunID == "" && (s.GraphNodeID != "" || s.ExecutionCWD != "" && s.Role != sessionRoleGeneralAgent && !managed || s.Role == "graph_node") {
			return errors.New("invalid private conversation ownership")
		}
		if s.GraphRunID != "" && (s.Role != "graph_node" || s.ParentID != "" || s.SelectedGraphID != "" || !filepath.IsAbs(s.ExecutionCWD)) {
			return errors.New("invalid graph conversation")
		}
		var selection CapabilitySelection
		if err := v.get("capability_selections", sqliteOwnedKey(sqliteOwnedKey(id, ""), ""), &selection); err != nil {
			return err
		}
		return normalizeSelection(&selection)
	}); err != nil {
		return err
	}
	return v.each("permission_grants", nil, func(id string) error {
		var g permissionGrant
		if err := v.get("permission_grants", id, &g); err != nil {
			return err
		}
		if g.ProjectID != "" {
			if exists, err := v.exists("projects", g.ProjectID); err != nil {
				return err
			} else if !exists {
				return errors.New("permission grant project missing")
			}
		}
		if g.SessionID != "" {
			s, err := v.session(g.SessionID)
			if err != nil {
				return err
			}
			if g.ProjectID == "" && s.Role != sessionRoleGeneralAgent || s.ProjectID != g.ProjectID || g.Harness != "" && s.Harness != g.Harness {
				return errors.New("permission grant scope mismatch")
			}
		}
		return nil
	})
}

func (v sqliteValidation) validateCollaboration() error {
	if err := v.rejectRows("duplicate spawn receipt", `SELECT x_from,x_operationId FROM data_session_spawns GROUP BY x_from,x_operationId HAVING count(*)>1`); err != nil {
		return err
	}
	if err := v.rejectRows("duplicate command receipt", `SELECT x_from,x_request_operationId FROM data_session_commands GROUP BY x_from,x_request_operationId HAVING count(*)>1`); err != nil {
		return err
	}
	if err := v.each("session_spawns", nil, func(id string) error {
		var r SessionSpawn
		if err := v.get("session_spawns", id, &r); err != nil {
			return err
		}
		from, err := v.session(r.From)
		if err != nil {
			return err
		}
		to, err := v.session(r.SessionID)
		if err != nil {
			return err
		}
		human, err := v.userEvent(r.From, r.SourceUserEventID)
		if err != nil {
			return err
		}
		if r.OperationID == "" || !spawnSender(&from, r.Request) || to.Role != "" || to.ParentID != "" || to.GraphRunID != "" || !human {
			return errors.New("invalid spawn receipt")
		}
		project := from.ProjectID
		if from.Role == sessionRoleGeneralAgent {
			project = r.Request.ProjectID
		}
		if exists, err := v.exists("projects", project); err != nil {
			return err
		} else if !exists || to.ProjectID != project {
			return errors.New("spawn project mismatch")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := v.each("session_commands", nil, func(id string) error {
		var r SessionCommand
		if err := v.get("session_commands", id, &r); err != nil {
			return err
		}
		from, err := v.session(r.From)
		if err != nil {
			return err
		}
		to, err := v.session(r.Request.SessionID)
		if err != nil {
			return err
		}
		paired := consultationPair(&from, &to) && (from.Role == sessionRoleGeneralAgent || to.ParentID == "" && to.Role == "" || from.ParentID == to.ID || to.ParentID == from.ID)
		if !validSendArgs(r.Request) || r.MessageID == "" || !validGraphTime(r.CreatedAt) || !paired {
			return errors.New("invalid session command")
		}
		if r.Request.SourceUserEventID != "" {
			human, err := v.userEvent(r.From, r.Request.SourceUserEventID)
			if err != nil {
				return err
			}
			if !human {
				return errors.New("command source is not a human event")
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return v.each("consultations", nil, func(id string) error {
		var c Consultation
		if err := v.get("consultations", id, &c); err != nil {
			return err
		}
		from, err := v.session(c.From)
		if err != nil {
			return err
		}
		to, err := v.session(c.To)
		if err != nil {
			return err
		}
		if !consultationPair(&from, &to) || !validLinkedText(c.Question, 32<<10) || !slices.Contains([]string{"queued", "answering", "completed", "failed", "cancelled", "interrupted"}, c.Status) || !slices.Contains([]string{"waiting", "pending", "delivering", "delivered", "failed", "cancelled", "interrupted"}, c.Delivery) {
			return errors.New("invalid consultation")
		}
		_, err = time.Parse(time.RFC3339Nano, c.CreatedAt)
		return err
	})
}
