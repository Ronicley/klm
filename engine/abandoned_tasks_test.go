package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func abandonedTasksFixture(t *testing.T) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	data := []byte(`{"version":2,"journalFormat":1,"taskSchema":1,"tasks":[{"id":"t"}],"taskRuns":[],"githubBindings":[],"githubReceipts":[],"engineTime":{"timezone":"UTC"},"projects":[{"id":"p","folders":[]}],"sessions":[{"id":"s","projectId":"p","taskId":"t","taskRunId":"run","status":"idle","events":[{"id":"e","type":"assistant","text":"taskSchema belongs in this message"}]}],"native":{"s":{"id":"native"}}}`)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, data
}

func TestAbandonedTasksCleanupPreservesJournalAndHistory(t *testing.T) {
	dir, original := abandonedTasksFixture(t)
	if err := appendStreamRecord(dir, streamRecord{Format: 1, Revision: 1, Transaction: []statePatch{
		{Path: []string{"tasks"}, Value: json.RawMessage(`[]`)},
		{Path: []string{"sessions", "0", "taskRunId"}, Value: json.RawMessage(`"new-run"`)},
		{Path: []string{"sessions", "0", "title"}, Value: json.RawMessage(`"Preserved title"`)},
		{Path: []string{"sessions", "0", "events", "0", "text"}, Value: json.RawMessage(`"Latest journal text"`)},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(streamJournalPath(dir), sealedJournalPath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := appendStreamRecord(dir, streamRecord{Format: 1, Revision: 2, Transaction: []statePatch{
		{Path: []string{"githubReceipts"}, Value: json.RawMessage(`[]`)},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := removeAbandonedTasks(dir); err != nil {
		t.Fatal(err)
	}
	state, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if state.GraphRevision != 2 || state.session("s").Title != "Preserved title" || state.session("s").Events[0].Text != "Latest journal text" || state.Native["s"].ID != "native" {
		t.Fatal("cleanup lost ordinary state or journal edits")
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "backup-before-tasks-removal-*"))
	if len(backups) != 1 {
		t.Fatal("missing backup")
	}
	backup, err := os.ReadFile(filepath.Join(backups[0], "state.json"))
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatal("backup did not preserve original checkpoint")
	}
	for _, name := range []string{"stream.sealed", "stream.journal"} {
		if _, err := os.Stat(filepath.Join(backups[0], name)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatal("old journal still live")
		}
	}
	if err := removeAbandonedTasks(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := filepath.Glob(filepath.Join(dir, "backup-before-tasks-removal-*"))
	if len(after) != 1 {
		t.Fatal("cleanup is not idempotent")
	}
}

func TestAbandonedTasksCleanupRefusesUnknownFields(t *testing.T) {
	dir, original := abandonedTasksFixture(t)
	original = bytes.Replace(original, []byte(`"taskSchema":1`), []byte(`"taskSchema":1,"unrecognizedFeature":true`), 1)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeAbandonedTasks(dir); err == nil {
		t.Fatal("unknown field was silently removed")
	}
	after, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("failed cleanup modified checkpoint")
	}
}

func TestAbandonedTasksCleanupRefusesCorruptJournal(t *testing.T) {
	dir, original := abandonedTasksFixture(t)
	journal := []byte{1, 0, 0, 0, 0, 0, 0, 0, 'x'}
	if err := os.WriteFile(streamJournalPath(dir), journal, 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeAbandonedTasks(dir); err == nil {
		t.Fatal("corrupt journal accepted")
	}
	after, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("failed cleanup modified checkpoint")
	}
	after, err = os.ReadFile(streamJournalPath(dir))
	if err != nil || !bytes.Equal(after, journal) {
		t.Fatal("failed cleanup modified journal")
	}
}
