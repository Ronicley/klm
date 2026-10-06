package main

import (
	"encoding/json"
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
	if usage == nil {
		return nil
	}
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	s := p.app.state.session(p.id)
	if s == nil || (!p.subagent && p.app.runs[p.id] != p.turn) || reflect.DeepEqual(s.Usage, usage) {
		return nil
	}
	if p.app.storageErr != nil {
		return p.app.storageErr
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
	record := streamRecord{Revision: p.app.state.GraphRevision + 1, Session: p.id, Updated: now(), Usage: &owned}
	if err := appendStreamRecord(p.app.dir, record); err != nil {
		return p.app.failStorageLocked(err)
	}
	s.Usage, s.UpdatedAt = &owned, record.Updated
	p.app.state.GraphRevision = record.Revision
	p.app.notifySessionLocked(p.id)
	return nil
}

func (p *adapter) clearContextUsage() error {
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
	messages    map[string]map[string]any
	steps       map[string]map[string]map[string]any
	windows     map[string]*int64
	compactedAt int64
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
		u.messages[id] = info
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
	u.steps[messageID][partID] = object(part["tokens"])
}

func (u *openCodeUsage) snapshot() *SessionUsage {
	var inputs, outputs []*int64
	var latest map[string]any
	var latestTime int64 = -1
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
