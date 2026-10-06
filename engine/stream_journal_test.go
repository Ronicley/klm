package main

import (
	"os"
	"reflect"
	"testing"
)

func TestStreamJournalRecoveryAndNoOp(t *testing.T) {
	dir := t.TempDir()
	state := diskState{Version: 2, Projects: []Project{{ID: "p", Folders: []string{}}}, Sessions: []Session{{ID: "s", ProjectID: "p", Status: "idle", Events: []Event{{ID: "e", Type: "assistant", Text: "before"}}}}, Native: map[string]nativeSession{}}
	if err := saveState(dir, &state); err != nil {
		t.Fatal(err)
	}
	a := &app{dir: dir, state: state}
	p := &adapter{app: a, id: "s"}
	update := streamUpdate{event: Event{ID: "e", Text: "after"}}
	if err := p.commitStreamUpdates([]streamUpdate{update}); err != nil {
		t.Fatal(err)
	}
	revision := a.state.GraphRevision
	if err := p.commitStreamUpdates([]streamUpdate{update}); err != nil {
		t.Fatal(err)
	}
	if revision != a.state.GraphRevision {
		t.Fatal("identical upsert wrote another record")
	}
	loaded, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := replayStreamJournal(dir, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.session("s").Events[0].Text != "after" || loaded.GraphRevision != revision {
		t.Fatal("stream record was not recovered")
	}
	if err := saveState(dir, &loaded); err != nil {
		t.Fatal(err)
	}
	// Crash after checkpoint replacement but before journal cleanup must not
	// duplicate an event, and an incomplete final frame can be truncated.
	f, err := os.OpenFile(streamJournalPath(dir), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{4, 0}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := replayStreamJournal(dir, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.session("s").Events[0].Text != "after" || checkpoint.GraphRevision != revision {
		t.Fatal("checkpoint replay changed committed state")
	}
}

func TestNewSubscriptionResetsAtCurrentRevision(t *testing.T) {
	a := &app{state: diskState{GraphRevision: 40, GraphViewRevision: 20, Projects: []Project{{ID: "p", Folders: []string{}}}, Sessions: []Session{{ID: "old", ProjectID: "p", Status: "idle", Events: []Event{{ID: "first", Text: "first"}}}, {ID: "new", ProjectID: "p", Status: "idle", Events: []Event{{ID: "second", Text: "second"}}}}}, journalBase: 40}
	update := a.sessionUpdateLocked("new", 40, true)
	if update == nil || update.Kind != "reset" || update.History == nil || len(update.History.Events) != 1 || update.History.Events[0].ID != "second" {
		t.Fatal("new logical subscription must receive its history regardless of the global revision")
	}
}

func TestStreamOwnsNativePayloadAndUsage(t *testing.T) {
	dir := t.TempDir()
	a := &app{dir: dir, state: diskState{Version: 2, Projects: []Project{{ID: "p", Folders: []string{}}}, Sessions: []Session{{ID: "s", ProjectID: "p", Status: "idle", Events: []Event{}}}, Native: map[string]nativeSession{}}}
	if err := saveState(dir, &a.state); err != nil {
		t.Fatal(err)
	}
	p := &adapter{app: a, id: "s", turn: &turn{}, keys: map[string]string{}, subagent: true}
	part := map[string]any{"text": "first"}
	if err := p.put("part", "assistant", "", "first", "running", false, map[string]any{"part": part}); err != nil {
		t.Fatal(err)
	}
	before := a.state.session("s").Events[0]
	part["text"] = "second"
	if before.Data["part"].(map[string]any)["text"] != "first" {
		t.Fatal("native map mutated published state")
	}
	count := int64(12)
	usage := &SessionUsage{InputTokens: &count}
	if err := p.setUsage(usage); err != nil {
		t.Fatal(err)
	}
	count = 99
	loaded, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := replayStreamJournal(dir, &loaded); err != nil {
		t.Fatal(err)
	}
	if *loaded.session("s").Usage.InputTokens != 12 || !reflect.DeepEqual(loaded.session("s").Usage, a.state.session("s").Usage) {
		t.Fatal("usage was not owned and recovered")
	}
}
