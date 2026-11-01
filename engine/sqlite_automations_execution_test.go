package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func sqliteAutomationApp(t *testing.T) (*app, Session) {
	t.Helper()
	t.Setenv("KLM_USER_SKILLS_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	a := &app{ctx: ctx, dir: dir, store: store, runs: map[string]*turn{}, runtimes: map[string]*sessionRuntime{}, binaries: map[string]binary{"pi": {path: filepath.Join(dir, "intentionally-missing-harness.exe")}}}
	t.Cleanup(func() { cancel(); a.wg.Wait(); store.close() })
	project := Project{ID: "project", Name: "Fixture", Folder: t.TempDir()}
	s := newTopLevelSession(project.ID, "Source", "Ungrouped", "pi", "saved-model", "")
	_, err = store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("projects", project.ID, project); err != nil {
			return err
		}
		topic, err := st.createTopic(project.ID, "Fixture", "blue", "")
		if err != nil {
			return err
		}
		s.TopicID = topic.ID
		return st.createSession(s, dir)
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, s
}

func TestSQLiteSpawnFrozenSettingsAndReceipt(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	owned := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	a.runs[s.ID] = owned
	p := &adapter{app: a, id: s.ID, turn: owned, harness: "pi"}
	project, err := a.store.readProjectMeta(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	cacheKey, err := modelCatalogCacheKey(Session{ProjectID: s.ProjectID, Harness: "pi"}, project.Folder)
	if err != nil {
		t.Fatal(err)
	}
	a.catalogs = map[string]catalogCache{cacheKey: {catalog: &ModelCatalog{DefaultModel: "frozen-model", Models: []ModelOption{{ID: "frozen-model"}}}, expires: time.Now().Add(time.Minute)}}
	_, err = a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if _, err := st.appendEvent(s.ID, Event{ID: "human", Type: "user", Text: "Create the requested session."}); err != nil {
			return err
		}
		if _, err := st.appendEvent(s.ID, Event{ID: "agent", Type: "agent_prompt", Text: "Not human authorization."}); err != nil {
			return err
		}
		r := AutomationRun{ID: "active", SessionID: s.ID, ProjectID: s.ProjectID, Status: "running", Snapshot: Automation{Harness: "pi", Model: "frozen-model", YOLO: true}}
		return st.putRoot("automation_runs", r.ID, r)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.spawnOptions(ctx, "", "", 10); err != nil {
		t.Fatal(err)
	}
	args := spawnArgs{Title: "Spawned", Prompt: "Isolated fixture work", OperationID: "create-once", SourceUserEventID: "agent"}
	if _, err := p.spawnSession(ctx, args); err == nil {
		t.Fatal("agent-authored authorization accepted")
	}
	args.SourceUserEventID = "human"
	first, err := p.spawnSession(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	retry, err := p.spawnSession(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(retry)
	if string(left) != string(right) {
		t.Fatal("retry changed accepted receipt")
	}
	args.Prompt = "Conflicting request"
	if _, err := p.spawnSession(ctx, args); err == nil {
		t.Fatal("operation conflict accepted")
	}
	var receipt SessionSpawn
	if err := decodeSQLiteRecord(ctx, a.store.readers, "session_spawns", sqliteOwnedKey(s.ID, "create-once"), 1<<20, &receipt); err != nil {
		t.Fatal(err)
	}
	child, err := a.store.readSessionMeta(ctx, receipt.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if child.TopicID != s.TopicID || child.Model != "frozen-model" || !child.YOLO {
		t.Fatalf("child settings: %+v", child)
	}
	saved, err := a.store.readSessionMeta(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Model != "saved-model" || saved.YOLO {
		t.Fatal("automation overlay overwrote source preferences")
	}
	page, err := a.store.readHistory(ctx, child.ID, "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) == 0 || page.Events[0].Event.Type != "agent_prompt" {
		t.Fatal("spawn input lost agent provenance")
	}
	if a.storageErr != nil || len(a.state.Sessions) != 0 {
		t.Fatal("legacy fallback or storage failure", a.storageErr)
	}
}

func TestSQLiteAutomationSchedulingAndOutcome(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	ctx := a.ctx
	queued := QueuedMessage{ID: "paused", Text: "Human input", Status: "paused", Mode: "queue"}
	reuse := Automation{ID: "reuse", ProjectID: s.ProjectID, Name: "Reuse", Mode: "webhook", SessionMode: "existing", SessionID: s.ID, FollowUpPrompt: "Follow up", Harness: "pi", TopicID: s.TopicID}
	once := Automation{ID: "once", ProjectID: s.ProjectID, Name: "Once", Enabled: true, Mode: "schedule", Harness: "pi", TopicID: s.TopicID, Prompt: "Scheduled fixture", NextScheduledAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), Schedule: AutomationSchedule{Recurrence: "once", Date: "2000-01-01", Time: "12:00"}}
	_, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("automations", reuse.ID, reuse); err != nil {
			return err
		}
		if err := st.putRoot("automations", once.ID, once); err != nil {
			return err
		}
		if _, err := st.enqueueAutomation(reuse, "fixture", "webhook", now(), nil, nil); err != nil {
			return err
		}
		return st.putRecord("queued_messages", sqliteOwnedKey(s.ID, queued.ID), s.ID, queued.ID, -1, queued)
	})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.scheduleAutomationsLocked()
	a.mu.Unlock()
	a.wg.Wait()
	if a.storageErr != nil {
		t.Fatal(a.storageErr)
	}
	var completed, blocked int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_automation_runs WHERE x_automationId='once' AND status='execution_failed'`).Scan(&completed); err != nil || completed != 1 {
		t.Fatal("scheduled run did not settle", err, completed)
	}
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_automation_triggers WHERE x_automationId='reuse' AND x_consumed<>'true'`).Scan(&blocked); err != nil || blocked != 1 {
		t.Fatal("busy destination consumed a trigger", err)
	}
	var current Automation
	if err := decodeSQLiteRecord(ctx, a.store.readers, "automations", "once", 8<<20, &current); err != nil {
		t.Fatal(err)
	}
	if current.OnceTriggeredAt == "" || current.NextScheduledAt != "" {
		t.Fatal("one-time occurrence not durably consumed")
	}
	_, err = a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		return st.deleteRecord("queued_messages", sqliteOwnedKey(s.ID, queued.ID))
	})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	run, _, err := a.admitSQLAutomationLocked(reuse.ID)
	a.mu.Unlock()
	if err != nil || run == nil {
		t.Fatal("reusable admission", err)
	}
	if run.SessionID != s.ID || run.PromptKind != "follow_up" || run.Snapshot.Model != "saved-model" {
		t.Fatalf("reuse did not capture current session: %+v", run)
	}
	a.mu.Lock()
	duplicate, _, err := a.admitSQLAutomationLocked(reuse.ID)
	a.mu.Unlock()
	if err != nil || duplicate != nil {
		t.Fatal("trigger admitted twice", err)
	}
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	owned := &turn{ctx: turnCtx, cancel: cancel, done: make(chan struct{})}
	a.runs[s.ID] = owned
	p := &adapter{app: a, id: s.ID, turn: owned, harness: "pi", automationRunID: run.ID, completed: true, processesDrained: true}
	if _, err := p.callAutomationTool("automation_succeed", json.RawMessage(`{"reason":"Fixture objective met"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.callAutomationTool("automation_fail", json.RawMessage(`{"reason":"Conflicting classification"}`)); err == nil {
		t.Fatal("conflicting outcome replaced the decision")
	}
	_, err = a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return p.settleSQLAutomation(st, nil) })
	if err != nil {
		t.Fatal(err)
	}
	meta, err := a.store.readSessionMeta(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Automation.Status != "succeeded" || meta.Status != "idle" || meta.Model != s.Model {
		t.Fatalf("automation settlement: %+v", meta)
	}
	if _, err := p.callAutomationTool("automation_succeed", json.RawMessage(`{"reason":"Late classification"}`)); err == nil {
		t.Fatal("completed run accepted classification")
	}
	if len(a.state.Sessions) != 0 || a.storageErr != nil {
		t.Fatal("legacy fallback or storage failure", a.storageErr)
	}
}
