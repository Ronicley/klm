package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSQLiteAutomationManagementHTTP(t *testing.T) {
	if !developmentBuild {
		t.Skip("dev-only auth bypass")
	}
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "1")
	a, s := sqliteAutomationApp(t)
	var err error
	a.clientAuth, err = newClientAuthStorage(a.dir, a.store)
	if err != nil {
		t.Fatal(err)
	}
	project, err := a.store.readProjectMeta(a.ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	cacheKey, err := modelCatalogCacheKey(Session{ProjectID: s.ProjectID, Harness: "pi"}, project.Folder)
	if err != nil {
		t.Fatal(err)
	}
	a.catalogs = map[string]catalogCache{cacheKey: {catalog: &ModelCatalog{DefaultModel: "fixture-model", Models: []ModelOption{{ID: "fixture-model"}}}, expires: time.Now().Add(time.Minute)}}
	handler := a.routes()
	call := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-KLM-Read-Contract", sqliteReadContract)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, res.Code, res.Body.String())
		}
		var value map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	path := "/api/projects/" + s.ProjectID + "/automations"
	v := Automation{Name: "Scheduled fixture", OperationID: "create-once", Mode: "schedule", Prompt: "Fixture objective", Harness: "pi", TopicID: s.TopicID, Schedule: AutomationSchedule{Recurrence: "daily", Time: "12:00"}}
	body := map[string]any{"automation": automationSQLView(v), "newTopic": map[string]string{"name": "Created atomically", "color": "blue"}}
	created := call("POST", path, body, 200)
	id := created["id"].(string)
	rev := created["revision"].(string)
	retry := call("POST", path, body, 200)
	if retry["id"] != id {
		t.Fatal("creation retry duplicated automation")
	}
	if created["topicId"] == s.TopicID {
		t.Fatal("new topic not assigned")
	}
	if created["model"] != "fixture-model" {
		t.Fatal("model default not resolved")
	}
	listed := call("GET", path+"?limit=1", nil, 200)
	if len(listed["items"].([]any)) != 1 {
		t.Fatal("list omitted automation")
	}
	call("GET", path+"/"+id, nil, 200)
	manual := call("POST", path+"/"+id+"/run-now", map[string]string{"operationId": "manual-once", "revision": rev}, 202)
	manualRetry := call("POST", path+"/"+id+"/run-now", map[string]string{"operationId": "manual-once", "revision": "0"}, 202)
	if manualRetry["triggerId"] != manual["triggerId"] {
		t.Fatal("manual retry duplicated trigger")
	}
	updated := call("PATCH", path+"/"+id, map[string]any{"enabled": true, "revision": rev}, 200)
	call("PATCH", path+"/"+id, map[string]any{"enabled": false, "revision": rev}, 409)
	call("POST", path+"/preview", AutomationSchedule{Recurrence: "daily", Time: "12:00"}, 200)
	settings := call("GET", "/api/settings/engine", nil, 200)
	call("PATCH", "/api/settings/engine", map[string]any{"timeZone": "UTC", "revision": settings["revision"]}, 200)
	call("DELETE", path+"/"+id, map[string]any{"revision": updated["revision"]}, 200)
	call("GET", path+"/"+id+"/runs", nil, 200)
	call("GET", path+"/"+id, nil, 404)
	var pending int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_automation_triggers WHERE x_consumed<>'true'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("deletion did not cancel pending triggers", err)
	}
	if a.storageErr != nil || len(a.state.Automations) != 0 {
		t.Fatal("legacy state fallback", a.storageErr)
	}
}

func TestSQLiteAutomationRecoveryDecisions(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	ctx := a.ctx
	general := Session{ID: "general", Role: sessionRoleGeneralAgent, Harness: "pi", ExecutionCWD: t.TempDir(), Status: "idle"}
	v := Automation{ID: "recoverable", ProjectID: s.ProjectID, Revision: 1, Name: "Recovery fixture", Mode: "schedule", SessionMode: "existing", SessionID: s.ID, Harness: "pi", Model: s.Model, TopicID: s.TopicID, FollowUpPrompt: "Recover this occurrence", Schedule: AutomationSchedule{Recurrence: "daily", Time: "12:00"}}
	r := AutomationRecovery{ID: "recovery", Snapshot: v, Source: "missed_schedule", From: "2026-01-01T12:00:00Z", Until: "2026-01-03T12:00:00Z", NextOccurrenceAt: "2026-01-01T12:00:00Z", TimeZone: "UTC", Delivery: "pending"}
	_, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		if err := st.createSession(general, a.dir); err != nil {
			return err
		}
		if _, err := st.appendEvent(general.ID, Event{ID: "early", Type: "user", Text: "Earlier unrelated input"}); err != nil {
			return err
		}
		if err := st.putRoot("automations", v.ID, v); err != nil {
			return err
		}
		return st.putRoot("automation_recoveries", r.ID, r)
	})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	err = a.scheduleSQLAutomationRecoveryLocked()
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := a.store.readSessionMeta(ctx, general.ID)
	if err != nil || meta.HarnessLocked {
		t.Fatal("internal notice locked general harness", err)
	}
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	owned := &turn{ctx: turnCtx, cancel: cancel, done: make(chan struct{})}
	a.runs[general.ID] = owned
	p := &adapter{app: a, id: general.ID, role: sessionRoleGeneralAgent, harness: "pi", turn: owned}
	value, err := p.callAutomationRecovery("automation_recovery_read", json.RawMessage(`{"recoveryId":"recovery","limit":3}`))
	if err != nil {
		t.Fatal(err)
	}
	items := value.(map[string]any)["items"].([]AutomationRecoveryOccurrence)
	if len(items) != 3 {
		t.Fatal("missed occurrences not enumerated", items)
	}
	args := automationRecoveryResolveArgs{RecoveryID: r.ID, OccurrenceIDs: []string{items[0].ID, items[2].ID}, Action: "recover", OperationID: "recover-two", SourceUserEventID: "early"}
	resolve := func(args automationRecoveryResolveArgs) error {
		body, _ := json.Marshal(args)
		_, err := p.callAutomationRecovery("automation_recovery_resolve", body)
		return err
	}
	if err := resolve(args); err == nil {
		t.Fatal("decision preceding notice authorized recovery")
	}
	_, err = a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, err := st.acceptUserEvent(general.ID, "later", "human-decision", Event{ID: "later", Type: "user", Text: "Recover the first and third, discard the second."})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	args.SourceUserEventID = "later"
	if err := resolve(args); err != nil {
		t.Fatal(err)
	}
	if err := resolve(args); err != nil {
		t.Fatal("idempotent resolution failed", err)
	}
	conflict := args
	conflict.Action = "discard"
	if err := resolve(conflict); err == nil {
		t.Fatal("receipt conflict accepted")
	}
	var triggers int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_automation_triggers`).Scan(&triggers); err != nil || triggers != 2 {
		t.Fatal("recovery did not admit exactly two occurrences", err, triggers)
	}
	if err := resolve(automationRecoveryResolveArgs{RecoveryID: r.ID, OccurrenceIDs: []string{items[1].ID}, Action: "discard", OperationID: "discard-one", SourceUserEventID: "later"}); err != nil {
		t.Fatal(err)
	}
	var saved AutomationRecovery
	if err := decodeSQLiteRecord(ctx, a.store.readers, "automation_recoveries", r.ID, 32<<20, &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Resolved || saved.NextOccurrenceAt != "" || len(saved.OccurrenceDecisions) != 0 || len(saved.Resolutions) != 0 {
		t.Fatal("recovery metadata retained collections or unresolved interval", saved)
	}
	if a.storageErr != nil || len(a.state.AutomationRecoveries) != 0 {
		t.Fatal("legacy state fallback", a.storageErr)
	}
}
