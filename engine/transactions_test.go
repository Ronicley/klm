package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func transactionFixture(t *testing.T) *app {
	t.Helper()
	a := &app{dir: t.TempDir(), state: diskState{Version: 2,
		Projects: []Project{{ID: "p", Folders: []string{}, ArchivedFolders: []string{}}}, Native: map[string]nativeSession{},
		Sessions: []Session{
			{ID: "s", ProjectID: "p", Status: "idle", Events: []Event{{ID: "user", Type: "user", Text: "Run checks"}, {ID: "assistant", Type: "assistant", Text: "before", Status: "completed"}}},
			{ID: "other", ProjectID: "p", Status: "idle", Events: []Event{{ID: "other-event", Type: "assistant", Text: strings.Repeat("history ", 1024*1024)}}},
		}}}
	if err := saveState(a.dir, &a.state); err != nil {
		t.Fatal(err)
	}
	a.indexMessageIDsLocked()
	return a
}

func assertRecoveredTransaction(t *testing.T, a *app) diskState {
	t.Helper()
	d, err := loadState(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := replayStreamJournal(a.dir, &d); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(a.state)
	got, _ := json.Marshal(d)
	if string(got) != string(want) {
		t.Fatal("replay differs from published state")
	}
	return d
}

func TestTransactionRecoveryCheckpointInterleaving(t *testing.T) {
	a := transactionFixture(t)
	p := &adapter{app: a, id: "s", turn: &turn{ctx: context.Background()}, completed: true}
	// Queue intent, receipt, a graph activity and BOTH consultation projections
	// commit together. No harness/process is used by this storage check.
	a.mu.Lock()
	err := a.graphChangeLocked(func(d *diskState) error {
		s := d.session("s")
		s.Queue = []QueuedMessage{{ID: "q", Text: "next", Mode: "steer", Status: "steering"}}
		d.QueuePayloads = map[string]queuedPayload{"q": {Submission: submission{Text: "next"}}}
		d.AcceptedMessages = map[string]string{"s/q": "fingerprint"}
		d.GraphActivities = []GraphActivity{{ID: "activity", ConversationID: "s", ProjectID: "p", GraphID: "checks", Objective: "Check work", Status: "scheduled", Version: 1, Authorization: []GraphAuthorization{{EventID: "user", Text: "Run checks"}}, CreatedAt: now(), UpdatedAt: now()}}
		d.Consultations = []Consultation{{ID: "consult", From: "s", To: "other", Topic: "Review", Question: "Check work", Status: "queued", Delivery: "waiting", CreatedAt: now()}}
		syncConsultation(d, &d.Consultations[0])
		return nil
	})
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	assertRecoveredTransaction(t, a)
	q, _, err := p.takeSteering()
	if err != nil || q == nil {
		t.Fatalf("claim: %v", err)
	}
	// Recover the sending claim, without auto-delivering/replaying it.
	if d := assertRecoveredTransaction(t, a); d.session("s").Queue[0].Status != "sending" {
		t.Fatal("claim not durable")
	}
	if err := p.finishSteering(q.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := p.commitStreamUpdates([]streamUpdate{{event: Event{ID: "assistant", Text: " delta"}, appendText: true}}); err != nil {
		t.Fatal(err)
	}
	sealedRevision := a.state.GraphRevision
	// Emulate interruption immediately after the production rotation boundary.
	// New commands/streams must be replayed AFTER this immutable segment.
	if err := replaceFile(streamJournalPath(a.dir), sealedJournalPath(a.dir)); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	err = a.commitLocked(func(d *diskState) {
		s := d.session("s")
		e := s.Events[1]
		e.Favorite = true
		d.setEvent(s, 1, e)
		s.YOLO = true
		d.Native["s"] = nativeSession{ID: "native"}
		d.Sessions = moveSessionBefore(d.Sessions, "other", "s")
	})
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.commitStreamUpdates([]streamUpdate{{event: Event{ID: "assistant", Text: " after seal"}, appendText: true}}); err != nil {
		t.Fatal(err)
	}
	assertRecoveredTransaction(t, a)
	if err := a.checkpointJournal(); err != nil {
		t.Fatal(err)
	}
	base, err := loadState(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if base.GraphRevision != sealedRevision {
		t.Fatal("checkpoint included active/unsealed revisions")
	}
	assertRecoveredTransaction(t, a)
	a.mu.Lock()
	err = p.finishTurnStateLocked(nil)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.checkpointJournal(); err != nil {
		t.Fatal(err)
	}
	assertRecoveredTransaction(t, a)
	// Crash after checkpoint replace but before removal: replay skips old frames.
	if err := appendStreamRecord(a.dir, streamRecord{Revision: a.state.GraphRevision, Session: "s", Updates: []streamOperation{{Event: Event{ID: "assistant", Text: "DO NOT APPLY"}}}}); err != nil {
		t.Fatal(err)
	}
	assertRecoveredTransaction(t, a)
}

func TestTransactionFailureAndPartialTail(t *testing.T) {
	a := transactionFixture(t)
	before, _ := json.Marshal(a.state)
	a.mu.Lock()
	err := a.graphChangeLocked(func(d *diskState) error {
		d.session("s").Title = "rejected"
		return errors.New("domain rejection")
	})
	a.mu.Unlock()
	if err == nil || a.storageErr != nil {
		t.Fatal("domain rejection poisoned storage")
	}
	if err := os.Mkdir(streamJournalPath(a.dir), 0700); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	err = a.commitLocked(func(d *diskState) {
		s := d.session("s")
		s.Title = "not published"
		e := s.Events[1]
		e.Text = "not published"
		d.setEvent(s, 1, e)
		d.AcceptedMessages = map[string]string{"s/failed": "not published"}
		s.Queue = []QueuedMessage{{ID: "failed"}}
	})
	a.mu.Unlock()
	after, _ := json.Marshal(a.state)
	if err == nil || a.storageErr == nil || string(before) != string(after) {
		t.Fatal("failed write published state")
	}
	if err := os.Remove(streamJournalPath(a.dir)); err != nil {
		t.Fatal(err)
	}
	a.storageErr = nil // isolated restart-equivalent fixture only
	a.mu.Lock()
	err = a.commitLocked(func(d *diskState) { d.session("s").Title = "durable" })
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(streamJournalPath(a.dir))
	if err != nil {
		t.Fatal(err)
	}
	// A partial transaction cannot expose any of its changes.
	if err := appendStreamRecord(a.dir, streamRecord{Revision: a.state.GraphRevision + 1, Format: 1, Transaction: []statePatch{{Path: []string{"sessions", "0", "title"}, Value: json.RawMessage(`"partial"`)}}}); err != nil {
		t.Fatal(err)
	}
	full, _ := os.Stat(streamJournalPath(a.dir))
	if err := os.Truncate(streamJournalPath(a.dir), full.Size()-2); err != nil {
		t.Fatal(err)
	}
	assertRecoveredTransaction(t, a)
	truncated, _ := os.Stat(streamJournalPath(a.dir))
	if truncated.Size() != info.Size() {
		t.Fatal("incomplete frame not removed")
	}
	// Complete corruption is not silently discarded.
	f, err := os.OpenFile(streamJournalPath(a.dir), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteAt([]byte{0xff}, 8)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	d, err := loadState(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := replayStreamJournal(a.dir, &d); err == nil {
		t.Fatal("corrupt complete frame accepted")
	}
}

func TestTransactionReplacementAndNewSession(t *testing.T) {
	a := transactionFixture(t)
	a.mu.Lock()
	err := a.commitLocked(func(d *diskState) {
		n := int64(12)
		d.session("s").Usage = &SessionUsage{InputTokens: &n, OutputTokens: &n}
		d.Sessions = append(d.Sessions, Session{ID: "new", ProjectID: "p", Status: "idle", Events: []Event{}})
		for i := 0; i < 8; i++ {
			d.session("s").Events = append(d.session("s").Events, event("assistant", "ordered"))
		}
	})
	update := a.currentSessionUpdateLocked("s")
	for i, change := range update.Changes {
		if i > 0 && change.Index <= update.Changes[i-1].Index {
			t.Fatal("SSE append changes are not ordered")
		}
	}
	if err == nil {
		err = a.commitLocked(func(d *diskState) { n := int64(5); d.session("s").Usage = &SessionUsage{InputTokens: &n} })
	}
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	d := assertRecoveredTransaction(t, a)
	if d.session("s").Usage.OutputTokens != nil || d.session("new").Events == nil {
		t.Fatal("replacement/empty new history semantics lost")
	}
	// Metadata-only transactions reuse even a long active history, never clone it.
	next := transactionState(a.state)
	if &next.session("other").Events[0] != &a.state.session("other").Events[0] {
		t.Fatal("transaction cloned history")
	}
	if !reflect.DeepEqual(next.session("s").Usage, a.state.session("s").Usage) {
		t.Fatal("metadata clone lost usage")
	}
}

func TestTransactionConcurrentCheckpoint(t *testing.T) {
	a := transactionFixture(t)
	p := &adapter{app: a, id: "s"}
	var wg sync.WaitGroup
	errors := make(chan error, 9)
	for worker := 0; worker < 3; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				var err error
				switch worker {
				case 0:
					err = p.commitStreamUpdates([]streamUpdate{{event: Event{ID: "assistant", Text: "x"}, appendText: true}})
				case 1:
					a.mu.Lock()
					err = a.commitLocked(func(d *diskState) { d.session("s").YOLO = i%2 == 0 })
					a.mu.Unlock()
				case 2:
					err = a.checkpointJournal()
				}
				if err != nil {
					errors <- err
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	d := assertRecoveredTransaction(t, a)
	if d.session("s").Events[1].Text != "beforexxx" || !d.session("s").YOLO {
		t.Fatal("concurrent writer/checkpoint lost updates")
	}
}

func TestTransactionLegacyMigration(t *testing.T) {
	for _, version := range []int{1, 2} {
		a := transactionFixture(t)
		encoded, _ := json.Marshal(a.state)
		var legacy map[string]any
		if err := json.Unmarshal(encoded, &legacy); err != nil {
			t.Fatal(err)
		}
		delete(legacy, "journalFormat")
		legacy["version"] = version
		encoded, _ = json.Marshal(legacy)
		if err := os.WriteFile(filepath.Join(a.dir, "state.json"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
		if err := appendStreamRecord(a.dir, streamRecord{Revision: 1, Session: "s", Updated: now(), Updates: []streamOperation{{Event: Event{ID: "assistant", Text: "legacy delta"}}}}); err != nil {
			t.Fatal(err)
		}
		d, err := loadState(a.dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := replayStreamJournal(a.dir, &d); err != nil {
			t.Fatal(err)
		}
		if err := saveState(a.dir, &d); err != nil {
			t.Fatal(err)
		}
		reloaded, err := loadState(a.dir)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Version != 2 || reloaded.JournalFormat != 1 || reloaded.session("s").Events[1].Text != "legacy delta" {
			t.Fatal("legacy data or stream delta lost on migration")
		}
	}
}

type unlockedRecorder struct {
	*httptest.ResponseRecorder
	t   *testing.T
	app *app
}

func (w unlockedRecorder) WriteHeader(code int) {
	if !w.app.mu.TryLock() {
		w.t.Fatal("HTTP write holds app.mu")
	}
	w.app.mu.Unlock()
	w.ResponseRecorder.WriteHeader(code)
}
func (w unlockedRecorder) Write(b []byte) (int, error) {
	if !w.app.mu.TryLock() {
		w.t.Fatal("HTTP write holds app.mu")
	}
	w.app.mu.Unlock()
	return w.ResponseRecorder.Write(b)
}

func TestTransactionHTTPOutsideLock(t *testing.T) {
	a := transactionFixture(t)
	for _, tc := range []struct {
		handler      http.HandlerFunc
		method, body string
	}{
		{a.updatePermissionSettings, "PATCH", `{"yolo":true}`},
		{a.patchEvent, "PATCH", `{"favorite":true}`},
		{a.changeQueuedMessage, "DELETE", ""}, // missing queue item error also unlocks
		{a.stop, "POST", ""},
	} {
		r := httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body))
		r.SetPathValue("id", "s")
		r.SetPathValue("eventID", "assistant")
		w := unlockedRecorder{httptest.NewRecorder(), t, a}
		tc.handler(w, r)
	}
}
