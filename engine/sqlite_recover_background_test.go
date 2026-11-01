package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteRuntimeBackgroundRecoveryBarrier(t *testing.T) {
	ctx := context.Background()
	state := sqliteSemanticFixture(t)
	at := time.Now().UTC()
	stamp := at.Format(time.RFC3339Nano)
	state.GraphActivities[0].Status = "running"
	state.GraphRuns[0].Status = "running"
	state.GraphRuns[0].EndedAt = ""
	state.GraphRuns[0].Result = nil
	state.GraphActivations[0].Status = "running"
	state.GraphActivations[0].EndedAt = ""
	state.Sessions[2].Status = "running"
	state.GraphNotifications = nil
	state.AutomationRuns[0].Status = "running"
	state.AutomationRuns[0].FinishedAt = ""
	state.Sessions[1].Status = "running"
	state.Sessions[1].Automation.Status = "running"
	state.EngineSettings.TimeZone = "UTC"
	state.Automations[0].Enabled = true
	state.Automations[0].Schedule.Time = at.Add(6 * time.Hour).Format("15:04")
	state.Automations[0].NextScheduledAt = at.Add(-time.Hour).Format(time.RFC3339Nano)
	state.AutomationRecoveries[0].Delivery = "delivering"
	e := state.Analytics.Executions["execution"]
	e.Accepted = stamp
	e.Waits = map[string]bool{"permission": true}
	state.Analytics.Executions["execution"] = e
	g := state.Analytics.Graphs["run"]
	g.End = ""
	g.Interrupted = false
	state.Analytics.Graphs["run"] = g
	for _, validate := range []func(*diskState) error{validateGraphRecords, validateAutomationRecords, validateAnalytics} {
		if err := validate(&state); err != nil {
			t.Fatal("invalid active fixture:", err)
		}
	}
	dir := t.TempDir()
	body, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	var snapshotDigest string
	if err := store.readers.QueryRow(`SELECT digest FROM data_graph_snapshots`).Scan(&snapshotDigest); err != nil {
		t.Fatal(err)
	}
	err = recoverSQLiteRuntime(ctx, store, at, func(phase string) error {
		if phase == "recovery:analytics" {
			return errors.New("injected restart between phases")
		}
		return nil
	})
	if err == nil {
		t.Fatal("recovery phase interruption not observed")
	}
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	store, err = openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	if err := recoverSQLiteRuntime(ctx, store, at, nil); err != nil {
		t.Fatal(err)
	}
	var run GraphRun
	if err := decodeSQLiteRecord(ctx, store.readers, "graph_runs", "run", 1<<20, &run); err != nil || run.Status != "interrupted" || run.Result == nil {
		t.Fatal("graph did not settle", err)
	}
	var digest string
	if err := store.readers.QueryRow(`SELECT digest FROM data_graph_snapshots`).Scan(&digest); err != nil || digest != snapshotDigest {
		t.Fatal("recovery rewrote captured definition", err)
	}
	var notification RunNotification
	if err := decodeSQLiteRecord(ctx, store.readers, "graph_notifications", "graph-result:run", 1<<20, &notification); err != nil || notification.Status != "pending" {
		t.Fatal("graph result notification lost", err)
	}
	var automation AutomationRun
	if err := decodeSQLiteRecord(ctx, store.readers, "automation_runs", "automation-run", 1<<20, &automation); err != nil || automation.Status != "execution_failed" {
		t.Fatal("automation was not interrupted", err)
	}
	var recovery AutomationRecovery
	if err := decodeSQLiteRecord(ctx, store.readers, "automation_recoveries", "recovery", 1<<20, &recovery); err != nil || recovery.Delivery != "uncertain" {
		t.Fatal("uncertain notice was replayed", err)
	}
	var recoveries int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_automation_recoveries`).Scan(&recoveries); err != nil || recoveries != 3 {
		t.Fatal("missing interrupted/missed recovery", recoveries, err)
	}
	var zone string
	if err := store.readers.QueryRow(`SELECT id FROM data_automation_recoveries WHERE x_source='missed_schedule'`).Scan(&zone); err != nil {
		t.Fatal(err)
	}
	if err := decodeSQLiteRecord(ctx, store.readers, "automation_recoveries", zone, 1<<20, &recovery); err != nil || recovery.TimeZone != "UTC" || recovery.NextOccurrenceAt == "" {
		t.Fatal("missed interval lost calendar context", err)
	}
	var execution AnalyticsExecution
	if err := decodeSQLiteRecord(ctx, store.readers, "analytics_executions", sqliteOwnedKey("settings", "execution"), 1<<20, &execution); err != nil || execution.Ended != e.Last || execution.UnknownUntil != stamp {
		t.Fatal("analytics interruption invented a tail", err)
	}
	var observations int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_analytics_usage`).Scan(&observations); err != nil || observations != 1 {
		t.Fatal("usage observations changed", err)
	}
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_automation_triggers`).Scan(&observations); err != nil || observations != 1 {
		t.Fatal("startup replayed missed work", err)
	}
	var beforeRevision string
	if err := store.readers.QueryRow(`SELECT value FROM store_meta WHERE key='revision'`).Scan(&beforeRevision); err != nil {
		t.Fatal(err)
	}
	if err := recoverSQLiteRuntime(ctx, store, at.Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	var afterRevision string
	if err := store.readers.QueryRow(`SELECT value FROM store_meta WHERE key='revision'`).Scan(&afterRevision); err != nil || beforeRevision != afterRevision {
		t.Fatal("idempotent recovery repeated writes", err)
	}
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_automation_recoveries`).Scan(&observations); err != nil || observations != recoveries {
		t.Fatal("recovery receipts duplicated", err)
	}
}
