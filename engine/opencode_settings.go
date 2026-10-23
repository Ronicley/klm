package main

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// A version selects a protocol, not an installer. Binaries live on the engine host.
type OpenCodeSettings struct {
	Version string `json:"version"`
	V1Path  string `json:"v1Path"`
	V2Path  string `json:"v2Path"`
}

func (s OpenCodeSettings) normalized() OpenCodeSettings {
	if s.Version == "" {
		s.Version = "v2"
	}
	return s
}

func (a *app) getOpenCodeSettings(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	saved, active := a.state.OpenCode.normalized(), a.openCodeSettings.normalized()
	a.mu.Unlock()
	respond(w, 200, map[string]any{"settings": saved, "activeVersion": active.Version, "restartRequired": saved != active})
}

func (a *app) updateOpenCodeSettings(w http.ResponseWriter, r *http.Request) {
	var body OpenCodeSettings
	if !decode(w, r, &body) {
		return
	}
	if body.Version != "v1" && body.Version != "v2" {
		fail(w, 400, "Choose OpenCode V1 or V2.")
		return
	}
	body.V1Path, body.V2Path = strings.TrimSpace(body.V1Path), strings.TrimSpace(body.V2Path)
	for _, path := range []string{body.V1Path, body.V2Path} {
		if path == "" {
			continue
		}
		if len(path) > 4096 || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
			fail(w, 400, "Executable paths must be absolute paths on the engine computer.")
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe")) {
			fail(w, 400, "Choose an existing executable on the engine computer (.exe on Windows).")
			return
		}
	}
	a.mu.Lock()
	err := a.commitLocked(func(d *diskState) { d.OpenCode = body })
	a.mu.Unlock()
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	a.getOpenCodeSettings(w, r)
}

func configureOpenCodeHarness(settings OpenCodeSettings, harnesses []Harness, binaries map[string]binary) {
	settings = settings.normalized()
	path := settings.V2Path
	if settings.Version == "v1" {
		path = settings.V1Path
	}
	if path == "" {
		path = os.Getenv("KLM_OPENCODE_" + strings.ToUpper(settings.Version) + "_BIN")
	}
	if path == "" {
		path = os.Getenv("KLM_OPENCODE_BIN")
	}
	b, available := binaries["opencode"]
	if path != "" {
		resolved, ok := executable(path)
		b, available = binary{path: resolved}, ok
	} else if settings.Version == "v1" {
		// Prefer the V1 npm package over the separately installed @opencode/cli V2.
		candidates := []string{}
		if root := os.Getenv("APPDATA"); root != "" {
			candidates = append(candidates, filepath.Join(root, "npm", "node_modules", "opencode-ai", "node_modules", "opencode-windows-x64", "bin", "opencode.exe"))
		}
		candidates = append(candidates, "opencode")
		available = false
		for _, candidate := range candidates {
			if resolved, ok := executable(candidate); ok {
				b, available = binary{path: resolved}, true
				break
			}
		}
	}
	delete(binaries, "opencode")
	if available {
		b.openCodeV1 = settings.Version == "v1"
		binaries["opencode"] = b
	}
	for i := range harnesses {
		if harnesses[i].ID != "opencode" {
			continue
		}
		harnesses[i].Available, harnesses[i].Error = available, ""
		if !available {
			harnesses[i].Error = "OpenCode executable not found. Configure its path in Settings > OpenCode and restart the engine."
		}
	}
}

func openCodeServerPrefix(v1 bool) string {
	if v1 {
		return "opencode server listening on "
	}
	return "server listening on "
}

func openCodeServerEnv(v1 bool, password string) []string {
	if v1 {
		return []string{"OPENCODE_SERVER_USERNAME=opencode", "OPENCODE_SERVER_PASSWORD=" + password, "OPENCODE_ENABLE_QUESTION_TOOL=true"}
	}
	return []string{"OPENCODE_PASSWORD=" + password}
}
