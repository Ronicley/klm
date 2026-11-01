package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"reflect"
	"strconv"
)

const sqliteContentChunkBytes = 64 << 10

type sqliteChunkWriter struct {
	ctx       context.Context
	tx        *sql.Tx
	table, id string
	buffer    []byte
	chunk     int
	bytes     int64
	hash      hash.Hash
}

func newSQLiteChunkWriter(ctx context.Context, tx *sql.Tx, table, id string) *sqliteChunkWriter {
	return &sqliteChunkWriter{ctx: ctx, tx: tx, table: table, id: id,
		buffer: make([]byte, 0, sqliteContentChunkBytes), hash: sha256.New()}
}

func (w *sqliteChunkWriter) Write(p []byte) (int, error) {
	consumed := 0
	for len(p) > 0 {
		n := min(len(p), sqliteContentChunkBytes-len(w.buffer))
		w.buffer = append(w.buffer, p[:n]...)
		w.hash.Write(p[:n])
		w.bytes += int64(n)
		p, consumed = p[n:], consumed+n
		if len(w.buffer) == sqliteContentChunkBytes {
			if err := w.flush(); err != nil {
				return consumed, err
			}
		}
	}
	return consumed, nil
}

func (w *sqliteChunkWriter) flush() error {
	if len(w.buffer) == 0 {
		return nil
	}
	if _, err := w.tx.ExecContext(w.ctx, `INSERT INTO data_`+w.table+`_chunks(record_key,chunk,body) VALUES(?,?,?)`, w.id, w.chunk, w.buffer); err != nil {
		return err
	}
	w.chunk++
	w.buffer = w.buffer[:0]
	return nil
}

func (w *sqliteChunkWriter) finish() error {
	if err := w.flush(); err != nil {
		return err
	}
	_, err := w.tx.ExecContext(w.ctx, `UPDATE data_`+w.table+` SET payload_bytes=?,digest=? WHERE id=?`, w.bytes, hex.EncodeToString(w.hash.Sum(nil)), w.id)
	return err
}

type legacyChild struct {
	id, position int64
	name         string
}

// A single row at a time; no collection-size-dependent Go allocations. Import
// transactions own their connection, so rows are closed before visiting children.
func (s legacyStage) children(id int64, visit func(legacyChild) error) error {
	var previous int64 = -1
	for {
		var c legacyChild
		err := s.tx.QueryRowContext(s.ctx, `SELECT id,name,position FROM legacy_nodes WHERE parent=? AND position>? ORDER BY position LIMIT 1`, id, previous).Scan(&c.id, &c.name, &c.position)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := visit(c); err != nil {
			return err
		}
		previous = c.position
	}
}

func (s legacyStage) emitMetadata(w io.Writer, id int64, children []sqliteDomain) error {
	if len(children) == 0 {
		return s.emit(w, id)
	}
	n, err := s.node(id)
	if err != nil {
		return err
	}
	if n.kind != "object" {
		return s.emit(w, id) // e.g. an absent optional analytics/snapshot object.
	}
	if _, err := io.WriteString(w, "{"); err != nil {
		return err
	}
	first := true
	err = s.children(id, func(c legacyChild) error {
		for _, child := range children {
			if child.field == c.name {
				return nil
			}
		}
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		first = false
		key, _ := json.Marshal(c.name)
		if _, err := w.Write(append(key, ':')); err != nil {
			return err
		}
		return s.emit(w, c.id)
	})
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "}")
	return err
}

func (s legacyStage) stringField(id int64, field string) (string, error) {
	child, err := s.child(id, field)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	n, err := s.node(child)
	if err != nil {
		return "", err
	}
	if n.kind != "scalar" {
		return "", fmt.Errorf("invalid scalar index field %s", field)
	}
	var value string
	if err := json.Unmarshal(n.scalar, &value); err != nil {
		return "", err
	}
	return value, nil
}

func sqliteOwnedKey(owner, key string) string {
	encoded, _ := json.Marshal([]string{owner, key})
	return string(encoded)
}

func (s legacyStage) importCollection(parentTable, parentID string, node int64, spec sqliteDomain) error {
	n, err := s.node(node)
	if err != nil {
		return err
	}
	kind := n.kind
	count := n.size
	if n.kind == "object" {
		if err := s.tx.QueryRowContext(s.ctx, `SELECT count(*) FROM legacy_nodes WHERE parent=?`, node).Scan(&count); err != nil {
			return err
		}
	}
	if spec.single {
		kind, count = "single", 1
	} else if n.kind == "scalar" {
		if string(n.scalar) != "null" {
			return errors.New("non-null scalar in legacy collection")
		}
		kind, count = "null", 0
	}
	if _, err := s.tx.ExecContext(s.ctx, `INSERT INTO record_collections(parent_table,parent_id,field,child_table,kind,size) VALUES(?,?,?,?,?,?)`, parentTable, parentID, spec.field, spec.table, kind, count); err != nil {
		return err
	}
	if _, err := s.tx.ExecContext(s.ctx, `INSERT INTO migration_collection_refs(parent_table,parent_id,field,source_node) VALUES(?,?,?,?)`, parentTable, parentID, spec.field, node); err != nil {
		return err
	}
	if spec.single {
		return s.importRecord(parentTable, parentID, "", 0, node, spec)
	}
	return s.children(node, func(c legacyChild) error {
		return s.importRecord(parentTable, parentID, c.name, c.position, c.id, spec)
	})
}

func (s legacyStage) importRecord(parentTable, owner, key string, position, node int64, spec sqliteDomain) error {
	fields := make([]string, 10)
	for i, field := range []string{"id", "projectId", "sessionId", "parentId", "status", "role", "createdAt", "updatedAt", "type", "consultationId"} {
		value, err := s.stringField(node, field)
		if err != nil {
			return err
		}
		fields[i] = value
	}
	id := sqliteOwnedKey(owner, key)
	if parentTable == "root" {
		if spec.single {
			id = "settings"
		} else if spec.typ.Kind() == reflect.Map {
			id = key
		} else if fields[0] != "" {
			id = fields[0]
		} else {
			// Sender-scoped receipts without an ID retain their original position;
			// the runtime receipt projection supplies sender+operation uniqueness.
			id = "ordinal:" + strconv.FormatInt(position, 10)
		}
	}
	if parentTable == "sessions" {
		fields[2] = owner
	}
	fields[6], fields[7] = sqliteTimeKey(fields[6]), sqliteTimeKey(fields[7])
	if _, err := s.tx.ExecContext(s.ctx, `INSERT INTO data_`+spec.table+`
 (id,owner_id,map_key,position,record_id,project_id,session_id,parent_id,status,role,created_at,updated_at,kind,correlation_id,revision)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,(SELECT value FROM store_meta WHERE key='revision'))`, id, owner, key, position, fields[0], fields[1], fields[2], fields[3], fields[4], fields[5], fields[6], fields[7], fields[8], fields[9]); err != nil {
		return err
	}
	if _, err := s.tx.ExecContext(s.ctx, `INSERT INTO migration_record_refs(table_name,record_key,source_node) VALUES(?,?,?)`, spec.table, id, node); err != nil {
		return err
	}
	if err := s.importIndexFields(spec.table, id, node); err != nil {
		return err
	}
	if spec.table == "session_events" {
		e := Event{Type: fields[8], CreatedAt: fields[6], ConsultationID: fields[9]}
		if e.Type == "assistant" && e.CreatedAt != "" && e.ConsultationID == "" {
			var err error
			e.Text, err = s.stringField(node, "text")
			if err != nil {
				return err
			}
		}
		if _, err := s.tx.ExecContext(s.ctx, `UPDATE data_session_events SET message_at=? WHERE id=?`, eventMessageAt(e), id); err != nil {
			return err
		}
	}
	if err := s.importLibraryReferences(spec.table, id, node); err != nil {
		return err
	}
	n, err := s.node(node)
	if err != nil {
		return err
	}
	if n.kind == "scalar" && sqliteScalarProjection(spec.table) {
		value, err := sqliteScalarText(n.scalar)
		if err != nil {
			return err
		}
		if _, err := s.tx.ExecContext(s.ctx, `UPDATE data_`+spec.table+` SET scalar_value=? WHERE id=?`, value, id); err != nil {
			return err
		}
	}
	writer := newSQLiteChunkWriter(s.ctx, s.tx, spec.table, id)
	if err := s.emitMetadata(writer, node, spec.children); err != nil {
		return err
	}
	if err := writer.finish(); err != nil {
		return err
	}
	for _, childSpec := range spec.children {
		child, err := s.child(node, childSpec.field)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err := s.importCollection(spec.table, id, child, childSpec); err != nil {
			return err
		}
	}
	return nil
}

// importStagedDomains runs only on an unpublished staging store. It never opens
// user files or executes filesystem outboxes. Each collection is a separate row
// table; one record's payload is streamed to 64 KiB SQL chunks.
func importStagedDomains(ctx context.Context, store *sqliteStore) error {
	specs, err := sqliteDomains()
	if err != nil {
		return err
	}
	return store.write(ctx, func(tx *sql.Tx) error {
		s := legacyStage{ctx: ctx, tx: tx}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE migration_record_refs(table_name TEXT NOT NULL,record_key TEXT NOT NULL,source_node INTEGER NOT NULL REFERENCES legacy_nodes(id),PRIMARY KEY(table_name,record_key),UNIQUE(table_name,source_node)) STRICT;
CREATE TABLE migration_collection_refs(parent_table TEXT NOT NULL,parent_id TEXT NOT NULL,field TEXT NOT NULL,source_node INTEGER NOT NULL REFERENCES legacy_nodes(id),PRIMARY KEY(parent_table,parent_id,field)) STRICT;`); err != nil {
			return err
		}
		for name := range sqliteScalarFields {
			id, err := s.child(1, name)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			n, err := s.node(id)
			if err != nil || n.kind != "scalar" {
				return errors.New("invalid legacy store metadata")
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO store_meta(key,value) VALUES(?,?)`, "legacy:"+name, string(n.scalar)); err != nil {
				return err
			}
			if name == "graphRevision" {
				if _, err := tx.ExecContext(ctx, `UPDATE store_meta SET value=? WHERE key='revision'`, string(n.scalar)); err != nil {
					return err
				}
			}
			if name == "webhookReceivedSequence" || name == "webhookReleaseSequence" {
				key := "webhook_received_sequence"
				if name == "webhookReleaseSequence" {
					key = "webhook_release_sequence"
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO store_meta(key,value) VALUES(?,?)`, key, string(n.scalar)); err != nil {
					return err
				}
			}
		}
		for _, spec := range specs {
			node, err := s.child(1, spec.field)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if err := s.importCollection("root", "", node, spec); err != nil {
				return fmt.Errorf("import %s: %w", spec.field, err)
			}
		}
		// Preserve the historical sender-scoped key grammar even for unusual
		// legacy session IDs containing '/'. No new global native-ID constraint.
		if _, err := tx.ExecContext(ctx, `INSERT INTO store_meta(key,value) SELECT 'webhook_event_count',CAST(count(*) AS TEXT) FROM data_webhook_events;
INSERT INTO store_meta(key,value) SELECT 'webhook_payload_bytes',CAST(coalesce(sum(CAST(x_payloadBytes AS INTEGER)),0) AS TEXT) FROM data_webhook_events;`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO accepted_message_ids(session_id,message_id,receipt_key)
 SELECT session.id,substr(receipt.map_key,length(session.id)+2),receipt.id
 FROM data_sessions session JOIN data_accepted_messages receipt
 ON substr(receipt.map_key,1,length(session.id)+1)=session.id||'/'`)
		if err != nil {
			return err
		}
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		if err := st.eachMatching("board_items", "1", nil, func(id string) error {
			var item activityBoardItem
			if err := decodeSQLiteRecord(ctx, tx, "board_items", id, 2<<20, &item); err != nil {
				return err
			}
			c, err := st.boardConnection(item.ConnectionID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			return indexSQLBoardItem(ctx, tx, item, c)
		}); err != nil {
			return err
		}
		after := ""
		for {
			ids, err := sqliteQueryIDs(ctx, tx, `SELECT id FROM data_webhook_events WHERE x_isPing<>'true' AND id>? ORDER BY id LIMIT 50`, after)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				return nil
			}
			for _, id := range ids {
				var e WebhookStoredEvent
				if err := decodeSQLiteRecord(ctx, tx, "webhook_events", id, 64<<10, &e); err != nil {
					return err
				}
				if err := decodeSQLiteRecord(ctx, tx, "webhook_payloads", sqliteOwnedKey(id, ""), maxStreamRecordBytes, &e.Payload); err != nil {
					return err
				}
				if err := indexSQLGithubActivity(ctx, tx, e); err != nil {
					return err
				}
				after = id
			}
		}
	})
}
