package main

import (
	"bytes"
	"context"
	"database/sql"
	encodingbinary "encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sqliteTestStore(t *testing.T) (*sqliteStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stage.sqlite3")
	s, err := createSQLiteStore(context.Background(), path, newID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.close() })
	return s, path
}

func sqliteLegacyFixture() diskState {
	return diskState{Version: 2, Projects: []Project{{ID: "p", Name: "Fixture", Folders: []string{}, ArchivedFolders: []string{}}},
		Sessions: []Session{
			{ID: "a", ProjectID: "p", Status: "idle", Events: []Event{{ID: "a-user", Type: "user", Text: "original"}, {ID: "reply", Type: "assistant", Text: "before", Favorite: true, Data: map[string]any{"old": "kept"}}}},
			{ID: "b", ProjectID: "p", Status: "idle", Events: []Event{{ID: "b-user", Type: "user", Text: "other"}}},
		}, Native: map[string]nativeSession{"a": {ID: "native-a", Path: "fixture-native.jsonl"}},
		AcceptedMessages: map[string]string{"existing": "fingerprint"}, WebhookReceivedSequence: ^uint64(0) - 1}
}

func sqliteTestStage(t *testing.T, state diskState) *sqliteStore {
	t.Helper()
	s, _ := sqliteTestStore(t)
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := stageLegacyCheckpoint(context.Background(), s, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	return s
}

func sqliteTestReadStage(t *testing.T, store *sqliteStore) diskState {
	t.Helper()
	var body bytes.Buffer
	if err := store.write(context.Background(), func(tx *sql.Tx) error {
		return (legacyStage{ctx: context.Background(), tx: tx}).emit(&body, 1)
	}); err != nil {
		t.Fatal(err)
	}
	var result diskState
	if err := json.Unmarshal(body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSQLiteMigrationReadOnlyReplayEquivalence(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			state := sqliteLegacyFixture()
			state.Version = version
			store := sqliteTestStage(t, state)
			dir := t.TempDir()
			first := streamRecord{Revision: 1, Session: "a", Updated: "first", Updates: []streamOperation{
				{Event: Event{ID: "reply", Text: " + streamed", Data: map[string]any{"new": "merged"}}, AppendText: true},
				{Event: Event{ID: "third", Type: "assistant", Text: "new event"}},
			}}
			if err := appendStreamRecord(dir, first); err != nil {
				t.Fatal(err)
			}
			if err := replaceFile(streamJournalPath(dir), sealedJournalPath(dir)); err != nil {
				t.Fatal(err)
			}
			// Reordered session IDs must carry their old histories to new positions.
			length := 3
			patches := []statePatch{
				{Path: []string{"sessions"}, Length: &length},
				{Path: []string{"sessions", "0", "id"}, Value: json.RawMessage(`"b"`)},
				{Path: []string{"sessions", "1", "id"}, Value: json.RawMessage(`"a"`)},
				{Path: []string{"sessions", "2"}, Value: json.RawMessage(`{"id":"new","projectId":"p","status":"idle","events":[]}`)},
				{Path: []string{"sessions", "2", "events"}, Length: new(int)},
				{Path: []string{"sessions", "1", "events", "1", "favorite"}, Value: json.RawMessage(`false`)},
				{Path: []string{"sessions", "1", "usage"}, Value: json.RawMessage(`{"inputTokens":12,"outputTokens":4}`)},
				{Path: []string{"native", "a"}, Value: json.RawMessage(`{"id":"resumed"}`)},
				{Path: []string{"acceptedMessages", "existing"}, Delete: true},
				{Path: []string{"acceptedMessages", "new"}, Value: json.RawMessage(`"new-fingerprint"`)},
			}
			if err := appendStreamRecord(dir, streamRecord{Format: 1, Revision: 2, Transaction: patches}); err != nil {
				t.Fatal(err)
			}
			if err := appendStreamRecord(dir, streamRecord{Format: 1, Revision: 3, Transaction: []statePatch{
				{Path: []string{"sessions", "1", "usage"}, Value: json.RawMessage(`{"inputTokens":5}`)},
				{Path: []string{"sessions", "0", "sources"}, Value: json.RawMessage(`null`)},
			}}); err != nil {
				t.Fatal(err)
			}
			// Legacy replay oracle is permitted only on disposable fixture copies.
			for _, path := range []string{sealedJournalPath(dir), streamJournalPath(dir)} {
				if err := replayJournalFile(path, &state); err != nil {
					t.Fatal(err)
				}
			}
			before, err := fingerprintLegacyFile(streamJournalPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := replayLegacyStage(context.Background(), store, dir); err != nil {
				t.Fatal(err)
			}
			got := sqliteTestReadStage(t, store)
			if !reflect.DeepEqual(state, got) {
				wantJSON, _ := json.Marshal(state)
				gotJSON, _ := json.Marshal(got)
				t.Fatalf("replay differs\nwant %s\ngot  %s", wantJSON, gotJSON)
			}
			after, _ := fingerprintLegacyFile(streamJournalPath(dir))
			if before != after {
				t.Fatal("read-only replay changed journal")
			}
			// Repeating covered-prefix replay is idempotent, including exact uint64.
			if _, err := replayLegacyStage(context.Background(), store, dir); err != nil {
				t.Fatal(err)
			}
			if again := sqliteTestReadStage(t, store); !reflect.DeepEqual(got, again) {
				t.Fatal("retry changed imported state")
			}
		})
	}
}

func TestSQLiteMigrationImmutableBackup(t *testing.T) {
	dir := t.TempDir()
	for _, name := range sqliteLegacyFiles {
		if name == "engine-access.json" {
			continue // Presence is an explicit part of the manifest.
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("  \r\n"+name+"\t exact bytes \x00"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := captureLegacyBackup(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "legacy-backups", manifest.ID)
	if err := verifyLegacyFiles(dir, manifest); err != nil {
		t.Fatal(err)
	}
	if err := verifyLegacyFiles(backup, manifest); err != nil {
		t.Fatal(err)
	}
	// A changed source can never reuse a stale generation, while older backups
	// remain byte-exact after creating a new generation.
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("newer legacy state"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyLegacyFiles(dir, manifest); err == nil {
		t.Fatal("stale source was accepted")
	}
	next, err := captureLegacyBackup(dir, nil)
	if err != nil || next.ID == manifest.ID {
		t.Fatalf("new generation: %v", err)
	}
	if err := verifyLegacyFiles(backup, manifest); err != nil {
		t.Fatal(err)
	}
	for _, boundary := range []string{"backup-file:state.json", "backup-manifest"} {
		t.Run(boundary, func(t *testing.T) {
			failed, err := captureLegacyBackup(dir, func(at string) error {
				if at == boundary {
					return errors.New("injected crash")
				}
				return nil
			})
			if err == nil {
				t.Fatal("injected failure not observed")
			}
			if _, err := os.Stat(filepath.Join(dir, "legacy-backups", failed.ID, "state.json")); err != nil {
				t.Fatal("partial backup evidence was removed")
			}
			if err := verifyLegacyFiles(dir, next); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func sqliteTestFrame(body []byte) []byte {
	var header [8]byte
	encodingbinary.LittleEndian.PutUint32(header[:4], uint32(len(body)))
	encodingbinary.LittleEndian.PutUint32(header[4:], crc32.ChecksumIEEE(body))
	return append(header[:], body...)
}

func TestSQLiteMigrationDamagedJournal(t *testing.T) {
	valid := sqliteTestFrame([]byte(`{"revision":1,"session":"a","updates":[{"event":{"id":"e","text":"ok"}}]}`))
	cases := []struct {
		name string
		data []byte
		bad  bool
		tail int64
	}{
		{"short header", append(append([]byte{}, valid...), 1, 2), false, 2},
		{"short body", append(append([]byte{}, valid...), valid[:len(valid)-1]...), false, int64(len(valid) - 1)},
		{"crc", append(append([]byte{}, valid...), append([]byte{1, 0, 0, 0, 0, 0, 0, 0}, 'x')...), true, 0},
		{"json", sqliteTestFrame([]byte(`{`)), true, 0},
		{"format", sqliteTestFrame([]byte(`{"format":2,"revision":1}`)), true, 0},
		{"unknown field", sqliteTestFrame([]byte(`{"revision":1,"unknownDomain":[]}`)), true, 0},
		{"zero size", make([]byte, 8), true, 0},
		{"gap", sqliteTestFrame([]byte(`{"revision":2,"session":"a","updates":[{"event":{"id":"e"}}]}`)), true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := streamJournalPath(dir)
			if err := os.WriteFile(path, tc.data, 0400); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(path, 0600) })
			_, scans, err := readLegacyJournals(dir, 0, func(streamRecord) error { return nil })
			if (err != nil) != tc.bad {
				t.Fatalf("unexpected read result: %v", err)
			}
			if !tc.bad && scans["stream.journal"].TailBytes != tc.tail {
				t.Fatalf("tail: %+v", scans)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, tc.data) {
				t.Fatal("damaged source changed")
			}
		})
	}
}

func TestSQLiteMigrationStageRejectsUnknownAndRollsBack(t *testing.T) {
	for _, body := range []string{
		`{"version":2,"projects":[],"sessions":[],"native":{},"newDomain":{}}`,
		`{"version":2,"projects":[],"sessions":[{"id":"s","unknown":true}],"native":{}}`,
		`{"version":2,"projects":[],"sessions":[],"native":{},"graphRevision":18446744073709551616}`,
		`{"version":2,"projects":[],"sessions":[],"native":{},"version":2}`,
		`{"version":2,"projects":null,"sessions":[],"native":{}}`,
	} {
		store, _ := sqliteTestStore(t)
		if err := stageLegacyCheckpoint(context.Background(), store, strings.NewReader(body)); err == nil {
			t.Fatalf("accepted invalid input %s", body)
		}
		var tables int
		if err := store.writer.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='legacy_nodes'`).Scan(&tables); err != nil || tables != 0 {
			t.Fatalf("partial stage committed: %d %v", tables, err)
		}
	}
	store := sqliteTestStage(t, sqliteLegacyFixture())
	before := sqliteTestReadStage(t, store)
	err := store.write(context.Background(), func(tx *sql.Tx) error {
		s := legacyStage{ctx: context.Background(), tx: tx}
		return s.transaction([]statePatch{
			{Path: []string{"sessions", "0", "title"}, Value: json.RawMessage(`"must rollback"`)},
			{Path: []string{"sessions", "99", "id"}, Value: json.RawMessage(`"invalid"`)},
		})
	})
	if err == nil || !reflect.DeepEqual(before, sqliteTestReadStage(t, store)) {
		t.Fatal("failed frame partially applied")
	}
}

func TestSQLiteMigrationRetiredFields(t *testing.T) {
	store, _ := sqliteTestStore(t)
	body := `{"version":2,"projects":[],"sessions":[{"id":"s","taskId":"retired","events":[]}],"native":{},"taskSchema":1,"tasks":[{"nested":[1,2,3]}]}`
	if err := stageLegacyCheckpoint(context.Background(), store, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.writer.QueryRow(`SELECT count(*) FROM legacy_nodes WHERE name IN ('tasks','taskId','taskSchema')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retired task authorization retained in stage: %d %v", count, err)
	}
}

func TestSQLiteAtomicityConfigurationAndReopen(t *testing.T) {
	store, path := sqliteTestStore(t)
	ctx := context.Background()
	if err := store.enableRuntime(ctx); err == nil {
		t.Fatal("uncommitted store enabled runtime")
	}
	if err := store.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE store_meta SET value='committed' WHERE key='cutover'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.enableRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.openReaders(ctx, path); err != nil {
		t.Fatal(err)
	}
	var connections []*sql.Conn
	for i := 0; i < sqliteReaderLimit; i++ {
		conn, err := store.readers.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		for pragma, want := range map[string]int{"foreign_keys": 1, "query_only": 1, "synchronous": 2, "temp_store": 1, "cache_size": -2048, "busy_timeout": 5000} {
			var got int
			if err := conn.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil || got != want {
				t.Fatalf("connection %d %s=%d want %d (%v)", i, pragma, got, want, err)
			}
		}
	}
	for _, c := range connections {
		c.Close()
	}
	if err := store.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO store_meta(key,value) VALUES('rolled-back','value')`); err != nil {
			return err
		}
		return errors.New("reject operation")
	}); err == nil {
		t.Fatal("domain rejection succeeded")
	}
	var count int
	if err := store.readers.QueryRow(`SELECT count(*) FROM store_meta WHERE key='rolled-back'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial transaction visible: %d %v", count, err)
	}
	if err := store.check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openSQLiteStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if err := reopened.enableRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := openSQLiteStore(ctx, filepath.Join(t.TempDir(), "missing.sqlite3")); err == nil {
		t.Fatal("missing authoritative database initialized")
	}
}
