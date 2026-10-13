package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxWebhookPayloadBytes   = 25 << 20
	maxWebhookRetainedBytes  = 256 << 20
	maxWebhookRetainedEvents = 10000
	maxWebhookConnections   = 1000
	maxProjectWebhooks       = 100
	webhookBatchSize         = 100
)

// These are private storage records. HTTP handlers only serialize explicit DTOs.
type WebhookConnection struct {
	ID             string `json:"id"`
	ProjectID      string `json:"projectId"`
	Provider       string `json:"provider"`
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	Secret         string `json:"secret"`
	Retention      string `json:"retention"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	LastReceivedAt string `json:"lastReceivedAt,omitempty"`
	LastPingAt     string `json:"lastPingAt,omitempty"`
}

type WebhookStoredEvent struct {
	ID               string `json:"id"`
	ProjectID        string `json:"projectId"`
	WebhookID        string `json:"webhookId"`
	Provider         string `json:"provider"`
	DeliveryID       string `json:"deliveryId"`
	EventType        string `json:"eventType"`
	Action           string `json:"action,omitempty"`
	IsPing           bool   `json:"isPing"`
	ReceivedAt       string `json:"receivedAt"`
	ExpiresAt        string `json:"expiresAt,omitempty"`
	ReceivedSequence uint64 `json:"receivedSequence"`
	Payload          string `json:"payload"` // Original UTF-8 bytes, immutable until deletion.
	PayloadBytes     int    `json:"payloadBytes"`
}

type WebhookPending struct {
	EventID          string `json:"eventId"`
	ReceivedSequence uint64 `json:"receivedSequence"`
}

type WebhookRelease struct {
	EventID         string `json:"eventId"`
	ReleaseSequence uint64 `json:"releaseSequence"`
	EmittedAt       string `json:"emittedAt"`
}

type webhookDeliveryKey struct{ webhookID, deliveryID string }

func initializeWebhookRecords(d *diskState) {
	if d.WebhookConnections == nil {
		d.WebhookConnections = map[string]WebhookConnection{}
	}
	if d.WebhookEvents == nil {
		d.WebhookEvents = map[string]WebhookStoredEvent{}
	}
	if d.WebhookPending == nil {
		d.WebhookPending = map[string]WebhookPending{}
	}
	if d.WebhookReleases == nil {
		d.WebhookReleases = map[string]WebhookRelease{}
	}
}

func validWebhookID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && id == strings.ToLower(id)
}

func validWebhookSecret(secret string) bool {
	return len(secret) >= 16 && len(secret) <= 512 && utf8.ValidString(secret) && strings.IndexFunc(secret, unicode.IsControl) == -1
}

func validWebhookRetention(retention string) bool {
	switch retention {
	case "1d", "3d", "1w", "1mo", "3mo", "6mo", "1y", "forever":
		return true
	}
	return false
}

// Calendar retention clamps to the last valid day instead of AddDate's rollover.
func webhookExpiry(received time.Time, retention string) string {
	received = received.UTC()
	var expires time.Time
	switch retention {
	case "forever":
		return ""
	case "1d":
		expires = received.Add(24 * time.Hour)
	case "3d":
		expires = received.Add(72 * time.Hour)
	case "1w":
		expires = received.Add(168 * time.Hour)
	default:
		months := map[string]int{"1mo": 1, "3mo": 3, "6mo": 6, "1y": 12}[retention]
		first := time.Date(received.Year(), received.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
		lastDay := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		expires = time.Date(first.Year(), first.Month(), min(received.Day(), lastDay), received.Hour(), received.Minute(), received.Second(), received.Nanosecond(), time.UTC)
	}
	return expires.Format(time.RFC3339Nano)
}

func webhookTime(value string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, value)
	return t, err == nil && !t.IsZero() && t.UTC().Format(time.RFC3339Nano) == value
}

func webhookExpired(e WebhookStoredEvent, at time.Time) bool {
	if e.ExpiresAt == "" {
		return false
	}
	expires, ok := webhookTime(e.ExpiresAt)
	return !ok || !expires.After(at)
}

func webhookASCII(value string, maxLength int, token bool) bool {
	if len(value) == 0 || len(value) > maxLength {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < 33 || c > 126 {
			return false
		}
		if token && !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}

func webhookPayloadAction(payload string) (string, error) {
	trimmed := strings.TrimSpace(payload)
	if !utf8.ValidString(payload) || len(trimmed) == 0 || trimmed[0] != '{' {
		return "", errors.New("Payload must be a UTF-8 JSON object.")
	}
	var body struct {
		Action json.RawMessage `json:"action"`
	}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return "", errors.New("Payload must be a valid JSON object.")
	}
	if len(body.Action) == 0 {
		return "", nil
	}
	var action string
	if bytes.Equal(bytes.TrimSpace(body.Action), []byte("null")) || json.Unmarshal(body.Action, &action) != nil || utf8.RuneCountInString(action) > 100 {
		return "", errors.New("Payload action must be a string of at most 100 characters.")
	}
	return action, nil
}

func validateWebhookConnection(c WebhookConnection) error {
	name, ok := cleanLabel(c.Name, 60)
	created, createdOK := webhookTime(c.CreatedAt)
	updated, updatedOK := webhookTime(c.UpdatedAt)
	if !validWebhookID(c.ID) || c.ProjectID == "" || c.Provider != "github" || !ok || name != c.Name || !validWebhookSecret(c.Secret) || !validWebhookRetention(c.Retention) || !createdOK || !updatedOK || updated.Before(created) {
		return errors.New("invalid webhook connection")
	}
	for _, stamp := range []string{c.LastReceivedAt, c.LastPingAt} {
		if stamp != "" {
			if t, ok := webhookTime(stamp); !ok || t.Before(created) {
				return errors.New("invalid webhook connection timestamp")
			}
		}
	}
	if c.LastPingAt != "" {
		ping, _ := webhookTime(c.LastPingAt)
		received, ok := webhookTime(c.LastReceivedAt)
		if !ok || ping.After(received) {
			return errors.New("invalid webhook verification timestamp")
		}
	}
	return nil
}

// Run at load, after replay and before checkpoint/save, never on ordinary chat commands.
func validateWebhookRecords(d *diskState) error {
	for id, c := range d.WebhookConnections {
		if id != c.ID || d.project(c.ProjectID) == nil {
			return errors.New("invalid webhook connection reference")
		}
		if err := validateWebhookConnection(c); err != nil {
			return err
		}
	}
	deliveries := map[webhookDeliveryKey]bool{}
	receivedSequences := map[uint64]bool{}
	releaseSequences := map[uint64]bool{}
	for id, e := range d.WebhookEvents {
		c, exists := d.WebhookConnections[e.WebhookID]
		received, receivedOK := webhookTime(e.ReceivedAt)
		if id != e.ID || !validWebhookID(id) || !exists || e.ProjectID != c.ProjectID || e.Provider != "github" || !webhookASCII(e.DeliveryID, 128, false) || !webhookASCII(e.EventType, 100, true) || e.IsPing != (e.EventType == "ping") || !receivedOK || e.PayloadBytes != len(e.Payload) || e.PayloadBytes == 0 || e.PayloadBytes > maxWebhookPayloadBytes || e.ReceivedSequence == 0 || e.ReceivedSequence > d.WebhookReceivedSequence || receivedSequences[e.ReceivedSequence] {
			return errors.New("invalid webhook event")
		}
		created, _ := webhookTime(c.CreatedAt)
		last, lastOK := webhookTime(c.LastReceivedAt)
		if received.Before(created) || !lastOK || received.After(last) {
			return errors.New("invalid webhook receipt timestamp")
		}
		if e.ExpiresAt != "" {
			expires, ok := webhookTime(e.ExpiresAt)
			validTTL := false
			// The connection may have been edited since receipt; any supported
			// historical retention is valid, but arbitrary expiration is not.
			for _, retention := range []string{"1d", "3d", "1w", "1mo", "3mo", "6mo", "1y"} {
				if webhookExpiry(received, retention) == e.ExpiresAt {
					validTTL = true
					break
				}
			}
			if !ok || !expires.After(received) || !validTTL {
				return errors.New("invalid webhook expiration")
			}
		}
		action, err := webhookPayloadAction(e.Payload)
		if err != nil || action != e.Action {
			return errors.New("invalid stored webhook payload")
		}
		key := webhookDeliveryKey{e.WebhookID, e.DeliveryID}
		if deliveries[key] {
			return errors.New("duplicate retained webhook delivery")
		}
		deliveries[key], receivedSequences[e.ReceivedSequence] = true, true
		pending, isPending := d.WebhookPending[id]
		release, isReleased := d.WebhookReleases[id]
		if isPending == isReleased {
			return errors.New("webhook must be pending or released")
		}
		if isPending && (pending.EventID != id || pending.ReceivedSequence != e.ReceivedSequence) {
			return errors.New("invalid webhook pending entry")
		}
		if isReleased {
			emitted, ok := webhookTime(release.EmittedAt)
			if release.EventID != id || release.ReleaseSequence == 0 || release.ReleaseSequence > d.WebhookReleaseSequence || releaseSequences[release.ReleaseSequence] || !ok || emitted.Before(received) || webhookExpired(e, emitted) {
				return errors.New("invalid webhook release")
			}
			releaseSequences[release.ReleaseSequence] = true
		}
	}
	for id := range d.WebhookPending {
		if _, ok := d.WebhookEvents[id]; !ok {
			return errors.New("orphan webhook pending entry")
		}
	}
	for id := range d.WebhookReleases {
		if _, ok := d.WebhookEvents[id]; !ok {
			return errors.New("orphan webhook release")
		}
	}
	return nil
}

func removeWebhookEvent(d *diskState, id string) {
	delete(d.WebhookEvents, id)
	delete(d.WebhookPending, id)
	delete(d.WebhookReleases, id)
}

// Derived indexes contain no payload copies and only change after a durable commit.
func (a *app) indexWebhooksLocked() {
	a.webhookDeliveries = make(map[webhookDeliveryKey]string, len(a.state.WebhookEvents))
	a.webhookBytes = 0
	for id, e := range a.state.WebhookEvents {
		a.webhookDeliveries[webhookDeliveryKey{e.WebhookID, e.DeliveryID}] = id
		a.webhookBytes += int64(e.PayloadBytes)
	}
}

func (a *app) commitWebhooksLocked(change func(*diskState) error) error {
	if err := a.commitTransactionLocked(func(d *diskState) error {
		initializeWebhookRecords(d)
		return change(d)
	}); err != nil {
		return err
	}
	a.indexWebhooksLocked()
	return nil
}

type webhookConnectionDTO struct {
	ID               string `json:"id"`
	ProjectID        string `json:"projectId"`
	Provider         string `json:"provider"`
	Name             string `json:"name"`
	Enabled          bool   `json:"enabled"`
	Retention        string `json:"retention"`
	SecretConfigured bool   `json:"secretConfigured"`
	EndpointPath     string `json:"endpointPath"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
	LastReceivedAt   string `json:"lastReceivedAt,omitempty"`
	LastPingAt       string `json:"lastPingAt,omitempty"`
}

func webhookConnectionView(c WebhookConnection) webhookConnectionDTO {
	return webhookConnectionDTO{
		ID: c.ID, ProjectID: c.ProjectID, Provider: c.Provider, Name: c.Name,
		Enabled: c.Enabled, Retention: c.Retention, SecretConfigured: c.Secret != "",
		EndpointPath: "/api/webhooks/github/" + c.ID, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		LastReceivedAt: c.LastReceivedAt, LastPingAt: c.LastPingAt,
	}
}

type webhookEventDTO struct {
	ID               string `json:"id"`
	ProjectID        string `json:"projectId"`
	WebhookID        string `json:"webhookId"`
	Provider         string `json:"provider"`
	DeliveryID       string `json:"deliveryId"`
	EventType        string `json:"eventType"`
	Action           string `json:"action,omitempty"`
	IsPing           bool   `json:"isPing"`
	ReceivedAt       string `json:"receivedAt"`
	ExpiresAt        string `json:"expiresAt,omitempty"`
	ReceivedSequence string `json:"receivedSequence"`
	Status           string `json:"status"`
	EmittedAt        string `json:"emittedAt,omitempty"`
	ReleaseSequence  string `json:"releaseSequence,omitempty"`
	PayloadBytes     int    `json:"payloadBytes"`
}

func (d *diskState) webhookEventView(e WebhookStoredEvent) webhookEventDTO {
	v := webhookEventDTO{
		ID: e.ID, ProjectID: e.ProjectID, WebhookID: e.WebhookID, Provider: e.Provider,
		DeliveryID: e.DeliveryID, EventType: e.EventType, Action: e.Action, IsPing: e.IsPing,
		ReceivedAt: e.ReceivedAt, ExpiresAt: e.ExpiresAt,
		ReceivedSequence: strconv.FormatUint(e.ReceivedSequence, 10), Status: "pending", PayloadBytes: e.PayloadBytes,
	}
	if release, ok := d.WebhookReleases[e.ID]; ok {
		v.Status, v.EmittedAt, v.ReleaseSequence = "emitted", release.EmittedAt, strconv.FormatUint(release.ReleaseSequence, 10)
	}
	return v
}

type webhookPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
	HasMore    bool   `json:"hasMore"`
}

type webhookCursor struct {
	Version   int    `json:"v"`
	Kind      string `json:"kind"`
	ProjectID string `json:"projectId"`
	WebhookID string `json:"webhookId,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	ID        string `json:"id,omitempty"`
	Sequence  string `json:"sequence,omitempty"`
}

func encodeWebhookCursor(c webhookCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decimalWebhookSequence(value string) (uint64, bool) {
	if value == "" || len(value) > 20 {
		return 0, false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil
}

func parseWebhookPage(r *http.Request, kind string) (int, webhookCursor, error) {
	limit := 50
	q := r.URL.Query()
	if values, ok := q["limit"]; ok {
		if len(values) != 1 {
			return 0, webhookCursor{}, errors.New("Invalid page limit.")
		}
		n, valid := decimalWebhookSequence(values[0])
		if !valid || n < 1 || n > 100 {
			return 0, webhookCursor{}, errors.New("Page limit must be between 1 and 100.")
		}
		limit = int(n)
	}
	c := webhookCursor{Version: 1, Kind: kind, ProjectID: r.PathValue("id"), WebhookID: r.PathValue("webhookId")}
	if values, ok := q["cursor"]; ok {
		if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 1024 {
			return 0, c, errors.New("Invalid webhook cursor.")
		}
		b, err := base64.RawURLEncoding.Strict().DecodeString(values[0])
		if err != nil {
			return 0, c, errors.New("Invalid webhook cursor.")
		}
		var parsed webhookCursor
		decoder := json.NewDecoder(bytes.NewReader(b))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&parsed) != nil || decoder.Decode(&struct{}{}) != io.EOF || parsed.Version != 1 || parsed.Kind != kind || parsed.ProjectID != c.ProjectID || parsed.WebhookID != c.WebhookID {
			return 0, c, errors.New("Invalid webhook cursor scope or version.")
		}
		if kind == "connections" {
			if _, ok := webhookTime(parsed.CreatedAt); !ok || !validWebhookID(parsed.ID) || parsed.Sequence != "" {
				return 0, c, errors.New("Invalid webhook cursor position.")
			}
		} else {
			if n, ok := decimalWebhookSequence(parsed.Sequence); !ok || n == 0 || parsed.ID != "" || parsed.CreatedAt != "" {
				return 0, c, errors.New("Invalid webhook cursor position.")
			}
		}
		c = parsed
	}
	return limit, c, nil
}

func (a *app) webhookProjectLocked(w http.ResponseWriter, projectID string) bool {
	p := a.state.project(projectID)
	if p == nil || p.Removed {
		fail(w, 404, "Project not found.")
		return false
	}
	return true
}

func (a *app) webhookConnectionLocked(w http.ResponseWriter, r *http.Request) (WebhookConnection, bool) {
	if !a.webhookProjectLocked(w, r.PathValue("id")) {
		return WebhookConnection{}, false
	}
	c, ok := a.state.WebhookConnections[r.PathValue("webhookId")]
	if !ok || c.ProjectID != r.PathValue("id") {
		fail(w, 404, "Webhook not found.")
		return WebhookConnection{}, false
	}
	return c, true
}

func (a *app) listWebhooks(w http.ResponseWriter, r *http.Request) {
	limit, cursor, err := parseWebhookPage(r, "connections")
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.mu.Lock()
	if !a.webhookProjectLocked(w, r.PathValue("id")) {
		a.mu.Unlock()
		return
	}
	connections := []WebhookConnection{}
	for _, c := range a.state.WebhookConnections {
		if c.ProjectID != cursor.ProjectID {
			continue
		}
		if cursor.ID != "" {
			created, _ := webhookTime(c.CreatedAt)
			position, _ := webhookTime(cursor.CreatedAt)
			if created.Before(position) || created.Equal(position) && c.ID <= cursor.ID {
				continue
			}
		}
		connections = append(connections, c)
	}
	a.mu.Unlock()
	sort.Slice(connections, func(i, j int) bool {
		a, _ := webhookTime(connections[i].CreatedAt)
		b, _ := webhookTime(connections[j].CreatedAt)
		return a.Before(b) || a.Equal(b) && connections[i].ID < connections[j].ID
	})
	page := webhookPage[webhookConnectionDTO]{Items: []webhookConnectionDTO{}, HasMore: len(connections) > limit}
	for _, c := range connections[:min(limit, len(connections))] {
		page.Items = append(page.Items, webhookConnectionView(c))
	}
	if page.HasMore {
		last := connections[limit-1]
		cursor.CreatedAt, cursor.ID = last.CreatedAt, last.ID
		page.NextCursor = encodeWebhookCursor(cursor)
	}
	respond(w, 200, page)
}

func (a *app) listWebhookEvents(w http.ResponseWriter, r *http.Request) {
	limit, cursor, err := parseWebhookPage(r, "events")
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	before, _ := decimalWebhookSequence(cursor.Sequence)
	a.mu.Lock()
	if _, ok := a.webhookConnectionLocked(w, r); !ok {
		a.mu.Unlock()
		return
	}
	items := []webhookEventDTO{}
	at := time.Now().UTC()
	for _, e := range a.state.WebhookEvents {
		if e.WebhookID == cursor.WebhookID && e.ProjectID == cursor.ProjectID && !webhookExpired(e, at) && (before == 0 || e.ReceivedSequence < before) {
			items = append(items, a.state.webhookEventView(e))
		}
	}
	a.mu.Unlock()
	sort.Slice(items, func(i, j int) bool {
		a, _ := decimalWebhookSequence(items[i].ReceivedSequence)
		b, _ := decimalWebhookSequence(items[j].ReceivedSequence)
		return a > b
	})
	page := webhookPage[webhookEventDTO]{Items: items[:min(limit, len(items))], HasMore: len(items) > limit}
	if page.HasMore {
		cursor.Sequence = items[limit-1].ReceivedSequence
		page.NextCursor = encodeWebhookCursor(cursor)
	}
	respond(w, 200, page)
}

func (a *app) getWebhookEvent(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	if _, ok := a.webhookConnectionLocked(w, r); !ok {
		a.mu.Unlock()
		return
	}
	e, ok := a.state.WebhookEvents[r.PathValue("eventId")]
	if !ok || e.WebhookID != r.PathValue("webhookId") || e.ProjectID != r.PathValue("id") || webhookExpired(e, time.Now().UTC()) {
		a.mu.Unlock()
		fail(w, 404, "Webhook event not found.")
		return
	}
	view := a.state.webhookEventView(e)
	a.mu.Unlock()
	respond(w, 200, struct {
		Event   webhookEventDTO `json:"event"`
		Payload string          `json:"payload"`
	}{view, e.Payload})
}

func (a *app) listWebhookReleases(w http.ResponseWriter, r *http.Request) {
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
	a.mu.Lock()
	if !a.webhookProjectLocked(w, r.PathValue("id")) {
		a.mu.Unlock()
		return
	}
	high := a.state.WebhookReleaseSequence
	if after > high {
		a.mu.Unlock()
		fail(w, 400, "Release cursor exceeds the high watermark.")
		return
	}
	items := []webhookEventDTO{}
	at := time.Now().UTC()
	for id, release := range a.state.WebhookReleases {
		e := a.state.WebhookEvents[id]
		if e.ProjectID == r.PathValue("id") && release.ReleaseSequence > after && release.ReleaseSequence <= high && !webhookExpired(e, at) {
			items = append(items, a.state.webhookEventView(e))
		}
	}
	a.mu.Unlock()
	sort.Slice(items, func(i, j int) bool {
		a, _ := decimalWebhookSequence(items[i].ReleaseSequence)
		b, _ := decimalWebhookSequence(items[j].ReleaseSequence)
		return a < b
	})
	hasMore := len(items) > limit
	next := strconv.FormatUint(high, 10)
	if hasMore {
		next = items[limit-1].ReleaseSequence
	}
	respond(w, 200, struct {
		Items         []webhookEventDTO `json:"items"`
		NextAfter     string            `json:"nextAfter"`
		HighWatermark string            `json:"highWatermark"`
		HasMore       bool              `json:"hasMore"`
	}{items[:min(limit, len(items))], next, strconv.FormatUint(high, 10), hasMore})
}

func webhookStorageError(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	fail(w, http.StatusServiceUnavailable, "Engine is unavailable or webhook capacity is exhausted.")
}

func (a *app) webhookWritableLocked(w http.ResponseWriter) bool {
	if a.closing || a.storageErr != nil {
		webhookStorageError(w)
		return false
	}
	return true
}

func maxWebhookTimestamp(previous string, at time.Time) string {
	if t, ok := webhookTime(previous); ok && t.After(at) {
		return previous
	}
	return at.UTC().Format(time.RFC3339Nano)
}

func webhookSequenceAvailable(sequence uint64, count int) error {
	if uint64(count) > ^uint64(0)-sequence {
		return fmt.Errorf("webhook sequence exhausted")
	}
	return nil
}
