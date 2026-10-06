package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessageIdentityReplay(t *testing.T) {
	const id = "client-identity"
	a := &app{state: diskState{GraphRevision: 1, GraphViewRevision: 1, Projects: []Project{{ID: "p", Folders: []string{}, Folder: t.TempDir()}}, Sessions: []Session{{ID: "s", ProjectID: "p", Status: "idle", Events: []Event{}, Queue: []QueuedMessage{{ID: id, Text: "hello", Mode: "queue", Status: "queued"}}}}, AcceptedMessages: map[string]string{"s/" + id: messageFingerprint("hello", "queue", nil, nil)}}}
	call := func(text string) int {
		r := httptest.NewRequest("POST", "/api/sessions/s/messages", strings.NewReader(`{"clientId":"`+id+`","text":"`+text+`","mode":"queue"}`))
		r.SetPathValue("id", "s")
		w := httptest.NewRecorder()
		a.message(w, r)
		return w.Code
	}
	if got := call("hello"); got != 202 {
		t.Fatalf("same identity: got %d", got)
	}
	if got := call("different"); got != 409 {
		t.Fatalf("conflicting identity: got %d", got)
	}
	if len(a.state.session("s").Queue) != 1 {
		t.Fatal("replay duplicated queue entry")
	}
}
