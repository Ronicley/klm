//go:build !windows && !linux

package main

import (
	"errors"
	"net"
	"os/exec"
)

func configureEngineWorker(cmd *exec.Cmd) {}
func engineControl(dir, command string) error {
	return errors.New("engine start/stop requires Windows")
}
func serveEngineControl(dir string, a *app, shutdown chan struct{}) (net.Listener, error) {
	return nil, errors.New("engine worker requires Windows")
}
