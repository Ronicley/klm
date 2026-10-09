package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelectedChatAgentPrompt(t *testing.T) {
	a := generalControlFixture(t)
	s := *a.state.session("source")
	s.SelectedAgent = &AgentRecord{ID: "planner"}
	project := *a.state.project(s.ProjectID)
	dir := filepath.Join(project.Folder, ".klm", "agents")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "planner.toml")
	definition := "name = 'Planner'\nenabled = true\ndefault_harness = 'pi'\nmodel = 'model'\nprompt = 'Plan this task.'\n"
	if err := os.WriteFile(path, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	prompt, err := a.chatAgentPrompt(s)
	if err != nil || prompt != "Plan this task." {
		t.Fatalf("selected prompt not applied: %q %v", prompt, err)
	}
	s.Harness = "codex"
	if _, err := a.chatAgentPrompt(s); err == nil {
		t.Fatal("changed harness silently accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s.Harness = "pi"
	if _, err := a.chatAgentPrompt(s); err == nil {
		t.Fatal("removed agent silently ignored")
	}
	s.SelectedAgent = nil
	if prompt, err := a.chatAgentPrompt(s); err != nil || prompt != "" {
		t.Fatal("None still injects an agent")
	}
}
