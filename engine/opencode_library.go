package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/tidwall/jsonc"
)

const managedOpenCodeAgent = "build"

func (p *adapter) openCodeManagedAgent() string {
	if p.openCodeRootAgent != "" {
		return p.openCodeRootAgent
	}
	return managedOpenCodeAgent
}

func validateOpenCodeLibraryBinary(b binary) error {
	if runtime.GOOS != "windows" {
		return errors.New("Managed OpenCode Library execution is validated on Windows only.")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(b.path)), "package.json"))
	var metadata struct {
		Version string `json:"version"`
		Name    string `json:"name"`
	}
	if err != nil || json.Unmarshal(data, &metadata) != nil || metadata.Name != "opencode-ai" || metadata.Version != "1.18.35" {
		return errors.New("Managed OpenCode Library execution requires the validated opencode-ai 1.18.35 installation.")
	}
	return nil
}

// Only provider/model/instruction data crosses from native configuration. Native
// MCPs, packages, plugins, tools and agents are not merged into the managed profile.
func openCodeLibraryProjection(cwd string) (map[string]any, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	configRoot := filepath.Join(configHome("XDG_CONFIG_HOME", ".config"), "opencode")
	result := map[string]any{}
	defaultAgent := "build"
	agentDefaults := map[string]any{}
	var nativePermissions any
	var nativeTools any
	var instructions []string
	defaultPermissions := map[string]any{}
	merge := func(data []byte, dir string) error {
		var cfg map[string]any
		if json.Unmarshal(jsonc.ToJSON(data), &cfg) != nil || cfg == nil {
			return errors.New("Cannot parse native OpenCode provider configuration for managed execution.")
		}
		ordered, err := readOpenCodeOrderedJSON(jsonc.ToJSON(data))
		if err != nil {
			return err
		}
		if value, ok := ordered.get("permission"); ok {
			nativePermissions = mergeOpenCodeOrdered(nativePermissions, permissionObject(value))
		}
		if value, ok := ordered.get("tools"); ok {
			nativeTools = mergeOpenCodeOrdered(nativeTools, value)
		}
		for _, kind := range []string{"mode", "agent"} {
			if list, ok := ordered.get(kind); ok {
				if agents, ok := list.(openCodeOrderedObject); ok {
					for _, agent := range agents {
						if cfg, ok := agent.value.(openCodeOrderedObject); ok {
							if value, ok := cfg.get("permission"); ok {
								defaultPermissions[agent.key] = mergeOpenCodeOrdered(defaultPermissions[agent.key], permissionObject(value))
							}
						}
					}
				}
			}
		}
		for _, key := range []string{"provider", "model", "small_model", "enabled_providers", "disabled_providers"} {
			if value, ok := cfg[key]; ok {
				if key == "provider" {
					result[key] = mergeOpenCodeProviderMaps(object(result[key]), object(rewriteOpenCodeFileReferences(value, dir)))
				} else {
					result[key] = value
				}
			}
		}
		if name := str(cfg, "default_agent"); name != "" {
			defaultAgent = name
		}
		for _, kind := range []string{"mode", "agent"} {
			for name, value := range object(cfg[kind]) {
				settings := object(agentDefaults[name])
				if settings == nil {
					settings = map[string]any{}
				}
				for _, key := range []string{"model", "variant", "tools", "mode", "hidden", "disable"} {
					if value, ok := object(value)[key]; ok {
						settings[key] = value
					}
				}
				agentDefaults[name] = settings
			}
		}
		if values, ok := cfg["instructions"].([]any); ok {
			for _, value := range values {
				if path, ok := value.(string); ok {
					path = openCodeEnvReference.ReplaceAllStringFunc(path, func(match string) string {
						return os.Getenv(openCodeEnvReference.FindStringSubmatch(match)[1])
					})
					if strings.Contains(path, "{file:") {
						return errors.New("Managed OpenCode cannot resolve file-substituted instruction paths; use an explicit instruction path.")
					}
					instructions = append(instructions, path)
				}
			}
		}
		return nil
	}
	read := func(path string) error {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return errors.New("Cannot inspect native OpenCode configuration.")
		}
		if len(data) > 2<<20 {
			return errors.New("Native OpenCode configuration exceeds 2 MiB.")
		}
		return merge(data, filepath.Dir(path))
	}
	for _, name := range []string{"config.json", "opencode.json", "opencode.jsonc"} {
		if err := read(filepath.Join(configRoot, name)); err != nil {
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
	ancestors := []string{}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		ancestors = append(ancestors, dir)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil || filepath.Dir(dir) == dir {
			break
		}
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		for _, name := range []string{"opencode.json", "opencode.jsonc", ".opencode/opencode.json", ".opencode/opencode.jsonc"} {
			if err := read(filepath.Join(ancestors[i], filepath.FromSlash(name))); err != nil {
				return nil, err
			}
		}
	}
	if dir := os.Getenv("OPENCODE_CONFIG_DIR"); dir != "" {
		for _, name := range []string{"opencode.json", "opencode.jsonc"} {
			if err := read(filepath.Join(dir, name)); err != nil {
				return nil, err
			}
		}
	}
	if cfg := os.Getenv("OPENCODE_CONFIG_CONTENT"); strings.TrimSpace(cfg) != "" {
		if err := merge([]byte(cfg), cwd); err != nil {
			return nil, err
		}
	}
	if value := os.Getenv("OPENCODE_PERMISSION"); strings.TrimSpace(value) != "" {
		// This native override is applied after configuration files/content. Do not
		// forward it to the child, where it could override owned capability rules.
		permission, err := readOpenCodeOrderedJSON([]byte(value))
		if err != nil {
			return nil, errors.New("Cannot safely project OPENCODE_PERMISSION; expected a JSON permission object.")
		}
		nativePermissions = mergeOpenCodeOrdered(nativePermissions, permission)
	}
	result["instructions"], err = openCodeLibraryInstructions(home, configRoot, ancestors, instructions)
	if err != nil {
		return nil, err
	}
	// Reuse the native primary agent so its build/plan baseline is retained.
	// A custom agent cannot be reconstructed without importing its definition.
	if defaultAgent != "build" && defaultAgent != "plan" {
		return nil, errors.New("Managed OpenCode Library requires the native build or plan root agent; custom root agents are unsupported.")
	}
	result["_klm_root_agent"] = defaultAgent
	if settings := object(agentDefaults[defaultAgent]); settings != nil {
		if settings["disable"] == true || settings["hidden"] == true || str(settings, "mode") == "subagent" {
			return nil, errors.New("Managed OpenCode Library requires an enabled, visible primary root agent.")
		}
		if model := str(settings, "model"); model != "" {
			result["model"] = model
		}
		if variant := str(settings, "variant"); variant != "" {
			result["_klm_variant"] = variant
		}
		if tools := settings["tools"]; tools != nil {
			result["_klm_root_tools"] = tools
		}
	}
	if nativePermissions != nil {
		result["permission"] = nativePermissions
	}
	if nativeTools != nil {
		result["tools"] = nativeTools
	}
	if value := defaultPermissions[defaultAgent]; value != nil {
		result["_klm_root_permission"] = value
	}
	return result, nil
}

// OpenCode 1.18.35 chooses the first existing global instruction file, then all
// ancestor matches of the first project filename with a match. Configured relative
// globs resolve from the execution directory up to the worktree, not the config
// file's directory. Resolve them before disabling native project discovery.
func openCodeLibraryInstructions(home, configRoot string, ancestors, configured []string) ([]any, error) {
	result := []any{}
	seen := map[string]bool{}
	add := func(path string) {
		key := filepath.Clean(path)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, path)
		}
	}
	claudeDisabled := func(key string) bool { v := os.Getenv(key); return v == "1" || v == "true" }
	useClaude := !claudeDisabled("OPENCODE_DISABLE_CLAUDE_CODE") && !claudeDisabled("OPENCODE_DISABLE_CLAUDE_CODE_PROMPT")
	global := []string{filepath.Join(configRoot, "AGENTS.md")}
	names := []string{"AGENTS.md"}
	if useClaude {
		global = append(global, filepath.Join(home, ".claude", "CLAUDE.md"))
		names = append(names, "CLAUDE.md")
	}
	names = append(names, "CONTEXT.md")
	for _, path := range global {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			add(path)
			break
		}
	}
	for _, name := range names {
		found := false
		for _, dir := range ancestors {
			path := filepath.Join(dir, name)
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				add(path)
				found = true
			}
		}
		if found {
			break
		}
	}
	for _, raw := range configured {
		if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
			add(raw)
			continue
		}
		if strings.HasPrefix(raw, "~/") {
			raw = filepath.Join(home, raw[2:])
		}
		patterns := []string{raw}
		if !filepath.IsAbs(raw) {
			patterns = nil
			for _, dir := range ancestors {
				patterns = append(patterns, filepath.Join(dir, raw))
			}
		}
		for _, pattern := range patterns {
			options := []doublestar.GlobOption{doublestar.WithFilesOnly(), doublestar.WithNoFollow()}
			if runtime.GOOS == "windows" {
				options = append(options, doublestar.WithCaseInsensitive())
			}
			matches, err := doublestar.FilepathGlob(pattern, options...)
			if err != nil {
				return nil, errors.New("Cannot resolve native OpenCode instruction paths for managed execution.")
			}
			for _, path := range matches {
				if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
					add(path)
				}
			}
		}
	}
	return result, nil
}

// Native permission pattern order is significant. Preserve it through JSONC
// projection instead of sorting maps and accidentally changing deny/allow order.
type openCodeOrderedField struct {
	key   string
	value any
}
type openCodeOrderedObject []openCodeOrderedField

func (o openCodeOrderedObject) get(key string) (any, bool) {
	for i := len(o) - 1; i >= 0; i-- {
		field := o[i]
		if field.key == key {
			return field.value, true
		}
	}
	return nil, false
}
func (o openCodeOrderedObject) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, field := range o {
		if i > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(field.key)
		value, err := json.Marshal(field.value)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}
func readOpenCodeOrderedJSON(data []byte) (openCodeOrderedObject, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var read func() (any, error)
	read = func() (any, error) {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch token {
		case json.Delim('{'):
			object := openCodeOrderedObject{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				value, err := read()
				if err != nil {
					return nil, err
				}
				object = append(object, openCodeOrderedField{key.(string), value})
			}
			_, err := decoder.Token()
			return object, err
		case json.Delim('['):
			array := []any{}
			for decoder.More() {
				value, err := read()
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			_, err := decoder.Token()
			return array, err
		default:
			return token, nil
		}
	}
	value, err := read()
	if err != nil {
		return nil, err
	}
	object, ok := value.(openCodeOrderedObject)
	if !ok {
		return nil, errors.New("Expected an OpenCode configuration object.")
	}
	return object, nil
}
func permissionObject(value any) any {
	if action, ok := value.(string); ok {
		return openCodeOrderedObject{{"*", action}}
	}
	return value
}
func mergeOpenCodeOrdered(left, right any) any {
	if right == nil {
		return left
	}
	l, lok := left.(openCodeOrderedObject)
	r, rok := right.(openCodeOrderedObject)
	if !lok || !rok {
		return right
	}
	result := append(openCodeOrderedObject{}, l...)
	for _, field := range r {
		found := false
		for i := range result {
			if result[i].key == field.key {
				result[i].value = mergeOpenCodeOrdered(result[i].value, field.value)
				found = true
				break
			}
		}
		if !found {
			result = append(result, field)
		}
	}
	return result
}

func openCodeSkillDenied(value any, name string) bool {
	policy, _ := permissionObject(value).(openCodeOrderedObject)
	action := ""
	match := func(value, pattern string) bool {
		expression := regexp.QuoteMeta(pattern)
		expression = strings.ReplaceAll(expression, `\*`, ".*")
		expression = strings.ReplaceAll(expression, `\?`, ".")
		matched, _ := regexp.MatchString("^"+expression+"$", value)
		return matched
	}
	for _, field := range policy {
		if !match("skill", field.key) {
			continue
		}
		if direct, ok := field.value.(string); ok {
			action = direct
			continue
		}
		if patterns, ok := field.value.(openCodeOrderedObject); ok {
			for _, pattern := range patterns {
				if match(name, pattern.key) {
					action, _ = pattern.value.(string)
				}
			}
		}
	}
	return action == "deny"
}

func mergeOpenCodeProviderMaps(left, right map[string]any) map[string]any {
	if left == nil {
		left = map[string]any{}
	}
	for key, value := range right {
		if nested, ok := value.(map[string]any); ok {
			left[key] = mergeOpenCodeProviderMaps(object(left[key]), nested)
		} else {
			left[key] = value
		}
	}
	return left
}

var openCodeFileReference = regexp.MustCompile(`\{file:([^}]+)\}`)
var openCodeEnvReference = regexp.MustCompile(`\{env:([^}]+)\}`)

func rewriteOpenCodeFileReferences(value any, dir string) any {
	switch v := value.(type) {
	case string:
		return openCodeFileReference.ReplaceAllStringFunc(v, func(match string) string {
			path := openCodeFileReference.FindStringSubmatch(match)[1]
			if filepath.IsAbs(path) || strings.HasPrefix(path, "~") {
				return match
			}
			return "{file:" + filepath.ToSlash(filepath.Join(dir, path)) + "}"
		})
	case map[string]any:
		for key, item := range v {
			v[key] = rewriteOpenCodeFileReferences(item, dir)
		}
	case []any:
		for i, item := range v {
			v[i] = rewriteOpenCodeFileReferences(item, dir)
		}
	}
	return value
}

// Remote config-bearing authentication and system policy are not silently
// overridden. The ordinary provider auth file and native history stay in place.
func inspectOpenCodeLibrarySources(ctx context.Context) error {
	dataRoot := filepath.Join(configHome("XDG_DATA_HOME", ".local", "share"), "opencode")
	authPath := filepath.Join(dataRoot, "auth.json")
	if data, err := os.ReadFile(authPath); err == nil {
		var auth map[string]struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &auth) != nil {
			return errors.New("Cannot inspect OpenCode authentication source types.")
		}
		for _, entry := range auth {
			if entry.Type == "wellknown" {
				return errors.New("Managed OpenCode Library does not support remote-config-bearing well-known authentication; use an explicit provider configuration.")
			}
		}
	} else if !os.IsNotExist(err) {
		return errors.New("Cannot inspect OpenCode authentication sources.")
	}
	managed := os.Getenv("ProgramData")
	if managed == "" {
		managed = `C:\ProgramData`
	}
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		if _, err := os.Stat(filepath.Join(managed, "opencode", name)); err == nil {
			return errors.New("Managed OpenCode Library cannot replace administrator-managed OpenCode configuration.")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	db := os.Getenv("OPENCODE_DB")
	if db == "" {
		db = filepath.Join(dataRoot, "opencode.db")
	}
	if !filepath.IsAbs(db) {
		return errors.New("Managed OpenCode requires an absolute native database path.")
	}
	if _, err := os.Stat(db); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	node, ok := executable("node")
	if !ok {
		return errors.New("Managed OpenCode requires Node with node:sqlite to inspect configuration-bearing account metadata.")
	}
	const script = `const {DatabaseSync}=require('node:sqlite');const db=new DatabaseSync(process.argv[1],{readOnly:true});const tables=db.prepare("SELECT name FROM sqlite_master WHERE type='table' AND name='account_state'").all();const row=tables.length?db.prepare('SELECT active_org_id FROM account_state WHERE active_org_id IS NOT NULL LIMIT 1').get():null;db.close();process.stdout.write(row?'remote':'local');`
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(check, node, "-e", script, db)
	configureProcess(cmd)
	output, err := cmd.Output()
	if err != nil {
		return errors.New("Cannot safely inspect OpenCode organization configuration metadata (Node 22+ required).")
	}
	if string(output) != "local" {
		return errors.New("Managed OpenCode Library does not support active organization-supplied configuration on this version.")
	}
	return nil
}

func (p *adapter) configureOpenCodeLibrary(config map[string]any, dir string) ([]string, error) {
	projection, err := openCodeLibraryProjection(p.cwd)
	if err != nil {
		return nil, err
	}
	if err := inspectOpenCodeLibrarySources(p.turn.ctx); err != nil {
		return nil, err
	}
	for key := range config {
		delete(config, key)
	}
	for key, value := range projection {
		if !strings.HasPrefix(key, "_klm_") {
			config[key] = value
		}
	}
	paths := []string{}
	rootSkills, childSkills := map[string]any{"*": "deny"}, map[string]any{"*": "deny"}
	for _, skill := range p.capabilities.Skills {
		if !p.yolo && openCodeSkillDenied(mergeOpenCodeOrdered(permissionObject(projection["permission"]), permissionObject(projection["_klm_root_permission"])), skill.Name) {
			return nil, fmt.Errorf("Native OpenCode permission denies selected skill %s.", skill.Name)
		}
		paths = append(paths, p.app.skillSnapshotPath(skill))
		rootSkills[skill.Name] = "allow"
		if !skill.Internal {
			childSkills[skill.Name] = "allow"
		}
	}
	config["skills"] = map[string]any{"paths": paths, "urls": []string{}}
	config["permission"] = mergeOpenCodeOrdered(permissionObject(config["permission"]), openCodeOrderedObject{{"skill", childSkills}, {"klm_linked_*", "deny"}})
	p.openCodeRootAgent = str(projection, "_klm_root_agent")
	rootAgent := p.openCodeManagedAgent()
	config["default_agent"] = rootAgent
	rootPermission := mergeOpenCodeOrdered(permissionObject(projection["_klm_root_permission"]), openCodeOrderedObject{{"skill", rootSkills}, {"klm_linked_*", "allow"}})
	config["agent"] = map[string]any{rootAgent: map[string]any{"mode": "primary", "permission": rootPermission}}
	if tools := projection["_klm_root_tools"]; tools != nil {
		object(object(config["agent"])[rootAgent])["tools"] = tools
	}
	if variant := str(projection, "_klm_variant"); variant != "" {
		object(object(config["agent"])[rootAgent])["variant"] = variant
	}
	config["mcp"] = map[string]any{}
	// Prevent native startup dependency installation. Our .mjs plugin imports no
	// npm SDK: the runtime passes its already-bundled client through PluginInput.
	profile := filepath.Join(dir, "config", "opencode")
	if err := os.MkdirAll(filepath.Join(profile, "node_modules"), 0700); err != nil {
		return nil, err
	}
	manifest := []byte(`{"private":true}`)
	lock := []byte(`{"lockfileVersion":3,"packages":{"":{"dependencies":{"@opencode-ai/plugin":"1.18.35"}}}}`)
	for name, data := range map[string][]byte{"package.json": manifest, "package-lock.json": lock} {
		if err := os.WriteFile(filepath.Join(profile, name), data, 0600); err != nil {
			return nil, err
		}
	}
	env := []string{"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "OPENCODE_CONFIG_DIR=" + profile, "OPENCODE_TEST_HOME=" + filepath.Join(dir, "discovery-home"), "OPENCODE_DISABLE_PROJECT_CONFIG=true", "OPENCODE_DISABLE_EXTERNAL_SKILLS=true", "OPENCODE_DISABLE_AUTOUPDATE=true", "OPENCODE_PURE=false"}
	for _, key := range []string{"OPENCODE_DB", "OPENCODE_MODELS_PATH", "OPENCODE_MODELS_URL", "OPENCODE_DISABLE_MODELS_FETCH"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env, nil
}

func (p *adapter) verifyOpenCodeLibrary(h *openCodeHTTP) error {
	var cfg map[string]any
	if err := h.json(p.turn.ctx, "GET", "/config", nil, &cfg); err != nil {
		return err
	}
	if len(object(cfg["mcp"])) != 0 {
		return errors.New("OpenCode imported an unmanaged native MCP configuration.")
	}
	var skills []struct {
		Name     string `json:"name"`
		Location string `json:"location"`
	}
	if err := h.json(p.turn.ctx, "GET", "/skill", nil, &skills); err != nil {
		return err
	}
	expected := map[string]string{}
	for _, skill := range p.capabilities.Skills {
		expected[skill.Name] = filepath.Join(p.app.skillSnapshotPath(skill), "SKILL.md")
	}
	for _, skill := range skills {
		if skill.Name == "customize-opencode" && skill.Location == "<built-in>" {
			continue
		}
		path, ok := expected[skill.Name]
		if !ok || !piSamePath(skill.Location, path) {
			return fmt.Errorf("OpenCode loaded an unselected skill (%s).", skill.Name)
		}
		delete(expected, skill.Name)
	}
	if len(expected) != 0 {
		return errors.New("OpenCode failed to load every captured Library skill.")
	}
	var agents []struct {
		Name       string `json:"name"`
		Permission []struct {
			Permission string `json:"permission"`
			Pattern    string `json:"pattern"`
			Action     string `json:"action"`
		} `json:"permission"`
	}
	if err := h.json(p.turn.ctx, "GET", "/agent", nil, &agents); err != nil {
		return err
	}
	rootFound := false
	for _, agent := range agents {
		if agent.Name == p.openCodeManagedAgent() {
			rootFound = true
		}
		linkedAction := "ask"
		for _, rule := range agent.Permission {
			if (rule.Permission == "*" || rule.Permission == "klm_linked_*" || rule.Permission == "klm_linked_linked_discover") && rule.Pattern == "*" {
				linkedAction = rule.Action
			}
		}
		if (linkedAction == "allow") != (agent.Name == p.openCodeManagedAgent()) {
			return errors.New("OpenCode delegated conversation-role tools to an unexpected native agent.")
		}
		for _, skill := range skills {
			expected := false
			for _, selected := range p.capabilities.Skills {
				if selected.Name == skill.Name && (!selected.Internal || agent.Name == p.openCodeManagedAgent()) {
					expected = true
				}
			}
			action := "ask"
			for _, rule := range agent.Permission {
				if (rule.Permission == "*" || rule.Permission == "skill") && (rule.Pattern == "*" || rule.Pattern == skill.Name) {
					action = rule.Action
				}
			}
			if (action == "allow") != expected {
				return errors.New("OpenCode effective skill permissions do not match the captured Library selection.")
			}
		}
	}
	if !rootFound {
		return errors.New("OpenCode did not initialize the managed root agent.")
	}
	return nil
}
