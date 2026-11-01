package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func sqlPagingHTTP(t *testing.T, a *app) func(string, string, any, int) map[string]any {
	t.Helper()
	if !developmentBuild {
		t.Skip("dev auth fixture")
	}
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "1")
	var err error
	a.clientAuth, err = newClientAuthStorage(a.dir, a.store)
	if err != nil {
		t.Fatal(err)
	}
	handler := a.routes()
	return func(method, path string, body any, want int) map[string]any {
		t.Helper()
		encoded, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(encoded))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-KLM-Read-Contract", "3")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var value map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
}

func TestSQLiteTopicAndLibraryPaging(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	call := sqlPagingHTTP(t, a)
	var far Topic
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		for i := 0; i < 215; i++ {
			name := fmt.Sprintf("Topic %03d", i)
			if i == 0 {
				name = "Far away Καλημέρα"
			}
			topic, err := st.createTopic(s.ProjectID, name, "blue", "")
			if err != nil {
				return err
			}
			if i == 0 {
				far = topic
			}
		}
		meta, err := st.sessionMeta(s.ID)
		if err != nil {
			return err
		}
		meta.TopicID = far.ID
		return st.putSessionMeta(meta)
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/projects/" + s.ProjectID + "/topics"
	first := call("GET", base+"?limit=200", nil, 200)
	if !first["hasMore"].(bool) {
		t.Fatal("large topic list was not paged")
	}
	for _, item := range first["items"].([]any) {
		if item.(map[string]any)["id"] == far.ID {
			t.Fatal("fixture topic unexpectedly in first page")
		}
	}
	exact := call("GET", "/api/sessions/"+s.ID, nil, 200)["session"].(map[string]any)["topic"].(map[string]any)
	if exact["id"] != far.ID || exact["name"] != far.Name {
		t.Fatal("exact session lost off-page topic", exact)
	}
	search := call("GET", base+"?active=true&search="+url.QueryEscape("ΚΑΛΗΜΈΡΑ"), nil, 200)
	if len(search["items"].([]any)) != 1 {
		t.Fatal("Unicode scope-wide topic search failed", search)
	}
	sessionSearch := call("GET", "/api/projects/"+s.ProjectID+"/sessions?search="+url.QueryEscape(" ΚΑΛΗΜΈΡΑ "), nil, 200)
	if len(sessionSearch["items"].([]any)) != 1 {
		t.Fatal("topic-name session search disagrees with picker search", sessionSearch)
	}
	call("GET", base+"?search=changed&cursor="+url.QueryEscape(first["nextCursor"].(string)), nil, 400)
	call("PATCH", base+"/"+far.ID, map[string]bool{"archived": true}, 200)
	exact = call("GET", "/api/sessions/"+s.ID, nil, 200)["session"].(map[string]any)["topic"].(map[string]any)
	if exact["archived"] != true {
		t.Fatal("off-page topic archive not reflected")
	}
	for i := 0; i < 55; i++ {
		id := fmt.Sprintf("paging-skill-%03d", i)
		dir := filepath.Join(os.Getenv("KLM_USER_SKILLS_DIR"), id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		description := "Ordinary fixture"
		if i == 54 {
			description = "Needle beyond the first skill page"
		}
		body := "---\nname: " + id + "\ndescription: " + description + "\n---\nFixture content.\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	connection := MCPConnection{ID: newID(), Name: "Unrelated connection name", Revision: newID(), Config: MCPConnectionConfig{Transport: "http", URL: "http://127.0.0.1:9", Auth: "none"}, Catalog: &MCPCatalog{Revision: "fixture", Tools: []*mcp.Tool{{Name: "target_tool", Description: "Needle tool description"}}}}
	if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error { return st.putRoot("mcp_connections", connection.ID, connection) }); err != nil {
		t.Fatal(err)
	}
	library := call("GET", "/api/library", nil, 200)
	if len(library["skills"].([]any)) != 50 || library["skillsHasMore"] != true {
		t.Fatal("skill page unbounded", library)
	}
	found := call("GET", "/api/library?search=needle", nil, 200)
	if len(found["skills"].([]any)) != 1 || len(found["connections"].([]any)) != 1 {
		t.Fatal("Library search was limited to loaded names/page", found)
	}
}

func TestSQLiteLargeRequestAndGraphPages(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	call := sqlPagingHTTP(t, a)
	snapshot := sqliteSemanticFixture(t).GraphRuns[0].Snapshot
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		side := newTopLevelSession(s.ProjectID, "Fixture side", "Ungrouped", "pi", "", "")
		side.ID = "paging-side"
		side.Role, side.ParentID = sessionRoleSideAgent, s.ID
		if err := st.createSession(side, a.dir); err != nil {
			return err
		}
		c := Consultation{ID: "paging-consultation", From: s.ID, To: side.ID, Topic: "Fixture", Question: "Fixture question", Status: "queued", Delivery: "waiting", CreatedAt: now(), UpdatedAt: now()}
		if err := st.putRoot("consultations", c.ID, c); err != nil {
			return err
		}
		for i := 0; i < 205; i++ {
			p := Permission{ID: fmt.Sprintf("normal-%03d", i), Title: "Fixture request", Patterns: []string{}, Decisions: []string{"once", "reject"}}
			if err := st.putRecord("pending_permissions", sqliteOwnedKey(s.ID, p.ID), s.ID, p.ID, -1, p); err != nil {
				return err
			}
		}
		for i := 0; i < 35; i++ {
			q := QueuedMessage{ID: fmt.Sprintf("queued-%03d", i), Text: "Fixture recovery input", Mode: "queue", Status: "paused"}
			if err := st.putRecord("queued_messages", sqliteOwnedKey(s.ID, q.ID), s.ID, q.ID, -1, q); err != nil {
				return err
			}
			if err := st.putRoot("queue_payloads", q.ID, queuedPayload{Submission: submission{Text: q.Text}, Harness: "pi"}); err != nil {
				return err
			}
		}
		activity := GraphActivity{ID: "paging-activity", ProjectID: s.ProjectID, ConversationID: s.ID, GraphID: snapshot.GraphID, Status: "running", Version: 1, Workspace: GraphWorkspaceDecision{Mode: "original"}}
		run := GraphRun{ID: "paging-run", ActivityID: activity.ID, ProjectID: s.ProjectID, ConversationID: s.ID, GraphID: snapshot.GraphID, Status: "running", Revision: 1, InitialWorkspaceID: "paging-workspace", Workspace: activity.Workspace, CreatedAt: now()}
		if err := st.putRoot("graph_activities", activity.ID, activity); err != nil {
			return err
		}
		if err := st.putRoot("graph_runs", run.ID, run); err != nil {
			return err
		}
		if err := st.putSingleChild("graph_snapshots", run.ID, snapshot); err != nil {
			return err
		}
		node := Session{ID: "paging-node", ProjectID: s.ProjectID, Role: "graph_node", GraphRunID: run.ID, GraphNodeID: "agent", Status: "running", Harness: "pi"}
		if err := st.createSession(node, a.dir); err != nil {
			return err
		}
		if err := st.putRoot("graph_activations", "paging-activation", GraphActivation{ID: "paging-activation", RunID: run.ID, NodeID: "agent", SessionID: node.ID, Status: "running", Occurrence: 1}); err != nil {
			return err
		}
		for i := 0; i < 205; i++ {
			p := Permission{ID: fmt.Sprintf("graph-%03d", i), Title: "Graph fixture request", Patterns: []string{}, Decisions: []string{"once", "reject"}}
			if err := st.putRecord("pending_permissions", sqliteOwnedKey(node.ID, p.ID), node.ID, p.ID, -1, p); err != nil {
				return err
			}
		}
		for i := 0; i < 125; i++ {
			e := event("assistant", fmt.Sprintf("Node row %03d", i))
			if _, err := st.appendEvent(node.ID, e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	call("POST", "/api/sessions/"+s.ID+"/consultations/paging-consultation/cancel", map[string]any{}, 200)
	var cancelled Consultation
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "consultations", "paging-consultation", 1<<20, &cancelled); err != nil || cancelled.Status != "cancelled" || cancelled.Delivery != "cancelled" {
		t.Fatal("exact consultation cancellation fell through to legacy state", err)
	}
	detail := call("GET", "/api/sessions/"+s.ID, nil, 200)
	if detail["more"].(map[string]any)["permissions"] != true || len(detail["session"].(map[string]any)["permissions"].([]any)) != 100 {
		t.Fatal("normal requests were not bounded")
	}
	page := call("GET", "/api/sessions/"+s.ID+"/pending?kind=permissions&afterId=normal-099", nil, 200)
	if len(page["items"].([]any)) != 100 || page["hasMore"] != true {
		t.Fatal("normal request continuation", page)
	}
	queue := call("GET", "/api/sessions/"+s.ID+"/pending?kind=queue&afterId=queued-031", nil, 200)
	if len(queue["items"].([]any)) != 3 {
		t.Fatal("legacy queue continuation", queue)
	}
	call("DELETE", "/api/sessions/"+s.ID+"/queue/queued-034", nil, 200)
	graph := call("GET", "/api/sessions/"+s.ID+"/graph?offset=200", nil, 200)
	requests := graph["requests"].([]any)
	if len(requests) != 5 || requests[0].(map[string]any)["sessionId"] != "paging-node" {
		t.Fatal("graph request ownership/page changed", graph)
	}
	activity := call("GET", "/api/sessions/"+s.ID+"/graph/runs/paging-run/nodes/agent/activity", nil, 200)["activation"].(map[string]any)
	if len(activity["events"].([]any)) != 50 || activity["hasEarlier"] != true || activity["hasMore"] != false {
		t.Fatal("node activity was not the latest bounded window", activity)
	}
}
