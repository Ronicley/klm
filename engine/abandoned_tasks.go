package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// These are storage fields from archive/tasks-2026-10-05, not graph input.task
// or user message content. They deliberately do not belong to diskState anymore.
func abandonedTaskFields(collection string) []string {
	switch collection {
	case "":
		return []string{"taskSchema", "tasks", "taskRuns", "githubBindings", "githubReceipts", "engineTime"}
	case "sessions":
		return []string{"taskId", "taskRunId"}
	case "graphActivities":
		return []string{"taskRunId", "taskGrantId", "taskYolo"}
	case "graphRuns":
		return []string{"taskRunId", "taskYolo"}
	}
	return nil
}

func stripAbandonedTaskObject(raw json.RawMessage, collection string) (json.RawMessage, bool, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, false, err
	}
	changed := false
	for _, key := range abandonedTaskFields(collection) {
		if value, exists := object[key]; exists {
			if key == "taskSchema" && string(value) != "0" && string(value) != "1" {
				return nil, false, errors.New("unsupported abandoned Tasks schema")
			}
			delete(object, key)
			changed = true
		}
	}
	if collection == "" {
		for _, key := range []string{"sessions", "graphActivities", "graphRuns"} {
			value, exists := object[key]
			if !exists {
				continue
			}
			clean, removed, err := stripAbandonedTaskCollection(value, key)
			if err != nil {
				return nil, false, err
			}
			if removed {
				object[key], changed = clean, true
			}
		}
	}
	if !changed {
		return raw, false, nil
	}
	clean, err := json.Marshal(object)
	return clean, true, err
}

func stripAbandonedTaskCollection(raw json.RawMessage, collection string) (json.RawMessage, bool, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, false, err
	}
	changed := false
	for i := range items {
		clean, removed, err := stripAbandonedTaskObject(items[i], collection)
		if err != nil {
			return nil, false, err
		}
		items[i], changed = clean, changed || removed
	}
	if !changed {
		return raw, false, nil
	}
	clean, err := json.Marshal(items)
	return clean, true, err
}

func stripAbandonedTaskPatches(record *streamRecord) error {
	patches := make([]statePatch, 0, len(record.Transaction))
	for _, patch := range record.Transaction {
		path := patch.Path
		if len(path) > 0 && slices.Contains(abandonedTaskFields(""), path[0]) ||
			len(path) >= 3 && slices.Contains(abandonedTaskFields(path[0]), path[2]) {
			continue
		}
		if len(path) > 0 && len(abandonedTaskFields(path[0])) > 0 && len(patch.Value) > 0 {
			var err error
			if len(path) == 1 {
				patch.Value, _, err = stripAbandonedTaskCollection(patch.Value, path[0])
			} else if len(path) == 2 {
				patch.Value, _, err = stripAbandonedTaskObject(patch.Value, path[0])
			}
			if err != nil {
				return err
			}
		}
		patches = append(patches, patch)
	}
	record.Transaction = patches
	return nil
}

// Called only with the engine data-directory lock held, before normal loading.
// Replay into an isolated copy first: never discard ordinary journal mutations
// or restore the old pre-Tasks backup (which would also roll back user history).
func removeAbandonedTasks(dir string) error {
	checkpoint, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	clean, changed, err := stripAbandonedTaskObject(checkpoint, "")
	if err != nil || !changed {
		return err
	}
	stage, err := os.MkdirTemp(dir, ".tasks-cleanup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage) // MkdirTemp created this directory under dir.
	if err := os.WriteFile(filepath.Join(stage, "state.json"), clean, 0600); err != nil {
		return err
	}
	originals := map[string][]byte{"state.json": checkpoint}
	for _, name := range []string{"stream.sealed", "stream.journal"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		originals[name] = data
		if err := os.WriteFile(filepath.Join(stage, name), data, 0600); err != nil {
			return err
		}
	}
	state, err := loadState(stage)
	if err != nil {
		return err
	}
	for _, name := range []string{"stream.sealed", "stream.journal"} {
		if err := replayJournalFile(filepath.Join(stage, name), &state, stripAbandonedTaskPatches); err != nil {
			return err
		}
	}
	// In particular, never convert Task grants into user graph authorization.
	if err := saveState(stage, &state); err != nil {
		return err
	}
	backup, err := os.MkdirTemp(dir, "backup-before-tasks-removal-")
	if err != nil {
		return err
	}
	for name, data := range originals {
		file, err := os.OpenFile(filepath.Join(backup, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(data)
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return err
		}
	}
	if err := saveState(dir, &state); err != nil {
		return err
	}
	// The durable checkpoint covers all journal revisions before either removal.
	// A crash here leaves only an already-checkpointed prefix for normal replay.
	for _, name := range []string{"stream.sealed", "stream.journal"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("checkpoint cleaned; cannot retire %s: %w", name, err)
		}
	}
	return nil
}
