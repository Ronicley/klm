package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type metricCoverage struct {
	Known int `json:"known"`
	Total int `json:"total"`
}

type metricsRow struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	ProjectID       string   `json:"projectId"`
	Project         string   `json:"project"`
	Harness         string   `json:"harness"`
	Models          []string `json:"models"`
	Archived        bool     `json:"archived"`
	CurrentState    string   `json:"currentState"`
	LastOutcome     *string  `json:"lastOutcome"`
	InputTokens     *int64   `json:"inputTokens"`
	OutputTokens    *int64   `json:"outputTokens"`
	Tokens          *int64   `json:"tokens"`
	Cost            *string  `json:"cost"`
	DurationMs      *int64   `json:"durationMs"`
	DurationPartial bool     `json:"durationPartial"`
	Executions      *int64   `json:"executions"`
	Failed          *int64   `json:"failed"`
	Cancelled       *int64   `json:"cancelled"`
	Interrupted     *int64   `json:"interrupted"`
	Completed       *int64   `json:"completed"`
	LastActivityAt  *string  `json:"lastActivityAt"`
	ObservedSince   *string  `json:"observedSince"`
}

// Public accounting deliberately excludes native identities, paths and raw evidence.
type metricsAccounting struct {
	Version            int                  `json:"version"`
	Protocol           string               `json:"protocol"`
	HarnessVersion     string               `json:"harnessVersion,omitempty"`
	Coverage           string               `json:"coverage"`
	Reason             string               `json:"reason,omitempty"`
	Usage              *ExecutionTokenUsage `json:"usage"`
	Cost               *ExecutionMoney      `json:"cost"`
	CostCoverage       string               `json:"costCoverage"`
	CostReason         string               `json:"costReason"`
	ModelAttribution   string               `json:"modelAttribution"`
	Source             *string              `json:"source"`
	BaselineObservedAt *string              `json:"baselineObservedAt"`
	EndpointObservedAt *string              `json:"endpointObservedAt"`
}

type metricsExecution struct {
	ID                 string             `json:"id"`
	StartedAt          string             `json:"startedAt"`
	EndedAt            *string            `json:"endedAt"`
	ObservedAt         string             `json:"observedAt"`
	Outcome            *string            `json:"outcome"`
	InterruptionReason string             `json:"interruptionReason,omitempty"`
	DurationMs         int64              `json:"durationMs"`
	Partial            bool               `json:"partial"`
	Harness            string             `json:"harness"`
	Models             []string           `json:"models"`
	RequestedModel     string             `json:"requestedModel,omitempty"`
	Source             string             `json:"source"`
	Accounting         *metricsAccounting `json:"accounting"`
	Role               *string            `json:"role"`
	Timing             *ExecutionTiming   `json:"timing"`
}

type executionUsageCoverage struct {
	Complete    int `json:"complete"`
	Partial     int `json:"partial"`
	Unavailable int `json:"unavailable"`
	Total       int `json:"total"`
}

type observedExecutionUsage struct {
	InputTokens  *int64                 `json:"inputTokens"`
	OutputTokens *int64                 `json:"outputTokens"`
	Tokens       *int64                 `json:"tokens"`
	Coverage     executionUsageCoverage `json:"coverage"`
	Cost         *string                `json:"cost"`
	AverageCost  *string                `json:"averageCost"`
	CostCoverage executionUsageCoverage `json:"costCoverage"`
	Reason       string                 `json:"reason,omitempty"`
}

func publicExecution(x ExecutionMetrics) metricsExecution {
	view := metricsExecution{ID: x.ID, StartedAt: x.StartedAt, EndedAt: x.EndedAt,
		ObservedAt: x.ObservedAt, Outcome: x.Outcome, InterruptionReason: x.InterruptionReason,
		DurationMs: x.DurationMs, Partial: x.Partial, Harness: x.Harness,
		Models: append([]string{}, x.Models...), RequestedModel: x.RequestedModel, Source: x.Source}
	if x.Role != "" {
		role := x.Role
		view.Role = &role
	}
	if x.Timing != nil {
		timing := *x.Timing
		if timing.FirstResponseAt != nil {
			stamp := *timing.FirstResponseAt
			timing.FirstResponseAt = &stamp
		}
		if timing.FirstResponseLatencyMs != nil {
			ms := *timing.FirstResponseLatencyMs
			timing.FirstResponseLatencyMs = &ms
		}
		view.Timing = &timing
	}
	if a := x.Accounting; a != nil {
		view.Accounting = &metricsAccounting{Version: a.Version, Protocol: a.Protocol,
			HarnessVersion: a.HarnessVersion, Coverage: a.Coverage, Reason: a.Reason,
			CostCoverage: "unavailable", CostReason: a.CostReason, ModelAttribution: a.ModelAttribution}
		if a.Usage != nil {
			usage := *a.Usage
			view.Accounting.Usage = &usage
		}
		if a.Baseline != nil {
			stamp := a.Baseline.ObservedAt
			view.Accounting.BaselineObservedAt = &stamp
		}
		if a.Endpoint != nil {
			stamp := a.Endpoint.ObservedAt
			view.Accounting.EndpointObservedAt = &stamp
		}
		if a.Protocol == "pi-rpc" && (a.Baseline != nil || a.Endpoint != nil) {
			source := "pi.get_session_stats"
			view.Accounting.Source = &source
		}
	}
	return view
}

// This measure covers recorded executions only, never Session.Usage snapshots.
func (u *observedExecutionUsage) observe(x ExecutionMetrics) {
	u.Coverage.Total++
	u.CostCoverage.Total++
	u.CostCoverage.Unavailable++
	a := x.Accounting
	if a == nil || a.Usage == nil || a.Invalidated || (a.Coverage != "complete" && a.Coverage != "partial") {
		u.Coverage.Unavailable++
		return
	}
	if a.Coverage == "complete" {
		u.Coverage.Complete++
	} else {
		u.Coverage.Partial++
	}
	if u.Reason != "" {
		return
	}
	zero := int64(0)
	input, output := u.InputTokens, u.OutputTokens
	if input == nil {
		input, output = &zero, &zero
	}
	u.InputTokens = sumTokens(input, &a.Usage.InputTokens)
	u.OutputTokens = sumTokens(output, &a.Usage.OutputTokens)
	u.Tokens = sumTokens(u.InputTokens, u.OutputTokens)
	if u.Tokens == nil {
		u.InputTokens, u.OutputTokens = nil, nil
		u.Reason = "counter_overflow"
	}
}

type metricsSummary struct {
	Interruptions          *metricsInterruptions     `json:"interruptions"`
	ObservedExecutionUsage observedExecutionUsage    `json:"observedExecutionUsage"`
	Sessions               int                       `json:"sessions"`
	InputTokens            *int64                    `json:"inputTokens"`
	OutputTokens           *int64                    `json:"outputTokens"`
	Tokens                 *int64                    `json:"tokens"`
	Cost                   *string                   `json:"cost"`
	AverageCost            *string                   `json:"averageCost"`
	DurationMs             *int64                    `json:"durationMs"`
	DurationPartial        bool                      `json:"durationPartial"`
	Executions             *int64                    `json:"executions"`
	Failed                 *int64                    `json:"failed"`
	Cancelled              *int64                    `json:"cancelled"`
	Interrupted            *int64                    `json:"interrupted"`
	Completed              *int64                    `json:"completed"`
	Working                int                       `json:"working"`
	Waiting                int                       `json:"waiting"`
	Coverage               map[string]metricCoverage `json:"coverage"`
}

func addKnown(total **int64, value *int64) {
	if value == nil {
		return
	}
	if *total == nil {
		n := *value
		*total = &n
	} else {
		**total += *value
	}
}

func activityTimestamp(s *Session) *string {
	var latest time.Time
	var stamp *string
	for _, e := range s.Events {
		switch e.Type {
		case "user", "agent_prompt", "assistant", "reasoning", "tool", "command", "mcp", "error":
		default:
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, e.CreatedAt)
		if err == nil && t.After(latest) {
			value := e.CreatedAt
			stamp, latest = &value, t
		}
	}
	return stamp
}

func sessionMetricsRow(d *diskState, s *Session, stamp string) metricsRow {
	project := d.project(s.ProjectID)
	row := metricsRow{ID: s.ID, Title: s.Title, ProjectID: s.ProjectID, Project: project.Name, Harness: s.Harness,
		Models: []string{}, Archived: d.conversationArchived(s), CurrentState: "idle", LastActivityAt: activityTimestamp(s)}
	if !row.Archived {
		if len(s.Permissions) > 0 || len(s.Questions) > 0 {
			row.CurrentState = "waiting"
		} else if s.Status == "running" {
			row.CurrentState = "working"
		}
	}
	if s.Usage != nil {
		row.InputTokens, row.OutputTokens = s.Usage.InputTokens, s.Usage.OutputTokens
		row.Tokens = sumTokens(row.InputTokens, row.OutputTokens)
	}
	if s.Metrics != nil {
		row.ObservedSince = &s.Metrics.Since
		n, duration := int64(len(s.Metrics.Executions)), int64(0)
		row.Executions, row.DurationMs = &n, &duration
		row.Failed, row.Cancelled, row.Interrupted, row.Completed = new(int64), new(int64), new(int64), new(int64)
		models := map[string]bool{}
		for _, x := range s.Metrics.Executions {
			for _, model := range x.Models {
				if !models[model] {
					row.Models = append(row.Models, model)
					models[model] = true
				}
			}
			if x.Outcome == nil {
				duration += elapsedMilliseconds(x.StartedAt, stamp)
				row.DurationPartial = true
			} else {
				duration += x.DurationMs
				switch *x.Outcome {
				case "failed":
					*row.Failed++
				case "cancelled":
					*row.Cancelled++
				case "interrupted":
					*row.Interrupted++
				case "completed":
					*row.Completed++
				}
			}
			row.DurationPartial = row.DurationPartial || x.Partial
		}
		if n > 0 {
			row.LastOutcome = s.Metrics.Executions[n-1].Outcome
		}
	}
	return row
}

func pagination(r *http.Request) (int, int, bool) {
	page, size := 1, 25
	var err error
	if value := r.URL.Query().Get("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, false
		}
	}
	if value := r.URL.Query().Get("pageSize"); value != "" {
		size, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, false
		}
	}
	return page, size, page > 0 && page <= 1000000 && size > 0 && size <= 100
}

func (a *app) sessionMetrics(w http.ResponseWriter, r *http.Request) {
	// shortcut: legacy metrics are not SQL-backed; port before enabling this dashboard on SQLite.
	if a.store != nil {
		fail(w, http.StatusNotImplemented, "Session metrics are unavailable for this storage version.")
		return
	}
	q := r.URL.Query()
	if view := q.Get("view"); view != "" && view != "analytics" {
		fail(w, 400, "Invalid metrics view.")
		return
	}
	page, size, valid := pagination(r)
	order := q.Get("sort")
	if order == "" {
		order = "activity"
	}
	if !valid || (order != "activity" && order != "tokens" && order != "duration" && order != "executions") {
		fail(w, 400, "Invalid pagination or sort.")
		return
	}
	if value := q.Get("state"); value != "" && value != "working" && value != "waiting" && value != "idle" {
		fail(w, 400, "Invalid state.")
		return
	}
	if value := q.Get("outcome"); value != "" && value != "completed" && value != "failed" && value != "cancelled" && value != "interrupted" {
		fail(w, 400, "Invalid outcome.")
		return
	}
	var period metricsPeriod
	var buckets []metricsBucket
	if q.Get("view") == "analytics" {
		var err error
		period, buckets, err = parseAnalyticsPeriod(r)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
	} else if q.Has("from") || q.Has("to") || q.Has("timezone") || q.Has("bucket") {
		fail(w, 400, "Period filters require view=analytics; inventory is all time.")
		return
	}
	stamp := now()
	rows := []metricsRow{}
	observedUsage := observedExecutionUsage{}
	var interruptions *metricsInterruptions
	a.mu.Lock()
	for i := range a.state.Sessions {
		s := &a.state.Sessions[i]
		p := a.state.project(s.ProjectID)
		// Native totals may already include native descendants. Never add child rows.
		if p == nil || p.Removed || s.Role != "" || s.ParentID != "" || s.GraphRunID != "" {
			continue
		}
		if q.Get("projectId") != "" && q.Get("projectId") != s.ProjectID || q.Get("harness") != "" && q.Get("harness") != s.Harness || !strings.Contains(strings.ToLower(s.Title), strings.ToLower(q.Get("search"))) {
			continue
		}
		row := sessionMetricsRow(&a.state, s, stamp)
		if row.Archived && q.Get("includeArchived") != "true" || q.Get("state") != "" && q.Get("state") != row.CurrentState || q.Get("outcome") != "" && (row.LastOutcome == nil || q.Get("outcome") != *row.LastOutcome) {
			continue
		}
		rows = append(rows, row)
		if s.Metrics != nil && q.Get("view") != "analytics" {
			if interruptions == nil {
				interruptions = &metricsInterruptions{}
			}
			for _, x := range s.Metrics.Executions {
				observedUsage.observe(x)
				interruptions.observe(x)
			}
		}
	}
	if q.Get("view") == "analytics" {
		analytics := buildExecutionAnalytics(&a.state, rows, period, buckets, stamp)
		a.mu.Unlock()
		respond(w, 200, map[string]any{"updatedAt": stamp, "scope": "main_recorded_executions", "analytics": analytics})
		return
	}
	// Own the scalar response values before releasing the state lock.
	summary := metricsSummary{Sessions: len(rows), Coverage: map[string]metricCoverage{}, ObservedExecutionUsage: observedUsage, Interruptions: interruptions}
	for _, row := range rows {
		addKnown(&summary.InputTokens, row.InputTokens)
		addKnown(&summary.OutputTokens, row.OutputTokens)
		addKnown(&summary.Tokens, row.Tokens)
		addKnown(&summary.DurationMs, row.DurationMs)
		addKnown(&summary.Executions, row.Executions)
		summary.DurationPartial = summary.DurationPartial || row.DurationPartial
		if row.Executions != nil {
			addKnown(&summary.Failed, row.Failed)
			addKnown(&summary.Cancelled, row.Cancelled)
			addKnown(&summary.Interrupted, row.Interrupted)
			addKnown(&summary.Completed, row.Completed)
		}
		if row.CurrentState == "working" {
			summary.Working++
		}
		if row.CurrentState == "waiting" {
			summary.Waiting++
		}
		for name, known := range map[string]bool{"tokens": row.Tokens != nil, "inputTokens": row.InputTokens != nil, "outputTokens": row.OutputTokens != nil, "duration": row.DurationMs != nil, "executions": row.Executions != nil, "cost": false} {
			c := summary.Coverage[name]
			c.Total++
			if known {
				c.Known++
			}
			summary.Coverage[name] = c
		}
	}
	a.mu.Unlock()
	// Missing values always sort last. Stable ID ties keep pages deterministic.
	sort.Slice(rows, func(i, j int) bool {
		l, r := rows[i], rows[j]
		var left, right *int64
		switch order {
		case "tokens":
			left, right = l.Tokens, r.Tokens
		case "duration":
			left, right = l.DurationMs, r.DurationMs
		case "executions":
			left, right = l.Executions, r.Executions
		default:
			if l.LastActivityAt == nil || r.LastActivityAt == nil {
				if l.LastActivityAt != r.LastActivityAt {
					return l.LastActivityAt != nil
				}
			} else {
				lt, _ := time.Parse(time.RFC3339Nano, *l.LastActivityAt)
				rt, _ := time.Parse(time.RFC3339Nano, *r.LastActivityAt)
				if !lt.Equal(rt) {
					return lt.After(rt)
				}
			}
		}
		if left == nil || right == nil {
			if left != right {
				return left != nil
			}
		} else if *left != *right {
			return *left > *right
		}
		return l.ID < r.ID
	})
	start := (page - 1) * size
	if start > len(rows) {
		start = len(rows)
	}
	end := start + size
	if end > len(rows) {
		end = len(rows)
	}
	respond(w, 200, map[string]any{"updatedAt": stamp, "scope": "main_native_totals", "period": "all_time", "summary": summary, "rows": rows[start:end], "page": page, "pageSize": size, "total": len(rows)})
}

func (a *app) sessionExecutions(w http.ResponseWriter, r *http.Request) {
	if a.store != nil {
		fail(w, http.StatusNotImplemented, "Session metrics are unavailable for this storage version.")
		return
	}
	page, size, valid := pagination(r)
	if !valid {
		fail(w, 400, "Invalid pagination.")
		return
	}
	a.mu.Lock()
	s := a.state.session(r.PathValue("id"))
	if s == nil || s.Role != "" || s.ParentID != "" || a.state.project(s.ProjectID) == nil || a.state.project(s.ProjectID).Removed {
		a.mu.Unlock()
		fail(w, 404, "Session not found.")
		return
	}
	rows := []metricsExecution{}
	total := 0
	if s.Metrics != nil {
		total = len(s.Metrics.Executions)
		start := (page - 1) * size
		end := start + size
		if end > total {
			end = total
		}
		for i := start; i < end; i++ {
			x := s.Metrics.Executions[total-1-i]
			if x.Outcome == nil {
				x.DurationMs = elapsedMilliseconds(x.StartedAt, now())
				x.Partial = true
			}
			rows = append(rows, publicExecution(x))
		}
	}
	a.mu.Unlock()
	respond(w, 200, map[string]any{"rows": rows, "total": total, "page": page, "pageSize": size})
}
