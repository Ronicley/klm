package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"sync"
	"time"
)

const sessionRuntimeIdleTimeout = 40 * time.Minute

// sessionRuntime owns the process container shared by ordinary turns in one
// conversation. Graph node processes intentionally never use this lifetime.
type sessionRuntime struct {
	app          *app
	id           string
	mu           sync.Mutex
	group        *runtimeProcessGroup
	bridge       *linkedBridge
	interactive  *runtimeInteractive
	openCode     *openCodeServer
	openCodeKey  string
	cancel       context.CancelFunc
	closed       bool
	turnActive   bool
	shutdownDone chan struct{}
	shutdownErr  error
}

type runtimeInteractive struct {
	process *interactiveProcess
	harness string
	model   string
	effort  string
	tools   string
	cleanup func()
}

type runtimeShutdown struct {
	group       *runtimeProcessGroup
	bridge      *linkedBridge
	interactive *runtimeInteractive
	openCode    *openCodeServer
}

func (a *app) beginSessionRuntime(id string) *sessionRuntime {
	for {
		a.mu.Lock()
		r := a.runtimes[id]
		if r == nil {
			r = &sessionRuntime{app: a, id: id}
			a.runtimes[id] = r
		}
		a.mu.Unlock()
		if r.beginTurn() {
			return r
		}
		a.mu.Lock()
		if a.runtimes[id] == r {
			delete(a.runtimes, id)
		}
		a.mu.Unlock()
	}
}

func (r *sessionRuntime) beginTurn() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.turnActive = true
	if r.group != nil {
		r.group.BeginTurn()
	}
	return true
}

func (r *sessionRuntime) prepare(cmd *exec.Cmd) (*ownedProcess, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, context.Canceled
	}
	if r.group == nil {
		group, err := newRuntimeProcessGroup()
		if err != nil {
			r.mu.Unlock()
			return nil, err
		}
		r.group = group
		r.group.BeginTurn()
	}
	group := r.group
	r.mu.Unlock()
	owner, err := prepareSessionOwnedProcess(cmd, group)
	if err == nil {
		r.publish()
	}
	return owner, err
}

func (r *sessionRuntime) markInfrastructure() {
	r.mu.Lock()
	group := r.group
	r.mu.Unlock()
	if group != nil {
		group.MarkInfrastructure()
	}
}

func (r *sessionRuntime) linkedBridge(p *adapter) (*linkedBridge, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, context.Canceled
	}
	if r.bridge != nil {
		bridge := r.bridge
		bridge.bind(p)
		r.mu.Unlock()
		return bridge, nil
	}
	r.mu.Unlock()
	bridge, err := newLinkedBridge(p, r.app.ctx)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		bridge.close()
		return nil, context.Canceled
	}
	r.bridge = bridge
	r.mu.Unlock()
	return bridge, nil
}

func (r *sessionRuntime) getInteractive(harness, model, effort, tools string) (*interactiveProcess, bool) {
	r.mu.Lock()
	current := r.interactive
	if current != nil {
		alive := true
		select {
		case <-current.process.Done:
			alive = false
		default:
		}
		if alive && current.harness == harness && current.model == model && current.effort == effort && current.tools == tools {
			process := current.process
			r.mu.Unlock()
			process.ResetTurn()
			return process, true
		}
		r.interactive = nil
	}
	r.mu.Unlock()
	if current != nil {
		closeRuntimeInteractive(current)
	}
	return nil, false
}

func (r *sessionRuntime) retainInteractive(process *interactiveProcess, harness, model, effort, tools string, cleanup func()) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return context.Canceled
	}
	r.interactive = &runtimeInteractive{process: process, harness: harness, model: model, effort: effort, tools: tools, cleanup: cleanup}
	return nil
}

func (r *sessionRuntime) discardInteractive(process *interactiveProcess) {
	r.mu.Lock()
	var current *runtimeInteractive
	if r.interactive != nil && r.interactive.process == process {
		current = r.interactive
		r.interactive = nil
	}
	r.mu.Unlock()
	if current != nil {
		closeRuntimeInteractive(current)
	}
}

func (r *sessionRuntime) getOpenCode(key string) (*openCodeServer, bool) {
	r.mu.Lock()
	current := r.openCode
	if current != nil && current.Alive() && r.openCodeKey == key {
		r.mu.Unlock()
		return current, true
	}
	r.openCode = nil
	r.openCodeKey = ""
	r.mu.Unlock()
	if current != nil {
		current.Close()
	}
	return nil, false
}

func (r *sessionRuntime) retainOpenCode(server *openCodeServer, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return context.Canceled
	}
	r.openCode = server
	r.openCodeKey = key
	return nil
}

func (r *sessionRuntime) discardOpenCode(server *openCodeServer) {
	r.mu.Lock()
	if r.openCode == server {
		r.openCode = nil
		r.openCodeKey = ""
	}
	r.mu.Unlock()
	server.Close()
}

func closeRuntimeInteractive(current *runtimeInteractive) {
	_ = current.process.EndInput()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
draining:
	for {
		select {
		case <-current.process.Done:
			break draining
		case _, ok := <-current.process.Frames:
			if !ok {
				break draining
			}
		case <-timer.C:
			break draining
		}
	}
	_ = current.process.Close()
	if current.cleanup != nil {
		current.cleanup()
	}
}

func (r *sessionRuntime) finishTurn() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	group := r.group
	r.turnActive = false
	if group == nil {
		r.closed = true
		resources := r.detachLocked()
		r.mu.Unlock()
		_ = r.finishShutdown(resources)
		return
	}
	group.EndTurn()
	if r.cancel != nil {
		r.cancel()
	}
	ctx, cancel := context.WithCancel(r.app.ctx)
	r.cancel = cancel
	r.mu.Unlock()
	go r.expire(ctx)
}

func (r *sessionRuntime) expire(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var idleSince time.Time
	for {
		r.mu.Lock()
		group := r.group
		closed := r.closed
		r.mu.Unlock()
		if closed || group == nil {
			return
		}
		if group.HasWorkload() {
			idleSince = time.Time{}
		} else if idleSince.IsZero() {
			idleSince = time.Now()
		} else if time.Since(idleSince) >= sessionRuntimeIdleTimeout {
			r.shutdownIdle(ctx)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *sessionRuntime) shutdownIdle(ctx context.Context) {
	r.mu.Lock()
	if ctx.Err() != nil || r.closed || r.turnActive {
		r.mu.Unlock()
		return
	}
	r.closed = true
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	resources := r.detachLocked()
	r.mu.Unlock()
	_ = r.finishShutdown(resources)
}

func (r *sessionRuntime) active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed && r.group != nil
}

func (r *sessionRuntime) shutdown() error {
	r.mu.Lock()
	if r.closed {
		done := r.shutdownDone
		r.mu.Unlock()
		if done != nil {
			<-done
		}
		r.mu.Lock()
		err := r.shutdownErr
		r.mu.Unlock()
		return err
	}
	r.closed = true
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	resources := r.detachLocked()
	r.mu.Unlock()
	return r.finishShutdown(resources)
}

func (r *sessionRuntime) detachLocked() runtimeShutdown {
	r.shutdownDone = make(chan struct{})
	resources := runtimeShutdown{group: r.group, bridge: r.bridge, interactive: r.interactive, openCode: r.openCode}
	r.group, r.bridge, r.interactive, r.openCode = nil, nil, nil, nil
	r.openCodeKey = ""
	return resources
}

func closeRuntimeResources(resources runtimeShutdown) error {
	// Stop the container first, not just the harness executable. This also kills
	// subprocesses holding inherited pipes or waiting on an unanswered permission.
	// Native abort/EOF and bridge cleanup must never delay this signal.
	var err error
	if resources.group != nil {
		err = resources.group.Close()
	}
	if resources.interactive != nil {
		closeRuntimeInteractive(resources.interactive)
	}
	if resources.openCode != nil {
		resources.openCode.Close()
	}
	if resources.bridge != nil {
		resources.bridge.close()
	}
	return err
}

func (r *sessionRuntime) finishShutdown(resources runtimeShutdown) error {
	err := closeRuntimeResources(resources)
	r.remove()
	r.mu.Lock()
	r.shutdownErr = err
	close(r.shutdownDone)
	r.mu.Unlock()
	return err
}

func (r *sessionRuntime) remove() {
	r.app.mu.Lock()
	if r.app.runtimes[r.id] == r {
		delete(r.app.runtimes, r.id)
		r.app.notifySessionLocked(r.id)
	}
	r.app.mu.Unlock()
}

func (r *sessionRuntime) publish() {
	r.app.mu.Lock()
	if r.app.runtimes[r.id] == r {
		r.app.notifySessionLocked(r.id)
	}
	r.app.mu.Unlock()
}

func (a *app) notifySessionLocked(id string) {
	for ch := range a.listeners[id] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	for subscription := range a.updateListeners {
		subscription.dirty[id] = true
		select {
		case subscription.wake <- struct{}{}:
		default:
		}
	}
}

func (a *app) sessionViewLocked(id string) *Session {
	view := a.state.sessionView(id)
	if view != nil {
		view.RuntimeActive = a.runtimes[id] != nil && a.runtimes[id].active()
	}
	return view
}

func (a *app) shutdownSessionRuntimes() {
	a.mu.Lock()
	runtimes := make([]*sessionRuntime, 0, len(a.runtimes))
	for _, runtime := range a.runtimes {
		runtimes = append(runtimes, runtime)
	}
	a.mu.Unlock()
	for _, runtime := range runtimes {
		runtime.shutdown()
	}
}

func (p *adapter) markRuntimeInfrastructure() {
	if p.runtime != nil {
		p.runtime.markInfrastructure()
	}
}

func (p *adapter) runtimeToolKey() string {
	encoded, _ := json.Marshal(map[string]any{"tools": p.bridgeTools(), "yolo": p.yolo})
	return string(encoded)
}
