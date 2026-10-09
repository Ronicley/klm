package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"slices"
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
	lines  *jsonLines
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
		omitNativeImageData(frame)
		if len(line) > 2<<20 {
			retained, err := json.Marshal(frame)
			if err != nil || len(retained) > 2<<20 {
				return errors.New("Harness event content exceeded the 2 MiB limit.")
			}
		}
		select {
		case frames <- frame:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	if slices.Contains(args, "rpc") {
		// Pi echoes image content in user messages and agent-end records.
		lines.lineLimit, lines.turnLimit = imageJSONLineLimit, 192<<20
	}
	p.lines = lines
	cmd.Stdout = lines
	cmd.Stderr = &cappedBuffer{}
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
		if lines.err != nil {
			p.err = lines.err
		} else if err != nil {
			p.err = errors.New("Harness process exited unsuccessfully. Check its authentication and configuration.")
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

func (p *interactiveProcess) ResetTurn() { p.lines.resetTurn() }
