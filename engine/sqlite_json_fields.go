package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
)

type sqliteJSONRange struct {
	offset, bytes int64
	kind          byte
}

var errSQLiteFieldFound = errors.New("stored JSON field found")

func sqliteRangeReader(ctx context.Context, q sqliteQuery, key string, value sqliteJSONRange) (io.Reader, error) {
	r, err := newSQLiteContentReader(ctx, q, "session_events", key)
	if err != nil {
		return nil, err
	}
	if value.offset < 0 || value.bytes < 0 || value.offset > r.size-value.bytes {
		return nil, errProtocolJSON
	}
	r.offset = value.offset
	return io.LimitReader(r, value.bytes), nil
}

// Iterate committed object fields, retaining only one small key and a source
// range. Nested useful objects/strings are validated without materialization.
func walkSQLiteJSONFields(ctx context.Context, input io.Reader, origin int64, visit func(string, sqliteJSONRange) error) error {
	counter := &protocolCountingReader{input: input, bytes: origin}
	p := &protocolJSONProjection{ctx: ctx, r: bufio.NewReaderSize(counter, protocolChunkSize), counter: counter}
	b, err := p.nonSpace()
	if err != nil || b != '{' {
		return errProtocolJSON
	}
	for {
		b, err := p.nonSpace()
		if err != nil {
			return err
		}
		if b == '}' {
			return nil
		}
		if b != '"' {
			return errProtocolJSON
		}
		raw, err := p.smallString(4096)
		if err != nil {
			return err
		}
		var key string
		if err := json.Unmarshal(raw, &key); err != nil {
			return err
		}
		b, err = p.nonSpace()
		if err != nil || b != ':' {
			return errProtocolJSON
		}
		b, err = p.nonSpace()
		if err != nil {
			return err
		}
		start := counter.bytes - int64(p.r.Buffered()) - 1
		_ = p.r.UnreadByte()
		if err := p.value(nil, 0, nil); err != nil {
			return err
		}
		if err := visit(key, sqliteJSONRange{offset: start, bytes: counter.bytes - int64(p.r.Buffered()) - start, kind: b}); err != nil {
			return err
		}
		b, err = p.nonSpace()
		if err != nil {
			return err
		}
		if b == '}' {
			return nil
		}
		if b != ',' {
			return errProtocolJSON
		}
	}
}

func sqliteEventFieldRange(ctx context.Context, q sqliteQuery, key, name string) (sqliteJSONRange, bool, error) {
	r, err := newSQLiteContentReader(ctx, q, "session_events", key)
	if err != nil {
		return sqliteJSONRange{}, false, err
	}
	var found sqliteJSONRange
	err = walkSQLiteJSONFields(ctx, r, 0, func(field string, value sqliteJSONRange) error {
		if field != name {
			return nil
		}
		found = value
		return errSQLiteFieldFound
	})
	if errors.Is(err, errSQLiteFieldFound) {
		return found, true, nil
	}
	return found, false, err
}

func writeMergedSQLiteEventData(ctx context.Context, q sqliteQuery, w io.Writer, key string, updates map[string]any) error {
	if key == "" {
		return writeProtocolJSON(w, updates)
	}
	value, found, err := sqliteEventFieldRange(ctx, q, key, "data")
	if err != nil {
		return err
	}
	if !found || value.kind == 'n' {
		return writeProtocolJSON(w, updates)
	}
	r, err := sqliteRangeReader(ctx, q, key, value)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, "{"); err != nil {
		return err
	}
	first := true
	emitKey := func(name string) error {
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		first = false
		raw, err := json.Marshal(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(raw); err != nil {
			return err
		}
		_, err = io.WriteString(w, ":")
		return err
	}
	err = walkSQLiteJSONFields(ctx, r, value.offset, func(name string, span sqliteJSONRange) error {
		if _, replaced := updates[name]; replaced {
			return nil
		}
		if err := emitKey(name); err != nil {
			return err
		}
		source, err := sqliteRangeReader(ctx, q, key, span)
		if err != nil {
			return err
		}
		_, err = io.CopyBuffer(w, source, make([]byte, protocolChunkSize))
		return err
	})
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(updates))
	for name := range updates {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if err := emitKey(name); err != nil {
			return err
		}
		if err := writeProtocolJSON(w, updates[name]); err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "}")
	return err
}
