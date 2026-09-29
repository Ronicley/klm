package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func permissionPersistenceState() diskState {
	return diskState{Version: 2, Native: map[string]nativeSession{},
		Projects: []Project{{ID: "project", Folders: []string{}}},
		Sessions: []Session{{ID: "main", ProjectID: "project", Harness: "opencode", Status: "running", Events: []Event{}}},
	}
}

func TestPermissionDecisionPersistence(t *testing.T) {
	for _, decision := range []string{"once", "session", "always", "reject", "deny_project", "allow_global", "deny_global"} {
		t.Run(decision, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			active := &turn{ctx: ctx, cancel: cancel}
			a := &app{dir: t.TempDir(), state: permissionPersistenceState(), runs: map[string]*turn{"main": active}, historyJournal: map[string]*sessionJournal{}}
			// Startup persists the base before commands append durable journal frames.
			if err := saveState(a.dir, &a.state); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			p := &adapter{app: a, id: "main", turn: active, harness: "opencode", cwd: filepath.Join(root, "workspace")}
			input := map[string]any{"filePath": filepath.Join(root, "other-project", "audio.rs")}
			calls, allowed := 0, false
			err := p.requestPermission(Permission{SourceID: "preflight:native-child/call", Kind: "read", Title: "Allow read",
				Details: map[string]any{"toolName": "read", "input": input}}, map[string]any{"input": input}, func(allow bool) error {
				calls++
				allowed = allow
				return nil
			})
			if err != nil || len(a.state.session("main").Permissions) != 1 || calls != 0 {
				t.Fatalf("request must wait for approval: %v", err)
			}
			id := a.state.session("main").Permissions[0].ID
			r := httptest.NewRequest(http.MethodPost, "/permissions/"+id, strings.NewReader(`{"decision":"`+decision+`"}`))
			r.Header.Set("Content-Type", "application/json")
			r.SetPathValue("id", "main")
			r.SetPathValue("permissionID", id)
			w := httptest.NewRecorder()
			a.permissionDecision(w, r)
			if w.Code != http.StatusOK || a.storageErr != nil || ctx.Err() != nil {
				t.Fatalf("approval poisoned storage: HTTP %d, %s", w.Code, w.Body.String())
			}
			wantAllow := decision != "reject" && decision != "deny_project" && decision != "deny_global"
			if calls != 1 || allowed != wantAllow || len(a.state.session("main").Permissions) != 0 {
				t.Fatal("decision was not delivered exactly once")
			}
			loaded, err := loadState(a.dir)
			if err != nil {
				t.Fatalf("saved permission cannot survive restart: %v", err)
			}
			if err := replayStreamJournal(a.dir, &loaded); err != nil {
				t.Fatalf("permission journal cannot survive restart: %v", err)
			}
			if decision == "once" || decision == "reject" {
				if len(loaded.Grants) != 0 {
					t.Fatal("one-time decision became a remembered grant")
				}
				return
			}
			if len(loaded.Grants) != 1 {
				t.Fatal("remembered decision was lost")
			}
			grant := loaded.Grants[0]
			if grant.Harness != "" || grant.Kind != "read" || (grant.ProjectID == "") != (decision == "allow_global" || decision == "deny_global") || (grant.SessionID != "") != (decision == "session") {
				t.Fatalf("permission scope changed: %+v", grant)
			}
		})
	}
}

func TestPermissionGrantOwnershipValidation(t *testing.T) {
	for _, test := range []struct {
		name, project, session, harness string
		valid                           bool
	}{
		{"legacy session", "project", "main", "opencode", true},
		{"shared session", "project", "main", "", true},
		{"shared global", "", "", "", true},
		{"native global", "", "", "opencode", true},
		{"unknown project", "missing", "", "", false},
		{"unknown session", "project", "missing", "", false},
		{"global with session", "", "main", "", false},
		{"different harness", "project", "main", "pi", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := permissionPersistenceState()
			d.Grants = []permissionGrant{{ID: "grant", ProjectID: test.project, SessionID: test.session, Harness: test.harness, Kind: "read", Key: "scope", CreatedAt: now()}}
			err := validateGraphRecords(&d)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, error=%v", test.valid, err)
			}
		})
	}
}
