package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// This manifest is the only source of installation targets. Client input never
// selects a package, version, registry, executable argument or download URL.
type harnessRecipe struct{ ID, Name, Package, Version string }

var harnessRecipes = []harnessRecipe{
	{"pi", "Pi", "@earendil-works/pi-coding-agent", "0.85.1"},
	{"opencode", "OpenCode", "opencode-ai", "1.18.35"},
	{"codex", "Codex", "@openai/codex", "0.153.4"},
}

func harnessRecipeFor(id string) (harnessRecipe, bool) {
	for _, recipe := range harnessRecipes {
		if recipe.ID == id {
			return recipe, true
		}
	}
	return harnessRecipe{}, false
}

type nodeInstallation struct {
	Node       string `json:"node"`
	NPM        string `json:"npm"`
	Version    string `json:"version"`
	NPMVersion string `json:"npmVersion"`
	Prefix     string `json:"prefix"`
	Root       string `json:"root"`
	Bootstrap  bool   `json:"bootstrap"`
	Error      string `json:"error,omitempty"`
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
func canonicalFile(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	p, _ := filepath.Abs(path)
	return p
}

// PATH is supplied only to child environments. Bootstrap never changes the
// engine's process-global environment while other turns may be running.
func runtimeEnvironment(env []string, node string) []string {
	if node == "" {
		return env
	}
	result := make([]string, 0, len(env)+1)
	path := os.Getenv("PATH")
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "PATH") {
			path = value
			continue
		}
		result = append(result, entry)
	}
	return append(result, "PATH="+filepath.Dir(node)+string(os.PathListSeparator)+path)
}

func probeOutput(ctx context.Context, node, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = runtimeEnvironment(os.Environ(), node)
	cmd.Dir = os.TempDir()
	var out, diagnostic cappedBuffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	owner, err := prepareOwnedProcess(cmd)
	if err != nil {
		return "", err
	}
	defer owner.Close()
	cmd.Cancel, cmd.WaitDelay = owner.Stop, 2*time.Second
	if err = cmd.Start(); err != nil {
		return "", errors.New("Could not start executable probe.")
	}
	if err = owner.Attach(); err != nil {
		_ = owner.Stop()
		_ = cmd.Wait()
		return "", err
	}
	if err = cmd.Wait(); err != nil {
		return "", errors.New("Executable probe failed or timed out.")
	}
	if out.truncated {
		return "", errors.New("Executable probe exceeded its output limit.")
	}
	return strings.TrimSpace(out.String()), nil
}

func nodeCompatible(version string) bool {
	var major, minor, patch int
	_, err := fmt.Sscanf(strings.TrimPrefix(version, "v"), "%d.%d.%d", &major, &minor, &patch)
	return err == nil && (major > 22 || major == 22 && (minor > 19 || minor == 19 && patch >= 0))
}

func resolveNode(ctx context.Context) nodeInstallation {
	n := nodeInstallation{}
	candidates := []string{}
	if p, ok := executable("node"); ok {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, knownNodePaths()...)
	for _, path := range candidates {
		if !regularFile(path) {
			continue
		}
		n.Node = canonicalFile(path)
		n.Version, _ = probeOutput(ctx, "", n.Node, "--version")
		// Respect an existing version-manager/PATH installation, even if broken.
		break
	}
	if n.Node == "" {
		n.Bootstrap = true
		n.Node = conventionalNodePath()
		n.NPM = conventionalNPMPath()
		n.Prefix = defaultGlobalPrefix()
		if prefix, err := bootstrapGlobalPrefix(defaultGlobalPrefix()); err != nil {
			n.Error = err.Error()
		} else {
			n.Prefix = prefix
		}
	} else {
		if !nodeCompatible(n.Version) {
			n.Error = "Existing Node.js is broken or older than 22.19.0. Upgrade it using its original installer/version manager, then refresh; it will not be replaced implicitly."
			return n
		}
		cliCandidates := []string{filepath.Join(filepath.Dir(n.Node), "node_modules", "npm", "bin", "npm-cli.js"), filepath.Join(filepath.Dir(filepath.Dir(n.Node)), "lib", "node_modules", "npm", "bin", "npm-cli.js")}
		if p, err := exec.LookPath("npm"); err == nil {
			p = canonicalFile(p)
			if filepath.Base(p) == "npm-cli.js" {
				cliCandidates = append([]string{p}, cliCandidates...)
			}
			cliCandidates = append(cliCandidates, filepath.Join(filepath.Dir(p), "node_modules", "npm", "bin", "npm-cli.js"))
		}
		for _, p := range cliCandidates {
			if regularFile(p) {
				n.NPM = canonicalFile(p)
				break
			}
		}
		if n.NPM == "" {
			n.Bootstrap = true
			base := filepath.Dir(n.Node)
			n.NPM = filepath.Join(base, "node_modules", "npm", "bin", "npm-cli.js")
			if runtime.GOOS != "windows" {
				base = filepath.Dir(base)
				n.NPM = filepath.Join(base, "lib", "node_modules", "npm", "bin", "npm-cli.js")
			}
			if prefix, err := bootstrapGlobalPrefix(base); err != nil {
				n.Error = err.Error()
			} else {
				n.Prefix = prefix
			}
			if err := prerequisiteBootstrapPermission(n); err != nil {
				n.Error = err.Error()
			}
		} else {
			n.NPMVersion, _ = probeOutput(ctx, n.Node, n.Node, n.NPM, "--version")
			prefix, err := probeOutput(ctx, n.Node, n.Node, n.NPM, "prefix", "--global")
			if err != nil || n.NPMVersion == "" || !filepath.IsAbs(prefix) {
				n.Error = "Could not resolve npm's absolute global prefix. Repair the engine user's npm configuration and refresh."
				return n
			}
			n.Prefix = filepath.Clean(prefix)
		}
	}
	if !filepath.IsAbs(n.Prefix) {
		n.Error = "The npm global prefix must be an absolute directory."
	}
	if runtime.GOOS == "windows" {
		n.Root = filepath.Join(n.Prefix, "node_modules")
	} else {
		n.Root = filepath.Join(n.Prefix, "lib", "node_modules")
	}
	return n
}

func packageVersion(dir, name string) string {
	f, err := os.Open(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	defer f.Close()
	var metadata struct{ Name, Version string }
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&metadata) != nil || metadata.Name != name {
		return ""
	}
	return metadata.Version
}

// Canonical npm symlink targets are inspected as files, never as shim text.
func globalRoots(n nodeInstallation) []string {
	roots := []string{}
	if n.Root != "" {
		roots = append(roots, n.Root)
	}
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		roots = append(roots, filepath.Join(appdata, "npm", "node_modules"))
	}
	for _, id := range []string{"pi", "opencode", "codex"} {
		if path, err := exec.LookPath(id); err == nil {
			roots = append(roots, filepath.Join(filepath.Dir(path), "node_modules"))
			real := canonicalFile(path)
			for dir, depth := filepath.Dir(real), 0; depth < 8 && filepath.Dir(dir) != dir; dir, depth = filepath.Dir(dir), depth+1 {
				if filepath.Base(dir) == "node_modules" {
					roots = append(roots, dir)
					break
				}
			}
		}
	}
	return roots
}

func bootstrapGlobalPrefix(defaultPrefix string) (string, error) {
	prefix := defaultPrefix
	if v := os.Getenv("PREFIX"); v != "" {
		prefix = v
	}
	if runtime.GOOS != "windows" {
		if v := os.Getenv("DESTDIR"); v != "" {
			prefix = filepath.Join(v, prefix)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	config := filepath.Join(home, ".npmrc")
	for _, key := range []string{"NPM_CONFIG_USERCONFIG", "npm_config_userconfig"} {
		if v := os.Getenv(key); v != "" {
			config = v
		}
	}
	if !filepath.IsAbs(config) {
		return "", errors.New("npm userconfig must be an absolute path for Node bootstrap.")
	}
	if f, err := os.Open(config); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(io.LimitReader(f, 1<<20))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "prefix") {
				prefix = os.ExpandEnv(strings.Trim(strings.TrimSpace(value), "\"'"))
			}
		}
		if err := scanner.Err(); err != nil {
			return "", errors.New("Could not read npm user prefix configuration.")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("Could not read npm user configuration for bootstrap.")
	}
	for _, key := range []string{"NPM_CONFIG_PREFIX", "npm_config_prefix"} {
		if v := os.Getenv(key); v != "" {
			prefix = v
		}
	}
	if !filepath.IsAbs(prefix) {
		return "", errors.New("Configure an absolute npm global prefix before Node bootstrap.")
	}
	return filepath.Clean(prefix), nil
}

func packageBinary(recipe harnessRecipe, root string, n nodeInstallation) (binary, bool) {
	dir := filepath.Join(root, filepath.FromSlash(recipe.Package))
	if packageVersion(dir, recipe.Package) == "" {
		return binary{}, false
	}
	b := binary{node: n.Node}
	switch recipe.ID {
	case "pi":
		entry := filepath.Join(dir, "dist", "cli.js") // Adapter imports require the unbundled CLI.
		if !regularFile(entry) || !regularFile(n.Node) {
			return binary{}, false
		}
		b.path, b.args = n.Node, []string{canonicalFile(entry)}
	case "opencode":
		b.path = filepath.Join(dir, "bin", "opencode.exe") // Postinstall uses this name on Linux too.
	case "codex":
		platform, triple, exe := "linux-x64", "x86_64-unknown-linux-musl", "codex"
		if runtime.GOOS == "windows" {
			platform, triple, exe = "win32-x64", "x86_64-pc-windows-msvc", "codex.exe"
		}
		for _, base := range []string{filepath.Join(dir, "node_modules", "@openai", "codex-"+platform), filepath.Join(root, "@openai", "codex-"+platform), dir} {
			p := filepath.Join(base, "vendor", triple, "bin", exe)
			if regularFile(p) {
				b.path = p
				break
			}
		}
	}
	if p, ok := executable(b.path); ok {
		b.path = canonicalFile(p)
		return b, true
	}
	return binary{}, false
}

var harnessVersionPattern = regexp.MustCompile(`(?:^|\s)v?(\d+\.\d+\.\d+)(?:\s|$)`)

func binaryVersion(ctx context.Context, b binary) string {
	text, err := probeOutput(ctx, b.node, b.path, append(append([]string{}, b.args...), "--version")...)
	if err != nil {
		return ""
	}
	match := harnessVersionPattern.FindStringSubmatch(text)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func discoverWithNode(ctx context.Context, n nodeInstallation) ([]Harness, map[string]binary) {
	harnesses, binaries := []Harness{}, map[string]binary{}
	for _, recipe := range harnessRecipes {
		h := Harness{ID: recipe.ID, Name: recipe.Name, RequiredVersion: recipe.Version, ExecutionSupported: runtime.GOOS == "windows", ChatExecutionSupported: managedChatPlatform(), GraphExecutionSupported: runtime.GOOS == "windows"}
		if !h.ExecutionSupported {
			h.ExecutionReason = "Managed chats require working Linux amd64 subreaper/pidfd supervision. Graph execution is unavailable; protected MCP credentials remain unsupported on Linux."
		}
		var b binary
		override := os.Getenv("KLM_" + strings.ToUpper(recipe.ID) + "_BIN")
		if override != "" {
			h.Source = "override"
			if recipe.ID == "pi" && strings.EqualFold(filepath.Ext(override), ".js") && regularFile(override) && regularFile(n.Node) {
				b = binary{path: n.Node, args: []string{canonicalFile(override)}, node: n.Node}
			} else if p, ok := executable(override); ok {
				b = binary{path: canonicalFile(p), node: n.Node}
			}
		} else {
			h.Source = "global npm"
			// On Unix, honor the native PATH identity first, mapping a package's
			// canonical symlink back to its real platform/adapter-compatible entry.
			if runtime.GOOS != "windows" {
				if p, ok := executable(recipe.ID); ok {
					real := canonicalFile(p)
					for dir, depth := filepath.Dir(real), 0; depth < 8 && filepath.Dir(dir) != dir; dir, depth = filepath.Dir(dir), depth+1 {
						if filepath.Base(dir) == "node_modules" {
							if candidate, ok := packageBinary(recipe, dir, n); ok {
								b = candidate
							}
							break
						}
					}
					if b.path == "" {
						b = binary{path: real, node: n.Node}
						h.Source = "PATH"
					}
				}
			}
			if b.path == "" {
				for _, root := range globalRoots(n) {
					if candidate, ok := packageBinary(recipe, root, n); ok {
						b = candidate
						break
					}
				}
			}
			if b.path == "" && recipe.ID == "codex" && runtime.GOOS == "windows" {
				if p, ok := executable(filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "OpenAI", "Codex", "bin", "codex.exe")); ok {
					b = binary{path: p, node: n.Node}
					h.Source = "standalone"
				}
			}
			if b.path == "" {
				h.Source = "PATH"
				if p, ok := executable(recipe.ID); ok {
					p = canonicalFile(p)
					// JS entry points need an absolute Node path, including stale PATH hosts.
					if recipe.ID == "pi" && filepath.Ext(p) == ".js" && regularFile(n.Node) {
						b = binary{path: n.Node, args: []string{p}, node: n.Node}
					} else {
						b = binary{path: p, node: n.Node}
					}
				}
			}
		}
		h.Path = b.path
		if len(b.args) > 0 {
			h.Path = b.args[0]
		}
		if b.path != "" {
			h.InstalledVersion = binaryVersion(ctx, b)
			h.Available = h.InstalledVersion != ""
			h.Compatible = h.InstalledVersion == recipe.Version
		}
		if h.Available {
			binaries[recipe.ID] = b
		} else {
			h.Error = "Executable not found or version probe failed. Install the harness in Settings → Harnesses, or repair its explicit executable override."
		}
		harnesses = append(harnesses, h)
	}
	return harnesses, binaries
}

func discoverHarnesses() ([]Harness, map[string]binary) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return discoverWithNode(ctx, resolveNode(ctx))
}

// Avoid accepting version-looking strings with nonnumeric trailing components
// when matching a consented plan.
func fileFingerprint(path string) string {
	if info, err := os.Stat(path); err == nil {
		return path + "/" + strconv.FormatInt(info.Size(), 10) + "/" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
	}
	return path + "/missing"
}
