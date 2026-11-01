package main

import "errors"

func automationSessionMode(v Automation) string {
	if v.SessionMode == "" {
		return "new_per_run"
	}
	return v.SessionMode
}
func automationUsesSession(v Automation) bool {
	return automationSessionMode(v) == "existing" || automationSessionMode(v) == "reuse_created" && v.SessionID != ""
}

func (d *diskState) automationSessionEligible(projectID, id string) error {
	s := d.session(id)
	if s == nil || s.ProjectID != projectID || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
		return errors.New("Choose a normal top-level session in this project.")
	}
	if _, ok := d.conversationDirectory(s); !ok || d.conversationArchived(s) {
		return errors.New("Restore the session, topic and project before using this automation.")
	}
	return nil
}

func (a *app) automationSessionBlocked(projectID, id string) string {
	if err := a.state.automationSessionEligible(projectID, id); err != nil {
		return err.Error()
	}
	s := a.state.session(id)
	if a.runs[id] != nil || a.state.activeAutomation(id) != nil || s.Status == "running" {
		return "Waiting for the session to finish its current work."
	}
	if len(s.Queue) > 0 {
		return "Waiting for pending session messages to be sent or removed."
	}
	if a.graphStarting[id] || automationWaiting(&a.state, id) {
		return "Waiting for graph or consultation work to finish."
	}
	for _, c := range a.state.Consultations {
		if (c.From == id || c.To == id) && (!consultationTerminal(c.Status) || c.Delivery == "pending" || c.Delivery == "delivering") {
			return "Waiting for the session's consultation."
		}
	}
	for _, activity := range a.state.GraphActivities {
		if activity.ConversationID == id && (activity.Status == "ready" || activity.Status == "scheduled" || activity.Status == "running") {
			return "Waiting for the session's graph work."
		}
	}
	return ""
}

func captureAutomationSessionSettings(v *Automation, s *Session) {
	v.Harness, v.Model, v.Effort, v.YOLO, v.TopicID = s.Harness, s.Model, s.Effort, s.YOLO, s.TopicID
	v.Selection = copySelection(s.Capabilities.Desired)
}

// Never rewrite user preferences to run a saved recovery snapshot. All active
// execution/policy views read this overlay, and return to normal at settlement.
func (d *diskState) automationSessionView(s *Session) *Session {
	if s == nil {
		return nil
	}
	r := d.activeAutomation(s.ID)
	if r == nil {
		return s
	}
	view := *s
	view.Harness, view.Model, view.Effort, view.YOLO = r.Snapshot.Harness, r.Snapshot.Model, r.Snapshot.Effort, r.Snapshot.YOLO
	view.Capabilities.Desired = copySelection(r.Snapshot.Selection)
	return &view
}

func (d *diskState) sessionYOLO(s *Session) bool {
	if s == nil {
		return false
	}
	if r := d.activeAutomation(s.ID); r != nil {
		return r.Snapshot.YOLO
	}
	return s.YOLO
}

func (a *app) blockAutomationTriggerLocked(id, reason string) {
	for _, t := range a.state.AutomationTriggers {
		if t.ID == id && t.BlockedReason == reason {
			return
		}
	}
	_ = a.commitLocked(func(d *diskState) {
		for i := range d.AutomationTriggers {
			if d.AutomationTriggers[i].ID == id {
				d.AutomationTriggers[i].BlockedReason = reason
				return
			}
		}
	})
}
