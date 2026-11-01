package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

func (s legacyStage) value(path []string, target any) error {
	id, _, err := s.resolve(path)
	if err != nil {
		return err
	}
	return s.decodeNode(id, target)
}

func (s legacyStage) nonnilCollection(path []string, empty any) error {
	id, _, err := s.resolve(path)
	if err != nil {
		return err
	}
	n, err := s.node(id)
	if err != nil {
		return err
	}
	if n.kind == "scalar" && string(n.scalar) == "null" {
		return s.set(path, empty)
	}
	return nil
}

// These are the old loadState compatibility transforms, before journal replay.
// Source bytes are never rewritten. Each query touches metadata, one selection,
// or one exact event marker; a session's history is never decoded as a slice.
// Domain/ownership validation is a distinct gate, not implied by normalization.
func normalizeLegacyCheckpoint(ctx context.Context, store *sqliteStore) error {
	return store.write(ctx, func(tx *sql.Tx) error {
		s := legacyStage{ctx: ctx, tx: tx}
		var revision, view uint64
		var format int
		if err := s.value([]string{"graphRevision"}, &revision); err != nil {
			return err
		}
		if err := s.value([]string{"graphViewRevision"}, &view); err != nil {
			return err
		}
		if err := s.value([]string{"journalFormat"}, &format); err != nil {
			return err
		}
		if format != 0 && format != 1 {
			return errors.New("unsupported legacy journal format")
		}
		// encoding/json accepts null for these scalar Go fields as zero. Store
		// their typed value, never the literal "null" as a SQL decimal revision.
		if err := s.set([]string{"graphRevision"}, revision); err != nil {
			return err
		}
		if err := s.set([]string{"journalFormat"}, format); err != nil {
			return err
		}
		for _, field := range []string{"webhookReceivedSequence", "webhookReleaseSequence"} {
			var value uint64
			if err := s.value([]string{field}, &value); err != nil {
				return err
			}
			if err := s.set([]string{field}, value); err != nil {
				return err
			}
		}
		if view == 0 {
			if err := s.set([]string{"graphViewRevision"}, revision); err != nil {
				return err
			}
		}
		if err := s.set([]string{"version"}, 2); err != nil {
			return err
		}
		var model string
		if err := s.value([]string{"transcription", "model"}, &model); err != nil {
			return err
		}
		if model == "" {
			if err := s.set([]string{"transcription", "model"}, defaultTranscriptionModel); err != nil {
				return err
			}
		}
		if err := s.nonnilCollection([]string{"transcription", "vocabulary"}, []TranscriptionVocabularyEntry{}); err != nil {
			return err
		}
		projects, err := s.child(1, "projects")
		if err != nil {
			return err
		}
		if err := s.children(projects, func(c legacyChild) error {
			return s.nonnilCollection([]string{"projects", strconv.FormatInt(c.position, 10), "archivedFolders"}, []string{})
		}); err != nil {
			return err
		}
		sessions, err := s.child(1, "sessions")
		if err != nil {
			return err
		}
		if err := s.children(sessions, func(c legacyChild) error {
			base := []string{"sessions", strconv.FormatInt(c.position, 10)}
			var selection CapabilitySelection
			if err := s.value(append(patchPath(base, "capabilities"), "desired"), &selection); err != nil {
				return err
			}
			if err := normalizeSelection(&selection); err != nil {
				return err
			}
			if err := s.set(append(patchPath(base, "capabilities"), "desired"), selection); err != nil {
				return err
			}
			if err := s.set(append(patchPath(base, "capabilities"), "preparing"), nil); err != nil {
				return err
			}
			role, err := s.stringField(c.id, "role")
			if err != nil {
				return err
			}
			parent, err := s.stringField(c.id, "parentId")
			if err != nil {
				return err
			}
			if role == "" && parent != "" {
				if err := s.set(patchPath(base, "role"), sessionRoleSideAgent); err != nil {
					return err
				}
			}
			if role == sessionRoleGeneralAgent {
				return s.normalizeGeneralLock(c.id, base)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, collection := range []string{"graphActivities", "graphRuns"} {
			id, _, err := s.resolve([]string{collection})
			if err != nil {
				return err
			}
			if err := s.children(id, func(c legacyChild) error {
				path := []string{collection, strconv.FormatInt(c.position, 10), "workspace", "mode"}
				var mode string
				if err := s.value(path, &mode); err != nil {
					return err
				}
				if mode == "" {
					return s.set(path, "original")
				}
				return nil
			}); err != nil {
				return err
			}
		}
		for _, field := range []string{"webhookConnections", "webhookEvents", "webhookPending", "webhookReleases", "activityBoardConnections", "activityBoardCredentials", "activityBoardItems", "activityBoardSync", "activityBoardMoves"} {
			if err := s.nonnilCollection([]string{field}, map[string]any{}); err != nil {
				return err
			}
		}
		var boardSchema int
		if err := s.value([]string{"activityBoardConfig", "schema"}, &boardSchema); err != nil {
			return err
		}
		if boardSchema == 0 {
			if err := s.set([]string{"activityBoardConfig", "schema"}, 1); err != nil {
				return err
			}
		}
		return s.nonnilCollection([]string{"activityBoardConfig", "phases"}, []activityBoardPhase{})
	})
}

func (s legacyStage) normalizeGeneralLock(session int64, base []string) error {
	id, err := s.stringField(session, "id")
	if err != nil {
		return err
	}
	queue, _, err := s.resolve(patchPath(base, "queue"))
	if err != nil {
		return err
	}
	var hasQueue, hasAcceptance bool
	if err := s.tx.QueryRowContext(s.ctx, `SELECT EXISTS(SELECT 1 FROM legacy_nodes WHERE parent=?)`, queue).Scan(&hasQueue); err != nil {
		return err
	}
	accepted, _, err := s.resolve([]string{"acceptedMessages"})
	if err != nil {
		return err
	}
	prefix := id + "/"
	if err := s.tx.QueryRowContext(s.ctx, `SELECT EXISTS(SELECT 1 FROM legacy_nodes WHERE parent=? AND substr(name,1,?)=?)`, accepted, len(prefix), prefix).Scan(&hasAcceptance); err != nil {
		return err
	}
	if hasQueue || hasAcceptance {
		return s.set(patchPath(base, "harnessLocked"), true)
	}
	events, _, err := s.resolve(patchPath(base, "events"))
	if err != nil {
		return err
	}
	hasEvent, hasInput := false, false
	if err := s.children(events, func(c legacyChild) error {
		hasEvent = true
		typ, err := s.stringField(c.id, "type")
		if err != nil {
			return err
		}
		if typ != "status" {
			hasInput = true
			return nil
		}
		var notice bool
		data, err := s.child(c.id, "data")
		if err == nil {
			marker, err := s.child(data, "automationRecoveryNotice")
			if err == nil {
				n, err := s.node(marker)
				if err != nil {
					return err
				}
				notice = string(n.scalar) == "true"
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !notice {
			eventID, err := s.stringField(c.id, "id")
			if err != nil {
				return err
			}
			encoded, _ := json.Marshal(eventID)
			if err := s.tx.QueryRowContext(s.ctx, `SELECT EXISTS(SELECT 1 FROM legacy_nodes root
 JOIN legacy_nodes recovery ON recovery.parent=root.id
 JOIN legacy_nodes marker ON marker.parent=recovery.id AND marker.name='noticeEventId'
 WHERE root.parent=1 AND root.name='automationRecoveries' AND marker.scalar=?)`, encoded).Scan(&notice); err != nil {
				return err
			}
		}
		if !notice {
			hasInput = true
		}
		return nil
	}); err != nil {
		return err
	}
	if hasInput || hasEvent {
		return s.set(patchPath(base, "harnessLocked"), hasInput)
	}
	return nil // An opaque prior lock without history stays locked.
}

// Folder-to-topic migration runs AFTER both journals. SQL keeps the name->ID map,
// including removed projects and archived folders, without loading all sessions.
func migrateLegacyStageTopics(ctx context.Context, store *sqliteStore) error {
	return store.write(ctx, func(tx *sql.Tx) error {
		s := legacyStage{ctx: ctx, tx: tx}
		var schema int
		if err := s.value([]string{"topicSchema"}, &schema); err != nil {
			return err
		}
		if schema == 1 {
			return nil
		}
		if schema != 0 {
			return errors.New("unsupported topic schema")
		}
		if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE legacy_topic_names(project TEXT NOT NULL,name TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(project,name))`); err != nil {
			return err
		}
		projects, err := s.child(1, "projects")
		if err != nil {
			return err
		}
		err = s.children(projects, func(project legacyChild) error {
			pid, err := s.stringField(project.id, "id")
			if err != nil {
				return err
			}
			base := []string{"projects", strconv.FormatInt(project.position, 10)}
			if err := s.set(patchPath(base, "topics"), []Topic{}); err != nil {
				return err
			}
			archived, _, err := s.resolve(patchPath(base, "archivedFolders"))
			if err != nil {
				return err
			}
			add := func(name string) (string, error) {
				if name == "" || name == "Ungrouped" {
					return "", nil
				}
				var id string
				err := tx.QueryRowContext(ctx, `SELECT id FROM legacy_topic_names WHERE project=? AND name=?`, pid, name).Scan(&id)
				if err == nil {
					return id, nil
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return "", err
				}
				var isArchived bool
				encoded, _ := json.Marshal(name)
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM legacy_nodes WHERE parent=? AND scalar=?)`, archived, encoded).Scan(&isArchived); err != nil {
					return "", err
				}
				id = newID()
				if err := s.append(patchPath(base, "topics"), Topic{ID: id, Name: name, Color: "neutral", CreatedAt: now(), Archived: isArchived}); err != nil {
					return "", err
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO legacy_topic_names(project,name,id) VALUES(?,?,?)`, pid, name, id)
				return id, err
			}
			folders, _, err := s.resolve(patchPath(base, "folders"))
			if err != nil {
				return err
			}
			if err := s.children(folders, func(c legacyChild) error {
				var name string
				if err := s.decodeNode(c.id, &name); err != nil {
					return err
				}
				_, err := add(name)
				return err
			}); err != nil {
				return err
			}
			sessions, err := s.child(1, "sessions")
			if err != nil {
				return err
			}
			return s.children(sessions, func(session legacyChild) error {
				projectID, err := s.stringField(session.id, "projectId")
				if err != nil {
					return err
				}
				if projectID != pid {
					return nil
				}
				for _, field := range []string{"role", "parentId", "graphRunId"} {
					value, err := s.stringField(session.id, field)
					if err != nil {
						return err
					}
					if value != "" {
						return nil
					}
				}
				name, err := s.stringField(session.id, "workspace")
				if err != nil {
					return err
				}
				id, err := add(name)
				if err != nil {
					return err
				}
				return s.set([]string{"sessions", strconv.FormatInt(session.position, 10), "topicId"}, id)
			})
		})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE legacy_topic_names`); err != nil {
			return err
		}
		return s.set([]string{"topicSchema"}, 1)
	})
}

// Complete post-replay compatibility without changing the distinct historical
// GraphViewRevision. In particular do NOT rerun the checkpoint revision fallback.
func normalizeLegacyPostReplay(ctx context.Context, store *sqliteStore) error {
	return store.write(ctx, func(tx *sql.Tx) error {
		s := legacyStage{ctx: ctx, tx: tx}
		if _, _, err := s.resolve([]string{"engineSettings"}); err != nil {
			return err
		}
		for _, collection := range []string{"graphActivities", "graphRuns"} {
			id, _, err := s.resolve([]string{collection})
			if err != nil {
				return err
			}
			if err := s.children(id, func(c legacyChild) error {
				path := []string{collection, strconv.FormatInt(c.position, 10), "workspace", "mode"}
				var mode string
				if err := s.value(path, &mode); err != nil {
					return err
				}
				if mode == "" {
					return s.set(path, "original")
				}
				return nil
			}); err != nil {
				return err
			}
		}
		id, err := s.child(1, "sessions")
		if err != nil {
			return err
		}
		return s.children(id, func(c legacyChild) error {
			base := []string{"sessions", strconv.FormatInt(c.position, 10)}
			if err := s.set(append(patchPath(base, "capabilities"), "preparing"), nil); err != nil {
				return err
			}
			role, err := s.stringField(c.id, "role")
			if err != nil {
				return err
			}
			if role == sessionRoleGeneralAgent {
				return s.normalizeGeneralLock(c.id, base)
			}
			return nil
		})
	})
}
