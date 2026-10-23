package main

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type GraphTraversalLimit int32

func (limit *GraphTraversalLimit) UnmarshalYAML(node *yaml.Node) error {
	// yaml.v3 otherwise silently truncates fractional numbers into integer fields.
	if node.Tag != "!!int" {
		return errors.New("Choice max_traversals must be an integer.")
	}
	var value int32
	if err := node.Decode(&value); err != nil {
		return err
	}
	*limit = GraphTraversalLimit(value)
	return nil
}

type GraphTransitionLimit struct {
	Limit      int `json:"limit"`
	Traversals int `json:"traversals"`
	Remaining  int `json:"remaining"`
}

// Deliveries are durable, unique per producer/connection, and retained in retry
// checkpoints. Counting them avoids a second counter that can drift on recovery.
func (d *diskState) graphTransitionLimits(r *GraphRun) map[string]GraphTransitionLimit {
	limits := map[string]GraphTransitionLimit{}
	for id, choice := range r.Snapshot.Graph.Definition.Choices {
		if choice.MaxTraversals != nil {
			limits[id] = GraphTransitionLimit{Limit: int(*choice.MaxTraversals), Remaining: int(*choice.MaxTraversals)}
		}
	}
	for _, delivery := range d.GraphDeliveries {
		if delivery.RunID != r.ID {
			continue
		}
		id := strings.TrimPrefix(delivery.ConnectionID, "choice:")
		if limit, ok := limits[id]; ok && delivery.ConnectionID == "choice:"+id {
			limit.Traversals++
			limit.Remaining = max(0, limit.Limit-limit.Traversals)
			limits[id] = limit
		}
	}
	return limits
}

// Called in the same locked transaction that would append the next delivery.
func (d *diskState) graphTransitionLimitResult(r *GraphRun, x *GraphActivation) *GraphRunResult {
	id := x.Submission.Choice.ID
	limit, ok := d.graphTransitionLimits(r)[id]
	if !ok || limit.Remaining > 0 {
		return nil
	}
	reason := fmt.Sprintf("Transition %s reached its limit of %d traversals. No further work was dispatched.", id, limit.Limit)
	output := make(map[string]string, len(x.Output)+2)
	for key, value := range x.Output {
		output[key] = value
	}
	output["status"], output["reason"] = "transition_limit_reached", reason
	return &GraphRunResult{Kind: "blocked", Choice: &ChoiceIdentity{Origin: "engine", ID: "blocked"}, Output: output, Error: reason}
}
