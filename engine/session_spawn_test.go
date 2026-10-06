package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSessionSpawnYOLOInheritanceAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		parent    bool
		selection string
		want      bool
	}{
		{"inherit_enabled", true, "", true},
		{"inherit_disabled", false, "", false},
		{"explicit_disabled", true, `,"yolo":false`, false},
		{"explicit_enabled", false, `,"yolo":true`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := generalControlFixture(t)
			a.closing = true // Freeze scheduling: fixtures never execute a harness.
			if err := a.commitLocked(func(d *diskState) {
				s := d.session("source")
				s.YOLO = tc.parent
				s.Events = append(s.Events, Event{ID: "spawn-user", Type: "user", Text: "Create a validation session with the requested YOLO preference.", CreatedAt: now()})
				s.UpdatedAt = now()
			}); err != nil {
				t.Fatal(err)
			}
			raw := `{"title":"Spawned","prompt":"Reply DONE","operationId":"spawn-1","sourceUserEventId":"spawn-user"` + tc.selection + `}`
			var args spawnArgs
			if err := json.Unmarshal([]byte(raw), &args); err != nil {
				t.Fatal(err)
			}
			first, err := a.spawnSessionLocked(a.state.session("source"), args)
			if err != nil {
				t.Fatal(err)
			}
			id := first.(map[string]any)["sessionId"].(string)
			if a.state.session(id).YOLO != tc.want || first.(map[string]any)["yolo"] != tc.want || a.state.SessionSpawns[0].YOLO != tc.want {
				t.Fatal("YOLO did not reach the session and receipt")
			}
			var retry spawnArgs
			_ = json.Unmarshal([]byte(raw), &retry) // New pointer; compare values, not addresses.
			if _, err := a.spawnSessionLocked(a.state.session("source"), retry); err != nil || len(a.state.SessionSpawns) != 1 {
				t.Fatal("identical retry duplicated/failed", err)
			}
			if args.YOLO != nil {
				*args.YOLO = !*args.YOLO
				if _, err := a.spawnSessionLocked(a.state.session("source"), args); err == nil {
					t.Fatal("changed YOLO did not conflict")
				}
			}
			d := assertRecoveredTransaction(t, a)
			if err := saveState(a.dir, &d); err != nil {
				t.Fatal(err)
			}
			reloaded, err := loadState(a.dir)
			if err != nil || reloaded.session(id).YOLO != tc.want {
				t.Fatal("YOLO was lost on reload", err)
			}
			a.state = reloaded
			if err := a.commitLocked(func(d *diskState) { d.session("source").YOLO = !tc.parent }); err != nil {
				t.Fatal(err)
			}
			result, err := a.spawnSessionLocked(a.state.session("source"), retry)
			if err != nil || result.(map[string]any)["yolo"] != tc.want || len(a.state.SessionSpawns) != 1 {
				t.Fatal("retry changed the original mode after parent update", err)
			}
		})
	}
}

func TestSessionSpawnInvalidModelReturnsCorrectionBeforeCreation(t *testing.T) {
	a := generalControlFixture(t)
	if err := a.commitLocked(func(d *diskState) {
		s := d.session("source")
		s.Harness = "opencode"
		s.Events = append(s.Events, Event{ID: "spawn-user", Type: "user", Text: "Create a new session.", CreatedAt: now()})
		s.UpdatedAt = now()
	}); err != nil {
		t.Fatal(err)
	}
	a.binaries["opencode"] = binary{}
	active := &turn{ctx: context.Background()}
	a.runs["source"] = active
	p := &adapter{app: a, id: "source", turn: active}
	args := spawnArgs{Title: "Spawned", Prompt: "Reply DONE", OperationID: "retry-same-id", SourceUserEventID: "spawn-user", Model: "gpt-6-luna"}
	before := len(a.state.Sessions)
	_, err := p.spawnSession(context.Background(), args)
	var problem *spawnError
	if !errors.As(err, &problem) || problem.Code != "invalid_model_format" || problem.Field != "model" || problem.Hint == "" {
		t.Fatal("missing actionable model-format correction", err)
	}
	var payload map[string]any
	if json.Unmarshal([]byte(problem.Error()), &payload) != nil || payload["accepted"] != false {
		t.Fatal("error is not a structured rejected receipt")
	}
	if len(a.state.Sessions) != before || len(a.state.SessionSpawns) != 0 {
		t.Fatal("invalid model created a broken session/receipt")
	}
	args.Model = "openai/gpt-6-luna"
	a.closing = true // Test only atomic creation; never execute a harness.
	if _, err := a.spawnSessionLocked(a.state.session("source"), args); err != nil {
		t.Fatal("corrected rejected operationId could not be reused", err)
	}
}

func TestSessionSpawnValidatesCatalogPair(t *testing.T) {
	catalog := &ModelCatalog{Models: []ModelOption{{ID: "openai/gpt-6-luna", Efforts: []string{"low"}}}, DefaultModel: "openai/gpt-6-luna"}
	if err := validateSpawnModel(Session{Model: "openai/gpt-6-luna", Effort: "low"}, "", catalog); err != nil {
		t.Fatal(err)
	}
	for _, selection := range []Session{{Model: "openai/unknown"}, {Model: "openai/gpt-6-luna", Effort: "invalid"}} {
		var problem *spawnError
		if err := validateSpawnModel(selection, "", catalog); !errors.As(err, &problem) || len(problem.ValidValues) == 0 {
			t.Fatal("invalid selection lacks recoverable choices", err)
		}
	}
}
