package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Separate from Session.Usage: these observations never update lifetime totals.
// Native parent accounting may include delegated work; model allocation is unknown.
type ExecutionAccounting struct {
	Version          int                     `json:"version"`
	Protocol         string                  `json:"protocol"`
	HarnessVersion   string                  `json:"harnessVersion,omitempty"`
	Epoch            string                  `json:"epoch"`
	NativeID         string                  `json:"nativeId,omitempty"`
	NativePath       string                  `json:"nativePath,omitempty"`
	Coverage         string                  `json:"coverage"`
	Reason           string                  `json:"reason,omitempty"`
	Baseline         *ExecutionUsageEvidence `json:"baseline"`
	Endpoint         *ExecutionUsageEvidence `json:"endpoint"`
	Usage            *ExecutionTokenUsage    `json:"usage"`
	Cost             *ExecutionMoney         `json:"cost"`
	CostReason       string                  `json:"costReason"`
	ModelAttribution string                  `json:"modelAttribution"`
	Invalidated      bool                    `json:"invalidated"`
}

// Reserved exact-decimal contract. No adapter in this slice emits money.
type ExecutionMoney struct {
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	Source    string `json:"source"`
	Valuation string `json:"valuation"`
}

type ExecutionTokenUsage struct {
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	ReasoningTokens  *int64 `json:"reasoningTokens"`
}

type ExecutionUsageEvidence struct {
	Key        string `json:"key"`
	ObservedAt string `json:"observedAt"`
	Source     string `json:"source"`
	NativeID   string `json:"nativeId"`
	NativePath string `json:"nativePath"`
	Input      int64  `json:"input"` // uncached Pi input
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cacheRead"`
	CacheWrite int64  `json:"cacheWrite"`
	Terminal   bool   `json:"terminal"`
}

func newExecutionAccounting(harness string) *ExecutionAccounting {
	reason := "adapter_not_instrumented"
	protocol := harness
	if harness == "pi" {
		protocol, reason = "pi-rpc", "baseline_not_observed"
	} else if harness == "codex" {
		reason = "trustworthy_boundary_baseline_unavailable"
	}
	return &ExecutionAccounting{Version: 1, Protocol: protocol, Epoch: newID(),
		Coverage: "unavailable", Reason: reason, CostReason: "currency_provenance_not_verified_for_runtime",
		ModelAttribution: "unavailable"}
}

func (a *ExecutionAccounting) gap(reason string, invalid bool) {
	if a.Invalidated {
		return
	} // Preserve the first identity/reset/gap failure.
	a.Reason = reason
	a.Invalidated = invalid
	if invalid {
		a.Usage = nil
	}
	if a.Usage == nil {
		a.Coverage = "unavailable"
	} else {
		a.Coverage = "partial"
	}
}

func settleExecutionAccounting(a *ExecutionAccounting, outcome string) {
	if a == nil {
		return
	}
	if outcome != "completed" && a.Coverage == "complete" {
		a.gap("execution_"+outcome, false)
	} else if a.Coverage != "complete" && a.Usage != nil {
		a.Coverage = "partial"
	}
}

func piAccountingVersion(b binary) string {
	if len(b.args) != 1 || filepath.Base(b.args[0]) != "cli.js" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(b.args[0])), "package.json"))
	var metadata struct {
		Version string `json:"version"`
	}
	if err != nil || json.Unmarshal(data, &metadata) != nil {
		return ""
	}
	return metadata.Version
}

func piUsageEvidence(data map[string]any, key string, terminal bool) *ExecutionUsageEvidence {
	t := object(data["tokens"])
	input, output := tokenCount(t["input"]), tokenCount(t["output"])
	read, write := tokenCount(t["cacheRead"]), tokenCount(t["cacheWrite"])
	total := sumTokens(input, output, read, write)
	if total == nil {
		return nil
	}
	if raw, present := t["total"]; present {
		check := tokenCount(raw)
		if check == nil || *check != *total {
			return nil
		}
	}
	return &ExecutionUsageEvidence{Key: key, ObservedAt: now(), Source: "pi.get_session_stats",
		NativeID: str(data, "sessionId"), NativePath: str(data, "sessionFile"),
		Input: *input, Output: *output, CacheRead: *read, CacheWrite: *write, Terminal: terminal}
}

func piEvidenceDecreased(a, b *ExecutionUsageEvidence) bool {
	return b.Input < a.Input || b.Output < a.Output || b.CacheRead < a.CacheRead || b.CacheWrite < a.CacheWrite
}

func (a *ExecutionAccounting) observePi(data map[string]any, key string, baseline, terminal bool) {
	if a.Invalidated {
		return
	}
	if str(data, "sessionId") != a.NativeID || !piSamePath(str(data, "sessionFile"), a.NativePath) {
		a.gap("native_identity_mismatch", true)
		return
	}
	e := piUsageEvidence(data, key, terminal)
	if e == nil {
		a.gap("invalid_or_missing_counters", true)
		return
	}
	if baseline {
		// Exactly one pre-prompt read. Never adopt an in-turn snapshot as a baseline.
		if a.Baseline == nil {
			a.Baseline = e
		}
		a.Reason = "final_not_observed"
		return
	}
	if a.Baseline == nil {
		return
	}
	previous := a.Baseline
	if a.Endpoint != nil {
		previous = a.Endpoint
	}
	if piEvidenceDecreased(previous, e) {
		a.gap("counter_reset_or_history_rewrite", true)
		return
	}
	// Replace the endpoint, then recompute once from the baseline, not the last tick.
	a.Endpoint = e
	b := a.Baseline
	input, read, write := e.Input-b.Input, e.CacheRead-b.CacheRead, e.CacheWrite-b.CacheWrite
	normalized := sumTokens(&input, &read, &write)
	if normalized == nil {
		a.gap("counter_overflow", true)
		return
	}
	a.Usage = &ExecutionTokenUsage{InputTokens: *normalized, OutputTokens: e.Output - b.Output,
		CacheReadTokens: read, CacheWriteTokens: write}
	a.Coverage, a.Reason = "partial", "final_not_observed"
	if terminal {
		a.Coverage, a.Reason = "complete", ""
	}
}

// Optional accounting uses existing owning-session transactions. Never return a
// telemetry error to the harness. Essential storage failure retains normal engine
// fail-closed behavior (commitLocked), not an independent metrics persistence store.
func (p *adapter) saveExecutionAccounting(a *ExecutionAccounting) {
	if p.turn == nil || p.subagent {
		return
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		return
	}
	var owned ExecutionAccounting
	if json.Unmarshal(encoded, &owned) != nil {
		return
	}
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	s := p.app.state.session(p.id)
	if p.app.runs[p.id] != p.turn {
		return
	}
	x := executionByID(s, p.turn.metricsID)
	if x == nil || x.Outcome != nil {
		return
	}
	_ = p.app.commitLocked(func(d *diskState) {
		x := executionByID(d.session(p.id), p.turn.metricsID)
		x.Accounting = &owned
		x.ObservedAt = now()
	})
}
