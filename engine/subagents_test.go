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

func TestOpenCodeSubagentReuseKeepsCurrentInvocationRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &app{dir: t.TempDir(), state: diskState{
		Version: 2, Native: map[string]nativeSession{},
		Projects: []Project{{ID: "project", Folders: []string{}}},
		Sessions: []Session{{ID: "main", ProjectID: "project", Harness: "opencode", Status: "running", Events: []Event{}}},
	}}
	p := &adapter{app: a, turn: &turn{ctx: ctx, cancel: cancel}, id: "main", harness: "opencode", keys: map[string]string{}}
	child, err := p.ensureSubagent("native-child", "First task", "", "")
	if err != nil {
		t.Fatal(err)
	}
	tracked := &openCodeSubagent{adapter: child}
	update := func(key, status string) {
		t.Helper()
		if err := p.put(key, "subagent", key, "", status, false, map[string]any{"childSessionId": child.id}); err != nil {
			t.Fatal(err)
		}
		if err := tracked.updateTask(p, key, status); err != nil {
			t.Fatal(err)
		}
	}
	update("first", "running")
	update("first", "completed")
	if a.state.session(child.id).Status != "idle" {
		t.Fatal("first invocation did not finish")
	}
	// A new parent turn also reuses the persisted native child, with new tracking.
	if err := p.closeSubagents(false, false); err != nil {
		t.Fatal(err)
	}
	p.subagents = nil
	child, err = p.ensureSubagent("native-child", "Second task", "", "")
	if err != nil {
		t.Fatal(err)
	}
	tracked = &openCodeSubagent{adapter: child}
	update("second", "running")
	// An older completed part can be replayed after the new task starts.
	update("first", "completed")
	if a.state.session(child.id).Status != "running" || a.state.session("main").Events[1].Status != "running" {
		t.Fatal("older completion settled the reused child or its current task")
	}
	// Completion must flush queued reasoning before marking it terminal.
	if err := child.put("thought", "reasoning", "", "New work", "running", false, nil); err != nil {
		t.Fatal(err)
	}
	update("second", "completed")
	if err := p.closeSubagents(false, false); err != nil {
		t.Fatal(err)
	}
	if s := a.state.session(child.id); s.Status != "idle" || len(s.Events) != 1 || s.Events[0].Status != "completed" {
		t.Fatalf("current invocation did not flush and finish: %#v", s)
	}
	// Cancellation/error of a later invocation must not rewrite finished history.
	if err := p.finishSubagent(child, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if parent := a.state.session("main"); parent.Events[0].Status != "completed" || parent.Events[1].Status != "completed" {
		t.Fatal("child shutdown rewrote earlier terminal task entries")
	}
}
