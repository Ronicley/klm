package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func messageEditFixture(t *testing.T) (*app, Session) {
	t.Helper()
	store, ctx, _, parent := delegationStorageFixture(t)
	if err := prepareProtocolStaging(store.dir); err != nil {
		t.Fatal(err)
	}
	parent.Events = nil
	a := &app{ctx: ctx, dir: store.dir, store: store, sqlChanges: &sqliteChangeBus{}, runs: map[string]*turn{}, runtimes: map[string]*sessionRuntime{}, binaries: map[string]binary{parent.Harness: {path: filepath.Join(store.dir, "never-execute.exe")}}}
	_, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		parent.ResolvedModel, parent.ResolvedEffort = "resolved/model", "high"
		if err := st.putSessionMeta(parent); err != nil {
			return err
		}
		if err := st.putRoot("native_sessions", parent.ID, nativeSession{ID: "retained-native", Path: "old-history"}); err != nil {
			return err
		}
		if err := st.putSingleChild("session_usage", parent.ID, &SessionUsage{}); err != nil {
			return err
		}
		for _, e := range []Event{
			{ID: "keep", Type: "user", Text: "before selection", CreatedAt: now()},
			{ID: "selected", Type: "user", Text: "original input", Data: map[string]any{"original": "retained"}, CreatedAt: now()},
			{ID: "answer", Type: "assistant", Text: "original answer", Status: "completed", Favorite: true, CreatedAt: now()},
		} {
			if _, err := st.appendEvent(parent.ID, e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, parent
}

func reserveMessageEditFixture(t *testing.T, a *app, owner, eventID string, text *string) (*sqliteMessageEdit, context.Context) {
	t.Helper()
	a.mu.Lock()
	edit, reservation, ctx, err := a.reserveSQLMessageEdit(a.ctx, owner, eventID, text)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		reservation.cancel()
		for _, prepared := range edit.prepared {
			_ = a.store.discardEventGeneration(context.Background(), prepared.generation.ID)
		}
		a.mu.Lock()
		delete(a.runs, owner)
		close(reservation.done)
		a.mu.Unlock()
		a.wg.Done()
	})
	return &edit, ctx
}

func readMessageEditEvent(t *testing.T, a *app, owner string, ordinal int) Event {
	t.Helper()
	var e Event
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "session_events", sqliteOwnedKey(owner, strconv.Itoa(ordinal)), 4<<20, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSQLiteMessageRevertHistoryQueueAndRuntime(t *testing.T) {
	a, parent := messageEditFixture(t)
	before := []Event{readMessageEditEvent(t, a, parent.ID, 0), readMessageEditEvent(t, a, parent.ID, 1), readMessageEditEvent(t, a, parent.ID, 2)}
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		for _, status := range []string{"queued", "uncertain"} {
			q := QueuedMessage{ID: status, Text: status, Mode: "queue", Status: status}
			if err := st.putRecord("queued_messages", sqliteOwnedKey(parent.ID, q.ID), parent.ID, q.ID, -1, q); err != nil {
				return err
			}
			if err := st.putRoot("queue_payloads", q.ID, queuedPayload{Submission: submission{Text: status}}); err != nil {
				return err
			}
			if err := st.recordAcceptedMessage(parent.ID, q.ID, status+"-fingerprint"); err != nil {
				return err
			}
		}
		if err := st.putRecord("pending_permissions", sqliteOwnedKey(parent.ID, "p"), parent.ID, "p", -1, Permission{ID: "p"}); err != nil {
			return err
		}
		return st.putRecord("pending_questions", sqliteOwnedKey(parent.ID, "q"), parent.ID, "q", -1, QuestionRequest{ID: "q"})
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &sessionRuntime{app: a, id: parent.ID}
	a.runtimes[parent.ID] = runtime
	graphCtx, cancelGraph := context.WithCancel(a.ctx)
	defer cancelGraph()
	graph := &graphExecution{ctx: graphCtx, cancel: cancelGraph}
	a.graphRuns = map[string]*graphExecution{"independent": graph}
	sub, err := a.sqlChanges.subscribe(sqliteChangeScope{Kind: "conversation", ID: parent.ID, ProjectID: parent.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	defer a.sqlChanges.unsubscribe(sub)
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
	request.SetPathValue("id", parent.ID)
	request.SetPathValue("eventID", "selected")
	reply := httptest.NewRecorder()
	a.revertEvent(reply, request)
	if reply.Code != 200 {
		t.Fatal("SQL revert handler failed", reply.Code, reply.Body.String())
	}
	if !runtime.closed || a.runtimes[parent.ID] != nil || a.runs[parent.ID] != nil {
		t.Fatal("retained runtime/admission survived reset")
	}
	if graphCtx.Err() != nil || a.graphRuns["independent"] != graph {
		t.Fatal("message revert cancelled an independent graph")
	}
	for i, old := range before {
		actual := readMessageEditEvent(t, a, parent.ID, i)
		if i > 0 {
			if actual.Data["reverted"] != true {
				t.Fatal("history was not reverted", i)
			}
			delete(actual.Data, "reverted")
			if len(actual.Data) == 0 {
				actual.Data = nil
			}
		}
		if !reflect.DeepEqual(actual, old) {
			t.Fatalf("original history changed at %d: %#v %#v", i, old, actual)
		}
	}
	archive := readMessageEditEvent(t, a, parent.ID, 3)
	var previous nativeSession
	raw, _ := json.Marshal(archive.Data["nativeBeforeReset"])
	if err := json.Unmarshal(raw, &previous); err != nil || previous.ID != "retained-native" || archive.Data["sourceOrdinal"] != json.Number("1") || archive.Data["sourceEventId"] != "selected" {
		t.Fatal("native provenance missing", err, previous)
	}
	var next nativeSession
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "native_sessions", parent.ID, 64<<10, &next); err != nil || next.ID != "" || next.Path == "old-history" {
		t.Fatal("old native continuation survived", err, next)
	}
	for _, table := range []string{"queued_messages", "queue_payloads", "pending_permissions", "pending_questions"} {
		var count int
		if err := a.store.readers.QueryRow("SELECT count(*) FROM data_" + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("operational rows survived", table, count, err)
		}
	}
	var fingerprint string
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "accepted_messages", parent.ID+"/uncertain", 64<<10, &fingerprint); err != nil {
		t.Fatal("uncertain receipt lost", err)
	}
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "accepted_messages", parent.ID+"/queued", 64<<10, &fingerprint); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unsent receipt retained", err)
	}
	meta, err := a.store.readSessionMeta(a.ctx, parent.ID)
	if err != nil || meta.Status != "idle" || meta.ResolvedModel != "" || meta.ResolvedEffort != "" {
		t.Fatal("resolved settings survived", err, meta)
	}
	select {
	case <-sub.notices:
	default:
		t.Fatal("committed reset did not invalidate conversation")
	}
	page, err := a.store.readHistory(a.ctx, parent.ID, "2", "", 1)
	if err != nil || len(page.Events) != 1 || page.Events[0].Ordinal != "1" || page.Events[0].ID != "selected" {
		t.Fatal("history pagination changed", err, page)
	}
	if len(a.state.Sessions) != 0 || a.storageErr != nil {
		t.Fatal("reset touched legacy authority", a.storageErr)
	}
}

func TestSQLiteMessageRevertStreamedDuplicateOrdinals(t *testing.T) {
	a, parent := messageEditFixture(t)
	stream := io.MultiReader(strings.NewReader(`{"id":"duplicate","type":"assistant","status":"completed","data":{"native":"`), &openCodeRepeatedBytes{2 << 20}, strings.NewReader(`","retained":42},"text":"tail\n\u00e9"}`))
	g, err := a.store.stageEventGeneration(a.ctx, parent.ID, "duplicate", stream)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if _, err := st.publishEventGeneration(parent.ID, Event{ID: "duplicate"}, g); err != nil {
			return err
		}
		_, err := st.appendEvent(parent.ID, Event{ID: "duplicate", Type: "assistant", Text: "second duplicate", CreatedAt: now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.editSQLMessage(a.ctx, parent.ID, "selected", nil); err != nil {
		t.Fatal(err)
	}
	large := readMessageEditEvent(t, a, parent.ID, 3)
	second := readMessageEditEvent(t, a, parent.ID, 4)
	if large.ID != "duplicate" || large.Text != "tail\né" || len(large.Data["native"].(string)) != 2<<20 || large.Data["reverted"] != true || second.Text != "second duplicate" || second.Data["reverted"] != true {
		t.Fatal("streamed data/text or exact ordinals lost")
	}
	if err := a.editSQLMessage(a.ctx, parent.ID, "selected", nil); err == nil {
		t.Fatal("already-reverted selection accepted")
	}
}

func TestSQLiteMessageEditSelectionCASAndRollback(t *testing.T) {
	for _, change := range []string{"selection", "event", "append"} {
		t.Run(change, func(t *testing.T) {
			a, parent := messageEditFixture(t)
			edit, ctx := reserveMessageEditFixture(t, a, parent.ID, "selected", nil)
			if err := edit.prepare(ctx, a.store); err != nil {
				t.Fatal(err)
			}
			_, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
				switch change {
				case "selection":
					meta, err := st.sessionMeta(parent.ID)
					if err != nil {
						return err
					}
					meta.SelectedGraphID = "changed-selection"
					return st.putSessionMeta(meta)
				case "event":
					return st.patchEventMetadata(parent.ID, edit.key, func(e *Event) error { e.Title = "changed"; return nil })
				default:
					_, err := st.appendEvent(parent.ID, event("status", "concurrent event"))
					return err
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return edit.publish(a, st, false) })
			if !errors.Is(err, errSQLiteContentChanged) {
				t.Fatal("changed conversation accepted", err)
			}
			if readMessageEditEvent(t, a, parent.ID, 1).Data["reverted"] == true {
				t.Fatal("failed CAS mutated history")
			}
		})
	}
	t.Run("publication failure", func(t *testing.T) {
		a, parent := messageEditFixture(t)
		if _, err := a.store.writer.Exec(`CREATE TRIGGER reject_reset BEFORE UPDATE ON data_native_sessions BEGIN SELECT RAISE(FAIL,'injected native reset failure'); END`); err != nil {
			t.Fatal(err)
		}
		if err := a.editSQLMessage(a.ctx, parent.ID, "selected", nil); err == nil {
			t.Fatal("failed publication accepted")
		}
		if readMessageEditEvent(t, a, parent.ID, 1).Data["reverted"] == true {
			t.Fatal("failed transaction exposed reverted content")
		}
		var count int
		if err := a.store.readers.QueryRow(`SELECT count(*) FROM event_content_generations g WHERE NOT EXISTS(SELECT 1 FROM event_content_publications p WHERE p.generation_id=g.id)`).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed reset leaked staged generations", count, err)
		}
	})
}

func TestSQLiteMessageEditReservationAndValidation(t *testing.T) {
	a, parent := messageEditFixture(t)
	for _, text := range []string{"", "\x00", string([]byte{0xff}), strings.Repeat("x", (128<<10)+1)} {
		if err := a.editSQLMessage(a.ctx, parent.ID, "selected", &text); err == nil {
			t.Fatal("invalid edited text accepted")
		}
	}
	text := "valid edit"
	if err := a.editSQLMessage(a.ctx, parent.ID, "answer", &text); err == nil {
		t.Fatal("assistant edit accepted")
	}
	if err := a.editSQLMessage(a.ctx, parent.ID, "not-owned", nil); err == nil {
		t.Fatal("foreign selection accepted")
	}
	_, _ = reserveMessageEditFixture(t, a, parent.ID, "selected", nil)
	if err := a.editSQLMessage(a.ctx, parent.ID, "selected", nil); err == nil {
		t.Fatal("overlapping edit admitted")
	}
	r := httptest.NewRequest("POST", "/", nil)
	if _, err := a.admitSQLMessage(r, parent.ID, "blocked-admission", "new input", "queue", nil, nil, nil); err == nil {
		t.Fatal("message admission crossed edit reservation")
	}
	if a.stopVersions[parent.ID] == 0 {
		t.Fatal("in-flight message preparations not fenced")
	}
}

func TestSQLiteMessageEditAttachmentsAttribution(t *testing.T) {
	a, parent := messageEditFixture(t)
	file := ChatFile{ID: newID(), Name: "original.txt", Type: "text/plain", Size: 8}
	content := []byte("original")
	digest := sha256.Sum256(content)
	file.SHA256 = hex.EncodeToString(digest[:])
	ownerHash := sha256.Sum256([]byte(parent.ID))
	dir := filepath.Join(a.dir, "attachments", hex.EncodeToString(ownerHash[:]), "message-fixture")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, file.ID+".txt")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	origin := &SpawnOrigin{SessionID: "sender", Title: "Sender", Kind: "instruction", SourceUserEventID: "real-original-reference"}
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		e, ordinal, err := st.eventByID(parent.ID, "selected")
		if err != nil {
			return err
		}
		e.Type = "agent_prompt"
		e.Data["files"], e.Data["origin"] = []ChatFile{file}, origin
		e.Data["mentions"] = []Mention{{Path: "missing.txt", Kind: "file"}}
		return st.writeEvent(parent.ID, ordinal, e, false)
	})
	if err != nil {
		t.Fatal(err)
	}
	text := "edited instruction without removed reference"
	edit, ctx := reserveMessageEditFixture(t, a, parent.ID, "selected", &text)
	if len(edit.queue.Mentions) != 0 || !reflect.DeepEqual(edit.queue.Origin, origin) {
		t.Fatal("edit lost attribution or retained removed mention")
	}
	edit.payload, err = a.prepareEditedMessage(ctx, parent.ID, edit.folder, edit.meta.Harness, edit.queue)
	if err != nil || !strings.Contains(edit.payload.Context, "original") {
		t.Fatal("original attachment not rebuilt", err)
	}
	if err := edit.prepare(ctx, a.store); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return edit.publish(a, st, true) }); err != nil {
		t.Fatal(err)
	}
	var q []QueuedMessage
	if err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		var err error
		q, err = (&sqliteSessionTx{ctx: ctx, tx: tx}).queue(parent.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(q) != 1 || q[0].Text != text || !reflect.DeepEqual(q[0].Origin, origin) || len(q[0].Files) != 1 {
		t.Fatal("replacement metadata lost", q)
	}
	var payload queuedPayload
	if err := decodeSQLiteRecord(ctx, a.store.readers, "queue_payloads", q[0].ID, 2<<20, &payload); err != nil || !strings.Contains(payload.Submission.Text, "not a human message") {
		t.Fatal("replacement prompt promoted agent authority", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := a.prepareEditedMessage(ctx, parent.ID, edit.folder, edit.meta.Harness, edit.queue); err == nil {
		t.Fatal("missing original attachment accepted")
	}
}

func TestSQLiteMessageEditInstrumentedNativeReset(t *testing.T) {
	a, parent := messageEditFixture(t)
	manifest, err := delegationCodexManifestV1()
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		meta, err := st.sessionMeta(parent.ID)
		if err != nil {
			return err
		}
		meta.Harness = "codex"
		if err := st.putSessionMeta(meta); err != nil {
			return err
		}
		if err := st.deleteRecord("native_sessions", parent.ID); err != nil {
			return err
		}
		return st.putNativeDelegationMetadata(parent.ID, nativeSession{ID: "instrumented-thread", Delegation: &manifest})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error { return st.putRoot("native_sessions", parent.ID, nativeSession{}) }); err == nil {
		t.Fatal("generic writer discarded immutable manifest")
	}
	var project Project
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "projects", parent.ProjectID, 1<<20, &project); err != nil {
		t.Fatal(err)
	}
	parent.Harness = "codex"
	child, owned, batch, invocation := delegationStorageRecords(parent, project.Folder, "retained-edit")
	owned.ActiveInvocationID = ""
	invocation.State, invocation.Outcome, invocation.EndedAt = DelegationInvocationTerminal, DelegationCancelled, now()
	invocation.Finality = DelegationFinality{NativeSettled: true, ProcessesDrained: true, ToolCallsSettled: true}
	invocation.ResultDigest, err = delegationInvocationResultDigest(invocation)
	if err != nil {
		t.Fatal(err)
	}
	batch.State, batch.Delivery, batch.Outcome, batch.EndedAt, batch.ResultDigest = DelegationSettled, DelegationDeliveryCancelled, DelegationCancelled, now(), strings.Repeat("b", 64)
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		return persistDelegationStorageFixture(st, a.dir, child, owned, batch, invocation)
	}); err != nil {
		t.Fatal(err)
	}
	retained := map[string]string{}
	for _, table := range []string{"delegation_batches", "delegation_invocations", "delegation_children", "delegation_calls"} {
		var digest string
		if err := a.store.readers.QueryRow("SELECT digest FROM data_" + table + " LIMIT 1").Scan(&digest); err != nil {
			t.Fatal(err)
		}
		retained[table] = digest
	}
	if err := a.editSQLMessage(a.ctx, parent.ID, "selected", nil); err != nil {
		t.Fatal(err)
	}
	archive := readMessageEditEvent(t, a, parent.ID, 3)
	var previous nativeSession
	raw, _ := json.Marshal(archive.Data["nativeBeforeReset"])
	if err := json.Unmarshal(raw, &previous); err != nil || previous.ID != "instrumented-thread" || previous.Delegation == nil || *previous.Delegation != manifest {
		t.Fatal("instrumented provenance lost", err, previous)
	}
	var next nativeSession
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "native_sessions", parent.ID, 64<<10, &next); err != nil || next.ID != "" || next.Delegation != nil {
		t.Fatal("instrumented continuation replayed", err, next)
	}
	if err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error { return (sqliteValidation{ctx: a.ctx, tx: tx}).validateDelegationRecords() }); err != nil {
		t.Fatal("native reset corrupted delegation records", err)
	}
	for table, expected := range retained {
		var digest string
		if err := a.store.readers.QueryRow("SELECT digest FROM data_" + table + " LIMIT 1").Scan(&digest); err != nil || digest != expected {
			t.Fatal("native reset changed historical delegation provenance", table, err)
		}
	}
}

func TestSQLiteMessageEditImagesSourcesAndMissingAttachment(t *testing.T) {
	a, parent := messageEditFixture(t)
	image := ChatImage{ID: newID(), Name: "original.png", MIME: "image/png", Size: 5}
	bytes := []byte("image")
	digest := sha256.Sum256(bytes)
	stored := storedImage{ChatImage: image, SessionID: parent.ID, SHA256: hex.EncodeToString(digest[:]), Accepted: true}
	if err := os.MkdirAll(filepath.Join(a.dir, "images"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.dir, "images", image.ID)
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error { return st.putRoot("chat_images", image.ID, stored) }); err != nil {
		t.Fatal(err)
	}
	sources := []SourceReference{{SessionID: parent.ID, MessageID: "keep", Passage: "before selection"}}
	q, err := editedMessageMetadata(Event{Type: "user", Data: map[string]any{"images": []ChatImage{image}, "sources": sources}}, "edited")
	if err != nil || !reflect.DeepEqual(q.Sources, sources) {
		t.Fatal("edit lost selected sources", err, q)
	}
	var payload submission
	if err := a.prepareSQLEditedImages(a.ctx, parent.ID, q, &payload); err != nil || len(payload.Images) != 1 || payload.Images[0].SHA256 != stored.SHA256 {
		t.Fatal("legacy image payload not rebuilt", err)
	}
	q.ID = newID()
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.putRecord("queued_messages", sqliteOwnedKey(parent.ID, q.ID), parent.ID, q.ID, -1, q); err != nil {
			return err
		}
		if err := st.putRoot("queue_payloads", q.ID, queuedPayload{Submission: payload}); err != nil {
			return err
		}
		return st.appendQueuedUser(parent.ID, q)
	}); err != nil {
		t.Fatal(err)
	}
	delivered := readMessageEditEvent(t, a, parent.ID, 3)
	var images []ChatImage
	rawImages, _ := json.Marshal(delivered.Data["images"])
	if err := json.Unmarshal(rawImages, &images); err != nil || !reflect.DeepEqual(images, q.Images) {
		t.Fatal("queue execution discarded edited image metadata", images, err)
	}
	if err := a.prepareSQLEditedImages(a.ctx, "foreign-owner", q, &submission{}); err == nil {
		t.Fatal("foreign image accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := a.prepareSQLEditedImages(a.ctx, parent.ID, q, &submission{}); err == nil {
		t.Fatal("missing original image accepted")
	}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		e, ordinal, err := st.eventByID(parent.ID, "selected")
		if err != nil {
			return err
		}
		e.Data["files"] = []ChatFile{{ID: newID(), Name: "missing.txt", Size: 1, SHA256: strings.Repeat("a", 64)}}
		return st.writeEvent(parent.ID, ordinal, e, false)
	}); err != nil {
		t.Fatal(err)
	}
	text := "edited"
	if err := a.editSQLMessage(a.ctx, parent.ID, "selected", &text); err == nil {
		t.Fatal("missing original file accepted")
	}
	if readMessageEditEvent(t, a, parent.ID, 1).Data["reverted"] == true {
		t.Fatal("missing file failure mutated history")
	}
}

func TestSQLiteMessageEditNativeActivityGuards(t *testing.T) {
	for _, activity := range []string{"turn", "runtime", "delegation", "uncertain delegation"} {
		t.Run(activity, func(t *testing.T) {
			a, parent := messageEditFixture(t)
			switch activity {
			case "turn":
				ctx, cancel := context.WithCancel(a.ctx)
				defer cancel()
				a.runs[parent.ID] = &turn{ctx: ctx, cancel: cancel}
			case "runtime":
				a.runtimes[parent.ID] = &sessionRuntime{app: a, id: parent.ID, turnActive: true}
			case "delegation", "uncertain delegation":
				var project Project
				if err := decodeSQLiteRecord(a.ctx, a.store.readers, "projects", parent.ProjectID, 1<<20, &project); err != nil {
					t.Fatal(err)
				}
				child, owned, batch, invocation := delegationStorageRecords(parent, project.Folder, "edit-block")
				if activity == "uncertain delegation" {
					owned.Quarantined = true
					invocation.State, invocation.Outcome, invocation.EndedAt = DelegationInvocationTerminal, DelegationFinalityUnconfirmed, now()
					var err error
					invocation.ResultDigest, err = delegationInvocationResultDigest(invocation)
					if err != nil {
						t.Fatal(err)
					}
					batch.State, batch.Delivery, batch.Outcome, batch.EndedAt, batch.ResultDigest = DelegationSettled, DelegationDeliveryUncertain, DelegationFinalityUnconfirmed, now(), strings.Repeat("b", 64)
				}
				if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
					return persistDelegationStorageFixture(st, a.dir, child, owned, batch, invocation)
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.editSQLMessage(a.ctx, parent.ID, "selected", nil); err == nil {
				t.Fatal("active native work reset")
			}
			if readMessageEditEvent(t, a, parent.ID, 1).Data["reverted"] == true {
				t.Fatal("activity rejection mutated history")
			}
		})
	}
}
