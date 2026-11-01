package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Deliberately a storage/field-coverage fixture, NOT a semantically valid engine
// startup fixture. Each serialized field gets a nonzero value so loss is visible.
func fillSQLiteFieldFixture(v reflect.Value, path string, depth int) {
	if depth > 32 {
		panic("recursive field fixture")
	}
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillSQLiteFieldFixture(v.Elem(), path, depth+1)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath == "" && v.Field(i).CanSet() {
				fillSQLiteFieldFixture(v.Field(i), path+"."+v.Type().Field(i).Name, depth+1)
			}
		}
	case reflect.Slice:
		if v.Type() == legacyRawType {
			v.SetBytes([]byte(`{"fixture":"raw","number":42}`))
		} else if v.Type().Elem().Kind() == reflect.Uint8 {
			v.SetBytes([]byte{0, 1, 2, 250, 255})
		} else {
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
			fillSQLiteFieldFixture(v.Index(0), path+"[0]", depth+1)
		}
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		element := reflect.New(v.Type().Elem()).Elem()
		fillSQLiteFieldFixture(element, path+"[key]", depth+1)
		v.SetMapIndex(reflect.ValueOf("key").Convert(v.Type().Key()), element)
	case reflect.Interface:
		v.Set(reflect.ValueOf(map[string]any{"fixture": path, "number": float64(42)}))
	case reflect.String:
		v.SetString(path)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(42)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(42)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(42.5)
	}
}

func sqliteTestDomainRoundTrip(t *testing.T, state diskState) *sqliteStore {
	t.Helper()
	store := sqliteTestStage(t, state)
	ctx := context.Background()
	if err := importStagedDomains(ctx, store); err != nil {
		t.Fatal(err)
	}
	specs, err := sqliteDomains()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = store.write(ctx, func(tx *sql.Tx) error {
		output.WriteByte('{')
		first := true
		for name := range sqliteScalarFields {
			var value string
			if err := tx.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key=?`, "legacy:"+name).Scan(&value); err == sql.ErrNoRows {
				continue
			} else if err != nil {
				return err
			}
			if !first {
				output.WriteByte(',')
			}
			first = false
			key, _ := json.Marshal(name)
			output.Write(key)
			output.WriteByte(':')
			output.WriteString(value)
		}
		for _, spec := range specs {
			var kind string
			if err := tx.QueryRowContext(ctx, `SELECT kind FROM record_collections WHERE parent_table='root' AND parent_id='' AND field=?`, spec.field).Scan(&kind); err == sql.ErrNoRows {
				continue
			} else if err != nil {
				return err
			}
			if !first {
				output.WriteByte(',')
			}
			first = false
			key, _ := json.Marshal(spec.field)
			output.Write(key)
			output.WriteByte(':')
			if err := emitSQLiteDomainCollection(ctx, tx, &output, spec, "", kind); err != nil {
				return fmt.Errorf("read back %s: %w", spec.field, err)
			}
		}
		output.WriteByte('}')
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var got diskState
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// JSON normalization accounts for intentionally open map[string]any numbers.
	wantJSON, _ := json.Marshal(state)
	gotJSON, _ := json.Marshal(got)
	if !bytes.Equal(wantJSON, gotJSON) {
		t.Fatalf("domain roundtrip lost fields\nwant %s\ngot %s", wantJSON, gotJSON)
	}
	if err := store.check(ctx); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestSQLiteMigrationDomainRoundTrip(t *testing.T) {
	t.Run("every-current-field", func(t *testing.T) {
		var state diskState
		fillSQLiteFieldFixture(reflect.ValueOf(&state).Elem(), "fixture", 0)
		state.Version, state.JournalFormat, state.TopicSchema = 2, 1, 1
		state.GraphRevision, state.WebhookReceivedSequence = ^uint64(0)-1, ^uint64(0)
		sqliteTestDomainRoundTrip(t, state)
	})
	t.Run("empty-and-null", func(t *testing.T) {
		sqliteTestDomainRoundTrip(t, sqliteLegacyFixture())
	})
}

func TestSQLiteMigrationGeneratedNestedPatches(t *testing.T) {
	var before diskState
	fillSQLiteFieldFixture(reflect.ValueOf(&before).Elem(), "fixture", 0)
	before.Version, before.TopicSchema, before.JournalFormat = 2, 1, 1
	store := sqliteTestStage(t, before)
	encoded, _ := json.Marshal(before)
	var after diskState
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	after.GraphRuns[0].Snapshot.Agents[0].Name = "promoted field mutation"
	after.GraphRuns[0].Snapshot.Agents[0].Skills = []string{"replacement-skill"}
	after.ActivityBoardConfig.Revision = ^uint64(0)
	after.ActivityBoardConfig.Projection = ^uint64(0) - 1
	move := after.ActivityBoardMoves["key"]
	move.ConfigRevision = ^uint64(0)
	after.ActivityBoardMoves["key"] = move
	var patches []statePatch
	if err := diffTransaction(reflect.ValueOf(before), reflect.ValueOf(after), nil, &patches); err != nil {
		t.Fatal(err)
	}
	if err := store.write(context.Background(), func(tx *sql.Tx) error { return (legacyStage{ctx: context.Background(), tx: tx}).transaction(patches) }); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(after)
	got, _ := json.Marshal(sqliteTestReadStage(t, store))
	if !bytes.Equal(want, got) {
		t.Fatalf("generated nested patches diverged\nwant %s\ngot %s", want, got)
	}
	// Whole embedded-field replacement must clear omitted fields without erasing
	// the enclosing AgentRecord's ID/revision/graphReferences.
	patch := statePatch{Path: []string{"graphRuns", "0", "snapshot", "agents", "0", "AgentDefinition"}, Value: json.RawMessage(`{"name":"only-name"}`)}
	if err := replayTransaction(&after, []statePatch{patch}); err != nil {
		t.Fatal(err)
	}
	if err := store.write(context.Background(), func(tx *sql.Tx) error {
		return (legacyStage{ctx: context.Background(), tx: tx}).transaction([]statePatch{patch})
	}); err != nil {
		t.Fatal(err)
	}
	want, _ = json.Marshal(after)
	got, _ = json.Marshal(sqliteTestReadStage(t, store))
	if !bytes.Equal(want, got) {
		t.Fatal("whole embedded-field replacement diverged")
	}
}

func TestSQLitePagingBoundedContentAndDirections(t *testing.T) {
	ctx := context.Background()
	state := sqliteLegacyFixture()
	state.Sessions[0].Events = nil
	for i := 0; i < 125; i++ {
		state.Sessions[0].Events = append(state.Sessions[0].Events, Event{ID: fmt.Sprintf("event-%03d", i), Type: "tool", Text: "same work group", ConsultationID: "long-group"})
	}
	state.Sessions[0].Events[120].Text = strings.Repeat("Unicode é🙂\n", 250000)
	store, path := sqliteTestStore(t)
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := stageLegacyCheckpoint(ctx, store, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := importStagedDomains(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := store.openReaders(ctx, path); err != nil {
		t.Fatal(err)
	}
	page, err := store.readHistory(ctx, "a", "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 50 || page.Before != "75" || page.After != "124" || !page.HasOlder || page.HasNewer || page.Count != "125" {
		t.Fatalf("tail bounds: %+v", page)
	}
	encoded, _ := json.Marshal(page)
	if len(encoded) > sqliteHistoryPageBytes {
		t.Fatal("history page exceeds byte budget")
	}
	ref := page.Events[45].Content
	if ref == nil || page.Events[45].Event != nil {
		t.Fatal("oversized event eagerly returned")
	}
	var content bytes.Buffer
	var offset int64
	for {
		chunk, err := readSQLiteContent(ctx, store.readers, "session_events", ref.Key, ref.Digest, offset, 4093)
		if err != nil {
			t.Fatal(err)
		}
		if len(chunk.Data) > 4093 {
			t.Fatal("content chunk too large")
		}
		content.Write(chunk.Data)
		offset = chunk.NextOffset
		if !chunk.HasMore {
			break
		}
	}
	var event Event
	if err := json.Unmarshal(content.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(event, state.Sessions[0].Events[120]) {
		t.Fatal("chunk continuation lost content")
	}
	digest := sha256.Sum256(content.Bytes())
	if hex.EncodeToString(digest[:]) != ref.Digest {
		t.Fatal("content digest mismatch")
	}
	if _, err := readSQLiteContent(ctx, store.readers, "session_events", ref.Key, "stale", 0, 100); err != errSQLiteContentChanged {
		t.Fatal("stale continuation accepted")
	}
	older, err := store.readHistory(ctx, "a", page.Before, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Events) != 50 || older.Before != "25" || older.After != "74" || !older.HasNewer {
		t.Fatalf("older bounds: %+v", older)
	}
	first, err := store.readHistory(ctx, "a", older.Before, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 25 || first.HasOlder || first.Before != "0" {
		t.Fatalf("first bounds: %+v", first)
	}
	newer, err := store.readHistory(ctx, "a", "", first.After, 50)
	if err != nil {
		t.Fatal(err)
	}
	if newer.Before != older.Before || newer.After != older.After {
		t.Fatal("forward reload skipped rows")
	}
	if _, err := store.readHistory(ctx, "missing", "", "", 50); err != sql.ErrNoRows {
		t.Fatal("missing session interpreted as empty history")
	}
	if _, err := store.readHistory(ctx, "a", "", "", 51); err == nil {
		t.Fatal("unbounded page allowed")
	}
	var plan string
	rows, err := store.readers.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT id FROM data_session_events WHERE owner_id=? AND position<? ORDER BY position DESC LIMIT 50`, "a", 75)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "SEARCH") || strings.Contains(plan, "SCAN data_session_events") {
		t.Fatalf("unindexed page: %s", plan)
	}
	t.Logf("page bytes=%d; oversized event bytes=%d; query plan=%s", len(encoded), ref.Bytes, plan)
}
