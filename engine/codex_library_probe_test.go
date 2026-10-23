package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func quoteJSONString(value string) string { data, _ := json.Marshal(value); return string(data) }

func TestCodexLibraryProbe(t *testing.T) {
	if os.Getenv("KLM_RUN_LIBRARY_NATIVE_PROBES") != "1" {
		t.Skip("set KLM_RUN_LIBRARY_NATIVE_PROBES=1 for disposable native probes")
	}
	_, binaries := discoverHarnesses()
	b, ok := binaries["codex"]
	if !ok {
		t.Skip("Codex not installed")
	}
	if err := validateCodexLibraryBinary(b); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "project")
	nativeHome := filepath.Join(root, "codex")
	for _, dir := range []string{home, cwd, nativeHome, filepath.Join(root, "state")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(root, "unselected-started")
	script := filepath.Join(root, "unselected.mjs")
	if err := os.WriteFile(script, []byte("import {writeFileSync} from 'node:fs';writeFileSync("+quoteJSONString(marker)+",'started');process.stdin.resume();"), 0600); err != nil {
		t.Fatal(err)
	}
	node, ok := executable("node")
	if !ok {
		t.Skip("Node not installed")
	}
	config := "model_provider='klm_fixture'\nmodel='fixture'\n[model_providers.klm_fixture]\nname='Fixture'\nbase_url='http://127.0.0.1:9/v1'\nwire_api='responses'\nrequires_openai_auth=false\n[mcp_servers.unselected]\ncommand=" + quoteJSONString(filepath.ToSlash(node)) + "\nargs=[" + quoteJSONString(filepath.ToSlash(script)) + "]\n"
	if err := os.WriteFile(filepath.Join(nativeHome, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", nativeHome)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("KLM_USER_SKILLS_DIR", filepath.Join(root, "klm-skills"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	for _, yolo := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		a := &app{dir: filepath.Join(root, "state"), ctx: ctx}
		p := &adapter{app: a, turn: &turn{ctx: ctx}, harness: "codex", capabilities: &CapabilitySnapshot{Adapter: "codex-library-v1", Skills: []SkillSnapshot{}}, yolo: yolo, mcp: &mcpExecution{tools: []map[string]any{{"name": "ext_fixture", "description": "Selected fixture MCP tool", "external": true, "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}}
		bridge, err := newLinkedBridge(p, ctx)
		if err != nil {
			t.Fatal(err)
		}
		p.bridge = bridge
		rpc, err := newCatalogRPC(ctx, binary{path: b.path, args: codexLibraryFlags()}, cwd, false)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := rpc.call("config/read", map[string]any{"cwd": cwd, "includeLayers": false})
		if err != nil {
			t.Fatal(err)
		}
		projection, err := p.codexLibraryConfig(object(cfg["config"]))
		if err != nil {
			t.Fatal(err)
		}
		approval, sandbox := "on-request", "read-only"
		if yolo {
			approval, sandbox = "never", "danger-full-access"
		}
		thread, err := rpc.call("thread/start", map[string]any{"cwd": cwd, "ephemeral": true, "config": projection, "approvalPolicy": approval, "sandbox": sandbox})
		if err != nil {
			t.Fatal(err)
		}
		id := str(object(thread["thread"]), "id")
		if id == "" {
			t.Fatal("no native thread identity")
		}
		inventory, err := rpc.call("mcpServerStatus/list", map[string]any{"threadId": id, "limit": 100})
		if err != nil {
			t.Fatal(err)
		}
		found, libraryFound := false, false
		for _, value := range inventory["data"].([]any) {
			server := object(value)
			if err := p.verifyCodexLibraryServer(server); err != nil {
				t.Fatal(err)
			}
			found = found || str(server, "name") == "klm_linked"
			libraryFound = libraryFound || str(server, "name") == "klm_library"
		}
		if !found || !libraryFound {
			t.Fatal("owned gateway missing")
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("unselected server was started")
		}
		t.Logf("attested managed native controls, gateway-only tools, disabled original server and preserved sandbox: yolo=%t", yolo)
		_ = rpc.process.Close()
		bridge.close()
		cancel()
	}
}
