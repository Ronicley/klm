package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"io"
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
		if err := change(&meta); err != nil {
			return err
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
		if err := writeMergedSQLiteEventData(ctx, tx, w, key, nil); err != nil {
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
	if err := st.tx.QueryRowContext(st.ctx, `SELECT revision FROM data_session_events WHERE id=? AND owner_id=?`, prepared.key, prepared.owner).Scan(&revision); err != nil {
		return err
	}
	if revision != prepared.revision {
		return errSQLiteContentChanged
	}
	_, err := st.publishEventGeneration(prepared.owner, prepared.meta, prepared.generation)
	return err
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
