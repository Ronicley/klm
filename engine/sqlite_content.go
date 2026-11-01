package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

type sqliteQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Table names are internal, compile-time domain names; never pass a client string
// into SQL. Validate even internal calls so future dispatch cannot inject SQL.
var sqliteKnownTables = sync.OnceValue(func() map[string]bool {
	result := map[string]bool{}
	tables, err := sqliteDomainTables()
	if err == nil {
		for _, spec := range tables {
			result[spec.table] = true
		}
	}
	return result
})

func sqliteKnownTable(table string) bool { return sqliteKnownTables()[table] }

type sqliteContentPage struct {
	Data       []byte `json:"data"` // Base64, so chunks never form partial UTF-8/JSON strings.
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"nextOffset"`
	TotalBytes int64  `json:"totalBytes"`
	Digest     string `json:"digest"`
	HasMore    bool   `json:"hasMore"`
}

var errSQLiteContentChanged = errors.New("content changed; reload its metadata")

func readSQLiteContent(ctx context.Context, q sqliteQuery, table, id, digest string, offset int64, limit int) (sqliteContentPage, error) {
	page := sqliteContentPage{Offset: offset}
	if !sqliteKnownTable(table) || offset < 0 || limit < 1 || limit > sqliteContentChunkBytes {
		return page, errors.New("invalid SQLite content request")
	}
	if err := q.QueryRowContext(ctx, `SELECT payload_bytes,digest FROM data_`+table+` WHERE id=?`, id).Scan(&page.TotalBytes, &page.Digest); err != nil {
		return page, err
	}
	if digest == "" || digest != page.Digest {
		return page, errSQLiteContentChanged
	}
	if offset > page.TotalBytes {
		return page, errors.New("content offset is out of range")
	}
	end := min(page.TotalBytes, offset+int64(limit))
	page.Data = make([]byte, end-offset)
	if end > offset {
		reader, err := newSQLiteContentReader(ctx, q, table, id)
		if err != nil {
			return page, err
		}
		reader.offset = offset
		if _, err := io.ReadFull(reader, page.Data); err != nil {
			return page, err
		}
	}
	if int64(len(page.Data)) != end-offset {
		return page, errors.New("incomplete SQLite content")
	}
	page.NextOffset, page.HasMore = end, end < page.TotalBytes
	return page, nil
}

// writeSQLiteRecordContent must run in a short read transaction or the import
// writer transaction, giving a consistent content snapshot. It streams bounded
// chunks and never allocates a complete transcript, snapshot or large event.
func writeSQLiteRecordContent(ctx context.Context, q sqliteQuery, w io.Writer, table, id string) error {
	if !sqliteKnownTable(table) {
		return errors.New("unknown SQLite content table")
	}
	reader, err := newSQLiteContentReader(ctx, q, table, id)
	if err != nil {
		return err
	}
	total, err := io.CopyBuffer(w, reader, make([]byte, sqliteContentChunkBytes))
	if err != nil {
		return err
	}
	if total != reader.size {
		return errors.New("SQLite content length mismatch")
	}
	return nil
}

// Verifier/export path ONLY. Runtime callers load metadata and requested child
// collections separately. This reconstructs a domain record into a stream, not
// a global diskState or a resident history. Caller owns a consistent transaction.
func emitSQLiteDomainRecord(ctx context.Context, q sqliteQuery, w io.Writer, spec sqliteDomain, id string) error {
	if len(spec.children) == 0 {
		return writeSQLiteRecordContent(ctx, q, w, spec.table, id)
	}
	// Metadata has no growing children; use SQL byte windows to stream it minus
	// its final object brace, then append the split fields one by one.
	var size int64
	var digest string
	if err := q.QueryRowContext(ctx, `SELECT payload_bytes,digest FROM data_`+spec.table+` WHERE id=?`, id).Scan(&size, &digest); err != nil {
		return err
	}
	first, err := readSQLiteContent(ctx, q, spec.table, id, digest, 0, 1)
	if err != nil {
		return err
	}
	if len(first.Data) == 0 || first.Data[0] != '{' {
		return writeSQLiteRecordContent(ctx, q, w, spec.table, id)
	}
	var offset int64
	for offset < size-1 {
		page, err := readSQLiteContent(ctx, q, spec.table, id, digest, offset, int(min(int64(sqliteContentChunkBytes), size-1-offset)))
		if err != nil {
			return err
		}
		if _, err := w.Write(page.Data); err != nil {
			return err
		}
		offset = page.NextOffset
	}
	empty := size == 2
	for _, child := range spec.children {
		var kind string
		if err := q.QueryRowContext(ctx, `SELECT kind FROM record_collections WHERE parent_table=? AND parent_id=? AND field=?`, spec.table, id, child.field).Scan(&kind); errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return err
		}
		if !empty {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		empty = false
		field, _ := json.Marshal(child.field)
		if _, err := w.Write(append(field, ':')); err != nil {
			return err
		}
		if err := emitSQLiteDomainCollection(ctx, q, w, child, id, kind); err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "}")
	return err
}

func emitSQLiteDomainCollection(ctx context.Context, q sqliteQuery, w io.Writer, spec sqliteDomain, owner, kind string) error {
	if kind == "null" {
		_, err := io.WriteString(w, "null")
		return err
	}
	if kind == "single" {
		id := sqliteOwnedKey(owner, "")
		if owner == "" {
			id = "settings"
		}
		return emitSQLiteDomainRecord(ctx, q, w, spec, id)
	}
	if kind != "array" && kind != "object" {
		return fmt.Errorf("unknown collection kind %s", kind)
	}
	start, end := "[", "]"
	if kind == "object" {
		start, end = "{", "}"
	}
	if _, err := io.WriteString(w, start); err != nil {
		return err
	}
	var previous int64 = -1
	for {
		var id, key string
		var position int64
		err := q.QueryRowContext(ctx, `SELECT id,map_key,position FROM data_`+spec.table+` WHERE owner_id=? AND position>? ORDER BY position LIMIT 1`, owner, previous).Scan(&id, &key, &position)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		if previous >= 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if kind == "object" {
			encoded, _ := json.Marshal(key)
			if _, err := w.Write(append(encoded, ':')); err != nil {
				return err
			}
		}
		if err := emitSQLiteDomainRecord(ctx, q, w, spec, id); err != nil {
			return err
		}
		previous = position
	}
	_, err := io.WriteString(w, end)
	return err
}
