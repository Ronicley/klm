package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"testing"
	"time"
)

func TestSQLiteGithubActivityGrouping(t *testing.T) {
	a, s := sqliteAutomationApp(t)
	ctx := a.ctx
	c := WebhookConnection{ID: newID(), ProjectID: s.ProjectID, Provider: "github", Name: "Fixture", Enabled: true, Secret: "synthetic-activity-secret", Retention: "forever", CreatedAt: now(), UpdatedAt: now()}
	if _, err := a.store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.putRoot("webhook_connections", c.ID, c) }); err != nil {
		t.Fatal(err)
	}
	sources := []githubActivitySource{}
	for index, input := range []struct{ kind, payload string }{
		{"workflow_run", `{"repository":{"id":42,"name":"repo"},"workflow_run":{"id":9007199254740993,"run_attempt":1,"name":"Build","status":"completed","conclusion":"success","check_suite_id":44}}`},
		{"workflow_job", `{"repository":{"id":42,"name":"repo"},"workflow_job":{"id":2,"run_id":9007199254740993,"run_attempt":1,"name":"Test","status":"completed","conclusion":"success","check_run_url":"https://api.github.com/check/2"}}`},
		{"check_run", `{"repository":{"id":42,"name":"repo"},"check_run":{"id":3,"url":"https://api.github.com/check/2","deployment":{"id":5},"status":"completed","conclusion":"success"}}`},
		{"deployment_status", `{"repository":{"id":42,"name":"repo"},"deployment":{"id":5,"environment":"production"},"deployment_status":{"id":6,"state":"success"}}`},
	} {
		body := []byte(input.payload)
		mac := hmac.New(sha256.New, []byte(c.Secret))
		mac.Write(body)
		at := time.Now().UTC()
		receipt, _, err := a.store.acceptGitHubWebhook(ctx, c.ID, string(rune('a'+index)), input.kind, "sha256="+hex.EncodeToString(mac.Sum(nil)), body, at)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.store.releaseWebhookBatch(ctx, at); err != nil {
			t.Fatal(err)
		}
		var e WebhookStoredEvent
		var release WebhookRelease
		if err := decodeSQLiteRecord(ctx, a.store.readers, "webhook_events", receipt.EventID, 64<<10, &e); err != nil {
			t.Fatal(err)
		}
		e.Payload = input.payload
		if err := decodeSQLiteRecord(ctx, a.store.readers, "webhook_releases", receipt.EventID, 64<<10, &release); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, githubActivitySource{event: e, release: release, projectName: "Fixture"})
	}
	page, err := a.readGithubActivityPage(ctx, url.Values{"limit": {"10"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := a.projectGithubActivity(ctx, sources, 4, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || len(oracle.items) != 1 {
		t.Fatal("explicit linked events did not form one card", len(page.Items), len(oracle.items))
	}
	got, want := page.Items[0], oracle.items[0]
	if got.ID != want.ID || got.Group.RunID != "9007199254740993" || got.Group.EventCount != 4 || got.Group.ChildCount != 2 || got.Result != want.Result || got.Group.Composition != want.Group.Composition {
		t.Fatalf("SQL grouping changed provider relationships: got %+v want %+v", got.Group, want.Group)
	}
	members, err := a.readGithubActivityPage(ctx, url.Values{"cardId": {got.ID}, "view": {"events"}, "limit": {"2"}, "high": {page.HighWatermark}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(members.Items) != 2 || !members.HasMore {
		t.Fatal("member paging missing")
	}
	if _, err := a.readGithubActivityPage(ctx, url.Values{"cardId": {got.ID}, "view": {"events"}, "limit": {"2"}, "cursor": {members.NextCursor}, "high": {page.HighWatermark}}, 0); err != nil {
		t.Fatal(err)
	}
	bookmarks, err := a.readGithubActivityBookmarks(ctx, []string{sources[2].event.ID}, page.HighWatermark)
	if err != nil || len(bookmarks) != 1 || bookmarks[0].CardID != got.ID {
		t.Fatal("bookmark anchor not resolved", bookmarks, err)
	}
	if _, err := a.readGithubActivityDetails(got.ID); err != nil {
		t.Fatal("group details", err)
	}
}
