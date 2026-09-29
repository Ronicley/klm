package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

type permissionFacts struct {
	operation string
	key       string
	label     string
	decision  string
	dangerous bool
}

// Classification is deliberately bounded. Shell recognition is not a sandbox;
// an unknown command never acquires a file permission through textual guessing.
var destructiveCommand = regexp.MustCompile(`(?i)(^|[\s;&|("'])(rm|rmdir|rd|del|erase|remove-item|ri|unlink)([\s;|&"']|$)|\bgit\s+(clean|reset\s+--hard)\b|\b(format-volume|clear-disk)\b`)

func permissionHash(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// Resolve existing ancestors as well as the leaf, so new files and junctions
// are checked against the real workspace. This is a preflight, not OS isolation.
func permissionPath(cwd, path string) (string, bool) {
	if path == "" || strings.ContainsAny(path, "\x00*?[]$`\r\n") || strings.HasPrefix(path, "~") {
		return "", false
	}
	if !filepath.IsAbs(path) {
		if !filepath.IsAbs(cwd) || filepath.VolumeName(path) != "" || strings.HasPrefix(path, `\`) || strings.HasPrefix(path, "/") {
			return "", false
		}
		path = filepath.Join(cwd, path)
	}
	if runtime.GOOS == "windows" && (strings.HasPrefix(path, `\\`) || strings.Contains(strings.TrimPrefix(path, filepath.VolumeName(path)), ":")) {
		return "", false
	}
	path = filepath.Clean(path)
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			if runtime.GOOS == "windows" {
				resolved = strings.ToLower(resolved)
			}
			return resolved, true
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(path) == path {
			return "", false
		}
		// A dangling symlink must not be mistaken for a not-yet-created file.
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", false
		}
		tail = append(tail, filepath.Base(path))
		path = filepath.Dir(path)
	}
}

func permissionWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func npmReadRoot(cwd, path string) string {
	// Cache/global package roots are local resources shared between projects.
	roots := []string{os.Getenv("npm_config_cache"), os.Getenv("NPM_CONFIG_CACHE")}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		roots = append(roots, filepath.Join(local, "npm-cache"))
	}
	if roaming := os.Getenv("APPDATA"); roaming != "" {
		roots = append(roots, filepath.Join(roaming, "npm", "node_modules"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".npm"))
	}
	// Only this execution's dependencies, not an arbitrary directory named npm.
	if cwd != "" {
		roots = append(roots, filepath.Join(cwd, "node_modules"))
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		resolved, ok := permissionPath("", root)
		if ok && permissionWithin(resolved, path) {
			return resolved
		}
	}
	return ""
}

// Skills are also loaded through ordinary read tools (notably by Pi). Permit
// reading their installed resources, never writes or commands merely located
// inside one of these directories.
func skillReadPath(cwd, path string) bool {
	roots := []string{}
	for _, relative := range []string{".agents/skills", ".claude/skills", ".opencode/skills", ".opencode/skill", ".pi/skills"} {
		if filepath.IsAbs(cwd) {
			roots = append(roots, filepath.Join(cwd, filepath.FromSlash(relative)))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, relative := range []string{".agents/skills", ".claude/skills", ".codex/skills", ".config/opencode/skills", ".config/opencode/skill", ".pi/agent/skills"} {
			roots = append(roots, filepath.Join(home, filepath.FromSlash(relative)))
		}
	}
	for _, root := range roots {
		if resolved, ok := permissionPath("", root); ok && permissionWithin(resolved, path) {
			return true
		}
	}
	return false
}

func permissionFactsFor(req Permission, harness, cwd string) permissionFacts {
	f := permissionFacts{dangerous: destructiveCommand.MatchString(req.Command)}
	if (harness == "opencode" || harness == "pi") && (req.Kind == "skill" || str(req.Details, "toolName") == "skill") ||
		harness == "opencode" && req.mcpTool || harness == "codex" && req.Kind == "mcp" {
		f.decision = "allow"
		return f
	}
	if req.Command != "" {
		tool := str(req.Details, "toolName")
		if tool != "" && tool != "bash" && tool != "shell" {
			return f
		}
		workdir := req.Path
		if workdir == "" {
			workdir = cwd
		}
		root, rootOK := permissionPath("", cwd)
		path, pathOK := permissionPath(cwd, workdir)
		if targets, known := deletionTargets(req.Command); known && rootOK && pathOK {
			resources := []string{}
			outside := false
			for _, target := range targets {
				resolved, ok := permissionPath(path, target)
				if !ok {
					return f
				}
				resources = append(resources, resolved)
				outside = outside || !permissionWithin(root, resolved)
			}
			slices.Sort(resources)
			f.operation, f.dangerous = "delete", true
			f.label = "delete: " + strings.Join(resources, ", ")
			f.key = permissionHash(map[string]any{"operation": "delete", "resources": resources, "command": req.Command,
				"additionalPermissions": req.Details["additionalPermissions"], "networkApprovalContext": req.Details["networkApprovalContext"]})
			if outside {
				f.decision = "deny"
			}
			return f
		}
		// No text substitutions in the command: quoted strings and extra arguments
		// remain part of the exact scope, including additional native permissions.
		if rootOK && pathOK {
			resource := path
			if permissionWithin(root, path) {
				rel, _ := filepath.Rel(root, path)
				resource = "<workspace>/" + filepath.ToSlash(rel)
			}
			f.operation, f.label = "command", "Run this exact command in "+resource
			f.key = permissionHash(map[string]any{"command": req.Command, "cwd": resource, "harness": harness,
				"additionalPermissions": req.Details["additionalPermissions"], "networkApprovalContext": req.Details["networkApprovalContext"], "environmentId": req.Details["environmentId"]})
			if resource == "<workspace>/." && req.Details["additionalPermissions"] == nil && req.Details["networkApprovalContext"] == nil && req.Details["environmentId"] == nil {
				switch strings.TrimSpace(req.Command) {
				case "pwd", "Get-Location", "git status", "git status --short", "git diff --stat", "git branch --show-current":
					f.decision = "allow"
				}
			}
		}
		return f
	}
	var paths []string
	if harness == "pi" {
		input := object(req.Details["input"])
		switch str(req.Details, "toolName") {
		case "read", "ls", "find", "grep":
			f.operation = "read"
		case "write", "edit":
			f.operation = "write"
		}
		if f.operation != "" {
			path := str(input, "path")
			if path == "" && slices.Contains([]string{"ls", "find", "grep"}, str(req.Details, "toolName")) {
				path = "."
			}
			paths = []string{path}
		}
	} else if harness == "opencode" {
		input := object(req.Details["input"])
		tool := str(req.Details, "toolName")
		if input != nil {
			switch tool {
			case "read", "glob", "grep", "list":
				f.operation = "read"
			case "write", "edit":
				f.operation = "write"
			case "apply_patch":
				var valid bool
				paths, f.dangerous, valid = patchPermissionPaths(str(input, "patchText"))
				if !valid {
					return f
				}
				f.operation = "write"
			case "webfetch":
				f.operation, f.label = "http", "Fetch: "+str(input, "url")
				f.key = permissionHash(map[string]any{"tool": "webfetch", "input": input})
				return f
			}
			if f.operation != "" && len(paths) == 0 {
				path := str(input, "filePath")
				if path == "" {
					path = str(input, "path")
				}
				if path == "" && slices.Contains([]string{"glob", "grep", "list"}, tool) {
					path = "."
				}
				paths = []string{path}
			}
		} else {
			switch req.Kind {
			case "read":
				f.operation, paths = "read", req.Patterns
			case "edit":
				f.operation, paths = "write", req.Patterns
			}
		}
	} else if harness == "codex" && req.Kind == "file" {
		changes, _ := req.Details["changes"].([]any)
		if len(changes) > 0 {
			f.operation = "write"
			for _, value := range changes {
				change := object(value)
				paths = append(paths, str(change, "path"))
				kind := object(change["kind"])
				if str(kind, "type") == "delete" {
					f.dangerous = true
				}
				if destination := str(kind, "move_path"); destination != "" {
					paths = append(paths, destination)
				}
			}
		}
	}
	if f.operation == "" || len(paths) == 0 {
		return f
	}
	root, ok := permissionPath("", cwd)
	if !ok {
		return f
	}
	allInside, allReadAllowed, outside, resources := true, true, false, []string{}
	for _, path := range paths {
		resolved, ok := permissionPath(cwd, path)
		if !ok {
			return permissionFacts{dangerous: f.dangerous}
		}
		inside := permissionWithin(root, resolved)
		allInside, outside = allInside && inside, outside || !inside
		allReadAllowed = allReadAllowed && f.operation == "read" && (inside || skillReadPath(cwd, resolved))
		resource := resolved
		if f.operation == "read" {
			if npm := npmReadRoot(cwd, resolved); npm != "" {
				resource = npm + string(filepath.Separator) + "**"
			}
		}
		resources = append(resources, resource)
	}
	slices.Sort(resources)
	resources = slices.Compact(resources)
	if f.dangerous {
		f.operation = "delete"
	}
	f.label = f.operation + ": " + strings.Join(resources, ", ")
	f.key = permissionHash(map[string]any{"operation": f.operation, "resources": resources})
	if f.dangerous {
		f.label = "File changes including deletion: " + strings.Join(resources, ", ")
		f.key = permissionHash(map[string]any{"operation": f.operation, "resources": resources,
			"changes": req.Details["changes"], "patch": str(object(req.Details["input"]), "patchText")})
	}
	if outside && f.operation != "read" {
		f.decision = "deny"
	} else if (allInside || allReadAllowed) && !f.dangerous {
		f.decision = "allow"
	}
	return f
}

func patchPermissionPaths(patch string) ([]string, bool, bool) {
	lines := strings.Split(strings.TrimSpace(patch), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "*** Begin Patch" || strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, false, false
	}
	var paths []string
	dangerous := false
	for _, line := range lines[1 : len(lines)-1] {
		line = strings.TrimSuffix(line, "\r")
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if path, ok := strings.CutPrefix(line, prefix); ok {
				if strings.TrimSpace(path) == "" {
					return nil, false, false
				}
				paths = append(paths, strings.TrimSpace(path))
				dangerous = dangerous || prefix == "*** Delete File: "
			}
		}
	}
	return paths, dangerous, len(paths) > 0
}

func deletionTargets(command string) ([]string, bool) {
	// Only a single literal command. Expansions, pipes, scripts, escapes and
	// command chaining retain a human decision rather than guessed targets.
	if strings.ContainsAny(command, ";&|><$`\n\r(){}*,?[]") {
		return nil, false
	}
	var words []string
	var word strings.Builder
	var quote rune
	for _, ch := range command {
		if ch == '\'' || ch == '"' {
			if quote == 0 {
				quote = ch
			} else if quote == ch {
				quote = 0
			} else {
				word.WriteRune(ch)
			}
		} else if (ch == ' ' || ch == '\t') && quote == 0 {
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
		} else {
			word.WriteRune(ch)
		}
	}
	if quote != 0 {
		return nil, false
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	if len(words) < 2 {
		return nil, false
	}
	name := strings.ToLower(words[0])
	if !slices.Contains([]string{"rm", "rmdir", "del", "erase", "rd", "remove-item", "ri", "unlink"}, name) {
		return nil, false
	}
	var targets []string
	for _, arg := range words[1:] {
		if strings.HasPrefix(arg, "-") || ((name == "del" || name == "rd" || name == "erase") && strings.HasPrefix(arg, "/")) {
			switch strings.ToLower(arg) {
			case "-r", "-f", "-rf", "-fr", "-d", "--recursive", "--force", "--", "-recurse", "-force", "-literalpath", "-path", "/s", "/q":
				continue
			default:
				return nil, false
			}
		}
		targets = append(targets, arg)
	}
	return targets, len(targets) > 0
}

func rememberedPermission(grants []permissionGrant, s *Session, harness, kind, key string) string {
	if key == "" {
		return ""
	}
	decision := ""
	for _, grant := range grants {
		if grant.Key != key || grant.Kind != kind || grant.Harness != harness ||
			grant.ProjectID != "" && grant.ProjectID != s.ProjectID || grant.SessionID != "" && grant.SessionID != s.ID {
			continue
		}
		if grant.Decision == "deny" {
			return "deny"
		}
		decision = "allow"
	}
	return decision
}

func (p *adapter) replyAutomaticPermission(req Permission, decision, reason string, reply func(bool) error) error {
	if err := reply(decision == "allow"); err != nil {
		return err
	}
	a := p.app
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.commitLocked(func(d *diskState) {
		s := d.session(p.id)
		entry := event("status", "Automatic permission decision: "+decision+" ("+reason+")")
		entry.Title, entry.Status, entry.ConsultationID = req.Title, "completed", p.turn.consultationID
		entry.Data = map[string]any{"permission": req.Kind, "decision": decision, "automatic": true, "scope": req.ScopeLabel, "reason": reason}
		s.Events = append(s.Events, entry)
		s.UpdatedAt = now()
	})
}

func (a *app) updatePermissionSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		YOLO *bool `json:"yolo"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.YOLO == nil {
		fail(w, 400, "Provide yolo.")
		return
	}
	a.mu.Lock()
	id := r.PathValue("id")
	s := a.state.session(id)
	if s == nil {
		a.mu.Unlock()
		fail(w, 404, "Session not found.")
		return
	}
	if a.runs[id] != nil {
		a.mu.Unlock()
		fail(w, 409, "Stop or finish the current turn before changing YOLO mode.")
		return
	}
	if err := a.commitLocked(func(d *diskState) {
		s := d.session(id)
		s.YOLO, s.UpdatedAt = *body.YOLO, now()
	}); err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	response := a.currentSessionUpdateLocked(id)
	a.mu.Unlock()
	respond(w, 200, response)
}

func (a *app) permissionRules(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	response := slices.Clone(a.state.Grants)
	a.mu.Unlock()
	respond(w, 200, response)
}

// Apply new project/global decisions to cards already waiting elsewhere too.
// Deliver callbacks outside app.mu; inactive turns and in-flight replies are skipped.
func (a *app) resolveRememberedPermissions() {
	type resolution struct {
		sessionID, requestID, decision string
		turn                           *turn
		pending                        *pendingApproval
	}
	var ready []resolution
	a.mu.Lock()
	for sessionID, t := range a.runs {
		s := a.state.session(sessionID)
		if s == nil || t.ctx.Err() != nil || a.storageErr != nil {
			continue
		}
		for id, pending := range t.approvals {
			if pending.busy {
				continue
			}
			decision := rememberedPermission(a.state.Grants, s, pending.ruleHarness, pending.ruleKind, pending.ruleKey)
			if decision == "" || decision == "allow" && !slices.Contains(pending.request.Decisions, "once") {
				continue
			}
			pending.busy = true
			ready = append(ready, resolution{sessionID, id, decision, t, pending})
		}
	}
	a.mu.Unlock()
	for _, item := range ready {
		err := item.turn.ctx.Err()
		if err == nil {
			err = item.pending.reply(item.decision == "allow")
		}
		a.mu.Lock()
		if err != nil {
			item.pending.busy = false
		} else {
			delete(item.turn.approvals, item.requestID)
			_ = a.commitLocked(func(d *diskState) {
				s := d.session(item.sessionID)
				s.Permissions = slices.DeleteFunc(s.Permissions, func(req Permission) bool { return req.ID == item.requestID })
				entry := event("status", "Automatic permission decision: "+item.decision+" (saved rule)")
				entry.Title, entry.Status, entry.ConsultationID = item.pending.request.Title, "completed", item.turn.consultationID
				entry.Data = map[string]any{"permission": item.pending.request.Kind, "decision": item.decision, "automatic": true, "scope": item.pending.request.ScopeLabel}
				s.Events = append(s.Events, entry)
				s.UpdatedAt = now()
			})
		}
		a.mu.Unlock()
	}
}

func (a *app) deletePermissionRule(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	if err := a.commitLocked(func(d *diskState) {
		d.Grants = slices.DeleteFunc(d.Grants, func(grant permissionGrant) bool { return grant.ID == r.PathValue("ruleID") })
	}); err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	a.mu.Unlock()
	respond(w, 200, map[string]bool{"deleted": true})
}
