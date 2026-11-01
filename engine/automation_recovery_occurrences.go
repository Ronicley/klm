package main

import (
	"errors"
	"slices"
	"strings"
	"time"
)

type automationRecoveryResolveArgs struct {
	TimeZone          string   `json:"timeZone,omitempty"`
	RecoveryID        string   `json:"recoveryId"`
	OccurrenceIDs     []string `json:"occurrenceIds"`
	Action            string   `json:"action"`
	SourceUserEventID string   `json:"sourceUserEventId"`
	OperationID       string   `json:"operationId"`
}

func recoveryPending(r *AutomationRecovery) bool { return r.Action == "" && !r.Resolved }
func recoveryZone(r *AutomationRecovery) string {
	if r.TimeZone == "" {
		if r.Source == "missed_schedule" {
			return ""
		}
		return "UTC"
	}
	return r.TimeZone
}
func recoveryOccurrence(r *AutomationRecovery, at time.Time) AutomationRecoveryOccurrence {
	id := r.ID
	if r.Source == "missed_schedule" {
		id += "/" + at.UTC().Format(time.RFC3339Nano)
	}
	item := AutomationRecoveryOccurrence{ID: id, ScheduledAt: at.UTC().Format(time.RFC3339Nano)}
	for _, decision := range r.OccurrenceDecisions {
		if decision.ID == id {
			item.Action = decision.Action
			break
		}
	}
	return item
}

// The persisted interval, calendar and zone define stable occurrence identities.
// Only explicit decisions are materialized; enumerating downtime starts no work.
func recoveryOccurrencePage(r *AutomationRecovery, after string, limit int) ([]AutomationRecoveryOccurrence, string, bool, error) {
	if r.Source == "missed_schedule" && r.TimeZone == "" {
		return nil, "", false, errors.New("This legacy interval has no saved time zone. Ask the user for its original IANA time zone before enumerating or resolving occurrences.")
	}
	start, err := time.Parse(time.RFC3339Nano, r.From)
	if err != nil {
		return nil, "", false, err
	}
	end, err := time.Parse(time.RFC3339Nano, r.Until)
	if err != nil {
		return nil, "", false, err
	}
	at := start
	if after != "" {
		cursor, err := time.Parse(time.RFC3339Nano, after)
		if err != nil {
			return nil, "", false, errors.New("Invalid occurrence cursor.")
		}
		if !cursor.Before(start) {
			if r.Source != "missed_schedule" {
				return []AutomationRecoveryOccurrence{}, "", false, nil
			}
			at = nextAutomationOccurrence(r.Snapshot.Schedule, recoveryZone(r), cursor)
		}
	}
	items := []AutomationRecoveryOccurrence{}
	next := ""
	for !at.IsZero() && !at.After(end) && len(items) < limit {
		items = append(items, recoveryOccurrence(r, at))
		next = at.Format(time.RFC3339Nano)
		if r.Source != "missed_schedule" {
			return items, next, false, nil
		}
		at = nextAutomationOccurrence(r.Snapshot.Schedule, recoveryZone(r), at)
	}
	return items, next, !at.IsZero() && !at.After(end), nil
}

func planRecoveryResolution(r *AutomationRecovery, args *automationRecoveryResolveArgs) ([]AutomationRecoveryOccurrence, bool, error) {
	if r.Source == "missed_schedule" {
		if r.TimeZone != "" && args.TimeZone != "" && args.TimeZone != r.TimeZone {
			return nil, false, errors.New("The saved recovery time zone cannot be changed.")
		}
		if args.TimeZone == "" {
			args.TimeZone = r.TimeZone
		}
		if args.TimeZone == "" || args.TimeZone == "Local" {
			return nil, false, errors.New("The legacy interval needs its original IANA time zone from the user's explicit decision.")
		}
		if _, err := time.LoadLocation(args.TimeZone); err != nil {
			return nil, false, err
		}
		view := *r
		view.TimeZone = args.TimeZone
		r = &view
	}
	if len(args.OccurrenceIDs) == 0 && r.Source != "missed_schedule" {
		args.OccurrenceIDs = []string{r.ID}
	}
	if len(args.OccurrenceIDs) == 0 || len(args.OccurrenceIDs) > 50 {
		return nil, false, errors.New("Select 1 to 50 explicit occurrenceIds from automation_recovery_read.")
	}
	args.OccurrenceIDs = slices.Clone(args.OccurrenceIDs)
	slices.Sort(args.OccurrenceIDs)
	for _, receipt := range r.Resolutions {
		if receipt.OperationID != args.OperationID {
			continue
		}
		if receipt.Action != args.Action || receipt.UserEventID != args.SourceUserEventID || receipt.TimeZone != args.TimeZone || !slices.Equal(receipt.OccurrenceIDs, args.OccurrenceIDs) {
			return nil, false, errors.New("operationId already belongs to a different recovery decision.")
		}
		return nil, true, nil
	}
	if !recoveryPending(r) {
		return nil, false, errors.New("Recovery is already resolved.")
	}
	start, err := time.Parse(time.RFC3339Nano, r.From)
	if err != nil {
		return nil, false, err
	}
	end, err := time.Parse(time.RFC3339Nano, r.Until)
	if err != nil {
		return nil, false, err
	}
	items := []AutomationRecoveryOccurrence{}
	for i, id := range args.OccurrenceIDs {
		if i > 0 && id == args.OccurrenceIDs[i-1] {
			return nil, false, errors.New("Duplicate occurrence ID.")
		}
		at := start
		if r.Source == "missed_schedule" {
			stamp, ok := strings.CutPrefix(id, r.ID+"/")
			if !ok {
				return nil, false, errors.New("Occurrence does not belong to this recovery.")
			}
			at, err = time.Parse(time.RFC3339Nano, stamp)
			if err != nil || at.Before(start) || at.After(end) || !at.Equal(start) && !nextAutomationOccurrence(r.Snapshot.Schedule, recoveryZone(r), at.Add(-time.Nanosecond)).Equal(at) {
				return nil, false, errors.New("Occurrence is not in the saved missed schedule.")
			}
		}
		item := recoveryOccurrence(r, at)
		if item.ID != id {
			return nil, false, errors.New("Invalid occurrence identity.")
		}
		if item.Action != "" {
			return nil, false, errors.New("An occurrence is already resolved; read the remaining choices.")
		}
		items = append(items, item)
	}
	return items, false, nil
}

func applyRecoveryResolution(d *diskState, r *AutomationRecovery, args automationRecoveryResolveArgs, items []AutomationRecoveryOccurrence) {
	if r.Source == "missed_schedule" && r.TimeZone == "" {
		r.TimeZone = args.TimeZone
	}
	for _, item := range items {
		item.Action = args.Action
		r.OccurrenceDecisions = append(r.OccurrenceDecisions, item)
		if args.Action == "recover" {
			snapshot := copyAutomation(r.Snapshot)
			triggerID := enqueueAutomation(d, snapshot, "recovery/"+item.ID, "recovery", item.ScheduledAt, r.Variables, &snapshot)
			for j := range d.AutomationTriggers {
				if d.AutomationTriggers[j].ID == triggerID {
					d.AutomationTriggers[j].Capabilities = r.Capabilities
				}
			}
		}
	}
	r.Resolutions = append(r.Resolutions, AutomationRecoveryResolution{TimeZone: args.TimeZone, OperationID: args.OperationID, Action: args.Action, UserEventID: args.SourceUserEventID, OccurrenceIDs: slices.Clone(args.OccurrenceIDs)})
	if r.Source != "missed_schedule" {
		r.Action, r.OperationID, r.UserEventID = args.Action, args.OperationID, args.SourceUserEventID
		r.Resolved = true
		return
	}
	stamp := r.NextOccurrenceAt
	if stamp == "" {
		stamp = r.From
	}
	at, _ := time.Parse(time.RFC3339Nano, stamp)
	end, _ := time.Parse(time.RFC3339Nano, r.Until)
	for !at.IsZero() && !at.After(end) && recoveryOccurrence(r, at).Action != "" {
		at = nextAutomationOccurrence(r.Snapshot.Schedule, recoveryZone(r), at)
	}
	r.Resolved = at.IsZero() || at.After(end)
	if !r.Resolved {
		r.NextOccurrenceAt = at.Format(time.RFC3339Nano)
	} else {
		r.NextOccurrenceAt = ""
	}
}

func validateRecoveryOccurrences(r *AutomationRecovery) error {
	if r.TimeZone != "" {
		if r.TimeZone == "Local" {
			return errors.New("Invalid recovery time zone.")
		}
		if _, err := time.LoadLocation(r.TimeZone); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, decision := range r.OccurrenceDecisions {
		if decision.ID == "" || seen[decision.ID] || decision.Action != "recover" && decision.Action != "discard" {
			return errors.New("Invalid recovery occurrence decision.")
		}
		if _, err := time.Parse(time.RFC3339Nano, decision.ScheduledAt); err != nil {
			return err
		}
		seen[decision.ID] = true
	}
	operations := map[string]bool{}
	for _, receipt := range r.Resolutions {
		if receipt.OperationID == "" || operations[receipt.OperationID] || receipt.UserEventID == "" || len(receipt.OccurrenceIDs) == 0 || len(receipt.OccurrenceIDs) > 50 || receipt.Action != "recover" && receipt.Action != "discard" {
			return errors.New("Invalid recovery resolution receipt.")
		}
		operations[receipt.OperationID] = true
		for _, id := range receipt.OccurrenceIDs {
			if !seen[id] {
				return errors.New("Recovery receipt has no occurrence decision.")
			}
		}
	}
	return nil
}
