package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type updateRecorder struct {
	*httptest.ResponseRecorder
	onFlush func()
}

func (w *updateRecorder) Flush() {
	w.ResponseRecorder.Flush()
	w.onFlush()
}

func TestMultiplexedStreamResumesObservedSession(t *testing.T) {
	a := &app{dir: t.TempDir(), state: diskState{GraphRevision: 1, GraphViewRevision: 1, Projects: []Project{{ID: "p", Folders: []string{}}}, Sessions: []Session{
		{ID: "old", ProjectID: "p", Status: "idle", Events: []Event{{ID: "first", Text: "first"}}},
		{ID: "new", ProjectID: "p", Status: "idle", Events: []Event{{ID: "second", Text: "second"}}},
	}}, journalBase: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r := httptest.NewRequest("GET", `/api/updates?ids=old,new&cursors=%7B%22old%22%3A1%7D`, nil).WithContext(ctx)
	flushes := 0
	w := &updateRecorder{ResponseRecorder: httptest.NewRecorder()}
	w.onFlush = func() {
		flushes++
		if flushes == 3 {
			p := &adapter{app: a, id: "new"}
			if err := p.commitStreamUpdates([]streamUpdate{{event: Event{ID: "second", Text: "final"}}}); err != nil {
				t.Fatal(err)
			}
		}
		if flushes == 4 {
			cancel()
		}
	}
	a.updates(w, r)
	if flushes != 4 {
		t.Fatalf("expected inventory, two initial updates and one directed delta; got %d", flushes)
	}
	resumed, hydrated, streamed := false, false, false
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var packet struct {
			Kind   string        `json:"kind"`
			Update SessionUpdate `json:"update"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &packet); err != nil {
			t.Fatal(err)
		}
		if packet.Kind != "session" {
			continue
		}
		u := packet.Update
		if u.Summary.ID == "old" {
			resumed = u.Kind == "delta" && u.History == nil
		}
		if u.Summary.ID == "new" && u.Revision == 1 {
			hydrated = u.Kind == "reset" && u.History != nil && len(u.History.Events) == 1
		}
		if u.Summary.ID == "new" && u.Revision == 2 {
			streamed = u.Kind == "delta" && len(u.Changes) == 1 && u.Changes[0].Event.Text == "final"
		}
	}
	if !resumed || !hydrated || !streamed {
		t.Fatal("logical cursors lost history or live changes")
	}
	if len(a.updateListeners) != 0 {
		t.Fatal("disconnected subscriber was retained")
	}
}
