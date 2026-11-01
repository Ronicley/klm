package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Appended generations share one immutable flat prefix and a bounded append
// range. They do not copy old payload chunks or build a generation chain.
func (s *sqliteStore) stageEventAppend(ctx context.Context, q sqliteQuery, owner, event, previous string, text any, status, title string, updates map[string]any) (g sqliteContentGeneration, handled bool, result error) {
	var base sql.NullString
	var baseBytes, appendBytes int64
	err := q.QueryRowContext(ctx, `SELECT payload_bytes,header,text_offset,text_bytes,text_preview,text_nonblank,preview_complete,hash_state,base_id,base_bytes,append_bytes FROM event_content_generations WHERE id=? AND ready=1`, previous).Scan(&g.Bytes, &g.Header, &g.TextOffset, &g.TextBytes, &g.Preview, &g.Nonblank, &g.PreviewComplete, &g.HashState, &base, &baseBytes, &appendBytes)
	if err != nil {
		return g, false, err
	}
	if len(g.HashState) == 0 {
		return g, false, nil
	}
	var meta Event
	if err := decodeSQLStreamHeader(g.Header, &meta); err != nil {
		return g, false, err
	}
	if status != "" && status != meta.Status || title != "" && title != meta.Title {
		return g, false, nil
	}
	for key, value := range updates {
		old, ok := meta.Data[key]
		if !ok || old == nil {
			return g, false, nil
		}
		a, err := json.Marshal(old)
		if err != nil {
			return g, false, err
		}
		b, err := json.Marshal(value)
		if err != nil {
			return g, false, err
		}
		if !bytes.Equal(a, b) {
			return g, false, nil
		}
	}
	root := previous
	if base.Valid {
		root = base.String
	} else {
		baseBytes = g.Bytes - 2
		appendBytes = 0
	}
	h := sha256.New()
	if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(g.HashState); err != nil {
		return g, false, nil
	}
	g.ID, g.Owner, g.EventID = newID(), owner, event
	s.setContentLive(g.ID, true)
	err = s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO event_content_generations(id,owner_id,event_id,base_id,base_bytes,append_bytes) VALUES(?,?,?,?,?,?)`, g.ID, owner, event, root, baseBytes, appendBytes)
		return err
	})
	if err != nil {
		s.setContentLive(g.ID, false)
		return g, true, err
	}
	defer func() {
		if result != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), sqliteGenerationCleanupTimeout)
			defer cancel()
			_ = s.discardEventGeneration(cleanup, g.ID)
		}
	}()
	w := &sqliteAppendStage{ctx: ctx, store: s, root: root, generation: g.ID, offset: appendBytes, buffer: make([]byte, 0, 16*sqliteContentChunkBytes), hash: h}
	r, err := nativeTextReader(text)
	if err != nil {
		return g, true, err
	}
	var preview strings.Builder
	preview.WriteString(g.Preview)
	if err := writeProtocolStringBody(w, r, func(value rune) {
		if !unicode.IsSpace(value) {
			g.Nonblank = true
		}
		if g.PreviewComplete {
			size := utf8.RuneLen(value)
			if preview.Len()+size <= 4096 {
				preview.WriteRune(value)
			} else {
				g.PreviewComplete = false
			}
		}
	}); err != nil {
		return g, true, err
	}
	if err := w.flush(); err != nil {
		return g, true, err
	}
	if w.offset == appendBytes {
		_ = s.discardEventGeneration(ctx, g.ID)
		g.ID = ""
		return g, true, nil
	}
	delta := w.offset - appendBytes
	g.Bytes += delta
	g.TextBytes += delta
	g.Preview = preview.String()
	g.HashState, err = sqlHashState(h)
	if err != nil {
		return g, true, err
	}
	_, _ = h.Write([]byte(`"}`))
	g.Digest = hex.EncodeToString(h.Sum(nil))
	err = s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE event_content_generations SET payload_bytes=?,digest=?,header=?,text_offset=?,text_bytes=?,text_preview=?,text_nonblank=?,preview_complete=?,hash_state=?,append_bytes=?,ready=1 WHERE id=?`, g.Bytes, g.Digest, g.Header, g.TextOffset, g.TextBytes, g.Preview, g.Nonblank, g.PreviewComplete, g.HashState, w.offset, g.ID)
		return err
	})
	return g, true, err
}

type sqliteAppendStage struct {
	ctx              context.Context
	store            *sqliteStore
	root, generation string
	offset           int64
	buffer           []byte
	hash             io.Writer
}

func (w *sqliteAppendStage) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		if err := w.ctx.Err(); err != nil {
			return 0, err
		}
		take := min(len(p), cap(w.buffer)-len(w.buffer))
		w.buffer = append(w.buffer, p[:take]...)
		p = p[take:]
		if len(w.buffer) == cap(w.buffer) {
			if err := w.flush(); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

func (w *sqliteAppendStage) flush() error {
	if len(w.buffer) == 0 {
		return nil
	}
	start := w.offset
	if err := w.store.reserveContentStage(w.generation, int64(len(w.buffer))); err != nil {
		return err
	}
	err := w.store.write(w.ctx, func(tx *sql.Tx) error {
		for offset := 0; offset < len(w.buffer); offset += sqliteContentChunkBytes {
			end := min(len(w.buffer), offset+sqliteContentChunkBytes)
			if _, err := tx.ExecContext(w.ctx, `INSERT INTO event_content_appends VALUES(?,?,?)`, w.root, w.offset, w.buffer[offset:end]); err != nil {
				return err
			}
			w.offset += int64(end - offset)
		}
		_, err := tx.ExecContext(w.ctx, `UPDATE event_content_generations SET append_bytes=? WHERE id=? AND ready=0`, w.offset, w.generation)
		return err
	})
	if err != nil {
		w.offset = start
		return err
	}
	if _, err := w.hash.Write(w.buffer); err != nil {
		return err
	}
	w.buffer = w.buffer[:0]
	return nil
}
