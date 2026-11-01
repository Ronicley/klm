package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func conventionalNodePath() string {
	return filepath.Join(os.Getenv("ProgramFiles"), "nodejs", "node.exe")
}
func conventionalNPMPath() string {
	return filepath.Join(os.Getenv("ProgramFiles"), "nodejs", "node_modules", "npm", "bin", "npm-cli.js")
}

func knownNodePaths() []string {
	paths := []string{conventionalNodePath(), filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "nodejs", "node.exe")}
	if path := os.Getenv("NVM_SYMLINK"); filepath.IsAbs(path) {
		paths = append(paths, filepath.Join(path, "node.exe"))
	}
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		if key, err := registry.OpenKey(root, `SOFTWARE\Node.js`, registry.QUERY_VALUE); err == nil {
			path, _, err := key.GetStringValue("InstallPath")
			key.Close()
			if err == nil && filepath.IsAbs(path) {
				paths = append(paths, filepath.Join(path, "node.exe"))
			}
		}
	}
	return paths
}

// The official zip has no builtin npmrc: npm's ordinary Windows default is
// dirname(node.exe). Preserve that rather than inventing a private prefix.
func defaultGlobalPrefix() string { return filepath.Dir(conventionalNodePath()) }
func harnessInstallPlatform() string {
	if runtime.GOARCH != "amd64" {
		return "Installation supports Windows amd64 only."
	}
	return ""
}
func bootstrapPermission() error {
	if !filepath.IsAbs(conventionalNodePath()) {
		return errors.New("ProgramFiles does not identify an absolute Node.js destination.")
	}
	return ensureAbsent(filepath.Dir(conventionalNodePath()))
}
func publishNode(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	destination := filepath.Dir(conventionalNodePath())
	if err := bootstrapPermission(); err != nil {
		return err
	}
	// Uses the existing token only: no hidden UAC window, passwords or detached MSI.
	if err := os.Mkdir(destination, 0755); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return needsInstallPermission("Node.js needs write permission to " + destination + ". Have a local administrator install Node.js >=22.19.0 with npm, then refresh.")
		}
		return err
	}
	if err := copyNodeTree(ctx, stage, destination); err != nil {
		return errors.New("Node.js bootstrap was interrupted or failed. Inspect the partially created conventional Node.js directory before retrying.")
	}
	// Match a conventional Windows installation's user PATH publication, without
	// changing os.Environ() in the running engine or replacing other entries.
	if err := ctx.Err(); err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return errors.New("Node.js was installed, but its user PATH entry could not be published. Add " + destination + " to the engine user's PATH and refresh.")
	}
	defer key.Close()
	path, valueType, readErr := key.GetStringValue("Path")
	if readErr != nil && !errors.Is(readErr, registry.ErrNotExist) {
		return errors.New("Node.js was installed, but the existing user PATH could not be read.")
	}
	for _, entry := range strings.Split(path, ";") {
		if strings.EqualFold(filepath.Clean(strings.Trim(entry, "\"")), destination) {
			return nil
		}
	}
	if path != "" && !strings.HasSuffix(path, ";") {
		path += ";"
	}
	if valueType == registry.SZ {
		err = key.SetStringValue("Path", path+destination)
	} else {
		err = key.SetExpandStringValue("Path", path+destination)
	}
	if err != nil {
		return err
	}
	// Notify ordinary terminal launchers. Existing terminals retain their own
	// inherited environment, as with a normal Windows installer.
	environment, _ := windows.UTF16PtrFromString("Environment")
	var result uintptr
	windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW").Call(0xffff, 0x1a, 0, uintptr(unsafe.Pointer(environment)), 0x2, 2000, uintptr(unsafe.Pointer(&result)))
	return nil
}
