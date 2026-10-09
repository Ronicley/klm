package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Tombstones contain no conversation content and prevent delayed updates/retries
// from resurrecting deleted sessions. Pending cleanup survives engine restarts.
type deletedSession struct {
	RootID         string   `json:"rootId"`
	Images         []string `json:"images,omitempty"`
	CleanupPending bool     `json:"cleanupPending,omitempty"`
}

func (d *diskState) deletedSessionIDs() []string {
	ids := make([]string, 0, len(d.DeletedSessions))
	for id := range d.DeletedSessions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (d *diskState) sessionDeletionSet(id string) (map[string]bool, map[string]bool) {
	ids, runs := map[string]bool{id: true}, map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, run := range d.GraphRuns {
			if ids[run.ConversationID] && !runs[run.ID] {
				runs[run.ID], changed = true, true
			}
		}
		for _, s := range d.Sessions {
			if !ids[s.ID] && (ids[s.ParentID] || runs[s.GraphRunID]) {
				ids[s.ID], changed = true, true
			}
		}
	}
	return ids, runs
}

func removeSessionRecords(d *diskState, root string, ids, runs map[string]bool) {
	if d.DeletedSessions == nil {
		d.DeletedSessions = map[string]deletedSession{}
	}
	if d.DeletedOperations == nil {
		d.DeletedOperations = map[string]bool{}
	}
	for id := range ids {
		record := deletedSession{RootID: root, CleanupPending: true}
		for imageID, image := range d.Images {
			if image.SessionID == id {
				record.Images = append(record.Images, imageID)
				delete(d.Images, imageID)
			}
		}
		d.DeletedSessions[id] = record
		delete(d.Native, id)
		for _, q := range d.session(id).Queue {
			delete(d.QueuePayloads, q.ID)
		}
		for key := range d.AcceptedMessages {
			if strings.HasPrefix(key, id+"/") {
				delete(d.AcceptedMessages, key)
			}
		}
	}
	for _, receipt := range d.SessionSpawns {
		if ids[receipt.SessionID] && !ids[receipt.From] {
			d.DeletedOperations["spawn/"+receipt.From+"/"+receipt.OperationID] = true
		}
	}
	for _, receipt := range d.SessionCommands {
		if ids[receipt.Request.SessionID] && !ids[receipt.From] {
			d.DeletedOperations["send/"+receipt.From+"/"+receipt.Request.OperationID] = true
		}
	}
	d.SessionSpawns = slices.DeleteFunc(d.SessionSpawns, func(x SessionSpawn) bool { return ids[x.From] || ids[x.SessionID] })
	d.SessionCommands = slices.DeleteFunc(d.SessionCommands, func(x SessionCommand) bool { return ids[x.From] || ids[x.Request.SessionID] })
	d.Consultations = slices.DeleteFunc(d.Consultations, func(x Consultation) bool { return ids[x.From] || ids[x.To] })
	d.Grants = slices.DeleteFunc(d.Grants, func(x permissionGrant) bool { return ids[x.SessionID] })
	d.GraphActivities = slices.DeleteFunc(d.GraphActivities, func(x GraphActivity) bool { return ids[x.ConversationID] })
	d.GraphRuns = slices.DeleteFunc(d.GraphRuns, func(x GraphRun) bool { return runs[x.ID] })
	d.GraphActivations = slices.DeleteFunc(d.GraphActivations, func(x GraphActivation) bool { return runs[x.RunID] })
	d.GraphDeliveries = slices.DeleteFunc(d.GraphDeliveries, func(x GraphDelivery) bool { return runs[x.RunID] })
	d.GraphJoinRounds = slices.DeleteFunc(d.GraphJoinRounds, func(x JoinRound) bool { return runs[x.RunID] })
	d.GraphNotifications = slices.DeleteFunc(d.GraphNotifications, func(x RunNotification) bool { return ids[x.ConversationID] })
	d.GraphWorkspaceUses = slices.DeleteFunc(d.GraphWorkspaceUses, func(x WorkspaceUse) bool { return runs[x.RunID] })
	// Worktrees stay on disk; only their deleted run's provenance is removed.
	d.GraphWorkspaces = slices.DeleteFunc(d.GraphWorkspaces, func(x WorkspaceRecord) bool { return runs[x.CreatedByRunID] })
	d.Sessions = slices.DeleteFunc(d.Sessions, func(x Session) bool { return ids[x.ID] })
}

func (a *app) deleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.mu.Lock()
	if record := a.state.DeletedSessions[id]; record.RootID != "" && record.RootID != id {
		a.mu.Unlock()
		fail(w, 400, "Only top-level project sessions can be deleted.")
		return
	}
	var runtimes []*sessionRuntime
	if a.state.DeletedSessions[id].RootID == "" {
		s := a.state.session(id)
		if s == nil {
			a.mu.Unlock()
			fail(w, 404, "Session not found.")
			return
		}
		if s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
			a.mu.Unlock()
			fail(w, 400, "Only top-level project sessions can be deleted.")
			return
		}
		ids, runs := a.state.sessionDeletionSet(id)
		for child := range ids {
			if a.runs[child] != nil || a.graphStarting[child] || a.state.session(child).Status == "running" {
				a.mu.Unlock()
				fail(w, 409, "Stop running chats and finish active graphs before deleting this session.")
				return
			}
			if runtime := a.runtimes[child]; runtime != nil {
				runtime.mu.Lock()
				group, active := runtime.group, runtime.turnActive
				runtime.mu.Unlock()
				if active || group != nil && group.HasWorkload() {
					a.mu.Unlock()
					fail(w, 409, "Stop this session's running processes before deleting it.")
					return
				}
			}
		}
		for runID := range runs {
			if graphRunActive(a.state.graphRun(runID).Status) || a.graphRuns[runID] != nil {
				a.mu.Unlock()
				fail(w, 409, "Finish the active graph before deleting this session. Paused graphs are still active.")
				return
			}
		}
		for _, c := range a.state.Consultations {
			if (ids[c.From] || ids[c.To]) && (!consultationTerminal(c.Status) || c.Delivery == "delivering") {
				a.mu.Unlock()
				fail(w, 409, "Finish or cancel pending consultations before deleting this session.")
				return
			}
		}
		// A workspace's creator record cannot disappear while another conversation
		// references it. Never delete that other conversation's provenance.
		for _, activity := range a.state.GraphActivities {
			if ids[activity.ConversationID] {
				continue
			}
			for _, workspaceID := range activity.Workspace.Reuse {
				if workspace := a.state.graphWorkspace(workspaceID); workspace != nil && runs[workspace.CreatedByRunID] {
					a.mu.Unlock()
					fail(w, 409, "Another session references a workspace created by this session's graph.")
					return
				}
			}
		}
		if err := a.commitLocked(func(d *diskState) { removeSessionRecords(d, id, ids, runs) }); err != nil {
			a.mu.Unlock()
			fail(w, 503, err.Error())
			return
		}
		for child := range ids {
			if runtime := a.runtimes[child]; runtime != nil {
				runtimes = append(runtimes, runtime)
				delete(a.runtimes, child)
			}
			delete(a.historyJournal, child)
			delete(a.streamIndexes, child)
		}
		for runID := range runs {
			delete(a.graphValidation, runID)
		}
		a.notifyAllLocked()
	}
	deletedIDs := a.state.deletedSessionIDs()
	a.mu.Unlock()
	var cleanupErr error
	for _, runtime := range runtimes {
		cleanupErr = errors.Join(cleanupErr, runtime.shutdown())
	}
	if cleanupErr == nil {
		cleanupErr = a.cleanupDeletedSessions(id)
	}
	response := map[string]any{"deletedSessionIds": deletedIDs}
	if cleanupErr != nil {
		response["cleanupError"] = "Session deleted, but file cleanup is incomplete. Retry Delete or restart the engine. " + cleanupErr.Error()
	}
	respond(w, 200, response)
}

func (a *app) cleanupDeletedSessions(root string) error {
	a.mu.Lock()
	entries := map[string]deletedSession{}
	for id, record := range a.state.DeletedSessions {
		if record.CleanupPending && (root == "" || record.RootID == root) {
			entries[id] = record
		}
	}
	a.mu.Unlock()
	if len(entries) == 0 {
		return nil
	}
	// Two rotations also cover deletion committed while an older sealed segment
	// existed. Do not remove files until the durable checkpoint excludes history.
	for range 2 {
		if err := a.checkpointJournal(); err != nil {
			return err
		}
	}
	for id, record := range entries {
		hash := sha256.Sum256([]byte(id))
		if err := os.RemoveAll(filepath.Join(a.dir, "attachments", hex.EncodeToString(hash[:]))); err != nil {
			return err
		}
		// Agent/harness changes create additional owned Pi histories with an ID
		// suffix. Delete those too, without following arbitrary native paths.
		files, err := os.ReadDir(filepath.Join(a.dir, "sessions"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, file := range files {
			name := file.Name()
			owned := name == id+".jsonl"
			if strings.HasPrefix(name, id+"-") && strings.HasSuffix(name, ".jsonl") {
				suffix, err := hex.DecodeString(strings.TrimSuffix(strings.TrimPrefix(name, id+"-"), ".jsonl"))
				owned = err == nil && len(suffix) == 16
			}
			if owned && !file.IsDir() {
				if err := os.Remove(filepath.Join(a.dir, "sessions", name)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
		for _, image := range record.Images {
			if err := os.Remove(filepath.Join(a.dir, "images", image)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.commitLocked(func(d *diskState) {
		for id := range entries {
			record := d.DeletedSessions[id]
			record.CleanupPending, record.Images = false, nil
			d.DeletedSessions[id] = record
		}
	})
}
