package main

import (
	"context"
	"database/sql"
	"errors"
)

// Existing automation admission owns app.mu, but source/model/Library discovery
// must not. Refresh only an idle bound reuse destination, then let the unchanged
// admission transaction recheck its queue/ownership/binding block before consuming.
type automationAgentAdmission struct {
	SessionID, Harness, Directory              string
	AutomationDigest, TriggerID, TriggerDigest string
	StopVersion                                uint64
	Preparation                                *AgentTurnPreparation
}

func (a *app) refreshAutomationAgentLocked(automationID string) (*automationAgentAdmission, error) {
	var destination string
	admission := &automationAgentAdmission{}
	err := a.store.readSnapshot(a.ctx, func(tx *sql.Tx) error {
		var config Automation
		if err := decodeSQLiteRecord(a.ctx, tx, "automations", automationID, 8<<20, &config); err != nil {
			return err
		}
		if !automationUsesSession(config) {
			return nil
		}
		var key string
		err := tx.QueryRowContext(a.ctx, `SELECT id FROM data_automation_triggers WHERE x_automationId=? AND x_consumed<>'true' ORDER BY position,id LIMIT 1`, automationID).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var trigger AutomationTrigger
		if err := decodeSQLiteRecord(a.ctx, tx, "automation_triggers", key, 32<<20, &trigger); err != nil {
			return err
		}
		if trigger.Snapshot != nil {
			return nil
		}
		st := &sqliteSessionTx{ctx: a.ctx, tx: tx}
		s, reason, err := a.sqlAutomationSessionBlocked(st, config.ProjectID, config.SessionID)
		if err != nil {
			return err
		}
		// A previous source incompatibility may have been repaired externally;
		// retry its metadata preparation, never a consumed execution/queue item.
		if reason != "" && s.AgentConfig == nil {
			return nil
		}
		if a.runs[s.ID] != nil || s.Status == "running" {
			return nil
		}
		binding, err := st.agentBinding(s.ID)
		if err != nil {
			return err
		}
		if binding != nil {
			destination = s.ID
			admission.SessionID, admission.Harness = s.ID, s.Harness
			admission.Directory, err = st.conversationDirectory(s)
			if err != nil {
				return err
			}
			admission.TriggerID = trigger.ID
			if err := tx.QueryRowContext(a.ctx, `SELECT digest FROM data_automations WHERE id=?`, automationID).Scan(&admission.AutomationDigest); err != nil {
				return err
			}
			if err := tx.QueryRowContext(a.ctx, `SELECT digest FROM data_automation_triggers WHERE id=?`, key).Scan(&admission.TriggerDigest); err != nil {
				return err
			}
			admission.StopVersion = a.stopVersions[s.ID]
		}
		return nil
	})
	if err != nil || destination == "" {
		return nil, err
	}
	a.mu.Unlock()
	admission.Preparation, err = a.prepareAgentTurn(a.ctx, destination, nil)
	a.mu.Lock()
	var problem *AgentConfigProblem
	if errors.As(err, &problem) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return admission, nil
}

func (a *app) prepareAutomationAgent(ctx context.Context, s Session) (*AgentTurnPreparation, bool, error) {
	var run *AutomationRun
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		var err error
		run, err = (&sqliteSessionTx{ctx: ctx, tx: tx}).activeAutomation(s.ID)
		return err
	})
	if err != nil || run == nil || run.Snapshot.AgentConfiguration == nil {
		return nil, false, err
	}
	frozen := *run.Snapshot.AgentConfiguration
	if run.Capabilities != nil {
		frozen.ExternalCapabilities = run.Capabilities
	}
	if frozen.ExternalCapabilities == nil {
		return nil, true, agentProblem(409, "automation_capture_missing", "agent", "This saved-agent run has no admitted Library capture. Start an explicitly fresh run; live content cannot fill the missing snapshot.")
	}
	prepared, err := a.prepareAgentTurn(ctx, s.ID, &frozen)
	return prepared, true, err
}
