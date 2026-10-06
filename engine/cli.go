package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func engineDataDir() (string, error) {
	if developmentBuild {
		if dir := os.Getenv("KLM_DEV_DATA_DIR"); dir != "" {
			if !filepath.IsAbs(dir) {
				return "", errors.New("KLM_DEV_DATA_DIR must be an absolute directory")
			}
			return filepath.Clean(dir), nil
		}
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "klm", dataDirName), nil
}

func dispatchCLI(args []string) error {
	if len(args) != 1 || (args[0] != "start" && args[0] != "stop" && args[0] != "__worker") {
		return errors.New("Usage: klm start | klm stop")
	}
	dir, err := engineDataDir()
	if err != nil {
		return err
	}
	if args[0] == "__worker" {
		return runEngine(dir)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Serialize start/stop without persisting a PID or interfering with the worker lock.
	deadline := time.Now().Add(60 * time.Second)
	var lock *os.File
	for {
		lock, err = lockDataDir(filepath.Join(dir, "cli.lock"))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("another start/stop is still in progress: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer lock.Close()
	if args[0] == "stop" {
		return stopEngine(dir)
	}
	if err := engineControl(dir, "ready"); err == nil {
		fmt.Println("KLM engine is already running.")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "engine.log")
	output, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer output.Close()
	cmd := exec.Command(exe, "__worker")
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdout, cmd.Stderr = output, output
	configureEngineWorker(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start engine: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline = time.Now().Add(45 * time.Second)
	for {
		if err := engineControl(dir, "ready"); err == nil {
			fmt.Printf("KLM engine started at http://localhost:%s.\n", apiPort)
			return nil
		}
		select {
		case err := <-exited:
			return fmt.Errorf("engine exited during startup (%v); see %s (check port %s and the data-directory lock)", err, logPath, apiPort)
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("engine readiness timed out; see %s", logPath)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func stopEngine(dir string) error {
	if err := engineControl(dir, "stop"); err != nil {
		lock, lockErr := lockDataDir(filepath.Join(dir, "engine.lock"))
		if lockErr != nil {
			return fmt.Errorf("cannot reach the running engine's local control channel: %w", err)
		}
		lock.Close()
		fmt.Println("KLM engine is already stopped.")
		return nil
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		lock, err := lockDataDir(filepath.Join(dir, "engine.lock"))
		if err == nil {
			lock.Close()
			fmt.Println("KLM engine stopped.")
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("engine shutdown is still in progress; retry klm stop")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
