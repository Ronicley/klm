package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sqliteDeletionFixture(t *testing.T) (*app, Session) {
	t.Helper()
	store, ctx, _, parent := delegationStorageFixture(t)
	return &app{ctx: ctx, store: store, dir: t.TempDir(), sqlChanges: &sqliteChangeBus{}, runs: map[string]*turn{}, runtimes: map[string]*sessionRuntime{}}, parent
}

func requestSQLDeletion(t *testing.T, a *app, id string, status int) (ids []string, cleanup string) {
	t.Helper()
	req := httptest.NewRequest("DELETE", "/api/sessions/"+id, nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	a.deleteSession(w, req)
	if w.Code != status {
		t.Fatalf("delete %s: %d %s", id, w.Code, w.Body.String())
	}
	if status == 200 {
		var result struct {
			IDs     []string `json:"deletedSessionIds"`
			Cleanup string   `json:"cleanupError"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.IDs, result.Cleanup
	}
	return nil, ""
}

func TestSQLiteSessionDeletionPersisted(t *testing.T) {
	a, root := sqliteDeletionFixture(t)
	other := root
	other.ID, other.Title = "independent", "Independent"
	side := root
	side.ID, side.ParentID, side.Role = "side", root.ID, sessionRoleSideAgent
	node := root
	node.ID, node.Role, node.GraphRunID, node.ExecutionCWD = "node", "graph_node", "owned-run", t.TempDir()
	general := Session{ID: "general", Role: sessionRoleGeneralAgent, Harness: "opencode", ExecutionCWD: t.TempDir(), Status: "idle"}
	imageID := strings.Repeat("a", 32)
	if err := os.Mkdir(filepath.Join(a.dir, "images"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.dir, "images", imageID), []byte("owned image"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		for _, s := range []Session{other, side, node, general} {
			if err := st.createSession(s, a.dir); err != nil {
				return err
			}
		}
		if _, err := st.appendEvent(root.ID, event("assistant", "owned history")); err != nil {
			return err
		}
		if _, err := st.appendEvent(other.ID, Event{ID: "human", Type: "user", Text: "Create a conversation"}); err != nil {
			return err
		}
		if err := st.recordAcceptedMessage(root.ID, "accepted", "fingerprint"); err != nil {
			return err
		}
		if err := st.putRoot("chat_images", imageID, storedImage{ChatImage: ChatImage{ID: imageID}, SessionID: root.ID}); err != nil {
			return err
		}
		if err := st.putRoot("graph_runs", "owned-run", GraphRun{ID: "owned-run", ProjectID: root.ProjectID, ConversationID: root.ID, Status: "completed"}); err != nil {
			return err
		}
		if err := st.putRoot("graph_runs", "other-run", GraphRun{ID: "other-run", ProjectID: root.ProjectID, ConversationID: other.ID, Status: "completed"}); err != nil {
			return err
		}
		if err := st.putRoot("graph_activations", "owned-activation", GraphActivation{ID: "owned-activation", RunID: "owned-run", Status: "completed"}); err != nil {
			return err
		}
		if err := st.putRoot("native_sessions", side.ID, nativeSession{ID: "native-side"}); err != nil {
			return err
		}
		if err := st.putRecord("queued_messages", sqliteOwnedKey(side.ID, "queued"), side.ID, "queued", -1, QueuedMessage{ID: "queued", Status: "paused"}); err != nil {
			return err
		}
		if err := st.putRoot("queue_payloads", "queued", queuedPayload{Submission: submission{Text: "unsent"}}); err != nil {
			return err
		}
		if err := st.putRoot("session_spawns", "spawn-incoming", SessionSpawn{From: other.ID, SessionID: root.ID, OperationID: "spawn-once"}); err != nil {
			return err
		}
		if err := st.putRoot("session_spawns", "spawn-independent", SessionSpawn{From: root.ID, SessionID: other.ID, OperationID: "independent-once"}); err != nil {
			return err
		}
		if err := st.putRoot("session_commands", "send-incoming", SessionCommand{From: other.ID, Request: sendArgs{SessionID: root.ID, OperationID: "send-once", Instruction: "accepted"}}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	generation, err := a.store.stageEventGeneration(a.ctx, root.ID, "generation", strings.NewReader(`{"id":"generation","type":"assistant","text":"owned generation"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		_, err := st.publishEventGeneration(root.ID, Event{ID: "generation", Type: "assistant"}, generation)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sub, err := a.sqlChanges.subscribe(sqliteChangeScope{Kind: "conversation", ID: root.ID, ProjectID: root.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	defer a.sqlChanges.unsubscribe(sub)
	for range 2 {
		ids, cleanup := requestSQLDeletion(t, a, root.ID, 200)
		if cleanup != "" || !slices.Equal(ids, []string{"node", "parent", "side"}) {
			t.Fatal(ids, cleanup)
		}
	}
	select {
	case <-sub.notices:
	default:
		t.Fatal("committed deletion did not invalidate open owner")
	}
	if _, err := a.store.readSessionMeta(a.ctx, other.ID); err != nil {
		t.Fatal("independent conversation removed", err)
	}
	var generations int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM event_content_generations WHERE owner_id=?`, root.ID).Scan(&generations); err != nil || generations != 0 {
		t.Fatal("owned generations remain", generations, err)
	}
	for _, table := range []string{"session_events", "queued_messages", "queue_payloads", "accepted_messages", "native_sessions", "session_spawns", "session_commands", "graph_activations", "chat_images"} {
		var n int
		where := "1=1"
		if table == "session_events" {
			where = "owner_id<>'independent'"
		}
		if err := a.store.readers.QueryRow("SELECT count(*) FROM data_" + table + " WHERE " + where).Scan(&n); err != nil || n != 0 {
			t.Fatalf("owned %s remains: %d %v", table, n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(a.dir, "images", imageID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned image remains", err)
	}
	if _, err := a.sendSQLSessionLocked(other, sendArgs{SessionID: root.ID, OperationID: "send-once", Instruction: "accepted"}); err == nil {
		t.Fatal("deleted send replay accepted")
	}
	if err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
		_, err := (&sqliteSessionTx{ctx: a.ctx, tx: tx}).existingSpawn(other, spawnArgs{OperationID: "spawn-once", SourceUserEventID: "human"})
		if err == nil {
			t.Fatal("deleted spawn replay accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error { return st.createSession(root, a.dir) })
	if err == nil {
		t.Fatal("deleted identity recreated")
	}
	path := filepath.Join(a.store.dir, "fixture.sqlite3")
	if err := a.store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openSQLiteStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if err := reopened.openReaders(a.ctx, path); err != nil {
		t.Fatal(err)
	}
	a.store = reopened
	requestSQLDeletion(t, a, root.ID, 200)
	if _, err := reopened.readSessionMeta(a.ctx, root.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted owner survived reopen", err)
	}
	if len(a.state.Sessions) != 0 || len(a.state.DeletedSessions) != 0 || a.storageErr != nil {
		t.Fatal("legacy authority used", a.storageErr)
	}
}

func TestSQLiteSessionDeletionBlocks(t *testing.T) {
	for _, condition := range []string{"general", "child", "running-child", "native-process", "starting", "paused", "settling", "consultation", "automation"} {
		t.Run(condition, func(t *testing.T) {
			a, root := sqliteDeletionFixture(t)
			id, status := root.ID, 409
			_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
				switch condition {
				case "general":
					s := Session{ID: "general", Role: sessionRoleGeneralAgent, Status: "idle", ExecutionCWD: t.TempDir()}
					id, status = s.ID, 400
					return st.createSession(s, a.dir)
				case "child", "running-child":
					s := root
					s.ID, s.ParentID, s.Role = "side", root.ID, sessionRoleSideAgent
					if condition == "child" {
						id, status = s.ID, 400
					} else {
						s.Status = "running"
					}
					return st.createSession(s, a.dir)
				case "native-process":
					a.runtimes[root.ID] = &sessionRuntime{turnActive: true}
				case "starting":
					a.graphStarting = map[string]bool{root.ID: true}
				case "paused":
					return st.putRoot("graph_runs", "run", GraphRun{ID: "run", ConversationID: root.ID, Status: "running", PauseRequested: true, Paused: true})
				case "settling":
					return st.putRoot("graph_runs", "run", GraphRun{ID: "run", ConversationID: root.ID, Status: "ending"})
				case "consultation":
					return st.putRoot("consultations", "consult", Consultation{ID: "consult", From: root.ID, To: root.ID, Status: "answering"})
				case "automation":
					return st.putRoot("automation_runs", "run", AutomationRun{ID: "run", SessionID: root.ID, Status: "running"})
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			requestSQLDeletion(t, a, id, status)
			if _, err := a.store.readSessionMeta(a.ctx, root.ID); err != nil {
				t.Fatal("blocked deletion mutated owner", err)
			}
			var n int
			if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_deleted_sessions`).Scan(&n); err != nil || n != 0 {
				t.Fatal("blocked deletion left tombstones", n, err)
			}
		})
	}
}

func TestSQLiteSessionDeletionCleanupRetry(t *testing.T) {
	a, root := sqliteDeletionFixture(t)
	hash := sha256.Sum256([]byte(root.ID))
	owned := filepath.Join(a.dir, "attachments", hex.EncodeToString(hash[:]))
	if err := os.MkdirAll(owned, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owned, "file.txt"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	// A file in place of a managed directory deterministically forces cleanup
	// failure on Windows and Unix without permissions tricks or live handles.
	if err := os.WriteFile(filepath.Join(a.dir, "sessions"), []byte("block cleanup"), 0600); err != nil {
		t.Fatal(err)
	}
	_, cleanup := requestSQLDeletion(t, a, root.ID, 200)
	if cleanup == "" {
		t.Fatal("cleanup failure not disclosed")
	}
	var tombstone deletedSession
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "deleted_sessions", root.ID, 1<<20, &tombstone); err != nil || !tombstone.CleanupPending {
		t.Fatal("pending cleanup lost", err)
	}
	if err := os.Remove(filepath.Join(a.dir, "sessions")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(a.dir, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(a.dir, "sessions", root.ID+"-"+newID()+".jsonl")
	if err := os.WriteFile(native, []byte("owned native"), 0600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(a.dir, "sessions", root.ID+"-unrelated.jsonl")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.store.dir, "fixture.sqlite3")
	if err := a.store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openSQLiteStore(a.ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if err := reopened.openReaders(a.ctx, path); err != nil {
		t.Fatal(err)
	}
	a.store = reopened
	if err := a.cleanupSQLDeletedSessions(""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(native); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("native cleanup incomplete", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("unowned file removed", err)
	}
	if _, cleanup := requestSQLDeletion(t, a, root.ID, 200); cleanup != "" {
		t.Fatal(cleanup)
	}
	tombstone = deletedSession{}
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "deleted_sessions", root.ID, 1<<20, &tombstone); err != nil || tombstone.CleanupPending {
		t.Fatal("cleanup not acknowledged", err)
	}
}
