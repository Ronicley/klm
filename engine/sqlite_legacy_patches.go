package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

func (s legacyStage) replace(id int64, raw []byte, typ reflect.Type, collection string) error {
	n, err := s.node(id)
	if err != nil {
		return err
	}
	if n.quoted && string(raw) != "null" {
		// Field-level legacy patches use json.Marshal(field), without the
		// containing struct's ,string tag. Keep the checkpoint wire shape.
		if err := json.Unmarshal(raw, reflect.New(typ).Interface()); err != nil {
			return err
		}
		raw, err = json.Marshal(string(raw))
		if err != nil {
			return err
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	copyID, err := s.decode(d, nil, "replacement", 0, typ, collection, 0, n.quoted)
	if err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing legacy patch value")
	}
	if _, err := s.tx.ExecContext(s.ctx, `DELETE FROM legacy_nodes WHERE parent=?`, id); err != nil {
		return err
	}
	if _, err := s.tx.ExecContext(s.ctx, `UPDATE legacy_nodes SET (kind,scalar,size)=(SELECT kind,scalar,size FROM legacy_nodes WHERE id=?) WHERE id=?`, copyID, id); err != nil {
		return err
	}
	if _, err := s.tx.ExecContext(s.ctx, `UPDATE legacy_nodes SET parent=? WHERE parent=?`, id, copyID); err != nil {
		return err
	}
	_, err = s.tx.ExecContext(s.ctx, `DELETE FROM legacy_nodes WHERE id=?`, copyID)
	return err
}

func (s legacyStage) zero(parent int64, name string, position int64, typ reflect.Type) (int64, error) {
	raw, err := json.Marshal(reflect.Zero(typ).Interface())
	if err != nil {
		return 0, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return s.decode(d, parent, name, position, typ, "", 0, false)
}

func (s legacyStage) nextPosition(parent int64) (int64, error) {
	var pos int64
	err := s.tx.QueryRowContext(s.ctx, `SELECT coalesce(max(position),-1)+1 FROM legacy_nodes WHERE parent=?`, parent).Scan(&pos)
	return pos, err
}

// resolve mirrors the historical typed path grammar. A missing struct field is
// a typed zero, not a missing map entry. Only the historical analytics pointer /
// map types permit nested patches. Arbitrary traversal into event.Data is refused.
func (s legacyStage) resolve(path []string) (int64, reflect.Type, error) {
	id, typ := int64(1), reflect.TypeOf(diskState{})
	for _, key := range path {
		if typ.Kind() == reflect.Pointer {
			if typ != analyticsStateType {
				return 0, nil, errors.New("invalid legacy pointer patch")
			}
			typ = typ.Elem()
		}
		if embedded, ok := legacyEmbeddedField(typ, key); ok {
			// Old diffTransaction addresses anonymous Go fields by their type name,
			// while checkpoint JSON flattens their fields into this same object.
			typ = embedded
			continue
		}
		n, err := s.node(id)
		if err != nil {
			return 0, nil, err
		}
		var childType reflect.Type
		var position int64
		switch typ.Kind() {
		case reflect.Struct:
			var ok bool
			childType, ok = legacyField(typ, key)
			if !ok {
				return 0, nil, fmt.Errorf("invalid legacy field %s", key)
			}
			if n.kind == "scalar" && string(n.scalar) == "null" {
				if _, err := s.tx.ExecContext(s.ctx, `UPDATE legacy_nodes SET kind='object',scalar=NULL WHERE id=?`, id); err != nil {
					return 0, nil, err
				}
			}
			position, err = s.nextPosition(id)
		case reflect.Slice:
			position, err = strconv.ParseInt(key, 10, 64)
			if err != nil || position < 0 || position >= n.size || n.kind != "array" {
				return 0, nil, errors.New("legacy patch slice index out of bounds")
			}
			key = strconv.FormatInt(position, 10)
			childType = typ.Elem()
		case reflect.Map:
			if typ != analyticsExecutionMapType && typ != analyticsGraphMapType {
				return 0, nil, errors.New("invalid legacy nested map patch")
			}
			childType = typ.Elem()
		default:
			return 0, nil, errors.New("invalid legacy patch path")
		}
		if err != nil {
			return 0, nil, err
		}
		child, err := s.child(id, key)
		if errors.Is(err, sql.ErrNoRows) && typ.Kind() == reflect.Struct {
			child, err = s.zero(id, key, position, childType)
			if err == nil && legacyQuotedField(typ, key) {
				_, err = s.tx.ExecContext(s.ctx, `UPDATE legacy_nodes SET quoted=1,scalar=CAST('"0"' AS BLOB) WHERE id=?`, child)
			}
		}
		if err != nil {
			return 0, nil, fmt.Errorf("legacy patch references missing %s: %w", key, err)
		}
		id, typ = child, childType
	}
	return id, typ, nil
}

func (s legacyStage) patch(p statePatch) error {
	if len(p.Path) > 256 {
		return errors.New("legacy patch exceeds nesting limit")
	}
	collection := ""
	if len(p.Path) > 0 {
		collection = p.Path[0]
	}
	// Terminal map-key writes/removals have different semantics from traversing
	// a map entry; delete absent is harmless, assignment creates a nil map.
	if len(p.Path) > 0 {
		parent, typ, err := s.resolve(p.Path[:len(p.Path)-1])
		if err != nil {
			return err
		}
		if embedded, ok := legacyEmbeddedField(typ, p.Path[len(p.Path)-1]); ok {
			if p.Delete || p.Length != nil {
				return errors.New("invalid embedded struct mutation")
			}
			return s.replaceEmbedded(parent, p.Value, embedded, collection)
		}
		if typ.Kind() == reflect.Map {
			key := p.Path[len(p.Path)-1]
			id, err := s.child(parent, key)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if p.Delete {
				_, err := s.tx.ExecContext(s.ctx, `DELETE FROM legacy_nodes WHERE parent=? AND name=?`, parent, key)
				return err
			}
			if errors.Is(err, sql.ErrNoRows) {
				pos, err := s.nextPosition(parent)
				if err != nil {
					return err
				}
				id, err = s.zero(parent, key, pos, typ.Elem())
				if err != nil {
					return err
				}
			}
			if _, err := s.tx.ExecContext(s.ctx, `UPDATE legacy_nodes SET kind='object',scalar=NULL WHERE id=?`, parent); err != nil {
				return err
			}
			return s.patchNode(id, typ.Elem(), p, collection)
		}
	}
	id, typ, err := s.resolve(p.Path)
	if err != nil {
		return err
	}
	return s.patchNode(id, typ, p, collection)
}

func legacyEmbeddedField(t reflect.Type, name string) (reflect.Type, bool) {
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.PkgPath == "" && f.Tag.Get("json") == "" && f.Name == name {
			return f.Type, true
		}
	}
	return nil, false
}

func (s legacyStage) replaceEmbedded(parent int64, raw []byte, typ reflect.Type, collection string) error {
	if string(raw) == "null" {
		var err error
		raw, err = json.Marshal(reflect.Zero(typ).Interface())
		if err != nil {
			return err
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	copyID, err := s.decode(d, nil, "embedded", 0, typ, collection, 0, false)
	if err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing embedded patch value")
	}
	var removeFields func(reflect.Type) error
	removeFields = func(t reflect.Type) error {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			if f.Anonymous && f.Tag.Get("json") == "" {
				if err := removeFields(f.Type); err != nil {
					return err
				}
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if _, err := s.tx.ExecContext(s.ctx, `DELETE FROM legacy_nodes WHERE parent=? AND name=?`, parent, name); err != nil {
				return err
			}
		}
		return nil
	}
	if err := removeFields(typ); err != nil {
		return err
	}
	position, err := s.nextPosition(parent)
	if err != nil {
		return err
	}
	if _, err := s.tx.ExecContext(s.ctx, `UPDATE legacy_nodes SET parent=?,position=position+? WHERE parent=?`, parent, position, copyID); err != nil {
		return err
	}
	_, err = s.tx.ExecContext(s.ctx, `DELETE FROM legacy_nodes WHERE id=?`, copyID)
	return err
}

func (s legacyStage) patchNode(id int64, typ reflect.Type, p statePatch, collection string) error {
	if p.Delete {
		return errors.New("legacy delete requires a map key")
	}
	if p.Length == nil {
		return s.replace(id, p.Value, typ, collection)
	}
	if typ.Kind() != reflect.Slice || *p.Length < 0 {
		return errors.New("invalid legacy transaction resize")
	}
	n, err := s.node(id)
	if err != nil {
		return err
	}
	if _, err := s.tx.ExecContext(s.ctx, `DELETE FROM legacy_nodes WHERE parent=? AND position>=?`, id, *p.Length); err != nil {
		return err
	}
	for i := n.size; i < int64(*p.Length); i++ {
		if _, err := s.zero(id, strconv.FormatInt(i, 10), i, typ.Elem()); err != nil {
			return err
		}
	}
	_, err = s.tx.ExecContext(s.ctx, `UPDATE legacy_nodes SET kind='array',scalar=NULL,size=? WHERE id=?`, *p.Length, id)
	return err
}

func legacyHistoryPatch(p statePatch) bool {
	return len(p.Path) >= 3 && p.Path[0] == "sessions" && p.Path[2] == "events"
}

func (s legacyStage) transaction(patches []statePatch) error {
	// Detach histories before positional metadata replacement. The temporary SQL
	// identity map, not a Go map of every history, survives slice reorder/shrink.
	if _, err := s.tx.ExecContext(s.ctx, `CREATE TEMP TABLE legacy_histories(session_id BLOB PRIMARY KEY,node INTEGER NOT NULL);
INSERT INTO legacy_histories SELECT identity.scalar,events.id
 FROM legacy_nodes collection JOIN legacy_nodes session ON session.parent=collection.id
 JOIN legacy_nodes identity ON identity.parent=session.id AND identity.name='id'
 JOIN legacy_nodes events ON events.parent=session.id AND events.name='events'
 WHERE collection.parent=1 AND collection.name='sessions';
UPDATE legacy_nodes SET parent=NULL WHERE id IN (SELECT node FROM legacy_histories);`); err != nil {
		return err
	}
	for _, p := range patches {
		if !legacyHistoryPatch(p) {
			if err := s.patch(p); err != nil {
				return err
			}
		}
	}
	collection, err := s.child(1, "sessions")
	if err != nil {
		return err
	}
	var previous int64 = -1
	for {
		var session, position int64
		var identity []byte
		err := s.tx.QueryRowContext(s.ctx, `SELECT session.id,session.position,identity.scalar FROM legacy_nodes session
 JOIN legacy_nodes identity ON identity.parent=session.id AND identity.name='id'
 WHERE session.parent=? AND session.position>? ORDER BY session.position LIMIT 1`, collection, previous).Scan(&session, &position, &identity)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		// History embedded in a metadata replacement is discarded by the old
		// replayTransaction too; explicit event patches are applied afterwards.
		if _, err := s.tx.ExecContext(s.ctx, `DELETE FROM legacy_nodes WHERE parent=? AND name='events'`, session); err != nil {
			return err
		}
		pos, err := s.nextPosition(session)
		if err != nil {
			return err
		}
		var history int64
		err = s.tx.QueryRowContext(s.ctx, `SELECT node FROM legacy_histories WHERE session_id=?`, identity).Scan(&history)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = s.zero(session, "events", pos, eventSliceType)
		} else if err == nil {
			_, err = s.tx.ExecContext(s.ctx, `UPDATE legacy_nodes SET parent=?,position=? WHERE id=?`, session, pos, history)
		}
		if err != nil {
			return err
		}
		previous = position
	}
	if _, err := s.tx.ExecContext(s.ctx, `DELETE FROM legacy_nodes WHERE parent IS NULL AND id IN (SELECT node FROM legacy_histories); DROP TABLE legacy_histories;`); err != nil {
		return err
	}
	for _, p := range patches {
		if legacyHistoryPatch(p) {
			if err := s.patch(p); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s legacyStage) decodeNode(id int64, target any) error {
	n, err := s.node(id)
	if err != nil {
		return err
	}
	if n.quoted {
		var number string
		if err := json.Unmarshal(n.scalar, &number); err != nil {
			return err
		}
		return json.Unmarshal([]byte(number), target)
	}
	var body bytes.Buffer
	if err := s.emit(&body, id); err != nil {
		return err
	}
	return json.Unmarshal(body.Bytes(), target)
}

func (s legacyStage) set(path []string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.patch(statePatch{Path: path, Value: encoded})
}

func (s legacyStage) append(path []string, value any) error {
	id, _, err := s.resolve(path)
	if err != nil {
		return err
	}
	n, err := s.node(id)
	if err != nil {
		return err
	}
	length := int(n.size + 1)
	if err := s.patch(statePatch{Path: path, Length: &length}); err != nil {
		return err
	}
	return s.set(patchPath(path, strconv.FormatInt(n.size, 10)), value)
}

func (s legacyStage) stream(record streamRecord) error {
	identity, _ := json.Marshal(record.Session)
	var session, position int64
	if err := s.tx.QueryRowContext(s.ctx, `SELECT session.id,session.position FROM legacy_nodes collection
 JOIN legacy_nodes session ON session.parent=collection.id
 JOIN legacy_nodes identity ON identity.parent=session.id AND identity.name='id'
 WHERE collection.parent=1 AND collection.name='sessions' AND identity.scalar=? LIMIT 1`, identity).Scan(&session, &position); err != nil {
		return errors.New("stream journal references missing session")
	}
	base := []string{"sessions", strconv.FormatInt(position, 10)}
	events, _, err := s.resolve(patchPath(base, "events"))
	if err != nil {
		return err
	}
	for _, op := range record.Updates {
		encoded, _ := json.Marshal(op.Event.ID)
		var existing, ordinal int64
		err := s.tx.QueryRowContext(s.ctx, `SELECT event.id,event.position FROM legacy_nodes event
 JOIN legacy_nodes identity ON identity.parent=event.id AND identity.name='id'
 WHERE event.parent=? AND identity.scalar=? ORDER BY event.position DESC LIMIT 1`, events, encoded).Scan(&existing, &ordinal)
		if errors.Is(err, sql.ErrNoRows) {
			if err := s.append(patchPath(base, "events"), op.Event); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		var event Event
		if err := s.decodeNode(existing, &event); err != nil {
			return err
		}
		changes := streamChanges([]Event{event}, []streamUpdate{{event: op.Event, appendText: op.AppendText}}, record.Revision)
		if len(changes) > 0 {
			if err := s.set(append(patchPath(base, "events"), strconv.FormatInt(ordinal, 10)), changes[0].Event); err != nil {
				return err
			}
		}
	}
	if err := s.set(patchPath(base, "updatedAt"), record.Updated); err != nil {
		return err
	}
	if record.Usage != nil {
		if err := s.set(patchPath(base, "usage"), record.Usage); err != nil {
			return err
		}
	}
	return s.analytics(record.Analytics, record.Session)
}

func (s legacyStage) analytics(m *analyticsUsageMutation, sessionID string) error {
	if m == nil {
		return nil
	}
	base := []string{"analytics", "executions", m.ExecutionID}
	// Read just the identity/sequence, never historical spans or usage arrays.
	var owner string
	var sequence uint64
	for name, target := range map[string]any{"sessionId": &owner, "sequence": &sequence} {
		id, _, err := s.resolve(patchPath(base, name))
		if err != nil {
			return err
		}
		if err := s.decodeNode(id, target); err != nil {
			return err
		}
	}
	if owner != sessionID || m.Key == "" || m.Counter.ExecutionID != m.ExecutionID || sequence == ^uint64(0) || m.Delta.Sequence != sequence+1 || analyticsTime(m.Delta.At).IsZero() || !analyticsValidCount(m.Counter.Input) || !analyticsValidCount(m.Counter.Output) || !analyticsValidCount(m.Delta.Input) || !analyticsValidCount(m.Delta.Output) {
		return errors.New("invalid analytics usage mutation")
	}
	for name, value := range map[string]any{"sequence": m.Delta.Sequence, "inputBaseline": m.InputBaseline, "outputBaseline": m.OutputBaseline, "inputFinal": m.InputFinal, "outputFinal": m.OutputFinal} {
		if err := s.set(patchPath(base, name), value); err != nil {
			return err
		}
	}
	if m.Delta.Input != nil || m.Delta.Output != nil {
		if err := s.append(patchPath(base, "usage"), m.Delta); err != nil {
			return err
		}
	}
	if m.Reason != "" {
		id, _, err := s.resolve(patchPath(base, "reasons"))
		if err != nil {
			return err
		}
		var exists bool
		reason, _ := json.Marshal(m.Reason)
		if err := s.tx.QueryRowContext(s.ctx, `SELECT EXISTS(SELECT 1 FROM legacy_nodes WHERE parent=? AND scalar=?)`, id, reason).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if err := s.append(patchPath(base, "reasons"), m.Reason); err != nil {
				return err
			}
		}
	}
	return s.set([]string{"analytics", "counters", m.Key}, m.Counter)
}

func replayLegacyStage(ctx context.Context, store *sqliteStore, dir string) (map[string]legacyJournalScan, error) {
	var revision uint64
	if err := store.write(ctx, func(tx *sql.Tx) error {
		s := legacyStage{ctx: ctx, tx: tx}
		id, _, err := s.resolve([]string{"graphRevision"})
		if err != nil {
			return err
		}
		return s.decodeNode(id, &revision)
	}); err != nil {
		return nil, err
	}
	_, scans, err := readLegacyJournals(dir, revision, func(record streamRecord) error {
		return store.write(ctx, func(tx *sql.Tx) error {
			s := legacyStage{ctx: ctx, tx: tx}
			if record.Format == 1 {
				if err := s.transaction(record.Transaction); err != nil {
					return err
				}
				if err := s.set([]string{"graphViewRevision"}, record.Revision); err != nil {
					return err
				}
			} else if err := s.stream(record); err != nil {
				return err
			}
			return s.set([]string{"graphRevision"}, record.Revision)
		})
	})
	return scans, err
}
