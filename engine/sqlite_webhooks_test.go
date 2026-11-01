package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sqliteWebhookRuntimeFixture(t *testing.T) (*sqliteStore, string, WebhookConnection) {
	t.Helper()
	dir := t.TempDir()
	state := sqliteLegacyFixture()
	state.WebhookReceivedSequence = 0
	body, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openOrMigrateSQLite(context.Background(), dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.close() })
	stamp := time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339Nano)
	c := WebhookConnection{ID: newID(), ProjectID: "p", Provider: "github", Name: "Runtime fixture", Secret: strings.Repeat("test-", 8), Enabled: true, Retention: "1d", CreatedAt: stamp, UpdatedAt: stamp}
	_, err = store.sessionTransaction(context.Background(), func(st *sqliteSessionTx) error { return st.putRoot("webhook_connections", c.ID, c) })
	if err != nil {
		t.Fatal(err)
	}
	return store, dir, c
}

func sqliteSignedWebhook(t *testing.T, store *sqliteStore, c WebhookConnection, delivery, eventType string, payload []byte, at time.Time) sqliteWebhookReceipt {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(c.Secret))
	mac.Write(payload)
	receipt, _, err := store.acceptGitHubWebhook(context.Background(), c.ID, delivery, eventType, "sha256="+hex.EncodeToString(mac.Sum(nil)), payload, at)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestSQLiteRuntimeWebhookHeadlessRestartAndAutomation(t *testing.T) {
	ctx := context.Background()
	store, dir, c := sqliteWebhookRuntimeFixture(t)
	a := Automation{ID: "webhook-automation", ProjectID: "p", Name: "Fixture", Mode: "webhook", Enabled: true, Harness: "pi", Prompt: "Fixture task", Revision: 1}
	_, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.putRoot("automations", a.ID, a) })
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"action":"opened","number":42,"repository":{"name":"repo","owner":{"type":"Organization","login":"fixture"}}}`)
	receipt := sqliteSignedWebhook(t, store, c, "delivery-1", "pull_request", payload, time.Now().UTC())
	if receipt.Duplicate || !receipt.Accepted {
		t.Fatal("first receipt not accepted")
	}
	if duplicate := sqliteSignedWebhook(t, store, c, "delivery-1", "pull_request", payload, time.Now().UTC()); !duplicate.Duplicate || duplicate.EventID != receipt.EventID {
		t.Fatal("delivery dedupe lost")
	}
	var pending int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_webhook_pending`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("pending acceptance not durable", err)
	}
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	store, err = openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	// Start the actual SQL worker with ZERO UI subscriptions. Notification is a
	// test observer of an already committed transaction, not an execution trigger.
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	committed := make(chan sqliteSessionChanges, 1)
	done := make(chan error, 1)
	go func() {
		done <- runSQLiteWebhookWorker(workerCtx, store, nil, func(c sqliteSessionChanges) {
			select {
			case committed <- c:
			default:
			}
		})
	}()
	select {
	case changes := <-committed:
		if changes.Revision == "" || !changes.Projects["p"] {
			t.Fatal("missing post-commit change scope")
		}
	case err := <-done:
		t.Fatalf("worker exited: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("headless pending release stalled")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var release WebhookRelease
	if err := decodeSQLiteRecord(ctx, store.readers, "webhook_releases", receipt.EventID, 64<<10, &release); err != nil {
		t.Fatal(err)
	}
	if release.ReleaseSequence != 1 {
		t.Fatalf("release sequence: %d", release.ReleaseSequence)
	}
	var triggerID string
	if err := store.readers.QueryRow(`SELECT id FROM data_automation_triggers WHERE x_automationId=?`, a.ID).Scan(&triggerID); err != nil {
		t.Fatal(err)
	}
	var trigger AutomationTrigger
	if err := decodeSQLiteRecord(ctx, store.readers, "automation_triggers", triggerID, 1<<20, &trigger); err != nil {
		t.Fatal(err)
	}
	if trigger.Consumed || trigger.Variables["pr.number"] != "42" || trigger.Variables["organization.name"] != "fixture" {
		t.Fatal("webhook automation admission lost variables")
	}
	var stored string
	if err := decodeSQLiteRecord(ctx, store.readers, "webhook_payloads", sqliteOwnedKey(receipt.EventID, ""), maxStreamRecordBytes, &stored); err != nil || stored != string(payload) {
		t.Fatal("raw payload changed", err)
	}
	if n, _, err := store.releaseWebhookBatch(ctx, time.Now().UTC()); err != nil || n != 0 {
		t.Fatal("release retry duplicated work", err)
	}
	if err := store.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	store, err = openOrMigrateSQLite(ctx, dir, prepareSQLiteImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	if duplicate := sqliteSignedWebhook(t, store, c, "delivery-1", "pull_request", payload, time.Now().UTC()); !duplicate.Duplicate || duplicate.EventID != receipt.EventID {
		t.Fatal("dedupe lost after SQL restart")
	}
	var count int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_automation_triggers`).Scan(&count); err != nil || count != 1 {
		t.Fatal("automation trigger duplicated", err)
	}
}

func TestSQLiteRuntimeWebhookRollbackRemovalAndExpiry(t *testing.T) {
	ctx := context.Background()
	store, _, c := sqliteWebhookRuntimeFixture(t)
	payload := []byte(`{"action":"opened"}`)
	mac := hmac.New(sha256.New, []byte(c.Secret))
	mac.Write(payload)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if _, _, err := store.acceptGitHubWebhook(ctx, c.ID, "invalid", "issues", "sha256="+strings.Repeat("0", 64), payload, time.Now().UTC()); err == nil {
		t.Fatal("invalid signature accepted")
	}
	if _, err := store.writer.Exec(`CREATE TRIGGER fail_pending BEFORE INSERT ON data_webhook_pending BEGIN SELECT RAISE(FAIL,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	receipt, changes, err := store.acceptGitHubWebhook(ctx, c.ID, "rollback", "issues", signature, payload, time.Now().UTC())
	if err == nil || receipt.Accepted || changes.Revision != "" {
		t.Fatal("partial receipt success published")
	}
	var count int
	if err := store.readers.QueryRow(`SELECT count(*) FROM data_webhook_events`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed pending insert retained event", err)
	}
	if _, err := store.writer.Exec(`DROP TRIGGER fail_pending`); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Truncate(time.Second)
	receipt = sqliteSignedWebhook(t, store, c, "retained", "issues", payload, stamp)
	_, err = store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		var p Project
		if err := decodeSQLiteRecord(ctx, st.tx, "projects", "p", 1<<20, &p); err != nil {
			return err
		}
		p.Removed = true
		return st.putRoot("projects", "p", p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, _, err := store.releaseWebhookBatch(ctx, stamp); err != nil || n != 0 {
		t.Fatal("removed project released pending work", err)
	}
	// Expiry still applies to removed projects. Canonical nanosecond SQL keys
	// correctly compare an exact-second expiry with a fractional-second clock.
	if n, _, err := store.cleanWebhookBatch(ctx, stamp.Add(24*time.Hour-1*time.Nanosecond)); err != nil || n != 0 {
		t.Fatal("premature retention cleanup", err)
	}
	if n, _, err := store.cleanWebhookBatch(ctx, stamp.Add(24*time.Hour+100*time.Millisecond)); err != nil || n != 1 {
		t.Fatal("expired event retained", err)
	}
	for _, table := range []string{"webhook_events", "webhook_payloads", "webhook_pending", "webhook_releases"} {
		if err := store.readers.QueryRow(`SELECT count(*) FROM data_` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("expiry left dependent rows", table, err)
		}
	}
	var quota string
	if err := store.readers.QueryRow(`SELECT value FROM store_meta WHERE key='webhook_payload_bytes'`).Scan(&quota); err != nil || quota != "0" {
		t.Fatal("quota not decremented", err)
	}
	_, err = store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		var p Project
		if err := decodeSQLiteRecord(ctx, st.tx, "projects", "p", 1<<20, &p); err != nil {
			return err
		}
		p.Removed = false
		return st.putRoot("projects", "p", p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if again := sqliteSignedWebhook(t, store, c, "retained", "issues", payload, time.Now().UTC()); again.EventID == receipt.EventID || again.Duplicate {
		t.Fatal("expired delivery could not be reaccepted")
	}
}

func TestSQLiteRuntimeScopedChangeBus(t *testing.T) {
	bus := &sqliteChangeBus{}
	global, err := bus.subscribe(sqliteChangeScope{Kind: "global"})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.unsubscribe(global)
	project, err := bus.subscribe(sqliteChangeScope{Kind: "project", ID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.unsubscribe(project)
	chat, err := bus.subscribe(sqliteChangeScope{Kind: "conversation", ID: "a", ProjectID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer bus.unsubscribe(chat)
	change := sqliteSessionChanges{Revision: "9007199254740993", Projects: map[string]bool{"p": true}, Sessions: map[string][]int64{"a": {9007199254740993}}}
	bus.publish(change)
	var notice sqliteChangeNotice
	if err := json.Unmarshal(<-global.notices, &notice); err != nil || len(notice.SessionIDs) != 0 {
		t.Fatal("global notice leaked session inventory", err)
	}
	select {
	case <-project.notices:
		t.Fatal("unrelated project was invalidated")
	default:
	}
	if err := json.Unmarshal(<-chat.notices, &notice); err != nil || notice.Ordinals["a"][0] != "9007199254740993" {
		t.Fatal("ordinal rounded", err)
	}
	bus.publish(change)
	change.Revision = "9007199254740994"
	bus.publish(change)
	if err := json.Unmarshal(<-chat.notices, &notice); err != nil || notice.Kind != "reset" || notice.Revision != change.Revision {
		t.Fatal("slow consumer was not scoped-reset", err)
	}
}
