package main

import (
	"encoding/json"
	"testing"
)

func TestSQLiteGraphActivityTools(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	ctx := a.ctx
	// This case exercises tool transactions; the runtime case owns dispatch.
	a.graphStarting = map[string]bool{s.ID: true}
	_, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, err := st.appendEvent(s.ID, Event{ID: "human", Type: "user", Text: "Authorize these graph activities."})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(op string, deps []string) string {
		t.Helper()
		raw, _ := json.Marshal(graphInvokeArgs{OperationID: op, GraphID: "fixture", Task: "Fixture task", Objective: "Fixture objective", Workspace: GraphWorkspaceDecision{Mode: "original"}, Authorization: []GraphAuthorization{{EventID: "human", Text: "User-requested fixture"}}, Dependencies: deps})
		value, err := a.graphConversationCall(ctx, s.ID, true, "graph_invoke", raw)
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(value.(map[string]any)["activity"].(json.RawMessage), &v); err != nil {
			t.Fatal(err)
		}
		if v.Version != "1" {
			t.Fatal("version not decimal")
		}
		return v.ID
	}
	first := invoke("first", nil)
	if invoke("first", nil) != first {
		t.Fatal("operation retry duplicated activity")
	}
	second := invoke("second", []string{first})
	update := map[string]any{"activityId": first, "operationId": "cycle", "expectedVersion": "1", "action": "change_dependencies", "userEventId": "human", "dependencies": []string{second}}
	raw, _ := json.Marshal(update)
	if _, err := a.graphConversationCall(ctx, s.ID, true, "graph_update_activity", raw); err == nil {
		t.Fatal("dependency cycle accepted")
	}
	value, err := a.graphConversationCall(ctx, s.ID, false, "graph_activities", json.RawMessage(`{"limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	page := value.(map[string]any)
	if !page["hasMore"].(bool) || len(page["items"].([]any)) != 1 {
		t.Fatal("unbounded activity page", page)
	}
	update["action"], update["operationId"] = "reprioritize", "priority"
	delete(update, "dependencies")
	update["priority"] = 7
	raw, _ = json.Marshal(update)
	if _, err := a.graphConversationCall(ctx, s.ID, false, "graph_update_activity", raw); err == nil {
		t.Fatal("side agent mutated activity")
	}
	if _, err := a.graphConversationCall(ctx, s.ID, true, "graph_update_activity", raw); err != nil {
		t.Fatal(err)
	}
	if len(a.state.GraphActivities) != 0 || a.storageErr != nil {
		t.Fatal("legacy state fallback", a.storageErr)
	}
	var count int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_graph_dependencies WHERE owner_id=?`, first).Scan(&count); err != nil || count != 0 {
		t.Fatal("cycle rollback leaked dependencies", err)
	}
}
