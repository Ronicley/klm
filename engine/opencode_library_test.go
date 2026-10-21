package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOpenCodeLibraryProjectionPreservesNativePolicy(t *testing.T) {
	root := t.TempDir()
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	cwd, config := filepath.Join(project, "src"), filepath.Join(root, "config", "opencode")
	files := map[string]string{
		filepath.Join(project, ".git", "HEAD"):                "fixture",
		filepath.Join(config, "AGENTS.md"):                    "global",
		filepath.Join(home, ".claude", "CLAUDE.md"):           "shadowed global",
		filepath.Join(project, "AGENTS.md"):                   "project",
		filepath.Join(cwd, "AGENTS.md"):                       "nested",
		filepath.Join(cwd, "CLAUDE.md"):                       "shadowed project",
		filepath.Join(project, "policy.md"):                   "relative to cwd ancestors",
		filepath.Join(project, "environment.md"):              "environment path",
		filepath.Join(project, "docs", "nested", "policy.md"): "glob",
		filepath.Join(project, ".opencode", "policy.md"):      "wrong config-relative path",
		filepath.Join(project, ".opencode", "opencode.json"): `{
			"default_agent":"plan", "permission":{"bash":"allow"},
			"instructions":["policy.md","docs/**/*.md","{env:KLM_TEST_OPENCODE_RULES}"]
		}`,
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(config))
	t.Setenv("KLM_TEST_OPENCODE_RULES", filepath.Join(project, "environment.md"))
	for _, key := range []string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG_CONTENT", "OPENCODE_DISABLE_CLAUDE_CODE", "OPENCODE_DISABLE_CLAUDE_CODE_PROMPT"} {
		t.Setenv(key, "")
	}
	t.Setenv("OPENCODE_PERMISSION", `{"bash":"deny","read":{"*":"allow","*.env":"deny"}}`)
	projection, err := openCodeLibraryProjection(cwd)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{filepath.Join(config, "AGENTS.md"), filepath.Join(cwd, "AGENTS.md"), filepath.Join(project, "AGENTS.md"), filepath.Join(project, "policy.md"), filepath.Join(project, "docs", "nested", "policy.md"), filepath.Join(project, "environment.md")}
	if !reflect.DeepEqual(projection["instructions"], want) {
		t.Fatalf("instruction precedence/relative glob resolution: got %v, want %v", projection["instructions"], want)
	}
	if str(projection, "_klm_root_agent") != "plan" {
		t.Fatal("native plan agent was replaced")
	}
	permission, _ := projection["permission"].(openCodeOrderedObject)
	if action, _ := permission.get("bash"); action != "deny" {
		t.Fatal("OPENCODE_PERMISSION override lost its native precedence")
	}
	t.Setenv("OPENCODE_PERMISSION", "invalid")
	if _, err := openCodeLibraryProjection(cwd); err == nil {
		t.Fatal("invalid native permission override must not silently remove restrictions")
	}
	t.Setenv("OPENCODE_PERMISSION", "{}")
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"instructions":["{file:rules-path.txt}"]}`)
	if _, err := openCodeLibraryProjection(cwd); err == nil {
		t.Fatal("unsupported file-substituted instruction paths must fail explicitly")
	}
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"default_agent":"custom"}`)
	if _, err := openCodeLibraryProjection(cwd); err == nil {
		t.Fatal("unsupported custom root must fail explicitly")
	}
}
