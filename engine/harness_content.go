package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"io"
	"time"
)

func (p *adapter) putStreamed(key, kind, title string, text any, status string, appendText bool, raw map[string]any, prepared ...*sql.Tx) (result error) {
	defer func() {
		if result != nil && p.turn.ctx.Err() == nil {
			result = p.app.contentFailure(result)
		}
	}()
	if len(prepared) == 0 {
		release, err := acquireProtocolStage(p.turn.ctx, protocolPersistenceSlots)
		if err != nil {
			return err
		}
		defer release()
	}
	if p.app.store == nil {
		return errors.New("Streaming harness content requires SQLite storage.")
	}
	if appendText {
		return p.putStreamedAppend(key, kind, title, text, status, raw)
	}
	if p.finalBatch != nil && len(p.finalBatch) > 0 {
		pending := p.finalBatch
		p.finalBatch = []streamUpdate{}
		if p.stream != nil {
			if err := p.stream.flushUpdates(pending); err != nil {
				return err
			}
		} else if err := p.commitStreamUpdates(pending); err != nil {
			return err
		}
	}
	if p.stream != nil {
		if err := p.stream.flush(nil); err != nil {
			return err
		}
	}
	if p.keys == nil {
		p.keys = map[string]string{}
	}
	id := p.keys[key]
	if key == "" || id == "" {
		id = newID()
		if key != "" {
			if err := p.turn.protocolScope.cacheValue("event-key/"+p.id+"/"+key, id); err != nil {
				return err
			}
			p.keys[key] = id
		}
	}
	meta := Event{ID: id, Type: kind, Title: title, Status: status, ConsultationID: p.turn.consultationID, CreatedAt: now()}
	expected := ""
	data := map[string]any{"harness": p.harness}
	for key, value := range raw {
		data[key] = value
	}
	frame := &protocolFrame{ctx: p.turn.ctx, dir: p.app.dir}
	defer frame.Close()
	build := func(tx *sql.Tx) error {
		var previousKey string
		err := tx.QueryRowContext(p.turn.ctx, `SELECT id,revision FROM data_session_events WHERE owner_id=? AND record_id=? ORDER BY position DESC LIMIT 1`, p.id, id).Scan(&previousKey, &expected)
		if errors.Is(err, sql.ErrNoRows) {
			previousKey = ""
		} else if err != nil {
			return err
		}
		if previousKey != "" {
			previous, err := readSQLiteEventMetadata(p.turn.ctx, tx, previousKey)
			if err != nil {
				return err
			}
			meta.CreatedAt, meta.Favorite, meta.Type = previous.CreatedAt, previous.Favorite, previous.Type
			if meta.Title == "" {
				meta.Title = previous.Title
			}
			if meta.Status == "" {
				meta.Status = previous.Status
			}
		}
		w := bufio.NewWriterSize(frame, protocolChunkSize)
		header, _, _, err := sqlEventEncoding(meta)
		if err != nil {
			return err
		}
		if _, err := w.Write(header[:len(header)-1]); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `,"data":`); err != nil {
			return err
		}
		if err := writeMergedSQLiteEventData(p.turn.ctx, tx, w, previousKey, data); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `,"text":`); err != nil {
			return err
		}
		reader, err := nativeTextReader(text)
		if err != nil {
			return err
		}
		if err := writeProtocolString(w, reader); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "}"); err != nil {
			return err
		}
		return w.Flush()
	}
	var err error
	if len(prepared) > 0 && prepared[0] != nil {
		err = build(prepared[0])
	} else {
		err = p.app.store.readSnapshot(p.turn.ctx, build)
	}
	if err != nil {
		return err
	}
	r, err := frame.reader()
	if err != nil {
		return err
	}
	generation, err := p.app.store.stageEventGeneration(p.turn.ctx, p.id, id, r)
	if err != nil {
		return err
	}
	err = p.publishStreamedGeneration(meta, generation, expected)
	if err == nil {
		if p.streamedContent == nil {
			p.streamedContent = map[string]bool{}
		}
		p.streamedContent[id] = true
	}
	return err
}

func (p *adapter) putStreamedAppend(key, kind, title string, text any, status string, raw map[string]any) error {
	if protocolHasContent(raw) {
		return errors.New("Native append metadata exceeded its supported envelope.")
	}
	if p.stream != nil {
		if err := p.stream.flush(nil); err != nil {
			return err
		}
	}
	id := p.keys[key]
	if id == "" {
		return p.putStreamed(key, kind, title, text, status, false, raw, nil)
	}
	var rewritten bool
	err := p.app.store.readSnapshot(p.turn.ctx, func(tx *sql.Tx) error {
		var storedKey, revision string
		err := tx.QueryRowContext(p.turn.ctx, `SELECT id,revision FROM data_session_events WHERE owner_id=? AND record_id=? ORDER BY position DESC LIMIT 1`, p.id, id).Scan(&storedKey, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		generation, err := sqlitePublishedGeneration(p.turn.ctx, tx, "session_events", storedKey)
		if err != nil {
			return err
		}
		if generation != "" {
			updates := map[string]any{"harness": p.harness}
			for key, value := range raw {
				updates[key] = value
			}
			staged, handled, err := p.app.store.stageEventAppend(p.turn.ctx, tx, p.id, id, generation, text, status, title, updates)
			if err != nil {
				return err
			}
			if handled {
				rewritten = true
				if staged.ID == "" {
					return nil
				}
				var meta Event
				if err := decodeSQLStreamHeader(staged.Header, &meta); err != nil {
					return err
				}
				return p.publishStreamedGeneration(meta, staged, revision)
			}
		}
		previous, err := readSQLiteEventText(p.turn.ctx, tx, storedKey)
		if err != nil {
			return err
		}
		delta, err := nativeTextReader(text)
		if err != nil {
			return err
		}
		rewritten = true
		return p.putStreamed(key, kind, title, &protocolText{open: func() (io.Reader, error) { return io.MultiReader(previous, delta), nil }}, status, false, raw, tx)
	})
	if err != nil || rewritten {
		return err
	}
	return p.putStreamed(key, kind, title, text, status, false, raw, nil)
}

func (p *adapter) publishStreamedGeneration(meta Event, generation sqliteContentGeneration, expected string) error {
	p.app.mu.Lock()
	ctx, cancel := context.WithTimeout(p.turn.ctx, 5*time.Second)
	err := p.app.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
		var revision string
		err := st.tx.QueryRowContext(ctx, `SELECT revision FROM data_session_events WHERE owner_id=? AND record_id=? ORDER BY position DESC LIMIT 1`, p.id, meta.ID).Scan(&revision)
		if errors.Is(err, sql.ErrNoRows) {
			revision = ""
		} else if err != nil {
			return err
		}
		if revision != expected {
			return errSQLiteContentChanged
		}
		if _, err := st.publishEventGeneration(p.id, meta, generation); err != nil {
			return err
		}
		session, err := st.sessionMeta(p.id)
		if err != nil {
			return err
		}
		session.UpdatedAt = now()
		return st.putSessionMeta(session)
	})
	cancel()
	p.app.mu.Unlock()
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), sqliteGenerationCleanupTimeout)
		defer cancel()
		_ = p.app.store.discardEventGeneration(cleanup, generation.ID)
	}
	return err
}
