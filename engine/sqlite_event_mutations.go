package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"io"
	"maps"
	"reflect"
)

type sqlitePreparedEventMutation struct {
	owner, key, revision string
	meta                 Event
	generation           sqliteContentGeneration
}

func (a *app) contentFailure(err error) error {
	var failure interface{ Code() int }
	if errors.As(err, &failure) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.storageErr != nil {
			return a.storageErr
		}
		return a.failStorageLocked(err)
	}
	return err
}

// Prepare metadata changes with the same canonical writer used by ingestion.
// Copy/read work happens outside app.mu and outside the publication transaction.
func (s *sqliteStore) prepareEventMutation(ctx context.Context, owner, key string, change func(*Event) error) (prepared sqlitePreparedEventMutation, result error) {
	ctx = protocolDirectoryContext(ctx, s.dir)
	prepared.owner, prepared.key = owner, key
	frame := &protocolFrame{ctx: ctx, dir: s.dir}
	defer frame.Close()
	err := s.readSnapshot(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM data_session_events WHERE id=? AND owner_id=?`, key, owner).Scan(&prepared.revision); err != nil {
			return err
		}
		meta, err := readSQLiteEventMetadata(ctx, tx, key)
		if err != nil {
			return err
		}
		original := maps.Clone(meta.Data)
		if err := change(&meta); err != nil {
			return err
		}
		// Patch only changed fields; projected metadata omits large native values.
		updates := map[string]any{}
		for name, value := range meta.Data {
			if previous, exists := original[name]; !exists || !reflect.DeepEqual(previous, value) {
				updates[name] = value
			}
		}
		for name := range original {
			if _, exists := meta.Data[name]; !exists {
				return errors.New("Event data removal requires an explicit content replacement.")
			}
		}
		if meta.Text != "" {
			return errors.New("Metadata mutation cannot replace event text.")
		}
		meta.Text = ""
		meta.Data = nil
		prepared.meta = meta
		header, _, _, err := sqlEventEncoding(meta)
		if err != nil {
			return err
		}
		w := bufio.NewWriterSize(frame, protocolChunkSize)
		if _, err := w.Write(header[:len(header)-1]); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `,"data":`); err != nil {
			return err
		}
		if err := writeMergedSQLiteEventData(ctx, tx, w, key, updates); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `,"text":`); err != nil {
			return err
		}
		text, err := readSQLiteEventText(ctx, tx, key)
		if err != nil {
			return err
		}
		if err := writeProtocolString(w, text); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "}"); err != nil {
			return err
		}
		return w.Flush()
	})
	if err != nil {
		return prepared, err
	}
	r, err := frame.reader()
	if err != nil {
		return prepared, err
	}
	prepared.generation, err = s.stageEventGeneration(ctx, owner, prepared.meta.ID, r)
	return prepared, err
}

func (st *sqliteSessionTx) publishEventMutation(prepared sqlitePreparedEventMutation) error {
	var revision string
	var ordinal int64
	if err := st.tx.QueryRowContext(st.ctx, `SELECT revision,position FROM data_session_events WHERE id=? AND owner_id=? AND record_id=?`, prepared.key, prepared.owner, prepared.meta.ID).Scan(&revision, &ordinal); err != nil {
		return err
	}
	if revision != prepared.revision {
		return errSQLiteContentChanged
	}
	// Native IDs can repeat in imported histories. Never resolve the destination
	// by record_id again: publish only to the exact prepared owning ordinal.
	g := prepared.generation
	var header []byte
	if err := st.tx.QueryRowContext(st.ctx, `SELECT header FROM event_content_generations WHERE id=? AND owner_id=? AND event_id=? AND ready=1 AND payload_bytes=? AND digest=?`, g.ID, prepared.owner, prepared.meta.ID, g.Bytes, g.Digest).Scan(&header); err != nil {
		return err
	}
	var meta Event
	if err := decodeSQLStreamHeader(header, &meta); err != nil {
		return err
	}
	if err := st.writeEvent(prepared.owner, ordinal, meta, false); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO event_content_publications(event_key,generation_id) VALUES(?,?)`, prepared.key, g.ID); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_session_events SET payload_bytes=?,digest=? WHERE id=?`, g.Bytes, g.Digest, prepared.key); err != nil {
		return err
	}
	if g.Nonblank && meta.Type == "assistant" && meta.ConsultationID == "" {
		if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_session_events SET message_at=? WHERE id=?`, meta.CreatedAt, prepared.key); err != nil {
			return err
		}
	}
	if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM event_stream_state WHERE event_id=?`, prepared.key); err != nil {
		return err
	}
	st.changes.publishedContent = append(st.changes.publishedContent, g.ID)
	return nil
}

func (a *app) prepareSessionEventStatuses(ctx context.Context, owner, status string) error {
	return a.prepareSelectedEventStatuses(ctx, owner, "", nil, status)
}

func (a *app) prepareSelectedEventStatuses(ctx context.Context, owner, where string, values []any, status string, includeTerminal ...bool) error {
	for {
		var key string
		filter := ` AND status IN ('running','pending','started')`
		args := []any{owner, protocolChunkSize}
		if len(includeTerminal) > 0 && includeTerminal[0] {
			filter = ` AND status<>?`
			args = append(args, status)
		}
		args = append(args, values...)
		err := a.store.readers.QueryRowContext(ctx, `SELECT id FROM data_session_events WHERE owner_id=? AND payload_bytes>? `+filter+` `+where+` ORDER BY position LIMIT 1`, args...).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		prepared, err := a.store.prepareEventMutation(ctx, owner, key, func(e *Event) error { e.Status = status; return nil })
		if err != nil {
			return err
		}
		a.mu.Lock()
		err = a.persistSQLLocked(ctx, func(st *sqliteSessionTx) error { return st.publishEventMutation(prepared) })
		a.mu.Unlock()
		if err != nil {
			_ = a.store.discardEventGeneration(ctx, prepared.generation.ID)
			return err
		}
	}
}
