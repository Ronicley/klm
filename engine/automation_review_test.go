package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAutomationReviewGeneralHarnessLock(t *testing.T) {
	for _, test := range []struct {
		name                                                           string
		locked, human, queued, removed, legacyNotice, legacyTurn, want bool
	}{
		{name: "first-use notice"},
		{name: "legacy notice before input", legacyNotice: true},
		{name: "repair notice-only migration", locked: true, legacyNotice: true},
		{name: "accepted human input", human: true, want: true},
		{name: "accepted queued input", queued: true, want: true},
		{name: "removed accepted input", removed: true, want: true},
		{name: "failed legacy native turn", legacyTurn: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			notice := Event{ID: "notice", Type: "status", Data: map[string]any{"automationRecoveryNotice": true}}
			if test.legacyNotice {
				notice.Data = nil
			}
			d := diskState{Sessions: []Session{{ID: "general", Role: sessionRoleGeneralAgent, Harness: "pi", HarnessLocked: test.locked, Events: []Event{notice}}}, AutomationRecoveries: []AutomationRecovery{{NoticeEventID: "notice"}}}
			s := &d.Sessions[0]
			if test.human {
				s.Events = append(s.Events, Event{ID: "human", Type: "user"})
			}
			if test.queued {
				s.Queue = []QueuedMessage{{ID: "queued", Status: "queued"}}
			}
			if test.removed {
				d.AcceptedMessages = map[string]string{"general/removed": "fingerprint"}
			}
			if test.legacyTurn {
				s.Events = append(s.Events, Event{Type: "error", Text: "Execution failed."})
			}
			d.normalizeGeneralHarnessLock(s)
			if s.HarnessLocked != test.want {
				t.Fatalf("lock before restart: got %v want %v", s.HarnessLocked, test.want)
			}
			if !test.want {
				s.Harness = "opencode"
			} // A notice is not first-use selection.
			encoded, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			var restarted diskState
			if err = json.Unmarshal(encoded, &restarted); err != nil {
				t.Fatal(err)
			}
			restarted.normalizeGeneralHarnessLock(&restarted.Sessions[0])
			if restarted.Sessions[0].HarnessLocked != test.want {
				t.Fatalf("lock after restart: got %v want %v", restarted.Sessions[0].HarnessLocked, test.want)
			}
			if !test.want && restarted.Sessions[0].Harness != "opencode" {
				t.Fatal("pre-input choice was not preserved")
			}
		})
	}
}

func TestAutomationReviewRecoveryOccurrences(t *testing.T) {
	r := AutomationRecovery{ID: "recovery", Source: "missed_schedule", From: "2026-10-20T12:00:00Z", Until: "2026-10-22T15:00:00Z", TimeZone: "America/Sao_Paulo", Delivery: "pending", Snapshot: Automation{ID: "automation", Schedule: AutomationSchedule{Time: "09:00", Recurrence: "daily"}}}
	first, cursor, more, err := recoveryOccurrencePage(&r, "", 2)
	if err != nil || len(first) != 2 || !more {
		t.Fatalf("first page: %v, %v, %v", first, more, err)
	}
	last, _, more, err := recoveryOccurrencePage(&r, cursor, 2)
	if err != nil || len(last) != 1 || more || last[0].ScheduledAt != "2026-10-22T12:00:00Z" {
		t.Fatalf("last page: %v, %v, %v", last, more, err)
	}
	d := diskState{EngineSettings: EngineSettings{TimeZone: "Asia/Tokyo"}, AutomationRecoveries: []AutomationRecovery{r}}
	args := automationRecoveryResolveArgs{OccurrenceIDs: []string{last[0].ID, first[0].ID}, Action: "recover", OperationID: "recover-two", SourceUserEventID: "human"}
	items, repeated, err := planRecoveryResolution(&d.AutomationRecoveries[0], &args)
	if err != nil || repeated {
		t.Fatal(err)
	}
	applyRecoveryResolution(&d, &d.AutomationRecoveries[0], args, items)
	if len(d.AutomationTriggers) != 2 || len(d.Sessions) != 0 || d.AutomationRecoveries[0].Resolved {
		t.Fatal("selection must queue exactly two occurrences without creating sessions or resolving the middle one")
	}
	if d.AutomationTriggers[0].At != first[0].ScheduledAt || d.AutomationTriggers[1].At != last[0].ScheduledAt {
		t.Fatal("lost original occurrence times/order")
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var restarted diskState
	if err = json.Unmarshal(encoded, &restarted); err != nil {
		t.Fatal(err)
	}
	d = restarted
	if d.AutomationRecoveries[0].TimeZone != "America/Sao_Paulo" || d.AutomationRecoveries[0].NextOccurrenceAt != first[1].ScheduledAt {
		t.Fatal("saved zone or unresolved occurrence was lost across restart")
	}
	if _, repeated, err = planRecoveryResolution(&d.AutomationRecoveries[0], &args); err != nil || !repeated {
		t.Fatal("identical decision must return its receipt", err)
	}
	conflict := args
	conflict.OccurrenceIDs = []string{first[1].ID}
	if _, _, err = planRecoveryResolution(&d.AutomationRecoveries[0], &conflict); err == nil {
		t.Fatal("changed selection reused an operationId")
	}
	middle := automationRecoveryResolveArgs{OccurrenceIDs: []string{first[1].ID}, Action: "discard", OperationID: "discard-middle", SourceUserEventID: "human"}
	items, _, err = planRecoveryResolution(&d.AutomationRecoveries[0], &middle)
	if err != nil {
		t.Fatal(err)
	}
	applyRecoveryResolution(&d, &d.AutomationRecoveries[0], middle, items)
	if !d.AutomationRecoveries[0].Resolved || len(d.AutomationTriggers) != 2 {
		t.Fatal("discard must resolve only its own occurrence")
	}
	all := automationRecoveryResolveArgs{OccurrenceIDs: []string{first[0].ID, first[1].ID, last[0].ID}, Action: "recover", OperationID: "recover-all", SourceUserEventID: "human"}
	fresh := diskState{AutomationRecoveries: []AutomationRecovery{r}}
	items, _, err = planRecoveryResolution(&fresh.AutomationRecoveries[0], &all)
	if err != nil {
		t.Fatal(err)
	}
	applyRecoveryResolution(&fresh, &fresh.AutomationRecoveries[0], all, items)
	if len(fresh.AutomationTriggers) != 3 {
		t.Fatal("three explicitly recovered times must remain three queued triggers")
	}
	legacy := r
	legacy.TimeZone = ""
	if _, _, _, err := recoveryOccurrencePage(&legacy, "", 50); err == nil {
		t.Fatal("an unknown legacy zone must not silently adopt engine settings")
	}
	unknown := automationRecoveryResolveArgs{OccurrenceIDs: all.OccurrenceIDs, Action: "recover", OperationID: "legacy", SourceUserEventID: "human"}
	if _, _, err := planRecoveryResolution(&legacy, &unknown); err == nil {
		t.Fatal("legacy resolution needs an explicit original time zone")
	}
	unknown.TimeZone = "America/Sao_Paulo"
	if _, _, err := planRecoveryResolution(&legacy, &unknown); err != nil {
		t.Fatal(err)
	}
}

func TestAutomationReviewWebhookReferences(t *testing.T) {
	v := Automation{Name: "Review", Prompt: "Review PR {{pr.number}} in {{repository.name}}", Mode: "webhook", Harness: "opencode"}
	if err := validateAutomationConfig(&v); err != nil {
		t.Fatal(err)
	}
	v.Mode = "schedule"
	v.Schedule = AutomationSchedule{Time: "09:00", Recurrence: "daily"}
	if err := validateAutomationConfig(&v); err == nil || !strings.Contains(err.Error(), "cannot use webhook references") {
		t.Fatal("schedule must reject webhook references", err)
	}
	if !strings.Contains(v.Prompt, "{{pr.number}}") {
		t.Fatal("validation must not erase prompt text")
	}
	run := AutomationRun{Snapshot: Automation{Prompt: "{{repository.name}} / {{pr.number}}"}, Trigger: AutomationTrigger{Variables: map[string]string{"repository.name": "{{pr.number}}", "pr.number": "42"}}}
	if got := automationTask(&run); got != "{{pr.number}} / 42" {
		t.Fatal("webhook substitution must be single-pass", got)
	}
}
