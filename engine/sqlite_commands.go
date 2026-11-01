package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

func (st *sqliteSessionTx) emptyCollection(parent, owner, field, kind string) error {
	var table string
	if parent == "root" {
		roots, _ := sqliteDomains()
		for _, d := range roots {
			if d.field == field {
				table = d.table
			}
		}
	} else {
		for _, d := range sqliteDomainByTable()[parent].children {
			if d.field == field {
				table = d.table
			}
		}
	}
	if table == "" {
		return errors.New("unknown collection field")
	}
	_, err := st.tx.ExecContext(st.ctx, `INSERT INTO record_collections(parent_table,parent_id,field,child_table,kind,size) VALUES(?,?,?,?,?,0) ON CONFLICT(parent_table,parent_id,field) DO NOTHING`, parent, owner, field, table, kind)
	return err
}

func (st *sqliteSessionTx) project(id string) (Project, error) {
	var p Project
	err := decodeSQLiteRecord(st.ctx, st.tx, "projects", id, 1<<20, &p)
	return p, err
}
func (st *sqliteSessionTx) topic(projectID, id string) (Topic, string, error) {
	var key string
	if err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_topics WHERE owner_id=? AND record_id=?`, projectID, id).Scan(&key); err != nil {
		return Topic{}, "", err
	}
	var topic Topic
	err := decodeSQLiteRecord(st.ctx, st.tx, "topics", key, 64<<10, &topic)
	return topic, key, err
}

func (st *sqliteSessionTx) registerProject(p Project, general bool, restoreID string) (Project, string, error) {
	var existing *Project
	var restorePosition int64 = 1<<63 - 1
	matches := []string{}
	// Explicit project registration consults project metadata only, in bounded
	// batches. It does not load topic arrays, sessions or histories.
	if err := st.eachMatching("projects", "1=1", nil, func(id string) error {
		candidate, err := st.project(id)
		if err != nil {
			return err
		}
		match := sameProjectFolder(candidate.Folder, p.Folder)
		if general {
			match = sameProjectFolder(filepath.Clean(candidate.Folder), p.Folder)
		}
		if restoreID != "" {
			match = id == restoreID
		}
		if match && (general || candidate.Removed) {
			if general {
				matches = append(matches, id)
			}
			var position int64
			if !general {
				if err := st.tx.QueryRowContext(st.ctx, `SELECT position FROM data_projects WHERE id=?`, id).Scan(&position); err != nil {
					return err
				}
			}
			if existing == nil || !general && position < restorePosition {
				copy := candidate
				existing = &copy
				restorePosition = position
			}
		}
		return nil
	}); err != nil {
		return Project{}, "", err
	}
	if general && len(matches) > 1 {
		return Project{}, "", controlError(409, fmt.Sprintf("Multiple registered projects match this path (project IDs: %s). Resolve the conflicting registrations before adding this path; no project was changed.", strings.Join(matches, ", ")))
	}
	if general && existing != nil && !existing.Removed {
		return *existing, "existing", nil
	}
	outcome := "created"
	if existing != nil {
		p.ID = existing.ID
		outcome = "restored"
	}
	p.Topics, p.TopicOrder, p.Folders, p.ArchivedFolders = nil, nil, nil, nil
	p.Removed = false
	if err := st.putRoot("projects", p.ID, p); err != nil {
		return Project{}, "", err
	}
	if existing == nil {
		for _, field := range []string{"topics", "folders", "archivedFolders"} {
			if err := st.emptyCollection("projects", p.ID, field, "array"); err != nil {
				return Project{}, "", err
			}
		}
	}
	return p, outcome, nil
}

func (st *sqliteSessionTx) createSession(s Session, dataDir string) error {
	if s.ID == "" {
		return errors.New("session requires identity")
	}
	var exists bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_sessions WHERE id=?)`, s.ID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return controlError(409, "Session already exists.")
	}
	engineOwnedChild := false
	if s.Role == sessionRoleSubagent && s.ProjectID == "" {
		parent, err := st.sessionMeta(s.ParentID)
		if err != nil {
			return err
		}
		engineOwnedChild = parent.Role == sessionRoleGeneralAgent
	}
	if s.Role != sessionRoleGeneralAgent && !engineOwnedChild {
		active, err := st.projectAvailable(s.ProjectID)
		if err != nil {
			return err
		}
		if !active {
			return controlError(404, "Project not found.")
		}
	}
	if s.TopicID != "" {
		topic, _, err := st.topic(s.ProjectID, s.TopicID)
		if err != nil || topic.Archived {
			return controlError(400, "Choose an existing active topic in the same project.")
		}
	}
	caps := s.Capabilities
	s.Events, s.Queue, s.Sources, s.Permissions, s.Questions, s.Usage = nil, nil, nil, nil, nil, nil
	s.Capabilities = ConversationCapabilities{}
	if err := st.putRoot("sessions", s.ID, s); err != nil {
		return err
	}
	for _, field := range []string{"events", "queue", "sources", "permissions", "questions"} {
		if err := st.emptyCollection("sessions", s.ID, field, "array"); err != nil {
			return err
		}
	}
	if err := st.putSingleChild("session_capabilities", s.ID, ConversationCapabilities{Revision: caps.Revision}); err != nil {
		return err
	}
	capKey := sqliteOwnedKey(s.ID, "")
	if err := st.putSingleChild("capability_selections", capKey, copySelection(caps.Desired)); err != nil {
		return err
	}
	if err := st.putSingleChild("capability_applied", capKey, caps.Applied); err != nil {
		return err
	}
	if err := st.putSingleChild("capability_preparing", capKey, nil); err != nil {
		return err
	}
	if err := st.putSingleChild("session_usage", s.ID, nil); err != nil {
		return err
	}
	if s.Harness == "pi" {
		return st.putRoot("native_sessions", s.ID, nativeSession{Path: filepath.Join(dataDir, "sessions", s.ID+".jsonl")})
	}
	return nil
}

func (st *sqliteSessionTx) effectivelyArchived(s Session) (bool, error) {
	if s.Archived {
		return true, nil
	}
	if s.Role == sessionRoleSideAgent {
		p, err := st.sessionMeta(s.ParentID)
		if err != nil {
			return false, err
		}
		return st.effectivelyArchived(p)
	}
	if s.TopicID == "" {
		return false, nil
	}
	topic, _, err := st.topic(s.ProjectID, s.TopicID)
	return topic.Archived, err
}

func (st *sqliteSessionTx) conversationDirectory(s Session) (string, error) {
	if s.Role == sessionRoleGeneralAgent {
		return s.ExecutionCWD, nil
	}
	if s.Role == sessionRoleSubagent && s.ProjectID == "" {
		parent, err := st.sessionMeta(s.ParentID)
		if err != nil {
			return "", err
		}
		if parent.Role != sessionRoleGeneralAgent {
			return "", controlError(404, "Conversation directory unavailable.")
		}
		return parent.ExecutionCWD, nil
	}
	p, err := st.project(s.ProjectID)
	if err != nil {
		return "", err
	}
	if p.Removed {
		return "", controlError(404, "Project not found.")
	}
	return p.Folder, nil
}

func (a *app) readSQLConversation(ctx context.Context, id string) (Session, string, error) {
	var s Session
	var cwd string
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		s, err = st.sessionMeta(id)
		if err != nil {
			return err
		}
		cwd, err = st.conversationDirectory(s)
		return err
	})
	return s, cwd, err
}

func (st *sqliteSessionTx) generalInputAccepted(id string) (bool, error) {
	var accepted bool
	err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_queued_messages WHERE owner_id=?) OR EXISTS(SELECT 1 FROM accepted_message_ids WHERE session_id=?) OR EXISTS(
 SELECT 1 FROM data_session_events e WHERE e.owner_id=? AND (e.kind<>'status' OR (e.x_data_automationRecoveryNotice<>'true' AND NOT EXISTS(SELECT 1 FROM data_automation_recoveries r WHERE r.x_noticeEventId=e.record_id))))`, id, id, id).Scan(&accepted)
	return accepted, err
}

func (a *app) createSQLSideLocked(ctx context.Context, mainID, harness string) (string, bool, error) {
	var id string
	created := false
	err := a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		main, err := st.sessionMeta(mainID)
		if err != nil {
			return err
		}
		if main.Role == sessionRoleGeneralAgent || main.ParentID != "" || main.GraphRunID != "" {
			return controlError(404, "Main conversation not found.")
		}
		available, err := st.projectAvailable(main.ProjectID)
		if err != nil {
			return err
		}
		if !available {
			return controlError(404, "Project not found.")
		}
		err = st.tx.QueryRowContext(ctx, `SELECT id FROM data_sessions WHERE parent_id=? AND role='side_agent' LIMIT 1`, mainID).Scan(&id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var pending bool
		if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return controlError(409, "A library deletion needs recovery before inheriting capabilities.")
		}
		model, effort := main.Model, main.Effort
		if harness == "" {
			harness = main.Harness
		} else if harness != main.Harness {
			model, effort = "", ""
		}
		if _, ok := a.binaries[harness]; !ok {
			return controlError(400, "Harness is not installed.")
		}
		var applied *CapabilitySnapshot
		capKey := sqliteOwnedKey(mainID, "")
		if err := readSQLCapabilityView(ctx, st.tx, "capability_applied", sqliteOwnedKey(capKey, ""), &applied); err != nil {
			return err
		}
		var selection CapabilitySelection
		if applied != nil {
			selection = copySelection(applied.Selection)
		} else if err := decodeSQLiteRecord(ctx, st.tx, "capability_selections", sqliteOwnedKey(capKey, ""), 8<<20, &selection); err != nil {
			return err
		}
		s := Session{ID: newID(), ParentID: mainID, ProjectID: main.ProjectID, Title: "Side agent", Workspace: main.Workspace, Role: sessionRoleSideAgent, Harness: harness, Model: model, Effort: effort, Status: "idle", CreatedAt: now(), UpdatedAt: now(), Capabilities: ConversationCapabilities{Desired: selection}}
		id, created = s.ID, true
		return st.createSession(s, a.dir)
	})
	return id, created, err
}

func (a *app) changeSQLHarnessLocked(ctx context.Context, id, harness string) error {
	return a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		s, err := st.sessionMeta(id)
		if err != nil {
			return err
		}
		if s.Role != sessionRoleSideAgent && s.Role != sessionRoleGeneralAgent {
			return controlError(404, "Conversation not found.")
		}
		if binding, err := st.agentBinding(id); err != nil {
			return err
		} else if binding != nil {
			return controlError(409, "A saved agent session's harness is fixed at creation.")
		}
		var accepted bool
		if s.Role == sessionRoleGeneralAgent {
			accepted, err = st.generalInputAccepted(id)
		} else {
			err = st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_session_events WHERE owner_id=?) OR EXISTS(SELECT 1 FROM data_queued_messages WHERE owner_id=?)`, id, id).Scan(&accepted)
		}
		if err != nil {
			return err
		}
		if s.HarnessLocked || accepted || a.runs[id] != nil {
			return controlError(409, "Harness can only change before the first turn.")
		}
		if _, ok := a.binaries[harness]; !ok {
			return controlError(400, "Harness is not installed.")
		}
		s.Harness, s.Model, s.Effort, s.ResolvedModel, s.ResolvedEffort = harness, "", "", "", ""
		s.UpdatedAt = now()
		if err := st.putSessionMeta(s); err != nil {
			return err
		}
		if err := st.deleteRecord("native_sessions", id); err != nil {
			return err
		}
		if harness == "pi" {
			return st.putRoot("native_sessions", id, nativeSession{Path: filepath.Join(a.dir, "sessions", id+".jsonl")})
		}
		return nil
	})
}

func (st *sqliteSessionTx) moveSessionTopic(s *Session, topicID string) error {
	if s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
		return controlError(400, "Only normal top-level sessions can change topic.")
	}
	available, err := st.projectAvailable(s.ProjectID)
	if err != nil {
		return err
	}
	if !available {
		return controlError(404, "Project not found.")
	}
	archived, err := st.effectivelyArchived(*s)
	if err != nil {
		return err
	}
	if archived {
		return controlError(409, "Restore the session and its topic before moving it.")
	}
	topic, _, err := st.topic(s.ProjectID, topicID)
	if err != nil || topic.Archived {
		return controlError(400, "Choose an existing active topic in the same project.")
	}
	s.TopicID = topic.ID
	return nil
}

func (st *sqliteSessionTx) patchSessionLabels(id string, title, topicID *string, archived *bool, origin *SpawnOrigin) error {
	s, err := st.sessionMeta(id)
	if err != nil {
		return err
	}
	if s.Role == sessionRoleGeneralAgent {
		return controlError(400, "The general conversation cannot be renamed, moved, reordered or archived.")
	}
	if s.GraphRunID != "" || s.Role == sessionRoleSubagent {
		return controlError(404, "Conversation not found.")
	}
	if title != nil {
		name, ok := cleanLabel(*title, 200)
		if !ok {
			return controlError(400, "Session title must contain 1 to 200 characters.")
		}
		s.Title = name
	}
	if archived != nil {
		if s.Role != "" || s.ParentID != "" {
			return controlError(400, "Only normal top-level sessions can be archived or restored.")
		}
		if !*archived && s.TopicID != "" {
			topic, _, err := st.topic(s.ProjectID, s.TopicID)
			if err != nil {
				return err
			}
			if topic.Archived {
				return controlError(409, "Restore the archived topic first; session membership is preserved.")
			}
		}
		s.Archived = *archived
	}
	if topicID != nil {
		if archived != nil {
			return controlError(400, "Move and archive/restore the session in separate requests.")
		}
		if err := st.moveSessionTopic(&s, *topicID); err != nil {
			return err
		}
	}
	s.UpdatedAt = now()
	if err := st.putSessionMeta(s); err != nil {
		return err
	}
	if origin != nil && archived != nil {
		temp := Session{}
		appendSessionControl(&temp, "Session archive state updated", origin, map[string]any{"archived": *archived, "folder": s.Workspace})
		_, err = st.appendEvent(id, temp.Events[0])
	}
	return err
}

const sqlTopicOrder = `SELECT t.record_id,row_number() OVER(ORDER BY CASE WHEN o.position IS NULL THEN 0 ELSE 1 END,o.position,coalesce((SELECT max(s.created_at) FROM data_sessions s WHERE s.project_id=t.owner_id AND s.x_topicId=t.record_id AND s.role='' AND s.parent_id='' AND s.x_graphRunId=''),'') DESC,t.record_id)-1
 FROM data_topics t LEFT JOIN data_project_topic_order o ON o.owner_id=t.owner_id AND o.scalar_value=t.record_id WHERE t.owner_id=? AND t.record_id<>?`

// Manual ordering touches only SQL metadata for this project; no Go inventory.
func (st *sqliteSessionTx) orderTopic(projectID, id, beforeID string, first bool) error {
	if _, err := st.tx.ExecContext(st.ctx, `CREATE TEMP TABLE topic_reorder(id TEXT PRIMARY KEY,position INTEGER NOT NULL)`); err != nil {
		return err
	}
	defer st.tx.ExecContext(st.ctx, `DROP TABLE IF EXISTS topic_reorder`)
	if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO topic_reorder `+sqlTopicOrder, projectID, id); err != nil {
		return err
	}
	var at int64
	if first {
		at = 0
	} else if beforeID != "" {
		if err := st.tx.QueryRowContext(st.ctx, `SELECT position FROM topic_reorder WHERE id=?`, beforeID).Scan(&at); err != nil {
			return controlError(400, "Topic position target not found.")
		}
	} else if err := st.tx.QueryRowContext(st.ctx, `SELECT count(*) FROM topic_reorder`).Scan(&at); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `UPDATE topic_reorder SET position=position+1 WHERE position>=?`, at); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO topic_reorder VALUES(?,?)`, id, at); err != nil {
		return err
	}
	if err := st.clearChildren("project_topic_order", projectID); err != nil {
		return err
	}
	var last int64 = -1
	for {
		var topic string
		var position int64
		err := st.tx.QueryRowContext(st.ctx, `SELECT id,position FROM topic_reorder WHERE position>? ORDER BY position LIMIT 1`, last).Scan(&topic, &position)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		key := strconv.FormatInt(position, 10)
		if err := st.putRecord("project_topic_order", sqliteOwnedKey(projectID, key), projectID, key, position, topic); err != nil {
			return err
		}
		last = position
	}
	st.changes.Projects[projectID] = true
	if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_projects SET revision=? WHERE id=?`, st.changes.Revision, projectID); err != nil {
		return err
	}
	return nil
}

func (st *sqliteSessionTx) createTopic(projectID, name, color, sessionID string) (Topic, error) {
	available, err := st.projectAvailable(projectID)
	if err != nil {
		return Topic{}, err
	}
	if !available {
		return Topic{}, controlError(404, "Project not found.")
	}
	t := Topic{ID: newID(), Name: name, Color: color, CreatedAt: now()}
	if err := st.putRecord("topics", sqliteOwnedKey(projectID, t.ID), projectID, t.ID, -1, t); err != nil {
		return Topic{}, err
	}
	if err := st.orderTopic(projectID, t.ID, "", true); err != nil {
		return Topic{}, err
	}
	if sessionID != "" {
		s, err := st.sessionMeta(sessionID)
		if err != nil {
			return Topic{}, err
		}
		if s.ProjectID != projectID {
			return Topic{}, controlError(404, "Session not found in this project.")
		}
		if err := st.moveSessionTopic(&s, t.ID); err != nil {
			return Topic{}, err
		}
		s.UpdatedAt = now()
		if err := st.putSessionMeta(s); err != nil {
			return Topic{}, err
		}
	}
	return t, nil
}

func (a *app) registerSQLProjectLocked(ctx context.Context, p Project, general bool, restoreID string) (Project, string, error) {
	var result Project
	var outcome string
	err := a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		var err error
		result, outcome, err = st.registerProject(p, general, restoreID)
		return err
	})
	return result, outcome, err
}

func (st *sqliteSessionTx) patchProject(id string, name, icon, before *string) (Project, error) {
	p, err := st.project(id)
	if err != nil {
		return p, err
	}
	if p.Removed {
		return p, sql.ErrNoRows
	}
	if name != nil {
		p.Name = *name
	}
	if icon != nil {
		p.Icon = *icon
	}
	if before != nil && *before != "" {
		target, err := st.project(*before)
		if err != nil || target.Removed {
			return p, controlError(400, "Position target must be an existing project.")
		}
	}
	if err := st.putRoot("projects", id, p); err != nil {
		return p, err
	}
	if before != nil && *before != id {
		if _, err := st.tx.ExecContext(st.ctx, `CREATE TEMP TABLE project_reorder(id TEXT PRIMARY KEY,position INTEGER NOT NULL)`); err != nil {
			return p, err
		}
		defer st.tx.ExecContext(st.ctx, `DROP TABLE IF EXISTS project_reorder`)
		if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO project_reorder SELECT id,row_number() OVER(ORDER BY position)-1 FROM data_projects WHERE id<>?`, id); err != nil {
			return p, err
		}
		var at int64
		if *before == "" {
			err = st.tx.QueryRowContext(st.ctx, `SELECT count(*) FROM project_reorder`).Scan(&at)
		} else {
			err = st.tx.QueryRowContext(st.ctx, `SELECT position FROM project_reorder WHERE id=?`, *before).Scan(&at)
		}
		if err != nil {
			return p, err
		}
		if _, err := st.tx.ExecContext(st.ctx, `UPDATE project_reorder SET position=position+1 WHERE position>=?`, at); err != nil {
			return p, err
		}
		if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO project_reorder VALUES(?,?)`, id, at); err != nil {
			return p, err
		}
		var offset int64
		if err := st.tx.QueryRowContext(st.ctx, `SELECT coalesce(max(position),0)+1 FROM data_projects`).Scan(&offset); err != nil {
			return p, err
		}
		if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_projects SET position=position+?`, offset); err != nil {
			return p, err
		}
		if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_projects SET position=(SELECT position FROM project_reorder WHERE id=data_projects.id)`); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (a *app) removeSQLProjectLocked(ctx context.Context, id string) error {
	return a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		p, err := st.project(id)
		if err != nil {
			return err
		}
		if p.Removed {
			return nil
		}
		for sessionID := range a.runs {
			var project string
			if err := st.tx.QueryRowContext(ctx, `SELECT project_id FROM data_sessions WHERE id=?`, sessionID).Scan(&project); err != nil {
				return err
			}
			if project == id {
				return controlError(409, "Stop running sessions before removing this project.")
			}
		}
		var busy bool
		if err := st.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_runs WHERE project_id=? AND status IN ('starting','running','ending')) OR EXISTS(SELECT 1 FROM data_sessions WHERE project_id=? AND status='running')`, id, id).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return controlError(409, "Project has active work.")
		}
		p.Removed = true
		if err := st.putRoot("projects", id, p); err != nil {
			return err
		}
		if err := st.eachMatching("board_connections", `project_id=?`, []any{id}, func(connection string) error {
			var sync activityBoardSync
			err := decodeSQLiteRecord(ctx, st.tx, "board_sync", connection, 64<<10, &sync)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			sync.Stale = true
			return st.putRoot("board_sync", connection, sync)
		}); err != nil {
			return err
		}
		var config activityBoardConfig
		if err := decodeSQLiteRecord(ctx, st.tx, "board_config", "settings", 64<<10, &config); err != nil {
			return err
		}
		if config.Projection == ^uint64(0) {
			return errors.New("board projection exhausted")
		}
		config.Projection++
		return st.putRoot("board_config", "settings", config)
	})
}
