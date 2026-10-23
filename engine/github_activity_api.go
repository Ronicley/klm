package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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

type githubActivityPage struct {
	Items         []githubActivityItem `json:"items"`
	NextCursor    string               `json:"nextCursor,omitempty"`
	HasMore       bool                 `json:"hasMore"`
	HighWatermark string               `json:"highWatermark"`
	AsOf          string               `json:"asOf"`
}

type githubActivityBookmark struct {
	EventID   string `json:"eventId"`
	CardID    string `json:"cardId"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// Resolve only requested original-event anchors, not aliases for every member.
// This stays bounded independently of history size and honors source retention.
func (a *app) readGithubActivityBookmarks(ctx context.Context, ids []string, watermark string) ([]githubActivityBookmark, error) {
	if len(ids) > 100 {
		return nil, errors.New("At most 100 bookmark anchors may be resolved per request.")
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		if !validWebhookID(id) {
			return nil, errors.New("Invalid bookmark event ID.")
		}
		wanted[id] = true
	}
	result := []githubActivityBookmark{}
	if len(wanted) == 0 {
		return result, nil
	}
	sources, high := a.githubActivitySnapshot(time.Now().UTC())
	if watermark != "" {
		n, err := strconv.ParseUint(watermark, 10, 64)
		if err != nil || n > high || strconv.FormatUint(n, 10) != watermark {
			return nil, errors.New("Invalid activity watermark.")
		}
		high = n
	}
	projection, err := a.projectGithubActivity(ctx, sources, high, nil, "", "")
	if err != nil {
		return nil, err
	}
	for cardID, members := range projection.events {
		for _, member := range members {
			if wanted[member.ID] {
				result = append(result, githubActivityBookmark{member.ID, cardID, member.ExpiresAt})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].EventID < result[j].EventID })
	return result, nil
}

func (a *app) getGithubActivityBookmarks(w http.ResponseWriter, r *http.Request) {
	items, err := a.readGithubActivityBookmarks(r.Context(), r.URL.Query()["eventId"], r.URL.Query().Get("high"))
	if err != nil {
		if r.Context().Err() == nil {
			fail(w, 400, err.Error())
		}
		return
	}
	respond(w, 200, map[string]any{"items": items})
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
	page, err := a.readGithubActivityPage(r.Context(), r.URL.Query(), 0)
	if err != nil {
		if r.Context().Err() == nil {
			fail(w, 400, err.Error())
		}
		return
	}
	respond(w, 200, page)
}

// HTTP and agent reads share visibility, projection and stable release cursors.
// A byte limit shortens agent pages without skipping the next available post.
func (a *app) readGithubActivityPage(ctx context.Context, q url.Values, byteLimit int) (githubActivityPage, error) {
	limit := 50
	if value := q.Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 100 {
			return githubActivityPage{}, errors.New("Limit must be between 1 and 100.")
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
		return githubActivityPage{}, errors.New("Invalid activity category.")
	}
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	if utf8.RuneCountInString(search) > 200 {
		return githubActivityPage{}, errors.New("Search must contain at most 200 characters.")
	}
	excluded := map[string]bool{}
	known := map[string]bool{}
	for _, item := range githubActivityCatalog {
		known[item.ID] = true
	}
	if len(q["exclude"]) > len(githubActivityCatalog) {
		return githubActivityPage{}, errors.New("Too many excluded types.")
	}
	for _, id := range q["exclude"] {
		if !known[id] {
			return githubActivityPage{}, errors.New("Unknown excluded activity type.")
		}
		excluded[id] = true
	}
	ids := make([]string, 0, len(excluded))
	for id := range excluded {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	cardID, view := q.Get("cardId"), q.Get("view")
	if view != "" && view != "events" && view != "children" || view != "" && cardID == "" || cardID != "" && view == "" {
		return githubActivityPage{}, errors.New("Supply cardId and view (events or children) together.")
	}
	canonical, _ := json.Marshal([]any{category, search, ids, cardID, view})
	digest := sha256.Sum256(canonical)
	filter := hex.EncodeToString(digest[:])
	at := time.Now().UTC()
	sources, high := a.githubActivitySnapshot(at)
	if value := q.Get("high"); value != "" {
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil || n > high || strconv.FormatUint(n, 10) != value {
			return githubActivityPage{}, errors.New("Invalid activity watermark.")
		}
		high = n
	}
	before := uint64(0)
	if raw := q.Get("cursor"); raw != "" {
		var cursor githubActivityCursor
		data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if len(raw) > 1024 || err != nil || decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.V != 2 || cursor.Kind != "github-activity" || cursor.Filter != filter {
			return githubActivityPage{}, errors.New("Invalid activity cursor. Refresh the feed.")
		}
		cursorHigh, errHigh := strconv.ParseUint(cursor.High, 10, 64)
		cursorBefore, errBefore := strconv.ParseUint(cursor.Before, 10, 64)
		if errHigh != nil || errBefore != nil || cursorBefore == 0 || cursorBefore > cursorHigh || cursorHigh > high || strconv.FormatUint(cursorHigh, 10) != cursor.High || strconv.FormatUint(cursorBefore, 10) != cursor.Before {
			return githubActivityPage{}, errors.New("Invalid activity cursor. Refresh the feed.")
		}
		high, before = cursorHigh, cursorBefore
	}
	projection, err := a.projectGithubActivity(ctx, sources, high, excluded, category, search)
	if err != nil {
		return githubActivityPage{}, err
	}
	candidates := projection.items
	if view == "events" {
		candidates = projection.events[cardID]
	}
	if view == "children" {
		candidates = projection.children[cardID]
	}
	items := make([]githubActivityItem, 0, limit+1)
	hasMore := false
	pageBytes := 2048 // Reserve the envelope, cursor and item separators.
	for _, item := range candidates {
		if err := ctx.Err(); err != nil {
			return githubActivityPage{}, err
		}
		seq := activitySequence(item)
		if seq > high || before != 0 && seq >= before {
			continue
		}
		itemBytes := 0
		if byteLimit > 0 {
			encoded, _ := json.Marshal(item)
			itemBytes = len(encoded) + 1
			if len(items) == 0 && pageBytes+itemBytes > byteLimit {
				return githubActivityPage{}, errors.New("Activity item exceeds tool response limit.")
			}
		}
		if len(items) == limit || byteLimit > 0 && pageBytes+itemBytes > byteLimit {
			hasMore = true
			break
		}
		items = append(items, item)
		pageBytes += itemBytes
	}
	next := ""
	if hasMore {
		data, _ := json.Marshal(githubActivityCursor{2, "github-activity", strconv.FormatUint(high, 10), items[len(items)-1].ReleaseSequence, filter})
		next = base64.RawURLEncoding.EncodeToString(data)
	}
	return githubActivityPage{items, next, hasMore, strconv.FormatUint(high, 10), at.Format(time.RFC3339Nano)}, nil
}

func (a *app) getGithubActivity(w http.ResponseWriter, r *http.Request) {
	details, err := a.readGithubActivityDetails(r.PathValue("eventId"))
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	respond(w, 200, details)
}

func (a *app) readGithubActivityDetails(id string) (githubActivityDetails, error) {
	if strings.HasPrefix(id, "pipeline-") {
		sources, high := a.githubActivitySnapshot(time.Now().UTC())
		projection, err := a.projectGithubActivity(context.Background(), sources, high, nil, "", "")
		if err != nil {
			return githubActivityDetails{}, err
		}
		for _, card := range projection.items {
			if card.ID == id {
				details, err := a.readGithubActivityDetails(card.DetailEventID)
				details.ID = id
				details.Fields = append(details.Fields, githubActivityField{"Retained events", strconv.Itoa(card.Group.EventCount)})
				return details, err
			}
		}
	}
	if validWebhookID(id) {
		sources, _ := a.githubActivitySnapshot(time.Now().UTC())
		for _, source := range sources {
			if source.event.ID == id {
				_, details := normalizeGithubActivity(source.event, true)
				return details, nil
			}
		}
	}
	return githubActivityDetails{}, errors.New("This update is no longer available.")
}
