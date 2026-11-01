package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteMessageExecutionLifecycle(t *testing.T) {
	if !developmentBuild {
		t.Skip("dev-only explicit auth bypass")
	}
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "1")
	t.Setenv("KLM_USER_SKILLS_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	a := &app{ctx: ctx, dir: dir, store: store, sqlChanges: &sqliteChangeBus{}, runs: map[string]*turn{}, runtimes: map[string]*sessionRuntime{}, binaries: map[string]binary{"pi": {path: filepath.Join(dir, "intentionally-missing-harness.exe")}}}
	a.clientAuth, err = newClientAuthStorage(dir, store)
	if err != nil {
		t.Fatal(err)
	}
	project := Project{ID: "project", Name: "Fixture", Folder: t.TempDir()}
	session := newTopLevelSession(project.ID, "Lifecycle", "Ungrouped", "pi", "", "")
	_, err = store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("projects", project.ID, project); err != nil {
			return err
		}
		return st.createSession(session, dir)
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := a.routes()
	send := func() {
		body, _ := json.Marshal(map[string]any{"clientId": "accepted-once", "text": "Fixture input; no installed harness is invoked.", "mode": "queue"})
		request := httptest.NewRequest("POST", "http://localhost/api/sessions/"+session.ID+"/messages", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-KLM-Read-Contract", sqliteReadContract)
		reply := httptest.NewRecorder()
		handler.ServeHTTP(reply, request)
		if reply.Code != 202 {
			t.Fatalf("admission: %d %s", reply.Code, reply.Body.String())
		}
	}
	send()
	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("execution did not settle")
	}
	meta, err := store.readSessionMeta(ctx, session.ID)
	if err != nil || meta.Status != "error" {
		t.Fatalf("preparation failure was not durably finalized: %+v %v", meta, err)
	}
	page, err := store.readHistory(ctx, session.ID, "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	users, failures := 0, 0
	for _, e := range page.Events {
		if e.Event.Type == "user" {
			users++
		}
		if e.Event.Type == "error" {
			failures++
		}
	}
	if users != 1 || failures != 1 {
		t.Fatalf("accept/finalize records: user=%d failure=%d", users, failures)
	}
	var count int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_queued_messages`).Scan(&count); err != nil || count != 0 {
		t.Fatal("claimed queue remained", err)
	}
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_analytics_executions WHERE x_ended<>''`).Scan(&count); err != nil || count != 1 {
		t.Fatal("execution accounting not closed", err)
	}
	send()
	a.wg.Wait()
	page, err = store.readHistory(ctx, session.ID, "", "", 50)
	if err != nil || len(page.Events) != 2 {
		t.Fatal("retry duplicated accepted work", err)
	}
	if a.storageErr != nil || len(a.state.Sessions) != 0 || a.streamIndexes != nil {
		t.Fatal("execution fell back to legacy persistence", a.storageErr)
	}
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteOwnedRequestsAndGrantRollback(t *testing.T) {
	ctx := context.Background()
	store, _ := sqliteSessionStore(t, 0)
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	running := &turn{ctx: tctx, cancel: cancel, done: make(chan struct{})}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		s, err := st.sessionMeta("a")
		if err != nil {
			return err
		}
		s.Harness = "codex"
		return st.putSessionMeta(s)
	}); err != nil {
		t.Fatal(err)
	}
	a := &app{ctx: ctx, store: store, runs: map[string]*turn{"a": running}}
	p := &adapter{app: a, id: "a", turn: running, harness: "codex", cwd: t.TempDir()}
	permission := Permission{SourceID: "native-permission", Kind: "unknown-native-scope", Title: "Fixture permission", Decisions: []string{"once", "session", "always", "reject"}}
	if err := p.requestPermission(permission, map[string]any{"resource": "fixture"}, func(bool) error { return errors.New("simulated native delivery failure") }); err != nil {
		t.Fatal(err)
	}
	var requestID string
	for id := range running.approvals {
		requestID = id
	}
	if requestID == "" {
		t.Fatal("pending permission missing")
	}
	if err := a.replyConversationPermission("a", requestID, "always", nil, nil); err == nil {
		t.Fatal("unconfirmed native reply accepted")
	}
	var count int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_permission_grants`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed native reply left a grant", err)
	}
	if err := store.readers.QueryRow(`SELECT count(*) FROM permission_reply_intents`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed reply left intent", err)
	}
	running.approvals[requestID].reply = func(bool) error { return nil }
	if err := a.replyConversationPermission("a", requestID, "session", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_permission_grants WHERE session_id='a'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("confirmed scope was not persisted", err)
	}
	answered := false
	if err := p.requestQuestion(QuestionRequest{SourceID: "native-question", Items: []QuestionItem{{ID: "item", Text: "Choose", Options: []QuestionOption{{Label: "Yes"}}}}}, func(values [][]string, cancelled bool) error {
		answered = !cancelled && values[0][0] == "Yes"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var questionID string
	for id := range running.questions {
		questionID = id
	}
	if err := a.replyConversationQuestion("a", questionID, [][]string{{"invalid"}}, false, nil); err == nil {
		t.Fatal("unoffered choice accepted")
	}
	if err := a.replyConversationQuestion("a", questionID, [][]string{{"Yes"}}, false, nil); err != nil || !answered {
		t.Fatal("question was not delivered", err)
	}
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_pending_questions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("answered question remained pending", err)
	}
}

func TestSQLiteSteeringInterruptAndStop(t *testing.T) {
	ctx := context.Background()
	store, dir := sqliteSessionStore(t, 0)
	cwd := t.TempDir()
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	running := &turn{ctx: turnCtx, cancel: cancel, done: make(chan struct{})}
	a := &app{ctx: ctx, store: store, dir: filepath.Dir(dir), runs: map[string]*turn{"a": running}, binaries: map[string]binary{"pi": {path: filepath.Join(cwd, "missing.exe")}}}
	p := &adapter{app: a, id: "a", harness: "pi", turn: running, processesDrained: true}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		project, err := st.project("p")
		if err != nil {
			return err
		}
		project.Folder = cwd
		if err := st.putRoot("projects", "p", project); err != nil {
			return err
		}
		s, err := st.sessionMeta("a")
		if err != nil {
			return err
		}
		s.Harness, s.Status = "pi", "running"
		return st.putSessionMeta(s)
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://localhost/api/sessions/a/messages", nil)
	for _, id := range []string{"steer-one", "steer-two"} {
		if err := a.admitSQLMessage(request, "a", id, "Input "+id, "steer", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	first, payload, err := p.takeSteering()
	if err != nil || first == nil {
		t.Fatal("native boundary did not claim input", err)
	}
	if err := p.acknowledgeSteering(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.finishPiSteering(payload.piText()); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_queued_messages WHERE owner_id='a'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("native incorporation left queue entries", err)
	}
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_session_events WHERE owner_id='a' AND record_id IN ('steer-one','steer-two') AND kind='user'`).Scan(&pending); err != nil || pending != 2 {
		t.Fatal("steering boundaries lost user attribution", err)
	}
	if err := a.admitSQLMessage(request, "a", "replacement", "Replace this turn", "queue", nil, nil); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	id, err := a.changeQueuedMessageLocked("a", "replacement", false, true, nil)
	a.mu.Unlock()
	if err != nil || id != "replacement" || running.restartMessageID != id || running.ctx.Err() == nil {
		t.Fatal("Send now did not cancel and reserve replacement", err)
	}
	a.mu.Lock()
	err = p.finishTurnStateLocked(context.Canceled)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var status string
	if err := store.readers.QueryRow(`SELECT status FROM data_queued_messages WHERE record_id='replacement'`).Scan(&status); err != nil || status != "queued" {
		t.Fatal("confirmed shutdown did not release selected replacement", err)
	}
	a.mu.Lock()
	_, err = a.stopSessionLocked("a", nil)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if running.restartMessageID != "" {
		t.Fatal("explicit Stop retained replacement intent")
	}
	if err := store.readers.QueryRow(`SELECT status FROM data_queued_messages WHERE record_id='replacement'`).Scan(&status); err != nil || status != "paused" {
		t.Fatal("Stop did not pause replacement", err)
	}
	if a.storageErr != nil || len(a.state.Sessions) != 0 {
		t.Fatal("control path used legacy state", a.storageErr)
	}
}

func TestSQLiteGeneralRegistrationAndNativeChild(t *testing.T) {
	ctx := context.Background()
	store, dir := sqliteSessionStore(t, 0)
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	running := &turn{ctx: turnCtx, cancel: cancel, done: make(chan struct{})}
	a := &app{ctx: ctx, dir: filepath.Dir(dir), store: store, runs: map[string]*turn{"general": running}, binaries: map[string]binary{"pi": {path: filepath.Join(filepath.Dir(dir), "missing.exe")}}}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		s, err := st.sessionMeta("general")
		if err != nil {
			return err
		}
		s.Harness = "pi"
		return st.putSessionMeta(s)
	}); err != nil {
		t.Fatal(err)
	}
	p := &adapter{app: a, id: "general", role: sessionRoleGeneralAgent, harness: "pi", turn: running, keys: map[string]string{}}
	args, _ := json.Marshal(map[string]any{"name": "Registered", "path": t.TempDir()})
	result, err := p.callGeneralTool(ctx, "project_add", args)
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["projectId"].(string)
	result, err = p.callGeneralTool(ctx, "project_add", args)
	if err != nil || result.(map[string]any)["outcome"] != "existing" || result.(map[string]any)["projectId"] != id {
		t.Fatal("general registration lost idempotent identity", err)
	}
	result, err = p.callGeneralTool(ctx, "project_list", json.RawMessage(`{"query":"Registered"}`))
	if err != nil || len(result.(map[string]any)["items"].([]any)) != 1 {
		t.Fatal("SQL project discovery failed", err)
	}
	child, err := p.ensureSubagent("native-child", "Child", "fixture-model", "")
	if err != nil || child == nil {
		t.Fatal("engine-owned native child creation failed", err)
	}
	defer p.closeSubagents(false, false)
	if err := p.put("invocation", "subagent", "Child", "", "running", false, map[string]any{"childSessionId": child.id}); err != nil {
		t.Fatal(err)
	}
	if err := child.put("output", "assistant", "", "Child output", "completed", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.finishSubagent(child, "completed"); err != nil {
		t.Fatal(err)
	}
	page, err := store.readHistory(ctx, "general", "", "", 50)
	if err != nil || len(page.Events) != 1 || page.Events[0].Event.Status != "completed" {
		t.Fatal("native child parent projection did not settle", err)
	}
	meta, err := store.readSessionMeta(ctx, child.id)
	if err != nil || meta.ProjectID != "" || meta.ParentID != "general" || meta.Role != sessionRoleSubagent {
		t.Fatal("native child acquired a user project or root role", err)
	}
	if a.storageErr != nil || len(a.state.Projects) != 0 || len(a.state.Sessions) != 0 {
		t.Fatal("private tool used legacy state", a.storageErr)
	}
}
