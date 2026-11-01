package main

// This tree is IMPORT-ONLY positional staging for the old patch language. It is
// never a runtime repository or an authoritative schema. Collections are rows,
// not JSON arrays. It permits bounded decoding of old histories and exact integer
// preservation before conversion to the domain tables and removal of this tree.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

const legacyStageSchema = `
CREATE TABLE legacy_nodes(
 id INTEGER PRIMARY KEY,
 parent INTEGER REFERENCES legacy_nodes(id) ON DELETE CASCADE,
 name TEXT NOT NULL, position INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('object','array','scalar')),
 scalar BLOB, size INTEGER NOT NULL DEFAULT 0, quoted INTEGER NOT NULL DEFAULT 0,
 UNIQUE(parent,name)
) STRICT;
CREATE INDEX legacy_nodes_position ON legacy_nodes(parent,position);
`

type legacyStage struct {
	ctx context.Context
	tx  *sql.Tx
}

type legacyNode struct {
	id, size int64
	kind     string
	scalar   []byte
	quoted   bool
}

var legacyRawType = reflect.TypeOf(json.RawMessage{})

func legacyJSONType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == legacyRawType || t != nil && t.Kind() == reflect.Interface {
		return nil // Deliberately open JSON payload, not a missing domain mapping.
	}
	return t
}

func legacyField(t reflect.Type, name string) (reflect.Type, bool) {
	t = legacyJSONType(t)
	if t == nil {
		return nil, true
	}
	if t.Kind() == reflect.Map {
		return t.Elem(), true
	}
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.Anonymous && key == "" {
			continue // encoding/json promotes these fields into their parent object.
		}
		if key == "" {
			key = field.Name
		}
		if key != "-" && key == name {
			return field.Type, true
		}
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Anonymous && field.PkgPath == "" && field.Tag.Get("json") == "" {
			if child, ok := legacyField(field.Type, name); ok {
				return child, true
			}
		}
	}
	return nil, false
}

func legacyQuotedField(t reflect.Type, name string) bool {
	t = legacyJSONType(t)
	if t == nil || t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		parts := strings.Split(t.Field(i).Tag.Get("json"), ",")
		if parts[0] == name && slices.Contains(parts[1:], "string") {
			return true
		}
		if t.Field(i).Anonymous && t.Field(i).Tag.Get("json") == "" && legacyQuotedField(t.Field(i).Type, name) {
			return true
		}
	}
	return false
}

func (s legacyStage) insert(parent any, name string, position int64, kind string, scalar []byte) (int64, error) {
	result, err := s.tx.ExecContext(s.ctx, `INSERT INTO legacy_nodes(parent,name,position,kind,scalar) VALUES(?,?,?,?,?)`, parent, name, position, kind, scalar)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// JSON Token decodes only one scalar at a time, including inside session.events,
// analytics spans/deltas, activation events and replacement payloads. SQL UNIQUE
// detects duplicate object keys without an unbounded Go map of keys.
func (s legacyStage) decode(d *json.Decoder, parent any, name string, position int64, typ reflect.Type, collection string, depth int, quoted bool) (int64, error) {
	if depth > 256 {
		return 0, errors.New("legacy JSON exceeds supported nesting depth")
	}
	token, err := d.Token()
	if err != nil {
		return 0, err
	}
	t := legacyJSONType(typ)
	delim, container := token.(json.Delim)
	if !container {
		encoded, err := json.Marshal(token)
		if err != nil {
			return 0, err
		}
		if typ != nil && typ != legacyRawType {
			// Check exact scalar range/type without converting uint64 to float64.
			value := encoded
			if quoted && token != nil {
				text, ok := token.(string)
				if !ok {
					return 0, fmt.Errorf("legacy field %s requires a quoted number", name)
				}
				value = []byte(text)
			}
			if err := json.Unmarshal(value, reflect.New(typ).Interface()); err != nil {
				return 0, fmt.Errorf("invalid legacy scalar %s: %w", name, err)
			}
		}
		id, err := s.insert(parent, name, position, "scalar", encoded)
		if err == nil && quoted {
			_, err = s.tx.ExecContext(s.ctx, `UPDATE legacy_nodes SET quoted=1 WHERE id=?`, id)
		}
		return id, err
	}
	if delim != '{' && delim != '[' {
		return 0, errors.New("unexpected legacy JSON delimiter")
	}
	kind := "object"
	if delim == '[' {
		kind = "array"
	}
	if t != nil && (delim == '{' && t.Kind() != reflect.Struct && t.Kind() != reflect.Map || delim == '[' && t.Kind() != reflect.Slice && t.Kind() != reflect.Array) {
		return 0, fmt.Errorf("invalid legacy container %s", name)
	}
	id, err := s.insert(parent, name, position, kind, nil)
	if err != nil {
		return 0, err
	}
	var count int64
	for d.More() {
		childName := strconv.FormatInt(count, 10)
		var childType reflect.Type
		childCollection := collection
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return 0, err
			}
			childName = key.(string)
			// Retired authorization remains only in the immutable source backup.
			retiredScope := "_none"
			if t == reflect.TypeOf(diskState{}) {
				retiredScope = ""
			} else if t == reflect.TypeOf(Session{}) || t == reflect.TypeOf(GraphActivity{}) || t == reflect.TypeOf(GraphRun{}) {
				retiredScope = collection
			}
			if slices.Contains(abandonedTaskFields(retiredScope), childName) {
				if childName == "taskSchema" {
					var version int
					if err := d.Decode(&version); err != nil || version < 0 || version > 1 {
						return 0, errors.New("unsupported abandoned Tasks schema")
					}
				} else if err := discardLegacyValue(d, 0); err != nil {
					return 0, err
				}
				continue
			}
			var found bool
			childType, found = legacyField(t, childName)
			if !found {
				return 0, fmt.Errorf("unmapped legacy field %s.%s", collection, childName)
			}
			if t == reflect.TypeOf(diskState{}) {
				childCollection = childName
			}
		} else if t != nil {
			childType = t.Elem()
		}
		if _, err := s.decode(d, id, childName, count, childType, childCollection, depth+1, delim == '{' && legacyQuotedField(t, childName)); err != nil {
			return 0, err
		}
		count++
	}
	end, err := d.Token()
	if err != nil {
		return 0, err
	}
	if delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
		return 0, errors.New("invalid legacy container end")
	}
	_, err = s.tx.ExecContext(s.ctx, `UPDATE legacy_nodes SET size=? WHERE id=?`, count, id)
	return id, err
}

func discardLegacyValue(d *json.Decoder, depth int) error {
	if depth > 256 {
		return errors.New("retired legacy value exceeds nesting limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); ok && (delim == '{' || delim == '[') {
		for d.More() {
			if delim == '{' {
				if _, err := d.Token(); err != nil {
					return err
				}
			}
			if err := discardLegacyValue(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
	}
	return err
}

func (s legacyStage) node(id int64) (legacyNode, error) {
	n := legacyNode{id: id}
	err := s.tx.QueryRowContext(s.ctx, `SELECT kind,scalar,size,quoted FROM legacy_nodes WHERE id=?`, id).Scan(&n.kind, &n.scalar, &n.size, &n.quoted)
	return n, err
}

func (s legacyStage) child(parent int64, name string) (int64, error) {
	var id int64
	err := s.tx.QueryRowContext(s.ctx, `SELECT id FROM legacy_nodes WHERE parent=? AND name=?`, parent, name).Scan(&id)
	return id, err
}

// emit streams a stage subtree for domain import or a small-fixture oracle.
// Only one child/ancestor stack is resident; it never fetches a collection first.
func (s legacyStage) emit(w io.Writer, id int64) error {
	n, err := s.node(id)
	if err != nil {
		return err
	}
	if n.kind == "scalar" {
		_, err := w.Write(n.scalar)
		return err
	}
	start, end := "{", "}"
	if n.kind == "array" {
		start, end = "[", "]"
	}
	if _, err := io.WriteString(w, start); err != nil {
		return err
	}
	var previous int64 = -1
	first := true
	for {
		var child, position int64
		var name string
		err := s.tx.QueryRowContext(s.ctx, `SELECT id,name,position FROM legacy_nodes WHERE parent=? AND position>? ORDER BY position LIMIT 1`, id, previous).Scan(&child, &name, &position)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if n.kind == "object" {
			key, _ := json.Marshal(name)
			if _, err := w.Write(append(key, ':')); err != nil {
				return err
			}
		}
		if err := s.emit(w, child); err != nil {
			return err
		}
		previous, first = position, false
	}
	_, err = io.WriteString(w, end)
	return err
}

func stageLegacyCheckpoint(ctx context.Context, store *sqliteStore, source io.Reader) error {
	return store.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, legacyStageSchema); err != nil {
			return err
		}
		d := json.NewDecoder(source)
		d.UseNumber()
		s := legacyStage{ctx: ctx, tx: tx}
		root, err := s.decode(d, nil, "root", 0, reflect.TypeOf(diskState{}), "", 0, false)
		if err != nil {
			return err
		}
		if root != 1 {
			return errors.New("invalid legacy staging root")
		}
		if _, err := d.Token(); err != io.EOF {
			return errors.New("trailing legacy checkpoint content")
		}
		// These fields distinguish a valid legacy checkpoint from missing/corrupt
		// input; absent optional collections retain Go's historical zero values.
		for name, kind := range map[string]string{"projects": "array", "sessions": "array", "native": "object"} {
			id, err := s.child(root, name)
			if err != nil {
				return fmt.Errorf("legacy checkpoint missing %s", name)
			}
			n, err := s.node(id)
			if err != nil || n.kind != kind {
				return fmt.Errorf("invalid legacy checkpoint %s", name)
			}
		}
		id, err := s.child(root, "version")
		if err != nil {
			return errors.New("legacy checkpoint missing version")
		}
		n, err := s.node(id)
		if err != nil || string(n.scalar) != "1" && string(n.scalar) != "2" {
			return errors.New("unsupported legacy checkpoint version")
		}
		return nil
	})
}
