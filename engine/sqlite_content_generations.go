package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const sqliteGenerationCleanupTimeout = 5 * time.Second

// Version 2 is additive: existing event chunks remain authoritative unless a
// publication points at an immutable generation. No history rewrite is needed.
const sqliteContentGenerationSchema = `
CREATE TABLE IF NOT EXISTS event_content_generations(
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, event_id TEXT NOT NULL,
 payload_bytes INTEGER NOT NULL DEFAULT 0, digest TEXT NOT NULL DEFAULT '',
 header BLOB NOT NULL DEFAULT X'7B7D', text_offset INTEGER NOT NULL DEFAULT 0, text_bytes INTEGER NOT NULL DEFAULT 0,
 text_preview TEXT NOT NULL DEFAULT '', text_nonblank INTEGER NOT NULL DEFAULT 0 CHECK(text_nonblank IN (0,1)),
 preview_complete INTEGER NOT NULL DEFAULT 1 CHECK(preview_complete IN(0,1)),
 hash_state BLOB NOT NULL DEFAULT X'', base_id TEXT REFERENCES event_content_generations(id),base_bytes INTEGER NOT NULL DEFAULT 0,append_bytes INTEGER NOT NULL DEFAULT 0,
 ready INTEGER NOT NULL DEFAULT 0 CHECK(ready IN (0,1))
) STRICT;
CREATE TABLE IF NOT EXISTS event_content_generation_chunks(
 generation_id TEXT NOT NULL REFERENCES event_content_generations(id) ON DELETE CASCADE,
 chunk INTEGER NOT NULL CHECK(chunk>=0), body BLOB NOT NULL CHECK(length(body)>0 AND length(body)<=65536),
 PRIMARY KEY(generation_id,chunk)
) STRICT;
CREATE TABLE IF NOT EXISTS event_content_publications(
 event_key TEXT PRIMARY KEY REFERENCES data_session_events(id) ON DELETE CASCADE,
 generation_id TEXT NOT NULL REFERENCES event_content_generations(id)
) STRICT;
CREATE INDEX IF NOT EXISTS event_content_publications_generation ON event_content_publications(generation_id);
CREATE INDEX IF NOT EXISTS event_content_generations_base ON event_content_generations(base_id);
CREATE TABLE IF NOT EXISTS event_content_appends(base_id TEXT NOT NULL REFERENCES event_content_generations(id) ON DELETE CASCADE,offset INTEGER NOT NULL,body BLOB NOT NULL CHECK(length(body)>0 AND length(body)<=65536),PRIMARY KEY(base_id,offset)) STRICT;
`

func (s *sqliteStore) migrateContentGenerations(ctx context.Context) error {
	if err := s.validatePublicationSchemaLayout(ctx); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
			return err
		}
		if version >= 2 && version <= sqliteSchemaVersion {
			return nil
		}
		if version != 1 {
			return errors.New("Unsupported SQLite content schema.")
		}
		if _, err := tx.ExecContext(ctx, sqliteContentGenerationSchema); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `PRAGMA user_version=2`)
		return err
	})
}

type sqliteContentGeneration struct {
	ID, Owner, EventID, Digest string
	Bytes                      int64
	Header                     []byte
	TextOffset, TextBytes      int64
	Preview                    string
	Nonblank                   bool
	HashState                  []byte
	PreviewComplete            bool
}

// Caller supplies a fully framed/validated canonical stream. Input reads occur
// before each short write transaction; neither app.mu nor the SQL writer waits
// for native/network input. The random unpublished generation is undiscoverable
// through public history/content until publishEventGeneration commits.
func (s *sqliteStore) stageEventGeneration(ctx context.Context, owner, event string, source io.Reader) (generation sqliteContentGeneration, result error) {
	ctx = protocolDirectoryContext(ctx, s.dir)
	if owner == "" || event == "" {
		return generation, errors.New("Content requires event ownership.")
	}
	generation = sqliteContentGeneration{ID: newID(), Owner: owner, EventID: event, PreviewComplete: true}
	s.setContentLive(generation.ID, true)
	err := s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO event_content_generations(id,owner_id,event_id) VALUES(?,?,?)`, generation.ID, owner, event)
		return err
	})
	if err != nil {
		s.setContentLive(generation.ID, false)
		return generation, err
	}
	defer func() {
		if result != nil {
			// Cancellation does not leave a visible partial generation. Remaining
			// unpublished chunks are also reclaimed by the locked startup sweep.
			cleanup, cancel := context.WithTimeout(context.Background(), sqliteGenerationCleanupTimeout)
			defer cancel()
			_ = s.discardEventGeneration(cleanup, generation.ID)
		}
	}()
	h := sha256.New()
	buffer := make([]byte, 16*sqliteContentChunkBytes)
	chunk := int64(0)
	for {
		n, readErr := io.ReadFull(source, buffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return generation, readErr
		}
		if n > 0 {
			if err := ctx.Err(); err != nil {
				return generation, err
			}
			if err := s.reserveContentStage(generation.ID, int64(n)); err != nil {
				return generation, err
			}
			_, _ = h.Write(buffer[:n])
			start := chunk
			err := s.write(ctx, func(tx *sql.Tx) error {
				for offset := 0; offset < n; offset += sqliteContentChunkBytes {
					end := min(n, offset+sqliteContentChunkBytes)
					if _, err := tx.ExecContext(ctx, `INSERT INTO event_content_generation_chunks VALUES(?,?,?)`, generation.ID, chunk, buffer[offset:end]); err != nil {
						return err
					}
					chunk++
				}
				return nil
			})
			if err != nil {
				chunk = start
				return generation, err
			}
			generation.Bytes += int64(n)
		}
		if readErr != nil {
			break
		}
	}
	generation.Digest = hex.EncodeToString(h.Sum(nil))
	reader := &sqliteContentReader{ctx: ctx, q: s.writer, generation: generation.ID, size: generation.Bytes, chunk: -1}
	headerPolicy := func(path []string) projectionAction {
		if len(path) == 1 && path[0] == "text" {
			return projectionDiscard
		}
		if len(path) == 2 && path[0] == "data" {
			return projectionSmallValue
		}
		return projectionKeep
	}
	header, _, err := projectProtocolJSON(ctx, reader, headerPolicy, 1<<20, errProtocolMetadata, false, func(path []string, start, end int64, kind byte) {
		if len(path) == 1 && path[0] == "text" && kind == '"' {
			generation.TextOffset, generation.TextBytes = start, end-start
		}
	})
	if err != nil {
		return generation, err
	}
	var meta Event
	if err := decodeSQLStreamHeader(header, &meta); err != nil || meta.ID != event || meta.Type == "" {
		return generation, errors.New("Invalid canonical content identity.")
	}
	generation.Header = header
	if generation.TextBytes > 0 {
		raw := &sqliteContentReader{ctx: ctx, q: s.writer, generation: generation.ID, offset: generation.TextOffset, size: generation.Bytes, chunk: -1}
		decoded := &jsonStringReader{r: bufio.NewReaderSize(io.LimitReader(raw, generation.TextBytes), protocolChunkSize)}
		text := bufio.NewReaderSize(decoded, protocolChunkSize)
		var preview strings.Builder
		previewDone := false
		var count uint64
		for {
			value, size, err := text.ReadRune()
			if err == io.EOF {
				break
			}
			if err != nil {
				return generation, err
			}
			if value == utf8.RuneError && size == 1 {
				return generation, errProtocolJSON
			}
			if !unicode.IsSpace(value) {
				generation.Nonblank = true
			}
			if !previewDone {
				if preview.Len()+size <= 4096 {
					preview.WriteRune(value)
				} else {
					previewDone = true
				}
			}
			count++
			if count%16384 == 0 {
				if err := ctx.Err(); err != nil {
					return generation, err
				}
			}
		}
		generation.Preview = preview.String()
		generation.PreviewComplete = !previewDone
	}
	if generation.TextBytes > 0 && generation.TextOffset+generation.TextBytes == generation.Bytes-1 {
		prefix := sha256.New()
		r := &sqliteContentReader{ctx: ctx, q: s.writer, generation: generation.ID, size: generation.Bytes, chunk: -1}
		if _, err := io.CopyN(prefix, r, generation.Bytes-2); err != nil {
			return generation, err
		}
		generation.HashState, err = sqlHashState(prefix)
		if err != nil {
			return generation, err
		}
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE event_content_generations SET payload_bytes=?,digest=?,header=?,text_offset=?,text_bytes=?,text_preview=?,text_nonblank=?,preview_complete=?,hash_state=?,ready=1 WHERE id=? AND ready=0`, generation.Bytes, generation.Digest, header, generation.TextOffset, generation.TextBytes, generation.Preview, generation.Nonblank, generation.PreviewComplete, generation.HashState, generation.ID)
		return err
	})
	return generation, err
}

// Bounded cleanup transactions allow small writes between deletion batches.
func (s *sqliteStore) discardEventGeneration(ctx context.Context, id string) error {
	s.setContentLive(id, false)
	for {
		var deleted int64
		err := s.write(ctx, func(tx *sql.Tx) error {
			result, err := tx.ExecContext(ctx, `DELETE FROM event_content_generation_chunks WHERE generation_id=? AND chunk IN (SELECT chunk FROM event_content_generation_chunks WHERE generation_id=? LIMIT 16) AND NOT EXISTS(SELECT 1 FROM event_content_publications WHERE generation_id=?) AND NOT EXISTS(SELECT 1 FROM event_content_generations WHERE base_id=?)`, id, id, id, id)
			if err != nil {
				return err
			}
			deleted, err = result.RowsAffected()
			return err
		})
		if err != nil {
			return err
		}
		if deleted == 0 {
			break
		}
	}
	for {
		var removed int64
		err := s.write(ctx, func(tx *sql.Tx) error {
			r, err := tx.ExecContext(ctx, `DELETE FROM event_content_appends WHERE base_id=? AND offset IN (SELECT offset FROM event_content_appends WHERE base_id=? LIMIT 16) AND NOT EXISTS(SELECT 1 FROM event_content_publications WHERE generation_id=?) AND NOT EXISTS(SELECT 1 FROM event_content_generations WHERE base_id=?)`, id, id, id, id)
			if err != nil {
				return err
			}
			removed, err = r.RowsAffected()
			return err
		})
		if err != nil {
			return err
		}
		if removed == 0 {
			break
		}
	}
	var parent sql.NullString
	err := s.write(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT base_id FROM event_content_generations WHERE id=?`, id).Scan(&parent)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM event_content_generations WHERE id=? AND NOT EXISTS(SELECT 1 FROM event_content_publications WHERE generation_id=?) AND NOT EXISTS(SELECT 1 FROM event_content_generations WHERE base_id=?)`, id, id, id)
		return err
	})
	if err != nil {
		return err
	}
	if parent.Valid {
		if err := s.trimEventAppendTail(ctx, parent.String); err != nil {
			return err
		}
		s.releaseContentStage(id)
		return s.discardEventGeneration(ctx, parent.String)
	}
	var retained bool
	if err := s.writer.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM event_content_generations WHERE id=?)`, id).Scan(&retained); err != nil {
		return err
	}
	if !retained {
		s.releaseContentStage(id)
	}
	return nil
}

func (s *sqliteStore) reserveContentStage(id string, bytes int64) error {
	free, err := protocolFreeDisk(s.dir)
	if err != nil {
		return errProtocolStagingIO
	}
	if free < protocolDiskReserve+uint64(bytes) {
		return errProtocolStaging
	}
	s.contentMu.Lock()
	defer s.contentMu.Unlock()
	protocolStagingBudget.Lock()
	defer protocolStagingBudget.Unlock()
	if bytes > protocolStagingLimit-protocolStagingBudget.bytes {
		return errProtocolStaging
	}
	if s.contentStaged == nil {
		s.contentStaged = map[string]int64{}
	}
	s.contentStaged[id] += bytes
	protocolStagingBudget.bytes += bytes
	return nil
}

func (s *sqliteStore) releaseContentStage(id string) {
	s.contentMu.Lock()
	defer s.contentMu.Unlock()
	protocolStagingBudget.Lock()
	protocolStagingBudget.bytes -= s.contentStaged[id]
	delete(s.contentStaged, id)
	protocolStagingBudget.Unlock()
}

func (s *sqliteStore) trimEventAppendTail(ctx context.Context, base string) error {
	for {
		var removed int64
		err := s.write(ctx, func(tx *sql.Tx) error {
			var end int64
			if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(append_bytes),0) FROM event_content_generations WHERE base_id=?`, base).Scan(&end); err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `DELETE FROM event_content_appends WHERE base_id=? AND offset IN (SELECT offset FROM event_content_appends WHERE base_id=? AND offset>=? LIMIT 16)`, base, base, end)
			if err != nil {
				return err
			}
			removed, err = result.RowsAffected()
			return err
		})
		if err != nil {
			return err
		}
		if removed == 0 {
			return nil
		}
	}
}

func (s *sqliteStore) recoverEventGenerations(ctx context.Context) error {
	// Startup only, under the data-directory lock and before active turns.
	for {
		var id string
		err := s.writer.QueryRowContext(ctx, `SELECT id FROM event_content_generations g WHERE NOT EXISTS(SELECT 1 FROM event_content_publications p WHERE p.generation_id=g.id) AND NOT EXISTS(SELECT 1 FROM event_content_generations child WHERE child.base_id=g.id) LIMIT 1`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.discardEventGeneration(ctx, id); err != nil {
			return err
		}
	}
}

func (s *sqliteStore) setContentLive(id string, live bool) {
	s.contentMu.Lock()
	defer s.contentMu.Unlock()
	if s.contentLive == nil {
		s.contentLive = map[string]bool{}
	}
	if live {
		s.contentLive[id] = true
	} else {
		delete(s.contentLive, id)
	}
}

func (s *sqliteStore) collectEventGenerations(ctx context.Context) error {
	ids, err := sqliteQueryIDs(ctx, s.writer, `SELECT id FROM event_content_generations g WHERE NOT EXISTS(SELECT 1 FROM event_content_publications p WHERE p.generation_id=g.id) AND NOT EXISTS(SELECT 1 FROM event_content_generations child WHERE child.base_id=g.id) LIMIT 64`)
	if err != nil {
		return err
	}
	for _, id := range ids {
		s.contentMu.Lock()
		live := s.contentLive[id]
		s.contentMu.Unlock()
		if live {
			continue
		}
		if err := s.discardEventGeneration(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func sqlitePublishedGeneration(ctx context.Context, q sqliteQuery, table, key string) (string, error) {
	if table != "session_events" {
		return "", nil
	}
	// Authority verification can read a version-1 database before the additive
	// migration is authorized. Its fixed-key chunks must remain readable then.
	var available bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='event_content_publications')`).Scan(&available); err != nil {
		return "", err
	}
	if !available {
		return "", nil
	}
	var id string
	err := q.QueryRowContext(ctx, `SELECT generation_id FROM event_content_publications WHERE event_key=?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (st *sqliteSessionTx) publishEventGeneration(owner string, meta Event, generation sqliteContentGeneration) (int64, error) {
	if generation.Owner != owner || generation.EventID != meta.ID || meta.ID == "" || meta.Text != "" {
		return 0, errors.New("Invalid content publication ownership.")
	}
	var valid bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM event_content_generations WHERE id=? AND owner_id=? AND event_id=? AND ready=1 AND payload_bytes=? AND digest=?)`, generation.ID, owner, meta.ID, generation.Bytes, generation.Digest).Scan(&valid); err != nil {
		return 0, err
	}
	if !valid {
		return 0, errors.New("Content generation is incomplete or no longer available.")
	}
	var header []byte
	if err := st.tx.QueryRowContext(st.ctx, `SELECT header FROM event_content_generations WHERE id=?`, generation.ID).Scan(&header); err != nil {
		return 0, err
	}
	if err := decodeSQLStreamHeader(header, &meta); err != nil {
		return 0, err
	}
	var ordinal int64
	err := st.tx.QueryRowContext(st.ctx, `SELECT position FROM data_session_events WHERE owner_id=? AND record_id=? ORDER BY position DESC LIMIT 1`, owner, meta.ID).Scan(&ordinal)
	if errors.Is(err, sql.ErrNoRows) {
		ordinal, err = st.appendEvent(owner, meta)
	} else if err == nil {
		err = st.writeEvent(owner, ordinal, meta, false)
	}
	if err != nil {
		return 0, err
	}
	key := sqliteOwnedKey(owner, strconv.FormatInt(ordinal, 10))
	if _, err := st.tx.ExecContext(st.ctx, `INSERT INTO event_content_publications(event_key,generation_id) VALUES(?,?) ON CONFLICT(event_key) DO UPDATE SET generation_id=excluded.generation_id`, key, generation.ID); err != nil {
		return 0, err
	}
	if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_session_events SET payload_bytes=?,digest=? WHERE id=?`, generation.Bytes, generation.Digest, key); err != nil {
		return 0, err
	}
	if generation.Nonblank && meta.Type == "assistant" && meta.ConsultationID == "" {
		if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_session_events SET message_at=? WHERE id=?`, meta.CreatedAt, key); err != nil {
			return 0, err
		}
	}
	// A full streamed event is not a small text-only append checkpoint. Native
	// readers must use its field readers instead of the old whole-event fallback.
	if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM event_stream_state WHERE event_id=?`, key); err != nil {
		return 0, err
	}
	st.changes.publishedContent = append(st.changes.publishedContent, generation.ID)
	return ordinal, nil
}
