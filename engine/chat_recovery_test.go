package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenCodeQuestionKeepsPlanAndContext(t *testing.T) {
	plan := "# Joint plan\n\nBackend and frontend implementation with shared acceptance."
	state := diskState{Version: 2, Projects: []Project{{ID: "p"}}, Native: map[string]nativeSession{}, Sessions: []Session{{ID: "s", ProjectID: "p", Status: "running", Events: []Event{{ID: "plan", Type: "assistant", Text: plan}}}}}
	activeTurn := &turn{ctx: context.Background()}
	a := &app{dir: t.TempDir(), state: state, runs: map[string]*turn{"s": activeTurn}}
	p := &adapter{app: a, id: "s", harness: "opencode", turn: activeTurn, graph: &graphAdapterBinding{hooks: GraphAdapterHooks{Node: true}, state: "open"}}
	properties := map[string]any{"id": "form", "title": "Approve plan", "fields": []map[string]any{{"key": "approval", "type": "string", "title": "Proceed?", "description": plan}}}
	if err := p.requestOpenCodeQuestion(context.Background(), nil, properties, map[string]*atomic.Bool{}); err != nil {
		t.Fatal(err)
	}
	q := a.state.session("s").Questions[0]
	if q.Items[0].Text != "Proceed?" || q.Items[0].Description != plan || q.Context != plan {
		t.Fatal("approval lost its title, plan description or visible node context")
	}
	encoded, _ := json.Marshal(q)
	var restored QuestionRequest
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Context != plan || restored.Items[0].Description != plan {
		t.Fatal("plan did not survive persistence")
	}
}

func TestEditedMessageRetainsFilesAndReorderedMentions(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("context "+file), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a := &app{dir: t.TempDir()}
	hash := sha256.Sum256([]byte("s"))
	dir := filepath.Join(a.dir, "attachments", hex.EncodeToString(hash[:]), "message-original")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "upload.txt"), []byte("uploaded context"), 0600); err != nil {
		t.Fatal(err)
	}
	e := Event{Type: "user", Data: map[string]any{
		"mentions": []Mention{{ID: "a", Path: "a.txt", Kind: "file", Start: 0, End: 6}, {ID: "b", Path: "b.txt", Kind: "file", Start: 7, End: 13}},
		"files":    []ChatFile{{ID: "upload", Name: "notes.txt", Type: "text/plain", URL: "/api/sessions/s/files/upload"}},
	}}
	q, err := editedMessageMetadata(e, "🙂 revised @b.txt then @a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Mentions) != 2 || q.Mentions[0].ID != "b" || len(q.Files) != 1 {
		t.Fatal("edit discarded attached context")
	}
	payload, err := a.prepareEditedMessage(context.Background(), "s", root, "pi", q)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"context a.txt", "context b.txt", "uploaded context"} {
		if !strings.Contains(payload.Context, text) {
			t.Fatalf("missing %s", text)
		}
	}
}
