package main

import (
	"errors"
	"slices"
	"time"
)

func (v sqliteValidation) validateAutomations() error {
	var settings EngineSettings
	if err := v.get("engine_settings", "settings", &settings); err != nil {
		return err
	}
	zone := settings.TimeZone
	if zone == "" {
		zone = defaultEngineTimeZone
	}
	if zone == "Local" {
		return errors.New("invalid engine time zone")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return err
	}
	checks := []struct{ label, query string }{
		{"automation identities", `SELECT record_id FROM (SELECT record_id FROM data_automations UNION ALL SELECT record_id FROM data_automation_triggers UNION ALL SELECT record_id FROM data_automation_runs UNION ALL SELECT record_id FROM data_automation_recoveries) GROUP BY record_id HAVING record_id='' OR count(*)>1`},
		{"automation trigger identity", `SELECT x_automationId,x_key FROM data_automation_triggers GROUP BY x_automationId,x_key HAVING count(*)>1`},
		{"automation trigger owner", `SELECT 1 FROM data_automation_triggers t LEFT JOIN data_automations a ON a.id=t.x_automationId WHERE a.id IS NULL`},
		{"automation active destination", `SELECT session_id FROM data_automation_runs WHERE status='running' GROUP BY session_id HAVING count(*)>1`},
		{"automation occurrence identity", `SELECT owner_id,record_id FROM data_automation_occurrence_decisions GROUP BY owner_id,record_id HAVING record_id='' OR count(*)>1`},
		{"automation resolution identity", `SELECT owner_id,x_operationId FROM data_automation_resolutions GROUP BY owner_id,x_operationId HAVING x_operationId='' OR count(*)>1`},
	}
	for _, c := range checks {
		if err := v.rejectRows(c.label, c.query); err != nil {
			return err
		}
	}
	if err := v.each("automations", nil, func(id string) error {
		var a Automation
		if err := v.get("automations", id, &a); err != nil {
			return err
		}
		exists, err := v.exists("projects", a.ProjectID)
		if err != nil {
			return err
		}
		if !exists || a.Revision == 0 {
			return errors.New("invalid automation project/revision")
		}
		if !automationUsesSession(a) {
			var topic bool
			if err := v.tx.QueryRowContext(v.ctx, `SELECT EXISTS(SELECT 1 FROM data_topics WHERE owner_id=? AND record_id=?)`, a.ProjectID, a.TopicID).Scan(&topic); err != nil {
				return err
			}
			if !topic {
				return errors.New("automation topic missing")
			}
		}
		copy := copyAutomation(a)
		if err := validateAutomationConfig(&copy); err != nil {
			return err
		}
		if a.NextScheduledAt != "" {
			if _, err := time.Parse(time.RFC3339Nano, a.NextScheduledAt); err != nil {
				return err
			}
			if a.Mode != "schedule" || !a.Enabled {
				return errors.New("invalid next automation occurrence")
			}
		}
		if a.Schedule.Anchor != "" {
			if _, err := time.Parse("2006-01-02", a.Schedule.Anchor); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := v.each("automation_runs", nil, func(id string) error {
		var r AutomationRun
		if err := v.get("automation_runs", id, &r); err != nil {
			return err
		}
		exists, err := v.exists("automations", r.AutomationID)
		if err != nil {
			return err
		}
		s, err := v.session(r.SessionID)
		if err != nil {
			return err
		}
		if !exists || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" || s.ProjectID != r.ProjectID || r.Snapshot.ProjectID != r.ProjectID || r.Snapshot.ID != r.AutomationID {
			return errors.New("invalid automation run association")
		}
		if r.Status == "running" && (s.Automation == nil || s.Automation.RunID != r.ID) {
			return errors.New("invalid active automation owner")
		}
		if !slices.Contains([]string{"", "initial", "follow_up"}, r.PromptKind) || !slices.Contains([]string{"running", "succeeded", "failed", "execution_failed", "interrupted_by_user"}, r.Status) || !slices.Contains([]string{"", "succeeded", "failed"}, r.Decision) {
			return errors.New("invalid automation run state")
		}
		return validateAutomationCapture(r.Capabilities, r.Snapshot.Harness)
	}); err != nil {
		return err
	}
	if err := v.rejectRows("session automation association", `SELECT 1 FROM data_sessions s LEFT JOIN data_automation_runs r ON r.id=s.x_automation_runId WHERE s.x_automation_runId<>'' AND (r.id IS NULL OR r.session_id<>s.id OR r.status<>s.x_automation_status OR r.x_automationId<>s.x_automation_automationId)`); err != nil {
		return err
	}
	if err := v.each("automation_recoveries", nil, func(id string) error {
		var r AutomationRecovery
		if err := v.get("automation_recoveries", id, &r); err != nil {
			return err
		}
		exists, err := v.exists("automations", r.Snapshot.ID)
		if err != nil {
			return err
		}
		if !exists || !slices.Contains([]string{"pending", "delivering", "delivered", "uncertain"}, r.Delivery) || !slices.Contains([]string{"", "recover", "discard"}, r.Action) {
			return errors.New("invalid automation recovery")
		}
		if err := validateAutomationCapture(r.Capabilities, r.Snapshot.Harness); err != nil {
			return err
		}
		// Collections are deliberately absent here; check the bounded metadata
		// with the existing rule and stream each decision/receipt independently.
		return validateRecoveryOccurrences(&r)
	}); err != nil {
		return err
	}
	if err := v.each("automation_occurrence_decisions", nil, func(id string) error {
		var d AutomationRecoveryOccurrence
		if err := v.get("automation_occurrence_decisions", id, &d); err != nil {
			return err
		}
		if d.ID == "" || d.Action != "recover" && d.Action != "discard" {
			return errors.New("invalid occurrence decision")
		}
		_, err := time.Parse(time.RFC3339Nano, d.ScheduledAt)
		return err
	}); err != nil {
		return err
	}
	return v.each("automation_resolutions", nil, func(id string) error {
		var r AutomationRecoveryResolution
		if err := v.get("automation_resolutions", id, &r); err != nil {
			return err
		}
		n, err := v.collectionCount("automation_resolutions", id, "occurrenceIds")
		if err != nil {
			return err
		}
		if r.OperationID == "" || r.UserEventID == "" || n == 0 || n > 50 || r.Action != "recover" && r.Action != "discard" {
			return errors.New("invalid recovery resolution")
		}
		return v.rejectRows("recovery receipt occurrence", `SELECT 1 FROM data_automation_resolution_occurrences o JOIN data_automation_resolutions r ON r.id=o.owner_id LEFT JOIN data_automation_occurrence_decisions d ON d.owner_id=r.owner_id AND d.record_id=o.scalar_value WHERE r.id=? AND d.id IS NULL`, id)
	})
}
