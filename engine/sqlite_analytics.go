package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"slices"
	"time"
)

type sqlAnalyticsLive struct {
	At    string `json:"at"`
	Drift bool   `json:"drift"`
}
type sqlAnalyticsReport struct {
	ctx                 context.Context
	tx                  *sql.Tx
	err                 error
	asOf                time.Time
	live, graphLive     map[string]sqlAnalyticsLive // owned handles only, never retained inventory
	liveJSON, graphJSON string
	provisional         map[string]bool
}

func (r *sqlAnalyticsReport) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}
func (r *sqlAnalyticsReport) matches(_, project string, scope analyticsScope) bool {
	return scope.ProjectID == "" || scope.ProjectID == project
} // iterators enforce topic/owner scope
func sqlAnalyticsScope(alias, owner string, scope analyticsScope) (string, []any) {
	where := ` AND (?='' OR ` + alias + `.project_id=?)`
	args := []any{scope.ProjectID, scope.ProjectID}
	if scope.TopicKey != "" {
		var key []string
		if json.Unmarshal([]byte(scope.TopicKey), &key) != nil || len(key) != 2 {
			return where + ` AND 0`, args
		}
		where += ` AND ` + alias + `.project_id=? AND ` + owner + `.x_topicId=?`
		args = append(args, key[0], key[1])
	}
	return where, args
}
func sqlAnalyticsRows[T any](r *sqlAnalyticsReport, table, from, where string, args []any) iter.Seq[T] {
	return func(yield func(T) bool) {
		if r.err != nil {
			return
		}
		after := ""
		for {
			values := append(append([]any{}, args...), after)
			ids, err := sqliteQueryIDs(r.ctx, r.tx, `SELECT v.id FROM data_`+table+` v `+from+` WHERE (`+where+`) AND v.id>? ORDER BY v.id LIMIT 100`, values...)
			if err != nil {
				r.fail(err)
				return
			}
			if len(ids) == 0 {
				return
			}
			for _, id := range ids {
				var value T
				if err := decodeSQLiteRecord(r.ctx, r.tx, table, id, 1<<20, &value); err != nil {
					r.fail(err)
					return
				}
				if !yield(value) {
					return
				}
				after = id
			}
		}
	}
}
func (s analyticsSnapshot) ownerRows(b analyticsBounds, scope analyticsScope) iter.Seq[analyticsInventory] {
	if s.sql == nil {
		return func(yield func(analyticsInventory) bool) {
			for _, v := range s.Owners {
				if !yield(v) {
					return
				}
			}
		}
	}
	return func(yield func(analyticsInventory) bool) {
		r := s.sql
		if r.err != nil || scope.Harness != "" || scope.GraphKey != "" || !b.End.After(b.Start) {
			return
		}
		filter, args := sqlAnalyticsScope("s", "s", scope)
		rows, err := r.tx.QueryContext(r.ctx, `SELECT s.id,s.project_id,s.x_topicId,s.created_at FROM data_sessions s JOIN data_projects p ON p.id=s.project_id WHERE p.x_removed<>'true' AND s.role='' AND s.parent_id='' AND s.x_graphRunId='' AND s.created_at>=? AND s.created_at<?`+filter, append([]any{sqliteTimeKey(analyticsStamp(b.Start)), sqliteTimeKey(analyticsStamp(b.End))}, args...)...)
		if err != nil {
			r.fail(err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var v analyticsInventory
			if err := rows.Scan(&v.ID, &v.ProjectID, &v.TopicID, &v.Created); err != nil {
				r.fail(err)
				return
			}
			if !yield(v) {
				return
			}
		}
		r.fail(rows.Err())
	}
}
func (s analyticsSnapshot) executionRows(b analyticsBounds, scope analyticsScope) iter.Seq[AnalyticsExecution] {
	if s.sql == nil {
		return func(yield func(AnalyticsExecution) bool) {
			for _, v := range s.Executions {
				if !yield(v) {
					return
				}
			}
		}
	}
	return func(yield func(AnalyticsExecution) bool) {
		r := s.sql
		if !b.End.After(b.Start) || scope.GraphKey != "" {
			return
		}
		filter, args := sqlAnalyticsScope("v", "o", scope)
		where := `p.x_removed<>'true' AND (?='' OR v.x_harness=?) AND (
 (v.x_accepted<>'' AND v.x_accepted<? AND (v.x_last>=? OR v.x_ended='' OR v.x_unknownUntil>=?)) OR
 (v.x_dispatched>=? AND v.x_dispatched<?) OR
 EXISTS(SELECT 1 FROM data_analytics_usage u WHERE u.owner_id=v.id AND u.x_at>=? AND u.x_at<?) OR
 EXISTS(SELECT 1 FROM data_analytics_spans z WHERE z.owner_id=v.id AND z.x_start<? AND z.x_end>?) )` + filter
		lo, hi := sqliteTimeKey(analyticsStamp(b.Start)), sqliteTimeKey(analyticsStamp(b.End))
		values := append([]any{scope.Harness, scope.Harness, hi, lo, lo, lo, hi, lo, hi, hi, lo}, args...)
		for e := range sqlAnalyticsRows[AnalyticsExecution](r, "analytics_executions", `JOIN data_projects p ON p.id=v.project_id LEFT JOIN data_sessions o ON o.id=v.x_ownerId`, where, values) {
			key := sqliteOwnedKey("settings", e.ID)
			reasons, more, err := readSQLChildren[string](r.ctx, r.tx, "analytics_reasons", key, 128, 64<<10)
			if err != nil {
				r.fail(err)
				return
			}
			if more {
				r.fail(errors.New("Invalid analytics reason capacity."))
				return
			}
			e.Reasons = reasons
			if live, ok := r.live[e.ID]; ok && e.Ended == "" && e.Accepted != "" {
				if live.Drift {
					analyticsReason(&e, "clock_discontinuity")
					e.UnknownUntil = analyticsStamp(r.asOf)
				} else {
					var waits, unknown bool
					err := r.tx.QueryRowContext(r.ctx, `SELECT EXISTS(SELECT 1 FROM data_analytics_waits WHERE owner_id=?),EXISTS(SELECT 1 FROM data_analytics_unknown_waits WHERE owner_id=?)`, key, key).Scan(&waits, &unknown)
					if err != nil {
						r.fail(err)
						return
					}
					if waits {
						e.Waits = map[string]bool{"current": true}
					}
					if unknown {
						e.UnknownWaits = map[string]bool{"current": true}
					}
					analyticsAdvance(&e, analyticsTime(live.At))
					r.provisional[e.ID] = true
				}
			}
			if !yield(e) {
				return
			}
		}
	}
}
func (s analyticsSnapshot) spanRows(e AnalyticsExecution, b analyticsBounds) iter.Seq[AnalyticsSpan] {
	return func(yield func(AnalyticsSpan) bool) {
		if s.sql != nil {
			r := s.sql
			for span := range sqlAnalyticsRows[AnalyticsSpan](r, "analytics_spans", "", `v.owner_id=? AND v.x_start<? AND v.x_end>?`, []any{sqliteOwnedKey("settings", e.ID), sqliteTimeKey(analyticsStamp(b.End)), sqliteTimeKey(analyticsStamp(b.Start))}) {
				if !yield(span) {
					return
				}
			}
		}
		for _, span := range e.Spans {
			if !yield(span) {
				return
			}
		}
	}
}
func (s analyticsSnapshot) usageRows(e AnalyticsExecution, b analyticsBounds) iter.Seq[AnalyticsUsageDelta] {
	return func(yield func(AnalyticsUsageDelta) bool) {
		if s.sql != nil {
			r := s.sql
			for delta := range sqlAnalyticsRows[AnalyticsUsageDelta](r, "analytics_usage", "", `v.owner_id=? AND v.x_at>=? AND v.x_at<?`, []any{sqliteOwnedKey("settings", e.ID), sqliteTimeKey(analyticsStamp(b.Start)), sqliteTimeKey(analyticsStamp(b.End))}) {
				if !yield(delta) {
					return
				}
			}
		} else {
			for _, delta := range e.Usage {
				if !yield(delta) {
					return
				}
			}
		}
	}
}

// Canonical timestamp projections and owned-live JSON keep exact distinct counts
// in SQLite. Neither IDs nor retained spans accumulate in Go sets for all-time views.
const sqlAnalyticsFacts = `WITH execution_facts AS (
 SELECT e.*,CASE WHEN e.x_accepted<>'' AND e.x_ended='' AND l.value IS NOT NULL AND json_extract(l.value,'$.drift')=0
 AND NOT EXISTS(SELECT 1 FROM data_analytics_reasons z WHERE z.owner_id=e.id AND z.scalar_value='clock_discontinuity')
 THEN max(e.x_last,json_extract(l.value,'$.at')) ELSE e.x_last END AS effective_last
 FROM data_analytics_executions e LEFT JOIN json_each(?) l ON l.key=e.record_id
), graph_times AS (
 SELECT g.*,t.id AS timing_id,t.x_interrupted,
 CASE WHEN t.id IS NOT NULL THEN CASE WHEN t.x_end<>'' THEN t.x_end WHEN l.value IS NOT NULL AND t.x_interrupted<>'true' AND json_extract(l.value,'$.drift')=0 THEN json_extract(l.value,'$.at') ELSE t.x_last END
 ELSE CASE WHEN g.status='interrupted' OR g.x_endedAt='' THEN g.created_at ELSE g.x_endedAt END END AS observed_end,
 CASE WHEN t.id IS NOT NULL THEN (t.x_interrupted='true' OR t.x_end='' AND (l.value IS NULL OR json_extract(l.value,'$.drift')=1)) ELSE (g.status='interrupted' OR g.x_endedAt='') END AS unknown,
 (t.id IS NOT NULL AND t.x_end='' AND t.x_interrupted<>'true' AND l.value IS NOT NULL AND json_extract(l.value,'$.drift')=0) AS provisional
 FROM data_graph_runs g LEFT JOIN data_analytics_graphs t ON t.map_key=g.id LEFT JOIN json_each(?) l ON l.key=g.id
), graph_facts AS (SELECT *,max(created_at,coalesce(observed_end,created_at)) AS effective_end FROM graph_times) `

func (s analyticsSnapshot) graphRows(b analyticsBounds, scope analyticsScope) iter.Seq[analyticsGraphFact] {
	if s.sql == nil {
		return func(yield func(analyticsGraphFact) bool) {
			for _, g := range s.Graphs {
				if !yield(g) {
					return
				}
			}
		}
	}
	return func(yield func(analyticsGraphFact) bool) {
		r := s.sql
		if r.err != nil || scope.Harness != "" || !b.End.After(b.Start) {
			return
		}
		filter, args := sqlAnalyticsScope("g", "o", scope)
		if scope.GraphKey != "" {
			var key []string
			if json.Unmarshal([]byte(scope.GraphKey), &key) != nil || len(key) != 2 {
				return
			}
			filter += ` AND g.project_id=? AND g.x_graphId=?`
			args = append(args, key[0], key[1])
		}
		rows, err := r.tx.QueryContext(r.ctx, sqlAnalyticsFacts+`SELECT g.id,g.project_id,g.x_conversationId,g.x_graphId,g.created_at,g.effective_end,g.x_endedAt,g.unknown,g.provisional FROM graph_facts g JOIN data_projects p ON p.id=g.project_id LEFT JOIN data_sessions o ON o.id=g.x_conversationId WHERE p.x_removed<>'true' AND g.created_at<? AND (g.effective_end>=? OR g.unknown AND (g.x_endedAt='' OR g.x_endedAt>?))`+filter, append([]any{r.liveJSON, r.graphJSON, sqliteTimeKey(analyticsStamp(b.End)), sqliteTimeKey(analyticsStamp(b.Start)), sqliteTimeKey(analyticsStamp(b.Start))}, args...)...)
		if err != nil {
			r.fail(err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var g analyticsGraphFact
			var ended string
			if err := rows.Scan(&g.ID, &g.ProjectID, &g.OwnerID, &g.GraphID, &g.Start, &g.End, &ended, &g.Unknown, &g.Provisional); err != nil {
				r.fail(err)
				return
			}
			if g.Unknown {
				g.UnknownUntil = ended
				if ended == "" {
					g.UnknownUntil = analyticsStamp(r.asOf)
				}
			}
			if !yield(g) {
				return
			}
		}
		r.fail(rows.Err())
	}
}
func (r *sqlAnalyticsReport) counts(b analyticsBounds, scope analyticsScope) (used, agents, children, runs, graphs int) {
	if r.err != nil || !b.End.After(b.Start) {
		return
	}
	lo, hi := sqliteTimeKey(analyticsStamp(b.Start)), sqliteTimeKey(analyticsStamp(b.End))
	eFilter, eArgs := sqlAnalyticsScope("e", "o", scope)
	gFilter, gArgs := sqlAnalyticsScope("g", "o", scope)
	if scope.Harness != "" {
		eFilter += ` AND e.x_harness=?`
		eArgs = append(eArgs, scope.Harness)
		gFilter += ` AND 0`
	}
	if scope.GraphKey != "" {
		eFilter += ` AND 0`
		var key []string
		if json.Unmarshal([]byte(scope.GraphKey), &key) != nil || len(key) != 2 {
			return
		}
		gFilter += ` AND g.project_id=? AND g.x_graphId=?`
		gArgs = append(gArgs, key[0], key[1])
	}
	query := sqlAnalyticsFacts + `, matched_executions AS (SELECT e.* FROM execution_facts e JOIN data_projects p ON p.id=e.project_id LEFT JOIN data_sessions o ON o.id=e.x_ownerId WHERE p.x_removed<>'true' AND e.x_accepted<>'' AND e.x_accepted<? AND (e.effective_last>? OR e.effective_last=e.x_accepted AND e.x_accepted>=?)` + eFilter + `), matched_graphs AS (SELECT g.* FROM graph_facts g JOIN data_projects p ON p.id=g.project_id LEFT JOIN data_sessions o ON o.id=g.x_conversationId WHERE p.x_removed<>'true' AND g.created_at<? AND (g.effective_end>? OR g.effective_end=g.created_at AND g.created_at>=?)` + gFilter + `) SELECT
 (SELECT count(*) FROM (SELECT x_ownerId AS owner FROM matched_executions UNION SELECT x_conversationId FROM matched_graphs) u JOIN data_sessions s ON s.id=u.owner WHERE s.role='' AND s.parent_id='' AND s.x_graphRunId=''),
 (SELECT count(DISTINCT session_id) FROM matched_executions WHERE role<>'subagent'),(SELECT count(DISTINCT session_id) FROM matched_executions WHERE role='subagent'),
 (SELECT count(*) FROM matched_graphs),(SELECT count(DISTINCT json_array(project_id,x_graphId)) FROM matched_graphs)`
	args := append([]any{r.liveJSON, r.graphJSON, hi, lo, lo}, eArgs...)
	args = append(args, hi, lo, lo)
	args = append(args, gArgs...)
	r.fail(r.tx.QueryRowContext(r.ctx, query, args...).Scan(&used, &agents, &children, &runs, &graphs))
	return
}
func (r *sqlAnalyticsReport) earliest(scope analyticsScope) time.Time {
	var stamp sql.NullString
	r.fail(r.tx.QueryRowContext(r.ctx, `SELECT min(stamp) FROM (SELECT s.created_at AS stamp,s.project_id FROM data_sessions s WHERE s.role='' AND s.parent_id='' AND s.x_graphRunId='' UNION ALL SELECT created_at,project_id FROM data_graph_runs) v JOIN data_projects p ON p.id=v.project_id WHERE p.x_removed<>'true' AND v.stamp<>'' AND (?='' OR v.project_id=?)`, scope.ProjectID, scope.ProjectID).Scan(&stamp))
	return analyticsTime(stamp.String)
}

func (a *app) getSQLAnalytics(w http.ResponseWriter, request *http.Request) {
	q, message := queryAnalytics(request)
	if message != "" {
		fail(w, 400, message)
		return
	}
	ctx := request.Context()
	a.mu.Lock()
	asOf := time.Now().UTC()
	report := &sqlAnalyticsReport{ctx: ctx, asOf: asOf, live: map[string]sqlAnalyticsLive{}, graphLive: map[string]sqlAnalyticsLive{}, provisional: map[string]bool{}}
	for _, t := range a.runs {
		if t.analyticsID != "" && t.ctx.Err() == nil {
			at, drift := t.analyticsInstant()
			report.live[t.analyticsID] = sqlAnalyticsLive{At: sqliteTimeKey(analyticsStamp(minTime(at, asOf))), Drift: drift}
		}
	}
	for id := range a.graphRuns {
		at, drift := a.analyticsGraphInstant(id)
		report.graphLive[id] = sqlAnalyticsLive{At: sqliteTimeKey(analyticsStamp(minTime(at, asOf))), Drift: drift}
	}
	tx, err := a.store.readers.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	var revision string
	if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='revision'`).Scan(&revision)
	}
	a.mu.Unlock()
	if err != nil {
		if tx != nil {
			tx.Rollback()
		}
		sqlHTTPError(w, err)
		return
	}
	defer tx.Rollback()
	report.tx = tx
	live, _ := json.Marshal(report.live)
	graphs, _ := json.Marshal(report.graphLive)
	report.liveJSON, report.graphJSON = string(live), string(graphs)
	st := &sqliteSessionTx{ctx: ctx, tx: tx}
	settings, err := st.engineSettings()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	q.location, err = time.LoadLocation(settings.TimeZone)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	if q.project != "" {
		available, err := st.projectAvailable(q.project)
		if err != nil {
			sqlHTTPError(w, err)
			return
		}
		if !available {
			fail(w, 404, "Project not found.")
			return
		}
	}
	var metadata *AnalyticsState
	if err := decodeSQLiteRecord(ctx, tx, "analytics_settings", "settings", 64<<10, &metadata); err != nil || metadata == nil {
		fail(w, 503, "Analytics storage is unavailable.")
		return
	}
	snapshot := analyticsSnapshot{sql: report, Started: metadata.Started, Revision: revision, AsOf: asOf, Provisional: report.provisional, Projects: []analyticsProject{}}
	err = st.eachMatching("projects", `x_removed<>'true'`, nil, func(id string) error {
		p, err := st.project(id)
		if err != nil {
			return err
		}
		project := analyticsProject{ID: p.ID, Name: p.Name}
		if q.project == "" || q.project == id {
			err = st.eachMatching("topics", `owner_id=?`, []any{id}, func(key string) error {
				var t Topic
				if err := decodeSQLiteRecord(ctx, tx, "topics", key, 64<<10, &t); err != nil {
					return err
				}
				project.Topics = append(project.Topics, t)
				return nil
			})
		}
		snapshot.Projects = append(snapshot.Projects, project)
		return err
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	slices.SortFunc(snapshot.Projects, func(a, b analyticsProject) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	rows, err := tx.QueryContext(ctx, `WITH ranked AS (SELECT r.id,r.project_id,r.x_conversationId,r.x_graphId,r.created_at,s.x_graph_definition_name,row_number() OVER(PARTITION BY r.project_id,r.x_graphId ORDER BY r.created_at DESC,r.id DESC) AS rank FROM data_graph_runs r JOIN data_projects p ON p.id=r.project_id JOIN data_graph_snapshots s ON s.owner_id=r.id WHERE p.x_removed<>'true' AND (?='' OR r.project_id=?)) SELECT id,project_id,x_conversationId,x_graphId,created_at,x_graph_definition_name FROM ranked WHERE rank=1`, q.project, q.project)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	for rows.Next() {
		var g analyticsGraphFact
		if err := rows.Scan(&g.ID, &g.ProjectID, &g.OwnerID, &g.GraphID, &g.Start, &g.Name); err != nil {
			rows.Close()
			sqlHTTPError(w, err)
			return
		}
		snapshot.Graphs = append(snapshot.Graphs, g)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	start := analyticsMonday(asOf.In(q.location))
	if q.week != "" {
		start, _ = time.ParseInLocation("2006-01-02", q.week, q.location)
	}
	if start.After(analyticsMonday(asOf.In(q.location))) {
		fail(w, 400, "Future weeks are unavailable.")
		return
	}
	result := snapshot.response(q.location, q.project, q.period, start, q.compare)
	if report.err != nil {
		sqlHTTPError(w, report.err)
		return
	}
	if err := tx.Commit(); err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, result)
}
