package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

type sqliteSessionChanges struct {
	publishedContent []string
	Revision         string
	Sessions         map[string][]int64 // exact affected ordinals; metadata-only is an empty list
	Records          map[string][]string
	Projects         map[string]bool
	Invalidated      map[string]bool // bounded set of domains when individual IDs exceed the change budget
}

type sqliteSessionTx struct {
	ctx     context.Context
	tx      *sql.Tx
	changes sqliteSessionChanges
}

// Runtime entry point for an explicit related-record write set. No diskState,
// all-session clone/diff, history hydration or notifications inside this gate.
// The returned changes may be published ONLY when this method returns nil error.
func (s *sqliteStore) sessionTransaction(ctx context.Context, change func(*sqliteSessionTx) error) (sqliteSessionChanges, error) {
	changes := sqliteSessionChanges{Sessions: map[string][]int64{}, Records: map[string][]string{}, Projects: map[string]bool{}, Invalidated: map[string]bool{}}
	err := s.write(ctx, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='revision'`).Scan(&current); err != nil {
			return err
		}
		revision, err := strconv.ParseUint(current, 10, 64)
		if err != nil || revision == ^uint64(0) {
			return errors.New("SQLite revision exhausted or invalid")
		}
		changes.Revision = sqliteRevision(revision + 1)
		st := &sqliteSessionTx{ctx: ctx, tx: tx, changes: changes}
		if err := change(st); err != nil {
			return err
		}
		changes.publishedContent = st.changes.publishedContent
		if err := st.validateDelegationChanges(); err != nil {
			return err
		}
		if len(st.changes.Sessions) == 0 && len(st.changes.Records) == 0 && len(st.changes.Invalidated) == 0 {
			changes.Revision = current
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE store_meta SET value=? WHERE key='revision'`, changes.Revision); err != nil {
			return err
		}
		for id := range changes.Sessions {
			if _, err := tx.ExecContext(ctx, `UPDATE data_sessions SET revision=? WHERE id=?`, changes.Revision, id); err != nil {
				return err
			}
			var project string
			if err := tx.QueryRowContext(ctx, `SELECT project_id FROM data_sessions WHERE id=?`, id).Scan(&project); err != nil {
				return err
			}
			if project != "" {
				changes.Projects[project] = true
			}
		}
		return nil
	})
	if err != nil {
		return sqliteSessionChanges{}, err
	}
	for _, id := range changes.publishedContent {
		s.setContentLive(id, false)
		s.releaseContentStage(id)
	}
	return changes, nil
}

func decodeSQLiteRecord(ctx context.Context, q sqliteQuery, table, id string, maxBytes int64, target any) error {
	if !sqliteKnownTable(table) {
		return errors.New("unknown SQLite domain")
	}
	var size int64
	if err := q.QueryRowContext(ctx, `SELECT payload_bytes FROM data_`+table+` WHERE id=?`, id).Scan(&size); err != nil {
		return err
	}
	if size > maxBytes {
		return errors.New("record requires bounded content continuation")
	}
	var body bytes.Buffer
	if err := writeSQLiteRecordContent(ctx, q, &body, table, id); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body.Bytes()))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func (st *sqliteSessionTx) sessionMeta(id string) (Session, error) {
	var s Session
	err := decodeSQLiteRecord(st.ctx, st.tx, "sessions", id, 1<<20, &s)
	return s, err
}

func (s *sqliteStore) readSessionMeta(ctx context.Context, id string) (Session, error) {
	var result Session
	if s.readers == nil {
		return result, errors.New("SQLite readers unavailable")
	}
	tx, err := s.readers.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err := decodeSQLiteRecord(ctx, tx, "sessions", id, 1<<20, &result); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func sqliteSessionMetadataJSON(meta Session) ([]byte, error) {
	if meta.AgentBinding != nil || meta.Events != nil || meta.Queue != nil || meta.Sources != nil || meta.Permissions != nil || meta.Questions != nil || meta.Usage != nil || !reflect.DeepEqual(meta.Capabilities, ConversationCapabilities{}) {
		return nil, errors.New("session metadata cannot contain history or operational collections")
	}
	meta.Graph, meta.RuntimeActive = nil, false // runtime projections are not durable facts.
	encoded, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	for _, name := range []string{"events", "queue", "sources", "permissions", "questions", "usage", "capabilities", "agentBinding"} {
		delete(fields, name)
	}
	return json.Marshal(fields)
}

func (st *sqliteSessionTx) putSessionMeta(meta Session) error {
	if err := st.validateManagedSessionUpdate(meta); err != nil {
		return err
	}
	encoded, err := sqliteSessionMetadataJSON(meta)
	if err != nil {
		return err
	}
	if len(encoded) > 1<<20 {
		return errors.New("session metadata exceeds supported size")
	}
	var project, title, topic, archived, created, role, parent string
	if err := st.tx.QueryRowContext(st.ctx, `SELECT project_id,x_title,x_topicId,x_archived,created_at,role,parent_id FROM data_sessions WHERE id=?`, meta.ID).Scan(&project, &title, &topic, &archived, &created, &role, &parent); err != nil {
		return err
	}
	listChanged := project != meta.ProjectID || title != meta.Title || topic != meta.TopicID || (archived == "true") != meta.Archived || created != sqliteTimeKey(meta.CreatedAt) || role != meta.Role || parent != meta.ParentID
	result, err := st.tx.ExecContext(st.ctx, `UPDATE data_sessions SET project_id=?,parent_id=?,status=?,role=?,created_at=?,updated_at=?,revision=? WHERE id=?`,
		meta.ProjectID, meta.ParentID, meta.Status, meta.Role, sqliteTimeKey(meta.CreatedAt), sqliteTimeKey(meta.UpdatedAt), st.changes.Revision, meta.ID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return errors.New("session metadata target not found")
	}
	if err := updateSQLiteIndexFields(st.ctx, st.tx, "sessions", meta.ID, encoded); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM data_sessions_chunks WHERE record_key=?`, meta.ID); err != nil {
		return err
	}
	w := newSQLiteChunkWriter(st.ctx, st.tx, "sessions", meta.ID)
	if _, err := w.Write(encoded); err != nil {
		return err
	}
	if err := w.finish(); err != nil {
		return err
	}
	if listChanged {
		if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_projects SET revision=? WHERE id IN (?,?)`, st.changes.Revision, project, meta.ProjectID); err != nil {
			return err
		}
	}
	if _, exists := st.changes.Sessions[meta.ID]; !exists {
		st.changes.Sessions[meta.ID] = []int64{}
	}
	return nil
}

func (st *sqliteSessionTx) eventByID(sessionID, eventID string) (Event, int64, error) {
	var key string
	var ordinal int64
	if err := st.tx.QueryRowContext(st.ctx, `SELECT id,position FROM data_session_events WHERE owner_id=? AND record_id=? ORDER BY position DESC LIMIT 1`, sessionID, eventID).Scan(&key, &ordinal); err != nil {
		return Event{}, 0, err
	}
	var e Event
	if err := decodeSQLiteRecord(st.ctx, st.tx, "session_events", key, protocolChunkSize, &e); err != nil {
		return e, 0, err
	}
	return e, ordinal, nil
}

func (st *sqliteSessionTx) appendEvent(sessionID string, e Event) (int64, error) {
	var size int64
	err := st.tx.QueryRowContext(st.ctx, `SELECT size FROM record_collections WHERE parent_table='sessions' AND parent_id=? AND field='events' AND kind='array'`, sessionID).Scan(&size)
	if err != nil {
		return 0, err
	}
	if size < 0 || size == int64(^uint64(0)>>1) {
		return 0, errors.New("event ordinal exhausted")
	}
	if err := st.writeEvent(sessionID, size, e, true); err != nil {
		return 0, err
	}
	if _, err := st.tx.ExecContext(st.ctx, `UPDATE record_collections SET size=size+1 WHERE parent_table='sessions' AND parent_id=? AND field='events'`, sessionID); err != nil {
		return 0, err
	}
	return size, nil
}

func (st *sqliteSessionTx) writeEvent(sessionID string, ordinal int64, e Event, appendOnly bool) error {
	if e.ID == "" || ordinal < 0 {
		return errors.New("event requires identity and ordinal")
	}
	header, prefix, text, err := sqlEventEncoding(e)
	if err != nil {
		return err
	}
	if len(prefix)+len(text)+2 > maxStreamRecordBytes {
		return errors.New("event exceeds supported record size")
	}
	key := sqliteOwnedKey(sessionID, strconv.FormatInt(ordinal, 10))
	if appendOnly {
		_, err = st.tx.ExecContext(st.ctx, `INSERT INTO data_session_events(id,owner_id,map_key,position,record_id,project_id,session_id,parent_id,status,role,created_at,updated_at,kind,correlation_id,revision)
 VALUES(?,?,?,?,?,'',?,'',?,'',?,'',?,?,?)`, key, sessionID, strconv.FormatInt(ordinal, 10), ordinal, e.ID, sessionID, e.Status, sqliteTimeKey(e.CreatedAt), e.Type, e.ConsultationID, st.changes.Revision)
	} else {
		var result sql.Result
		result, err = st.tx.ExecContext(st.ctx, `UPDATE data_session_events SET record_id=?,status=?,created_at=?,kind=?,correlation_id=?,revision=? WHERE id=? AND owner_id=?`, e.ID, e.Status, sqliteTimeKey(e.CreatedAt), e.Type, e.ConsultationID, st.changes.Revision, key, sessionID)
		if err == nil {
			var n int64
			n, err = result.RowsAffected()
			if err == nil && n != 1 {
				return sql.ErrNoRows
			}
		}
		if err == nil {
			_, err = st.tx.ExecContext(st.ctx, `DELETE FROM event_content_publications WHERE event_key=?`, key)
		}
		if err == nil {
			_, err = st.tx.ExecContext(st.ctx, `DELETE FROM data_session_events_chunks WHERE record_key=?`, key)
		}
	}
	if err != nil {
		return err
	}
	w := newSQLiteChunkWriter(st.ctx, st.tx, "session_events", key)
	if err := updateSQLiteIndexFields(st.ctx, st.tx, "session_events", key, header); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_session_events SET message_at=? WHERE id=?`, eventMessageAt(e), key); err != nil {
		return err
	}
	if _, err := w.Write(prefix); err != nil {
		return err
	}
	if _, err := w.Write(text); err != nil {
		return err
	}
	hashState, err := sqlHashState(w.hash)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(`"}`)); err != nil {
		return err
	}
	if err := w.finish(); err != nil {
		return err
	}
	if err := st.saveEventStreamState(key, header, int64(len(prefix)), int64(len(text)), hashState, strings.TrimSpace(e.Text) != "", boundedText(e.Text, 4096)); err != nil {
		return err
	}
	st.changes.Sessions[sessionID] = append(st.changes.Sessions[sessionID], ordinal)
	return nil
}

// Idempotency/collision lookups stay in SQL, including old native IDs which are
// not globally unique. The native event composite identity remains unchanged.
func (st *sqliteSessionTx) messageIdentityExists(id string) (bool, error) {
	var found bool
	err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_session_events WHERE record_id=?) OR EXISTS(SELECT 1 FROM data_queued_messages WHERE record_id=?) OR EXISTS(SELECT 1 FROM accepted_message_ids WHERE message_id=?)`, id, id, id).Scan(&found)
	return found, err
}

func (st *sqliteSessionTx) acceptedMessage(sessionID, messageID string) (string, bool, error) {
	var fingerprint string
	err := decodeSQLiteRecord(st.ctx, st.tx, "accepted_messages", sessionID+"/"+messageID, 64<<10, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return fingerprint, err == nil, err
}

func (st *sqliteSessionTx) recordAcceptedMessage(sessionID, messageID, fingerprint string) error {
	if sessionID == "" || messageID == "" || fingerprint == "" {
		return errors.New("acceptance requires identity and fingerprint")
	}
	if previous, found, err := st.acceptedMessage(sessionID, messageID); err != nil {
		return err
	} else if found {
		if previous != fingerprint {
			return errors.New("message identity already accepted with different content")
		}
		return nil
	}
	id := sessionID + "/" + messageID
	// Original root collection ordering is retained for diagnostics. New rows get
	// an indexed max-position lookup rather than scanning/copying the receipt map.
	var position int64
	if err := st.tx.QueryRowContext(st.ctx, `SELECT coalesce(max(position),-1)+1 FROM data_accepted_messages WHERE owner_id=''`).Scan(&position); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO data_accepted_messages(id,owner_id,map_key,position,record_id,project_id,session_id,parent_id,status,role,created_at,updated_at,revision)
 VALUES(?,'',?,?,'','',?,'','','','','',?)`, id, id, position, sessionID, st.changes.Revision); err != nil {
		return err
	}
	encoded, _ := json.Marshal(fingerprint)
	w := newSQLiteChunkWriter(st.ctx, st.tx, "accepted_messages", id)
	if _, err := w.Write(encoded); err != nil {
		return err
	}
	if err := w.finish(); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO accepted_message_ids(session_id,message_id,receipt_key) VALUES(?,?,?)`, sessionID, messageID, id); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO record_collections(parent_table,parent_id,field,child_table,kind,size) VALUES('root','','acceptedMessages','accepted_messages','object',1)
 ON CONFLICT(parent_table,parent_id,field) DO UPDATE SET kind='object',size=size+1`); err != nil {
		return err
	}
	if _, exists := st.changes.Sessions[sessionID]; !exists {
		st.changes.Sessions[sessionID] = []int64{}
	}
	return nil
}

func (st *sqliteSessionTx) acceptUserEvent(sessionID, messageID, fingerprint string, e Event) (bool, error) {
	if prior, found, err := st.acceptedMessage(sessionID, messageID); err != nil {
		return false, err
	} else if found {
		if prior != fingerprint {
			return false, errors.New("message identity already accepted with different content")
		}
		return false, nil
	}
	if e.Type != "user" || e.ID != messageID {
		return false, errors.New("acceptance event identity mismatch")
	}
	meta, err := st.sessionMeta(sessionID)
	if err != nil {
		return false, err
	}
	if exists, err := st.messageIdentityExists(messageID); err != nil {
		return false, err
	} else if exists {
		return false, fmt.Errorf("message identity %s already exists", messageID)
	}
	if _, err := st.appendEvent(sessionID, e); err != nil {
		return false, err
	}
	if err := st.recordAcceptedMessage(sessionID, messageID, fingerprint); err != nil {
		return false, err
	}
	if meta.Role == sessionRoleGeneralAgent {
		meta.HarnessLocked = true
	}
	meta.UpdatedAt = e.CreatedAt
	if err := st.putSessionMeta(meta); err != nil {
		return false, err
	}
	return true, nil
}
