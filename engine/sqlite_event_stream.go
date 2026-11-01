package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"strings"
)

// Put text last, retaining canonical JSON and the normal digest/content contract.
// A hash checkpoint before the closing quote permits O(delta) append writes.
func sqlEventEncoding(e Event) (header, prefix, text []byte, err error) {
	value := e.Text
	e.Text = ""
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, nil, nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return
	}
	delete(fields, "text")
	header, err = json.Marshal(fields)
	if err != nil {
		return
	}
	prefix = append(append([]byte{}, header[:len(header)-1]...), []byte(`,"text":"`)...)
	raw, err = json.Marshal(value)
	if err == nil {
		text = raw[1 : len(raw)-1]
	}
	return
}
func sqlHashState(h hash.Hash) ([]byte, error) { return h.(encoding.BinaryMarshaler).MarshalBinary() }

type sqlEventStreamState struct {
	header, hash []byte
	prefix, text int64
	nonblank     bool
	preview      string
}

func decodeSQLStreamHeader(raw []byte, e *Event) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(e)
}

func (st *sqliteSessionTx) saveEventStreamState(key string, header []byte, prefix, text int64, state []byte, nonblank bool, preview string) error {
	if len(header) > 1<<20 {
		_, err := st.tx.ExecContext(st.ctx, `DELETE FROM event_stream_state WHERE event_id=?`, key)
		return err
	}
	_, err := st.tx.ExecContext(st.ctx, `INSERT INTO event_stream_state VALUES(?,?,?,?,?,?,?) ON CONFLICT(event_id) DO UPDATE SET header=excluded.header,prefix_bytes=excluded.prefix_bytes,text_bytes=excluded.text_bytes,hash_state=excluded.hash_state,nonblank=excluded.nonblank,preview=excluded.preview`, key, header, prefix, text, state, nonblank, preview)
	return err
}
func (st *sqliteSessionTx) eventStreamState(key string) (sqlEventStreamState, error) {
	var c sqlEventStreamState
	err := st.tx.QueryRowContext(st.ctx, `SELECT header,prefix_bytes,text_bytes,hash_state,nonblank,preview FROM event_stream_state WHERE event_id=?`, key).Scan(&c.header, &c.prefix, &c.text, &c.hash, &c.nonblank, &c.preview)
	return c, err
}
func (st *sqliteSessionTx) writeStreamMetadata(owner, key string, ordinal int64, e Event, c sqlEventStreamState, appendText string) error {
	header, prefix, _, err := sqlEventEncoding(e)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(appendText)
	if err != nil {
		return err
	}
	delta := encoded[1 : len(encoded)-1]
	var hashState []byte
	var total int64
	var digest string
	if bytes.Equal(header, c.header) {
		end := c.prefix + c.text
		chunk := end / sqliteContentChunkBytes
		var tail []byte
		if err := st.tx.QueryRowContext(st.ctx, `SELECT body FROM data_session_events_chunks WHERE record_key=? AND chunk=?`, key, chunk).Scan(&tail); err != nil {
			return err
		}
		keep := end % sqliteContentChunkBytes
		if int64(len(tail)) < keep {
			return errors.New("Invalid event stream tail.")
		}
		h := sha256.New()
		if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(c.hash); err != nil {
			return err
		}
		if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM data_session_events_chunks WHERE record_key=? AND chunk>=?`, key, chunk); err != nil {
			return err
		}
		w := newSQLiteChunkWriter(st.ctx, st.tx, "session_events", key)
		w.hash = h
		w.bytes = end
		w.chunk = int(chunk)
		w.buffer = append(w.buffer, tail[:keep]...)
		if _, err := w.Write(delta); err != nil {
			return err
		}
		hashState, err = sqlHashState(w.hash)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(`"}`)); err != nil {
			return err
		}
		if err := w.finish(); err != nil {
			return err
		}
		total, digest = w.bytes, hex.EncodeToString(w.hash.Sum(nil))
	} else {
		// Metadata changes may move the prefix. Stream existing encoded text into
		// transaction-local staging chunks, then replace atomically; never hydrate it.
		w := newSQLiteChunkWriter(st.ctx, st.tx, "session_events_rewrite", key)
		if _, err := w.Write(prefix); err != nil {
			return err
		}
		var oldDigest string
		if err := st.tx.QueryRowContext(st.ctx, `SELECT digest FROM data_session_events WHERE id=?`, key).Scan(&oldDigest); err != nil {
			return err
		}
		for offset, end := c.prefix, c.prefix+c.text; offset < end; {
			page, err := readSQLiteContent(st.ctx, st.tx, "session_events", key, oldDigest, offset, int(min(int64(sqliteContentChunkBytes), end-offset)))
			if err != nil {
				return err
			}
			if _, err := w.Write(page.Data); err != nil {
				return err
			}
			offset = page.NextOffset
		}
		if _, err := w.Write(delta); err != nil {
			return err
		}
		hashState, err = sqlHashState(w.hash)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(`"}`)); err != nil {
			return err
		}
		if err := w.flush(); err != nil {
			return err
		}
		total, digest = w.bytes, hex.EncodeToString(w.hash.Sum(nil))
		if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM data_session_events_chunks WHERE record_key=?`, key); err != nil {
			return err
		}
		if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO data_session_events_chunks SELECT record_key,chunk,body FROM data_session_events_rewrite_chunks WHERE record_key=?`, key); err != nil {
			return err
		}
		if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM data_session_events_rewrite_chunks WHERE record_key=?`, key); err != nil {
			return err
		}
	}
	nonblank := c.nonblank || strings.TrimSpace(appendText) != ""
	preview := c.preview
	if len(preview) < 4096 {
		preview = boundedText(preview+appendText, 4096)
	}
	messageAt := ""
	if e.ConsultationID == "" && e.CreatedAt != "" && (e.Type == "user" || e.Type == "assistant" && nonblank) {
		messageAt = e.CreatedAt
	}
	if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_session_events SET payload_bytes=?,digest=?,status=?,kind=?,created_at=?,correlation_id=?,message_at=?,revision=? WHERE id=?`, total, digest, e.Status, e.Type, sqliteTimeKey(e.CreatedAt), e.ConsultationID, messageAt, st.changes.Revision, key); err != nil {
		return err
	}
	if err := updateSQLiteIndexFields(st.ctx, st.tx, "session_events", key, header); err != nil {
		return err
	}
	if err := st.saveEventStreamState(key, header, int64(len(prefix)), c.text+int64(len(delta)), hashState, nonblank, preview); err != nil {
		return err
	}
	st.changes.Sessions[owner] = append(st.changes.Sessions[owner], ordinal)
	return nil
}
func (st *sqliteSessionTx) streamEvent(owner string, u streamUpdate) (Event, bool, error) {
	var key string
	var ordinal int64
	err := st.tx.QueryRowContext(st.ctx, `SELECT id,position FROM data_session_events WHERE owner_id=? AND record_id=? ORDER BY position DESC LIMIT 1`, owner, u.event.ID).Scan(&key, &ordinal)
	if errors.Is(err, sql.ErrNoRows) {
		_, err := st.appendEvent(owner, u.event)
		return u.event, err == nil, err
	}
	if err != nil {
		return Event{}, false, err
	}
	c, err := st.eventStreamState(key)
	if errors.Is(err, sql.ErrNoRows) {
		old, _, err := st.eventByID(owner, u.event.ID)
		if err != nil {
			return old, false, err
		}
		changes := streamChanges([]Event{old}, []streamUpdate{u}, 0)
		if len(changes) == 0 {
			return old, false, nil
		}
		e := changes[0].Event
		err = st.writeEvent(owner, ordinal, e, false)
		return e, err == nil, err
	}
	if err != nil {
		return Event{}, false, err
	}
	var e Event
	if err := decodeSQLStreamHeader(c.header, &e); err != nil {
		return e, false, err
	}
	if u.event.Title != "" {
		e.Title = u.event.Title
	}
	if u.event.Status != "" {
		e.Status = u.event.Status
	}
	if len(u.event.Data) > 0 {
		if e.Data == nil {
			e.Data = map[string]any{}
		}
		for k, v := range u.event.Data {
			e.Data[k] = v
		}
	}
	if u.appendText {
		header, _, _, err := sqlEventEncoding(e)
		if err != nil {
			return e, false, err
		}
		if u.event.Text == "" && bytes.Equal(header, c.header) {
			e.Text = c.preview
			return e, false, nil
		}
		err = st.writeStreamMetadata(owner, key, ordinal, e, c, u.event.Text)
		e.Text = boundedText(c.preview+u.event.Text, 4096)
		return e, err == nil, err
	}
	e.Text = u.event.Text
	err = st.writeEvent(owner, ordinal, e, false)
	return e, err == nil, err
}
func (st *sqliteSessionTx) patchEventMetadata(owner, key string, change func(*Event) error) error {
	var ordinal, size int64
	if err := st.tx.QueryRowContext(st.ctx, `SELECT position,payload_bytes FROM data_session_events WHERE id=? AND owner_id=?`, key, owner).Scan(&ordinal, &size); err != nil {
		return err
	}
	if size > protocolChunkSize {
		meta, err := readSQLiteEventMetadata(st.ctx, st.tx, key)
		if err != nil {
			return err
		}
		before, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		if err := change(&meta); err != nil {
			return err
		}
		after, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		if bytes.Equal(before, after) {
			return nil
		}
		return errors.New("Large event metadata must be prepared before publication.")
	}
	c, err := st.eventStreamState(key)
	var e Event
	if errors.Is(err, sql.ErrNoRows) {
		if err := decodeSQLiteRecord(st.ctx, st.tx, "session_events", key, protocolChunkSize, &e); err != nil {
			return err
		}
		if err := change(&e); err != nil {
			return err
		}
		return st.writeEvent(owner, ordinal, e, false)
	}
	if err != nil {
		return err
	}
	if err := decodeSQLStreamHeader(c.header, &e); err != nil {
		return err
	}
	if err := change(&e); err != nil {
		return err
	}
	return st.writeStreamMetadata(owner, key, ordinal, e, c, "")
}
