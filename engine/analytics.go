package main

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

type AnalyticsMetric struct {
	Value       *int64   `json:"value"`
	Coverage    string   `json:"coverage"`
	Provisional bool     `json:"provisional,omitempty"`
	Reasons     []string `json:"reasons,omitempty"`
}
type AnalyticsTotals struct {
	SessionsUsed    AnalyticsMetric `json:"sessionsUsed"`
	SessionsCreated AnalyticsMetric `json:"sessionsCreated"`
	AgentsExecuted  AnalyticsMetric `json:"agentsExecuted"`
	NativeSubagents AnalyticsMetric `json:"nativeSubagents"`
	WorkingMs       AnalyticsMetric `json:"workingMs"`
	BlockedMs       AnalyticsMetric `json:"blockedMs"`
	InputTokens     AnalyticsMetric `json:"inputTokens"`
	OutputTokens    AnalyticsMetric `json:"outputTokens"`
	TotalTokens     AnalyticsMetric `json:"totalTokens"`
	GraphsUsed      AnalyticsMetric `json:"graphsUsed"`
	GraphRuns       AnalyticsMetric `json:"graphRuns"`
	GraphWallMs     AnalyticsMetric `json:"graphWallMs"`
	GraphTimedRuns  int64           `json:"graphTimedRuns"`
	GraphAverageMs  AnalyticsMetric `json:"graphAverageMs"`
}
type analyticsInventory struct{ ID, ProjectID, TopicID, Created string }
type analyticsGraphFact struct {
	ID, ProjectID, OwnerID, GraphID, Name, Start, End, UnknownUntil string
	Unknown, Provisional                                            bool
}
type analyticsSnapshot struct {
	sql               *sqlAnalyticsReport
	Started, Revision string
	AsOf              time.Time
	Projects          []analyticsProject
	Owners            map[string]analyticsInventory
	Executions        []AnalyticsExecution
	Graphs            []analyticsGraphFact
	Provisional       map[string]bool
}
type analyticsProject struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Topics []Topic `json:"-"`
}
type analyticsScope struct {
	ProjectID string
	TopicKey  string
	Harness   string
	GraphKey  string
}
type analyticsBounds struct{ Start, End time.Time }
type analyticsPeriod struct {
	Kind      string `json:"kind"`
	Start     string `json:"start"`
	End       string `json:"endExclusive"`
	WeekStart string `json:"weekStart,omitempty"`
	Current   bool   `json:"current"`
}
type analyticsWeek struct {
	Start   string `json:"startDate"`
	End     string `json:"endDate"`
	Current bool   `json:"current"`
}
type analyticsBucket struct {
	Key             string           `json:"key"`
	Start           string           `json:"start"`
	End             string           `json:"endExclusive"`
	Label           string           `json:"label"`
	Future          bool             `json:"future"`
	Working         AnalyticsMetric  `json:"workingMs"`
	Tokens          AnalyticsMetric  `json:"tokens"`
	PreviousWorking *AnalyticsMetric `json:"previousWorkingMs,omitempty"`
	PreviousTokens  *AnalyticsMetric `json:"previousTokens,omitempty"`
}
type analyticsTopicRow struct {
	Key         string          `json:"key"`
	ProjectID   string          `json:"projectId"`
	TopicID     *string         `json:"topicId"`
	Name        string          `json:"name"`
	ProjectName string          `json:"projectName"`
	Color       string          `json:"color"`
	Archived    bool            `json:"archived"`
	Totals      AnalyticsTotals `json:"totals"`
}
type analyticsGraphRow struct {
	Key         string          `json:"key"`
	ProjectID   string          `json:"projectId"`
	GraphID     string          `json:"graphId"`
	Name        string          `json:"name"`
	ProjectName string          `json:"projectName"`
	Runs        AnalyticsMetric `json:"runs"`
	Wall        AnalyticsMetric `json:"wallMs"`
	TimedRuns   int64           `json:"timedRuns"`
	Average     AnalyticsMetric `json:"averageMs"`
}
type analyticsCalendarDay struct {
	Date      string          `json:"date"`
	WeekStart string          `json:"weekStart"`
	Future    bool            `json:"future"`
	Working   AnalyticsMetric `json:"workingMs"`
}
type analyticsWeekday struct {
	Weekday int             `json:"weekday"`
	Future  bool            `json:"future"`
	Working AnalyticsMetric `json:"workingMs"`
}
type analyticsHarnessRow struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Working AnalyticsMetric `json:"workingMs"`
}
type analyticsComparison struct {
	Start   string          `json:"start"`
	End     string          `json:"endExclusive"`
	Summary AnalyticsTotals `json:"summary"`
}
type AnalyticsResponse struct {
	Version  int    `json:"version"`
	Revision string `json:"revision"`
	AsOf     string `json:"asOf"`
	TimeZone string `json:"timeZone"`
	Started  string `json:"collectionStartedAt"`
	Scope    struct {
		ProjectID        *string `json:"projectId"`
		IncludesArchived bool    `json:"includesArchived"`
		IncludesGeneral  bool    `json:"includesGeneral"`
	} `json:"scope"`
	Projects       []analyticsProject    `json:"projects"`
	Period         analyticsPeriod       `json:"period"`
	AvailableWeeks []analyticsWeek       `json:"availableWeeks"`
	Summary        AnalyticsTotals       `json:"summary"`
	Comparison     *analyticsComparison  `json:"comparison"`
	Series         []analyticsBucket     `json:"series"`
	Weekdays       []analyticsWeekday    `json:"weekdays"`
	Topics         []analyticsTopicRow   `json:"topics"`
	Harnesses      []analyticsHarnessRow `json:"harnesses"`
	Graphs         []analyticsGraphRow   `json:"graphs"`
	Calendar       struct {
		Start      string                 `json:"startDate"`
		End        string                 `json:"endDate"`
		ActiveDays AnalyticsMetric        `json:"activeDays"`
		Days       []analyticsCalendarDay `json:"days"`
	} `json:"calendar"`
	Coverage struct {
		Reasons      []string                `json:"reasons"`
		ChildSupport []analyticsChildSupport `json:"childSupport"`
	} `json:"coverage"`
}
type analyticsChildSupport struct {
	Harness string `json:"harness"`
	Role    string `json:"role"`
	Status  string `json:"status"`
}

func analyticsKey(project, id string) string {
	b, _ := json.Marshal([]string{project, id})
	return string(b)
}
func analyticsMetric(n int64) AnalyticsMetric {
	return AnalyticsMetric{Value: &n, Coverage: "complete"}
}
func analyticsMissing(reason string) AnalyticsMetric {
	return AnalyticsMetric{Coverage: "unavailable", Reasons: []string{reason}}
}
func analyticsPartial(m AnalyticsMetric, reason string) AnalyticsMetric {
	if m.Value != nil {
		m.Coverage = "partial"
	}
	for _, r := range m.Reasons {
		if r == reason {
			return m
		}
	}
	m.Reasons = append(append([]string(nil), m.Reasons...), reason)
	return m
}
func analyticsAdd(m *AnalyticsMetric, n int64) {
	if m.Value == nil {
		return
	}
	if n < 0 || n > maxTokenCount-*m.Value {
		*m = analyticsMissing("overflow")
		return
	}
	v := *m.Value + n
	m.Value = &v
}
func analyticsCombined(a, b AnalyticsMetric) AnalyticsMetric {
	if a.Value == nil || b.Value == nil {
		return analyticsMissing("component_unavailable")
	}
	m := analyticsMetric(*a.Value)
	analyticsAdd(&m, *b.Value)
	if a.Coverage != "complete" || b.Coverage != "complete" {
		m = analyticsPartial(m, "component_partial")
	}
	m.Provisional = a.Provisional || b.Provisional
	return m
}
func analyticsDuration(start, end string, bounds analyticsBounds) int64 {
	s, e := analyticsTime(start), analyticsTime(end)
	if s.Before(bounds.Start) {
		s = bounds.Start
	}
	if e.After(bounds.End) {
		e = bounds.End
	}
	if s.IsZero() || !e.After(s) {
		return 0
	}
	return e.Sub(s).Milliseconds()
}
func analyticsContains(t time.Time, b analyticsBounds) bool {
	return !t.IsZero() && !t.Before(b.Start) && t.Before(b.End)
}
func analyticsOverlaps(start, end string, b analyticsBounds) bool {
	s, e := analyticsTime(start), analyticsTime(end)
	return !s.IsZero() && (s.Before(b.End) && e.After(b.Start) || s.Equal(e) && analyticsContains(s, b))
}
func analyticsMonday(t time.Time) time.Time {
	y, m, d := t.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	return day.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
}
func analyticsDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// Copy compact immutable accounting and inventory only: no chat histories, graph
// source text, native paths or credentials are traversed by this read endpoint.
func (a *app) analyticsSnapshotLocked(asOf time.Time) analyticsSnapshot {
	s := analyticsSnapshot{Started: a.state.Analytics.Started, Revision: strconv.FormatUint(a.state.GraphRevision, 10), AsOf: asOf, Projects: []analyticsProject{}, Owners: map[string]analyticsInventory{}, Provisional: map[string]bool{}}
	allowed := map[string]bool{}
	for _, p := range a.state.Projects {
		if !p.Removed {
			s.Projects = append(s.Projects, analyticsProject{ID: p.ID, Name: p.Name, Topics: append([]Topic(nil), p.Topics...)})
			allowed[p.ID] = true
		}
	}
	sort.Slice(s.Projects, func(i, j int) bool {
		if s.Projects[i].Name == s.Projects[j].Name {
			return s.Projects[i].ID < s.Projects[j].ID
		}
		return s.Projects[i].Name < s.Projects[j].Name
	})
	for _, row := range a.state.Sessions {
		if allowed[row.ProjectID] && row.ParentID == "" && row.GraphRunID == "" && row.Role == "" {
			s.Owners[row.ID] = analyticsInventory{ID: row.ID, ProjectID: row.ProjectID, TopicID: row.TopicID, Created: row.CreatedAt}
		}
	}
	for _, row := range a.state.Analytics.Executions {
		if !allowed[row.ProjectID] {
			continue
		}
		e := row
		e.Spans = append([]AnalyticsSpan(nil), row.Spans...)
		e.Usage = append([]AnalyticsUsageDelta(nil), row.Usage...)
		e.Reasons = append([]string(nil), row.Reasons...)
		if t := a.runs[e.SessionID]; t != nil && t.analyticsID == e.ID && t.ctx.Err() == nil && e.Ended == "" && e.Accepted != "" {
			at, drift := t.analyticsInstant()
			if drift {
				analyticsReason(&e, "clock_discontinuity")
				e.UnknownUntil = analyticsStamp(asOf)
			} else {
				if at.After(asOf) {
					at = asOf
				}
				analyticsAdvance(&e, at)
				s.Provisional[e.ID] = true
			}
		}
		e.Waits = nil
		e.UnknownWaits = nil
		s.Executions = append(s.Executions, e)
	}
	for _, r := range a.state.GraphRuns {
		if !allowed[r.ProjectID] {
			continue
		}
		g := analyticsGraphFact{ID: r.ID, ProjectID: r.ProjectID, OwnerID: r.ConversationID, GraphID: r.GraphID, Name: r.Snapshot.Graph.Definition.Name, Start: r.CreatedAt, End: r.EndedAt}
		if timing, ok := a.state.Analytics.Graphs[r.ID]; ok {
			g.End, g.Unknown = timing.End, timing.Interrupted
			if g.End == "" {
				g.End = timing.Last
				if a.graphRuns[r.ID] != nil && !g.Unknown {
					at, drift := a.analyticsGraphInstant(r.ID)
					if drift {
						g.Unknown = true
					} else {
						g.End = analyticsStamp(minTime(at, asOf))
						g.Provisional = true
					}
				} else {
					g.Unknown = true
				}
			}
		} else if r.Status == "interrupted" || r.EndedAt == "" {
			g.End, g.Unknown = r.CreatedAt, true
		}
		if analyticsTime(g.End).Before(analyticsTime(g.Start)) {
			g.End, g.Unknown = g.Start, true
		}
		if g.Unknown {
			g.UnknownUntil = r.EndedAt
			if g.UnknownUntil == "" {
				g.UnknownUntil = analyticsStamp(asOf)
			}
		}
		s.Graphs = append(s.Graphs, g)
	}
	return s
}
func (s analyticsSnapshot) matches(owner, project string, scope analyticsScope) bool {
	if s.sql != nil {
		return s.sql.matches(owner, project, scope)
	}
	if scope.ProjectID != "" && scope.ProjectID != project {
		return false
	}
	if scope.TopicKey == "" {
		return true
	}
	o, ok := s.Owners[owner]
	return ok && analyticsKey(project, o.TopicID) == scope.TopicKey
}
func (s analyticsSnapshot) aggregate(b analyticsBounds, scope analyticsScope) AnalyticsTotals {
	base := analyticsMetric(0)
	started := analyticsTime(s.Started)
	if !b.End.After(b.Start) {
		base = analyticsMissing("future")
	} else if !b.End.After(started) {
		base = analyticsMissing("before_collection")
	} else if b.Start.Before(started) {
		base = analyticsPartial(base, "before_collection")
	}
	t := AnalyticsTotals{SessionsUsed: base, SessionsCreated: analyticsMetric(0), AgentsExecuted: base, NativeSubagents: base, WorkingMs: base, BlockedMs: base, InputTokens: base, OutputTokens: base, TotalTokens: base, GraphsUsed: analyticsMetric(0), GraphRuns: analyticsMetric(0), GraphWallMs: analyticsMetric(0)}
	used, agents, children, runs, graphs := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	childUnsupported, onlyDisabled, anyExecution := false, true, false
	inputEvidence, outputEvidence, tokenExecution := false, false, false
	graphTimeEvidence := false
	for owner := range s.ownerRows(b, scope) {
		if s.matches(owner.ID, owner.ProjectID, scope) && scope.Harness == "" && scope.GraphKey == "" && analyticsContains(analyticsTime(owner.Created), b) {
			analyticsAdd(&t.SessionsCreated, 1)
		}
	}
	for e := range s.executionRows(b, scope) {
		if !s.matches(e.OwnerID, e.ProjectID, scope) || scope.Harness != "" && e.Harness != scope.Harness || scope.GraphKey != "" {
			continue
		}
		if e.UnknownUntil != "" && analyticsOverlaps(e.Last, e.UnknownUntil, b) {
			reason := "interrupted_tail_unknown"
			if e.Accepted == "" {
				reason = "dispatch_acceptance_unknown"
			}
			t.SessionsUsed = analyticsPartial(t.SessionsUsed, reason)
			t.AgentsExecuted = analyticsPartial(t.AgentsExecuted, reason)
			t.WorkingMs = analyticsPartial(t.WorkingMs, reason)
			t.BlockedMs = analyticsPartial(t.BlockedMs, reason)
			t.InputTokens = analyticsPartial(t.InputTokens, "usage_incomplete")
			t.OutputTokens = analyticsPartial(t.OutputTokens, "usage_incomplete")
			tokenExecution = true
		}
		if e.Accepted == "" && analyticsContains(analyticsTime(e.Dispatched), b) {
			t.SessionsUsed = analyticsPartial(t.SessionsUsed, "dispatch_acceptance_unknown")
			t.AgentsExecuted = analyticsPartial(t.AgentsExecuted, "dispatch_acceptance_unknown")
			t.WorkingMs = analyticsPartial(t.WorkingMs, "dispatch_acceptance_unknown")
			t.BlockedMs = analyticsPartial(t.BlockedMs, "dispatch_acceptance_unknown")
			t.InputTokens = analyticsPartial(t.InputTokens, "dispatch_acceptance_unknown")
			t.OutputTokens = analyticsPartial(t.OutputTokens, "dispatch_acceptance_unknown")
			tokenExecution = true
		}
		intersects := e.Accepted != "" && analyticsOverlaps(e.Accepted, e.Last, b)
		if intersects {
			tokenExecution = true
			inputEvidence = inputEvidence || e.InputBaseline && e.InputFinal
			outputEvidence = outputEvidence || e.OutputBaseline && e.OutputFinal
			if s.sql == nil {
				if _, ok := s.Owners[e.OwnerID]; ok {
					used[e.OwnerID] = true
				}
			}
			anyExecution = true
			if e.Role == sessionRoleSubagent {
				if s.sql == nil {
					children[e.SessionID] = true
				}
				t.WorkingMs = analyticsPartial(t.WorkingMs, "child_timing_unavailable")
				t.BlockedMs = analyticsPartial(t.BlockedMs, "child_waits_unavailable")
			} else {
				if s.sql == nil {
					agents[e.SessionID] = true
				}
			}
			if e.Harness != "codex" {
				childUnsupported, onlyDisabled = true, false
			}
			for _, reason := range e.Reasons {
				if reason == "native_child_coverage_partial" {
					t.InputTokens = analyticsPartial(t.InputTokens, reason)
					t.OutputTokens = analyticsPartial(t.OutputTokens, reason)
				}
				if reason == "usage_start_unobserved" || reason == "counter_decrease" {
					t.InputTokens = analyticsPartial(t.InputTokens, reason)
					t.OutputTokens = analyticsPartial(t.OutputTokens, reason)
					continue
				}
				t.WorkingMs, t.BlockedMs = analyticsPartial(t.WorkingMs, reason), analyticsPartial(t.BlockedMs, reason)
			}
			if !e.InputBaseline || !e.InputFinal {
				t.InputTokens = analyticsPartial(t.InputTokens, "usage_incomplete")
			}
			if !e.OutputBaseline || !e.OutputFinal {
				t.OutputTokens = analyticsPartial(t.OutputTokens, "usage_incomplete")
			}
			if s.Provisional[e.ID] {
				t.WorkingMs.Provisional, t.BlockedMs.Provisional, t.InputTokens.Provisional, t.OutputTokens.Provisional = true, true, true, true
			}
			if e.Ended == "" && !s.Provisional[e.ID] {
				t.WorkingMs = analyticsPartial(t.WorkingMs, "unobserved_tail")
			}
		}
		for span := range s.spanRows(e, b) {
			duration := analyticsDuration(span.Start, span.End, b)
			if span.Kind == "working" {
				analyticsAdd(&t.WorkingMs, duration)
			} else if span.Kind == "blocked" {
				analyticsAdd(&t.BlockedMs, duration)
			}
		}
		for delta := range s.usageRows(e, b) {
			if analyticsContains(analyticsTime(delta.At), b) {
				// Delayed receipts retain the same all-role coverage as the execution,
				// even when its working spans fall outside this receipt-date bucket.
				if e.Harness != "codex" {
					childUnsupported = true
					onlyDisabled = false
				}
				if delta.Input != nil {
					inputEvidence = true
					analyticsAdd(&t.InputTokens, *delta.Input)
				}
				if delta.Output != nil {
					outputEvidence = true
					analyticsAdd(&t.OutputTokens, *delta.Output)
				}
				if !e.InputFinal || !e.InputBaseline {
					t.InputTokens = analyticsPartial(t.InputTokens, "usage_incomplete")
				}
				if !e.OutputFinal || !e.OutputBaseline {
					t.OutputTokens = analyticsPartial(t.OutputTokens, "usage_incomplete")
				}
				for _, r := range e.Reasons {
					if r == "usage_start_unobserved" || r == "counter_decrease" {
						t.InputTokens = analyticsPartial(t.InputTokens, r)
						t.OutputTokens = analyticsPartial(t.OutputTokens, r)
					}
				}
			}
		}
	}
	for g := range s.graphRows(b, scope) {
		key := analyticsKey(g.ProjectID, g.GraphID)
		if !s.matches(g.OwnerID, g.ProjectID, scope) || scope.Harness != "" || scope.GraphKey != "" && scope.GraphKey != key {
			continue
		}
		if g.Unknown && analyticsOverlaps(g.Start, g.UnknownUntil, b) {
			t.GraphRuns = analyticsPartial(t.GraphRuns, "graph_end_unknown")
			t.GraphsUsed = analyticsPartial(t.GraphsUsed, "graph_end_unknown")
			t.GraphWallMs = analyticsPartial(t.GraphWallMs, "graph_end_unknown")
		}
		if !analyticsOverlaps(g.Start, g.End, b) {
			continue
		}
		if s.sql == nil {
			runs[g.ID], graphs[key] = true, true
			if _, ok := s.Owners[g.OwnerID]; ok {
				used[g.OwnerID] = true
			}
		}
		duration := analyticsDuration(g.Start, g.End, b)
		graphTimeEvidence = graphTimeEvidence || !g.Unknown || duration > 0
		if g.Unknown {
			t.GraphRuns = analyticsPartial(t.GraphRuns, "graph_end_unknown")
			t.GraphWallMs = analyticsPartial(t.GraphWallMs, "graph_end_unknown")
		} else {
			t.GraphTimedRuns++
		}
		analyticsAdd(&t.GraphWallMs, duration)
		if g.Provisional {
			t.GraphRuns.Provisional, t.GraphWallMs.Provisional = true, true
		}
	}
	setCount := func(m AnalyticsMetric, n int) AnalyticsMetric {
		if m.Value == nil && n > 0 {
			m = analyticsPartial(analyticsMetric(0), "before_collection")
		}
		if m.Value != nil {
			v := int64(n)
			m.Value = &v
		}
		return m
	}
	usedCount, agentCount, childCount, runCount, graphCount := len(used), len(agents), len(children), len(runs), len(graphs)
	if s.sql != nil {
		usedCount, agentCount, childCount, runCount, graphCount = s.sql.counts(b, scope)
	}
	t.SessionsUsed = setCount(t.SessionsUsed, usedCount)
	t.AgentsExecuted = setCount(t.AgentsExecuted, agentCount)
	t.NativeSubagents = setCount(t.NativeSubagents, childCount)
	if childUnsupported {
		if childCount == 0 {
			t.NativeSubagents = analyticsMissing("native_children_unsupported")
		} else {
			t.NativeSubagents = analyticsPartial(t.NativeSubagents, "native_children_partial")
		}
		t.WorkingMs = analyticsPartial(t.WorkingMs, "native_children_partial")
		t.BlockedMs = analyticsPartial(t.BlockedMs, "native_children_partial")
		t.InputTokens = analyticsPartial(t.InputTokens, "native_children_partial")
		t.OutputTokens = analyticsPartial(t.OutputTokens, "native_children_partial")
	} else if onlyDisabled && anyExecution && t.NativeSubagents.Value != nil {
		t.NativeSubagents.Coverage = "not_applicable"
		t.NativeSubagents.Reasons = []string{"managed_delegation_disabled"}
	}
	t.GraphRuns = setCount(t.GraphRuns, runCount)
	t.GraphsUsed = setCount(t.GraphsUsed, graphCount)
	if t.GraphWallMs.Coverage == "partial" && !graphTimeEvidence {
		t.GraphWallMs = analyticsMissing("graph_end_unknown")
	}
	if tokenExecution && !inputEvidence {
		t.InputTokens = analyticsMissing("usage_incomplete")
	}
	if tokenExecution && !outputEvidence {
		t.OutputTokens = analyticsMissing("usage_incomplete")
	}
	t.TotalTokens = analyticsCombined(t.InputTokens, t.OutputTokens)
	t.GraphAverageMs = analyticsMissing("no_timed_runs")
	if t.GraphWallMs.Coverage == "complete" && t.GraphTimedRuns > 0 && t.GraphWallMs.Value != nil {
		t.GraphAverageMs = analyticsMetric(*t.GraphWallMs.Value / t.GraphTimedRuns)
		t.GraphAverageMs.Provisional = t.GraphWallMs.Provisional
	}
	return t
}

func (s analyticsSnapshot) response(location *time.Location, project, period string, start time.Time, compare bool) AnalyticsResponse {
	localNow := s.AsOf.In(location)
	monday := analyticsMonday(localNow)
	scope := analyticsScope{ProjectID: project}
	end := start.AddDate(0, 0, 7)
	current := start.Equal(monday)
	if current {
		end = s.AsOf
	}
	if period == "all" {
		start = analyticsTime(s.Started).In(location)
		if s.sql != nil {
			if earliest := s.sql.earliest(scope); !earliest.IsZero() && earliest.Before(start) {
				start = earliest.In(location)
			}
		}
		for _, o := range s.Owners {
			t := analyticsTime(o.Created)
			if s.matches(o.ID, o.ProjectID, scope) && !t.IsZero() && t.Before(start) {
				start = t.In(location)
			}
		}
		for _, g := range s.Graphs {
			t := analyticsTime(g.Start)
			if s.matches(g.OwnerID, g.ProjectID, scope) && !t.IsZero() && t.Before(start) {
				start = t.In(location)
			}
		}
		end, current = s.AsOf, true
	}
	b := analyticsBounds{start, end}
	r := AnalyticsResponse{Version: 1, Revision: s.Revision, AsOf: analyticsStamp(s.AsOf), TimeZone: location.String(), Started: s.Started, Projects: s.Projects, Period: analyticsPeriod{Kind: period, Start: analyticsStamp(start), End: analyticsStamp(end), Current: current}, Summary: s.aggregate(b, scope), AvailableWeeks: []analyticsWeek{}, Series: []analyticsBucket{}, Weekdays: []analyticsWeekday{}, Topics: []analyticsTopicRow{}, Harnesses: []analyticsHarnessRow{}, Graphs: []analyticsGraphRow{}}
	r.Scope.IncludesArchived = true
	if project != "" {
		r.Scope.ProjectID = &project
	}
	if period == "week" {
		r.Period.WeekStart = start.Format("2006-01-02")
	}
	var previous analyticsBounds
	if period == "week" && compare {
		previous = analyticsBounds{start.AddDate(0, 0, -7), end.In(location).AddDate(0, 0, -7)}
		r.Comparison = &analyticsComparison{Start: analyticsStamp(previous.Start), End: analyticsStamp(previous.End), Summary: s.aggregate(previous, scope)}
	}
	for i := -12; i <= 0; i++ {
		w := monday.AddDate(0, 0, i*7)
		r.AvailableWeeks = append(r.AvailableWeeks, analyticsWeek{Start: w.Format("2006-01-02"), End: w.AddDate(0, 0, 6).Format("2006-01-02"), Current: i == 0})
	}
	bucket := func(a, z time.Time, label string) analyticsBucket {
		future := !a.Before(s.AsOf)
		if z.After(s.AsOf) {
			z = s.AsOf
		}
		t := s.aggregate(analyticsBounds{a, z}, scope)
		return analyticsBucket{Key: a.Format("2006-01-02"), Start: analyticsStamp(a), End: analyticsStamp(z), Label: label, Future: future, Working: t.WorkingMs, Tokens: t.TotalTokens}
	}
	if period == "week" {
		for i := 0; i < 7; i++ {
			a := start.AddDate(0, 0, i)
			z := a.AddDate(0, 0, 1)
			v := bucket(a, z, a.Format("Mon"))
			if r.Comparison != nil {
				pa := previous.Start.AddDate(0, 0, i)
				pz := pa.AddDate(0, 0, 1)
				if pz.After(previous.End) {
					pz = previous.End
				}
				p := s.aggregate(analyticsBounds{pa, pz}, scope)
				v.PreviousWorking, v.PreviousTokens = &p.WorkingMs, &p.TotalTokens
			}
			r.Series = append(r.Series, v)
		}
	} else {
		first := analyticsMonday(start)
		count := 0
		for w := first; w.Before(end); w = w.AddDate(0, 0, 7) {
			count++
		}
		stride := max(1, (count+12)/13)
		for w := first; w.Before(end); w = w.AddDate(0, 0, 7*stride) {
			z := w.AddDate(0, 0, 7*stride)
			if z.After(end) {
				z = end
			}
			r.Series = append(r.Series, bucket(w, z, w.Format("Jan 2")))
		}
	}
	// Split at local calendar midnights, including 23/25-hour DST days.
	for weekday := 0; weekday < 7; weekday++ {
		m := analyticsMetric(0)
		if !end.After(analyticsTime(s.Started)) {
			m = analyticsMissing("before_collection")
		} else if start.Before(analyticsTime(s.Started)) {
			m = analyticsPartial(m, "before_collection")
		}
		future := period == "week" && !start.AddDate(0, 0, weekday).Before(s.AsOf)
		if future {
			m = analyticsMissing("future")
		}
		for e := range s.executionRows(b, scope) {
			if !s.matches(e.OwnerID, e.ProjectID, scope) {
				continue
			}
			for span := range s.spanRows(e, b) {
				if span.Kind != "working" {
					continue
				}
				lo, hi := analyticsTime(span.Start).In(location), analyticsTime(span.End).In(location)
				if lo.Before(start) {
					lo = start
				}
				if hi.After(end) {
					hi = end
				}
				for d := analyticsDay(lo); d.Before(hi); d = d.AddDate(0, 0, 1) {
					if (int(d.Weekday())+6)%7 == weekday {
						analyticsAdd(&m, analyticsDuration(span.Start, span.End, analyticsBounds{maxTime(d, lo), minTime(d.AddDate(0, 0, 1), hi)}))
					}
				}
			}
		}
		if r.Summary.WorkingMs.Coverage == "partial" {
			m = analyticsPartial(m, "timing_partial")
		}
		m.Provisional = r.Summary.WorkingMs.Provisional
		r.Weekdays = append(r.Weekdays, analyticsWeekday{Weekday: weekday, Future: future, Working: m})
	}
	for _, p := range s.Projects {
		if project != "" && p.ID != project {
			continue
		}
		for _, topic := range append(append([]Topic(nil), p.Topics...), Topic{Name: "Ungrouped", Color: "neutral"}) {
			key := analyticsKey(p.ID, topic.ID)
			ts := scope
			ts.TopicKey = key
			var tid *string
			if topic.ID != "" {
				id := topic.ID
				tid = &id
			}
			r.Topics = append(r.Topics, analyticsTopicRow{Key: key, ProjectID: p.ID, TopicID: tid, Name: topic.Name, ProjectName: p.Name, Color: topic.Color, Archived: topic.Archived, Totals: s.aggregate(b, ts)})
		}
	}
	sort.Slice(r.Topics, func(i, j int) bool {
		a, b := r.Topics[i].Totals.WorkingMs.Value, r.Topics[j].Totals.WorkingMs.Value
		if a != nil && b != nil && *a != *b {
			return *a > *b
		}
		return r.Topics[i].Key < r.Topics[j].Key
	})
	for _, h := range []struct{ ID, Name string }{{"pi", "Pi"}, {"opencode", "OpenCode"}, {"codex", "Codex"}} {
		hs := scope
		hs.Harness = h.ID
		r.Harnesses = append(r.Harnesses, analyticsHarnessRow{ID: h.ID, Name: h.Name, Working: s.aggregate(b, hs).WorkingMs})
	}
	graphNames := map[string]analyticsGraphFact{}
	for _, g := range s.Graphs {
		if project != "" && g.ProjectID != project {
			continue
		}
		key := analyticsKey(g.ProjectID, g.GraphID)
		old, ok := graphNames[key]
		if !ok || analyticsTime(g.Start).After(analyticsTime(old.Start)) || analyticsTime(g.Start).Equal(analyticsTime(old.Start)) && g.ID > old.ID {
			graphNames[key] = g
		}
	}
	for key, g := range graphNames {
		gs := scope
		gs.GraphKey = key
		t := s.aggregate(b, gs)
		name := ""
		for _, p := range s.Projects {
			if p.ID == g.ProjectID {
				name = p.Name
			}
		}
		r.Graphs = append(r.Graphs, analyticsGraphRow{Key: key, ProjectID: g.ProjectID, GraphID: g.GraphID, Name: g.Name, ProjectName: name, Runs: t.GraphRuns, Wall: t.GraphWallMs, TimedRuns: t.GraphTimedRuns, Average: t.GraphAverageMs})
	}
	sort.Slice(r.Graphs, func(i, j int) bool { return r.Graphs[i].Key < r.Graphs[j].Key })
	calendarStart := monday.AddDate(0, 0, -84)
	r.Calendar.Start, r.Calendar.End = calendarStart.Format("2006-01-02"), monday.AddDate(0, 0, 6).Format("2006-01-02")
	r.Calendar.Days = []analyticsCalendarDay{}
	active := analyticsMetric(0)
	for i := 0; i < 91; i++ {
		d := calendarStart.AddDate(0, 0, i)
		z := minTime(d.AddDate(0, 0, 1), s.AsOf)
		t := s.aggregate(analyticsBounds{d, z}, scope)
		future := !d.Before(s.AsOf)
		if !future {
			if t.WorkingMs.Value != nil && *t.WorkingMs.Value > 0 {
				analyticsAdd(&active, 1)
			}
			if t.WorkingMs.Coverage != "complete" {
				active = analyticsPartial(active, "calendar_partial")
			}
		}
		r.Calendar.Days = append(r.Calendar.Days, analyticsCalendarDay{Date: d.Format("2006-01-02"), WeekStart: analyticsMonday(d).Format("2006-01-02"), Future: future, Working: t.WorkingMs})
	}
	if !s.AsOf.After(analyticsTime(s.Started)) {
		active = analyticsMissing("before_collection")
	}
	r.Calendar.ActiveDays = active
	r.Coverage.Reasons = []string{"prospective_collection", "current_topic_membership", "tokens_by_receipt_date"}
	r.Coverage.ChildSupport = []analyticsChildSupport{{Harness: "pi", Role: "all", Status: "unavailable"}, {Harness: "opencode", Role: "main", Status: "partial_identity_only"}, {Harness: "opencode", Role: "side_node_nested", Status: "unavailable"}, {Harness: "codex", Role: "managed", Status: "not_applicable"}}
	return r
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
