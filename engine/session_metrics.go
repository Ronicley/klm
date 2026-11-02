package main

import "time"

// Execution history belongs to the conversation, so normal deletion removes it.
// Legacy conversations intentionally have no inferred executions.
type ExecutionMetrics struct {
	ID                 string               `json:"id"`
	StartedAt          string               `json:"startedAt"`
	EndedAt            *string              `json:"endedAt"`
	ObservedAt         string               `json:"observedAt"`
	Outcome            *string              `json:"outcome"`
	InterruptionReason string               `json:"interruptionReason,omitempty"`
	DurationMs         int64                `json:"durationMs"`
	Partial            bool                 `json:"partial"`
	Harness            string               `json:"harness"`
	Models             []string             `json:"models"`
	RequestedModel     string               `json:"requestedModel,omitempty"`
	Source             string               `json:"source"`
	Accounting         *ExecutionAccounting `json:"accounting,omitempty"`
	Role               string               `json:"role,omitempty"`
	Timing             *ExecutionTiming     `json:"timing,omitempty"`
}

type SessionMetrics struct {
	// Since marks the start of observation, not the beginning of the native history.
	Since      string             `json:"since"`
	Executions []ExecutionMetrics `json:"executions"`
}

func elapsedMilliseconds(start, end string) int64 {
	a, e1 := time.Parse(time.RFC3339Nano, start)
	b, e2 := time.Parse(time.RFC3339Nano, end)
	if e1 != nil || e2 != nil || b.Before(a) {
		return 0
	}
	return b.Sub(a).Milliseconds()
}

func executionByID(s *Session, id string) *ExecutionMetrics {
	if s == nil || s.Metrics == nil || id == "" {
		return nil
	}
	for i := range s.Metrics.Executions {
		if s.Metrics.Executions[i].ID == id {
			return &s.Metrics.Executions[i]
		}
	}
	return nil
}

func startExecutionMetrics(s *Session, id string) {
	if executionByID(s, id) != nil {
		return
	}
	stamp := now()
	if s.Metrics == nil {
		s.Metrics = &SessionMetrics{Since: stamp, Executions: []ExecutionMetrics{}}
	}
	s.Metrics.Executions = append(s.Metrics.Executions, ExecutionMetrics{
		ID: id, StartedAt: stamp, ObservedAt: stamp, Harness: s.Harness,
		RequestedModel: s.Model, Models: []string{}, Source: "engine",
		Accounting: newExecutionAccounting(s.Harness), Role: executionRole(s),
		Timing: &ExecutionTiming{Source: "engine_visible_emit"},
	})
}

func finishExecutionMetrics(s *Session, id, outcome, reason string) {
	x := executionByID(s, id)
	if x == nil || x.Outcome != nil {
		return
	}
	stamp := now()
	x.EndedAt, x.Outcome, x.ObservedAt = &stamp, &outcome, stamp
	x.DurationMs = elapsedMilliseconds(x.StartedAt, stamp)
	x.InterruptionReason = reason
	settleExecutionAccounting(x.Accounting, outcome)
}

// Called after journal replay, before recovery changes UpdatedAt. Restart time
// is not an observed end time, and downtime never contributes to duration.
func interruptExecutionMetrics(d *diskState) bool {
	changed := false
	for i := range d.Sessions {
		s := &d.Sessions[i]
		if s.Metrics == nil {
			continue
		}
		for j := range s.Metrics.Executions {
			x := &s.Metrics.Executions[j]
			if x.Outcome != nil {
				continue
			}
			outcome := "interrupted"
			x.Outcome, x.Partial = &outcome, true
			x.InterruptionReason = "engine_restart"
			settleExecutionAccounting(x.Accounting, outcome)
			if elapsedMilliseconds(x.StartedAt, s.UpdatedAt) > elapsedMilliseconds(x.StartedAt, x.ObservedAt) {
				x.ObservedAt = s.UpdatedAt
			}
			x.DurationMs = elapsedMilliseconds(x.StartedAt, x.ObservedAt)
			changed = true
		}
	}
	return changed
}
