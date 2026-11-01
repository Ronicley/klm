package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

var sqliteDomainByTable = sync.OnceValue(func() map[string]sqliteDomain {
	out := map[string]sqliteDomain{}
	tables, err := sqliteDomainTables()
	if err != nil {
		return out
	}
	for _, spec := range tables {
		out[spec.table] = spec
	}
	return out
})

func (st *sqliteSessionTx) markRecord(table, id, project string) {
	if project != "" {
		st.changes.Projects[project] = true
	}
	if st.changes.Invalidated[table] {
		return
	}
	if len(st.changes.Records[table]) >= 256 {
		delete(st.changes.Records, table)
		st.changes.Invalidated[table] = true
		return
	}
	st.changes.Records[table] = append(st.changes.Records[table], id)
}

// putRecord writes ONE leaf/metadata record. Growing children are explicit
// separate operations, never replaced implicitly by a nil/omitted struct field.
// Existing ordinal/map identity is retained; a new record uses an indexed tail.
func (st *sqliteSessionTx) putRecord(table, id, owner, mapKey string, position int64, value any) error {
	spec, ok := sqliteDomainByTable()[table]
	if !ok {
		return errors.New("unknown SQL record domain")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := st.validateDelegationWrite(table, id, owner, raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if len(raw) > 0 && raw[0] == '{' {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
	}
	for _, child := range spec.children {
		if body, exists := fields[child.field]; exists {
			// A typed zero is safe to omit from a metadata update, but nonempty
			// payloads/collections must go through their own explicit write set.
			zero := reflect.New(child.typ)
			if err := json.Unmarshal(body, zero.Interface()); err != nil {
				return err
			}
			if !zero.Elem().IsZero() {
				return errors.New("metadata write contains a split child collection")
			}
			delete(fields, child.field)
		}
	}
	if fields != nil {
		raw, err = json.Marshal(fields)
		if err != nil {
			return err
		}
	}
	if len(raw) > maxStreamRecordBytes {
		return errors.New("SQL record exceeds supported size")
	}
	var oldOwner, oldMapKey string
	var oldPosition int64
	err = st.tx.QueryRowContext(st.ctx, `SELECT owner_id,map_key,position FROM data_`+table+` WHERE id=?`, id).Scan(&oldOwner, &oldMapKey, &oldPosition)
	existing := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if existing {
		if oldOwner != owner || oldMapKey != mapKey {
			return errors.New("record ownership cannot change in a payload update")
		}
		position = oldPosition
	} else if position < 0 {
		if err := st.tx.QueryRowContext(st.ctx, `SELECT coalesce(max(position),-1)+1 FROM data_`+table+` WHERE owner_id=?`, owner).Scan(&position); err != nil {
			return err
		}
	}
	text := func(field string) (string, error) {
		var value string
		if len(fields[field]) > 0 {
			if err := json.Unmarshal(fields[field], &value); err != nil {
				return "", err
			}
		}
		return value, nil
	}
	columns := []string{"id", "projectId", "sessionId", "parentId", "status", "role", "createdAt", "updatedAt", "type", "consultationId"}
	indexed := make([]string, len(columns))
	for i, field := range columns {
		indexed[i], err = text(field)
		if err != nil {
			return err
		}
	}
	indexed[6], indexed[7] = sqliteTimeKey(indexed[6]), sqliteTimeKey(indexed[7])
	if spec.parentTable == "sessions" {
		indexed[2] = owner
	}
	_, err = st.tx.ExecContext(st.ctx, `INSERT INTO data_`+table+`(id,owner_id,map_key,position,record_id,project_id,session_id,parent_id,status,role,created_at,updated_at,kind,correlation_id,revision)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET record_id=excluded.record_id,project_id=excluded.project_id,session_id=excluded.session_id,parent_id=excluded.parent_id,status=excluded.status,role=excluded.role,created_at=excluded.created_at,updated_at=excluded.updated_at,kind=excluded.kind,correlation_id=excluded.correlation_id,revision=excluded.revision`, id, owner, mapKey, position, indexed[0], indexed[1], indexed[2], indexed[3], indexed[4], indexed[5], indexed[6], indexed[7], indexed[8], indexed[9], st.changes.Revision)
	if err != nil {
		return err
	}
	if err := updateSQLiteIndexFields(st.ctx, st.tx, table, id, raw); err != nil {
		return err
	}
	if err := refreshSQLiteLibraryReferences(st.ctx, st.tx, table, id, raw); err != nil {
		return err
	}
	if sqliteScalarProjection(table) {
		scalar, err := sqliteScalarText(raw)
		if err != nil {
			return err
		}
		if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_`+table+` SET scalar_value=? WHERE id=?`, scalar, id); err != nil {
			return err
		}
	}
	if existing {
		if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM data_`+table+`_chunks WHERE record_key=?`, id); err != nil {
			return err
		}
	}
	w := newSQLiteChunkWriter(st.ctx, st.tx, table, id)
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.finish(); err != nil {
		return err
	}
	parent := spec.parentTable
	if parent == "" {
		parent = "root"
	}
	kind := "single"
	if !spec.single {
		kind = "array"
		if spec.typ.Kind() == reflect.Map {
			kind = "object"
		}
	}
	if !existing {
		_, err = st.tx.ExecContext(st.ctx, `INSERT INTO record_collections(parent_table,parent_id,field,child_table,kind,size) VALUES(?,?,?,?,?,1)
 ON CONFLICT(parent_table,parent_id,field) DO UPDATE SET kind=excluded.kind,size=size+1`, parent, owner, spec.field, table, kind)
		if err != nil {
			return err
		}
	}
	project := indexed[1]
	if table == "projects" {
		project = id
	}
	if spec.parentTable == "projects" {
		project = owner
	}
	if spec.parentTable == "projects" || table == "sessions" {
		if _, err := st.tx.ExecContext(st.ctx, `UPDATE data_projects SET revision=? WHERE id=?`, st.changes.Revision, project); err != nil {
			return err
		}
	}
	if table == "sessions" {
		if _, ok := st.changes.Sessions[id]; !ok {
			st.changes.Sessions[id] = []int64{}
		}
	}
	if spec.parentTable == "sessions" {
		if _, ok := st.changes.Sessions[owner]; !ok {
			st.changes.Sessions[owner] = []int64{}
		}
	}
	if table == "pending_permissions" || table == "pending_questions" {
		if err := st.markManagedRequestOwner(owner); err != nil {
			return err
		}
	}
	if err := st.markDelegationRecord(table, id, owner, raw); err != nil {
		return err
	}
	st.markRecord(table, id, project)
	return nil
}

func (st *sqliteSessionTx) putRoot(table, id string, value any) error {
	spec, ok := sqliteDomainByTable()[table]
	if !ok || spec.parentTable != "" {
		return errors.New("record is not a root domain")
	}
	mapKey := id
	if spec.single {
		mapKey = ""
	}
	if !spec.single && spec.typ.Kind() == reflect.Slice {
		var old string
		err := st.tx.QueryRowContext(st.ctx, `SELECT map_key FROM data_`+table+` WHERE id=?`, id).Scan(&old)
		if err == nil {
			mapKey = old
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return st.putRecord(table, id, "", mapKey, -1, value)
}

func (st *sqliteSessionTx) putSingleChild(table, owner string, value any) error {
	spec, ok := sqliteDomainByTable()[table]
	if !ok || !spec.single || spec.parentTable == "" {
		return errors.New("record is not a child singleton")
	}
	return st.putRecord(table, sqliteOwnedKey(owner, ""), owner, "", 0, value)
}

func (st *sqliteSessionTx) deleteRecord(table, id string) error {
	if delegationStorageDomain(table) {
		return errors.New("Delegation history/receipts cannot be deleted by a state transition.")
	}
	if table == "native_sessions" {
		var value nativeSession
		err := decodeSQLiteRecord(st.ctx, st.tx, table, id, 64<<10, &value)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if value.Delegation != nil {
			return errors.New("Instrumented native history metadata cannot be silently removed.")
		}
	}
	spec, ok := sqliteDomainByTable()[table]
	if !ok {
		return errors.New("unknown SQL record domain")
	}
	var owner, project string
	if err := st.tx.QueryRowContext(st.ctx, `SELECT owner_id,project_id FROM data_`+table+` WHERE id=?`, id).Scan(&owner, &project); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	for _, child := range spec.children {
		for {
			var key string
			err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_`+child.table+` WHERE owner_id=? LIMIT 1`, id).Scan(&key)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return err
			}
			if err := st.deleteRecord(child.table, key); err != nil {
				return err
			}
		}
	}
	if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM record_collections WHERE parent_table=? AND parent_id=?`, table, id); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM data_`+table+` WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM selection_references WHERE domain=? AND record_key=?`, table, id); err != nil {
		return err
	}
	if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM credential_references WHERE domain=? AND record_key=?`, table, id); err != nil {
		return err
	}
	for _, references := range []string{"credential_connections", "capture_references"} {
		if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM `+references+` WHERE domain=? AND record_key=?`, table, id); err != nil {
			return err
		}
	}
	if _, err := st.tx.ExecContext(st.ctx, `DELETE FROM capability_views WHERE domain=? AND record_key=?`, table, id); err != nil {
		return err
	}
	parent := spec.parentTable
	if parent == "" {
		parent = "root"
	}
	if _, err := st.tx.ExecContext(st.ctx, `UPDATE record_collections SET size=size-1 WHERE parent_table=? AND parent_id=? AND field=? AND size>0`, parent, owner, spec.field); err != nil {
		return err
	}
	if spec.parentTable == "sessions" {
		if _, ok := st.changes.Sessions[owner]; !ok {
			st.changes.Sessions[owner] = []int64{}
		}
	}
	if table == "pending_permissions" || table == "pending_questions" {
		if err := st.markManagedRequestOwner(owner); err != nil {
			return err
		}
	}
	st.markRecord(table, id, project)
	return nil
}

func (st *sqliteSessionTx) sequence(key string) (uint64, error) {
	var text string
	err := st.tx.QueryRowContext(st.ctx, `SELECT coalesce((SELECT value FROM store_meta WHERE key=?),'0')`, key).Scan(&text)
	if err != nil {
		return 0, err
	}
	return parseSQLiteUint(text)
}

func (st *sqliteSessionTx) nextSequence(key string) (uint64, error) {
	current, err := st.sequence(key)
	if err != nil {
		return 0, err
	}
	if current == ^uint64(0) {
		return 0, errors.New("durable sequence exhausted")
	}
	current++
	_, err = st.tx.ExecContext(st.ctx, `INSERT INTO store_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, sqliteRevision(current))
	return current, err
}

// Project visibility is metadata, independent of UI cache or subscription state.
func (st *sqliteSessionTx) projectAvailable(id string) (bool, error) {
	var active bool
	err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_projects WHERE id=? AND x_removed<>'true')`, id).Scan(&active)
	return active, err
}

func parseSQLiteUint(value string) (uint64, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return 0, errors.New("invalid durable sequence")
	}
	return strconv.ParseUint(value, 10, 64)
}
