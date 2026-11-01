//go:build !linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Preserve existing Windows and unsupported-platform behavior.
func validateEngineDataDir(dir string) error { return nil }

func openEngineLog(dir string) (*os.File, error) {
	return os.OpenFile(filepath.Join(dir, "engine.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
}

func abortEngineStartup(cmd *exec.Cmd, exited <-chan error) error { return nil }
