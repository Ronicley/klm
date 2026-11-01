package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

type sqliteRequestError struct {
	Status  int
	Message string
}

func (e *sqliteRequestError) Error() string { return e.Message }
func sqliteReject(status int, message string) error {
	return &sqliteRequestError{Status: status, Message: message}
}

type sqliteWebhookReceipt struct {
	EventID   string `json:"eventId"`
	Accepted  bool   `json:"accepted"`
	Duplicate bool   `json:"duplicate"`
}

// The transport still owns body limits, header multiplicity and the two ingress
// slots. This atomic admission rechecks the current secret/project/config and
// commits immutable payload+pending+quota+sequence together, independent of UI.
func (s *sqliteStore) acceptGitHubWebhook(ctx context.Context, connectionID, deliveryID, eventType, signature string, body []byte, received time.Time) (sqliteWebhookReceipt, sqliteSessionChanges, error) {
	var receipt sqliteWebhookReceipt
	if len(body) > maxWebhookPayloadBytes {
		return receipt, sqliteSessionChanges{}, sqliteReject(413, "Webhook payload exceeds 25 MiB.")
	}
	if !webhookASCII(deliveryID, 128, false) || !webhookASCII(eventType, 100, true) {
		return receipt, sqliteSessionChanges{}, sqliteReject(400, "Invalid GitHub delivery or event header.")
	}
	if len(signature) != 71 || !strings.HasPrefix(signature, "sha256=") {
		return receipt, sqliteSessionChanges{}, sqliteReject(401, "Invalid webhook signature.")
	}
	digest, err := hex.DecodeString(signature[7:])
	if err != nil {
		return receipt, sqliteSessionChanges{}, sqliteReject(401, "Invalid webhook signature.")
	}
	payload := string(body)
	action, err := webhookPayloadAction(payload)
	if err != nil {
		return receipt, sqliteSessionChanges{}, sqliteReject(400, err.Error())
	}
	received = received.UTC()
	changes, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		var c WebhookConnection
		if err := decodeSQLiteRecord(ctx, st.tx, "webhook_connections", connectionID, 64<<10, &c); errors.Is(err, sql.ErrNoRows) {
			return sqliteReject(404, "Webhook not found.")
		} else if err != nil {
			return err
		}
		active, err := st.projectAvailable(c.ProjectID)
		if err != nil {
			return err
		}
		if !active {
			return sqliteReject(404, "Webhook not found.")
		}
		if !c.Enabled {
			return sqliteReject(409, "Webhook is disabled.")
		}
		mac := hmac.New(sha256.New, []byte(c.Secret))
		mac.Write(body)
		if !hmac.Equal(mac.Sum(nil), digest) {
			return sqliteReject(401, "Invalid webhook signature.")
		}
		if !validWebhookRetention(c.Retention) {
			return errors.New("invalid retained webhook configuration")
		}
		var previousID string
		err = st.tx.QueryRowContext(ctx, `SELECT id FROM data_webhook_events WHERE x_webhookId=? AND x_deliveryId=? LIMIT 1`, connectionID, deliveryID).Scan(&previousID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if previousID != "" {
			var previous WebhookStoredEvent
			if err := decodeSQLiteRecord(ctx, st.tx, "webhook_events", previousID, 64<<10, &previous); err != nil {
				return err
			}
			if !webhookExpired(previous, time.Now().UTC()) {
				receipt = sqliteWebhookReceipt{EventID: previousID, Accepted: true, Duplicate: true}
				return nil
			}
			if err := st.deleteWebhookEvent(previousID); err != nil {
				return err
			}
		}
		count, err := st.sequence("webhook_event_count")
		if err != nil {
			return err
		}
		bytes, err := st.sequence("webhook_payload_bytes")
		if err != nil {
			return err
		}
		if count >= maxWebhookRetainedEvents || bytes > maxWebhookRetainedBytes || uint64(len(body)) > maxWebhookRetainedBytes-bytes {
			return sqliteReject(503, "Webhook storage capacity reached.")
		}
		created, _ := webhookTime(c.CreatedAt)
		if received.Before(created) {
			return sqliteReject(503, "Receipt time precedes webhook creation.")
		}
		sequence, err := st.nextSequence("webhook_received_sequence")
		if err != nil {
			return err
		}
		e := WebhookStoredEvent{ID: newID(), ProjectID: c.ProjectID, WebhookID: c.ID, Provider: "github", DeliveryID: deliveryID, EventType: eventType, Action: action, IsPing: eventType == "ping", ReceivedAt: received.Format(time.RFC3339Nano), ExpiresAt: webhookExpiry(received, c.Retention), ReceivedSequence: sequence, PayloadBytes: len(body)}
		if err := st.putRoot("webhook_events", e.ID, e); err != nil {
			return err
		}
		if err := st.putSingleChild("webhook_payloads", e.ID, payload); err != nil {
			return err
		}
		if !e.IsPing {
			indexed := e
			indexed.Payload = payload
			if err := indexSQLGithubActivity(ctx, st.tx, indexed); err != nil {
				return err
			}
		}
		if err := st.putRoot("webhook_pending", e.ID, WebhookPending{EventID: e.ID, ReceivedSequence: sequence}); err != nil {
			return err
		}
		c.LastReceivedAt = maxWebhookTimestamp(c.LastReceivedAt, received)
		if e.IsPing {
			c.LastPingAt = maxWebhookTimestamp(c.LastPingAt, received)
		}
		if err := validateWebhookConnection(c); err != nil {
			return err
		}
		if err := st.putRoot("webhook_connections", c.ID, c); err != nil {
			return err
		}
		if err := st.setWebhookQuota(count+1, bytes+uint64(len(body))); err != nil {
			return err
		}
		receipt = sqliteWebhookReceipt{EventID: e.ID, Accepted: true}
		return nil
	})
	if err != nil {
		return sqliteWebhookReceipt{}, sqliteSessionChanges{}, err
	}
	return receipt, changes, nil
}

func (st *sqliteSessionTx) setWebhookQuota(count, bytes uint64) error {
	_, err := st.tx.ExecContext(st.ctx, `INSERT INTO store_meta(key,value) VALUES('webhook_event_count',?),('webhook_payload_bytes',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, sqliteRevision(count), sqliteRevision(bytes))
	return err
}

func (st *sqliteSessionTx) deleteWebhookEvent(id string) error {
	var e WebhookStoredEvent
	if err := decodeSQLiteRecord(st.ctx, st.tx, "webhook_events", id, 64<<10, &e); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	count, err := st.sequence("webhook_event_count")
	if err != nil {
		return err
	}
	bytes, err := st.sequence("webhook_payload_bytes")
	if err != nil {
		return err
	}
	if count == 0 || e.PayloadBytes < 0 || bytes < uint64(e.PayloadBytes) {
		return errors.New("webhook quota accounting mismatch")
	}
	for _, table := range []string{"webhook_pending", "webhook_releases", "webhook_events"} {
		if err := st.deleteRecord(table, id); err != nil {
			return err
		}
	}
	st.changes.Projects[e.ProjectID] = true
	return st.setWebhookQuota(count-1, bytes-uint64(e.PayloadBytes))
}

func (s *sqliteStore) cleanWebhookBatch(ctx context.Context, at time.Time) (int, sqliteSessionChanges, error) {
	n := 0
	changes, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		ids, err := sqliteQueryIDs(ctx, st.tx, `SELECT id FROM data_webhook_events WHERE x_expiresAt<>'' AND x_expiresAt<=? ORDER BY x_expiresAt,id LIMIT ?`, sqliteTimeKey(at.UTC().Format(time.RFC3339Nano)), webhookBatchSize)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := st.deleteWebhookEvent(id); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, sqliteSessionChanges{}, err
	}
	return n, changes, nil
}

func sqliteQueryIDs(ctx context.Context, q sqliteQuery, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *sqliteStore) releaseWebhookBatch(ctx context.Context, at time.Time) (int, sqliteSessionChanges, error) {
	n := 0
	changes, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		ids, err := sqliteQueryIDs(ctx, st.tx, `SELECT pending.id FROM data_webhook_pending pending JOIN data_webhook_events event ON event.id=pending.id JOIN data_projects project ON project.id=event.project_id
 WHERE project.x_removed<>'true' AND (event.x_expiresAt='' OR event.x_expiresAt>?) ORDER BY length(pending.x_receivedSequence),pending.x_receivedSequence,pending.id LIMIT ?`, sqliteTimeKey(at.UTC().Format(time.RFC3339Nano)), webhookBatchSize)
		if err != nil {
			return err
		}
		for _, id := range ids {
			var e WebhookStoredEvent
			if err := decodeSQLiteRecord(ctx, st.tx, "webhook_events", id, 64<<10, &e); err != nil {
				return err
			}
			seq, err := st.nextSequence("webhook_release_sequence")
			if err != nil {
				return err
			}
			if err := st.putRoot("webhook_releases", id, WebhookRelease{EventID: id, ReleaseSequence: seq, EmittedAt: maxWebhookTimestamp(e.ReceivedAt, at)}); err != nil {
				return err
			}
			if err := st.enqueueWebhookAutomations(e); err != nil {
				return err
			}
			if err := st.deleteRecord("webhook_pending", id); err != nil {
				return err
			}
			st.changes.Projects[e.ProjectID] = true
			n++
		}
		return nil
	})
	if err != nil {
		return 0, sqliteSessionChanges{}, err
	}
	return n, changes, nil
}

func (st *sqliteSessionTx) enqueueWebhookAutomations(e WebhookStoredEvent) error {
	if e.Provider != "github" || e.EventType != "pull_request" || e.Action != "opened" {
		return nil
	}
	if err := decodeSQLiteRecord(st.ctx, st.tx, "webhook_payloads", sqliteOwnedKey(e.ID, ""), maxStreamRecordBytes, &e.Payload); err != nil {
		return err
	}
	var payload struct {
		Number       int64 `json:"number"`
		Organization struct {
			Login string `json:"login"`
		} `json:"organization"`
		Repository struct {
			Name  string `json:"name"`
			Owner struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"owner"`
		} `json:"repository"`
	}
	if json.Unmarshal([]byte(e.Payload), &payload) != nil || payload.Number <= 0 {
		return nil
	}
	org := payload.Organization.Login
	if org == "" && payload.Repository.Owner.Type == "Organization" {
		org = payload.Repository.Owner.Login
	}
	if len(org) > 200 || len(payload.Repository.Name) > 200 {
		return nil
	}
	if name, ok := cleanLabel(payload.Repository.Name, 200); !ok || name != payload.Repository.Name {
		return nil
	}
	if org != "" {
		if name, ok := cleanLabel(org, 200); !ok || name != org {
			return nil
		}
	}
	variables := map[string]string{"pr.number": strconv.FormatInt(payload.Number, 10), "organization.name": org, "repository.name": payload.Repository.Name}
	after := ""
	for {
		var id string
		err := st.tx.QueryRowContext(st.ctx, `SELECT id FROM data_automations WHERE project_id=? AND x_enabled='true' AND x_deleted<>'true' AND x_mode='webhook' AND id>? ORDER BY id LIMIT 1`, e.ProjectID, after).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var a Automation
		if err := decodeSQLiteRecord(st.ctx, st.tx, "automations", id, 1<<20, &a); err != nil {
			return err
		}
		if e.ReceivedSequence > a.AfterSequence {
			var exists bool
			key := "webhook/" + e.ID
			if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_automation_triggers WHERE x_automationId=? AND x_key=?)`, a.ID, key).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				trigger := AutomationTrigger{ID: newID(), AutomationID: a.ID, Key: key, Source: "webhook", At: e.ReceivedAt, Variables: variables}
				if err := st.putRoot("automation_triggers", trigger.ID, trigger); err != nil {
					return err
				}
			}
		}
		after = id
	}
}
