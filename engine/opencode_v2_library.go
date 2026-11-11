package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/tidwall/jsonc"
)

const managedOpenCodeV2Version = "2.2.7"

func openCodeV2Rules(value any) []any {
	rules, _ := value.([]any)
	return rules
}

func openCodeV2ConfigReferences(value any, dir string) any {
	value = rewriteOpenCodeFileReferences(value, dir)
	var expand func(any) any
	expand = func(value any) any {
		switch v := value.(type) {
		case string:
			return openCodeEnvReference.ReplaceAllStringFunc(v, func(match string) string { return os.Getenv(openCodeEnvReference.FindStringSubmatch(match)[1]) })
		case map[string]any:
			for key, item := range v {
				v[key] = expand(item)
			}
		case []any:
			for i, item := range v {
				v[i] = expand(item)
			}
		}
		return value
	}
	return expand(value)
}

func validateOpenCodeV2LibraryBinary(b binary) error {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(b.path)), "package.json"))
	var pkg struct{ Name, Version string }
	if err != nil || json.Unmarshal(data, &pkg) != nil || pkg.Name != "@opencode/cli" || pkg.Version != managedOpenCodeV2Version {
		return errors.New("Managed OpenCode V2 Library requires @opencode/cli 2.2.7; other versions have not established catalog isolation.")
	}
	return nil
}

// Only native V2 provider, instruction and permission settings enter this profile.
// Discovery is disabled, never relocated to a fake execution directory or database.
func openCodeV2LibraryProjection(cwd string) (map[string]any, error) {
	root := os.Getenv("OPENCODE_CONFIG_DIR")
	if root == "" {
		root = filepath.Join(configHome("XDG_CONFIG_HOME", ".config"), "opencode")
	}
	if !filepath.IsAbs(root) || !filepath.IsAbs(cwd) {
		return nil, errors.New("Managed OpenCode V2 requires absolute configuration and execution directories.")
	}
	projectDisabled := os.Getenv("OPENCODE_DISABLE_PROJECT_CONFIG") == "1" || os.Getenv("OPENCODE_DISABLE_PROJECT_CONFIG") == "true"
	result := map[string]any{"default_agent": "build"}
	var instructions []string
	var policies []any
	merge := func(data []byte, dir string) error {
		var cfg map[string]any
		if json.Unmarshal(jsonc.ToJSON(data), &cfg) != nil || cfg == nil {
			return errors.New("Cannot parse native OpenCode V2 configuration for managed execution.")
		}
		// Do not silently discard restrictions from an unimplemented legacy format.
		for _, key := range []string{"provider", "permission", "tools", "agent", "mode", "enabled_providers", "disabled_providers", "small_model"} {
			if _, exists := cfg[key]; exists {
				return fmt.Errorf("Managed OpenCode V2 Library requires native V2 configuration for %s; the source file remains unchanged.", key)
			}
		}
		for _, key := range []string{"providers", "model", "default_agent", "permissions", "agents", "policies"} {
			value, exists := cfg[key]
			if !exists {
				continue
			}
			value = openCodeV2ConfigReferences(value, dir)
			switch key {
			case "providers", "agents":
				if object(value) == nil {
					return errors.New("Invalid native OpenCode V2 provider or agent configuration.")
				}
				if key == "agents" {
					for id, raw := range object(value) {
						if object(raw) == nil {
							return errors.New("Invalid native OpenCode V2 agent configuration.")
						}
						for _, legacy := range []string{"permission", "tools", "prompt", "variant", "disable"} {
							if _, exists := object(raw)[legacy]; exists {
								return errors.New("Managed OpenCode V2 Library requires native V2 agent settings.")
							}
						}
						prior := object(object(result[key])[id])
						if rawRules, exists := object(raw)["permissions"]; exists {
							rules, ok := rawRules.([]any)
							if !ok {
								return errors.New("Invalid native OpenCode V2 agent permission rules.")
							}
							object(raw)["permissions"] = append(openCodeV2Rules(prior["permissions"]), rules...)
						}
					}
				}
				result[key] = mergeOpenCodeProviderMaps(object(result[key]), object(value))
			case "permissions":
				rules, ok := value.([]any)
				if !ok {
					return errors.New("Invalid native OpenCode V2 permission rules.")
				}
				result[key] = append(openCodeV2Rules(result[key]), rules...)
			case "policies":
				rules, ok := value.([]any)
				if !ok {
					return errors.New("Invalid native OpenCode V2 policy rules.")
				}
				policies = append(rules, policies...) // Broader documents outrank narrower ones.
			default:
				result[key] = value
			}
		}
		if experimental := object(cfg["experimental"]); experimental["policies"] != nil {
			return errors.New("Managed OpenCode V2 Library requires top-level native policies.")
		}
		if raw, exists := cfg["instructions"]; exists {
			values, ok := raw.([]any)
			if !ok {
				return errors.New("Invalid native OpenCode V2 instruction paths.")
			}
			for _, value := range values {
				path, ok := value.(string)
				if !ok || strings.Contains(path, "{file:") {
					return errors.New("Managed OpenCode V2 requires explicit instruction paths.")
				}
				path = openCodeEnvReference.ReplaceAllStringFunc(path, func(match string) string { return os.Getenv(openCodeEnvReference.FindStringSubmatch(match)[1]) })
				instructions = append(instructions, path)
			}
		}
		return nil
	}
	read := func(path string) error {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil || len(data) > 2<<20 {
			return errors.New("Cannot inspect native OpenCode V2 configuration within the 2 MiB limit.")
		}
		return merge(data, filepath.Dir(path))
	}
	var ancestors []string
	for dir := cwd; ; dir = filepath.Dir(dir) {
		ancestors = append(ancestors, dir)
		if filepath.Dir(dir) == dir {
			break
		}
	}
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		if err := read(filepath.Join(root, name)); err != nil {
			return nil, err
		}
	}
	if path := os.Getenv("OPENCODE_CONFIG"); path != "" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if err := read(path); err != nil {
			return nil, err
		}
	}
	for _, prefix := range []string{"", ".opencode"} {
		if projectDisabled {
			break
		}
		for i := len(ancestors) - 1; i >= 0; i-- {
			for _, name := range []string{"opencode.json", "opencode.jsonc"} {
				if err := read(filepath.Join(ancestors[i], prefix, name)); err != nil {
					return nil, err
				}
			}
		}
	}
	if content := os.Getenv("OPENCODE_CONFIG_CONTENT"); strings.TrimSpace(content) != "" {
		if err := merge([]byte(content), cwd); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(os.Getenv("OPENCODE_PERMISSION")) != "" {
		return nil, errors.New("Managed OpenCode V2 Library requires native permissions instead of OPENCODE_PERMISSION.")
	}
	rootAgent := str(result, "default_agent")
	if rootAgent != "build" && rootAgent != "plan" {
		return nil, errors.New("Managed OpenCode V2 Library requires the native build or plan root agent.")
	}
	sourceDirs := []string{root}
	if !projectDisabled {
		sourceDirs = append(sourceDirs, openCodeV2ProjectConfigDirs(ancestors)...)
	}
	for _, dir := range sourceDirs {
		for _, name := range []string{"agent", "agents", "mode", "modes"} {
			files, err := os.ReadDir(filepath.Join(dir, name))
			if err != nil && !os.IsNotExist(err) || len(files) > 0 {
				return nil, errors.New("Managed OpenCode V2 Library cannot project file-based native agents; use native V2 agents configuration.")
			}
		}
	}
	for id, raw := range object(result["agents"]) {
		if !slices.Contains([]string{"build", "plan", "general", "explore", "compaction", "title", "summary"}, id) {
			delete(object(result["agents"]), id)
			continue
		}
		if id == rootAgent && (object(raw)["disabled"] == true || object(raw)["hidden"] == true || str(object(raw), "mode") == "subagent") {
			return nil, errors.New("Managed OpenCode V2 requires an enabled, visible primary root agent.")
		}
	}
	// Resolve initial AGENTS.md without V1's CLAUDE.md fallback.
	ambient := []string{}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	insideHome, relErr := filepath.Rel(home, cwd)
	underHome := relErr == nil && insideHome != ".." && !strings.HasPrefix(insideHome, ".."+string(filepath.Separator)) && !filepath.IsAbs(insideHome)
	for _, dir := range ancestors {
		if projectDisabled {
			break
		}
		ambient = append(ambient, dir)
		if piSamePath(dir, home) {
			break
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil && !underHome {
			break
		}
	}
	paths := []any{}
	add := func(path string) {
		if !slices.Contains(paths, any(path)) {
			paths = append(paths, path)
		}
	}
	for _, dir := range append([]string{root}, ambient...) {
		path := filepath.Join(dir, "AGENTS.md")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			add(path)
		}
	}
	// Relative configured globs retain their native execution-directory scope.
	projectAncestors := []string{}
	for _, dir := range ancestors {
		projectAncestors = append(projectAncestors, dir)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}
	}
	if projectDisabled {
		projectAncestors = []string{root}
	}
	if err := resolveOpenCodeInstructionPaths(home, projectAncestors, instructions, add); err != nil {
		return nil, err
	}
	result["instructions"], result["policies"] = paths, policies
	return result, nil
}

func openCodeV2ProjectConfigDirs(ancestors []string) []string {
	result := make([]string, 0, len(ancestors))
	for _, dir := range ancestors {
		result = append(result, filepath.Join(dir, ".opencode"))
	}
	return result
}

func (p *adapter) configureOpenCodeV2Library(config map[string]any, dir string) ([]string, error) {
	projection, err := openCodeV2LibraryProjection(p.cwd)
	if err != nil {
		return nil, err
	}
	if err := inspectOpenCodeLibrarySources(p.turn.ctx, true); err != nil {
		return nil, err
	}
	managed := "/etc/opencode"
	if runtime.GOOS == "windows" {
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = `C:\ProgramData`
		}
		managed = filepath.Join(programData, "opencode")
	}
	for _, name := range []string{"plugins", "plugin", "skills", "skill", "agents", "agent", "modes", "mode"} {
		entries, err := os.ReadDir(filepath.Join(managed, name))
		if err != nil && !os.IsNotExist(err) || len(entries) > 0 {
			return nil, errors.New("Managed OpenCode V2 cannot replace administrator-managed discovery sources.")
		}
	}
	for key := range config {
		delete(config, key)
	}
	for key, value := range projection {
		config[key] = value
	}
	p.openCodeRootAgent = str(config, "default_agent")
	paths := []string{}
	for _, skill := range p.capabilities.Skills {
		paths = append(paths, p.app.skillSnapshotPath(skill))
	}
	config["skills"] = paths
	config["mcp"] = map[string]any{"servers": map[string]any{}}
	config["update"] = "disable"
	policy := func(action, resource, effect string) any {
		return map[string]any{"action": action, "resource": resource, "effect": effect}
	}
	owned := []any{policy("plugin.use", "*", "deny"), policy("plugin.use", dir, "allow"), policy("mcp.use", "*", "deny"), policy("mcp.use", "klm_linked", "allow"), policy("mcp.use", "klm_library", "allow"), policy("skill.use", "*", "deny")}
	for _, skill := range p.capabilities.Skills {
		owned = append(owned, policy("skill.use", skill.ID, "allow"))
	}
	config["policies"] = append(owned, openCodeV2Rules(config["policies"])...)
	profile := filepath.Join(dir, "profile")
	if err := os.MkdirAll(profile, 0700); err != nil {
		return nil, err
	}
	env := []string{"OPENCODE_CONFIG_DIR=" + profile, "OPENCODE_DISABLE_PROJECT_CONFIG=true", "OPENCODE_DISABLE_EXTERNAL_SKILLS=true"}
	if value := os.Getenv("OPENCODE_DB"); value != "" {
		env = append(env, "OPENCODE_DB="+value)
	}
	return env, nil
}

func (p *adapter) verifyOpenCodeV2Library(h *openCodeHTTP) error {
	ctx := p.turn.ctx
	var info struct{ Version string }
	if err := h.json(ctx, http.MethodGet, "/api/info", nil, &info); err != nil {
		return err
	}
	if info.Version != managedOpenCodeV2Version {
		return errors.New("OpenCode V2 Library isolation is validated only for 2.2.7.")
	}
	var skills []struct{ ID, Path string }
	if err := h.json(ctx, http.MethodGet, "/api/skill", nil, &skills); err != nil {
		return err
	}
	expected := map[string]string{}
	for _, skill := range p.capabilities.Skills {
		expected[skill.ID] = filepath.Join(p.app.skillSnapshotPath(skill), "SKILL.md")
	}
	for _, skill := range skills {
		path, ok := expected[skill.ID]
		if !ok || !piSamePath(path, skill.Path) {
			return errors.New("OpenCode V2 loaded a skill outside the captured Library.")
		}
		delete(expected, skill.ID)
	}
	if len(expected) != 0 {
		return errors.New("OpenCode V2 did not load every captured Library skill.")
	}
	var plugins []struct {
		ID     string
		Source struct{ Type, Path string }
	}
	if err := h.json(ctx, http.MethodGet, "/api/plugin", nil, &plugins); err != nil {
		return err
	}
	ownedFound := false
	for _, plugin := range plugins {
		if plugin.Source.Type == "builtin" {
			continue // Shipped implementations are covered by the exact binary gate.
		}
		if ownedFound || plugin.ID != "klm.policy" || plugin.Source.Type != "local" {
			return errors.New("OpenCode V2 loaded an unmanaged plugin.")
		}
		for _, entry := range p.openCodeLibraryEnv {
			if profile, ok := strings.CutPrefix(entry, "OPENCODE_CONFIG_DIR="); ok && !piSamePath(filepath.Dir(profile), plugin.Source.Path) {
				return errors.New("OpenCode V2 loaded the Library policy from an unexpected source.")
			}
		}
		ownedFound = true
	}
	if !ownedFound {
		return errors.New("OpenCode V2 did not initialize the owned Library policy.")
	}
	var agents []struct {
		ID          string
		Permissions []struct{ Action, Resource, Effect string }
	}
	if err := h.json(ctx, http.MethodGet, "/api/agent", nil, &agents); err != nil {
		return err
	}
	rootFound := false
	for _, agent := range agents {
		root := agent.ID == p.openCodeManagedAgent()
		rootFound = rootFound || root
		effect := func(action, resource string) string {
			result := "ask"
			for _, rule := range agent.Permissions {
				if (rule.Action == "*" || rule.Action == action || rule.Action == "klm_linked_*" && strings.HasPrefix(action, "klm_linked_")) && (rule.Resource == "*" || rule.Resource == resource) {
					result = rule.Effect
				}
			}
			return result
		}
		if (effect("klm_linked_linked_discover", "*") == "allow") != root || effect("skill", "unselected-probe") != "deny" {
			return errors.New("OpenCode V2 role permissions do not match the managed Library.")
		}
		for _, skill := range p.capabilities.Skills {
			if (effect("skill", skill.ID) == "allow") != (!skill.Internal || root) {
				return errors.New("OpenCode V2 delegated a captured skill to an unexpected role.")
			}
		}
	}
	if !rootFound {
		return errors.New("OpenCode V2 did not initialize the managed root agent.")
	}
	return nil
}

func (p *adapter) verifyOpenCodeV2LibraryTools(ids []string) error {
	expected := map[string]bool{}
	p.bridge.mu.RLock()
	for _, tool := range p.bridge.tools {
		prefix := "klm_linked_"
		if truth(tool, "external") {
			prefix = "klm_library_"
		}
		expected[prefix+str(tool, "name")] = true
	}
	p.bridge.mu.RUnlock()
	for _, id := range ids {
		if strings.HasPrefix(id, "opencode_") {
			return errors.New("OpenCode V2 exposed native administration in the managed catalog.")
		}
		if strings.HasPrefix(id, "klm_linked_") || strings.HasPrefix(id, "klm_library_") {
			if !expected[id] {
				return errors.New("OpenCode V2 exposed a tool outside its captured gateways.")
			}
			delete(expected, id)
		}
	}
	if len(expected) != 0 {
		return errors.New("OpenCode V2 did not expose every captured gateway tool.")
	}
	return nil
}

func (h *openCodeHTTP) addV2Gateway(ctx context.Context, name, address, token string, timeout int64) (map[string]any, error) {
	if err := h.json(ctx, http.MethodPut, "/api/experimental/mcp/"+name, map[string]any{"config": map[string]any{"type": "remote", "url": address, "headers": map[string]string{"Authorization": "Bearer " + token}, "oauth": false, "codemode": false, "timeout": map[string]any{"execution": timeout}}}, nil); err != nil {
		return nil, err
	}
	return h.waitMCPConnected(ctx, name)
}
