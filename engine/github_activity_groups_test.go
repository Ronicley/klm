package main

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"
)

func groupingSource(seq uint64, kind, body string) githubActivitySource {
	id := fmt.Sprintf("%032x", seq)
	return githubActivitySource{
		event:   WebhookStoredEvent{ID: id, ProjectID: "p", WebhookID: "w", EventType: kind, Payload: `{"repository":{"id":9007199254740993},` + body + `}`},
		release: WebhookRelease{EventID: id, ReleaseSequence: seq}, projectName: "Project",
	}
}

func TestGithubActivityGroupingRetentionAndBookmarks(t *testing.T) {
	a := &app{state: diskState{Projects: []Project{{ID: "p", Name: "Project"}}}}
	initializeWebhookRecords(&a.state)
	a.state.WebhookConnections["w"] = WebhookConnection{ID: "w", ProjectID: "p"}
	add := func(s githubActivitySource) {
		a.state.WebhookEvents[s.event.ID] = s.event
		a.state.WebhookReleases[s.event.ID] = s.release
		a.state.WebhookReleaseSequence = s.release.ReleaseSequence
	}
	deployment := groupingSource(1, "deployment", `"deployment":{"id":99,"environment":"Production"}`)
	check := groupingSource(2, "check_run", `"check_run":{"id":700,"url":"https://api.github.com/repos/a/b/check-runs/700"}`)
	add(deployment)
	add(check)
	before, err := a.readGithubActivityBookmarks(context.Background(), []string{deployment.event.ID, check.event.ID}, "")
	if err != nil || len(before) != 2 || before[0].CardID == before[1].CardID {
		t.Fatal("initial anchors missing", err)
	}
	add(groupingSource(3, "workflow_job", `"workflow_job":{"id":7,"run_id":42,"run_attempt":1,"check_run_url":"https://api.github.com/repos/a/b/check-runs/700"},"deployment":{"id":99}`))
	after, err := a.readGithubActivityBookmarks(context.Background(), []string{deployment.event.ID, check.event.ID}, "")
	if err != nil || len(after) != 2 || after[0].CardID != after[1].CardID || after[0].CardID == before[0].CardID || after[1].CardID == before[1].CardID {
		t.Fatal("anchors did not follow late proof", err)
	}
	old, err := a.readGithubActivityBookmarks(context.Background(), []string{deployment.event.ID, check.event.ID}, "2")
	if err != nil || len(old) != 2 || old[0].CardID != before[0].CardID || old[1].CardID != before[1].CardID {
		t.Fatal("bookmark resolution crossed the displayed watermark", err)
	}
	full, _ := a.readGithubActivityPage(context.Background(), nil, 0)
	event := a.state.WebhookEvents[deployment.event.ID]
	event.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	a.state.WebhookEvents[event.ID] = event
	delete(a.state.WebhookEvents, check.event.ID)
	remaining, _ := a.readGithubActivityPage(context.Background(), nil, 0)
	if len(remaining.Items) != 1 || remaining.Items[0].ID != full.Items[0].ID || remaining.Items[0].ReleaseSequence != full.Items[0].ReleaseSequence || remaining.Items[0].Group.Composition == full.Items[0].Group.Composition {
		t.Fatal("retention did not change composition with stable card/release")
	}
	anchors, err := a.readGithubActivityBookmarks(context.Background(), []string{deployment.event.ID, check.event.ID}, "")
	if err != nil || len(anchors) != 0 {
		t.Fatal("expired/removed bookmark anchors remain available", err)
	}
}

func TestGithubActivityGroupingIdentityAndState(t *testing.T) {
	a := &app{}
	sources := []githubActivitySource{
		groupingSource(1, "workflow_run", `"workflow_run":{"id":42,"run_attempt":1,"check_suite_id":50,"status":"completed","conclusion":"failure","updated_at":"2026-10-15T10:00:00Z"}`),
		groupingSource(2, "workflow_job", `"workflow_job":{"id":7,"run_id":42,"run_attempt":1,"name":"Verify","status":"completed","conclusion":"success","check_run_url":"https://api.github.com/repos/a/b/check-runs/700"}`),
		groupingSource(3, "workflow_job", `"workflow_job":{"id":8,"run_id":42,"run_attempt":1,"name":"Deploy","status":"completed","conclusion":"skipped"},"deployment":{"id":99}`),
		groupingSource(4, "check_run", `"check_run":{"id":700,"url":"https://api.github.com/repos/a/b/check-runs/700","status":"completed","conclusion":"success"}`),
		groupingSource(5, "deployment_status", `"deployment":{"id":99},"deployment_status":{"id":100,"state":"success","created_at":"2026-10-15T10:00:00Z"}`),
		groupingSource(6, "deployment_status", `"deployment":{"id":99},"deployment_status":{"id":101,"state":"inactive","created_at":"2026-10-15T11:00:00Z"}`),
		// This later receipt must not regress the completed attempt.
		groupingSource(7, "workflow_run", `"workflow_run":{"id":42,"run_attempt":1,"status":"queued","updated_at":"2026-10-15T09:00:00Z"}`),
		groupingSource(8, "check_suite", `"check_suite":{"id":50,"status":"completed","conclusion":"failure"}`),
		groupingSource(9, "workflow_run", `"workflow_run":{"id":42,"run_attempt":2,"status":"queued"}`),
		groupingSource(10, "workflow_job", `"workflow_job":{"id":11,"run_id":42,"name":"Missing attempt"}`),
	}
	p, err := a.projectGithubActivity(context.Background(), sources, 10, nil, "", "")
	if err != nil || len(p.items) != 4 {
		t.Fatalf("cards=%d err=%v", len(p.items), err)
	}
	var card githubActivityItem
	for _, i := range p.items {
		if i.Group != nil && i.Group.Attempt == "1" {
			card = i
		}
	}
	if card.Group == nil || card.Group.EventCount != 7 || card.Result != "failure" || card.Group.ChildCount != 3 {
		t.Fatalf("unexpected attempt: %+v", card)
	}
	states := map[string]string{}
	for _, child := range card.Group.Children {
		states[child.Pipeline.Kind+child.Pipeline.ID] = child.Pipeline.Conclusion + child.Pipeline.Status
	}
	if states["workflow_job7"] != "successcompleted" || states["workflow_job8"] != "skippedcompleted" || states["deployment_status101"] != "inactive" {
		t.Fatalf("lost child states: %v", states)
	}
	if card.Pipeline.RepositoryID != "9007199254740993" {
		t.Fatal("ID precision lost")
	}
	filtered, err := a.projectGithubActivity(context.Background(), sources, 10, map[string]bool{"workflow_job": true}, "", "Verify")
	if err != nil || len(filtered.items) != 0 {
		t.Fatal("search leaked excluded job text", err)
	}
	filtered, _ = a.projectGithubActivity(context.Background(), sources, 10, map[string]bool{"workflow_job": true}, "", "")
	for _, i := range filtered.items {
		if i.ID == card.ID && (!i.Group.Filtered || i.Group.ChildCount != 1) {
			t.Fatal("filtered presentation lost identity or included hidden jobs")
		}
	}
	// The old release snapshot cannot absorb the future check/job/deployment links.
	old, _ := a.projectGithubActivity(context.Background(), sources, 1, nil, "", "")
	if len(old.items) != 1 || old.items[0].ID != card.ID || old.items[0].Group.EventCount != 1 {
		t.Fatal("watermark or stable identity changed")
	}
}

func TestGithubActivityGroupingPaginationAndMembers(t *testing.T) {
	a := &app{state: diskState{Projects: []Project{{ID: "p", Name: "Project"}}}}
	initializeWebhookRecords(&a.state)
	a.state.WebhookConnections["w"] = WebhookConnection{ID: "w", ProjectID: "p"}
	for _, s := range []githubActivitySource{
		groupingSource(1, "workflow_run", `"workflow_run":{"id":42,"run_attempt":1,"status":"queued"}`),
		groupingSource(2, "issues", `"issue":{"id":1,"title":"Separate"}`),
		groupingSource(3, "workflow_run", `"workflow_run":{"id":42,"run_attempt":1,"status":"completed","conclusion":"success"}`),
	} {
		a.state.WebhookEvents[s.event.ID] = s.event
		a.state.WebhookReleases[s.event.ID] = s.release
	}
	a.state.WebhookReleaseSequence = 3
	first, err := a.readGithubActivityPage(context.Background(), url.Values{"limit": {"1"}}, 64<<10)
	if err != nil || len(first.Items) != 1 || first.Items[0].Group.EventCount != 2 || !first.HasMore {
		t.Fatal("grouping must precede pagination", err)
	}
	next, err := a.readGithubActivityPage(context.Background(), url.Values{"limit": {"1"}, "cursor": {first.NextCursor}}, 64<<10)
	if err != nil || len(next.Items) != 1 || next.Items[0].EventType != "issues" || next.HasMore {
		t.Fatal("duplicate/missing card", err)
	}
	members, err := a.readGithubActivityPage(context.Background(), url.Values{"cardId": {first.Items[0].ID}, "view": {"events"}, "high": {first.HighWatermark}, "limit": {"1"}}, 64<<10)
	if err != nil || len(members.Items) != 1 || members.Items[0].Group != nil || !members.HasMore || members.Items[0].GroupRelation == "" {
		t.Fatal("original member history unavailable", err)
	}
	if _, err := a.readGithubActivityDetails(members.Items[0].ID); err != nil {
		t.Fatal("original event ID unavailable", err)
	}
	if _, err := a.readGithubActivityDetails(first.Items[0].ID); err != nil {
		t.Fatal("card Details unavailable", err)
	}
}
