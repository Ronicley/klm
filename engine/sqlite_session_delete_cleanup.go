package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func managedDeletionBase(dir, child string) (string, error) {
	base := filepath.Join(dir, child)
	info, err := os.Lstat(base)
	if errors.Is(err, os.ErrNotExist) {
		return base, nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Managed cleanup directory is not a regular directory: " + child)
	}
	return base, nil
}

func cleanupSQLSessionArtifacts(dir, id string, record deletedSession) error {
	attachments, err := managedDeletionBase(dir, "attachments")
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(id))
	// RemoveAll removes links themselves, never traversing directory links below
	// the checked base. Project folders and worktrees are never cleanup targets.
	if err := os.RemoveAll(filepath.Join(attachments, hex.EncodeToString(hash[:]))); err != nil {
		return err
	}
	native, err := managedDeletionBase(dir, "sessions")
	if err != nil {
		return err
	}
	files, err := os.ReadDir(native)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, file := range files {
		name := file.Name()
		owned := name == id+".jsonl"
		if strings.HasPrefix(name, id+"-") && strings.HasSuffix(name, ".jsonl") {
			suffix, err := hex.DecodeString(strings.TrimSuffix(strings.TrimPrefix(name, id+"-"), ".jsonl"))
			owned = err == nil && len(suffix) == 16
		}
		if owned && file.IsDir() {
			return errors.New("Managed native cleanup target is a directory.")
		}
		if owned {
			if err := os.Remove(filepath.Join(native, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	images, err := managedDeletionBase(dir, "images")
	if err != nil {
		return err
	}
	for _, image := range record.Images {
		if image == "" || image == "." || image == ".." || filepath.Base(image) != image || strings.ContainsAny(image, "/\\:") {
			return errors.New("Invalid managed image identity; cleanup was not completed.")
		}
		if err := os.Remove(filepath.Join(images, image)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (a *app) cleanupSQLDeletedSessions(root string) error {
	// Retry and startup share the same durable worklist. No SQL checkpoint,
	// commitLocked, legacy journal rotation or tombstone inventory in app.state.
	a.checkpointMu.Lock()
	defer a.checkpointMu.Unlock()
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	after := ""
	var cleanupErr error
	for {
		query := `SELECT id FROM data_deleted_sessions WHERE id>? AND status<>'cleaned'`
		args := []any{after}
		if root != "" {
			query += ` AND (id=? OR parent_id=? OR parent_id='')`
			args = append(args, root, root)
		}
		query += ` ORDER BY id LIMIT 100`
		ids, err := sqliteQueryIDs(ctx, a.store.readers, query, args...)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return cleanupErr
		}
		for _, id := range ids {
			after = id
			var record deletedSession
			if err := decodeSQLiteRecord(ctx, a.store.readers, "deleted_sessions", id, maxStreamRecordBytes, &record); err != nil {
				return err
			}
			// Schema-5 imports have no routing projection. Decode bounded
			// candidates, but never clean or acknowledge another root's records.
			if root != "" && record.RootID != root {
				continue
			}
			if record.CleanupPending {
				if err := cleanupSQLSessionArtifacts(a.dir, id, record); err != nil {
					cleanupErr = errors.Join(cleanupErr, err)
					continue
				}
			}
			a.mu.Lock()
			err := a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
				record.CleanupPending, record.Images = false, nil
				if err := st.putRoot("deleted_sessions", id, record); err != nil {
					return err
				}
				_, err := st.tx.ExecContext(st.ctx, `UPDATE data_deleted_sessions SET parent_id=?,status='cleaned' WHERE id=?`, record.RootID, id)
				return err
			})
			a.mu.Unlock()
			if err != nil {
				return errors.Join(cleanupErr, err)
			}
		}
	}
}
