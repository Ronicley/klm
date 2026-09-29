package main

import (
	"errors"
	"testing"
)

func TestOpenCodeFinalSnapshotErrorPersistsValidParts(t *testing.T) {
	dir := t.TempDir()
	state := diskState{Version: 2, Projects: []Project{{ID: "p", Folders: []string{}}}, Sessions: []Session{{ID: "s", ProjectID: "p", Status: "running", Events: []Event{}}}, Native: map[string]nativeSession{}}
	if err := saveState(dir, &state); err != nil {
		t.Fatal(err)
	}
	p := &adapter{app: &app{dir: dir, state: state}, id: "s", harness: "opencode", turn: &turn{}, keys: map[string]string{}}
	if err := p.withFinalBatch(func() error {
		if err := p.put("valid", "assistant", "", "valid part", "completed", false, nil); err != nil {
			return err
		}
		failed, err := p.openCodeFinalError(map[string]any{"error": map[string]any{"name": "NativeError", "message": "native failure"}}, false)
		if !failed {
			t.Fatal("snapshot error was not recognized")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !p.failed || p.finalBatch != nil {
		t.Fatal("failure or batch was not finalized")
	}
	loaded, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := replayStreamJournal(dir, &loaded); err != nil {
		t.Fatal(err)
	}
	events := loaded.session("s").Events
	if len(events) != 2 || events[0].Text != "valid part" || events[1].Type != "error" || events[1].Text != "native failure" {
		t.Fatalf("lost final snapshot diagnosis: %+v", events)
	}
	// An invalid part is rejected before it enters the durable batch. A later
	// validation error still flushes the already validated events.
	if err := p.withFinalBatch(func() error {
		if err := p.put("another", "assistant", "", "retained", "completed", false, nil); err != nil {
			return err
		}
		return errors.New("invalid native part")
	}); err == nil {
		t.Fatal("validation error was hidden")
	}
	if len(p.app.state.session("s").Events) != 3 {
		t.Fatal("valid preceding part was discarded")
	}
	p.app.storageErr = errors.New("storage unavailable")
	if err := p.withFinalBatch(func() error {
		return p.put("lost", "assistant", "", "not durable", "completed", false, nil)
	}); !errors.Is(err, p.app.storageErr) || p.finalBatch != nil {
		t.Fatalf("flush failure was hidden or batch leaked: %v", err)
	}
	if len(p.app.state.session("s").Events) != 3 {
		t.Fatal("published a part despite failed persistence")
	}
}
