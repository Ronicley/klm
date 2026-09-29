package main

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"
)

type updateSubscription struct {
	wake  chan struct{}
	dirty map[string]bool // protected by app.mu
	all   bool
}

// Each logical subscription carries its own cursor. Missing/expired cursors
// hydrate from a bounded reset; a current global cursor cannot skip a new chat.
func (a *app) updates(w http.ResponseWriter, r *http.Request) {
	ids := strings.Split(r.URL.Query().Get("ids"), ",")
	if len(ids) > 256 {
		fail(w, 400, "Too many observed conversations.")
		return
	}
	observed := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			observed[id] = true
		}
	}
	cursors := map[string]uint64{}
	if value := r.URL.Query().Get("cursors"); value != "" {
		if len(value) > 32<<10 || json.Unmarshal([]byte(value), &cursors) != nil {
			fail(w, 400, "Invalid conversation cursors.")
			return
		}
	}
	if cursors == nil {
		cursors = map[string]uint64{}
	}
	subscription := &updateSubscription{wake: make(chan struct{}, 1), dirty: map[string]bool{}, all: true}
	a.mu.Lock()
	for id := range observed {
		s := a.state.session(id)
		if s == nil || s.GraphRunID != "" {
			a.mu.Unlock()
			fail(w, 404, "Conversation not found.")
			return
		}
	}
	if a.updateListeners == nil {
		a.updateListeners = map[*updateSubscription]bool{}
	}
	a.updateListeners[subscription] = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.updateListeners, subscription); a.mu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func(value any) error {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err = w.Write(append(append([]byte("data: "), b...), '\n', '\n')); err != nil {
			return err
		}
		return controller.Flush()
	}
	// The listener was registered before the initial snapshot. Any concurrent
	// commit therefore wakes the next loop; session revisions deduplicate it.
	known := map[string]SessionSummary{}
	seen := cursors
	var knownProjects []Project
	var knownIDs []string
	flush := func(initial bool) error {
		a.mu.Lock()
		allDirty, dirty := subscription.all, subscription.dirty
		subscription.all, subscription.dirty = false, map[string]bool{}
		projects := []Project{}
		visible := map[string]bool{}
		for _, p := range a.state.Projects {
			if !p.Removed {
				projects = append(projects, p)
				visible[p.ID] = true
			}
		}
		changed := []SessionSummary{}
		changedIDs := map[string]bool{}
		ids := []string{}
		for _, s := range a.state.Sessions {
			if !visible[s.ProjectID] || s.GraphRunID != "" {
				continue
			}
			ids = append(ids, s.ID)
			if !allDirty && !dirty[s.ID] {
				continue
			}
			summary := *a.sessionSummaryLocked(s.ID)
			if before, ok := known[s.ID]; !ok || !reflect.DeepEqual(before, summary) {
				changed = append(changed, summary)
				changedIDs[s.ID] = true
			}
			known[s.ID] = summary
		}
		inventoryChanged := initial || !reflect.DeepEqual(knownProjects, projects) || !reflect.DeepEqual(knownIDs, ids)
		all := []SessionSummary{}
		if inventoryChanged {
			for _, id := range ids {
				all = append(all, known[id])
			}
		}
		updates := []*SessionUpdate{}
		for id := range observed {
			if !initial && !changedIDs[id] {
				continue
			}
			_, hydrated := seen[id]
			update := a.sessionUpdateLocked(id, seen[id], !hydrated)
			if update != nil {
				updates = append(updates, update)
			}
		}
		revision := a.state.GraphRevision
		harnesses := a.harnesses
		a.mu.Unlock()
		if inventoryChanged {
			if err := send(map[string]any{"kind": "inventory", "projects": projects, "sessions": all, "harnesses": harnesses, "revision": revision}); err != nil {
				return err
			}
		} else {
			for _, summary := range changed {
				if !observed[summary.ID] {
					if err := send(map[string]any{"kind": "summary", "summary": summary}); err != nil {
						return err
					}
				}
			}
		}
		for _, update := range updates {
			if err := send(map[string]any{"kind": "session", "update": update}); err != nil {
				return err
			}
			seen[update.Summary.ID] = update.Revision
		}
		knownProjects, knownIDs = projects, ids
		return nil
	}
	if flush(true) != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-subscription.wake:
			if flush(false) != nil {
				return
			}
		case <-heartbeat.C:
			_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil || controller.Flush() != nil {
				return
			}
		}
	}
}
