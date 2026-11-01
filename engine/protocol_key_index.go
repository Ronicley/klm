package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
)

type protocolDirectoryKey struct{}

func protocolDirectoryContext(ctx context.Context, dir string) context.Context {
	if value, _ := ctx.Value(protocolDirectoryKey{}).(string); value != "" {
		return ctx
	}
	return context.WithValue(ctx, protocolDirectoryKey{}, dir)
}

// Only oversized object-key sets spill here. This is a disposable private index,
// not the engine database: its transaction never owns the canonical SQL writer
// while native input is read. It preserves duplicate-key validation with bounded
// heap even for very wide useful JSON objects.
type protocolKeyIndex struct {
	db       *sql.DB
	tx       *sql.Tx
	insert   *sql.Stmt
	ctx      context.Context
	path     string
	reserved int64
}

func newProtocolKeyIndex(ctx context.Context, dir string) (*protocolKeyIndex, error) {
	if !filepath.IsAbs(dir) {
		return nil, errProtocolMetadata
	}
	stage := filepath.Join(dir, "ingestion-staging")
	if err := os.MkdirAll(stage, 0700); err != nil {
		return nil, errProtocolStagingIO
	}
	f, err := os.CreateTemp(stage, "keys-*.sqlite3")
	if err != nil {
		return nil, errProtocolStagingIO
	}
	path := f.Name()
	_ = f.Close()
	index := &protocolKeyIndex{ctx: ctx, path: path}
	index.db, err = sqlitePool(ctx, path, false, 1)
	if err == nil {
		_, err = index.db.ExecContext(ctx, `PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF; PRAGMA cache_size=-1024; CREATE TABLE object_keys(object_id INTEGER NOT NULL,key TEXT NOT NULL,PRIMARY KEY(object_id,key)) WITHOUT ROWID;`)
	}
	if err == nil {
		index.tx, err = index.db.BeginTx(ctx, nil)
	}
	if err == nil {
		index.insert, err = index.tx.PrepareContext(ctx, `INSERT INTO object_keys VALUES(?,?)`)
	}
	if err != nil {
		_ = index.Close()
		return nil, errProtocolStagingIO
	}
	return index, nil
}

func (i *protocolKeyIndex) add(object int64, key string) error {
	if err := i.ctx.Err(); err != nil {
		return err
	}
	free, err := protocolFreeDisk(filepath.Dir(i.path))
	if err != nil {
		return errProtocolStagingIO
	}
	if free < protocolDiskReserve+128<<10 {
		return errProtocolStaging
	}
	reservation := int64(len(key) + 128)
	protocolStagingBudget.Lock()
	if reservation > protocolStagingLimit-protocolStagingBudget.bytes {
		protocolStagingBudget.Unlock()
		return errProtocolStaging
	}
	protocolStagingBudget.bytes += reservation
	i.reserved += reservation
	protocolStagingBudget.Unlock()
	if _, err := i.insert.ExecContext(i.ctx, object, key); err != nil {
		var code interface{ Code() int }
		if errors.As(err, &code) && code.Code()&255 == 19 {
			return errProtocolJSON
		}
		if i.ctx.Err() != nil {
			return i.ctx.Err()
		}
		return errProtocolStagingIO
	}
	return nil
}

func (i *protocolKeyIndex) drop(object int64) error {
	if _, err := i.tx.ExecContext(i.ctx, `DELETE FROM object_keys WHERE object_id=?`, object); err != nil {
		if i.ctx.Err() != nil {
			return i.ctx.Err()
		}
		return errProtocolStagingIO
	}
	return nil
}

func (i *protocolKeyIndex) Close() error {
	if i.insert != nil {
		_ = i.insert.Close()
	}
	if i.tx != nil {
		_ = i.tx.Rollback()
	}
	if i.db != nil {
		_ = i.db.Close()
	}
	if i.path != "" {
		if err := os.Remove(i.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errProtocolStagingIO
		}
	}
	protocolStagingBudget.Lock()
	protocolStagingBudget.bytes -= i.reserved
	protocolStagingBudget.Unlock()
	i.reserved = 0
	i.path = ""
	return nil
}
