package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *app) createWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider  string          `json:"provider"`
		Name      string          `json:"name"`
		Secret    json.RawMessage `json:"secret"`
		Retention string          `json:"retention"`
	}
	if !decode(w, r, &body) {
		return
	}
	name, valid := cleanLabel(body.Name, 60)
	var secret string
	if body.Provider != "github" || !valid || !decodeWebhookPatchField(body.Secret, &secret) || !validWebhookSecret(secret) || !validWebhookRetention(body.Retention) {
		fail(w, 400, "Provide provider github, a name of 1 to 60 characters, a secret of 16 to 512 UTF-8 bytes without controls, and an explicit valid retention.")
		return
	}
	a.mu.Lock()
	if !a.webhookProjectLocked(w, r.PathValue("id")) || !a.webhookWritableLocked(w) {
		a.mu.Unlock()
		return
	}
	count := 0
	for _, c := range a.state.WebhookConnections {
		if c.ProjectID == r.PathValue("id") {
			count++
		}
	}
	if count >= maxProjectWebhooks || len(a.state.WebhookConnections) >= maxWebhookConnections {
		a.mu.Unlock()
		fail(w, 409, "Webhook connection limit reached.")
		return
	}
	stamp := now()
	c := WebhookConnection{ID: newID(), ProjectID: r.PathValue("id"), Provider: "github", Name: name, Enabled: true, Secret: secret, Retention: body.Retention, CreatedAt: stamp, UpdatedAt: stamp}
	if err := validateWebhookConnection(c); err != nil {
		a.mu.Unlock()
		fail(w, 400, "Invalid webhook configuration.")
		return
	}
	if err := a.commitWebhooksLocked(func(d *diskState) error {
		d.WebhookConnections[c.ID] = c
		return nil
	}); err != nil {
		a.mu.Unlock()
		webhookStorageError(w)
		return
	}
	a.mu.Unlock()
	respond(w, 201, webhookConnectionView(c))
}

// Raw fields distinguish omission from null; ordinary pointer fields cannot.
func decodeWebhookPatchField(raw json.RawMessage, value any) bool {
	return len(raw) > 0 && utf8.Valid(raw) && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && json.Unmarshal(raw, value) == nil
}

func (a *app) patchWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      json.RawMessage `json:"name"`
		Enabled   json.RawMessage `json:"enabled"`
		Retention json.RawMessage `json:"retention"`
		Secret    json.RawMessage `json:"secret"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Name)+len(body.Enabled)+len(body.Retention)+len(body.Secret) == 0 {
		fail(w, 400, "Provide at least one webhook field to update.")
		return
	}
	var name, secret, retention string
	var enabled bool
	if len(body.Name) > 0 {
		if !decodeWebhookPatchField(body.Name, &name) {
			fail(w, 400, "Webhook name must be a string.")
			return
		}
		var valid bool
		name, valid = cleanLabel(name, 60)
		if !valid {
			fail(w, 400, "Webhook name must contain 1 to 60 characters.")
			return
		}
	}
	if len(body.Enabled) > 0 && !decodeWebhookPatchField(body.Enabled, &enabled) {
		fail(w, 400, "Enabled must be a boolean.")
		return
	}
	if len(body.Retention) > 0 && (!decodeWebhookPatchField(body.Retention, &retention) || !validWebhookRetention(retention)) {
		fail(w, 400, "Invalid webhook retention.")
		return
	}
	if len(body.Secret) > 0 && (!decodeWebhookPatchField(body.Secret, &secret) || !validWebhookSecret(secret)) {
		fail(w, 400, "Secret must contain 16 to 512 UTF-8 bytes without control characters.")
		return
	}
	a.mu.Lock()
	c, ok := a.webhookConnectionLocked(w, r)
	if !ok || !a.webhookWritableLocked(w) {
		a.mu.Unlock()
		return
	}
	if len(body.Name) > 0 {
		c.Name = name
	}
	if len(body.Enabled) > 0 {
		c.Enabled = enabled
	}
	if len(body.Retention) > 0 {
		c.Retention = retention
	}
	if len(body.Secret) > 0 && c.Secret != secret {
		c.Secret, c.LastPingAt = secret, ""
	}
	c.UpdatedAt = maxWebhookTimestamp(c.UpdatedAt, time.Now().UTC())
	if err := validateWebhookConnection(c); err != nil {
		a.mu.Unlock()
		fail(w, 400, "Invalid webhook configuration.")
		return
	}
	if err := a.commitWebhooksLocked(func(d *diskState) error {
		d.WebhookConnections[c.ID] = c
		return nil
	}); err != nil {
		a.mu.Unlock()
		webhookStorageError(w)
		return
	}
	a.mu.Unlock()
	respond(w, 200, webhookConnectionView(c))
}

func (a *app) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	c, ok := a.webhookConnectionLocked(w, r)
	if !ok || !a.webhookWritableLocked(w) {
		a.mu.Unlock()
		return
	}
	if err := a.commitWebhooksLocked(func(d *diskState) error {
		for id, e := range d.WebhookEvents {
			if e.WebhookID == c.ID {
				removeWebhookEvent(d, id)
			}
		}
		delete(d.WebhookConnections, c.ID)
		return nil
	}); err != nil {
		a.mu.Unlock()
		webhookStorageError(w)
		return
	}
	a.mu.Unlock()
	respond(w, 200, map[string]bool{"removed": true})
}

func singleWebhookHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	if len(values) != 1 {
		return "", false
	}
	return values[0], true
}

// The caller holds app.mu. Repeat both before body processing and before commit.
func (a *app) webhookIngressConnectionLocked(w http.ResponseWriter, id string) (WebhookConnection, bool) {
	if !a.webhookWritableLocked(w) {
		return WebhookConnection{}, false
	}
	c, exists := a.state.WebhookConnections[id]
	p := a.state.project(c.ProjectID)
	if !exists || p == nil || p.Removed {
		fail(w, 404, "Webhook not found.")
		return WebhookConnection{}, false
	}
	if !c.Enabled {
		fail(w, 409, "Webhook is disabled.")
		return WebhookConnection{}, false
	}
	return c, true
}

func (a *app) receiveGithubWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("webhookId")
	if !validWebhookID(id) {
		fail(w, 404, "Webhook not found.")
		return
	}
	contentType, single := singleWebhookHeader(r, "Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if !single || err != nil || mediaType != "application/json" {
		fail(w, 415, "GitHub webhooks require application/json.")
		return
	}
	if values := r.Header.Values("Content-Encoding"); len(values) > 1 || len(values) == 1 && !strings.EqualFold(strings.TrimSpace(values[0]), "identity") {
		fail(w, 415, "Compressed webhook bodies are not supported.")
		return
	}
	a.mu.Lock()
	c, ok := a.webhookIngressConnectionLocked(w, id)
	if !ok {
		a.mu.Unlock()
		return
	}
	if a.webhookIngress == nil {
		a.webhookIngress = make(chan struct{}, 2)
	}
	slots := a.webhookIngress
	select {
	case slots <- struct{}{}:
	default:
		a.mu.Unlock()
		webhookStorageError(w)
		return
	}
	a.mu.Unlock()
	defer func() { <-slots }()

	signature, valid := singleWebhookHeader(r, "X-Hub-Signature-256")
	if !valid || len(signature) != 71 || !strings.HasPrefix(signature, "sha256=") {
		fail(w, 401, "Invalid webhook signature.")
		return
	}
	digest, err := hex.DecodeString(signature[len("sha256="):])
	if err != nil {
		fail(w, 401, "Invalid webhook signature.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookPayloadBytes)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(w, 413, "Webhook payload exceeds 25 MiB.")
		} else {
			fail(w, 400, "Could not read webhook body.")
		}
		return
	}
	// The receipt time belongs to body reading, not lock scheduling or emission.
	received := time.Now().UTC()
	mac := hmac.New(sha256.New, []byte(c.Secret))
	_, _ = mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), digest) {
		fail(w, 401, "Invalid webhook signature.")
		return
	}
	deliveryID, deliveryOK := singleWebhookHeader(r, "X-GitHub-Delivery")
	eventType, typeOK := singleWebhookHeader(r, "X-GitHub-Event")
	if !deliveryOK || !typeOK || !webhookASCII(deliveryID, 128, false) || !webhookASCII(eventType, 100, true) {
		fail(w, 400, "Invalid GitHub delivery or event header.")
		return
	}
	payload := string(body)
	action, err := webhookPayloadAction(payload)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.mu.Lock()
	current, ok := a.webhookIngressConnectionLocked(w, id)
	if !ok {
		a.mu.Unlock()
		return
	}
	if r.Context().Err() != nil {
		a.mu.Unlock()
		webhookStorageError(w)
		return
	}
	if current.ProjectID != c.ProjectID || current.Secret != c.Secret {
		a.mu.Unlock()
		fail(w, 409, "Webhook secret changed during delivery. Redeliver with the current secret.")
		return
	}
	if !validWebhookRetention(current.Retention) {
		a.mu.Unlock()
		webhookStorageError(w)
		return
	}
	if a.webhookDeliveries == nil {
		a.indexWebhooksLocked()
	}
	key := webhookDeliveryKey{id, deliveryID}
	previousID := a.webhookDeliveries[key]
	previous, previousExists := a.state.WebhookEvents[previousID]
	if previousExists && !webhookExpired(previous, time.Now().UTC()) {
		a.mu.Unlock()
		respond(w, 200, map[string]any{"accepted": true, "duplicate": true, "eventId": previousID})
		return
	}
	count, totalBytes := len(a.state.WebhookEvents), a.webhookBytes
	if previousExists {
		count--
		totalBytes -= int64(previous.PayloadBytes)
	}
	if count >= maxWebhookRetainedEvents || totalBytes+int64(len(body)) > maxWebhookRetainedBytes || webhookSequenceAvailable(a.state.WebhookReceivedSequence, 1) != nil {
		a.mu.Unlock()
		webhookStorageError(w)
		return
	}
	created, _ := webhookTime(current.CreatedAt)
	if received.Before(created) {
		a.mu.Unlock()
		webhookStorageError(w)
		return
	}
	e := WebhookStoredEvent{
		ID: newID(), ProjectID: current.ProjectID, WebhookID: id, Provider: "github",
		DeliveryID: deliveryID, EventType: eventType, Action: action, IsPing: eventType == "ping",
		ReceivedAt: received.Format(time.RFC3339Nano), ExpiresAt: webhookExpiry(received, current.Retention),
		ReceivedSequence: a.state.WebhookReceivedSequence + 1, Payload: payload, PayloadBytes: len(body),
	}
	current.LastReceivedAt = maxWebhookTimestamp(current.LastReceivedAt, received)
	if e.IsPing {
		current.LastPingAt = maxWebhookTimestamp(current.LastPingAt, received)
	}
	if err := validateWebhookConnection(current); err != nil {
		a.mu.Unlock()
		webhookStorageError(w)
		return
	}
	if err := a.commitWebhooksLocked(func(d *diskState) error {
		if previousExists {
			removeWebhookEvent(d, previousID)
		}
		d.WebhookEvents[e.ID] = e
		d.WebhookPending[e.ID] = WebhookPending{EventID: e.ID, ReceivedSequence: e.ReceivedSequence}
		d.WebhookReceivedSequence = e.ReceivedSequence
		d.WebhookConnections[id] = current
		return nil
	}); err != nil {
		a.mu.Unlock()
		webhookStorageError(w)
		return
	}
	a.wakeWebhooksLocked()
	a.mu.Unlock()
	respond(w, 202, map[string]any{"accepted": true, "duplicate": false, "eventId": e.ID})
}
