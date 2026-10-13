package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type githubActivityCursor struct {
	V      int    `json:"v"`
	Kind   string `json:"kind"`
	High   string `json:"high"`
	Before string `json:"before"`
	Filter string `json:"filter"`
}
type githubActivitySource struct {
	event       WebhookStoredEvent
	release     WebhookRelease
	projectName string
}

func (a *app) githubActivitySnapshot(at time.Time) ([]githubActivitySource, uint64) {
	a.mu.Lock()
	sources := make([]githubActivitySource, 0, len(a.state.WebhookReleases))
	live := make(map[string]bool, len(a.state.WebhookReleases))
	for id, release := range a.state.WebhookReleases {
		e, ok := a.state.WebhookEvents[id]
		if !ok || e.IsPing || webhookExpired(e, at) {
			continue
		}
		c, ok := a.state.WebhookConnections[e.WebhookID]
		if !ok || c.ProjectID != e.ProjectID {
			continue
		}
		p := a.state.project(e.ProjectID)
		if p == nil || p.Removed {
			continue
		}
		live[id] = true
		sources = append(sources, githubActivitySource{e, release, p.Name})
	}
	high := a.state.WebhookReleaseSequence
	a.mu.Unlock()
	a.githubActivity.prune(live)
	return sources, high
}

func (a *app) getGithubActivityCatalog(w http.ResponseWriter, r *http.Request) {
	respond(w, 200, map[string]any{"version": 1, "items": githubActivityCatalog})
}

func (a *app) listGithubActivity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if value := q.Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 100 {
			fail(w, 400, "Limit must be between 1 and 100.")
			return
		}
		limit = n
	}
	category := q.Get("category")
	if category == "All" {
		category = ""
	}
	switch category {
	case "", "PRs", "Pipelines", "Issues", "Security", "Repository", "Other":
	default:
		fail(w, 400, "Invalid activity category.")
		return
	}
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	if utf8.RuneCountInString(search) > 200 {
		fail(w, 400, "Search must contain at most 200 characters.")
		return
	}
	excluded := map[string]bool{}
	known := map[string]bool{}
	for _, item := range githubActivityCatalog {
		known[item.ID] = true
	}
	if len(q["exclude"]) > len(githubActivityCatalog) {
		fail(w, 400, "Too many excluded types.")
		return
	}
	for _, id := range q["exclude"] {
		if !known[id] {
			fail(w, 400, "Unknown excluded activity type.")
			return
		}
		excluded[id] = true
	}
	ids := make([]string, 0, len(excluded))
	for id := range excluded {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	canonical, _ := json.Marshal([]any{category, search, ids})
	digest := sha256.Sum256(canonical)
	filter := hex.EncodeToString(digest[:])
	at := time.Now().UTC()
	sources, high := a.githubActivitySnapshot(at)
	before := uint64(0)
	if raw := q.Get("cursor"); raw != "" {
		var cursor githubActivityCursor
		data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if len(raw) > 1024 || err != nil || decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.V != 1 || cursor.Kind != "github-activity" || cursor.Filter != filter {
			fail(w, 400, "Invalid activity cursor. Refresh the feed.")
			return
		}
		cursorHigh, errHigh := strconv.ParseUint(cursor.High, 10, 64)
		cursorBefore, errBefore := strconv.ParseUint(cursor.Before, 10, 64)
		if errHigh != nil || errBefore != nil || cursorBefore == 0 || cursorBefore > cursorHigh || cursorHigh > high || strconv.FormatUint(cursorHigh, 10) != cursor.High || strconv.FormatUint(cursorBefore, 10) != cursor.Before {
			fail(w, 400, "Invalid activity cursor. Refresh the feed.")
			return
		}
		high, before = cursorHigh, cursorBefore
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].release.ReleaseSequence > sources[j].release.ReleaseSequence })
	items := make([]githubActivityItem, 0, limit+1)
	for _, source := range sources {
		if r.Context().Err() != nil {
			return
		}
		seq := source.release.ReleaseSequence
		if seq > high || before != 0 && seq >= before {
			continue
		}
		item := a.githubActivity.summary(source.event)
		if excluded[item.CatalogID] || category != "" && item.Category != category {
			continue
		}
		item.ProjectName = source.projectName
		if search != "" && !strings.Contains(strings.ToLower(strings.Join([]string{item.ProjectName, item.Repository, item.Actor, item.Title, item.Summary, item.Reference, item.Branch, item.BaseBranch, item.ActionLabel, item.EventType, "GitHub"}, " ")), search) {
			continue
		}
		item.ID = source.event.ID
		item.ProjectID = source.event.ProjectID
		item.WebhookID = source.event.WebhookID
		item.ReleaseSequence = strconv.FormatUint(seq, 10)
		item.ReceivedAt = source.event.ReceivedAt
		item.ExpiresAt = source.event.ExpiresAt
		item.EmittedAt = source.release.EmittedAt
		items = append(items, item)
		if len(items) > limit {
			break
		}
	}
	hasMore := len(items) > limit
	next := ""
	if hasMore {
		items = items[:limit]
		data, _ := json.Marshal(githubActivityCursor{1, "github-activity", strconv.FormatUint(high, 10), items[len(items)-1].ReleaseSequence, filter})
		next = base64.RawURLEncoding.EncodeToString(data)
	}
	respond(w, 200, struct {
		Items         []githubActivityItem `json:"items"`
		NextCursor    string               `json:"nextCursor,omitempty"`
		HasMore       bool                 `json:"hasMore"`
		HighWatermark string               `json:"highWatermark"`
		AsOf          string               `json:"asOf"`
	}{items, next, hasMore, strconv.FormatUint(high, 10), at.Format(time.RFC3339Nano)})
}

func (a *app) getGithubActivity(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("eventId")
	if validWebhookID(id) {
		sources, _ := a.githubActivitySnapshot(time.Now().UTC())
		for _, source := range sources {
			if source.event.ID == id {
				_, details := normalizeGithubActivity(source.event, true)
				respond(w, 200, details)
				return
			}
		}
	}
	fail(w, 404, "This update is no longer available.")
}
