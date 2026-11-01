package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
)

func conventionalNodePath() string { return "/usr/local/bin/node" }
func conventionalNPMPath() string  { return "/usr/local/lib/node_modules/npm/bin/npm-cli.js" }
func knownNodePaths() []string {
	return []string{conventionalNodePath(), "/usr/bin/node", "/usr/bin/nodejs"}
}
func defaultGlobalPrefix() string { return "/usr/local" }
func harnessInstallPlatform() string {
	if runtime.GOARCH != "amd64" {
		return "Installation supports Linux amd64 only."
	}
	if _, err := os.Stat("/lib64/ld-linux-x86-64.so.2"); err != nil {
		return "Installation requires glibc Linux amd64 (Ubuntu/Debian or compatible). musl is unsupported."
	}
	// Official Node 22 Linux artifacts require glibc >=2.28.
	text, err := probeOutput(context.Background(), "", "/lib64/ld-linux-x86-64.so.2", "--version")
	version := regexp.MustCompile(`(?:GLIBC|GNU libc)[^\n]*?([0-9]+)\.([0-9]+)`).FindStringSubmatch(text)
	if err != nil || len(version) != 3 {
		return "Could not verify glibc >=2.28 for the official Node.js distribution."
	}
	major, _ := strconv.Atoi(version[1])
	minor, _ := strconv.Atoi(version[2])
	if major < 2 || major == 2 && minor < 28 {
		return "Node.js bootstrap requires glibc >=2.28."
	}
	return ""
}
func bootstrapPermission() error {
	for _, path := range []string{"/usr/local/lib/node_modules/npm", "/usr/local/bin/node", "/usr/local/bin/npm", "/usr/local/bin/npx"} {
		if err := ensureAbsent(path); err != nil {
			return err
		}
	}
	return nil
}
func publishNode(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := bootstrapPermission(); err != nil {
		return err
	}
	// No sudo is enabled or configured. These fixed writes succeed only when
	// the current OS identity already has the required system-prefix access.
	for _, dir := range []string{"/usr/local/lib/node_modules", "/usr/local/bin"} {
		for parent := dir; parent != "/"; parent = filepath.Dir(parent) {
			if info, err := os.Lstat(parent); err == nil && info.Mode()&os.ModeSymlink != 0 {
				return errors.New("Refusing a linked Node.js system destination: " + parent)
			}
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return needsInstallPermission("Node.js bootstrap needs write access to /usr/local. Have an administrator install Node.js >=22.19.0 with npm, then refresh.")
		}
	}
	// Reserve only Node/npm-owned destinations, never overlay /usr/local itself.
	if err := os.Mkdir("/usr/local/lib/node_modules/npm", 0755); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return needsInstallPermission("Node.js bootstrap needs administrator-granted write access to /usr/local/lib/node_modules.")
		}
		return err
	}
	if err := copyNodeTree(ctx, filepath.Join(stage, "lib/node_modules/npm"), "/usr/local/lib/node_modules/npm"); err != nil {
		return errors.New("Node.js bootstrap was interrupted. Inspect /usr/local/lib/node_modules/npm before retrying.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := os.Open(filepath.Join(stage, "bin/node"))
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile("/usr/local/bin/node", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err = errors.Join(err, out.Close()); err != nil {
		return err
	}
	for _, name := range []string{"npm", "npx"} {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := filepath.Join("/usr/local/lib/node_modules/npm/bin", name+"-cli.js")
		if err := os.Symlink(target, "/usr/local/bin/"+name); err != nil {
			return errors.New("Could not publish the Node.js " + name + " link. Inspect /usr/local/bin before retrying.")
		}
	}
	return nil
}
