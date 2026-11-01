package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteMigrationCompatibilityTransforms(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			state := sqliteLegacyFixture()
			state.Version = version
			state.GraphRevision = 7
			state.Projects[0].Folders = []string{"Archived", "Active"}
			state.Projects[0].ArchivedFolders = []string{"Archived"}
			state.Sessions[0].Workspace = "Active"
			state.Sessions[1].Workspace = "Archived"
			state.Sessions = append(state.Sessions,
				Session{ID: "side", ParentID: "a", ProjectID: "p", Status: "idle", Events: []Event{}},
				Session{ID: "general", Role: sessionRoleGeneralAgent, Status: "idle", ExecutionCWD: filepath.Join(t.TempDir(), "work"), HarnessLocked: true, Events: []Event{{ID: "notice", Type: "status", Data: map[string]any{"automationRecoveryNotice": true}}}},
			)
			state.Sessions[0].Capabilities.Preparing = &CapabilitySnapshot{Version: 1}
			body, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			oracleDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(oracleDir, "state.json"), body, 0600); err != nil {
				t.Fatal(err)
			}
			oracle, err := loadState(oracleDir)
			if err != nil {
				t.Fatal(err)
			}
			// v1's old loader writes a JSON-journal downgrade fence. The SQL importer
			// retains the source journal format and installs its own fence at cutover.
			oracle.JournalFormat = state.JournalFormat
			store := sqliteTestStage(t, state)
			if err := normalizeLegacyCheckpoint(context.Background(), store); err != nil {
				t.Fatal(err)
			}
			got := sqliteTestReadStage(t, store)
			wantJSON, _ := json.Marshal(oracle)
			gotJSON, _ := json.Marshal(got)
			if !bytes.Equal(wantJSON, gotJSON) {
				t.Fatalf("compatibility transforms diverged\nwant %s\ngot %s", wantJSON, gotJSON)
			}
			if err := migrateLegacyStageTopics(context.Background(), store); err != nil {
				t.Fatal(err)
			}
			got = sqliteTestReadStage(t, store)
			if got.TopicSchema != 1 || len(got.Projects[0].Topics) != 2 || got.Sessions[0].TopicID == "" || got.Sessions[1].TopicID == "" || got.Sessions[0].TopicID == got.Sessions[1].TopicID {
				t.Fatal("topic identities/memberships lost")
			}
			if !got.Projects[0].Topics[0].Archived || got.Projects[0].Topics[1].Archived {
				t.Fatal("folder archive state lost")
			}
			if got.Sessions[2].Role != sessionRoleSideAgent || got.Sessions[2].TopicID != "" || got.Sessions[3].HarnessLocked {
				t.Fatal("legacy child/general semantics changed")
			}
			first, _ := json.Marshal(got)
			if err := migrateLegacyStageTopics(context.Background(), store); err != nil {
				t.Fatal(err)
			}
			again, _ := json.Marshal(sqliteTestReadStage(t, store))
			if !bytes.Equal(first, again) {
				t.Fatal("topic migration retry changed identities")
			}
		})
	}
}
