package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSQLiteMultiplexScopedUpdates(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	a.sqlChanges = &sqliteChangeBus{}
	call := sqlPagingHTTP(t, a)
	otherProject := call("POST", "/api/projects", map[string]any{"name": "Cold", "folder": t.TempDir()}, 201)["id"].(string)
	other := call("POST", "/api/sessions", map[string]any{"projectId": otherProject, "title": "Cold", "harness": "pi"}, 201)["session"].(map[string]any)["id"].(string)
	server := httptest.NewServer(a.routes())
	defer server.Close()
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	params := url.Values{"contract": {"3"}, "watch": {"global/", "project/" + s.ProjectID, "conversation/" + s.ID, "conversation/" + other}}
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/updates?"+params.Encode(), nil)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("multiplex stream", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	read := func() sqliteChangeNotice {
		t.Helper()
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		var notice sqliteChangeNotice
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &notice); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		return notice
	}
	if read().Kind != "ready" {
		t.Fatal("missing ready")
	}
	call("PATCH", "/api/sessions/"+s.ID, map[string]any{"title": "Updated"}, 200)
	notice := read()
	if notice.Kind != "invalidate" || strings.Join(notice.Scopes, ",") != "global/,project/"+s.ProjectID+",conversation/"+s.ID {
		t.Fatalf("scope routing leaked or omitted a scope: %+v", notice)
	}
	if len(notice.SessionIDs) != 0 || len(notice.Ordinals) != 0 {
		t.Fatal("multiplex notice contained history metadata")
	}
	cancel()
	response.Body.Close()
}

func TestSQLiteHTTPProjectTopicSessionVertical(t *testing.T) {
	if !developmentBuild {
		t.Skip("explicit dev auth-bypass fixture")
	}
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "1")
	ctx := context.Background()
	dir := t.TempDir()
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	a := &app{dir: dir, store: store, ctx: ctx, sqlChanges: &sqliteChangeBus{}, runs: map[string]*turn{}, binaries: map[string]binary{"pi": {path: "never-executed-fixture"}}, harnesses: []Harness{{ID: "pi", Name: "Pi", Available: true}}}
	a.clientAuth, err = newClientAuthStorage(dir, store)
	if err != nil {
		t.Fatal(err)
	}
	handler := a.routes()
	call := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		encoded, _ := json.Marshal(body)
		request := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(encoded))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-KLM-Read-Contract", sqliteReadContract)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	old := httptest.NewRecorder()
	handler.ServeHTTP(old, httptest.NewRequest("GET", "http://localhost/api/state", nil))
	if old.Code != 426 {
		t.Fatal("old inventory contract accepted")
	}
	p1 := call("POST", "/api/projects", map[string]any{"name": "Alpha", "folder": t.TempDir()}, 201)
	p2 := call("POST", "/api/projects", map[string]any{"name": "Beta", "folder": t.TempDir()}, 201)
	id1, id2 := p1["id"].(string), p2["id"].(string)
	server := httptest.NewServer(handler)
	defer server.Close()
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	streamRequest, _ := http.NewRequestWithContext(streamCtx, "GET", server.URL+"/api/updates?contract=3&scope=project&id="+id1, nil)
	streamResponse, err := server.Client().Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer streamResponse.Body.Close()
	if streamResponse.StatusCode != 200 {
		t.Fatal("scoped SSE failed", streamResponse.StatusCode)
	}
	reader := bufio.NewReader(streamResponse.Body)
	readNotice := func() sqliteChangeNotice {
		t.Helper()
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		var notice sqliteChangeNotice
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &notice); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		return notice
	}
	if notice := readNotice(); notice.Kind != "ready" || len(notice.SessionIDs) != 0 {
		t.Fatal("SSE initial response enumerated inventory")
	}
	call("PATCH", "/api/projects/"+id1, map[string]any{"name": "Updated Alpha"}, 200)
	if notice := readNotice(); notice.Kind != "invalidate" || notice.Revision == "" || len(notice.SessionIDs) != 0 {
		t.Fatal("project SSE did not publish committed scoped invalidation")
	}
	stopStream()
	streamResponse.Body.Close()
	topic := call("POST", "/api/projects/"+id1+"/topics", map[string]any{"name": "Work", "color": "blue"}, 201)["topic"].(map[string]any)["id"].(string)
	session := call("POST", "/api/sessions", map[string]any{"projectId": id1, "topicId": topic, "title": "One", "harness": "pi"}, 201)["session"].(map[string]any)["id"].(string)
	call("POST", "/api/sessions", map[string]any{"projectId": id1, "title": "Second", "harness": "pi"}, 201)
	other := call("POST", "/api/sessions", map[string]any{"projectId": id2, "title": "Unopened other project", "harness": "pi"}, 201)["session"].(map[string]any)["id"].(string)
	_, err = store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		for i := 0; i < 125; i++ {
			if _, err := st.appendEvent(session, Event{ID: fmt.Sprintf("message-%d", i), Type: "assistant", Text: "Historical text must not enter bootstrap", CreatedAt: now()}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	boot := call("GET", "/api/state", nil, 200)
	bootBytes, _ := json.Marshal(boot)
	if _, ok := boot["sessions"]; ok || strings.Contains(string(bootBytes), session) || strings.Contains(string(bootBytes), "Historical text") {
		t.Fatal("bootstrap hydrated session inventory/history")
	}
	page := call("GET", "/api/projects/"+id1+"/sessions?limit=1", nil, 200)
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), other) || len(page["items"].([]any)) != 1 {
		t.Fatal("project list crossed scope")
	}
	if page["hasMore"] != true {
		t.Fatal("project summary pagination missing")
	}
	call("GET", "/api/projects/"+id1+"/sessions?limit=1&cursor="+page["nextCursor"].(string), nil, 200)
	ungrouped := call("GET", "/api/projects/"+id1+"/sessions?topicId=", nil, 200)
	if len(ungrouped["items"].([]any)) != 1 {
		t.Fatal("ungrouped scope included assigned topics")
	}
	history := call("GET", "/api/sessions/"+session+"/history", nil, 200)
	if len(history["events"].([]any)) != 50 || history["count"] != "125" {
		t.Fatal("history is not bounded")
	}
	earlier := call("GET", "/api/sessions/"+session+"/history?before="+history["before"].(string), nil, 200)
	if len(earlier["events"].([]any)) != 50 {
		t.Fatal("older history page failed")
	}
	call("PATCH", "/api/sessions/"+session, map[string]any{"title": "Renamed", "archived": true}, 200)
	call("GET", "/api/projects/"+id1+"/sessions?limit=1&cursor="+page["nextCursor"].(string), nil, 409)
	call("PATCH", "/api/projects/"+id1+"/topics/"+topic, map[string]any{"archived": true}, 200)
	call("PATCH", "/api/sessions/"+session, map[string]any{"archived": false}, 409)
	call("PATCH", "/api/projects/"+id1+"/topics/"+topic, map[string]any{"archived": false}, 200)
	call("PATCH", "/api/sessions/"+session, map[string]any{"archived": false}, 200)
	call("PATCH", "/api/projects/"+id2, map[string]any{"position": map[string]any{"beforeId": id1}}, 200)
	projects := call("GET", "/api/state", nil, 200)["projects"].([]any)
	if projects[0].(map[string]any)["id"] != id2 {
		t.Fatal("project ordering was not persisted")
	}
	general := call("POST", "/api/general-session", map[string]any{}, 200)["session"].(map[string]any)["id"].(string)
	call("PATCH", "/api/sessions/"+general+"/harness", map[string]any{"harness": "pi"}, 200)
	side := call("POST", "/api/sessions/"+session+"/side", map[string]any{}, 201)["session"].(map[string]any)["id"].(string)
	if again := call("POST", "/api/sessions/"+session+"/side", map[string]any{}, 200)["session"].(map[string]any)["id"]; again != side {
		t.Fatal("side identity changed")
	}
	if again := call("POST", "/api/general-session", map[string]any{}, 200)["session"].(map[string]any)["id"]; again != general {
		t.Fatal("general singleton changed")
	}
	call("DELETE", "/api/projects/"+id1, map[string]any{}, 200)
	call("GET", "/api/projects/"+id1+"/sessions", nil, 404)
	call("POST", "/api/projects", map[string]any{"name": "Restored Alpha", "folder": p1["folder"]}, 201)
	detail := call("GET", "/api/sessions/"+session, nil, 200)
	if detail["session"].(map[string]any)["title"] != "Renamed" || detail["eventCount"] != "125" {
		t.Fatal("restoration lost identity/history")
	}
	call("GET", "/api/analytics", nil, 503) // No silent read from an empty legacy diskState.
	if len(a.state.Sessions) != 0 || len(a.state.Projects) != 0 {
		t.Fatal("SQL HTTP handlers hydrated legacy state")
	}
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
}
