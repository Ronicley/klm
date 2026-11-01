package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestSQLiteAnalyticsReportSemantics(t *testing.T) {
	d := sqliteSemanticFixture(t)
	d.Analytics.Started = "2026-01-01T00:00:00Z"
	stamp := "2026-01-05T12:00:00Z"
	end := "2026-01-05T12:00:10Z"
	for i := range d.Sessions {
		d.Sessions[i].CreatedAt = stamp
	}
	e := d.Analytics.Executions["execution"]
	e.Reserved, e.Dispatched, e.Accepted, e.Last, e.Ended = stamp, stamp, stamp, end, end
	e.InputBaseline, e.InputFinal, e.OutputBaseline, e.OutputFinal = true, true, true, true
	e.Spans = []AnalyticsSpan{{Start: stamp, End: "2026-01-05T12:00:06Z", Kind: "working"}, {Start: "2026-01-05T12:00:06Z", End: end, Kind: "blocked"}}
	input, output := int64(10), int64(3)
	e.Usage = []AnalyticsUsageDelta{{Sequence: 1, At: end, Input: &input, Output: &output}}
	d.Analytics.Executions[e.ID] = e
	d.GraphRuns[0].CreatedAt, d.GraphRuns[0].EndedAt = stamp, end
	d.GraphRuns[0].Status = "completed"
	d.GraphRuns[0].Result = &GraphRunResult{Kind: "completed", Choice: &ChoiceIdentity{Origin: "graph", ID: "done"}}
	d.Analytics.Graphs["run"] = AnalyticsGraphTiming{Last: end, End: end}
	store, path := sqliteTestStore(t)
	body, _ := json.Marshal(d)
	ctx := context.Background()
	if err := stageLegacyCheckpoint(ctx, store, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := importStagedDomains(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := store.openReaders(ctx, path); err != nil {
		t.Fatal(err)
	}
	a := &app{store: store, ctx: ctx, runs: map[string]*turn{}}
	req := httptest.NewRequest("GET", "http://localhost/api/analytics?period=week&weekStart=2026-01-05&compare=none", nil)
	res := httptest.NewRecorder()
	a.getAnalytics(res, req)
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	var actual AnalyticsResponse
	if err := json.Unmarshal(res.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	legacy := &app{state: d}
	asOf := analyticsTime(actual.AsOf)
	snapshot := legacy.analyticsSnapshotLocked(asOf)
	location, _ := time.LoadLocation(defaultEngineTimeZone)
	start, _ := time.ParseInLocation("2006-01-02", "2026-01-05", location)
	expected := snapshot.response(location, "", "week", start, false)
	for name, pair := range map[string][2]any{"summary": {actual.Summary, expected.Summary}, "topics": {actual.Topics, expected.Topics}, "harnesses": {actual.Harnesses, expected.Harnesses}, "graphs": {actual.Graphs, expected.Graphs}, "weekdays": {actual.Weekdays, expected.Weekdays}} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			got, _ := json.Marshal(pair[0])
			want, _ := json.Marshal(pair[1])
			t.Fatalf("%s semantics changed:\ngot %s\nwant %s", name, got, want)
		}
	}
}
