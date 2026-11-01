package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Run with -trimpath to exercise the installed binary fallback without source
// paths. The worker's cwd contains an executable named klm, not a klm directory.
func TestInternalPromptDistribution(t *testing.T) {
	t.Setenv("KLM_PROMPTS_DIR", "")
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("klm", []byte("executable placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := packagedInternalPrompts.ReadDir("prompts")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		want, err := packagedInternalPrompts.ReadFile("prompts/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		got, err := loadInternalPrompt(entry.Name())
		if err != nil || got != string(want) || got == "" {
			t.Fatalf("%s: packaged prompt mismatch or load error: %v", entry.Name(), err)
		}
	}
	if _, err := loadInternalPrompt("unknown.md"); err == nil {
		t.Fatal("missing packaged prompt must fail")
	}
	if _, err := loadInternalPrompt("../session-collaboration.md"); err == nil {
		t.Fatal("invalid prompt name must fail")
	}
}

func TestInternalPromptOverrideIsAuthoritative(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KLM_PROMPTS_DIR", dir)
	name := "session-collaboration.md"
	if _, err := loadInternalPrompt(name); err == nil {
		t.Fatal("missing override must not fall back to packaged guidance")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(" \n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadInternalPrompt(name); err == nil {
		t.Fatal("empty override must not fall back to packaged guidance")
	}
	if err := os.WriteFile(path, []byte("editable guidance"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := loadInternalPrompt(name)
	if err != nil || got != "editable guidance" {
		t.Fatalf("override was not applied: %v", err)
	}
}
