package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

func (st *sqliteSessionTx) webhookConnection(project, id string) (WebhookConnection, error) {
	var c WebhookConnection
	err := decodeSQLiteRecord(st.ctx, st.tx, "webhook_connections", id, 64<<10, &c)
	if err != nil {
		return c, err
	}
	if project != "" && c.ProjectID != project {
		return c, sql.ErrNoRows
	}
	available, err := st.projectAvailable(c.ProjectID)
	if err != nil {
		return c, err
	}
	if !available {
		return c, sql.ErrNoRows
	}
	return c, nil
}
func (s *sqliteStore) readWebhookConnection(ctx context.Context, project, id string) (WebhookConnection, error) {
	var c WebhookConnection
	err := s.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		c, err = st.webhookConnection(project, id)
		return err
	})
	return c, err
}
func (a *app) createSQLWebhook(w http.ResponseWriter, r *http.Request, name, secret, retention string) {
	c := WebhookConnection{ID: newID(), ProjectID: r.PathValue("id"), Provider: "github", Name: name, Enabled: true, Secret: secret, Retention: retention, CreatedAt: now(), UpdatedAt: now()}
	a.mu.Lock()
	err := a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		available, err := st.projectAvailable(c.ProjectID)
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		var all, project int
		if err := st.tx.QueryRowContext(st.ctx, `SELECT count(*),coalesce(sum(project_id=?),0) FROM data_webhook_connections`, c.ProjectID).Scan(&all, &project); err != nil {
			return err
		}
		if all >= maxWebhookConnections || project >= maxProjectWebhooks {
			return controlError(409, "Webhook connection limit reached.")
		}
		if err := validateWebhookConnection(c); err != nil {
			return err
		}
		return st.putRoot("webhook_connections", c.ID, c)
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 201, webhookConnectionView(c))
}
func (a *app) mutateSQLWebhook(w http.ResponseWriter, r *http.Request, change func(*WebhookConnection)) {
	var c WebhookConnection
	a.mu.Lock()
	err := a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		var err error
		c, err = st.webhookConnection(r.PathValue("id"), r.PathValue("webhookId"))
		if err != nil {
			return err
		}
		change(&c)
		c.UpdatedAt = maxWebhookTimestamp(c.UpdatedAt, time.Now().UTC())
		if err := validateWebhookConnection(c); err != nil {
			return err
		}
		return st.putRoot("webhook_connections", c.ID, c)
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, webhookConnectionView(c))
}
func (a *app) deleteSQLWebhook(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	err := a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
		c, err := st.webhookConnection(r.PathValue("id"), r.PathValue("webhookId"))
		if err != nil {
			return err
		}
		if err := st.eachMatching("webhook_events", `x_webhookId=?`, []any{c.ID}, st.deleteWebhookEvent); err != nil {
			return err
		}
		return st.deleteRecord("webhook_connections", c.ID)
	})
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"removed": true})
}
func (a *app) acceptSQLWebhookHTTP(w http.ResponseWriter, r *http.Request, previous WebhookConnection, delivery, kind, signature string, body []byte, at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.webhookWritableLocked(w) {
		return
	}
	current, err := a.store.readWebhookConnection(r.Context(), "", previous.ID)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	if previous.Secret != current.Secret || previous.ProjectID != current.ProjectID {
		fail(w, 409, "Webhook secret changed during delivery. Redeliver with the current secret.")
		return
	}
	receipt, changes, err := a.store.acceptGitHubWebhook(r.Context(), current.ID, delivery, kind, signature, body, at)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	if a.sqlChanges != nil {
		a.sqlChanges.publish(changes)
	}
	a.wakeWebhooksLocked()
	status := 202
	if receipt.Duplicate {
		status = 200
	}
	respond(w, status, receipt)
}
func (st *sqliteSessionTx) webhookEventView(e WebhookStoredEvent) (webhookEventDTO, error) {
	v := webhookEventDTO{ID: e.ID, ProjectID: e.ProjectID, WebhookID: e.WebhookID, Provider: e.Provider, DeliveryID: e.DeliveryID, EventType: e.EventType, Action: e.Action, IsPing: e.IsPing, ReceivedAt: e.ReceivedAt, ExpiresAt: e.ExpiresAt, ReceivedSequence: sqliteRevision(e.ReceivedSequence), Status: "pending", PayloadBytes: e.PayloadBytes}
	var release WebhookRelease
	err := decodeSQLiteRecord(st.ctx, st.tx, "webhook_releases", e.ID, 64<<10, &release)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	v.Status, v.EmittedAt, v.ReleaseSequence = "emitted", release.EmittedAt, sqliteRevision(release.ReleaseSequence)
	return v, nil
}
func (a *app) listSQLWebhooks(w http.ResponseWriter, r *http.Request) {
	limit, cursor, err := parseWebhookPage(r, "connections")
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	page := webhookPage[webhookConnectionDTO]{Items: []webhookConnectionDTO{}}
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		available, err := st.projectAvailable(cursor.ProjectID)
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		ids, err := sqliteQueryIDs(st.ctx, tx, `SELECT id FROM data_webhook_connections WHERE project_id=? AND (created_at>? OR created_at=? AND id>?) ORDER BY created_at,id LIMIT ?`, cursor.ProjectID, sqliteTimeKey(cursor.CreatedAt), sqliteTimeKey(cursor.CreatedAt), cursor.ID, limit+1)
		if err != nil {
			return err
		}
		page.HasMore = len(ids) > limit
		if page.HasMore {
			ids = ids[:limit]
		}
		for _, id := range ids {
			c, err := st.webhookConnection(cursor.ProjectID, id)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, webhookConnectionView(c))
			cursor.CreatedAt, cursor.ID = c.CreatedAt, c.ID
		}
		if page.HasMore {
			page.NextCursor = encodeWebhookCursor(cursor)
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, page)
}
func (a *app) listSQLWebhookEvents(w http.ResponseWriter, r *http.Request) {
	limit, cursor, err := parseWebhookPage(r, "events")
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	page := webhookPage[webhookEventDTO]{Items: []webhookEventDTO{}}
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		if _, err := st.webhookConnection(cursor.ProjectID, cursor.WebhookID); err != nil {
			return err
		}
		ids, err := sqliteQueryIDs(st.ctx, tx, `SELECT id FROM data_webhook_events WHERE x_webhookId=? AND (x_expiresAt='' OR x_expiresAt>?) AND (?='' OR length(x_receivedSequence)<length(?) OR length(x_receivedSequence)=length(?) AND x_receivedSequence<?) ORDER BY length(x_receivedSequence) DESC,x_receivedSequence DESC LIMIT ?`, cursor.WebhookID, sqliteTimeKey(now()), cursor.Sequence, cursor.Sequence, cursor.Sequence, cursor.Sequence, limit+1)
		if err != nil {
			return err
		}
		page.HasMore = len(ids) > limit
		if page.HasMore {
			ids = ids[:limit]
		}
		for _, id := range ids {
			var e WebhookStoredEvent
			if err := decodeSQLiteRecord(st.ctx, tx, "webhook_events", id, 64<<10, &e); err != nil {
				return err
			}
			view, err := st.webhookEventView(e)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, view)
			cursor.Sequence = view.ReceivedSequence
		}
		if page.HasMore {
			page.NextCursor = encodeWebhookCursor(cursor)
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, page)
}
func (a *app) getSQLWebhookEvent(w http.ResponseWriter, r *http.Request) {
	started := false
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		if _, err := st.webhookConnection(r.PathValue("id"), r.PathValue("webhookId")); err != nil {
			return err
		}
		var e WebhookStoredEvent
		if err := decodeSQLiteRecord(st.ctx, tx, "webhook_events", r.PathValue("eventId"), 64<<10, &e); err != nil {
			return err
		}
		if e.ProjectID != r.PathValue("id") || e.WebhookID != r.PathValue("webhookId") || webhookExpired(e, time.Now().UTC()) {
			return sql.ErrNoRows
		}
		view, err := st.webhookEventView(e)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(view)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json")
		started = true
		if _, err := io.WriteString(w, `{"event":`+string(encoded)+`,"payload":`); err != nil {
			return err
		}
		if err := writeSQLiteRecordContent(st.ctx, tx, w, "webhook_payloads", sqliteOwnedKey(e.ID, "")); err != nil {
			return err
		}
		_, err = io.WriteString(w, "}")
		return err
	})
	if err != nil && !started {
		sqlHTTPError(w, err)
	}
}
func (a *app) listSQLWebhookReleases(w http.ResponseWriter, r *http.Request) {
	limit, _, err := parseWebhookPage(r, "releases")
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	afterText := "0"
	if values, ok := r.URL.Query()["after"]; ok {
		if len(values) != 1 {
			fail(w, 400, "Invalid release cursor.")
			return
		}
		afterText = values[0]
	}
	after, valid := decimalWebhookSequence(afterText)
	if !valid {
		fail(w, 400, "Release cursor must be an unsigned decimal string.")
		return
	}
	afterText = sqliteRevision(after)
	items := []webhookEventDTO{}
	high, next := "", ""
	more := false
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		available, err := st.projectAvailable(r.PathValue("id"))
		if err != nil {
			return err
		}
		if !available {
			return sql.ErrNoRows
		}
		watermark, err := st.sequence("webhook_release_sequence")
		if err != nil {
			return err
		}
		if after > watermark {
			return controlError(400, "Release cursor exceeds the high watermark.")
		}
		high = sqliteRevision(watermark)
		next = high
		ids, err := sqliteQueryIDs(st.ctx, tx, `SELECT e.id FROM data_webhook_releases r JOIN data_webhook_events e ON e.id=r.x_eventId WHERE e.project_id=? AND (e.x_expiresAt='' OR e.x_expiresAt>?) AND (length(r.x_releaseSequence)>length(?) OR length(r.x_releaseSequence)=length(?) AND r.x_releaseSequence>?) ORDER BY length(r.x_releaseSequence),r.x_releaseSequence LIMIT ?`, r.PathValue("id"), sqliteTimeKey(now()), afterText, afterText, afterText, limit+1)
		if err != nil {
			return err
		}
		more = len(ids) > limit
		if more {
			ids = ids[:limit]
		}
		for _, id := range ids {
			var e WebhookStoredEvent
			if err := decodeSQLiteRecord(st.ctx, tx, "webhook_events", id, 64<<10, &e); err != nil {
				return err
			}
			view, err := st.webhookEventView(e)
			if err != nil {
				return err
			}
			items = append(items, view)
		}
		if more {
			next = items[len(items)-1].ReleaseSequence
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "nextAfter": next, "highWatermark": high, "hasMore": more})
}
