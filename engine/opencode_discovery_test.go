package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Discovery fixtures are files only; no installed harness or engine is started.
func TestOpenCodeDiscoverySelection(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", "")
	t.Setenv("APPDATA", root)
	for _, key := range []string{"KLM_OPENCODE_BIN", "KLM_OPENCODE_V1_BIN", "KLM_OPENCODE_V2_BIN"} {
		t.Setenv(key, "")
	}
	n := nodeInstallation{Root: filepath.Join(root, "node_modules"), Node: "fixture-node"}
	paths := map[string]string{}
	for version, name := range map[string]string{"v1": "opencode-ai", "v2": "@opencode/cli"} {
		dir := filepath.Join(n.Root, filepath.FromSlash(name))
		path := filepath.Join(dir, "bin", "opencode.exe")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		metadata, _ := json.Marshal(map[string]string{"name": name, "version": strings.TrimPrefix(version, "v") + ".0.0"})
		if err := os.WriteFile(filepath.Join(dir, "package.json"), metadata, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("not executed"), 0700); err != nil {
			t.Fatal(err)
		}
		paths[version] = canonicalFile(path)
	}
	for _, version := range []string{"", "v1", "v2"} {
		settings := OpenCodeSettings{Version: version}.normalized()
		b, source := resolveOpenCodeBinary(settings, n)
		if b.path != paths[settings.Version] || source != "global npm" || b.openCodeV1 != (settings.Version == "v1") || b.node != n.Node {
			t.Fatalf("%q selected wrong package/protocol: %+v %s", version, b, source)
		}
	}
	t.Setenv("KLM_OPENCODE_BIN", paths["v1"])
	t.Setenv("KLM_OPENCODE_V2_BIN", paths["v2"])
	for _, tc := range []struct {
		name     string
		settings OpenCodeSettings
		want     string
	}{
		{"version-env", OpenCodeSettings{}, paths["v2"]},
		{"saved-path", OpenCodeSettings{V2Path: paths["v1"]}, paths["v1"]},
		{"invalid-saved-path", OpenCodeSettings{V2Path: filepath.Join(root, "missing.exe")}, ""},
		{"generic-env", OpenCodeSettings{Version: "v1"}, paths["v1"]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, source := resolveOpenCodeBinary(tc.settings, n)
			if b.path != tc.want || source != "override" {
				t.Fatalf("override was ignored or fell back: %+v %s", b, source)
			}
		})
	}
	for _, tc := range []struct {
		version string
		v1, ok  bool
	}{
		{"1.18.35", true, true}, {"2.0.0", false, true},
		{"1.18.35", false, false}, {"2.0.0", true, false},
		{"", false, false}, {"3.0.0", false, false},
	} {
		if got := validateOpenCodeDiscoveryVersion(tc.version, tc.v1); got != tc.ok {
			t.Fatalf("version %q v1=%v: accepted=%v", tc.version, tc.v1, got)
		}
	}
}

func TestOpenCodeInstallPlans(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KLM_OPENCODE_BIN", "")
	n := nodeInstallation{Root: filepath.Join(root, "node_modules"), Prefix: root}
	for _, tc := range []struct {
		name     string
		settings OpenCodeSettings
		harness  Harness
		blocked  bool
		noop     bool
	}{
		{"default-v2", OpenCodeSettings{}, Harness{Available: true, Compatible: true, InstalledVersion: "2.0.0"}, true, false},
		{"install-v1", OpenCodeSettings{Version: "v1"}, Harness{}, false, false},
		{"installed-v1", OpenCodeSettings{Version: "v1"}, Harness{Available: true, Compatible: true, InstalledVersion: "1.18.35"}, false, true},
		{"broken-v1-override", OpenCodeSettings{Version: "v1"}, Harness{Source: "override"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.harness.ID = "opencode"
			plans := buildHarnessInstallPlans(n, []Harness{tc.harness}, "", tc.settings)
			if len(plans) != 1 || plans[0].Package != "opencode-ai" || (plans[0].Blocked != "") != tc.blocked || plans[0].Noop != tc.noop || plans[0].Token == "" {
				t.Fatalf("incorrect protocol/override consent plan: %+v", plans)
			}
		})
	}
}
