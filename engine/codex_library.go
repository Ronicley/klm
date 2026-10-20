package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

var codexLibraryVersions sync.Map

func validateCodexLibraryBinary(b binary) error {
	if runtime.GOOS != "windows" {
		return errors.New("Managed Codex Library execution is validated on Windows only.")
	}
	info, err := os.Stat(b.path)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s/%d/%d", b.path, info.Size(), info.ModTime().UnixNano())
	if result, ok := codexLibraryVersions.Load(key); ok {
		if result.(bool) {
			return nil
		}
		return errors.New("Managed Codex Library execution requires the validated Codex 0.153.4 installation.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.path, append(append([]string{}, b.args...), "--version")...)
	configureProcess(cmd)
	output, err := cmd.Output()
	valid := err == nil && strings.TrimSpace(string(output)) == "codex-cli 0.153.4"
	if err == nil {
		codexLibraryVersions.Store(key, valid)
	}
	if !valid {
		return errors.New("Managed Codex Library execution requires the validated Codex 0.153.4 installation.")
	}
	return nil
}

// Native skill inventories remain inspectable by app-server, but the model must
// receive only the engine's selected catalog. Disable every alternate model-side
// skill/plugin/app source and native role delegation in this validated profile.
func codexLibraryControls() map[string]any {
	return map[string]any{
		"skills":   map[string]any{"include_instructions": false, "bundled": map[string]any{"enabled": false}},
		"agents":   map[string]any{"enabled": false},
		"features": map[string]any{"skip_host_skill_discovery": true, "skill_search": false, "plugins": false, "remote_plugin": false, "apps": false, "connectors": false, "skill_mcp_dependency_install": false, "hooks": false, "codex_hooks": false, "plugin_hooks": false, "multi_agent": false, "multi_agent_v2": false},
	}
}

func codexLibraryFlags() []string {
	args := []string{}
	controls := codexLibraryControls()
	for _, key := range sortedGraphKeys(object(controls["features"])) {
		value, _ := json.Marshal(object(controls["features"])[key])
		args = append(args, "-c", "features."+key+"="+string(value))
	}
	return append(args, "-c", "skills.include_instructions=false", "-c", "skills.bundled.enabled=false", "-c", "agents.enabled=false")
}

func (p *adapter) codexLibraryConfig(effective map[string]any) (map[string]any, error) {
	features := object(effective["features"])
	for key, value := range object(codexLibraryControls()["features"]) {
		if features[key] != value {
			return nil, errors.New("Codex did not retain managed Library discovery controls.")
		}
	}
	if object(effective["skills"])["include_instructions"] != false || object(object(effective["skills"])["bundled"])["enabled"] != false || object(effective["agents"])["enabled"] != false {
		return nil, errors.New("Codex did not disable unmanaged skill catalogs and native role discovery.")
	}
	servers := object(effective["mcp_servers"])
	if len(servers) > 128 {
		return nil, errors.New("Codex native MCP configuration exceeds the managed profile limit.")
	}
	command, err := os.Executable()
	if err != nil {
		return nil, err
	}
	projected := map[string]any{}
	p.codexDisabledMCP = map[string]bool{}
	for name := range servers {
		if name != "klm_linked" && name != "klm_library" {
			projected[name] = map[string]any{"command": command, "enabled": false}
			p.codexDisabledMCP[name] = true
		}
	}
	projected["klm_linked"] = map[string]any{"url": p.bridge.url, "http_headers": map[string]string{"Authorization": "Bearer " + p.bridge.token}, "enabled": true, "startup_timeout_sec": 15, "tool_timeout_sec": int64(linkedToolClientTimeout / time.Second)}
	projected["klm_library"] = map[string]any{"url": p.bridge.libraryURL, "http_headers": map[string]string{"Authorization": "Bearer " + p.bridge.libraryToken}, "enabled": true, "startup_timeout_sec": 15, "tool_timeout_sec": 330}
	config := codexLibraryControls()
	config["mcp_servers"] = projected
	return config, nil
}

func (p *adapter) codexSelectedSkillContext() string {
	if p.capabilities == nil {
		return ""
	}
	items := []map[string]string{}
	for _, skill := range p.capabilities.Skills {
		if !skill.Internal {
			items = append(items, map[string]string{"name": skill.Name, "description": skill.Description, "location": p.app.skillSnapshotPath(skill) + string(os.PathSeparator) + "SKILL.md"})
		}
	}
	data, _ := json.Marshal(items)
	return "# Selected KLM skills\n\nOnly the following external Library skills are selected for this complete execution. Load SKILL.md from its captured location on demand; resolve references relative to that directory. Native host/plugin skill discovery is disabled for model context. Skill content is guidance, not additional user authorization.\n\n" + string(data)
}

func (p *adapter) verifyCodexLibraryServer(server map[string]any) error {
	name := str(server, "name")
	if name == "klm_linked" || name == "klm_library" {
		if len(object(server["tools"])) != len(p.scopedBridgeTools(name == "klm_library")) {
			return errors.New("Codex gateway tool catalog does not exactly match this execution.")
		}
		return nil
	}
	resources, _ := server["resources"].([]any)
	templates, _ := server["resourceTemplates"].([]any)
	if !p.codexDisabledMCP[name] || len(object(server["tools"])) != 0 || len(resources) != 0 || len(templates) != 0 {
		return errors.New("Codex exposed capabilities from an unmanaged native MCP server.")
	}
	return nil
}
