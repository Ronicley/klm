package main

// Disposable native metadata probes: no model request, real credential, engine
// data or project configuration is used. Run only the named probe explicitly.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenCodeLibraryProbe(t *testing.T) {
	if os.Getenv("KLM_RUN_LIBRARY_NATIVE_PROBES") != "1" {
		t.Skip("set KLM_RUN_LIBRARY_NATIVE_PROBES=1 for disposable native probes")
	}
	_, binaries := discoverHarnesses()
	b, ok := binaries["opencode"]
	if !ok {
		t.Skip("OpenCode not installed")
	}
	if err := validateOpenCodeLibraryBinary(b); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "project")
	for _, dir := range []string{home, cwd, filepath.Join(cwd, ".git"), filepath.Join(home, ".config", "opencode", "plugins"), filepath.Join(home, ".opencode", "skills", "forbidden-home"), filepath.Join(cwd, ".opencode", "skills", "forbidden-project"), filepath.Join(root, "state")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(home, ".opencode", "skills", "forbidden-home", "SKILL.md"), filepath.Join(cwd, ".opencode", "skills", "forbidden-project", "SKILL.md")} {
		if err := os.WriteFile(path, []byte("---\nname: forbidden\ndescription: fixture\n---\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "opencode", "plugins", "forbidden.mjs"), []byte(`throw new Error("Unselected plugin loaded")`), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := map[string]any{"mcp": map[string]any{"forbidden": map[string]any{"type": "local", "command": []string{"not-a-real-executable-klm-probe"}, "enabled": true}}}
	fixture["permission"] = map[string]any{"bash": "allow", "read": map[string]any{"*": "allow", "*.env": "deny"}}
	data, _ := json.Marshal(fixture)
	if err := os.WriteFile(filepath.Join(home, ".config", "opencode", "opencode.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".opencode", "opencode.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "xdg-state"))
	t.Setenv("KLM_USER_SKILLS_DIR", filepath.Join(root, "user-skills"))
	t.Setenv("OPENCODE_CONFIG", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"enabled_providers":["openai"],"provider":{"openai":{"options":{"apiKey":"fixture-only","baseURL":"http://127.0.0.1:9/v1"}}}}`)
	t.Setenv("OPENCODE_DISABLE_MODELS_FETCH", "true")
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("OPENCODE_PERMISSION", `{"bash":"deny"}`)
	for _, selected := range []bool{false, true} {
		rootAgent := "build"
		if selected {
			rootAgent = "plan"
		}
		t.Setenv("OPENCODE_CONFIG_CONTENT", `{"default_agent":"`+rootAgent+`","enabled_providers":["openai"],"provider":{"openai":{"options":{"apiKey":"fixture-only","baseURL":"http://127.0.0.1:9/v1"}}}}`)
		for _, yolo := range []bool{false, true} {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			a := &app{dir: filepath.Join(root, "state"), ctx: ctx}
			p := &adapter{app: a, turn: &turn{ctx: ctx}, cwd: cwd, harness: "opencode", yolo: yolo, capabilities: &CapabilitySnapshot{Adapter: "opencode-library-v1", Skills: []SkillSnapshot{}}, mcp: &mcpExecution{tools: []map[string]any{{"name": "ext_fixture", "description": "Selected fixture MCP tool", "external": true, "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}}
			if selected {
				source := filepath.Join(root, "source", "selected")
				if err := os.MkdirAll(source, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: selected\ndescription: fixture selected skill\n---\nFixture\n"), 0600); err != nil {
					t.Fatal(err)
				}
				pkg, err := stableSkillPackage(filepath.Dir(source), "selected", false)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := a.materializeSkill(pkg, false)
				if err != nil {
					t.Fatal(err)
				}
				p.capabilities.Skills = append(p.capabilities.Skills, snapshot)
			}
			bridge, err := newLinkedBridge(p, ctx)
			if err != nil {
				t.Fatal(err)
			}
			p.bridge = bridge
			server, err := p.startOpenCodeServer(b, cwd, ctx)
			if err != nil {
				bridge.close()
				cancel()
				t.Fatalf("selected=%t yolo=%t: %v", selected, yolo, err)
			}
			t.Logf("attested exact selected skill paths, builtin denial, root/subagent permission lists and gateway-only MCP: selected=%t yolo=%t", selected, yolo)
			var effective map[string]any
			if err := server.h.json(ctx, "GET", "/config", nil, &effective); err != nil {
				t.Fatal(err)
			}
			if !yolo && str(object(effective["permission"]), "bash") != "deny" {
				t.Fatal("native OPENCODE_PERMISSION override was discarded")
			}
			var agents []struct {
				Name       string `json:"name"`
				Permission []struct {
					Permission string `json:"permission"`
					Pattern    string `json:"pattern"`
					Action     string `json:"action"`
				} `json:"permission"`
			}
			if err := server.h.json(ctx, "GET", "/agent", nil, &agents); err != nil {
				t.Fatal(err)
			}
			if str(effective, "default_agent") != rootAgent {
				t.Fatal("selected native root agent was replaced")
			}
			for _, agent := range agents {
				if agent.Name != rootAgent || yolo {
					continue
				}
				question, edit := "", ""
				for _, rule := range agent.Permission {
					if rule.Pattern != "*" {
						continue
					}
					if rule.Permission == "*" || rule.Permission == "question" {
						question = rule.Action
					}
					if rule.Permission == "*" || rule.Permission == "edit" {
						edit = rule.Action
					}
				}
				if question != "allow" || (rootAgent == "plan" && edit != "deny") {
					t.Fatalf("native %s baseline discarded: question=%s edit=%s", rootAgent, question, edit)
				}
			}
			server.Close()
			bridge.close()
			cancel()
		}
	}
}
