package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenCodeSubagentQuestion(t *testing.T) {
	for _, action := range []string{"answer", "dismiss", "stop", "retry"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			active := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
			a := &app{dir: t.TempDir(), historyJournal: map[string]*sessionJournal{}, runs: map[string]*turn{"main": active}, state: diskState{
				Version: 2, Native: map[string]nativeSession{}, Projects: []Project{{ID: "project", Folders: []string{}}},
				Sessions: []Session{
					{ID: "main", ProjectID: "project", Harness: "opencode", Status: "running", Events: []Event{}},
					{ID: "child", ParentID: "main", Role: sessionRoleSubagent, ProjectID: "project", Harness: "opencode", Status: "running", Events: []Event{}},
				},
			}}
			p := &adapter{app: a, id: "main", turn: active, harness: "opencode", yolo: true}
			posts := 0
			h := &openCodeHTTP{base: "http://opencode.invalid", client: &http.Client{Transport: openCodeHistoryTransport(func(r *http.Request) (*http.Response, error) {
				status, body := 204, ""
				if r.Method == http.MethodGet && r.URL.Path == "/api/session/native-child" {
					status, body = 200, `{"data":{"id":"native-child","parentID":"native-main"}}`
				} else {
					posts++
					wantPath, wantMethod := "/api/session/native-child/form/native-question/reply", http.MethodPost
					if action == "dismiss" {
						wantPath, wantMethod = "/api/session/native-child/form/native-question", http.MethodDelete
					}
					if r.Method != wantMethod || r.URL.Path != wantPath {
						t.Fatalf("answer went to the wrong endpoint: %s %s", r.Method, r.URL.Path)
					}
					if action != "dismiss" {
						var sent struct {
							Answer map[string][]string `json:"answer"`
						}
						if err := json.NewDecoder(r.Body).Decode(&sent); err != nil || !reflect.DeepEqual(sent.Answer, map[string][]string{"choice": {"a", "b"}}) {
							t.Fatalf("native choices changed: %+v (%v)", sent, err)
						}
					}
					if action == "retry" && posts == 1 {
						status = 503
					}
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			owned, err := h.ownsSession(ctx, "native-child", map[string]bool{"native-main": true})
			if err != nil || !owned {
				t.Fatalf("child request was discarded: %v", err)
			}
			questions := map[string]*atomic.Bool{}
			err = p.requestOpenCodeQuestion(ctx, h, map[string]any{
				"id": "native-question", "sessionID": "native-child",
				"title": "Choice", "fields": []any{map[string]any{"key": "choice", "title": "Which options?", "type": "multiselect", "custom": false,
					"options": []any{map[string]any{"value": "a", "label": "A", "description": "First"}, map[string]any{"value": "b", "label": "B", "description": "Second"}}}},
			}, questions)
			if err != nil {
				t.Fatal(err)
			}
			main := a.state.session("main")
			if len(main.Questions) != 1 || len(a.state.session("child").Questions) != 0 || posts != 0 {
				t.Fatal("question must wait for the user on the main conversation, even in YOLO")
			}
			q := main.Questions[0]
			if !q.Items[0].Multiple || q.Items[0].Custom || q.Items[0].Options[1].Label != "B" {
				t.Fatal("question choices or selection semantics were lost")
			}
			if action == "stop" {
				cancel()
			}
			answer := func() int {
				body := `{"answers":[["A","B"]]}`
				if action == "dismiss" {
					body = `{"cancelled":true}`
				}
				r := httptest.NewRequest(http.MethodPost, "/questions/reply", strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.SetPathValue("id", "main")
				r.SetPathValue("questionID", q.ID)
				w := httptest.NewRecorder()
				a.answerQuestion(w, r)
				return w.Code
			}
			if action == "retry" && answer() != http.StatusConflict {
				t.Fatal("failed delivery was not reported to the user")
			}
			if action == "stop" {
				if answer() != http.StatusConflict || posts != 0 {
					t.Fatal("an answer was delivered after Stop")
				}
				if err := p.dismissQuestion("native-question"); err != nil {
					t.Fatal(err)
				}
			} else if code := answer(); code != http.StatusOK {
				t.Fatalf("answer failed: HTTP %d", code)
			}
			if len(a.state.session("main").Questions) != 0 || len(a.state.session("main").Queue) != 0 {
				t.Fatal("question remained pending or became a new queued prompt")
			}
		})
	}
}
