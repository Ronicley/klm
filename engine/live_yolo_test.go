package main

import (
	"context"
	"testing"
)

func TestLiveYOLOResolvesPendingAndFuturePermissions(t *testing.T) {
	a := generalControlFixture(t)
	active := a.runs["target"]
	p := &adapter{app: a, id: "target", turn: active, harness: "opencode", cwd: a.state.project("p2").Folder, yolo: false}
	questionAnswered := false
	if err := p.requestQuestion(QuestionRequest{SourceID: "native-question", Items: []QuestionItem{{ID: "choice", Text: "Choose a token", Custom: true}}}, func([][]string, bool) error { questionAnswered = true; return nil }); err != nil {
		t.Fatal(err)
	}
	permission := func(id string) Permission {
		return Permission{SourceID: id, Kind: "bash", Command: "echo validation", Path: p.cwd, Details: map[string]any{"toolName": "bash"}}
	}
	pendingAllowed, nextAllowed := false, false
	if err := p.requestPermission(permission("pending"), map[string]any{"command": "echo validation"}, func(allow bool) error { pendingAllowed = allow; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(a.state.session("target").Permissions) != 1 {
		t.Fatal("normal permission did not wait")
	}
	enabled := true
	if err := a.changeSessionSettings(context.Background(), "target", nil, nil, &enabled); err != nil {
		t.Fatal(err)
	}
	if !pendingAllowed || len(a.state.session("target").Permissions) != 0 || len(a.state.Grants) != 0 || a.runs["target"] != active || active.ctx.Err() != nil {
		t.Fatal("live YOLO failed to resolve without granting/stopping")
	}
	if len(a.state.session("target").Questions) != 1 || questionAnswered {
		t.Fatal("YOLO answered a native question")
	}
	if err := p.requestPermission(permission("future"), map[string]any{"command": "echo validation"}, func(allow bool) error { nextAllowed = allow; return nil }); err != nil {
		t.Fatal(err)
	}
	if !nextAllowed || len(a.state.session("target").Permissions) != 0 {
		t.Fatal("existing adapter did not observe live YOLO for next permission")
	}
	enabled = false
	if err := a.changeSessionSettings(context.Background(), "target", nil, nil, &enabled); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := p.requestPermission(permission("after-disable"), map[string]any{"command": "echo validation"}, func(bool) error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if called || len(a.state.session("target").Permissions) != 1 {
		t.Fatal("disabling YOLO did not restore future permission handling")
	}
}

func TestLiveYOLORejectsUnsafeCodexDowngrade(t *testing.T) {
	a := generalControlFixture(t)
	if err := a.commitLocked(func(d *diskState) { s := d.session("target"); s.Harness, s.YOLO = "codex", true }); err != nil {
		t.Fatal(err)
	}
	p := &adapter{app: a, id: "target", turn: a.runs["target"], harness: "codex", yolo: true}
	if !p.captureNativeYOLO() {
		t.Fatal("native full-access mode not captured")
	}
	if err := a.changeSessionYOLO(context.Background(), "target", false); err == nil || !a.state.session("target").YOLO {
		t.Fatal("unsafe native sandbox downgrade silently accepted")
	}
	delete(a.runs, "target")
	if err := a.changeSessionYOLO(context.Background(), "target", false); err != nil {
		t.Fatal(err)
	}
	a.runs["target"] = &turn{ctx: context.Background()}
	p.turn = a.runs["target"]
	if p.captureNativeYOLO() {
		t.Fatal("native normal mode not captured")
	}
	if err := a.changeSessionYOLO(context.Background(), "target", true); err != nil {
		t.Fatal(err)
	}
	if err := a.changeSessionYOLO(context.Background(), "target", false); err != nil {
		t.Fatal("turn started normally cannot restore normal engine policy", err)
	}
}
