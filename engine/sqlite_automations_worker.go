package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The worker owns live eligibility, independently of client subscriptions. Scan
// indexed candidates in bounded ID pages; completed runs and histories stay cold.
func (a *app) sqlAutomationCandidates(query string, args []any, visit func(string) error) error {
	after := ""
	for {
		values := append(append([]any{}, args...), after)
		ids, err := sqliteQueryIDs(a.ctx, a.store.readers, query, values...)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			if err := visit(id); err != nil {
				return err
			}
			after = id
		}
	}
}

func (st *sqliteSessionTx) enqueueAutomation(v Automation, key, source, at string, vars map[string]string, snapshot *Automation) (string, error) {
	var id string
	err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_automation_triggers WHERE x_automationId=? AND x_key=? LIMIT 1`, v.ID, key).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	t := AutomationTrigger{ID: newID(), AutomationID: v.ID, Key: key, Source: source, At: at, Variables: vars, Snapshot: snapshot}
	return t.ID, st.putRoot("automation_triggers", t.ID, t)
}

func (a *app) scheduleSQLAutomationsLocked() error {
	if a.closing || a.storageErr != nil {
		return nil
	}
	at := time.Now().UTC()
	err := a.sqlAutomationCandidates(`SELECT a.id FROM data_automations a JOIN data_projects p ON p.id=a.project_id WHERE a.x_enabled='true' AND a.x_deleted<>'true' AND a.x_mode='schedule' AND a.x_nextScheduledAt<>'' AND a.x_nextScheduledAt<=? AND p.x_removed<>'true' AND a.id>? ORDER BY a.id LIMIT 100`, []any{sqliteTimeKey(at.Format(time.RFC3339Nano))}, func(id string) error {
		return a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
			var v Automation
			if err := decodeSQLiteRecord(st.ctx, st.tx, "automations", id, 8<<20, &v); err != nil {
				return err
			}
			var settings EngineSettings
			if err := decodeSQLiteRecord(st.ctx, st.tx, "engine_settings", "settings", 64<<10, &settings); err != nil {
				return err
			}
			if settings.TimeZone == "" {
				settings.TimeZone = defaultEngineTimeZone
			}
			if _, err := st.enqueueAutomation(v, "schedule/"+v.NextScheduledAt, "schedule", v.NextScheduledAt, nil, nil); err != nil {
				return err
			}
			if v.Schedule.Recurrence == "once" {
				v.OnceTriggeredAt = v.NextScheduledAt
			}
			due, err := time.Parse(time.RFC3339Nano, v.NextScheduledAt)
			if err != nil {
				return err
			}
			next := nextAutomationOccurrence(v.Schedule, settings.TimeZone, due)
			v.NextScheduledAt = ""
			if !next.IsZero() {
				v.NextScheduledAt = next.Format(time.RFC3339Nano)
			}
			return st.putRoot("automations", v.ID, v)
		})
	})
	if err != nil {
		return err
	}
	if a.harnessInstalling {
		return nil
	}
	if err := a.sqlAutomationCandidates(`SELECT id FROM data_automation_runs WHERE status='running' AND x_needsContinuation='true' AND id>? ORDER BY id LIMIT 100`, nil, a.continueSQLAutomationLocked); err != nil {
		return err
	}
	// Visit each automation once, then drain at most 100 parallel heads for it.
	// A blocked oldest trigger never hides another automation's eligible work.
	return a.sqlAutomationCandidates(`SELECT a.id FROM data_automations a JOIN data_projects p ON p.id=a.project_id WHERE a.x_deleted<>'true' AND p.x_removed<>'true' AND EXISTS(SELECT 1 FROM data_automation_triggers t WHERE t.x_automationId=a.id AND t.x_consumed<>'true') AND a.id>? ORDER BY a.id LIMIT 100`, nil, func(id string) error {
		for n := 0; n < 100; n++ {
			run, parallel, err := a.admitSQLAutomationLocked(id)
			if err != nil {
				return err
			}
			if run == nil {
				return nil
			}
			if err := a.startSQLAutomationTurnLocked(*run, automationPrompt(run)); err != nil {
				return err
			}
			if !parallel {
				return nil
			}
		}
		return nil
	})
}

func (st *sqliteSessionTx) automationWaiting(id string) (bool, error) {
	var waiting bool
	err := st.tx.QueryRowContext(st.ctx, `SELECT
		EXISTS(SELECT 1 FROM data_graph_runs WHERE x_conversationId=? AND status IN ('starting','running','ending')) OR
		EXISTS(SELECT 1 FROM data_consultations WHERE x_from=? AND x_delivery IN ('waiting','pending','delivering')) OR
		EXISTS(SELECT 1 FROM data_graph_notifications WHERE x_conversationId=? AND status IN ('pending','delivering'))`, id, id, id).Scan(&waiting)
	return waiting, err
}

func (a *app) sqlAutomationSessionBlocked(st *sqliteSessionTx, project, id string) (Session, string, error) {
	s, err := st.sessionMeta(id)
	if errors.Is(err, sql.ErrNoRows) {
		return s, "Choose a normal top-level session in this project.", nil
	}
	if err != nil {
		return s, "", err
	}
	if s.ProjectID != project || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
		return s, "Choose a normal top-level session in this project.", nil
	}
	if binding, err := st.agentBinding(id); err != nil {
		return s, "", err
	} else if binding != nil && binding.Blocked != nil {
		return s, binding.Blocked.Message, nil
	}
	archived, err := st.effectivelyArchived(s)
	if err != nil {
		return s, "", err
	}
	if archived {
		return s, "Restore the session, topic and project before using this automation.", nil
	}
	var busy bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_automation_runs WHERE session_id=? AND status='running')`, id).Scan(&busy); err != nil {
		return s, "", err
	}
	if busy || a.runs[id] != nil || s.Status == "running" {
		return s, "Waiting for the session to finish its current work.", nil
	}
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_queued_messages WHERE owner_id=?)`, id).Scan(&busy); err != nil {
		return s, "", err
	}
	if busy {
		return s, "Waiting for pending session messages to be sent or removed.", nil
	}
	waiting, err := st.automationWaiting(id)
	if err != nil {
		return s, "", err
	}
	if waiting || a.graphStarting[id] {
		return s, "Waiting for graph or consultation work to finish.", nil
	}
	if err := st.tx.QueryRowContext(st.ctx, `SELECT
		EXISTS(SELECT 1 FROM data_consultations WHERE (x_from=? OR x_to=?) AND (status IN ('queued','answering') OR x_delivery IN ('pending','delivering'))) OR
		EXISTS(SELECT 1 FROM data_graph_activities WHERE x_conversationId=? AND status IN ('ready','scheduled','running'))`, id, id, id).Scan(&busy); err != nil {
		return s, "", err
	}
	if busy {
		return s, "Waiting for the session's graph or consultation work.", nil
	}
	err = decodeSQLiteRecord(st.ctx, st.tx, "capability_selections", sqliteOwnedKey(sqliteOwnedKey(id, ""), ""), 8<<20, &s.Capabilities.Desired)
	return s, "", err
}

func (a *app) admitSQLAutomationLocked(id string) (*AutomationRun, bool, error) {
	agentAdmission, err := a.refreshAutomationAgentLocked(id)
	if err != nil {
		return nil, false, err
	}
	if a.harnessInstalling {
		return nil, false, nil
	}
	var admitted *AutomationRun
	parallel := false
	err = a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
		var v Automation
		if err := decodeSQLiteRecord(st.ctx, st.tx, "automations", id, 8<<20, &v); err != nil {
			return err
		}
		project, err := st.project(v.ProjectID)
		if err != nil {
			return err
		}
		if v.Deleted || project.Removed {
			return nil
		}
		var key string
		err = st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_automation_triggers WHERE x_automationId=? AND x_consumed<>'true' ORDER BY position,id LIMIT 1`, id).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var trigger AutomationTrigger
		if err := decodeSQLiteRecord(st.ctx, st.tx, "automation_triggers", key, 32<<20, &trigger); err != nil {
			return err
		}
		config := copyAutomation(v)
		if trigger.Snapshot != nil {
			config = copyAutomation(*trigger.Snapshot)
		}
		parallel = automationSessionMode(v) == "new_per_run" && automationSessionMode(config) == "new_per_run" && v.Mode == "webhook" && v.Parallel && config.Mode == "webhook" && config.Parallel
		var active, serial bool
		if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_automation_runs WHERE x_automationId=? AND status='running'), EXISTS(SELECT 1 FROM data_automation_runs WHERE x_automationId=? AND status='running' AND (x_snapshot_mode='schedule' OR x_snapshot_sessionMode NOT IN ('','new_per_run')))`, id, id).Scan(&active, &serial); err != nil {
			return err
		}
		if active && (serial || !parallel) {
			return nil
		}
		mode := automationSessionMode(config)
		if mode == "reuse_created" {
			if automationSessionMode(v) != mode {
				return nil
			}
			if v.SessionID != "" {
				config.SessionID = v.SessionID
			}
		}
		creating := !automationUsesSession(config)
		var s Session
		promptKind := "initial"
		if creating {
			topic, _, err := st.topic(v.ProjectID, config.TopicID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if topic.Archived {
				return nil
			}
			s = newTopLevelSession(v.ProjectID, config.Name, "Ungrouped", config.Harness, config.Model, config.Effort)
			s.TopicID, s.YOLO = config.TopicID, config.YOLO
			s.Capabilities.Desired = copySelection(config.Selection)
		} else {
			var reason string
			s, reason, err = a.sqlAutomationSessionBlocked(st, v.ProjectID, config.SessionID)
			if err != nil {
				return err
			}
			if reason == "" && trigger.Snapshot != nil && config.Harness != s.Harness {
				reason = "The recovery snapshot uses a different harness from its linked session."
			}
			if reason != "" {
				if trigger.BlockedReason == reason {
					return nil
				}
				trigger.BlockedReason = reason
				return st.putRoot("automation_triggers", trigger.ID, trigger)
			}
			promptKind = "follow_up"
			if trigger.Snapshot == nil {
				captureAutomationSessionSettings(&config, &s)
			}
		}
		prompt := config.Prompt
		if !creating {
			prompt = config.FollowUpPrompt
		}
		if err := validateAutomationPrompt(prompt, config.Mode); err != nil {
			return nil
		}
		if mode == "reuse_created" {
			config.SessionID = s.ID
		}
		r := AutomationRun{ID: newID(), AutomationID: v.ID, ProjectID: v.ProjectID, SessionID: s.ID, Snapshot: config, PromptKind: promptKind, Capabilities: trigger.Capabilities, Trigger: trigger, Status: "running", StartedAt: now()}
		if !creating && trigger.Snapshot == nil {
			if binding, err := st.agentBinding(s.ID); err != nil {
				return err
			} else if binding != nil {
				if agentAdmission == nil || agentAdmission.Preparation == nil || agentAdmission.SessionID != s.ID || agentAdmission.Harness != s.Harness || agentAdmission.StopVersion != a.stopVersions[s.ID] || binding.Revision != agentAdmission.Preparation.BindingRevision {
					return nil
				}
				var autoDigest, triggerDigest string
				if err := st.tx.QueryRowContext(st.ctx, `SELECT digest FROM data_automations WHERE id=?`, id).Scan(&autoDigest); err != nil {
					return err
				}
				if err := st.tx.QueryRowContext(st.ctx, `SELECT digest FROM data_automation_triggers WHERE id=?`, key).Scan(&triggerDigest); err != nil {
					return err
				}
				if autoDigest != agentAdmission.AutomationDigest || trigger.ID != agentAdmission.TriggerID || triggerDigest != agentAdmission.TriggerDigest || project.Folder != agentAdmission.Directory {
					return nil
				}
				var selection ConversationCapabilities
				if err := decodeSQLiteRecord(st.ctx, st.tx, "session_capabilities", sqliteOwnedKey(s.ID, ""), 64<<10, &selection); err != nil {
					return err
				}
				prepared := agentAdmission.Preparation
				if prepared.ExternalCapabilities == nil || sqliteRevision(selection.Revision) != prepared.SelectionRevision || agentConfigDigest(binding.Desired) != agentConfigDigest(prepared.Resolution.Config) {
					return nil
				}
				capture := prepared.Resolution.Capture
				r.Snapshot.AgentConfiguration = &AgentExecutionCapture{Config: prepared.Resolution.Config, Source: &capture, Revision: agentConfigDigest(prepared.Resolution.Config)}
				r.Capabilities = prepared.ExternalCapabilities
			} else if agentAdmission != nil {
				return nil // Destination lost its binding while the capture was prepared.
			}
		}
		if r.Snapshot.AgentConfiguration != nil && r.Capabilities == nil && r.Snapshot.AgentConfiguration.ExternalCapabilities == nil {
			return agentProblem(409, "automation_capture_missing", "agent", "The saved-agent recovery snapshot has no admitted Library capture. Choose a fresh run explicitly.")
		}
		trigger.Consumed, trigger.BlockedReason = true, ""
		if err := st.putRoot("automation_triggers", trigger.ID, trigger); err != nil {
			return err
		}
		if creating {
			if err := st.createSession(s, a.dir); err != nil {
				return err
			}
		}
		meta, err := st.sessionMeta(s.ID)
		if err != nil {
			return err
		}
		meta.Automation = &AutomationSession{RunID: r.ID, AutomationID: v.ID, Status: "running"}
		meta.UpdatedAt = now()
		if err := st.putSessionMeta(meta); err != nil {
			return err
		}
		e := event("agent_prompt", automationTask(&r))
		e.Title = config.Name
		e.Data = map[string]any{"automationId": v.ID, "runId": r.ID, "source": trigger.Source, "promptKind": promptKind}
		if _, err := st.appendEvent(s.ID, e); err != nil {
			return err
		}
		if mode == "reuse_created" && v.SessionID == "" {
			if v.Revision == ^uint64(0) {
				return errors.New("automation revision exhausted")
			}
			v.SessionID = s.ID
			v.Revision++
			if err := st.putRoot("automations", v.ID, v); err != nil {
				return err
			}
		}
		if err := st.putRoot("automation_runs", r.ID, r); err != nil {
			return err
		}
		admitted = &r
		return nil
	})
	return admitted, parallel, err
}

func (a *app) startSQLAutomationTurnLocked(run AutomationRun, text string) error {
	if a.harnessInstalling {
		return nil
	}
	if a.runs[run.SessionID] != nil {
		return nil
	}
	s, native, _, err := a.store.readTurnSession(a.ctx, run.SessionID)
	if err != nil {
		return err
	}
	project, err := a.store.readProjectMeta(a.ctx, s.ProjectID)
	if err != nil {
		return err
	}
	b, installed := a.binaries[s.Harness]
	cwd, dirErr := existingDirectory(project.Folder)
	if project.Removed || !installed || dirErr != nil {
		return a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
			return st.finishAutomation(&run, "execution_failed", "Project directory or harness is unavailable.")
		})
	}
	if err := a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
		saved, err := st.sessionMeta(s.ID)
		if err != nil {
			return err
		}
		saved.Status, saved.UpdatedAt = "running", now()
		if err := st.putSessionMeta(saved); err != nil {
			return err
		}
		run.NeedsContinuation = false
		return st.putRoot("automation_runs", run.ID, run)
	}); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	t := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	a.runs[s.ID] = t
	a.wg.Add(1)
	go a.execute(t, s, native, b, cwd, submission{Text: text})
	return nil
}

func (a *app) continueSQLAutomationLocked(id string) error {
	if a.harnessInstalling {
		return nil
	}
	var start *AutomationRun
	err := a.writeSQLLocked(a.ctx, func(st *sqliteSessionTx) error {
		var r AutomationRun
		if err := decodeSQLiteRecord(st.ctx, st.tx, "automation_runs", id, 32<<20, &r); err != nil {
			return err
		}
		if r.Status != "running" || !r.NeedsContinuation || a.runs[r.SessionID] != nil {
			return nil
		}
		s, err := st.sessionMeta(r.SessionID)
		if err != nil {
			return err
		}
		archived, err := st.effectivelyArchived(s)
		if err != nil {
			return err
		}
		if archived {
			return nil
		}
		project, err := st.project(s.ProjectID)
		if err != nil {
			return err
		}
		if project.Removed {
			return nil
		}
		waiting, err := st.automationWaiting(s.ID)
		if err != nil {
			return err
		}
		if waiting {
			return nil
		}
		if r.StopRequested {
			return st.finishAutomation(&r, "interrupted_by_user", "Stopped by user.")
		}
		if r.Reminder {
			return st.finishAutomation(&r, "execution_failed", "Agent ended twice without reporting the objective outcome.")
		}
		r.Reminder = true
		if err := st.putRoot("automation_runs", r.ID, r); err != nil {
			return err
		}
		start = &r
		return nil
	})
	if err != nil || start == nil {
		return err
	}
	return a.startSQLAutomationTurnLocked(*start, "The automation run is still active. Classify the objective now using automation_succeed or automation_fail with a short reason. A normal turn ending does not classify the objective.")
}

// app.mu is held by the shared dispatcher. Native completion alone never
// classifies an automation; only its exact owning main-agent turn may record it.
func (p *adapter) recordSQLAutomationOutcome(name, reason string) (any, error) {
	status := "succeeded"
	if name == "automation_fail" {
		status = "failed"
	}
	err := p.app.writeSQLLocked(p.turn.ctx, func(st *sqliteSessionTx) error {
		r, err := st.activeAutomation(p.id)
		if err != nil {
			return err
		}
		if r == nil || r.ID != p.automationRunID || p.subagent || p.role != "" || p.graphNode() || p.app.runs[p.id] != p.turn || p.turn.ctx.Err() != nil || r.StopRequested {
			return errors.New("This turn does not own an active automation run.")
		}
		if r.Decision != "" && (r.Decision != status || r.Reason != reason) {
			return errors.New("An outcome is already recorded for this run.")
		}
		r.Decision, r.Reason = status, reason
		return st.putRoot("automation_runs", r.ID, r)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"recorded": true, "status": status, "instruction": "Finish this turn. The engine will finalize the run after settlement."}, nil
}
