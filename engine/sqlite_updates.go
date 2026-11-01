package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

func (a *app) scopedSQLUpdates(w http.ResponseWriter, r *http.Request) {
	watches := r.URL.Query()["watch"]
	if len(watches) > 64 {
		fail(w, 400, "Too many update scopes.")
		return
	}
	scopes := []sqliteChangeScope{{Kind: r.URL.Query().Get("scope"), ID: r.URL.Query().Get("id")}}
	if len(watches) > 0 {
		scopes = nil
		seen := map[string]bool{}
		for _, watch := range watches {
			kind, id, ok := strings.Cut(watch, "/")
			if !ok {
				fail(w, 400, "Invalid update scope.")
				return
			}
			if !seen[watch] {
				scopes = append(scopes, sqliteChangeScope{Kind: kind, ID: id})
				seen[watch] = true
			}
		}
	}
	for i := range scopes {
		scope := &scopes[i]
		if scope.Kind == "" {
			scope.Kind = "global"
		}
		switch scope.Kind {
		case "global":
			if scope.ID != "" {
				fail(w, 400, "Global scope has no identity.")
				return
			}
		case "project":
			p, err := a.store.readProjectMeta(r.Context(), scope.ID)
			if err != nil {
				sqlHTTPError(w, err)
				return
			}
			if p.Removed {
				fail(w, 404, "Project not found.")
				return
			}
		case "conversation":
			s, err := a.store.readSessionMeta(r.Context(), scope.ID)
			if err != nil {
				sqlHTTPError(w, err)
				return
			}
			if s.GraphRunID != "" {
				fail(w, 404, "Conversation not found.")
				return
			}
			scope.ProjectID = s.ProjectID
			if scope.ProjectID != "" {
				p, err := a.store.readProjectMeta(r.Context(), scope.ProjectID)
				if err != nil {
					sqlHTTPError(w, err)
					return
				}
				if p.Removed {
					fail(w, 404, "Project not found.")
					return
				}
			}
		default:
			fail(w, 400, "Invalid update scope.")
			return
		}
	}
	if a.sqlChanges == nil {
		fail(w, 503, "Live updates are unavailable.")
		return
	}
	subscription, err := a.sqlChanges.subscribeScopes(scopes, len(watches) > 0)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	defer a.sqlChanges.unsubscribe(subscription)
	// Register first. A concurrent commit is either covered by this revision or
	// remains queued. New scopes always reset, regardless of a global cursor.
	var revision string
	if err := a.store.readers.QueryRowContext(r.Context(), `SELECT value FROM store_meta WHERE key='revision'`).Scan(&revision); err != nil {
		sqlHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func(body []byte) error {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := w.Write(append(append([]byte("data: "), body...), '\n', '\n')); err != nil {
			return err
		}
		return controller.Flush()
	}
	ready, _ := json.Marshal(sqliteChangeNotice{Kind: "ready", Revision: revision})
	if send(ready) != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case body, open := <-subscription.notices:
			if !open {
				return
			}
			if send(body) != nil {
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
