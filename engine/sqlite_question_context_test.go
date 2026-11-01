package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func sqliteQuestionNode(t *testing.T) (*adapter, Session) {
	t.Helper()
	a, owner, run := sqliteRunningGraph(t, "running")
	node := Session{ID: "question-node", ProjectID: owner.ProjectID, Role: "graph_node", GraphRunID: run.ID, GraphNodeID: "agent", Harness: "codex", Status: "running"}
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.createSession(node, a.dir); err != nil {
			return err
		}
		if _, err := st.appendEvent(node.ID, event("assistant", "Previous activation must not leak.")); err != nil {
			return err
		}
		if _, err := st.appendEvent(owner.ID, event("assistant", "Owner history must not leak.")); err != nil {
			return err
		}
		x := GraphActivation{ID: "question-activation", RunID: run.ID, NodeID: "agent", SessionID: node.ID, Status: "running", NativeEventStart: 1}
		return st.putRoot("graph_activations", x.ID, x)
	})
	if err != nil {
		t.Fatal(err)
	}
	owned := &turn{ctx: a.ctx}
	a.runs[node.ID] = owned
	return &adapter{app: a, id: node.ID, harness: node.Harness, turn: owned, graph: &graphAdapterBinding{hooks: GraphAdapterHooks{Node: true}}}, owner
}

func TestSQLiteGraphQuestionContext(t *testing.T) {
	p, owner := sqliteQuestionNode(t)
	a := p.app
	appendText := func(text string) {
		t.Helper()
		if _, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
			_, err := st.appendEvent(p.id, event("assistant", text))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendText("Plan é🐱")
	appendText("Approval rationale")
	want := "Plan é🐱\n\n---\n\nApproval rationale"
	req := QuestionRequest{SourceID: "private-native-id", Items: []QuestionItem{{ID: "approval", Header: "Approve", Text: "Proceed?", Description: "Full description", Options: []QuestionOption{{Label: "Yes", Description: "Continue"}}, Secret: true}}}
	deliveryFails := true
	delivered := false
	if err := p.requestQuestion(req, func(answers [][]string, cancelled bool) error {
		if deliveryFails {
			return errors.New("fixture native delivery failure")
		}
		delivered = !cancelled && answers[0][0] == "Yes"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var id string
	for key, pending := range p.turn.questions {
		id = key
		if pending.request.Context != want || pending.request.SourceID != req.SourceID {
			t.Fatal("snapshot or private native source lost", pending.request)
		}
	}
	appendText("Later text must not alter the snapshot.")
	if err := p.requestQuestion(req, func([][]string, bool) error { t.Fatal("duplicate replaced callback"); return nil }); err != nil || len(p.turn.questions) != 1 {
		t.Fatal("native duplicate was not deduplicated", err)
	}
	call := sqlPagingHTTP(t, a)
	check := func(value map[string]any) {
		t.Helper()
		if value["context"] != want || value["sourceId"] != nil || value["SourceID"] != nil || value["items"].([]any)[0].(map[string]any)["description"] != "Full description" {
			t.Fatal("public request lost context/payload or exposed native identity", value)
		}
	}
	graph := call("GET", "/api/sessions/"+owner.ID+"/graph", nil, 200)
	projection := graph["requests"].([]any)[0].(map[string]any)
	if projection["sessionId"] != p.id || projection["activationId"] != "question-activation" {
		t.Fatal("question projected onto wrong native owner", projection)
	}
	check(projection["question"].(map[string]any))
	call("GET", "/api/sessions/"+p.id, nil, 404)
	call("GET", "/api/sessions/"+p.id+"/pending?kind=questions", nil, 404)
	call("GET", "/api/sessions/"+p.id+"/history", nil, 404)
	// Owning-chat reads retain their own questions, never expose a private node
	// as a public conversation. Graph requests use the scoped projection above.
	parentTurn := &turn{ctx: a.ctx}
	a.runs[owner.ID] = parentTurn
	parent := &adapter{app: a, id: owner.ID, harness: "codex", turn: parentTurn}
	req.Context = want
	if err := parent.requestQuestion(req, func([][]string, bool) error { return nil }); err != nil {
		t.Fatal(err)
	}
	check(call("GET", "/api/sessions/"+owner.ID, nil, 200)["session"].(map[string]any)["questions"].([]any)[0].(map[string]any))
	check(call("GET", "/api/sessions/"+owner.ID+"/pending?kind=questions", nil, 200)["items"].([]any)[0].(map[string]any))
	if err := a.replyConversationQuestion(owner.ID, id, [][]string{{"Yes"}}, false, &SpawnOrigin{SessionID: "general"}); err == nil {
		t.Fatal("unconfirmed native delivery accepted")
	}
	var saved QuestionRequest
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "pending_questions", sqliteOwnedKey(p.id, id), 2<<20, &saved); err != nil || saved.Context != want || saved.Resolving {
		t.Fatal("delivery failure dropped context or left resolving state", err, saved)
	}
	deliveryFails = false
	if err := a.replyConversationQuestion(owner.ID, id, [][]string{{"Yes"}}, false, &SpawnOrigin{SessionID: "general"}); err != nil || !delivered {
		t.Fatal("owning-chat reply did not reach original native callback", err)
	}
	page, err := a.store.readHistory(a.ctx, p.id, "", "", 50)
	if err != nil || page.Events[len(page.Events)-1].Event.Text != "Proceed?\n\n[Private answer sent]" || page.Events[len(page.Events)-1].Event.Type != "agent_prompt" {
		t.Fatal("private answer/history attribution changed", err, page)
	}
	if len(a.state.Sessions) != 0 || a.storageErr != nil {
		t.Fatal("question context used legacy state", a.storageErr)
	}
}

func TestSQLiteGraphQuestionContextBounds(t *testing.T) {
	p, _ := sqliteQuestionNode(t)
	a := p.app
	// Published generations and legacy chunks must use the same field reader,
	// even when unrelated native data exceeds the normal event decode budget.
	generation, err := a.store.stageEventGeneration(a.ctx, p.id, "published-plan", strings.NewReader(`{"id":"published-plan","type":"assistant","data":{"large":"`+strings.Repeat("x", 2<<20)+`"},"text":"Published plan"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if _, err := st.graphQuestionContext("unrelated-owner"); !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("unrelated owner exposed node context: %v", err)
		}
		if _, err := st.publishEventGeneration(p.id, Event{ID: "published-plan"}, generation); err != nil {
			return err
		}
		for i := 0; i < 70; i++ {
			if _, err := st.appendEvent(p.id, event("assistant", " \n\t")); err != nil {
				return err
			}
		}
		if _, err := st.appendEvent(p.id, event("reasoning", "Private reasoning must not leak.")); err != nil {
			return err
		}
		if _, err := st.appendEvent(p.id, event("assistant", strings.Repeat("\u2003", 45000))); err != nil {
			return err
		}
		got, err := st.graphQuestionContext(p.id)
		if err != nil || got != "Published plan" {
			return fmt.Errorf("bounded published/empty history: %q %v", got, err)
		}
		if _, err := st.appendEvent(p.id, event("assistant", strings.Repeat("é", 65537))); err != nil {
			return err
		}
		if _, err := st.appendEvent(p.id, event("assistant", "Newest")); err != nil {
			return err
		}
		got, err = st.graphQuestionContext(p.id)
		if err != nil || got != "Newest" {
			return fmt.Errorf("oversized message was truncated or crossed: %q %v", got, err)
		}
		exact := strings.Repeat("é", 65536)
		if _, err := st.appendEvent(p.id, event("assistant", exact)); err != nil {
			return err
		}
		got, err = st.graphQuestionContext(p.id)
		if err != nil || got != exact {
			return fmt.Errorf("exact byte budget changed: %d %v", len(got), err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteGraphQuestionContextReadFailure(t *testing.T) {
	p, _ := sqliteQuestionNode(t)
	a := p.app
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		_, err := st.appendEvent(p.id, event("assistant", "Cannot silently drop a missing text chunk."))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.writer.Exec(`DELETE FROM data_session_events_chunks WHERE record_key=?`, sqliteOwnedKey(p.id, "1")); err != nil {
		t.Fatal(err)
	}
	err = p.requestQuestion(QuestionRequest{SourceID: "broken-context", Items: []QuestionItem{{ID: "q", Text: "Proceed?"}}}, func([][]string, bool) error { return nil })
	if err == nil || len(p.turn.questions) != 0 {
		t.Fatal("corrupt context admitted a partial question", err)
	}
	var count int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_pending_questions WHERE owner_id=?`, p.id).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed context extraction persisted request", count, err)
	}
}
