package main

import (
	"context"
	"encoding/base64"
	encbinary "encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

type graphOutputBuffer struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *graphOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remain := (64 << 10) - len(b.data)
	if len(p) > remain {
		p = p[:remain]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

type graphProcessResult struct {
	Output                      string
	ExitCode                    int
	Truncated, Drained, Started bool
}
type graphUnconfirmedError struct{ cause error }

func (e *graphUnconfirmedError) Error() string { return e.cause.Error() }
func (e *graphUnconfirmedError) Unwrap() error { return e.cause }

// Only pipe-drain confirmation has a bounded wait. There is no task timeout.
func ownedGraphCommand(ctx context.Context, cwd, executable string, args []string, environment []string) (graphProcessResult, error) {
	result := graphProcessResult{Drained: true}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = cwd
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), environment...)
	cmd.WaitDelay = 5 * time.Second
	var output graphOutputBuffer
	cmd.Stdout, cmd.Stderr = &output, &output
	owner, err := PrepareGraphProcess(cmd)
	if err != nil {
		return result, err
	}
	cmd.Cancel = owner.Stop
	if err = cmd.Start(); err != nil {
		_ = owner.Close()
		return result, err
	}
	result.Started = true
	if err = owner.Attach(); err != nil {
		_ = owner.Stop()
		_ = cmd.Wait()
		closeErr := owner.Close()
		result.Drained = owner.Drained()
		failure := errors.Join(err, closeErr)
		if !result.Drained {
			failure = &graphUnconfirmedError{cause: failure}
		}
		return result, failure
	}
	waitErr := cmd.Wait()
	closeErr := owner.Close()
	result.Drained = owner.Drained()
	result.Output = strings.ToValidUTF8(string(output.data), "�")
	result.Truncated = output.truncated
	if len(result.Output) > 64<<10 {
		result.Output = strings.ToValidUTF8(result.Output[:64<<10], "")
		result.Truncated = true
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if !result.Drained {
		return result, &graphUnconfirmedError{cause: errors.Join(closeErr, errors.New("Owned process drainage could not be confirmed."))}
	}
	if closeErr != nil {
		return result, closeErr
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exit *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exit) {
		return result, waitErr
	}
	return result, nil // A shell's nonzero exit is data, not a graph failure.
}

func powerShellExecutable() (string, error) {
	for _, name := range []string{"pwsh", "powershell"} {
		if path, ok := executable(name); ok {
			return path, nil
		}
	}
	return "", errors.New("PowerShell is not installed; Terminal nodes require PowerShell.")
}
func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	bytes := make([]byte, len(units)*2)
	for i, u := range units {
		encbinary.LittleEndian.PutUint16(bytes[i*2:], u)
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

func (a *app) executeGraphTerminal(g *graphExecution, run GraphRun, x GraphActivation) graphCompletion {
	completion := graphCompletion{activationID: x.ID, drained: true}
	shell, err := powerShellExecutable()
	if err != nil {
		completion.err = err
		return completion
	}
	a.mu.Lock()
	cwd := ""
	if a.store != nil {
		workspace, readErr := a.readSQLGraphWorkspace(g.ctx, x.WorkspaceID)
		if readErr != nil {
			a.mu.Unlock()
			completion.err = readErr
			return completion
		}
		cwd = workspace.Directory
	} else {
		workspace := a.state.graphWorkspace(x.WorkspaceID)
		if workspace != nil {
			cwd = workspace.Directory
		}
	}
	a.mu.Unlock()
	if cwd == "" {
		completion.err = errors.New("Terminal workspace is unavailable.")
		return completion
	}
	dir := filepath.Join(a.dir, "graph-inputs", run.ID, x.ID)
	if err = os.MkdirAll(dir, 0700); err != nil {
		completion.err = err
		return completion
	}
	encoded, err := json.Marshal(x.Input)
	if err != nil {
		completion.err = err
		return completion
	}
	inputFile := filepath.Join(dir, "payload.json")
	if err = os.WriteFile(inputFile, encoded, 0600); err != nil {
		completion.err = err
		return completion
	}
	// A private data channel, never interpolation of values into configured code.
	// The only appended source is the author-owned script itself, as one context.
	script := "[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)\n$payload = ConvertFrom-Json -InputObject ([IO.File]::ReadAllText($env:KLM_GRAPH_PAYLOAD_FILE, [Text.Encoding]::UTF8))\n" + run.Snapshot.Graph.Definition.Nodes[x.NodeID].Command
	result, err := ownedGraphCommand(g.ctx, cwd, shell, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(script)}, []string{"KLM_GRAPH_PAYLOAD_FILE=" + inputFile})
	completion.drained, completion.err = result.Drained, err
	completion.command = &result
	if err == nil {
		compiled, e := compileGraphSnapshot(run.Snapshot)
		if e != nil {
			completion.err = e
			return completion
		}
		completion.output, completion.err = resolveGraphOutput(compiled.Outputs["terminal:"+x.NodeID], GraphOutputContext{Task: run.Input.Task, Payload: x.Input, CommandResult: &result.Output})
	}
	return completion
}

func graphGit(ctx context.Context, cwd string, args ...string) (string, error) {
	path, ok := executable("git")
	if !ok {
		return "", errors.New("Git is not installed.")
	}
	result, err := ownedGraphCommand(ctx, cwd, path, args, []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never"})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("Git %s failed (exit %d): %s", args[0], result.ExitCode, boundedText(result.Output, 4096))
	}
	if result.Truncated {
		return "", errors.New("Git metadata exceeded its capture limit.")
	}
	return strings.TrimSpace(result.Output), nil
}
