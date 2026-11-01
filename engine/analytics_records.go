package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Only normalized accounting evidence belongs here. Native identifiers are hashed;
// no prompt, native path, request body or tool result is retained in this ledger.
type AnalyticsState struct {
	Version    int                             `json:"version"`
	Started    string                          `json:"started"`
	Executions map[string]AnalyticsExecution   `json:"executions"`
	Counters   map[string]AnalyticsCounter     `json:"counters"`
	Graphs     map[string]AnalyticsGraphTiming `json:"graphs"`
}
type AnalyticsSpan struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Kind  string `json:"kind"`
}
type AnalyticsUsageDelta struct {
	Sequence uint64 `json:"sequence"`
	At       string `json:"at"`
	Input    *int64 `json:"input"`
	Output   *int64 `json:"output"`
}
type AnalyticsExecution struct {
	ID             string                `json:"id"`
	SessionID      string                `json:"sessionId"`
	OwnerID        string                `json:"ownerId"`
	ProjectID      string                `json:"projectId,omitempty"`
	Role           string                `json:"role"`
	Harness        string                `json:"harness"`
	GraphRunID     string                `json:"graphRunId,omitempty"`
	ActivationID   string                `json:"activationId,omitempty"`
	GraphTurnID    string                `json:"graphTurnId,omitempty"`
	Reserved       string                `json:"reserved"`
	Dispatched     string                `json:"dispatched,omitempty"`
	Accepted       string                `json:"accepted,omitempty"`
	Last           string                `json:"last"`
	Ended          string                `json:"ended,omitempty"`
	UnknownUntil   string                `json:"unknownUntil,omitempty"` // Unmeasured tail upper bound, never charged as effort.
	Spans          []AnalyticsSpan       `json:"spans"`
	Waits          map[string]bool       `json:"waits,omitempty"`
	UnknownWaits   map[string]bool       `json:"unknownWaits,omitempty"`
	Usage          []AnalyticsUsageDelta `json:"usage"`
	Sequence       uint64                `json:"sequence"`
	InputBaseline  bool                  `json:"inputBaseline"`
	OutputBaseline bool                  `json:"outputBaseline"`
	InputFinal     bool                  `json:"inputFinal"`
	OutputFinal    bool                  `json:"outputFinal"`
	Reasons        []string              `json:"reasons,omitempty"`
}
type AnalyticsCounter struct {
	Input         *int64 `json:"input"`
	Output        *int64 `json:"output"`
	ExecutionID   string `json:"executionId"`
	InputInvalid  bool   `json:"inputInvalid,omitempty"`
	OutputInvalid bool   `json:"outputInvalid,omitempty"`
}
type AnalyticsGraphTiming struct {
	Last        string `json:"last"`
	End         string `json:"end,omitempty"`
	Interrupted bool   `json:"interrupted,omitempty"`
}

// A small append mutation shares the very same fsynced frame as Session.Usage.
// The counter and sequence are replaced; the dated delta is appended once.
type analyticsUsageMutation struct {
	ExecutionID    string              `json:"executionId"`
	Key            string              `json:"key"`
	Counter        AnalyticsCounter    `json:"counter"`
	Delta          AnalyticsUsageDelta `json:"delta"`
	InputBaseline  bool                `json:"inputBaseline"`
	OutputBaseline bool                `json:"outputBaseline"`
	InputFinal     bool                `json:"inputFinal"`
	OutputFinal    bool                `json:"outputFinal"`
	Reason         string              `json:"reason,omitempty"`
}

func analyticsHash(values ...string) string {
	b, _ := json.Marshal(values)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func analyticsTime(s string) time.Time  { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func analyticsStamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func analyticsReason(e *AnalyticsExecution, reason string) {
	for _, r := range e.Reasons {
		if r == reason {
			return
		}
	}
	e.Reasons = append(e.Reasons, reason)
}
func analyticsOwner(d *diskState, s *Session) *Session {
	seen := map[string]bool{}
	for s != nil && !seen[s.ID] {
		seen[s.ID] = true
		if s.GraphRunID != "" {
			r := d.graphRun(s.GraphRunID)
			if r == nil {
				return nil
			}
			s = d.session(r.ConversationID)
			continue
		}
		if s.ParentID == "" {
			return s
		}
		s = d.session(s.ParentID)
	}
	return nil
}
func initializeAnalytics(d *diskState) error {
	if d.Analytics != nil {
		return validateAnalytics(d)
	}
	d.Analytics = &AnalyticsState{Version: 1, Started: now(), Executions: map[string]AnalyticsExecution{}, Counters: map[string]AnalyticsCounter{}, Graphs: map[string]AnalyticsGraphTiming{}}
	// Old usage is intentionally not spend. The next adapter must reconcile its
	// native history before it may attribute deltas to a new execution.
	return nil
}
func recoverAnalytics(d *diskState) bool {
	if d.Analytics == nil {
		return false
	}
	changed := false
	for id, e := range d.Analytics.Executions {
		if e.Ended != "" {
			continue
		}
		if analyticsCloseUnaccepted(&e, now()) {
			d.Analytics.Executions[id], changed = e, true
			continue
		}
		e.Ended, e.Waits = e.Last, nil
		e.UnknownWaits = nil
		if e.Accepted != "" {
			e.UnknownUntil = now()
		}
		analyticsReason(&e, "interrupted_tail_unknown")
		d.Analytics.Executions[id], changed = e, true
	}
	for id, g := range d.Analytics.Graphs {
		if g.End != "" {
			continue
		}
		g.End, g.Interrupted = g.Last, true
		d.Analytics.Graphs[id], changed = g, true
	}
	return changed
}
func validateAnalytics(d *diskState) error {
	a := d.Analytics
	if a == nil {
		return nil
	} // Legacy checkpoints initialize only after replay.
	if a.Version != 1 || analyticsTime(a.Started).IsZero() || a.Executions == nil || a.Counters == nil || a.Graphs == nil {
		return errors.New("invalid analytics schema")
	}
	for id, e := range a.Executions {
		if e.ID != id || d.session(e.SessionID) == nil || analyticsTime(e.Reserved).IsZero() || analyticsTime(e.Last).IsZero() {
			return errors.New("invalid analytics execution")
		}
		if err := analyticsRecordLimit(e); err != nil {
			return err
		}
		previous := time.Time{}
		for _, span := range e.Spans {
			s, end := analyticsTime(span.Start), analyticsTime(span.End)
			if s.IsZero() || end.Before(s) || s.Before(previous) || (span.Kind != "working" && span.Kind != "blocked" && span.Kind != "unknown") {
				return errors.New("invalid analytics interval")
			}
			previous = end
		}
		var seq uint64
		for _, delta := range e.Usage {
			if delta.Sequence <= seq || delta.Sequence > e.Sequence || analyticsTime(delta.At).IsZero() || !analyticsValidCount(delta.Input) || !analyticsValidCount(delta.Output) {
				return errors.New("invalid analytics usage")
			}
			seq = delta.Sequence
		}
	}
	for _, c := range a.Counters {
		if _, ok := a.Executions[c.ExecutionID]; !ok || !analyticsValidCount(c.Input) || !analyticsValidCount(c.Output) {
			return errors.New("invalid analytics counter")
		}
	}
	for id, g := range a.Graphs {
		if d.graphRun(id) == nil || analyticsTime(g.Last).IsZero() || g.End != "" && analyticsTime(g.End).IsZero() {
			return errors.New("invalid analytics graph timing")
		}
	}
	return nil
}
func analyticsValidCount(n *int64) bool { return n == nil || *n >= 0 && *n <= maxTokenCount }

func (d *diskState) applyAnalyticsUsage(m *analyticsUsageMutation, sessionID string) error {
	if m == nil {
		return nil
	}
	if d.Analytics == nil {
		return errors.New("analytics usage without collection")
	}
	e, ok := d.Analytics.Executions[m.ExecutionID]
	if !ok || e.SessionID != sessionID || m.Key == "" || m.Counter.ExecutionID != e.ID || m.Delta.Sequence != e.Sequence+1 || analyticsTime(m.Delta.At).IsZero() || !analyticsValidCount(m.Counter.Input) || !analyticsValidCount(m.Counter.Output) || !analyticsValidCount(m.Delta.Input) || !analyticsValidCount(m.Delta.Output) {
		return errors.New("invalid analytics usage mutation")
	}
	e.Sequence = m.Delta.Sequence
	if m.Delta.Input != nil || m.Delta.Output != nil {
		e.Usage = append(e.Usage, m.Delta)
	}
	if err := analyticsRecordLimit(e); err != nil {
		return err
	}
	e.InputBaseline, e.OutputBaseline = m.InputBaseline, m.OutputBaseline
	e.InputFinal, e.OutputFinal = m.InputFinal, m.OutputFinal
	if m.Reason != "" {
		analyticsReason(&e, m.Reason)
	}
	d.Analytics.Counters[m.Key], d.Analytics.Executions[e.ID] = m.Counter, e
	return nil
}

func analyticsAdvance(e *AnalyticsExecution, at time.Time) {
	if e.Accepted == "" || e.Ended != "" {
		return
	}
	for _, r := range e.Reasons {
		if r == "clock_discontinuity" {
			return
		}
	}
	last := analyticsTime(e.Last)
	if !at.After(last) {
		return
	}
	kind := "working"
	if e.Role == sessionRoleSubagent {
		kind = "unknown"
	} else if len(e.Waits) > 0 {
		kind = "blocked"
	} else if len(e.UnknownWaits) > 0 {
		kind = "unknown"
	}
	stamp := analyticsStamp(at)
	if n := len(e.Spans); n > 0 && e.Spans[n-1].Kind == kind && e.Spans[n-1].End == e.Last {
		e.Spans[n-1].End = stamp
	} else {
		e.Spans = append(e.Spans, AnalyticsSpan{Start: e.Last, End: stamp, Kind: kind})
	}
	e.Last = stamp
}
func (t *turn) analyticsInstant() (time.Time, bool) {
	if t.analyticsAnchor.IsZero() {
		return time.Now().UTC(), true
	}
	mapped := t.analyticsAnchor.Add(time.Since(t.analyticsAnchor))
	drift := time.Now().UTC().Sub(mapped.UTC()) // Strip monotonic fields for wall-clock comparison.
	if !t.analyticsEndCandidate.IsZero() && t.analyticsEndCandidate.Before(mapped) {
		mapped = t.analyticsEndCandidate
	}
	return mapped.UTC(), drift < -2*time.Second || drift > 2*time.Second
}
func analyticsWait(d *diskState, t *turn, key string, start bool, attributable bool) {
	if d.Analytics == nil || t == nil {
		return
	}
	e, ok := d.analyticsExecution(t.analyticsID)
	if !ok || e.Ended != "" {
		return
	}
	if !attributable {
		analyticsReason(&e, "wait_origin_or_exclusivity_unknown")
		d.setAnalyticsExecution(e)
		return
	}
	if e.Accepted != "" {
		at, drift := t.analyticsInstant()
		if drift {
			analyticsReason(&e, "clock_discontinuity")
			e.UnknownUntil = now()
		} else {
			analyticsAdvance(&e, at)
		}
	}
	if start {
		if e.Waits == nil {
			e.Waits = map[string]bool{}
		}
		e.Waits[key] = true
	} else {
		delete(e.Waits, key)
		delete(e.UnknownWaits, key)
	}
	d.setAnalyticsExecution(e)
}
func (p *adapter) analyticsWaitEnd(key string) error {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	if p.app.store != nil {
		return p.sqlAnalyticsWrite(func(st *sqliteSessionTx) error { return st.analyticsWait(p.turn, key, false, true, true) })
	}
	if p.app.state.Analytics == nil {
		return nil
	}
	e := p.app.state.Analytics.Executions[p.turn.analyticsID]
	if !e.Waits[key] && !e.UnknownWaits[key] {
		return nil
	}
	return p.app.commitLocked(func(d *diskState) { analyticsWait(d, p.turn, key, false, true) })
}
func (p *adapter) analyticsOwnRequest(origin string) bool {
	// A child wait does not prove its parent is blocked.
	if p.subagent {
		return false
	}
	// OpenCode child requests are projected on the root UI before task discovery.
	// Only an explicit matching native origin may subtract from the root clock.
	if origin == "unknown" {
		return false
	}
	if origin == "" {
		return p.harness == "pi"
	}
	return origin == p.analyticsNative
}
func (p *adapter) analyticsWaitStart(d *diskState, key, origin string) {
	own := p.analyticsOwnRequest(origin)
	analyticsWait(d, p.turn, key, true, own)
	if !own || p.harness == "pi" || d.Analytics == nil {
		return
	}
	e, ok := d.analyticsExecution(p.turn.analyticsID)
	if !ok || e.Ended != "" {
		return
	}
	// Native origin proves which call is waiting, not that parallel native
	// work has stopped. Preserve this span as unknown unless exclusivity is
	// established by the adapter; never charge it as working or blocked time.
	delete(e.Waits, key)
	if e.UnknownWaits == nil {
		e.UnknownWaits = map[string]bool{}
	}
	e.UnknownWaits[key] = true
	analyticsReason(&e, "wait_exclusivity_unknown")
	d.setAnalyticsExecution(e)
}
func (p *adapter) analyticsBindNative(id, path string) {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	p.analyticsNative = analyticsHash(p.id, p.harness, id, path)
}
func (p *adapter) analyticsStart() error {
	return p.analyticsStartAt(time.Now())
}
func (p *adapter) analyticsDispatch() (time.Time, error) {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	if p.app.store != nil {
		err := p.sqlAnalyticsWrite(func(st *sqliteSessionTx) error {
			e, key, err := st.analyticsExecution(p.turn.analyticsID)
			if err != nil {
				return err
			}
			e.Dispatched = now()
			e.Last = e.Dispatched
			return st.putExistingRecord("analytics_executions", key, e)
		})
		return time.Now(), err
	}
	if p.app.state.Analytics == nil {
		return time.Now(), nil
	}
	err := p.app.commitLocked(func(d *diskState) {
		e, ok := d.analyticsExecution(p.turn.analyticsID)
		if !ok {
			return
		}
		e.Dispatched = now()
		e.Last = e.Dispatched
		d.setAnalyticsExecution(e)
	})
	return time.Now(), err
}
func (p *adapter) analyticsStartAt(anchor time.Time) error {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	if p.app.store != nil {
		if p.subagent {
			return nil
		}
		changed := false
		err := p.sqlAnalyticsWrite(func(st *sqliteSessionTx) error {
			e, key, err := st.analyticsExecution(p.turn.analyticsID)
			if err != nil {
				return err
			}
			if e.Accepted != "" || e.Ended != "" {
				return nil
			}
			if err := st.clearPreparationTiming(key); err != nil {
				return err
			}
			e.UnknownUntil = ""
			e.Accepted, e.Last = analyticsStamp(anchor), analyticsStamp(anchor)
			changed = true
			return st.putExistingRecord("analytics_executions", key, e)
		})
		if err == nil && changed {
			p.turn.analyticsAnchor = anchor
		}
		return err
	}
	if p.app.state.Analytics == nil || p.subagent {
		return nil
	}
	e, ok := p.app.state.Analytics.Executions[p.turn.analyticsID]
	if !ok || e.Accepted != "" || e.Ended != "" {
		return nil
	}
	if err := p.app.commitLocked(func(d *diskState) {
		// Only accepted work has a clock mapping. Discard any old preparation-
		// era timing diagnostics before establishing the first native anchor.
		e, _ = d.analyticsExecution(p.turn.analyticsID)
		e.Reasons = analyticsWithoutPreparationTiming(e.Reasons)
		e.UnknownUntil = ""
		e.Accepted, e.Last = analyticsStamp(anchor), analyticsStamp(anchor)
		d.setAnalyticsExecution(e)
	}); err != nil {
		return err
	}
	p.turn.analyticsAnchor = anchor
	return nil
}
func (p *adapter) analyticsEnd() error {
	return p.analyticsEndAt(time.Now())
}
func (p *adapter) analyticsIdleCandidate(at time.Time) {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	p.turn.analyticsEndCandidate = at
}
func (p *adapter) analyticsTerminated() error {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	if p.app.store != nil {
		return p.sqlAnalyticsWrite(func(st *sqliteSessionTx) error { return st.finishAnalytics(p.turn, !p.processesDrained, nil) })
	}
	if p.app.state.Analytics == nil {
		return nil
	}
	e, ok := p.app.state.Analytics.Executions[p.turn.analyticsID]
	if !ok || e.Accepted == "" || e.Ended != "" {
		return nil
	}
	return p.app.commitLocked(func(d *diskState) { analyticsFinish(d, p.turn, !p.processesDrained) })
}
func (p *adapter) analyticsEndAt(at time.Time) error {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	if p.app.store != nil {
		return p.sqlAnalyticsWrite(func(st *sqliteSessionTx) error { return st.finishAnalytics(p.turn, false, &at) })
	}
	return p.app.commitLocked(func(d *diskState) {
		if d.Analytics == nil {
			return
		}
		e, ok := d.analyticsExecution(p.turn.analyticsID)
		if !ok || e.Ended != "" {
			return
		}
		if analyticsCloseUnaccepted(&e, now()) {
			d.setAnalyticsExecution(e)
			return
		}
		mapped, drift := p.turn.analyticsInstant()
		if drift {
			analyticsReason(&e, "clock_discontinuity")
			e.UnknownUntil = now()
		} else {
			end := p.turn.analyticsAnchor.Add(at.Sub(p.turn.analyticsAnchor)).UTC()
			if end.After(mapped) {
				end = mapped
			}
			// Reconciliation can race a liveness checkpoint. A confirmed native
			// end replaces (never adds to) that provisional tail.
			if !end.Before(analyticsTime(e.Accepted)) && end.Before(analyticsTime(e.Last)) {
				spans := make([]AnalyticsSpan, 0, len(e.Spans))
				for _, span := range e.Spans {
					if !analyticsTime(span.Start).Before(end) {
						break
					}
					if analyticsTime(span.End).After(end) {
						span.End = analyticsStamp(end)
					}
					spans = append(spans, span)
				}
				e.Spans = spans
				e.Last = analyticsStamp(end)
			} else {
				analyticsAdvance(&e, end)
			}
		}
		e.Ended, e.Waits = e.Last, nil
		e.UnknownWaits = nil
		for _, reason := range e.Reasons {
			if reason == "clock_discontinuity" {
				e.UnknownUntil = now()
			}
		}
		d.setAnalyticsExecution(e)
	})
}
func analyticsFinish(d *diskState, t *turn, uncertain bool) {
	if d.Analytics == nil {
		return
	}
	e, ok := d.analyticsExecution(t.analyticsID)
	if !ok || e.Ended != "" {
		return
	}
	if analyticsCloseUnaccepted(&e, now()) {
		d.setAnalyticsExecution(e)
		return
	}
	at, drift := t.analyticsInstant()
	if uncertain || drift {
		analyticsReason(&e, "termination_or_clock_unknown")
		e.UnknownUntil = now()
	} else {
		analyticsAdvance(&e, at)
	}
	e.Ended, e.Waits = e.Last, nil
	e.UnknownWaits = nil
	d.setAnalyticsExecution(e)
}
func analyticsWithoutPreparationTiming(reasons []string) []string {
	clean := make([]string, 0, len(reasons))
	for _, r := range reasons {
		switch r {
		case "clock_discontinuity", "termination_or_clock_unknown", "interrupted_tail_unknown", "dispatch_acceptance_unknown":
		default:
			clean = append(clean, r)
		}
	}
	return clean
}
func analyticsCloseUnaccepted(e *AnalyticsExecution, at string) bool {
	if e.Accepted != "" {
		return false
	}
	e.Reasons = analyticsWithoutPreparationTiming(e.Reasons)
	e.UnknownUntil = ""
	if e.Dispatched != "" {
		// Unknown acceptance starts at dispatch, never at reservation/preparation.
		e.Last = e.Dispatched
		e.UnknownUntil = at
		analyticsReason(e, "dispatch_acceptance_unknown")
	}
	e.Ended, e.Waits, e.UnknownWaits = e.Last, nil, nil
	return true
}
func (a *app) reserveAnalyticsLocked(t *turn, s Session) error {
	if a.store != nil {
		t.analyticsID = newID()
		return a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error { return st.reserveAnalytics(t, s) })
	}
	if a.state.Analytics == nil {
		return nil
	}
	t.analyticsID = newID()
	return a.commitLocked(func(d *diskState) {
		owner := analyticsOwner(d, d.session(s.ID))
		ownerID, projectID := "", s.ProjectID
		if owner != nil {
			ownerID, projectID = owner.ID, owner.ProjectID
		}
		role := s.Role
		if role == "" {
			role = "main"
		}
		stamp := now()
		e := AnalyticsExecution{ID: t.analyticsID, SessionID: s.ID, OwnerID: ownerID, ProjectID: projectID, Role: role, Harness: s.Harness, GraphRunID: s.GraphRunID, Reserved: stamp, Last: stamp, Spans: []AnalyticsSpan{}, Usage: []AnalyticsUsageDelta{}}
		if s.GraphRunID != "" {
			for _, activation := range d.GraphActivations {
				if activation.RunID == s.GraphRunID && activation.SessionID == s.ID && activation.CurrentTurnID != "" {
					e.ActivationID, e.GraphTurnID = activation.ID, activation.CurrentTurnID
				}
			}
		}
		d.setAnalyticsExecution(e)
	})
}

// Invocations prove child identity/use, not task queued-to-finished working time.
func (p *adapter) analyticsChildInvocation(child *adapter, invocation string) error {
	if child == nil {
		return nil
	}
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	if p.app.store != nil {
		return p.sqlAnalyticsWrite(func(st *sqliteSessionTx) error {
			if err := st.initializeAnalytics(); err != nil {
				return err
			}
			id := analyticsHash(child.id, invocation)
			key := sqliteOwnedKey("settings", id)
			var exists bool
			if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_analytics_executions WHERE id=?)`, key).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return nil
			}
			s, err := st.sessionMeta(p.id)
			if err != nil {
				return err
			}
			owner, err := st.analyticsOwner(s)
			if err != nil {
				return err
			}
			stamp := now()
			e := AnalyticsExecution{ID: id, SessionID: child.id, OwnerID: owner.ID, ProjectID: owner.ProjectID, Role: sessionRoleSubagent, Harness: p.harness, Reserved: stamp, Accepted: stamp, Last: stamp, Ended: stamp}
			if err := st.putRecord("analytics_executions", key, "settings", id, -1, e); err != nil {
				return err
			}
			for _, field := range []string{"spans", "usage"} {
				if err := st.emptyCollection("analytics_executions", key, field, "array"); err != nil {
					return err
				}
			}
			if err := st.analyticsReason(key, "child_timing_and_usage_unavailable"); err != nil {
				return err
			}
			if p.turn.analyticsID != "" {
				return st.analyticsReason(sqliteOwnedKey("settings", p.turn.analyticsID), "native_child_coverage_partial")
			}
			return nil
		})
	}
	if p.app.state.Analytics == nil {
		return nil
	}
	id := analyticsHash(child.id, invocation)
	if _, exists := p.app.state.Analytics.Executions[id]; exists {
		return nil
	}
	return p.app.commitLocked(func(d *diskState) {
		owner := analyticsOwner(d, d.session(p.id))
		if owner == nil {
			return
		}
		stamp := now()
		d.setAnalyticsExecution(AnalyticsExecution{ID: id, SessionID: child.id, OwnerID: owner.ID, ProjectID: owner.ProjectID, Role: sessionRoleSubagent, Harness: p.harness, Reserved: stamp, Accepted: stamp, Last: stamp, Ended: stamp, Spans: []AnalyticsSpan{}, Usage: []AnalyticsUsageDelta{}, Reasons: []string{"child_timing_and_usage_unavailable"}})
		if root, ok := d.analyticsExecution(p.turn.analyticsID); ok {
			analyticsReason(&root, "native_child_coverage_partial")
			d.setAnalyticsExecution(root)
		}
	})
}
func (a *app) syncAnalyticsGraphs(before, d *diskState) {
	if d.Analytics == nil {
		return
	}
	for _, r := range d.GraphRuns {
		g, exists := d.analyticsGraph(r.ID)
		if !exists {
			if before.graphRun(r.ID) != nil {
				continue
			}
			g.Last = r.CreatedAt
			if a.analyticsGraphClocks == nil {
				a.analyticsGraphClocks = map[string]time.Time{}
			}
			a.analyticsGraphClocks[r.ID] = time.Now()
		}
		if g.End == "" && r.EndedAt != "" {
			_, drift := a.analyticsGraphInstant(r.ID)
			if r.Status == "interrupted" || drift || g.Interrupted {
				g.End, g.Interrupted = g.Last, true
			} else {
				g.Last, g.End = r.EndedAt, r.EndedAt
			}
		}
		if old, ok := d.analyticsGraph(r.ID); !ok || old != g {
			d.setAnalyticsGraph(r.ID, g)
		}
	}
}
func (a *app) analyticsGraphInstant(id string) (time.Time, bool) {
	anchor := a.analyticsGraphClocks[id]
	if anchor.IsZero() {
		return time.Time{}, true
	}
	at := anchor.Add(time.Since(anchor)).UTC()
	drift := time.Now().UTC().Sub(at)
	return at, drift < -2*time.Second || drift > 2*time.Second
}
func (a *app) checkpointAnalytics() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			open := len(a.runs) > 0 || len(a.graphRuns) > 0
			if open && a.storageErr == nil {
				if a.store != nil {
					_ = a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
						for _, t := range a.runs {
							e, key, err := st.analyticsExecution(t.analyticsID)
							if errors.Is(err, sql.ErrNoRows) {
								continue
							}
							if err != nil {
								return err
							}
							if e.Ended != "" || e.Accepted == "" {
								continue
							}
							at, drift := t.analyticsInstant()
							if drift {
								e.UnknownUntil = now()
								if err := st.analyticsReason(key, "clock_discontinuity"); err != nil {
									return err
								}
							} else if err := st.analyticsAdvance(&e, key, at); err != nil {
								return err
							}
							if err := st.putExistingRecord("analytics_executions", key, e); err != nil {
								return err
							}
						}
						for id:=range a.graphRuns{key:=sqliteOwnedKey("settings",id);var g AnalyticsGraphTiming;err:=decodeSQLiteRecord(st.ctx,st.tx,"analytics_graphs",key,64<<10,&g);if errors.Is(err,sql.ErrNoRows){continue};if err!=nil{return err};if g.End!=""{continue};at,drift:=a.analyticsGraphInstant(id);if drift{g.Interrupted=true}else if !g.Interrupted{g.Last=analyticsStamp(at)};if err:=st.putExistingRecord("analytics_graphs",key,g);err!=nil{return err}}
						return nil
					})
				} else {
					_ = a.commitLocked(a.checkpointAnalyticsLocked)
				}
			}
			a.mu.Unlock()
		}
	}
}
func (a *app) checkpointAnalyticsLocked(d *diskState) {
	if d.Analytics == nil {
		return
	}
	for _, t := range a.runs {
		e, ok := d.analyticsExecution(t.analyticsID)
		// Reservation/preparation and unconfirmed dispatch have no execution clock.
		if !ok || e.Ended != "" || e.Accepted == "" {
			continue
		}
		at, drift := t.analyticsInstant()
		if drift {
			analyticsReason(&e, "clock_discontinuity")
			e.UnknownUntil = now()
		} else {
			analyticsAdvance(&e, at)
		}
		d.setAnalyticsExecution(e)
	}
	for id := range a.graphRuns {
		g, ok := d.analyticsGraph(id)
		if !ok || g.End != "" {
			continue
		}
		at, drift := a.analyticsGraphInstant(id)
		if drift {
			g.Interrupted = true
		} else if !g.Interrupted {
			g.Last = analyticsStamp(at)
		}
		d.setAnalyticsGraph(id, g)
	}
}

func analyticsRecordLimit(e AnalyticsExecution) error {
	// Fail explicitly rather than purge retained evidence or silently drop ticks.
	if len(e.Spans) > 100000 || len(e.Usage) > 100000 || len(e.Waits)+len(e.UnknownWaits) > 1024 {
		return fmt.Errorf("analytics execution record capacity exceeded")
	}
	return nil
}
