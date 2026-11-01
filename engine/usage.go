package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"reflect"
)

// Totals cover the native session; context is a separate, latest usage snapshot.
// Nil counts mean unavailable, not zero. Output includes reported reasoning.
type SessionUsage struct {
	InputTokens  *int64        `json:"inputTokens"`
	OutputTokens *int64        `json:"outputTokens"`
	Context      *ContextUsage `json:"context"`
}

type ContextUsage struct {
	Tokens *int64 `json:"tokens"`
	Window *int64 `json:"window"`
}

const maxTokenCount int64 = 1<<53 - 1 // JSON numbers must remain exact in the client.

func tokenCount(value any) *int64 {
	var n int64
	switch value := value.(type) {
	case json.Number:
		var err error
		n, err = value.Int64()
		if err != nil {
			return nil
		}
	case float64:
		if math.IsNaN(value) || value < 0 || value > float64(maxTokenCount) || value != math.Trunc(value) {
			return nil
		}
		n = int64(value)
	default:
		return nil
	}
	if n < 0 || n > maxTokenCount {
		return nil
	}
	return &n
}

func sumTokens(counts ...*int64) *int64 {
	var total int64
	for _, count := range counts {
		if count == nil || *count > maxTokenCount-total {
			return nil
		}
		total += *count
	}
	return &total
}

func contextUsage(tokens, window *int64) *ContextUsage {
	if window != nil && *window == 0 {
		window = nil
	}
	if tokens == nil && window == nil {
		return nil
	}
	return &ContextUsage{Tokens: tokens, Window: window}
}

// Replace authoritative snapshots instead of adding repeated stream updates.
func (p *adapter) setUsage(usage *SessionUsage) error {
	return p.setUsageObservation(usage, false, false)
}

func (p *adapter) setUsageBaseline(usage *SessionUsage) error {
	return p.setUsageObservation(usage, true, false)
}
func (p *adapter) setUsageFinal(usage *SessionUsage) error {
	return p.setUsageObservation(usage, false, true)
}
func (p *adapter) setUsageObservation(usage *SessionUsage, baseline, final bool) error {
	if usage == nil {
		return nil
	}
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	if p.app.store != nil {
		return p.setSQLUsageLocked(usage, baseline, final)
	}
	s := p.app.state.session(p.id)
	if s == nil || (!p.subagent && p.app.runs[p.id] != p.turn) {
		return nil
	}
	if p.app.storageErr != nil {
		return p.app.storageErr
	}
	mutation := p.analyticsUsageLocked(usage, baseline, final)
	if mutation == nil && reflect.DeepEqual(s.Usage, usage) {
		return nil
	}
	// Token telemetry is also part of the stream hot path. Persist only this
	// snapshot rather than checkpointing every conversation for each usage tick.
	encoded, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	var owned SessionUsage
	if err := json.Unmarshal(encoded, &owned); err != nil {
		return err
	}
	record := streamRecord{Revision: p.app.state.GraphRevision + 1, Session: p.id, Updated: now(), Usage: &owned, Analytics: mutation}
	// Validate a small private shell before writing the single atomic frame.
	var next diskState
	if mutation != nil {
		a := p.app.state.Analytics
		e := a.Executions[mutation.ExecutionID]
		e.Usage = append([]AnalyticsUsageDelta(nil), e.Usage...)
		e.Reasons = append([]string(nil), e.Reasons...)
		next.Analytics = &AnalyticsState{Executions: map[string]AnalyticsExecution{mutation.ExecutionID: e}, Counters: map[string]AnalyticsCounter{}}
		if err := next.applyAnalyticsUsage(mutation, p.id); err != nil {
			return p.app.failStorageLocked(err)
		}
	}
	if err := appendStreamRecord(p.app.dir, record); err != nil {
		return p.app.failStorageLocked(err)
	}
	s.Usage, s.UpdatedAt = &owned, record.Updated
	if mutation != nil {
		p.app.state.Analytics.Executions[mutation.ExecutionID] = next.Analytics.Executions[mutation.ExecutionID]
		p.app.state.Analytics.Counters[mutation.Key] = mutation.Counter
	}
	p.app.state.GraphRevision = record.Revision
	p.app.notifySessionLocked(p.id)
	return nil
}

func (p *adapter) analyticsUsageLocked(usage *SessionUsage, baseline, final bool) *analyticsUsageMutation {
	a := p.app.state.Analytics
	if a == nil || p.subagent || p.analyticsNative == "" {
		return nil
	}
	e, ok := a.Executions[p.turn.analyticsID]
	if !ok {
		return nil
	}
	c := a.Counters[p.analyticsNative]
	return analyticsUsageFor(e, c, p.analyticsNative, usage, baseline, final)
}

func analyticsUsageFor(e AnalyticsExecution, c AnalyticsCounter, key string, usage *SessionUsage, baseline, final bool) *analyticsUsageMutation {
	// A persisted high-water mark does not prove a new turn's starting total.
	if c.ExecutionID != e.ID {
		c.ExecutionID = e.ID
		c.InputInvalid, c.OutputInvalid = false, false
	}
	m := &analyticsUsageMutation{ExecutionID: e.ID, Key: key, Counter: c, Delta: AnalyticsUsageDelta{Sequence: e.Sequence + 1, At: now()}, InputBaseline: e.InputBaseline, OutputBaseline: e.OutputBaseline, InputFinal: e.InputFinal, OutputFinal: e.OutputFinal}
	component := func(current *int64, previous **int64, ready *bool, invalid *bool, complete *bool) *int64 {
		if current == nil {
			if final {
				*complete = false
			}
			return nil
		}
		if baseline {
			if *previous != nil && *current < **previous {
				m.Reason = "counter_decrease"
			}
			*previous, *ready, *invalid = current, true, false
			return nil
		}
		if *invalid {
			return nil
		}
		if !*ready || *previous == nil {
			*previous, *ready = current, true
			m.Reason = "usage_start_unobserved"
			return nil
		}
		if *current < **previous {
			*invalid = true
			m.Reason = "counter_decrease"
			return nil
		}
		delta := *current - **previous
		*previous = current
		if final {
			*complete = true
		}
		if delta == 0 {
			return nil
		}
		return &delta
	}
	m.Delta.Input = component(usage.InputTokens, &m.Counter.Input, &m.InputBaseline, &m.Counter.InputInvalid, &m.InputFinal)
	m.Delta.Output = component(usage.OutputTokens, &m.Counter.Output, &m.OutputBaseline, &m.Counter.OutputInvalid, &m.OutputFinal)
	if reflect.DeepEqual(c, m.Counter) && m.Delta.Input == nil && m.Delta.Output == nil && m.InputBaseline == e.InputBaseline && m.OutputBaseline == e.OutputBaseline && m.InputFinal == e.InputFinal && m.OutputFinal == e.OutputFinal && m.Reason == "" {
		return nil
	}
	return m
}

func (p *adapter) clearContextUsage() error {
	if p.app.store != nil {
		var usage *SessionUsage
		err := p.app.store.readSnapshot(context.Background(), func(tx *sql.Tx) error {
			return decodeSQLiteRecord(context.Background(), tx, "session_usage", sqliteOwnedKey(p.id, ""), 64<<10, &usage)
		})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if usage != nil && usage.Context != nil {
			usage.Context = contextUsage(nil, usage.Context.Window)
		}
		return p.setUsage(usage)
	}
	p.app.mu.Lock()
	s := p.app.state.session(p.id)
	var next *SessionUsage
	if s != nil && s.Usage != nil {
		copy := *s.Usage
		if copy.Context != nil {
			copy.Context = contextUsage(nil, copy.Context.Window)
		}
		next = &copy
	}
	p.app.mu.Unlock()
	return p.setUsage(next)
}

func piSessionUsage(data map[string]any) *SessionUsage {
	tokens := object(data["tokens"])
	if tokens == nil {
		return nil
	}
	context := object(data["contextUsage"])
	return &SessionUsage{
		InputTokens:  sumTokens(tokenCount(tokens["input"]), tokenCount(tokens["cacheRead"]), tokenCount(tokens["cacheWrite"])),
		OutputTokens: tokenCount(tokens["output"]),
		Context:      contextUsage(tokenCount(context["tokens"]), tokenCount(context["contextWindow"])),
	}
}

func codexSessionUsage(data map[string]any) *SessionUsage {
	total, last := object(data["total"]), object(data["last"])
	if total == nil {
		return nil
	}
	// Codex input already includes cached input; output already includes reasoning.
	return &SessionUsage{
		InputTokens: tokenCount(total["inputTokens"]), OutputTokens: tokenCount(total["outputTokens"]),
		Context: contextUsage(tokenCount(last["totalTokens"]), tokenCount(data["modelContextWindow"])),
	}
}

// One entry per native message/step makes reconciliation and repeated SSE events
// idempotent. Step parts take precedence over the message's last-step usage.
type openCodeUsage struct {
	baseline                      bool
	baselineInput, baselineOutput *int64
	baselineLatest                map[string]any
	messages                      map[string]map[string]any
	steps                         map[string]map[string]map[string]any
	windows                       map[string]*int64
	compactedAt                   int64
}

func openCodeTokenCounts(tokens map[string]any) (input, output *int64) {
	cache := object(tokens["cache"])
	input = sumTokens(tokenCount(tokens["input"]), tokenCount(cache["read"]), tokenCount(cache["write"]))
	// OpenCode separates reasoning from output, unlike Pi and Codex.
	output = sumTokens(tokenCount(tokens["output"]), tokenCount(tokens["reasoning"]))
	return
}

func (u *openCodeUsage) message(info map[string]any) {
	if id := str(info, "id"); id != "" {
		u.messages[id] = compactOpenCodeInfo(info)
	}
}

func (u *openCodeUsage) part(part map[string]any) {
	if str(part, "type") != "step-finish" {
		return
	}
	messageID, partID := str(part, "messageID"), str(part, "id")
	if messageID == "" || partID == "" {
		return
	}
	if u.steps[messageID] == nil {
		u.steps[messageID] = map[string]map[string]any{}
	}
	u.steps[messageID][partID] = compactOpenCodeTokens(part["tokens"])
}

func (u *openCodeUsage) addBaseline(info map[string]any, value *SessionUsage) {
	if value == nil {
		return
	}
	if !u.baseline {
		u.baseline = true
		u.baselineInput = value.InputTokens
		u.baselineOutput = value.OutputTokens
	} else {
		u.baselineInput = sumTokens(u.baselineInput, value.InputTokens)
		u.baselineOutput = sumTokens(u.baselineOutput, value.OutputTokens)
	}
	created := tokenCount(object(info["time"])["created"])
	previous := tokenCount(object(u.baselineLatest["time"])["created"])
	if created != nil && (previous == nil || *created > *previous || *created == *previous && str(info, "id") > str(u.baselineLatest, "id")) {
		u.baselineLatest = compactOpenCodeInfo(info)
	}
}

func (u *openCodeUsage) snapshot() *SessionUsage {
	var inputs, outputs []*int64
	if u.baseline {
		inputs = append(inputs, u.baselineInput)
		outputs = append(outputs, u.baselineOutput)
	}
	latest := u.baselineLatest
	var latestTime int64 = -1
	if created := tokenCount(object(latest["time"])["created"]); created != nil {
		latestTime = *created
	}
	for id, info := range u.messages {
		if str(info, "role") != "assistant" {
			continue
		}
		steps := u.steps[id]
		if len(steps) > 0 {
			for _, tokens := range steps {
				input, output := openCodeTokenCounts(tokens)
				inputs, outputs = append(inputs, input), append(outputs, output)
			}
		} else if object(info["time"])["completed"] != nil {
			input, output := openCodeTokenCounts(object(info["tokens"]))
			inputs, outputs = append(inputs, input), append(outputs, output)
		} else {
			continue
		}
		created := tokenCount(object(info["time"])["created"])
		if created != nil && (*created > latestTime || (*created == latestTime && id > str(latest, "id"))) {
			latest, latestTime = info, *created
		}
	}
	if len(inputs) == 0 {
		return nil
	}
	var context *ContextUsage
	if latest != nil {
		input, output := openCodeTokenCounts(object(latest["tokens"]))
		used := sumTokens(input, output)
		// Summary usage describes the old context, not the compacted prompt.
		if truth(latest, "summary") || latestTime <= u.compactedAt || (used != nil && *used == 0) {
			used = nil
		}
		context = contextUsage(used, u.windows[str(latest, "providerID")+"/"+str(latest, "modelID")])
	}
	return &SessionUsage{InputTokens: sumTokens(inputs...), OutputTokens: sumTokens(outputs...), Context: context}
}
