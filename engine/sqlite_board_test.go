package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSQLiteBoardHTTPAndRecovery(t *testing.T) {
	if !developmentBuild {
		t.Skip("dev auth bypass fixture")
	}
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "1")
	a, s := sqliteAutomationApp(t)
	var err error
	a.clientAuth, err = newClientAuthStorage(a.dir, a.store)
	if err != nil {
		t.Fatal(err)
	}
	handler := a.routes()
	call := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		encoded, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(encoded))
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
	config := call("GET", "/api/activity-board/config", nil, 200)
	updated := call("PATCH", "/api/activity-board/config", map[string]any{"expectedRevision": config["revision"], "name": "Todo", "mappings": map[string]any{}}, 200)
	phase := updated["phases"].([]any)[0].(map[string]any)["id"].(string)
	c := activityBoardConnection{ID: "connection", Name: "Fixture", ProjectID: s.ProjectID, Provider: "github", Scope: activityBoardScope{Repository: "fixture/repo", RepositoryID: "42"}, Mappings: map[string][]activityBoardValue{phase: {{ID: "label", Name: "todo"}}}}
	item := activityBoardItem{Key: strings.Repeat("a", 64), ConnectionID: c.ID, ExternalID: "1", Title: "Fixture task", Version: "observed", Values: []activityBoardValue{{ID: "label", Name: "todo"}}}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("board_connections", c.ID, c); err != nil {
			return err
		}
		if err := st.putRoot("board_credentials", c.ID, []byte("invalid-protected-fixture")); err != nil {
			return err
		}
		if err := st.putRoot("board_sync", c.ID, activityBoardSync{}); err != nil {
			return err
		}
		return st.putBoardItem(item, c)
	})
	if err != nil {
		t.Fatal(err)
	}
	page := call("GET", "/api/activity-board/items?phaseId="+phase, nil, 200)
	if page["total"].(float64) != 1 || len(page["items"].([]any)) != 1 {
		t.Fatal("indexed phase projection missing", page)
	}
	receipt := call("POST", "/api/activity-board/items/"+item.Key+"/phase/cancel", map[string]any{"operationId": "cancelled-operation-once", "phaseId": phase, "expectedConfigRevision": updated["revision"], "observedVersion": item.Version}, 200)
	if receipt["status"] != "failed" {
		t.Fatal("pre-dispatch cancellation was not recorded")
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		return st.putRoot("board_moves", "interrupted-operation", activityBoardMove{OperationID: "interrupted-operation", ConnectionID: c.ID, Key: item.Key, PhaseID: phase, Status: "pending", CreatedAt: now(), UpdatedAt: now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.startActivityBoard(); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait() // Invalid protected fixture fails before any provider HTTP.
	moved := call("GET", "/api/activity-board/moves/interrupted-operation", nil, 200)
	if moved["status"] != "unconfirmed" {
		t.Fatal("startup replayed or forgot an uncertain provider write")
	}
	call("PATCH", "/api/activity-board/config", map[string]any{"expectedRevision": updated["revision"], "phaseId": phase, "name": "Renamed"}, 409)
	if a.storageErr != nil || len(a.state.ActivityBoardItems) != 0 {
		t.Fatal("legacy board fallback", a.storageErr)
	}
}
