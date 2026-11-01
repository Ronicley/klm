package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// prepareSQLiteImport is the production, non-executing importer. This is not
// startup recovery: no native process, provider call or outbox write occurs here.
// The cutover coordinator retains originals and handles publication separately.
func prepareSQLiteImport(ctx context.Context, store *sqliteStore, backup string) error {
	f, err := os.Open(filepath.Join(backup, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		for _, name := range []string{"stream.sealed", "stream.journal"} {
			if _, err := os.Stat(filepath.Join(backup, name)); err == nil {
				return errors.New("legacy journal exists without its checkpoint; recovery required")
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		err = stageLegacyCheckpoint(ctx, store, strings.NewReader(`{"version":2,"projects":[],"sessions":[],"native":{},"engineSettings":{},"transcription":{}}`))
	} else if err == nil {
		err = stageLegacyCheckpoint(ctx, store, f)
		closeErr := f.Close()
		err = errors.Join(err, closeErr)
	}
	if err != nil {
		return err
	}
	if err := normalizeLegacyCheckpoint(ctx, store); err != nil {
		return err
	}
	if _, err := replayLegacyStage(ctx, store, backup); err != nil {
		return err
	}
	if err := normalizeLegacyPostReplay(ctx, store); err != nil {
		return err
	}
	if err := migrateLegacyStageTopics(ctx, store); err != nil {
		return err
	}
	if err := importStagedDomains(ctx, store); err != nil {
		return err
	}
	if err := importSQLiteAuth(ctx, store, backup); err != nil {
		return err
	}
	if err := validateSQLiteImport(ctx, store); err != nil {
		return err
	}
	return store.write(ctx, func(tx *sql.Tx) error {
		report, err := verifySQLiteImport(ctx, tx)
		if err != nil {
			return err
		}
		body, err := json.Marshal(report)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO store_meta(key,value) VALUES('import_verification',?),('domain_validation','complete')`, string(body)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DROP TABLE migration_record_refs; DROP TABLE migration_collection_refs; DROP TABLE legacy_nodes;`)
		return err
	})
}

func validateSQLiteImport(ctx context.Context, s *sqliteStore) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		v := sqliteValidation{ctx: ctx, tx: tx}
		for _, validate := range []func() error{v.validateIdentities, v.validateSessionsAndTopics, v.validateCollaboration, v.validateWebhooks, v.validateBoard, v.validateAnalytics, v.validateAutomations, v.validateGraphs, v.validateDelegationRecords} {
			if err := validate(); err != nil {
				return err
			}
		}
		return nil
	})
}

type sqliteImportDomainReport struct {
	Records int64  `json:"records"`
	Bytes   int64  `json:"bytes"`
	Digest  string `json:"digest"`
}

// Validate every normalized row against its source staging record, not just the
// SQL writer's own checksum. Re-read actual chunks and check their SHA, byte count,
// routing indexes, source coverage, and all split collection counts/shape. Each
// iteration retains only one 64 KiB chunk plus one scalar source value.
func verifySQLiteImport(ctx context.Context, tx *sql.Tx) (map[string]sqliteImportDomainReport, error) {
	tables, err := sqliteDomainTables()
	if err != nil {
		return nil, err
	}
	report := map[string]sqliteImportDomainReport{}
	v := sqliteValidation{ctx: ctx, tx: tx}
	stage := legacyStage{ctx: ctx, tx: tx}
	var collections, collectionRefs int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM record_collections`).Scan(&collections); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM migration_collection_refs`).Scan(&collectionRefs); err != nil {
		return nil, err
	}
	if collections != collectionRefs {
		return nil, errors.New("import collection coverage mismatch")
	}
	roots, err := sqliteDomains()
	if err != nil {
		return nil, err
	}
	for _, root := range roots {
		node, err := stage.child(1, root.field)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var reference int64
		if err := tx.QueryRowContext(ctx, `SELECT source_node FROM migration_collection_refs WHERE parent_table='root' AND parent_id='' AND field=?`, root.field).Scan(&reference); err != nil {
			return nil, err
		}
		if reference != node {
			return nil, errors.New("root collection source mismatch")
		}
	}
	for _, spec := range tables {
		var total, refs int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM data_`+spec.table).Scan(&total); err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM migration_record_refs WHERE table_name=?`, spec.table).Scan(&refs); err != nil {
			return nil, err
		}
		if total != refs {
			return nil, fmt.Errorf("import record coverage mismatch: %s", spec.table)
		}
		digest := sha256.New()
		domain := sqliteImportDomainReport{Records: total}
		if err := v.each(spec.table, nil, func(id string) error {
			var source, expectedBytes int64
			var expectedDigest string
			if err := tx.QueryRowContext(ctx, `SELECT r.source_node,d.payload_bytes,d.digest FROM migration_record_refs r JOIN data_`+spec.table+` d ON d.id=r.record_key WHERE r.table_name=? AND r.record_key=?`, spec.table, id).Scan(&source, &expectedBytes, &expectedDigest); err != nil {
				return err
			}
			sourceHash := sha256.New()
			counter := &sqliteCountingWriter{writer: sourceHash}
			if err := stage.emitMetadata(counter, source, spec.children); err != nil {
				return err
			}
			if counter.bytes != expectedBytes || hex.EncodeToString(sourceHash.Sum(nil)) != expectedDigest {
				return errors.New("source/SQL metadata digest mismatch")
			}
			actual := sha256.New()
			if err := writeSQLiteRecordContent(ctx, tx, actual, spec.table, id); err != nil {
				return err
			}
			if hex.EncodeToString(actual.Sum(nil)) != expectedDigest {
				return errors.New("SQL content checksum mismatch")
			}
			var owner, mapKey string
			var position int64
			if err := tx.QueryRowContext(ctx, `SELECT owner_id,map_key,position FROM data_`+spec.table+` WHERE id=?`, id).Scan(&owner, &mapKey, &position); err != nil {
				return err
			}
			var sourceName string
			var sourcePosition int64
			if err := tx.QueryRowContext(ctx, `SELECT name,position FROM legacy_nodes WHERE id=?`, source).Scan(&sourceName, &sourcePosition); err != nil {
				return err
			}
			if spec.single {
				sourceName, sourcePosition = "", 0
			}
			if sourceName != mapKey || sourcePosition != position {
				return errors.New("record position/map identity mismatch")
			}
			for _, f := range []struct{ json, column string }{{"id", "record_id"}, {"projectId", "project_id"}, {"sessionId", "session_id"}, {"parentId", "parent_id"}, {"status", "status"}, {"role", "role"}, {"createdAt", "created_at"}, {"updatedAt", "updated_at"}, {"type", "kind"}, {"consultationId", "correlation_id"}} {
				want, err := stage.stringField(source, f.json)
				if err != nil {
					return err
				}
				if f.json == "sessionId" && spec.parentTable == "sessions" {
					want = owner
				}
				if f.json == "createdAt" || f.json == "updatedAt" {
					want = sqliteTimeKey(want)
				}
				var got string
				if err := tx.QueryRowContext(ctx, `SELECT `+f.column+` FROM data_`+spec.table+` WHERE id=?`, id).Scan(&got); err != nil {
					return err
				}
				if got != want {
					return errors.New("record header/source mismatch")
				}
			}
			for _, child := range spec.children {
				node, err := stage.child(source, child.field)
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				var reference int64
				if err := tx.QueryRowContext(ctx, `SELECT source_node FROM migration_collection_refs WHERE parent_table=? AND parent_id=? AND field=?`, spec.table, id, child.field).Scan(&reference); err != nil {
					return err
				}
				if reference != node {
					return errors.New("split field/source mismatch")
				}
			}
			for _, field := range sqliteIndexFields[spec.table] {
				want, err := stage.projectedScalar(source, field)
				if err != nil {
					return err
				}
				var got string
				if err := tx.QueryRowContext(ctx, `SELECT `+sqliteIndexColumn(field)+` FROM data_`+spec.table+` WHERE id=?`, id).Scan(&got); err != nil {
					return err
				}
				if sqliteIndexedValue(field, want) != got {
					return errors.New("SQL routing projection mismatch")
				}
			}
			encoded, _ := json.Marshal([]string{id, expectedDigest})
			digest.Write(encoded)
			domain.Bytes += expectedBytes
			return nil
		}); err != nil {
			return nil, fmt.Errorf("verify %s: %w", spec.table, err)
		}
		domain.Digest = hex.EncodeToString(digest.Sum(nil))
		report[spec.table] = domain
	}
	var cursor int64
	for {
		var rowid, node, size int64
		var parentTable, parent, field, childTable, kind string
		err := tx.QueryRowContext(ctx, `SELECT c.rowid,c.parent_table,c.parent_id,c.field,c.child_table,c.kind,c.size,r.source_node FROM record_collections c JOIN migration_collection_refs r USING(parent_table,parent_id,field) WHERE c.rowid>? ORDER BY c.rowid LIMIT 1`, cursor).Scan(&rowid, &parentTable, &parent, &field, &childTable, &kind, &size, &node)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, err
		}
		if !sqliteKnownTable(childTable) {
			return nil, errors.New("unknown collection table")
		}
		var actual int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM data_`+childTable+` WHERE owner_id=?`, parent).Scan(&actual); err != nil {
			return nil, err
		}
		if actual != size {
			return nil, errors.New("import collection count mismatch")
		}
		n, err := stage.node(node)
		if err != nil {
			return nil, err
		}
		if kind == "single" {
			if size != 1 {
				return nil, errors.New("invalid singleton import")
			}
		} else if kind == "null" {
			if n.kind != "scalar" || string(n.scalar) != "null" || size != 0 {
				return nil, errors.New("null collection mismatch")
			}
		} else {
			var expected int64
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM legacy_nodes WHERE parent=?`, node).Scan(&expected); err != nil {
				return nil, err
			}
			if kind != n.kind || expected != size {
				return nil, errors.New("source collection mismatch")
			}
		}
		comparison := "n.parent<>?"
		if kind == "single" {
			comparison = "n.id<>?"
		}
		if err := v.rejectRows("record collection ownership", `SELECT 1 FROM data_`+childTable+` d JOIN migration_record_refs r ON r.table_name=? AND r.record_key=d.id JOIN legacy_nodes n ON n.id=r.source_node WHERE d.owner_id=? AND (`+comparison+`)`, childTable, parent, node); err != nil {
			return nil, err
		}
		cursor = rowid
	}
	return report, nil
}

type sqliteCountingWriter struct {
	writer io.Writer
	bytes  int64
}

func (w *sqliteCountingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.bytes += int64(n)
	return n, err
}
