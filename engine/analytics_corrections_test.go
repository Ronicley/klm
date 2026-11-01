package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// These bounded checks exercise only in-memory records/patches. No engine,
// harness, journal file, graph execution or user data is opened.
func analyticsCorrectionState() diskState {
	d := diskState{Sessions: []Session{{ID: "s", ProjectID: "p"}}}
	_ = initializeAnalytics(&d)
	d.Analytics.Started = analyticsStamp(time.Now().Add(-time.Hour))
	return d
}

func TestAnalyticsCorrectionReservationLifecycle(t *testing.T) {
	d := analyticsCorrectionState()
	anchor := time.Now().Add(-11 * time.Second)
	reserved := analyticsStamp(anchor.Add(-time.Minute))
	e := AnalyticsExecution{ID: "e", SessionID: "s", OwnerID: "s", ProjectID: "p", Harness: "codex", Reserved: reserved, Last: reserved}
	d.Analytics.Executions[e.ID] = e
	trn := &turn{analyticsID: e.ID}
	a := &app{runs: map[string]*turn{"s": trn}}
	a.checkpointAnalyticsLocked(&d)
	if got := d.Analytics.Executions[e.ID]; len(got.Reasons) != 0 || got.UnknownUntil != "" || got.Last != reserved {
		t.Fatal("preparation checkpoint clocked an unaccepted reservation")
	}

	// Establish acceptance after slow preparation, then cap native work at 10s.
	e.Accepted, e.Last = analyticsStamp(anchor), analyticsStamp(anchor)
	d.Analytics.Executions[e.ID] = e
	trn.analyticsAnchor, trn.analyticsEndCandidate = anchor, anchor.Add(10*time.Second)
	a.checkpointAnalyticsLocked(&d)
	analyticsFinish(&d, trn, false)
	got := d.Analytics.Executions[e.ID]
	if len(got.Spans) != 1 || analyticsDuration(got.Spans[0].Start, got.Spans[0].End, analyticsBounds{anchor, anchor.Add(time.Minute)}) != 10000 || len(got.Reasons) != 0 {
		t.Fatalf("expected 10s native work without preparation: %+v", got)
	}

	for _, dispatch := range []bool{false, true} {
		state := analyticsCorrectionState()
		row := AnalyticsExecution{ID: "prepare", SessionID: "s", OwnerID: "s", ProjectID: "p", Harness: "pi", Reserved: reserved, Last: reserved}
		if dispatch {
			row.Dispatched = analyticsStamp(anchor)
		}
		state.Analytics.Executions[row.ID] = row
		analyticsFinish(&state, &turn{analyticsID: row.ID}, true)
		closed := state.Analytics.Executions[row.ID]
		if closed.Ended == "" || len(closed.Spans) != 0 {
			t.Fatal("unaccepted execution did not settle cleanly")
		}
		if dispatch {
			if closed.Last != row.Dispatched || closed.UnknownUntil == "" || !reflect.DeepEqual(closed.Reasons, []string{"dispatch_acceptance_unknown"}) {
				t.Fatal("unconfirmed dispatch lost its bounded acceptance uncertainty")
			}
		} else {
			if closed.UnknownUntil != "" || len(closed.Reasons) != 0 {
				t.Fatal("preparation failure created an execution uncertainty tail")
			}
			snapshot := analyticsSnapshot{Started: state.Analytics.Started, Executions: []AnalyticsExecution{closed}}
			totals := snapshot.aggregate(analyticsBounds{anchor.Add(-time.Minute), time.Now()}, analyticsScope{})
			for _, m := range []AnalyticsMetric{totals.SessionsUsed, totals.AgentsExecuted, totals.WorkingMs, totals.BlockedMs, totals.TotalTokens} {
				if m.Value == nil || *m.Value != 0 || m.Coverage != "complete" {
					t.Fatal("preparation failure polluted totals")
				}
			}
		}
		// Recovery follows the same distinctions without measuring offline time.
		row.Ended = ""
		state.Analytics.Executions[row.ID] = row
		recoverAnalytics(&state)
		if recovered := state.Analytics.Executions[row.ID]; (recovered.UnknownUntil != "") != dispatch {
			t.Fatal("recovery conflated preparation with dispatch")
		}
	}
}

func TestAnalyticsCorrectionSparsePatches(t *testing.T) {
	d := analyticsCorrectionState()
	anchor := time.Now().Add(-10 * time.Second)
	stamp := analyticsStamp(anchor)
	payload := make([]AnalyticsUsageDelta, 256)
	for i := range payload {
		payload[i] = AnalyticsUsageDelta{Sequence: uint64(i + 1), At: stamp}
	}
	for i := 0; i < 256; i++ {
		id := fmt.Sprintf("closed-%d", i)
		d.Analytics.Executions[id] = AnalyticsExecution{ID: id, SessionID: "s", Reserved: stamp, Last: stamp, Ended: stamp, Usage: payload, Sequence: 256}
	}
	d.Analytics.Executions["live"] = AnalyticsExecution{ID: "live", SessionID: "s", Accepted: stamp, Reserved: stamp, Last: stamp, Spans: []AnalyticsSpan{{Start: stamp, End: stamp, Kind: "working"}}, Usage: payload, Sequence: 256}
	d.Analytics.Graphs["run"] = AnalyticsGraphTiming{Last: stamp}
	n := transactionState(d)
	if n.Analytics != d.Analytics || n.tx.analyticsExecutions != nil {
		t.Fatal("unrelated transaction copied Analytics")
	}
	var patches []statePatch
	if err := diffTransaction(reflect.ValueOf(d), reflect.ValueOf(n), nil, &patches); err != nil {
		t.Fatal(err)
	}
	if err := diffAnalyticsTransaction(d.Analytics, &n, &patches); err != nil || len(patches) != 0 {
		t.Fatal("unrelated transaction traversed or patched Analytics")
	}
	a := &app{runs: map[string]*turn{"s": {analyticsID: "live", analyticsAnchor: anchor}}, graphRuns: map[string]*graphExecution{"run": nil}, analyticsGraphClocks: map[string]time.Time{"run": anchor}}
	a.checkpointAnalyticsLocked(&n)
	live, _ := n.analyticsExecution("live")
	if &live.Usage[0] != &d.Analytics.Executions["live"].Usage[0] {
		t.Fatal("checkpoint copied retained usage payload")
	}
	n.session("s").Status = "idle" // Same lifecycle frame, not a second write.
	if err := diffTransaction(reflect.ValueOf(d), reflect.ValueOf(n), nil, &patches); err != nil {
		t.Fatal(err)
	}
	if err := diffAnalyticsTransaction(d.Analytics, &n, &patches); err != nil {
		t.Fatal(err)
	}
	for _, p := range patches {
		if p.Path[0] != "analytics" {
			continue
		}
		if len(p.Path) < 3 {
			t.Fatalf("whole collector patch: %v", p.Path)
		}
		if p.Path[1] == "executions" && p.Path[2] != "live" {
			t.Fatal("checkpoint rewrote a closed execution")
		}
		for _, part := range p.Path {
			if part == "usage" {
				t.Fatal("checkpoint rewrote usage history")
			}
		}
		t.Logf("patch: %v", p.Path)
	}
	encoded, _ := json.Marshal(patches)
	if len(encoded) > 2048 {
		t.Fatalf("checkpoint frame grew with retained ledger: %d bytes", len(encoded))
	}
	if d.Analytics.Executions["live"].Last != stamp || d.Analytics.Executions["live"].Spans[0].End != stamp || d.session("s").Status != "" {
		t.Fatal("staged mutation leaked before publication")
	}
	// Whole-object legacy Analytics patch, then new sparse patches: same state as
	// publication, as at checkpoint + WAL recovery, without filesystem I/O.
	base, _ := json.Marshal(d.Analytics)
	recovered := diskState{Sessions: []Session{{ID: "s", ProjectID: "p"}}}
	if err := replayTransaction(&recovered, []statePatch{{Path: []string{"analytics"}, Value: base}}); err != nil {
		t.Fatal(err)
	}
	if err := replayTransaction(&recovered, patches); err != nil {
		t.Fatal(err)
	}
	publishAnalyticsTransaction(&n)
	want, _ := json.Marshal(n)
	actual, _ := json.Marshal(recovered)
	if string(want) != string(actual) {
		t.Fatal("sparse replay differs from publication")
	}
	t.Logf("256 closed rows x 256 usage samples: checkpoint %d bytes, %d patches", len(encoded), len(patches))
	legacy := diskState{}
	initial := transactionState(legacy)
	_ = initializeAnalytics(&initial)
	var initPatches []statePatch
	if err := diffAnalyticsTransaction(nil, &initial, &initPatches); err != nil {
		t.Fatal(err)
	}
	if err := replayTransaction(&legacy, initPatches); err != nil || legacy.Analytics == nil {
		t.Fatal("nil legacy initialization failed")
	}
}

func TestAnalyticsCorrectionAllRoleCoverage(t *testing.T) {
	anchor := time.Now().Add(-time.Minute)
	end := anchor.Add(10 * time.Second)
	input, output := int64(100), int64(20)
	for _, h := range []string{"pi", "opencode", "codex"} {
		e := AnalyticsExecution{ID: "e", SessionID: "s", OwnerID: "s", ProjectID: "p", Harness: h, Accepted: analyticsStamp(anchor), Last: analyticsStamp(end), Ended: analyticsStamp(end), InputBaseline: true, OutputBaseline: true, InputFinal: true, OutputFinal: true, Spans: []AnalyticsSpan{{Start: analyticsStamp(anchor), End: analyticsStamp(end), Kind: "working"}}, Usage: []AnalyticsUsageDelta{{At: analyticsStamp(end), Input: &input, Output: &output}}}
		if h == "opencode" {
			e.Role = sessionRoleSideAgent
		}
		s := analyticsSnapshot{Started: analyticsStamp(anchor.Add(-time.Hour)), Executions: []AnalyticsExecution{e}}
		totals := s.aggregate(analyticsBounds{anchor, end.Add(time.Second)}, analyticsScope{})
		want := "partial"
		if h == "codex" {
			want = "complete"
		}
		for _, m := range []AnalyticsMetric{totals.WorkingMs, totals.BlockedMs, totals.InputTokens, totals.OutputTokens, totals.TotalTokens} {
			if m.Coverage != want || m.Value == nil {
				t.Fatalf("%s all-role coverage: %+v", h, m)
			}
		}
		if *totals.InputTokens.Value != input || *totals.OutputTokens.Value != output || *totals.TotalTokens.Value != input+output {
			t.Fatal("coverage correction changed observed usage")
		}
		if h == "codex" && totals.NativeSubagents.Coverage != "not_applicable" {
			t.Fatal("managed Codex disabled handling changed")
		}
	}
}
