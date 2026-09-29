package main

import (
	"context"
	"testing"
)

func TestSubagentSessionsPersistBesideSideAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	a := &app{dir: dir, historyJournal: map[string]*sessionJournal{}, state: diskState{
		Version:  2,
		Native:   map[string]nativeSession{},
		Projects: []Project{{ID: "project", Folders: []string{}}},
		Sessions: []Session{
			{ID: "main", ProjectID: "project", Title: "Main", Workspace: "Ungrouped", Harness: "opencode", Status: "running", Events: []Event{}},
			{ID: "side", ParentID: "main", Role: sessionRoleSideAgent, ProjectID: "project", Title: "Side agent", Workspace: "Ungrouped", Harness: "opencode", Status: "idle", Events: []Event{}},
		},
	}}
	// Preserve the startup base; subsequent mutations must recover from the journal.
	if err := saveState(dir, &a.state); err != nil {
		t.Fatal(err)
	}
	p := &adapter{app: a, turn: &turn{ctx: ctx, cancel: cancel}, id: "main", harness: "opencode", keys: map[string]string{}, subagents: map[string]*adapter{}}
	first, err := p.ensureSubagent("native-one", "Explore engine", "openai/model", "high")
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.ensureSubagent("native-two", "Explore desktop", "openai/model", "high")
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || second == nil || first.id == second.id {
		t.Fatal("subagents were not created independently")
	}
	if err := p.put("task-one", "subagent", "Explore engine", "", "running", false, map[string]any{"childSessionId": first.id}); err != nil {
		t.Fatal(err)
	}
	if err := p.finishSubagent(first, "completed"); err != nil {
		t.Fatal(err)
	}
	if got := a.state.session(first.id); got == nil || got.Role != sessionRoleSubagent || got.Status != "idle" {
		t.Fatalf("unexpected first subagent state: %#v", got)
	}
	if got := a.state.session("main").Events[0].Status; got != "completed" {
		t.Fatalf("parent event status = %q, want completed", got)
	}
	if err := p.closeSubagents(false, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := replayStreamJournal(dir, &loaded); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.id, second.id} {
		if child := loaded.session(id); child == nil || child.ParentID != "main" || child.Role != sessionRoleSubagent || child.Status != "idle" {
			t.Fatalf("subagent identity or relationship was not recovered: %#v", child)
		}
	}
	if parent := loaded.session("main"); parent == nil || len(parent.Events) == 0 || parent.Events[0].Status != "completed" {
		t.Fatal("completed parent event was not recovered")
	}
	if loaded.linked("main") == nil || loaded.linked("main").ID != "side" {
		t.Fatal("subagents displaced the side agent relationship")
	}
}
