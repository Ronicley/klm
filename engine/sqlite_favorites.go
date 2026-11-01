package main

import (
	"database/sql"
	"net/http"
	"strconv"
)

func (a *app) patchSQLFavorite(w http.ResponseWriter, r *http.Request, favorite bool) {
	var key string
	var size, position int64
	if err := a.store.readers.QueryRowContext(r.Context(), `SELECT id,payload_bytes,position FROM data_session_events WHERE owner_id=? AND record_id=? ORDER BY position DESC LIMIT 1`, r.PathValue("id"), r.PathValue("eventID")).Scan(&key, &size, &position); err != nil {
		sqlHTTPError(w, err)
		return
	}
	change := func(e *Event) error {
		if e.Type != "assistant" {
			return controlError(404, "Assistant message not found.")
		}
		if e.Status != "" && e.Status != "completed" {
			return controlError(409, "Only completed responses can be favorited.")
		}
		e.Favorite = favorite
		return nil
	}
	if size > protocolChunkSize {
		prepared, err := a.store.prepareEventMutation(r.Context(), r.PathValue("id"), key, change)
		if err != nil {
			sqlHTTPError(w, err)
			return
		}
		a.mu.Lock()
		err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
			if err := st.publishEventMutation(prepared); err != nil {
				return err
			}
			s, err := st.sessionMeta(prepared.owner)
			if err != nil {
				return err
			}
			s.UpdatedAt = now()
			return st.putSessionMeta(s)
		})
		a.mu.Unlock()
		if err != nil {
			_ = a.store.discardEventGeneration(r.Context(), prepared.generation.ID)
			sqlHTTPError(w, err)
			return
		}
		respond(w, 200, map[string]any{"id": prepared.meta.ID, "type": prepared.meta.Type, "title": prepared.meta.Title, "status": prepared.meta.Status, "favorite": favorite, "createdAt": prepared.meta.CreatedAt, "text": "", "previewText": prepared.generation.Preview, "content": map[string]any{"sessionId": prepared.owner, "ordinal": strconv.FormatInt(position, 10), "digest": prepared.generation.Digest, "bytes": prepared.generation.Bytes}})
		return
	}
	a.mu.Lock()
	err := a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		s, err := st.sessionMeta(r.PathValue("id"))
		if err != nil {
			return err
		}
		if err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_session_events WHERE owner_id=? AND record_id=? ORDER BY position DESC LIMIT 1`, s.ID, r.PathValue("eventID")).Scan(&key); err != nil {
			return err
		}
		if err := st.patchEventMetadata(s.ID, key, change); err != nil {
			return err
		}
		s.UpdatedAt = now()
		return st.putSessionMeta(s)
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error { return writeSQLiteRecordContent(r.Context(), tx, w, "session_events", key) })
}
