package main

import (
	"context"
	"database/sql"
	"errors"
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
	if a.store != nil {
		var err error
		saved, err = a.readSQLOpenCodeSettings(r.Context())
		if err != nil {
			sqlHTTPError(w, err)
			return
		}
	}
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
	var err error
	if a.store != nil {
		err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
			return st.putRoot("opencode_settings", "settings", body)
		})
	} else {
		err = a.commitLocked(func(d *diskState) { d.OpenCode = body })
	}
	a.mu.Unlock()
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	a.getOpenCodeSettings(w, r)
}

func (a *app) readSQLOpenCodeSettings(ctx context.Context) (OpenCodeSettings, error) {
	var settings OpenCodeSettings
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		err := decodeSQLiteRecord(ctx, tx, "opencode_settings", "settings", 64<<10, &settings)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return settings.normalized(), err
}

func resolveOpenCodeBinary(settings OpenCodeSettings, n nodeInstallation) (binary, string) {
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
	if path != "" {
		resolved, _ := executable(path)
		return binary{path: resolved, node: n.Node, openCodeV1: settings.Version == "v1"}, "override"
	}
	recipe, _ := harnessRecipeFor("opencode")
	if settings.Version == "v2" {
		recipe.Package = "@opencode/cli"
	}
	for _, root := range globalRoots(n) {
		if b, ok := packageBinary(recipe, root, n); ok {
			b.openCodeV1 = settings.Version == "v1"
			return b, "global npm"
		}
	}
	resolved, _ := executable("opencode")
	return binary{path: resolved, node: n.Node, openCodeV1: settings.Version == "v1"}, "PATH"
}

func validateOpenCodeDiscoveryVersion(version string, v1 bool) bool {
	major := "2."
	if v1 {
		major = "1."
	}
	return strings.HasPrefix(version, major)
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
