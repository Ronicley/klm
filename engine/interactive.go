package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

type interactiveProcess struct {
	Frames <-chan map[string]any
	Done   <-chan struct{}
	stdin  io.WriteCloser
	cancel context.CancelFunc
	mu     sync.Mutex
	err    error
	owner  *ownedProcess
}

func startInteractive(t *turn, b binary, args []string, cwd string, runtime *sessionRuntime) (*interactiveProcess, error) {
	parent := t.ctx
	if runtime != nil {
		parent = runtime.app.ctx
	}
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, b.path, append(append([]string{}, b.args...), args...)...)
	cmd.Dir = cwd
	var owner *ownedProcess
	var err error
	if runtime == nil {
		owner, err = prepareOwnedProcess(cmd)
	} else {
		owner, err = runtime.prepare(cmd)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Cancel = owner.Stop
	cmd.WaitDelay = 5 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		_ = owner.Close()
		return nil, errors.New("Could not open harness input.")
	}
	frames := make(chan map[string]any, 64)
	done := make(chan struct{})
	p := &interactiveProcess{Frames: frames, Done: done, stdin: stdin, cancel: cancel, owner: owner}
	lines := &jsonLines{cancel: cancel, consume: func(line []byte) error {
		var frame map[string]any
		d := json.NewDecoder(bytes.NewReader(line))
		d.UseNumber()
		if err := d.Decode(&frame); err != nil || frame == nil {
			return errors.New("Harness returned invalid protocol JSON.")
		}
		select {
		case frames <- frame:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	cmd.Stdout = lines
	cmd.Stderr = &cappedBuffer{}
	started := time.Now()
	if err := cmd.Start(); err != nil {
		cancel()
		stdin.Close()
		_ = owner.Close()
		return nil, errors.New("Could not start the harness. Check its installation.")
	}
	if err := owner.Attach(); err != nil {
		cancel()
		stdin.Close()
		_ = owner.Stop()
		_ = cmd.Wait()
		_ = owner.Close()
		return nil, err
	}
	go func() {
		err := cmd.Wait()
		if ctx.Err() == nil {
			_ = lines.flush()
		}
		p.err = lines.err
		if err != nil {
			// Only expose exit metadata, never argv, stderr or arbitrary Wait errors.
			exit := "unavailable"
			if state := cmd.ProcessState; state != nil && state.ExitCode() >= 0 {
				exit = fmt.Sprintf("%d (0x%08X)", state.ExitCode(), uint32(state.ExitCode()))
			}
			reason := "Harness process exited unsuccessfully"
			if errors.Is(err, exec.ErrWaitDelay) {
				reason = "Harness process output did not close within the shutdown deadline"
			}
			p.err = errors.Join(p.err, fmt.Errorf("%s (exit code: %s; process lifetime: %s; process context cancelled: %t).",
				reason, exit, time.Since(started).Round(time.Millisecond), ctx.Err() != nil))
		}
		close(frames)
		close(done)
	}()
	return p, nil
}

func (p *interactiveProcess) Send(value any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.Done:
		if p.err != nil {
			return p.err
		}
		return errors.New("Harness connection has closed.")
	default:
	}
	return json.NewEncoder(p.stdin).Encode(value)
}

func (p *interactiveProcess) Err() error { <-p.Done; return p.err }

// EOF requests native shutdown without cancelling the process context. Callers
// must keep draining Frames, await Done, and still close/check the owned Job.
func (p *interactiveProcess) EndInput() error { return p.stdin.Close() }

func (p *interactiveProcess) Close() error {
	p.cancel()
	_ = p.stdin.Close()
	<-p.Done
	return errors.Join(p.err, p.owner.Close())
}

func (p *interactiveProcess) Drained() bool { return p.owner.Drained() }
