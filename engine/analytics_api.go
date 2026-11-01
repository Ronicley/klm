package main

import (
	"net/http"
	"net/url"
	"time"
	_ "time/tzdata" // Portable IANA zone rules for installed Windows engines.
)

func (a *app) getAnalytics(w http.ResponseWriter, r *http.Request) {
	if a.store != nil {
		a.getSQLAnalytics(w, r)
		return
	}
	q, err := queryAnalytics(r)
	if err != "" {
		fail(w, 400, err)
		return
	}
	a.mu.Lock()
	q.location, _ = time.LoadLocation(a.state.engineZone())
	if a.storageErr != nil || a.state.Analytics == nil {
		a.mu.Unlock()
		fail(w, 503, "Analytics storage is unavailable.")
		return
	}
	if q.project != "" {
		p := a.state.project(q.project)
		if p == nil || p.Removed {
			a.mu.Unlock()
			fail(w, 404, "Project not found.")
			return
		}
	}
	asOf := time.Now().UTC()
	start := analyticsMonday(asOf.In(q.location))
	if q.week != "" {
		start, _ = time.ParseInLocation("2006-01-02", q.week, q.location)
	}
	if start.After(analyticsMonday(asOf.In(q.location))) {
		a.mu.Unlock()
		fail(w, 400, "Future weeks are unavailable.")
		return
	}
	snapshot := a.analyticsSnapshotLocked(asOf)
	a.mu.Unlock()
	if r.Context().Err() != nil {
		return
	}
	result := snapshot.response(q.location, q.project, q.period, start, q.compare)
	if r.Context().Err() != nil {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, result)
}

type analyticsQuery struct {
	project, period, week string
	location              *time.Location
	compare               bool
}

func queryAnalytics(r *http.Request) (analyticsQuery, string) {
	q := analyticsQuery{period: "week", location: time.UTC, compare: true}
	values, err := parseAnalyticsQuery(r)
	if err != "" {
		return q, err
	}
	if v, ok := values["period"]; ok {
		if v != "week" && v != "all" {
			return q, "Invalid analytics period."
		}
		q.period = v
	}
	if v, ok := values["compare"]; ok {
		if v != "previous" && v != "none" {
			return q, "Invalid comparison."
		}
		q.compare = v == "previous"
	}
	if v, ok := values["projectId"]; ok {
		if v == "" {
			return q, "Invalid project."
		}
		if v != "all" {
			q.project = v
		}
	}
	if v, ok := values["timeZone"]; ok {
		if len(v) == 0 || len(v) > 100 || v == "Local" {
			return q, "Invalid time zone."
		}
		loc, e := time.LoadLocation(v)
		if e != nil {
			return q, "Invalid IANA time zone."
		}
		q.location = loc
	}
	if v, ok := values["weekStart"]; ok {
		d, e := time.ParseInLocation("2006-01-02", v, q.location)
		if e != nil || d.Weekday() != time.Monday || d.Year() < 1970 || q.period != "week" {
			return q, "weekStart must be a Monday date for a weekly period."
		}
		q.week = v
	}
	if q.period == "all" {
		q.compare = false
	}
	return q, ""
}
func parseAnalyticsQuery(r *http.Request) (map[string]string, string) {
	values, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, "Invalid analytics query."
	}
	result := map[string]string{}
	for key, list := range values {
		switch key {
		case "period", "weekStart", "projectId", "timeZone", "compare":
		default:
			return nil, "Unknown analytics parameter."
		}
		if len(list) != 1 {
			return nil, "Analytics parameters must have one value."
		}
		result[key] = list[0]
	}
	return result, ""
}
