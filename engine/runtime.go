package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os/exec"
	"sync"
	"time"
)

const sessionRuntimeIdleTimeout = 40 * time.Minute

// sessionRuntime owns the process container shared by ordinary turns in one
// conversation. Graph node processes intentionally never use this lifetime.
type sessionRuntime struct {
	app               *app
	id                string
	mu                sync.Mutex
	group             *runtimeProcessGroup
	bridge            *linkedBridge
	interactive       *runtimeInteractive
	openCode          *openCodeServer
	openCodeKey       string
	cancel            context.CancelFunc
	closed            bool
	turnActive        bool
	refreshBeforeTurn bool // Retain idle resources, but never reuse their managed profile.
	shutdownDone      chan struct{}
	shutdownErr       error
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

func (a *app) beginSessionRuntime(id string) (*sessionRuntime, error) {
	for {
		a.mu.Lock()
		r := a.runtimes[id]
		if r == nil {
			r = &sessionRuntime{app: a, id: id}
			if a.runtimes == nil {
				a.runtimes = map[string]*sessionRuntime{}
			}
			a.runtimes[id] = r
		}
		a.mu.Unlock()
		ready, err := r.beginTurn()
		if err != nil {
			return nil, err
		}
		if ready {
			return r, nil
		}
		a.mu.Lock()
		if a.runtimes[id] == r {
			delete(a.runtimes, id)
		}
		a.mu.Unlock()
	}
}

func (r *sessionRuntime) beginTurn() (bool, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false, r.shutdown()
	}
	if r.group != nil {
		// Capture carried workload before the new harness can be classified as
		// infrastructure, even if the idle watcher has not run between turns.
		if err := r.group.BeginTurn(); err != nil {
			r.mu.Unlock()
			return false, errors.Join(err, r.shutdown())
		}
	}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.turnActive = true
	if r.refreshBeforeTurn {
		// Rotate only turn-scoped transports/profiles. The session Job also owns
		// user workloads (browsers, servers, etc.) and must survive this boundary.
		resources := runtimeShutdown{bridge: r.bridge, interactive: r.interactive, openCode: r.openCode}
		retireErr := closeRuntimeResources(resources)
		if retireErr != nil || resources.openCode != nil && resources.openCode.Alive() {
			r.mu.Unlock()
			return false, errors.Join(errors.New("Could not confirm retirement of the previous managed profile."), retireErr, r.shutdown())
		}
		r.bridge, r.interactive, r.openCode = nil, nil, nil
		r.openCodeKey = ""
		r.refreshBeforeTurn = false
	}
	r.mu.Unlock()
	return true, nil
}

func (r *sessionRuntime) prepare(cmd *exec.Cmd) (*ownedProcess, error) {
	r.mu.Lock()
	created := false
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
		if err := group.BeginTurn(); err != nil {
			_ = group.Close()
			r.mu.Unlock()
			return nil, err
		}
		r.group = group
		created = true
	}
	group := r.group
	r.mu.Unlock()
	if created {
		watchRuntimeGroup(group, r.supervisionFailed)
	}
	owner, err := prepareSessionOwnedProcess(cmd, group)
	if err == nil {
		r.publish()
	}
	return owner, err
}

// Loss of an idle supervisor is an observable failure too, not an indefinitely
// retained "idle" runtime. Active adapters record their own native/teardown error.
func (r *sessionRuntime) supervisionFailed(cause error) {
	shutdownErr := r.shutdown()
	a := r.app
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runs[r.id] != nil {
		return
	}
	message := errors.Join(cause, shutdownErr).Error()
	if a.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
			s, err := st.sessionMeta(r.id)
			if err != nil {
				return err
			}
			s.Status, s.UpdatedAt = "error", now()
			if _, err := st.appendEvent(r.id, event("error", message)); err != nil {
				return err
			}
			return st.putSessionMeta(s)
		})
	} else if a.state.session(r.id) != nil {
		_ = a.commitLocked(func(d *diskState) {
			s := d.session(r.id)
			s.Status, s.UpdatedAt = "error", now()
			s.Events = append(s.Events, event("error", message))
		})
	}
}

func (r *sessionRuntime) checkHealth() error {
	r.mu.Lock()
	group, closed, done := r.group, r.closed, r.shutdownDone
	r.mu.Unlock()
	if group != nil {
		return runtimeGroupHealth(group)
	}
	if closed && done != nil {
		<-done
		r.mu.Lock()
		err := r.shutdownErr
		r.mu.Unlock()
		return err
	}
	return nil
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

func (r *sessionRuntime) retainInteractive(process *interactiveProcess, harness, model, effort, tools string, perTurn bool, cleanup func()) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return context.Canceled
	}
	r.interactive = &runtimeInteractive{process: process, harness: harness, model: model, effort: effort, tools: tools, cleanup: cleanup}
	r.refreshBeforeTurn = perTurn
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

func (r *sessionRuntime) retainOpenCode(server *openCodeServer, key string, perTurn bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return context.Canceled
	}
	r.openCode = server
	r.openCodeKey = key
	r.refreshBeforeTurn = perTurn
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

func closeRuntimeInteractive(current *runtimeInteractive) error {
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
	// Deliberate native termination can have a nonzero exit status. Ownership
	// confirmation is separate and must not be discarded with that exit metadata.
	ownedErr := current.process.owner.Close()
	if current.cleanup != nil {
		current.cleanup()
	}
	return ownedErr
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
		err = errors.Join(err, closeRuntimeInteractive(resources.interactive))
	}
	if resources.openCode != nil {
		resources.openCode.Close()
		err = errors.Join(err, resources.openCode.owner.Close())
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
	if a.store != nil {
		active := a.runtimes[id] != nil && a.runtimes[id].active()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.persistSQLLocked(ctx, func(st *sqliteSessionTx) error {
			var before bool
			err := st.tx.QueryRowContext(ctx, `SELECT active FROM runtime_activity WHERE session_id=?`, id).Scan(&before)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if before == active {
				return nil
			}
			if _, err := st.tx.ExecContext(ctx, `INSERT INTO runtime_activity(session_id,active) VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET active=excluded.active`, id, active); err != nil {
				return err
			}
			st.changes.Sessions[id] = nil
			return nil
		})
		return
	}
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
	view = a.state.automationSessionView(view)
	if view != nil {
		view.RuntimeActive = a.runtimes[id] != nil && a.runtimes[id].active()
		if view.Role == sessionRoleSideAgent {
			if topic := a.state.sessionTopic(a.state.session(id)); topic != nil {
				view.TopicID = topic.ID
			}
		}
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
	delegationTurn := ""
	if p.delegation != nil {
		delegationTurn = p.delegation.parentTurnID
	}
	encoded, _ := json.Marshal(map[string]any{"delegationTurn": delegationTurn, "automationRunId": p.automationRunID, "tools": p.bridgeTools(), "yolo": p.yolo, "capabilities": p.capabilities, "internalSkills": p.internalSkills})
	return string(encoded)
}
