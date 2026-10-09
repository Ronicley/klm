package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyChatStatePreservation(t *testing.T) {
	a := generalControlFixture(t)
	encoded, _ := json.Marshal(a.state)
	var old map[string]any
	_ = json.Unmarshal(encoded, &old)
	source := old["sessions"].([]any)[1].(map[string]any)
	source["selectedAgent"] = map[string]any{"id": "planner", "name": "Planner", "enabled": true, "defaultHarness": "pi", "model": "model", "prompt": "Plan."}
	source["agentSelectionLocked"] = true
	id := "0123456789abcdef0123456789abcdef"
	old["images"] = map[string]any{id: map[string]any{"id": id, "name": "old.png", "mime": "image/png", "size": 42, "width": 2, "height": 2, "SessionID": "source", "SHA256": "hash", "CreatedAt": now(), "Accepted": true}}
	settings := old["transcription"].(map[string]any)
	settings["provider"] = "openrouter"
	settings["openrouter"] = map[string]any{"apiKey": "fixture-only", "model": "audio-model"}
	old["queuePayloads"] = map[string]any{"old-message": map[string]any{"Submission": map[string]any{"Images": []any{map[string]any{"id": id, "name": "old.png", "mime": "image/png", "size": 42, "width": 2, "height": 2, "Path": filepath.Join(a.dir, "images", id), "SHA256": "hash"}}}, "Harness": "pi", "Directory": a.state.project("p1").Folder}}
	source["queue"] = []any{map[string]any{"id": "old-message", "text": "", "mode": "queue", "status": "paused", "images": []any{map[string]any{"id": id, "name": "old.png", "mime": "image/png", "size": 42, "width": 2, "height": 2}}}}
	encoded, _ = json.Marshal(old)
	if err := os.WriteFile(filepath.Join(a.dir, "state.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(a.dir)
	if err != nil {
		t.Fatal("legacy state rejected", err)
	}
	if loaded.session("source").selectedAgentID() != "planner" || !loaded.session("source").AgentSelectionLocked || loaded.Images[id].SHA256 != "hash" || loaded.QueuePayloads["old-message"].Submission.Images[0].MIME != "image/png" {
		t.Fatal("legacy agent or image data lost")
	}
	if loaded.Transcription.Provider != "openrouter" || loaded.Transcription.OpenRouter.APIKey != "fixture-only" || loaded.Transcription.OpenRouter.Model != "audio-model" {
		t.Fatal("provider configuration lost")
	}
	if err := saveState(a.dir, &loaded); err != nil {
		t.Fatal(err)
	}
	roundtrip, err := loadState(a.dir)
	if err != nil || roundtrip.Transcription.OpenRouter != loaded.Transcription.OpenRouter || roundtrip.session("source").Queue[0].Images[0].ID != id {
		t.Fatal("rewrite discarded legacy configuration or queue")
	}
}
