package main

import (
	"context"
	"errors"
	"time"
)

func (a *app) automationWorker() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		a.mu.Lock()
		if !a.closing && a.storageErr == nil {
			a.scheduleAutomationsLocked()
			a.scheduleAutomationRecoveryLocked()
		}
		a.mu.Unlock()
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *app) startAutomationTurnLocked(r *AutomationRun, text string) {
	if a.harnessInstalling {
		return
	}
	s := a.state.session(r.SessionID)
	if s == nil || a.runs[s.ID] != nil || a.state.conversationArchived(s) {
		return
	}
	s = a.state.automationSessionView(s)
	folder, available := a.state.conversationDirectory(s)
	b, installed := a.binaries[s.Harness]
	cwd, err := existingDirectory(folder)
	if !available || !installed || err != nil {
		_ = a.commitLocked(func(d *diskState) {
			finishAutomation(d, d.automationRun(r.ID), "execution_failed", "Project directory or harness is unavailable.")
		})
		return
	}
	if err := a.commitLocked(func(d *diskState) {
		next := d.session(s.ID)
		next.Status, next.UpdatedAt = "running", now()
		d.automationRun(r.ID).NeedsContinuation = false
	}); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	t := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	a.runs[s.ID] = t
	a.wg.Add(1)
	go a.execute(t, *a.state.automationSessionView(a.state.session(s.ID)), a.state.Native[s.ID], b, cwd, submission{Text: text})
}

func automationWaiting(d *diskState, sessionID string) bool {
	if d.activeGraphRun(sessionID) != nil {
		return true
	}
	for _, c := range d.Consultations {
		if c.From == sessionID && (c.Delivery == "waiting" || c.Delivery == "pending" || c.Delivery == "delivering") {
			return true
		}
	}
	for _, n := range d.GraphNotifications {
		if n.ConversationID == sessionID && (n.Status == "pending" || n.Status == "delivering") {
			return true
		}
	}
	return false
}

func (a *app) scheduleAutomationsLocked() {
	if a.store != nil {
		if err := a.scheduleSQLAutomationsLocked(); err != nil && a.ctx.Err() == nil {
			a.failStorageLocked(err)
		}
		return
	}
	at := time.Now().UTC()
	for _, v := range a.state.Automations {
		p := a.state.project(v.ProjectID)
		if v.Deleted || !v.Enabled || v.Mode != "schedule" || v.NextScheduledAt == "" || p == nil || p.Removed {
			continue
		}
		due, err := time.Parse(time.RFC3339Nano, v.NextScheduledAt)
		if err != nil || due.After(at) {
			continue
		}
		if err := a.commitLocked(func(d *diskState) {
			current := d.automation(v.ID)
			enqueueAutomation(d, *current, "schedule/"+current.NextScheduledAt, "schedule", current.NextScheduledAt, nil, nil)
			if current.Schedule.Recurrence == "once" {
				current.OnceTriggeredAt = current.NextScheduledAt
			}
			next := nextAutomationOccurrence(current.Schedule, d.engineZone(), due)
			current.NextScheduledAt = ""
			if !next.IsZero() {
				current.NextScheduledAt = next.Format(time.RFC3339Nano)
			}
		}); err != nil {
			return
		}
	}
	// Continue harvesting due occurrences while installation suspends execution.
	// Do not consume a trigger, continuation reminder or delivery receipt yet.
	if a.harnessInstalling {
		return
	}
	// A continuation is admitted only after correlated graph/consultation results.
	for _, run := range a.state.AutomationRuns {
		if run.Status != "running" || a.runs[run.SessionID] != nil || !run.NeedsContinuation || automationWaiting(&a.state, run.SessionID) {
			continue
		}
		if a.state.conversationArchived(a.state.session(run.SessionID)) {
			continue
		}
		if run.StopRequested {
			_ = a.commitLocked(func(d *diskState) {
				finishAutomation(d, d.automationRun(run.ID), "interrupted_by_user", "Stopped by user.")
			})
			continue
		}
		if run.Reminder {
			_ = a.commitLocked(func(d *diskState) {
				finishAutomation(d, d.automationRun(run.ID), "execution_failed", "Agent ended twice without reporting the objective outcome.")
			})
			continue
		}
		if err := a.commitLocked(func(d *diskState) { d.automationRun(run.ID).Reminder = true }); err != nil {
			return
		}
		a.startAutomationTurnLocked(a.state.automationRun(run.ID), "The automation run is still active. Classify the objective now using automation_succeed or automation_fail with a short reason. A normal turn ending does not classify the objective.")
	}
	blocked := map[string]bool{}
	for _, trigger := range a.state.AutomationTriggers {
		if trigger.Consumed {
			continue
		}
		v := a.state.automation(trigger.AutomationID)
		if v == nil || v.Deleted {
			continue
		}
		if blocked[v.ID] {
			continue
		}
		blocked[v.ID] = true
		config := copyAutomation(*v)
		if trigger.Snapshot != nil {
			config = copyAutomation(*trigger.Snapshot)
		}
		p := a.state.project(v.ProjectID)
		if p == nil || p.Removed {
			continue
		}
		active, scheduledActive := false, false
		for _, run := range a.state.AutomationRuns {
			if run.AutomationID == v.ID && run.Status == "running" {
				active = true
				scheduledActive = scheduledActive || run.Snapshot.Mode == "schedule" || automationSessionMode(run.Snapshot) != "new_per_run"
			}
		}
		if active && (scheduledActive || automationSessionMode(*v) != "new_per_run" || automationSessionMode(config) != "new_per_run" || v.Mode != "webhook" || !v.Parallel || config.Mode != "webhook" || !config.Parallel) {
			continue
		}
		mode := automationSessionMode(config)
		if mode == "reuse_created" {
			if automationSessionMode(*v) != mode {
				continue
			}
			if v.SessionID != "" {
				config.SessionID = v.SessionID
			}
		}
		creating := !automationUsesSession(config)
		var s Session
		promptKind := "initial"
		if creating {
			topic := p.topic(config.TopicID)
			if topic == nil || topic.Archived {
				continue
			}
			s = newTopLevelSession(v.ProjectID, config.Name, "Ungrouped", config.Harness, config.Model, config.Effort)
			s.TopicID, s.YOLO = config.TopicID, config.YOLO
			s.Capabilities = ConversationCapabilities{Desired: copySelection(config.Selection)}
		} else {
			if reason := a.automationSessionBlocked(v.ProjectID, config.SessionID); reason != "" {
				a.blockAutomationTriggerLocked(trigger.ID, reason)
				continue
			}
			s = *a.state.session(config.SessionID)
			promptKind = "follow_up"
			if trigger.Snapshot == nil {
				captureAutomationSessionSettings(&config, &s)
			} else if config.Harness != s.Harness {
				a.blockAutomationTriggerLocked(trigger.ID, "The recovery snapshot uses a different harness from its linked session.")
				continue
			}
		}
		if err := validateAutomationPrompt(map[bool]string{true: config.Prompt, false: config.FollowUpPrompt}[creating], config.Mode); err != nil {
			continue
		}
		if mode == "reuse_created" {
			config.SessionID = s.ID
		}
		r := AutomationRun{ID: newID(), AutomationID: v.ID, ProjectID: v.ProjectID, SessionID: s.ID, Snapshot: config, PromptKind: promptKind, Capabilities: trigger.Capabilities, Trigger: trigger, Status: "running", StartedAt: now()}
		e := event("agent_prompt", automationTask(&r))
		e.Title = config.Name
		e.Data = map[string]any{"automationId": v.ID, "runId": r.ID, "source": trigger.Source, "promptKind": promptKind}
		if err := a.commitLocked(func(d *diskState) {
			for i := range d.AutomationTriggers {
				if d.AutomationTriggers[i].ID == trigger.ID {
					d.AutomationTriggers[i].Consumed = true
					d.AutomationTriggers[i].BlockedReason = ""
				}
			}
			if creating {
				a.appendTopLevelSession(d, s)
			}
			next := d.session(s.ID)
			next.Automation = &AutomationSession{RunID: r.ID, AutomationID: v.ID, Status: "running"}
			next.Events = append(next.Events, e)
			if mode == "reuse_created" && d.automation(v.ID).SessionID == "" {
				nextAutomation := d.automation(v.ID)
				nextAutomation.SessionID = s.ID
				nextAutomation.Revision++
			}
			d.AutomationRuns = append(d.AutomationRuns, r)
		}); err != nil {
			return
		}
		a.startAutomationTurnLocked(a.state.automationRun(r.ID), automationPrompt(&r))
		if mode == "new_per_run" && v.Mode == "webhook" && v.Parallel && config.Mode == "webhook" && config.Parallel {
			blocked[v.ID] = false
		}
	}
}

func (p *adapter) settleAutomation(d *diskState, err error) {
	r := d.activeAutomation(p.id)
	if r == nil || r.ID != p.automationRunID {
		return
	}
	s := d.session(p.id)
	switch {
	case p.turn.stopErr != nil || !p.processesDrained:
		// Keep the serial slot until confirmed process settlement; no next run.
		r.Reason = "Process shutdown could not be confirmed. Stop this conversation again to recover."
	case r.StopRequested:
		finishAutomation(d, r, "interrupted_by_user", "Stopped by user.")
	case err != nil || p.failed || !p.completed || p.turn.ctx.Err() != nil:
		if p.app.closing {
			d.AutomationRecoveries = append(d.AutomationRecoveries, AutomationRecovery{ID: newID(), RunID: r.ID, Snapshot: copyAutomation(r.Snapshot), Capabilities: r.Capabilities, Variables: r.Trigger.Variables, Source: "interrupted", From: r.StartedAt, Until: now(), SessionID: r.SessionID, Delivery: "pending"})
		}
		reason := "Harness execution failed."
		if err != nil {
			reason = err.Error()
		}
		finishAutomation(d, r, "execution_failed", reason)
	case r.Decision != "":
		finishAutomation(d, r, r.Decision, r.Reason)
	default:
		r.NeedsContinuation = true
		s.Status = "running"
	}
}

func (p *adapter) automationSnapshot(s Session) (*CapabilitySnapshot, error) {
	a := p.app
	a.mu.Lock()
	run := a.state.activeAutomation(s.ID)
	if run == nil || run.ID != p.automationRunID || a.runs[s.ID] != p.turn {
		a.mu.Unlock()
		return nil, errors.New("Automation run is no longer active.")
	}
	existing := run.Capabilities
	id := run.ID
	a.mu.Unlock()
	if existing != nil {
		return existing, nil
	}
	snapshot, err := a.captureCapabilities(p.turn.ctx, run.Snapshot.Selection, s.Harness)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if current := a.state.activeAutomation(s.ID); current == nil || current.ID != id || a.runs[s.ID] != p.turn || p.turn.ctx.Err() != nil {
		return nil, errors.New("Automation finished during preparation.")
	}
	if err = a.commitLocked(func(d *diskState) { d.automationRun(id).Capabilities = snapshot }); err != nil {
		return nil, err
	}
	return snapshot, nil
}
