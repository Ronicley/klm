package main

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type githubActivityItem struct {
	ID               string                  `json:"id"`
	ProjectID        string                  `json:"projectId"`
	ProjectName      string                  `json:"projectName"`
	WebhookID        string                  `json:"webhookId"`
	Provider         string                  `json:"provider"`
	EventType        string                  `json:"eventType"`
	Action           string                  `json:"action,omitempty"`
	CatalogID        string                  `json:"catalogId,omitempty"`
	ReleaseSequence  string                  `json:"releaseSequence"`
	ReceivedAt       string                  `json:"receivedAt"`
	EmittedAt        string                  `json:"emittedAt"`
	ExpiresAt        string                  `json:"expiresAt,omitempty"`
	Category         string                  `json:"category"`
	Tone             string                  `json:"tone"`
	Icon             string                  `json:"icon"`
	ActionLabel      string                  `json:"actionLabel"`
	Summary          string                  `json:"summary"`
	Actor            string                  `json:"actor,omitempty"`
	Repository       string                  `json:"repository,omitempty"`
	Title            string                  `json:"title"`
	Reference        string                  `json:"reference"`
	Branch           string                  `json:"branch,omitempty"`
	BaseBranch       string                  `json:"baseBranch,omitempty"`
	TargetURL        string                  `json:"targetUrl,omitempty"`
	TargetKind       string                  `json:"targetKind,omitempty"`
	Result           string                  `json:"result,omitempty"`
	SummaryTruncated bool                    `json:"summaryTruncated,omitempty"`
	Pipeline         *githubPipelineMetadata `json:"pipeline,omitempty"`
	Group            *githubActivityGroup    `json:"group,omitempty"`
	DetailEventID    string                  `json:"detailEventId,omitempty"`
	GroupRelation    string                  `json:"groupRelation,omitempty"`
}
type githubActivityField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type githubActivityEntry struct {
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
}
type githubActivityDetails struct {
	ID        string                `json:"id"`
	Fields    []githubActivityField `json:"fields"`
	Body      string                `json:"body,omitempty"`
	Entries   []githubActivityEntry `json:"entries,omitempty"`
	Truncated bool                  `json:"truncated"`
}

// Only compact summaries are memoized, never raw payloads or detail bodies.
// The cache is scoped to this app and guarded separately from persistence.
type githubActivityCache struct {
	mu    sync.Mutex
	items map[string]githubActivityItem
	bytes int
}

func activityValue(p map[string]any, path string) any {
	var value any = p
	for _, key := range strings.Split(path, ".") {
		obj, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = obj[key]
	}
	return value
}
func activityText(p map[string]any, paths ...string) string {
	for _, path := range paths {
		switch v := activityValue(p, path).(type) {
		case string:
			if v != "" {
				return v
			}
		case json.Number:
			return v.String()
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
	}
	return ""
}
func activityArray(p map[string]any, path string) []any {
	v, _ := activityValue(p, path).([]any)
	return v
}
func activityObject(v any) map[string]any { p, _ := v.(map[string]any); return p }
func activityHuman(s string) string       { return strings.ReplaceAll(s, "_", " ") }
func activityBound(s string, limit int, truncated *bool) string {
	if len(s) <= limit {
		return s
	}
	*truncated = true
	n := limit
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.Clone(s[:n])
}
func activityURL(value string) string {
	if len(value) > 2048 || strings.IndexFunc(value, unicode.IsControl) >= 0 || strings.Contains(value, "\\") {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	return value
}
func activityFirstURL(p map[string]any, paths ...string) string {
	for _, path := range paths {
		if value := activityURL(activityText(p, path)); value != "" {
			return value
		}
	}
	return ""
}

func normalizeGithubActivity(e WebhookStoredEvent, withDetails bool) (githubActivityItem, githubActivityDetails) {
	var p map[string]any
	decoder := json.NewDecoder(strings.NewReader(e.Payload))
	decoder.UseNumber()
	_ = decoder.Decode(&p) // Ingress validates JSON; partial fields still produce a card.
	kind := githubActivityTypeFor(e.EventType, p)
	o := kind.object
	field := func(names ...string) string {
		paths := make([]string, len(names))
		for i, name := range names {
			paths[i] = o + "." + name
		}
		return activityText(p, paths...)
	}
	item := githubActivityItem{Provider: "github", EventType: e.EventType, Action: e.Action, CatalogID: kind.ID, Category: kind.Category, Tone: "accent", Icon: "event", Actor: activityText(p, "sender.login"), Repository: activityText(p, "repository.full_name", "repository.name"), ActionLabel: kind.noun, Title: field("title", "name", "full_name", "summary", "secret_type_display_name", "rule.description", "security_advisory.summary", "affected_package_name", "pattern", "login", "resource_identifier", "tag_name"), Reference: kind.noun}
	if e.Action != "" {
		item.ActionLabel += " " + activityHuman(e.Action)
	}
	if kind.ID == "" {
		item.Tone = "muted"
		item.Title = e.EventType
		item.Reference = e.EventType
	}
	item.TargetURL = activityFirstURL(p, o+".html_url", o+"._links.html.href")
	number := field("number", "id")
	if number != "" {
		item.Reference += " #" + number
	}
	if kind.Category == "Security" {
		item.Icon = "security"
		item.Tone = "warning"
	}
	if kind.Category == "Issues" {
		item.Icon = "issue"
	}
	if o == "pull_request" {
		item.Icon = "pr"
		item.Branch = field("head.ref")
		item.BaseBranch = field("base.ref")
		if n := activityText(p, "pull_request.number", "number"); n != "" {
			item.Reference = "PR #" + n
		}
		if e.EventType == "pull_request" && e.Action == "opened" {
			item.Tone = "success"
		}
		if e.EventType == "pull_request" && e.Action == "closed" && activityValue(p, "pull_request.merged") == true {
			item.ActionLabel = "Pull request merged"
			item.Icon = "merge"
			item.Tone = "accent"
		}
		if e.EventType == "pull_request_review" {
			item.TargetURL = activityFirstURL(p, "review.html_url", "pull_request.html_url")
			if state := activityText(p, "review.state"); state != "" {
				item.Result = activityHuman(state)
				item.ActionLabel += " · " + item.Result
				if state == "approved" {
					item.Tone = "success"
				}
				if state == "changes_requested" {
					item.Tone = "warning"
				}
			}
		}
	}
	if o == "issue" || o == "blocked_issue" || o == "sub_issue" {
		if n := field("number"); n != "" {
			item.Reference = "Issue #" + n
		}
		if e.EventType == "issue_comment" && activityValue(p, "issue.pull_request") != nil {
			item.Category = "PRs"
			item.Icon = "pr"
			item.ActionLabel = "Pull request comment " + activityHuman(e.Action)
			item.Reference = "PR #" + field("number")
		}
	}
	if strings.HasSuffix(e.EventType, "comment") {
		item.Icon = "comment"
		item.TargetURL = activityFirstURL(p, "comment.html_url", o+".html_url")
		if e.EventType == "commit_comment" {
			item.Title = "Comment on commit " + activityShort(activityText(p, "comment.commit_id"))
		}
	}
	if e.EventType == "pull_request_review_thread" {
		comments := activityArray(p, "thread.comments")
		if len(comments) > 0 {
			if u := activityFirstURL(activityObject(comments[0]), "html_url"); u != "" {
				item.TargetURL = u
			}
		}
	}
	if kind.Category == "Pipelines" {
		item.Icon = "pipeline"
		state := field("conclusion", "state", "status")
		if e.EventType == "status" {
			state = activityText(p, "state")
			item.Title = activityText(p, "context")
			item.Reference = activityShort(activityText(p, "sha"))
			item.TargetURL = activityFirstURL(p, "target_url", "commit.html_url")
		}
		if state != "" {
			item.Result = activityHuman(state)
			item.ActionLabel = kind.noun + " · " + item.Result
		}
		switch state {
		case "success", "built":
			item.Tone = "success"
		case "failure", "error", "timed_out", "errored":
			item.Tone = "danger"
		case "queued", "pending", "waiting", "in_progress", "requested":
			item.Tone = "accent"
		default:
			item.Tone = "muted"
		}
		item.Branch = field("head_branch", "ref")
		if n := field("run_number"); n != "" {
			item.Reference = "Run #" + n
		} else if n := field("id"); n != "" {
			item.Reference = kind.noun + " #" + n
		}
		if e.EventType == "check_suite" {
			item.Title = field("app.name")
			item.Reference = "Check suite #" + field("id")
		}
		if e.EventType == "deployment_status" || e.EventType == "deployment" {
			item.Title = activityText(p, "deployment.environment")
			item.Branch = activityText(p, "deployment.ref")
			item.TargetURL = activityFirstURL(p, "deployment_status.environment_url", "deployment_status.log_url")
			item.Reference = "Deployment #" + activityText(p, "deployment.id")
		}
	}
	switch e.EventType {
	case "merge_group":
		item.Icon = "merge"
		item.Branch = strings.TrimPrefix(field("head_ref"), "refs/heads/")
		item.BaseBranch = strings.TrimPrefix(field("base_ref"), "refs/heads/")
		item.Title = item.Branch
		item.Reference = "Merge group · " + activityShort(field("head_sha"))
	case "branch_protection_configuration":
		item.Branch = field("default_branch")
	case "repository_advisory":
		if id := field("ghsa_id"); id != "" {
			item.Reference = id
		}
	case "push":
		item.Icon = "commit"
		ref := activityText(p, "ref")
		item.Branch = strings.TrimPrefix(ref, "refs/heads/")
		item.Reference = activityShort(activityText(p, "after"))
		item.Title = activityText(p, "head_commit.message")
		if strings.HasPrefix(ref, "refs/tags/") {
			item.Branch = ""
			item.Reference = "Tag " + strings.TrimPrefix(ref, "refs/tags/")
		}
		if activityValue(p, "forced") == true {
			item.ActionLabel = "Force push received"
		}
		if activityValue(p, "created") == true {
			item.ActionLabel = "Reference created by push"
		}
		if activityValue(p, "deleted") == true {
			item.ActionLabel = "Reference deleted by push"
			item.Title = ref
		}
		item.TargetURL = activityFirstURL(p, "compare", "head_commit.url")
	case "create", "delete":
		item.Title = activityText(p, "ref")
		item.Reference = activityText(p, "ref_type") + " · " + item.Title
		if activityText(p, "ref_type") == "branch" {
			item.Branch = item.Title
		}
	case "meta":
		item.Title = "GitHub webhook"
		item.Reference = "Hook #" + activityText(p, "hook_id")
	case "repository_import":
		item.Result = activityText(p, "status")
		item.ActionLabel += " · " + activityHuman(item.Result)
	case "secret_scanning_scan":
		item.Title = activityText(p, "type")
		item.Reference = activityText(p, "source")
		if activityText(p, "completed_at") != "" {
			item.ActionLabel += " completed"
		}
	case "gollum":
		pages := activityArray(p, "pages")
		if len(pages) > 0 {
			page := activityObject(pages[0])
			item.Title = activityText(page, "title", "page_name")
			item.TargetURL = activityFirstURL(page, "html_url")
			item.Reference = "Wiki · " + activityHuman(activityText(page, "action"))
		}
	}
	if o == "exemption_request" {
		item.Result = activityText(p, "exemption_response.status", "exemption_request.status")
		if item.Result != "" {
			item.ActionLabel += " · " + activityHuman(item.Result)
		}
		if item.Actor == "" {
			item.Actor = field("requester_login")
		}
	}
	if item.Title == "" {
		item.Title = kind.noun
		if item.Repository != "" {
			item.Title += " · " + item.Repository
		}
	}
	if item.TargetURL != "" {
		item.TargetKind = "resource"
	} else if u := activityFirstURL(p, "repository.html_url"); u != "" {
		item.TargetURL = u
		item.TargetKind = "repository"
	}
	item.Summary = item.ActionLabel
	if e.EventType == "pull_request" && e.Action == "opened" {
		item.Summary = "A new PR was created"
	}
	if item.Actor != "" && item.Actor != "ghost" {
		item.Summary += " by " + item.Actor
	}
	if item.Repository != "" {
		item.Summary += " in " + item.Repository
	}
	item.Summary += "."
	if e.EventType == "push" && activityValue(p, "commits") != nil {
		item.Summary += " " + strconv.Itoa(len(activityArray(p, "commits"))) + " commits in delivery."
	}
	// Bounds are in bytes, including UTF-8. Compact metadata stays below ~8 KiB.
	for _, value := range []*string{&item.Title, &item.Summary} {
		*value = activityBound(*value, 1024, &item.SummaryTruncated)
	}
	for _, value := range []*string{&item.Reference, &item.Branch, &item.BaseBranch, &item.Actor, &item.Repository, &item.ActionLabel, &item.Result} {
		*value = activityBound(*value, 256, &item.SummaryTruncated)
	}
	// JSON escaping may expand otherwise bounded text. Leave room for storage
	// metadata applied by the API. Never truncate a URL into a different target.
	for {
		data, _ := json.Marshal(item)
		if len(data) <= 6<<10 {
			break
		}
		item.SummaryTruncated = true
		if item.TargetURL != "" {
			item.TargetURL, item.TargetKind = "", ""
			continue
		}
		for _, value := range []*string{&item.Title, &item.Summary, &item.Reference, &item.Branch, &item.BaseBranch, &item.Actor, &item.Repository, &item.ActionLabel, &item.Result} {
			*value = activityBound(*value, len(*value)/2, &item.SummaryTruncated)
		}
	}
	item.Pipeline = extractGithubPipeline(e.EventType, p)
	details := githubActivityDetails{ID: e.ID, Fields: []githubActivityField{}}
	if !withDetails {
		return item, details
	}
	add := func(label, value string) {
		if value == "" {
			return
		}
		if len(details.Fields) >= 20 {
			details.Truncated = true
			return
		}
		details.Fields = append(details.Fields, githubActivityField{label, activityBound(value, 512, &details.Truncated)})
	}
	add("Event", e.EventType)
	add("Action", e.Action)
	add("Sender", item.Actor)
	add("Repository", item.Repository)
	add("Reference", item.Reference)
	add("State", field("state", "status"))
	add("Result", item.Result)
	add("Base", item.BaseBranch)
	add("Head", item.Branch)
	add("Author", field("user.login"))
	if o == "pull_request" {
		switch field("draft") {
		case "true":
			add("Review status", "Draft")
		case "false":
			if field("state") == "open" {
				add("Review status", "Ready for review")
			}
		}
	} else {
		add("Draft", field("draft"))
	}
	add("Changed files", field("changed_files"))
	add("Review state", activityText(p, "review.state"))
	add("Assignee", activityText(p, "assignee.login"))
	add("Request type", field("request_type"))
	add("Reviewer", activityText(p, "exemption_response.reviewer_login"))
	add("Run ID", field("run_id"))
	add("Resource", field("resource_identifier"))
	add("Environment", activityText(p, "deployment.environment"))
	add("Related issue", activityText(p, "related_issue.title", "blocking_issue.title", "parent_issue.title"))
	add("Severity", field("severity", "rule.severity", "security_advisory.severity"))
	add("Package", field("dependency.package.name", "affected_package_name"))
	add("Version", field("package_version.name", "tag_name"))
	add("Scan type", activityText(p, "type"))
	add("Scan source", activityText(p, "source"))
	if e.EventType == "secret_scanning_alert_location" {
		add("Location", activityText(p, "location.type"))
		add("Path", activityText(p, "location.details.path"))
	}
	if start, err := time.Parse(time.RFC3339Nano, field("started_at", "run_started_at")); err == nil {
		if end, err := time.Parse(time.RFC3339Nano, field("completed_at")); err == nil && !end.Before(start) {
			add("Duration", end.Sub(start).Round(time.Second).String())
		}
	}
	// Explicit body allowlist. Security payloads and keys never get a generic dump.
	body := ""
	switch e.EventType {
	case "pull_request", "issues", "discussion", "release":
		body = field("body")
	case "pull_request_review":
		body = activityText(p, "review.body")
	case "issue_comment", "discussion_comment", "pull_request_review_comment", "commit_comment":
		body = activityText(p, "comment.body")
	}
	if o == "exemption_request" {
		body = field("requester_comment")
	}
	details.Body = activityBound(body, 24<<10, &details.Truncated)
	entry := func(text, u string) {
		if text == "" {
			return
		}
		if len(details.Entries) >= 20 {
			details.Truncated = true
			return
		}
		details.Entries = append(details.Entries, githubActivityEntry{activityBound(text, 512, &details.Truncated), activityURL(u)})
	}
	for _, value := range activityArray(p, o+".labels") {
		entry("Label: "+activityText(activityObject(value), "name"), "")
	}
	if e.EventType == "push" {
		for _, value := range activityArray(p, "commits") {
			c := activityObject(value)
			entry(activityShort(activityText(c, "id"))+" · "+activityText(c, "message"), activityText(c, "url"))
		}
	}
	if e.EventType == "gollum" {
		for _, value := range activityArray(p, "pages") {
			page := activityObject(value)
			entry(activityText(page, "title", "page_name")+" · "+activityText(page, "action"), activityText(page, "html_url"))
		}
	}
	// Entries can have long URLs: enforce a hard serialized response ceiling too.
	for {
		data, _ := json.Marshal(details)
		if len(data) <= 64<<10 {
			break
		}
		details.Truncated = true
		if len(details.Entries) > 0 {
			details.Entries = details.Entries[:len(details.Entries)-1]
		} else {
			details.Body = activityBound(details.Body, len(details.Body)/2, &details.Truncated)
		}
	}
	return item, details
}

func activityShort(s string) string {
	if len(s) > 12 {
		truncated := false
		return activityBound(s, 12, &truncated)
	}
	return s
}

func (c *githubActivityCache) prune(live map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, item := range c.items {
		if !live[id] {
			data, _ := json.Marshal(item)
			c.bytes -= len(data)
			delete(c.items, id)
		}
	}
}
func (c *githubActivityCache) summary(e WebhookStoredEvent) githubActivityItem {
	c.mu.Lock()
	item, found := c.items[e.ID]
	c.mu.Unlock()
	if found {
		return item
	}
	item, _ = normalizeGithubActivity(e, false)
	data, _ := json.Marshal(item)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil || len(c.items) >= 10000 || c.bytes+len(data) > 64<<20 {
		c.items = map[string]githubActivityItem{}
		c.bytes = 0
	}
	if _, exists := c.items[e.ID]; !exists {
		c.items[e.ID] = item
		c.bytes += len(data)
	}
	return item
}
