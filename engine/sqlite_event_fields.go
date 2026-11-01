package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// Decode one JSON string incrementally, including escapes/surrogate pairs. The
// canonical source has already been validated, but corrupt stored input still
// fails explicitly. No Decoder.Token string or whole-event hydration is used.
type jsonStringReader struct {
	r              *bufio.Reader
	started, ended bool
	pending        []byte
	encoded        [utf8.UTFMax]byte
}

var errJSONStringChunkFull = errors.New("decoded string chunk complete")

func (r *jsonStringReader) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	if len(r.pending) > 0 {
		n := copy(out, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	if r.ended {
		return 0, io.EOF
	}
	if !r.started {
		b, err := r.r.ReadByte()
		if err != nil || b != '"' {
			return 0, errProtocolJSON
		}
		r.started = true
	}
	n := 0
	err := jsonStreamString(r.r, func(value rune) error {
		encoded := utf8.AppendRune(r.encoded[:0], value)
		written := copy(out[n:], encoded)
		n += written
		if written < len(encoded) {
			r.pending = encoded[written:]
		}
		if n == len(out) {
			return errJSONStringChunkFull
		}
		return nil
	})
	if err == nil {
		r.ended = true
	}
	if err != nil && !errors.Is(err, errJSONStringChunkFull) {
		if errors.Is(err, io.EOF) {
			err = errProtocolJSON
		}
		return n, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// Explicit metadata-only read. Text is always obtained through the field reader;
// native data in streamed generations remains behind canonical bounded content.
func readSQLiteEventMetadata(ctx context.Context, q sqliteQuery, key string) (Event, error) {
	var e Event
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT g.header FROM event_content_publications p JOIN event_content_generations g ON g.id=p.generation_id WHERE p.event_key=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		err = q.QueryRowContext(ctx, `SELECT header FROM event_stream_state WHERE event_id=?`, key).Scan(&raw)
	}
	if errors.Is(err, sql.ErrNoRows) {
		reader, err := newSQLiteContentReader(ctx, q, "session_events", key)
		if err != nil {
			return e, err
		}
		policy := func(path []string) projectionAction {
			if len(path) == 1 && path[0] == "text" {
				return projectionDiscard
			}
			if len(path) == 2 && path[0] == "data" {
				return projectionSmallValue
			}
			return projectionKeep
		}
		raw, _, err = projectProtocolJSON(ctx, reader, policy, 1<<20, errProtocolMetadata, false)
		if err != nil {
			return e, err
		}
	} else if err != nil {
		return e, err
	}
	if len(raw) > 1<<20 {
		return e, errProtocolMetadata
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	err = decoder.Decode(&e)
	e.Text = ""
	return e, err
}

func readSQLiteEventText(ctx context.Context, q sqliteQuery, key string) (io.Reader, error) {
	var offset, length int64
	err := q.QueryRowContext(ctx, `SELECT g.text_offset,g.text_bytes FROM event_content_publications p JOIN event_content_generations g ON g.id=p.generation_id WHERE p.event_key=?`, key).Scan(&offset, &length)
	if errors.Is(err, sql.ErrNoRows) {
		err = q.QueryRowContext(ctx, `SELECT prefix_bytes-1,text_bytes+2 FROM event_stream_state WHERE event_id=?`, key).Scan(&offset, &length)
	}
	if errors.Is(err, sql.ErrNoRows) {
		reader, err := newSQLiteContentReader(ctx, q, "session_events", key)
		if err != nil {
			return nil, err
		}
		_, _, err = projectProtocolJSON(ctx, reader, nil, 0, nil, true, func(path []string, start, end int64, kind byte) {
			if len(path) == 1 && path[0] == "text" && kind == '"' {
				offset, length = start, end-start
			}
		})
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if length == 0 {
		return strings.NewReader(""), nil
	}
	reader, err := newSQLiteContentReader(ctx, q, "session_events", key)
	if err != nil {
		return nil, err
	}
	if offset < 0 || length < 2 || offset > reader.size-length {
		return nil, errors.New("Invalid stored text range.")
	}
	reader.offset = offset
	return &jsonStringReader{r: bufio.NewReaderSize(io.LimitReader(reader, length), protocolChunkSize)}, nil
}

// Offsets and limits count Unicode code points, matching linked/general history
// semantics. Only the requested window is materialized, never []rune(fullText).
func sqliteEventTextWindow(ctx context.Context, q sqliteQuery, key string, offset, limit int) (string, bool, error) {
	if offset < 0 || limit < 0 || limit > 64<<10 {
		return "", false, errors.New("Invalid text window.")
	}
	text, err := readSQLiteEventText(ctx, q, key)
	if err != nil {
		return "", false, err
	}
	return readRuneTextWindow(ctx, text, offset, limit)
}

func readRuneTextWindow(ctx context.Context, text io.Reader, offset, limit int) (string, bool, error) {
	if offset < 0 || limit < 0 || limit > 64<<10 {
		return "", false, errors.New("Invalid text window.")
	}
	r := bufio.NewReaderSize(text, protocolChunkSize)
	var result strings.Builder
	for index := 0; ; index++ {
		if index%16384 == 0 {
			if err := ctx.Err(); err != nil {
				return "", false, err
			}
		}
		value, _, err := r.ReadRune()
		if err == io.EOF {
			return result.String(), false, nil
		}
		if err != nil {
			return "", false, err
		}
		if index < offset {
			continue
		}
		if index-offset == limit {
			return result.String(), true, nil
		}
		result.WriteRune(value)
	}
}
