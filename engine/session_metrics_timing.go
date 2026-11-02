package main

import (
	"strings"
	"time"
)

// Optional on new executions only. This is engine emission latency, including
// preparation and stream batching, not native generation or client rendering time.
// Other timing/retry metrics intentionally have no collection in this slice.
type ExecutionTiming struct {
	Source                 string  `json:"source"`
	FirstResponseAt        *string `json:"firstResponseAt"`
	FirstResponseLatencyMs *int64  `json:"firstResponseLatencyMs"`
}

// Stored in the same journal frame as the first exposed assistant content.
// Replay applies this explicit observation; it never scans old conversation text.
type executionResponseObservation struct {
	ExecutionID string `json:"executionId"`
	ObservedAt  string `json:"observedAt"`
}

func executionRole(s *Session) string {
	if s.Role != "" {
		return s.Role
	}
	if s.GraphRunID != "" {
		return "graph_node"
	}
	return "main"
}

func responseLatency(x *ExecutionMetrics, stamp string) *int64 {
	if x == nil || x.Outcome != nil || x.Timing == nil || x.Timing.FirstResponseAt != nil {
		return nil
	}
	start, e1 := time.Parse(time.RFC3339Nano, x.StartedAt)
	end, e2 := time.Parse(time.RFC3339Nano, stamp)
	if e1 != nil || e2 != nil || end.Before(start) {
		return nil
	}
	ms := end.Sub(start).Milliseconds()
	return &ms
}

func firstResponseObservation(s *Session, executionID, stamp string, changes []EventChange) *executionResponseObservation {
	if responseLatency(executionByID(s, executionID), stamp) == nil {
		return nil
	}
	for _, change := range changes {
		if change.Event.Type == "assistant" && strings.TrimSpace(change.Event.Text) != "" {
			return &executionResponseObservation{ExecutionID: executionID, ObservedAt: stamp}
		}
	}
	return nil
}

func applyResponseObservation(s *Session, observation *executionResponseObservation) {
	if observation == nil {
		return
	}
	x := executionByID(s, observation.ExecutionID)
	if ms := responseLatency(x, observation.ObservedAt); ms != nil {
		stamp := observation.ObservedAt
		x.Timing.FirstResponseAt, x.Timing.FirstResponseLatencyMs = &stamp, ms
	}
}

// Counts only recorded terminal interruptions. Cancellation without a durable
// reason is not classified as Stop, replacement or retry. Graphs are not traversed.
type metricsInterruptions struct {
	Total       int64 `json:"total"`
	Stop        int64 `json:"stop"`
	Replacement int64 `json:"replacement"`
	Restart     int64 `json:"restart"`
	Shutdown    int64 `json:"shutdown"`
	Other       int64 `json:"other"`
}

func (m *metricsInterruptions) observe(x ExecutionMetrics) {
	if x.Outcome == nil {
		return
	}
	switch x.InterruptionReason {
	case "stop":
		m.Stop++
	case "replacement":
		m.Replacement++
	case "engine_restart":
		m.Restart++
	case "engine_shutdown":
		m.Shutdown++
	default:
		if *x.Outcome != "interrupted" {
			return
		}
		m.Other++
	}
	m.Total++
}
