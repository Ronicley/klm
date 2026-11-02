package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // Calendar buckets must also work on Windows without host zone files.
)

type metricsPeriod struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
	Bucket   string `json:"bucket"`
	from, to time.Time
	location *time.Location
}

type analyticsMeasure struct {
	Executions       int                    `json:"executions"` // Start cohort, not completion cohort.
	DurationMs       *int64                 `json:"durationMs"`
	DurationCoverage executionUsageCoverage `json:"durationCoverage"`
	Usage            observedExecutionUsage `json:"usage"` // End cohort, never native lifetime totals.
	durationOverflow bool
}

type analyticsGroup struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	analyticsMeasure
}

type metricsBucket struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
	analyticsMeasure
	from, to time.Time
}

type executionAnalytics struct {
	Period                 metricsPeriod    `json:"period"`
	Summary                analyticsMeasure `json:"summary"`
	Projects               []analyticsGroup `json:"projects"`
	Harnesses              []analyticsGroup `json:"harnesses"`
	Buckets                []metricsBucket  `json:"buckets"`
	MatchedSessions        int              `json:"matchedSessions"`
	SessionsWithHistory    int              `json:"sessionsWithHistory"`
	UndatedUsageExecutions int              `json:"undatedUsageExecutions"`
	TimeAttribution        string           `json:"timeAttribution"`
	ModelAttribution       string           `json:"modelAttribution"`
}

func parseAnalyticsPeriod(r *http.Request) (metricsPeriod, []metricsBucket, error) {
	q := r.URL.Query()
	p := metricsPeriod{Timezone: q.Get("timezone"), Bucket: q.Get("bucket")}
	if p.Timezone == "" {
		p.Timezone = "UTC"
	}
	if p.Bucket == "" {
		p.Bucket = "daily"
	}
	if p.Bucket != "daily" && p.Bucket != "weekly" {
		return p, nil, fmt.Errorf("Bucket must be daily or weekly.")
	}
	// Reject host-relative Local and fixed-offset aliases; require a named IANA zone or UTC.
	if p.Timezone != "UTC" && (!strings.Contains(p.Timezone, "/") || strings.Contains(p.Timezone, "\\")) {
		return p, nil, fmt.Errorf("Use an IANA timezone or UTC.")
	}
	var err error
	p.location, err = time.LoadLocation(p.Timezone)
	if err != nil {
		return p, nil, fmt.Errorf("Unknown IANA timezone.")
	}
	p.from, err = time.Parse(time.RFC3339Nano, q.Get("from"))
	if err != nil {
		return p, nil, fmt.Errorf("From must be an RFC3339 instant with an explicit offset.")
	}
	p.to, err = time.Parse(time.RFC3339Nano, q.Get("to"))
	if err != nil || !p.to.After(p.from) {
		return p, nil, fmt.Errorf("To must be an RFC3339 instant after from.")
	}
	// Bounded calendar iteration and duration arithmetic; no speculative historical backfill.
	if p.from.Year() < 1970 || p.to.Year() > 9999 || p.to.Sub(p.from) > 366*24*time.Hour*5 {
		return p, nil, fmt.Errorf("Period must be within 1970–9999 and at most five years.")
	}
	p.from, p.to = p.from.UTC(), p.to.UTC()
	p.From, p.To = p.from.Format(time.RFC3339Nano), p.to.Format(time.RFC3339Nano)
	local := p.from.In(p.location)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, p.location)
	days := 1
	if p.Bucket == "weekly" {
		days = 7
		start = start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
	}
	buckets := []metricsBucket{}
	for start.Before(p.to) {
		// Some historical zones skip/repeat midnight. Refuse a normalized or
		// ambiguous boundary rather than silently shifting a calendar bucket.
		if start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 || start.Add(-time.Nanosecond).In(p.location).Format("2006-01-02") == start.Format("2006-01-02") {
			return p, nil, fmt.Errorf("Timezone midnight boundary unavailable for this period.")
		}
		if len(buckets) >= 400 {
			return p, nil, fmt.Errorf("Period exceeds 400 buckets; shorten it or choose weekly.")
		}
		end := start.AddDate(0, 0, days)
		if !end.After(start) || end.Hour() != 0 || end.Minute() != 0 || end.Second() != 0 {
			return p, nil, fmt.Errorf("Timezone calendar interval unavailable.")
		}
		buckets = append(buckets, metricsBucket{From: start.UTC().Format(time.RFC3339Nano), To: end.UTC().Format(time.RFC3339Nano), Label: start.Format("2006-01-02"), from: start, to: end})
		start = end
	}
	return p, buckets, nil
}

func inAnalyticsPeriod(t, from, to time.Time) bool { return !t.Before(from) && t.Before(to) }

func (m *analyticsMeasure) observe(x ExecutionMetrics, from, to, stamp time.Time) {
	start, err := time.Parse(time.RFC3339Nano, x.StartedAt)
	if err != nil {
		return
	}
	cohort := inAnalyticsPeriod(start, from, to)
	if cohort {
		m.Executions++
	}
	// Tokens use only a known engine end timestamp, once, as an approximation.
	if x.EndedAt != nil {
		if end, err := time.Parse(time.RFC3339Nano, *x.EndedAt); err == nil && !end.Before(start) && inAnalyticsPeriod(end, from, to) {
			m.Usage.observe(x)
		}
	}
	endText := x.ObservedAt
	partial := x.Partial || x.EndedAt == nil
	var end time.Time
	if x.EndedAt != nil {
		endText = *x.EndedAt
	}
	end, err = time.Parse(time.RFC3339Nano, endText)
	if x.Outcome == nil {
		end, err = stamp, nil
	}
	if err != nil || end.Before(start) {
		if cohort {
			m.DurationCoverage.Total++
			m.DurationCoverage.Unavailable++
		}
		return
	}
	// Known observed intervals are clipped, including recovery records without an invented end.
	left, right := start, end
	if left.Before(from) {
		left = from
	}
	if right.After(to) {
		right = to
	}
	if right.Before(left) || (right.Equal(left) && !cohort) {
		return
	}
	if !end.After(from) && !cohort {
		return
	}
	if !start.Before(to) {
		return
	}
	m.DurationCoverage.Total++
	if partial {
		m.DurationCoverage.Partial++
	} else {
		m.DurationCoverage.Complete++
	}
	ms := right.Sub(left).Milliseconds()
	// Bound by the same checked integer addition used for tokens.
	if !m.durationOverflow {
		if m.DurationMs == nil {
			m.DurationMs = new(int64)
		}
		sum := sumTokens(m.DurationMs, &ms)
		if sum == nil {
			m.DurationMs = nil
			m.durationOverflow = true
		} else {
			m.DurationMs = sum
		}
	}
}

// Called under the existing inventory lock; all values are response-owned scalars.
func buildExecutionAnalytics(d *diskState, rows []metricsRow, p metricsPeriod, buckets []metricsBucket, stamp string) executionAnalytics {
	result := executionAnalytics{Period: p, Buckets: buckets, Projects: []analyticsGroup{}, Harnesses: []analyticsGroup{}, MatchedSessions: len(rows), TimeAttribution: "execution_end_approximation", ModelAttribution: "unallocated"}
	sampled, _ := time.Parse(time.RFC3339Nano, stamp)
	projects, harnesses := map[string]*analyticsGroup{}, map[string]*analyticsGroup{}
	for _, row := range rows {
		s := d.session(row.ID)
		if s == nil || s.Metrics == nil {
			continue
		}
		result.SessionsWithHistory++
		pg := projects[row.ProjectID]
		if pg == nil {
			pg = &analyticsGroup{ID: row.ProjectID, Label: row.Project}
			projects[row.ProjectID] = pg
		}
		for _, x := range s.Metrics.Executions {
			// Historical harness comes from the execution, never the current selection.
			harness := x.Harness
			if harness == "" {
				harness = "unallocated"
			}
			hg := harnesses[harness]
			if hg == nil {
				hg = &analyticsGroup{ID: harness, Label: harness}
				harnesses[harness] = hg
			}
			result.Summary.observe(x, p.from, p.to, sampled)
			pg.observe(x, p.from, p.to, sampled)
			hg.observe(x, p.from, p.to, sampled)
			if x.EndedAt == nil && x.Accounting != nil && x.Accounting.Usage != nil && !x.Accounting.Invalidated {
				result.UndatedUsageExecutions++
			}
			for i := range result.Buckets {
				b := &result.Buckets[i]
				from, to := b.from, b.to
				if from.Before(p.from) {
					from = p.from
				}
				if to.After(p.to) {
					to = p.to
				}
				b.observe(x, from, to, sampled)
			}
		}
	}
	for _, g := range projects {
		result.Projects = append(result.Projects, *g)
	}
	for _, g := range harnesses {
		result.Harnesses = append(result.Harnesses, *g)
	}
	sort.Slice(result.Projects, func(i, j int) bool { return result.Projects[i].ID < result.Projects[j].ID })
	sort.Slice(result.Harnesses, func(i, j int) bool { return result.Harnesses[i].ID < result.Harnesses[j].ID })
	return result
}
