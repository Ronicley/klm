package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pristine SQLite startup and the actual handler's cached/transient empty states.
// This never probes or installs host software.
func TestHarnessInventoryEmptyHistory(t *testing.T) {
	ctx := context.Background()
	store, err := openOrMigrateSQLite(ctx, t.TempDir(), prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	a := &app{ctx: ctx, store: store, binaries: map[string]binary{}}
	for _, recipe := range harnessRecipes {
		a.harnesses = append(a.harnesses, Harness{ID: recipe.ID, Name: recipe.Name})
	}
	if err := a.startHarnessInstaller(); err != nil {
		t.Fatal(err)
	}
	for _, active := range []bool{false, true} {
		s := a.harnessInstaller
		if active {
			s.active = "reserved-fixture"
			s.plans = nil
		} else {
			s.plans = []harnessInstallPlan{{HarnessID: "pi"}}
		}
		w := httptest.NewRecorder()
		a.getHarnessInstallInventory(w, httptest.NewRequest("GET", "/api/harnesses?cached=true", nil))
		if w.Code != 200 {
			t.Fatalf("inventory: %d %s", w.Code, w.Body.String())
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"jobs", "plans", "harnesses"} {
			if len(payload[field]) == 0 || payload[field][0] != '[' {
				t.Fatalf("%s must be an array: %s", field, w.Body.String())
			}
		}
		var hs []Harness
		if err := json.Unmarshal(payload["harnesses"], &hs); err != nil || len(hs) != 3 {
			t.Fatalf("expected all three pristine rows: %s", w.Body.String())
		}
		if string(payload["jobs"]) != "[]" {
			t.Fatalf("expected empty history: %s", payload["jobs"])
		}
	}
}

func TestHarnessLatchedStorageReadOnly(t *testing.T) {
	t.Run("cached-plan-post", func(t *testing.T) {
		a, _ := sqliteAutomationApp(t)
		if err := a.startHarnessInstaller(); err != nil {
			t.Fatal(err)
		}
		s := a.harnessInstaller
		n := nodeInstallation{Node: filepath.Join(a.dir, "fixture-node.exe"), Prefix: a.dir, Root: filepath.Join(a.dir, "node_modules"), Version: "v22.22.0"}
		hs := []Harness{{ID: "pi", Name: "Pi", Available: true, Compatible: true, InstalledVersion: "0.85.1"}}
		s.plans = buildHarnessInstallPlans(n, hs, "")
		s.inventoryReady = true
		a.harnesses = hs
		a.mu.Lock()
		latched := a.failStorageLocked(errors.New("fixture transient failure; writer remains usable"))
		a.mu.Unlock()
		// A real write still succeeds at the raw DB layer. Only the engine's
		// latched policy, not an unusable connection, must reject this POST.
		if _, err := a.store.writer.ExecContext(a.ctx, `UPDATE store_meta SET value=value WHERE key='revision'`); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(harnessInstallRequest{OperationID: "cached-plan-after-storage-freeze", PlanToken: s.plans[0].Token, ConfirmGlobal: true, ConfirmReplacement: true, ConfirmPrerequisites: true})
		ctx, cancel := context.WithCancel(a.ctx)
		defer cancel()
		r := httptest.NewRequest("POST", "/api/harnesses/pi/install", bytes.NewReader(body)).WithContext(ctx)
		r.SetPathValue("harnessId", "pi")
		w := httptest.NewRecorder()
		// Holding the probe serialization lock proves rejection happens before
		// discovery; no fake/real harness process or install worker can start.
		s.probeMu.Lock()
		done := make(chan struct{})
		go func() { a.installHarnessHTTP(w, r); close(done) }()
		select {
		case <-done:
			s.probeMu.Unlock()
		case <-time.After(2 * time.Second):
			cancel()
			a.mu.Lock()
			a.closing = true
			a.mu.Unlock()
			s.probeMu.Unlock()
			<-done
			t.Fatal("frozen POST entered refresh/probe admission instead of refusing immediately")
		}
		if w.Code != 503 || !strings.Contains(w.Body.String(), "read-only") {
			t.Fatalf("expected storage-unavailable response: %d %s", w.Code, w.Body.String())
		}
		if len(s.jobs) != 0 || s.active != "" || a.harnessInstalling || !a.harnessAdmissionStarted.IsZero() || a.harnessInstallCancel != nil || a.harnessConsumers.Load() != 0 {
			t.Fatal("frozen POST reserved/accepted installer work")
		}
		var jobs int
		if err := a.store.readers.QueryRow(`SELECT count(*) FROM harness_installations`).Scan(&jobs); err != nil || jobs != 0 {
			t.Fatal("frozen POST persisted a job", jobs, err)
		}
		if _, plans, err := s.refresh(a.ctx); err == nil || plans != nil {
			t.Fatal("refresh suppressed freeze into a cached writable plan", err)
		}
		if a.storageErr != latched {
			t.Fatal("storage latch was cleared/replaced")
		}
	})
	t.Run("phase-and-terminal-writes", func(t *testing.T) {
		a, _ := sqliteAutomationApp(t)
		if err := a.startHarnessInstaller(); err != nil {
			t.Fatal(err)
		}
		s := a.harnessInstaller
		job := harnessInstallJob{ID: "frozen-phase", OperationID: "accepted-before-storage-freeze", HarnessID: "pi", PlanToken: "known-plan", State: "verifying", CreatedAt: now()}
		if err := s.persist(job); err != nil {
			t.Fatal(err)
		}
		s.jobs = []harnessInstallJob{job}
		s.active = job.ID
		a.harnessInstalling = true
		a.harnesses = []Harness{{ID: "pi", Name: "Pi", Available: false}}
		worker, cancel := context.WithCancel(a.ctx)
		defer cancel()
		a.mu.Lock()
		a.harnessInstallCancel = cancel
		latched := a.failStorageLocked(errors.New("fixture transient failure"))
		a.mu.Unlock()
		if worker.Err() == nil {
			t.Fatal("storage freeze did not cancel owned installer context")
		}
		if _, err := a.store.writer.ExecContext(a.ctx, `UPDATE store_meta SET value=value WHERE key='revision'`); err != nil {
			t.Fatal(err)
		}
		if err := s.setJob(a.ctx, job.ID, "installing_harness", "Installing", "Must not enter another phase"); err == nil {
			t.Fatal("phase transition bypassed storage latch")
		}
		released := false
		s.finalize(a.ctx, job, []Harness{{ID: "pi", Name: "Pi", Available: true}}, map[string]binary{}, nil, nil, "Must not publish completion", func() { released = true })
		if !released || !a.harnessInstalling || s.active != job.ID || s.jobs[0].State != "verifying" || a.harnessSnapshot()[0].Available || a.storageErr != latched {
			t.Fatal("finalization bypassed frozen state")
		}
		var raw string
		if err := a.store.readers.QueryRow(`SELECT payload FROM harness_installations WHERE id=?`, job.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var saved harnessInstallJob
		if err := json.Unmarshal([]byte(raw), &saved); err != nil || saved.State != "verifying" {
			t.Fatal("raw writer changed frozen receipt", raw, err)
		}
		lookup := httptest.NewRecorder()
		a.lookupHarnessInstallation(lookup, httptest.NewRequest("GET", "/api/harness-installations?operationId="+job.OperationID, nil))
		if lookup.Code != 200 {
			t.Fatal("receipt lookup was lost during freeze", lookup.Body.String())
		}
		body, _ := json.Marshal(harnessInstallRequest{OperationID: job.OperationID, PlanToken: job.PlanToken})
		r := httptest.NewRequest("POST", "/api/harnesses/pi/install", bytes.NewReader(body))
		r.SetPathValue("harnessId", "pi")
		duplicate := httptest.NewRecorder()
		a.installHarnessHTTP(duplicate, r)
		if duplicate.Code != 202 {
			t.Fatal("idempotent receipt read was not retained", duplicate.Body.String())
		}
	})
}

func TestHarnessAdmissionPreservesPendingWork(t *testing.T) {
	a, source := sqliteAutomationApp(t)
	other := newTopLevelSession(source.ProjectID, "Other", "Ungrouped", "pi", "", "")
	other.TopicID = source.TopicID
	stamp := time.Now().UTC()
	schedule := AutomationSchedule{Recurrence: "daily", Time: "12:00"}
	due := nextAutomationOccurrence(schedule, defaultEngineTimeZone, stamp.Add(-72*time.Hour))
	automation := Automation{ID: "during-install", ProjectID: source.ProjectID, TopicID: source.TopicID, Name: "Due during install", Enabled: true, Mode: "schedule", Harness: "pi", Prompt: "Fixture", Schedule: schedule, NextScheduledAt: due.Format(time.RFC3339Nano)}
	queued := QueuedMessage{ID: "untouched", Mode: "queue", Status: "queued", Text: "Pending"}
	consultation := Consultation{ID: "queued-question", From: source.ID, To: other.ID, Status: "queued", Delivery: "waiting", CreatedAt: stamp.Add(-time.Minute).Format(time.RFC3339Nano)}
	reply := Consultation{ID: "pending-reply", From: source.ID, To: other.ID, Status: "completed", Delivery: "pending", CreatedAt: now()}
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.createSession(other, a.dir); err != nil {
			return err
		}
		if err := st.putRoot("automations", automation.ID, automation); err != nil {
			return err
		}
		if err := st.putRecord("queued_messages", sqliteOwnedKey(source.ID, queued.ID), source.ID, queued.ID, -1, queued); err != nil {
			return err
		}
		if err := st.putRoot("consultations", consultation.ID, consultation); err != nil {
			return err
		}
		return st.putRoot("consultations", reply.ID, reply)
	})
	if err != nil {
		t.Fatal(err)
	}
	a.harnessInstalling = true
	a.harnessAdmissionStarted = stamp
	a.mu.Lock()
	for i := 0; i < 3; i++ {
		if err := a.scheduleSQLAutomationsLocked(); err != nil {
			t.Fatal(err)
		}
	}
	a.scheduleSQLMessagesLocked()
	a.scheduleSQLConsultationsLocked()
	a.scheduleLinkedLocked()
	if run, _, err := a.admitSQLAutomationLocked(automation.ID); err != nil || run != nil {
		t.Fatalf("admission under gate: %v %+v", err, run)
	}
	if err := a.startSQLAutomationTurnLocked(AutomationRun{SessionID: source.ID}, "blocked"); err != nil {
		t.Fatal(err)
	}
	if err := a.continueSQLAutomationLocked("must-not-be-read"); err != nil {
		t.Fatal(err)
	}
	if err := a.scheduleSQLAutomationRecoveryLocked(); err != nil {
		t.Fatal(err)
	}
	if err := a.scheduleSQLGraphNotificationsLocked(); err != nil {
		t.Fatal(err)
	}
	// Simulate a long accepted install without waiting or changing global tools.
	if err := a.endHarnessAdmissionPauseLocked(stamp.Add(15 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if a.consultationExpiredLocked(consultation, stamp.Add(15*time.Minute)) {
		t.Fatal("installation consumed the queued consultation's timeout budget")
	}
	a.mu.Unlock()
	var pending, runs int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_automation_triggers WHERE x_automationId=? AND x_consumed<>'true'`, automation.ID).Scan(&pending); err != nil || pending != 3 {
		t.Fatal("due occurrences lost/consumed", pending, err)
	}
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_automation_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("automation admitted while suspended", runs, err)
	}
	var savedQueue QueuedMessage
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "queued_messages", sqliteOwnedKey(source.ID, queued.ID), 1<<20, &savedQueue); err != nil || savedQueue.Status != "queued" {
		t.Fatal("queue mutated", savedQueue, err)
	}
	for _, expected := range []Consultation{consultation, reply} {
		var saved Consultation
		if err := decodeSQLiteRecord(a.ctx, a.store.readers, "consultations", expected.ID, 1<<20, &saved); err != nil || saved != expected {
			t.Fatal("consultation/receipt mutated", saved, err)
		}
	}
	a.mu.Lock()
	a.harnessInstalling = false
	run, _, err := a.admitSQLAutomationLocked(automation.ID)
	a.mu.Unlock()
	if err != nil || run == nil {
		t.Fatal("normal admission did not resume", err)
	}
	if len(a.runs) != 0 || a.storageErr != nil {
		t.Fatal("unexpected execution/storage failure", a.storageErr)
	}
}

func TestHarnessFinalizationAdmissionBoundary(t *testing.T) {
	for _, failedPersistence := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "storage-failure"}[failedPersistence], func(t *testing.T) {
			a, _ := sqliteAutomationApp(t)
			if err := a.startHarnessInstaller(); err != nil {
				t.Fatal(err)
			}
			s := a.harnessInstaller
			job := harnessInstallJob{ID: "finalizing", OperationID: "isolated-finalize-operation", HarnessID: "pi", State: "verifying", CreatedAt: now(), Log: []string{"Verifying"}}
			if err := s.persist(job); err != nil {
				t.Fatal(err)
			}
			s.jobs = []harnessInstallJob{job}
			s.active = job.ID
			a.harnessInstalling = true
			a.harnesses = []Harness{{ID: "pi", Name: "Pi", Available: false}}
			if failedPersistence {
				if err := a.store.writer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			released := false
			s.finalize(a.ctx, job, []Harness{{ID: "pi", Name: "Pi", Available: true, Compatible: true}}, map[string]binary{"pi": {path: "not-executed-fixture"}}, []harnessInstallPlan{}, nil, "Verified fixture", func() { released = true })
			if !released {
				t.Fatal("shared mutation lock retained")
			}
			release, err := a.acquireHarnessConsumer()
			if failedPersistence {
				if err == nil {
					release()
					t.Fatal("admission opened after failed terminal persistence")
				}
				if s.jobs[0].State == "completed" || a.harnessSnapshot()[0].Available || s.active == "" {
					t.Fatal("false public completion after persistence failure")
				}
				return
			}
			if err != nil {
				t.Fatalf("first completed state cannot admit consumer: %v", err)
			}
			release()
			if s.active != "" || s.jobs[0].State != "completed" || !a.harnessSnapshot()[0].Available {
				t.Fatal("receipt/inventory/gate were not finalized together")
			}
			// An empty plan collection after finalization is still a ready cached
			// snapshot, not a reason to launch a second full executable discovery.
			w := httptest.NewRecorder()
			a.getHarnessInstallInventory(w, httptest.NewRequest("GET", "/api/harnesses?cached=true", nil))
			var inventory struct {
				Harnesses []Harness           `json:"harnesses"`
				Jobs      []harnessInstallJob `json:"jobs"`
				Active    string              `json:"activeJobId"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &inventory); err != nil || w.Code != 200 || len(inventory.Harnesses) != 1 || !inventory.Harnesses[0].Available || inventory.Active != "" || len(inventory.Jobs) != 1 || inventory.Jobs[0].State != "completed" {
				t.Fatal("first completed API snapshot was not immediately usable", w.Body.String(), err)
			}
			var raw string
			if err := a.store.readers.QueryRow(`SELECT payload FROM harness_installations WHERE id=?`, job.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var persisted harnessInstallJob
			if err := json.Unmarshal([]byte(raw), &persisted); err != nil || persisted.State != "completed" {
				t.Fatal("terminal receipt not durable", raw, err)
			}
		})
	}
}
