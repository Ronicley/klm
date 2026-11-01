package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
)

// Caller owns a read snapshot. At most one chunk is resident and no SQL rows or
// writer transaction remain open while the caller processes its bytes.
type sqliteContentReader struct {
	ctx                    context.Context
	q                      sqliteQuery
	table, key, generation string
	offset, size           int64
	chunk                  int64
	buffer                 []byte
	baseID                 string
	baseBytes, appendBytes int64
	base                   *sqliteContentReader
	appendOffset           int64
}

func newSQLiteContentReader(ctx context.Context, q sqliteQuery, table, key string) (*sqliteContentReader, error) {
	if !sqliteKnownTable(table) {
		return nil, errors.New("Unknown content domain.")
	}
	r := &sqliteContentReader{ctx: ctx, q: q, table: table, key: key, chunk: -1}
	if err := q.QueryRowContext(ctx, `SELECT payload_bytes FROM data_`+table+` WHERE id=?`, key).Scan(&r.size); err != nil {
		return nil, err
	}
	var err error
	r.generation, err = sqlitePublishedGeneration(ctx, q, table, key)
	if err == nil && r.generation != "" {
		var base sql.NullString
		err = q.QueryRowContext(ctx, `SELECT base_id,base_bytes,append_bytes FROM event_content_generations WHERE id=?`, r.generation).Scan(&base, &r.baseBytes, &r.appendBytes)
		if base.Valid {
			r.baseID = base.String
			r.base = &sqliteContentReader{ctx: ctx, q: q, generation: base.String, size: r.baseBytes, chunk: -1}
			r.appendOffset = -1
		}
	}
	return r, err
}

func (r *sqliteContentReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.offset >= r.size {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.base != nil {
		return r.readAppended(p)
	}
	chunk := r.offset / sqliteContentChunkBytes
	if r.chunk != chunk {
		query, key := `SELECT body FROM data_`+r.table+`_chunks WHERE record_key=? AND chunk=?`, r.key
		if r.generation != "" {
			query, key = `SELECT body FROM event_content_generation_chunks WHERE generation_id=? AND chunk=?`, r.generation
		}
		if err := r.q.QueryRowContext(r.ctx, query, key, chunk).Scan(&r.buffer); err != nil {
			return 0, err
		}
		if len(r.buffer) == 0 || len(r.buffer) > sqliteContentChunkBytes {
			return 0, errors.New("Invalid content chunk.")
		}
		r.chunk = chunk
	}
	start := int(r.offset % sqliteContentChunkBytes)
	if start >= len(r.buffer) {
		return 0, errors.New("Incomplete content chunk.")
	}
	n := copy(p, r.buffer[start:min(len(r.buffer), start+int(min(int64(len(p)), r.size-r.offset)))])
	r.offset += int64(n)
	return n, nil
}

func (r *sqliteContentReader) readAppended(out []byte) (int, error) {
	if r.offset < r.baseBytes {
		r.base.offset = r.offset
		n, err := r.base.Read(out[:min(int64(len(out)), r.baseBytes-r.offset)])
		r.offset += int64(n)
		if err == io.EOF && r.offset < r.size {
			err = nil
		}
		return n, err
	}
	if r.offset < r.baseBytes+r.appendBytes {
		offset := r.offset - r.baseBytes
		if r.appendOffset < 0 || offset < r.appendOffset || offset >= r.appendOffset+int64(len(r.buffer)) {
			if err := r.q.QueryRowContext(r.ctx, `SELECT offset,body FROM event_content_appends WHERE base_id=? AND offset<=? ORDER BY offset DESC LIMIT 1`, r.baseID, offset).Scan(&r.appendOffset, &r.buffer); err != nil {
				return 0, err
			}
		}
		start := offset - r.appendOffset
		if start < 0 || start >= int64(len(r.buffer)) || len(r.buffer) > sqliteContentChunkBytes {
			return 0, errProtocolJSON
		}
		n := copy(out, r.buffer[start:min(int64(len(r.buffer)), start+min(int64(len(out)), r.appendBytes-offset))])
		r.offset += int64(n)
		return n, nil
	}
	suffix := []byte(`"}`)
	offset := r.offset - r.baseBytes - r.appendBytes
	if offset < 0 || offset >= 2 || r.size != r.baseBytes+r.appendBytes+2 {
		return 0, errProtocolJSON
	}
	n := copy(out, suffix[offset:])
	r.offset += int64(n)
	return n, nil
}
