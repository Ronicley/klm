package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"
	"strings"
)

var analyticsStateType = reflect.TypeOf((*AnalyticsState)(nil))
var analyticsExecutionMapType = reflect.TypeOf(map[string]AnalyticsExecution{})
var analyticsGraphMapType = reflect.TypeOf(map[string]AnalyticsGraphTiming{})

// Transactions share the published collector and stage only touched rows. Usage
// arrays are immutable here: only the atomic usage stream mutation appends them.
// An unrelated engine transaction never clones or compares retained Analytics.
func (d *diskState) analyticsExecution(id string) (AnalyticsExecution, bool) {
	if d.Analytics == nil {
		return AnalyticsExecution{}, false
	}
	if d.tx != nil {
		if e, ok := d.tx.analyticsExecutions[id]; ok {
			return e, true
		}
	}
	e, ok := d.Analytics.Executions[id]
	if ok && d.tx != nil {
		e.Spans = slices.Clone(e.Spans)
		e.Reasons = slices.Clone(e.Reasons)
		cloneKeys := func(keys map[string]bool) map[string]bool {
			if keys == nil {
				return nil
			}
			copy := make(map[string]bool, len(keys))
			for k, v := range keys {
				copy[k] = v
			}
			return copy
		}
		e.Waits, e.UnknownWaits = cloneKeys(e.Waits), cloneKeys(e.UnknownWaits)
		d.setAnalyticsExecution(e)
	}
	return e, ok
}
func (d *diskState) setAnalyticsExecution(e AnalyticsExecution) {
	if d.tx == nil {
		d.Analytics.Executions[e.ID] = e
		return
	}
	if d.tx.analyticsExecutions == nil {
		d.tx.analyticsExecutions = map[string]AnalyticsExecution{}
	}
	d.tx.analyticsExecutions[e.ID] = e
}
func (d *diskState) analyticsGraph(id string) (AnalyticsGraphTiming, bool) {
	if d.Analytics == nil {
		return AnalyticsGraphTiming{}, false
	}
	if d.tx != nil {
		if g, ok := d.tx.analyticsGraphs[id]; ok {
			return g, true
		}
	}
	g, ok := d.Analytics.Graphs[id]
	return g, ok
}
func (d *diskState) setAnalyticsGraph(id string, g AnalyticsGraphTiming) {
	if d.tx == nil {
		d.Analytics.Graphs[id] = g
		return
	}
	if d.tx.analyticsGraphs == nil {
		d.tx.analyticsGraphs = map[string]AnalyticsGraphTiming{}
	}
	d.tx.analyticsGraphs[id] = g
}

func diffAnalyticsTransaction(before *AnalyticsState, d *diskState, patches *[]statePatch) error {
	a := d.Analytics
	if before == nil && a != nil {
		// Initialization is the only whole-object patch. Legacy nil state and old
		// whole-object frames remain readable with the same JSON schema.
		b, err := json.Marshal(a)
		if err != nil {
			return err
		}
		*patches = append(*patches, statePatch{Path: []string{"analytics"}, Value: b})
	} else if before != a {
		return errors.New("analytics collection cannot be replaced in a transaction")
	}
	if a == nil || d.tx == nil {
		return nil
	}
	keys := make([]string, 0, len(d.tx.analyticsExecutions))
	for id := range d.tx.analyticsExecutions {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		e := d.tx.analyticsExecutions[id]
		if err := analyticsRecordLimit(e); err != nil {
			return err
		}
		old, exists := a.Executions[id]
		path := []string{"analytics", "executions", id}
		if !exists {
			b, err := json.Marshal(e)
			if err != nil {
				return err
			}
			*patches = append(*patches, statePatch{Path: path, Value: b})
			continue
		}
		if !reflect.DeepEqual(old.Usage, e.Usage) {
			return errors.New("analytics usage requires the atomic stream mutation")
		}
		bv, av := reflect.ValueOf(old), reflect.ValueOf(e)
		for i := 0; i < av.NumField(); i++ {
			if reflect.DeepEqual(bv.Field(i).Interface(), av.Field(i).Interface()) {
				continue
			}
			name := strings.Split(av.Type().Field(i).Tag.Get("json"), ",")[0]
			if err := diffTransaction(bv.Field(i), av.Field(i), patchPath(path, name), patches); err != nil {
				return err
			}
		}
	}
	keys = keys[:0]
	for id := range d.tx.analyticsGraphs {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		g := d.tx.analyticsGraphs[id]
		old, exists := a.Graphs[id]
		if exists && old == g {
			continue
		}
		b, err := json.Marshal(g)
		if err != nil {
			return err
		}
		*patches = append(*patches, statePatch{Path: []string{"analytics", "graphs", id}, Value: b})
	}
	return nil
}
func publishAnalyticsTransaction(d *diskState) {
	if d.Analytics == nil || d.tx == nil {
		return
	}
	for id, e := range d.tx.analyticsExecutions {
		d.Analytics.Executions[id] = e
	}
	for id, g := range d.tx.analyticsGraphs {
		d.Analytics.Graphs[id] = g
	}
}
