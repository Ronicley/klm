package main

import "testing"

func TestSQLiteSessionDeletionReferences(t *testing.T) {
	a, root := sqliteDeletionFixture(t)
	other := root
	other.ID = "independent"
	fixed := Automation{ID: "fixed", ProjectID: root.ProjectID, Revision: 1, Enabled: true, Name: "Fixed", SessionMode: "reuse_created", SessionID: root.ID, FollowUpPrompt: "Continue"}
	_, err := a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.createSession(other, a.dir); err != nil {
			return err
		}
		if err := st.putRoot("automations", fixed.ID, fixed); err != nil {
			return err
		}
		for _, s := range []Session{root, other} {
			run := AutomationRun{ID: "automation-" + s.ID, AutomationID: fixed.ID, ProjectID: s.ProjectID, SessionID: s.ID, Snapshot: fixed, Status: "succeeded"}
			if err := st.putRoot("automation_runs", run.ID, run); err != nil {
				return err
			}
			if err := st.putRoot("automation_recoveries", "recovery-"+s.ID, AutomationRecovery{ID: "recovery-" + s.ID, RunID: run.ID, Snapshot: fixed}); err != nil {
				return err
			}
		}
		if err := st.initializeAnalytics(); err != nil {
			return err
		}
		for _, s := range []Session{root, other} {
			execution := AnalyticsExecution{ID: "execution-" + s.ID, SessionID: s.ID, OwnerID: s.ID, ProjectID: s.ProjectID, Ended: now()}
			if err := st.putRecord("analytics_executions", sqliteOwnedKey("settings", execution.ID), "settings", execution.ID, -1, execution); err != nil {
				return err
			}
			if err := st.putRecord("analytics_counters", sqliteOwnedKey("settings", s.ID), "settings", s.ID, -1, AnalyticsCounter{ExecutionID: execution.ID}); err != nil {
				return err
			}
		}
		return st.putRoot("permission_grants", "owned-grant", permissionGrant{ID: "owned-grant", SessionID: root.ID})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, cleanup := requestSQLDeletion(t, a, root.ID, 200); cleanup != "" {
		t.Fatal(cleanup)
	}
	var saved Automation
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "automations", fixed.ID, 1<<20, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Enabled || saved.SessionID != root.ID || saved.Revision != fixed.Revision+1 {
		t.Fatal("fixed destination could be recreated", saved)
	}
	for _, table := range []string{"automation_runs", "automation_recoveries", "analytics_executions", "analytics_counters"} {
		var n int
		if err := a.store.readers.QueryRow("SELECT count(*) FROM data_" + table).Scan(&n); err != nil || n != 1 {
			t.Fatal("independent evidence changed or owned evidence retained", table, n, err)
		}
	}
	var grants int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_permission_grants`).Scan(&grants); err != nil || grants != 0 {
		t.Fatal("owned grant retained", grants, err)
	}
}
