package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Derived, compact per-event facts are produced once on ingress/import. Provider
// payloads remain lossless and cold. Group membership is evaluated from indexed
// explicit links at the requested watermark, including retention and ambiguity.
func indexSQLGithubActivity(ctx context.Context, tx *sql.Tx, e WebhookStoredEvent) error {
	i, _ := normalizeGithubActivity(e, false)
	i.ID, i.DetailEventID, i.ProjectID, i.WebhookID = e.ID, e.ID, e.ProjectID, e.WebhookID
	encoded, err := json.Marshal(i)
	if err != nil {
		return err
	}
	scope, runKey, runCard, deployKey, deployCard := "", "", "", "", ""
	m := i.Pipeline
	if m == nil {
		m = &githubPipelineMetadata{}
	} else if m.RepositoryID != "" {
		scope = pipelineScope(i)
		runKey = pipelineRunKey(i)
		if runKey != "" {
			runCard = pipelineCardID(runKey)
		}
		if m.DeploymentID != "" {
			deployKey = scope + "/deployment/" + m.DeploymentID
			deployCard = pipelineCardID(deployKey)
		}
	}
	rank := 0
	if i.Pipeline != nil {
		if m.Kind == "deployment_status" || (m.Kind != "deployment" && pipelineTerminal(i)) {
			rank = 1
		}
	}
	search := strings.ToLower(strings.Join([]string{i.Repository, i.Actor, i.Title, i.Summary, i.Reference, i.Branch, i.BaseBranch, i.ActionLabel, i.EventType, "GitHub"}, " "))
	_, err = tx.ExecContext(ctx, `INSERT INTO github_activity_summaries VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(event_id) DO UPDATE SET payload=excluded.payload,scope=excluded.scope,run_key=excluded.run_key,run_card=excluded.run_card,deploy_key=excluded.deploy_key,deploy_card=excluded.deploy_card,kind=excluded.kind,provider_id=excluded.provider_id,attempt=excluded.attempt,check_url=excluded.check_url,suite_id=excluded.suite_id,deployment_id=excluded.deployment_id,state_rank=excluded.state_rank,state_at=excluded.state_at,category=excluded.category,catalog=excluded.catalog,search=excluded.search`, e.ID, encoded, scope, runKey, runCard, deployKey, deployCard, m.Kind, m.ID, m.Attempt, m.CheckURL, m.SuiteID, m.DeploymentID, rank, sqliteTimeKey(pipelineStateTime(i).Format(time.RFC3339Nano)), i.Category, i.CatalogID, search)
	return err
}

const sqlGithubGroups = `WITH base AS (
 SELECT n.*,e.project_id,e.x_receivedAt AS received,e.x_expiresAt AS expires,r.x_emittedAt AS emitted,r.x_releaseSequence AS seq,
 substr('00000000000000000000'||r.x_releaseSequence,-20) AS seq_key,p.x_name AS project_name
 FROM github_activity_summaries n JOIN data_webhook_events e ON e.id=n.event_id JOIN data_webhook_releases r ON r.x_eventId=e.id JOIN data_projects p ON p.id=e.project_id
 WHERE p.x_removed<>'true' AND (e.x_expiresAt='' OR e.x_expiresAt>?) AND substr('00000000000000000000'||r.x_releaseSequence,-20)<=?
), checks AS (
 SELECT scope,check_url,max(run_key) AS link,max(run_card) AS card,max(provider_id) AS job,count(DISTINCT run_key||char(0)||provider_id) AS n FROM base WHERE kind='workflow_job' AND run_key<>'' AND provider_id<>'' AND check_url<>'' GROUP BY scope,check_url
), suites AS (
 SELECT scope,suite_id,max(run_key) AS link,max(run_card) AS card,max(attempt) AS attempt,count(DISTINCT run_key) AS n FROM base WHERE kind='workflow_run' AND run_key<>'' AND suite_id<>'' GROUP BY scope,suite_id
), deployment_links AS (
 SELECT scope,deployment_id,run_key AS link,run_card AS card,provider_id AS job FROM base WHERE kind='workflow_job' AND provider_id<>'' AND run_key<>'' AND deployment_id<>''
 UNION SELECT b.scope,b.deployment_id,c.link,c.card,c.job FROM base b JOIN checks c ON c.scope=b.scope AND c.check_url=b.check_url AND c.n=1 WHERE b.kind='check_run' AND b.deployment_id<>'' AND b.check_url<>''
), deployments AS (
 SELECT scope,deployment_id,max(link) AS link,max(card) AS card,max(job) AS job,count(DISTINCT link||char(0)||job) AS n FROM deployment_links GROUP BY scope,deployment_id
), grouped AS (
 SELECT b.*,
 CASE WHEN b.kind IN ('workflow_run','workflow_job') AND b.provider_id<>'' AND b.run_key<>'' THEN b.run_card
 WHEN b.kind='check_run' AND c.n=1 THEN c.card
 WHEN b.kind='check_suite' AND b.attempt<>'' AND s.n=1 AND s.attempt=b.attempt THEN s.card
 WHEN b.kind IN ('deployment','deployment_status') AND b.deployment_id<>'' AND b.scope<>'' THEN CASE WHEN d.n=1 THEN d.card ELSE b.deploy_card END
 ELSE b.event_id END AS card_id,
 CASE WHEN b.kind IN ('workflow_run','workflow_job') AND b.provider_id<>'' AND b.run_key<>'' THEN b.run_key
 WHEN b.kind='check_run' AND c.n=1 THEN c.link
 WHEN b.kind='check_suite' AND b.attempt<>'' AND s.n=1 AND s.attempt=b.attempt THEN s.link
 WHEN b.kind IN ('deployment','deployment_status') AND b.deployment_id<>'' AND b.scope<>'' THEN CASE WHEN d.n=1 THEN d.link ELSE b.deploy_key END ELSE '' END AS group_key,
 CASE WHEN b.kind='workflow_run' THEN 'workflow_run.id + run_attempt' WHEN b.kind='workflow_job' THEN 'workflow_job.run_id + run_attempt'
 WHEN b.kind='check_run' AND c.n=1 THEN 'workflow_job.check_run_url = check_run.url'
 WHEN b.kind='check_suite' AND b.attempt<>'' AND s.n=1 AND s.attempt=b.attempt THEN 'workflow_run.check_suite_id + check_suite.run_attempt'
 WHEN b.kind IN ('deployment','deployment_status') AND b.deployment_id<>'' THEN CASE WHEN d.n=1 THEN 'explicit job/check deployment.id; job '||d.job ELSE 'deployment.id' END ELSE '' END AS relation,
 CASE WHEN b.kind='workflow_job' THEN 'job/'||b.provider_id WHEN b.kind IN ('deployment','deployment_status') THEN 'deployment/'||b.deployment_id ELSE '' END AS child_key
 FROM base b LEFT JOIN checks c ON c.scope=b.scope AND c.check_url=b.check_url LEFT JOIN suites s ON s.scope=b.scope AND s.suite_id=b.provider_id LEFT JOIN deployments d ON d.scope=b.scope AND d.deployment_id=b.deployment_id
), visible AS (SELECT * FROM grouped WHERE (?='' OR category=?) AND catalog NOT IN (SELECT value FROM json_each(?))),
 members AS (SELECT *,row_number() OVER(PARTITION BY card_id ORDER BY seq_key DESC,event_id) AS newest FROM visible),
 states AS (SELECT *,row_number() OVER(PARTITION BY card_id ORDER BY state_rank DESC,state_at DESC,seq_key DESC) AS state_order FROM visible WHERE kind='workflow_run' OR (kind IN ('deployment','deployment_status') AND relation NOT LIKE 'explicit job%')),
 children AS (SELECT *,row_number() OVER(PARTITION BY card_id,child_key ORDER BY state_rank DESC,state_at DESC,seq_key DESC) AS child_order FROM visible WHERE child_key<>'') `

type sqlGithubQuery struct {
	ctx      context.Context
	tx       *sql.Tx
	args     []any
	high     string
	excluded map[string]bool
}

func newSQLGithubQuery(ctx context.Context, tx *sql.Tx, high string, excluded map[string]bool, category string) (sqlGithubQuery, error) {
	st := &sqliteSessionTx{ctx: ctx, tx: tx}
	current, err := st.sequence("webhook_release_sequence")
	if err != nil {
		return sqlGithubQuery{}, err
	}
	if high == "" {
		high = sqliteRevision(current)
	}
	n, err := sqlExpectedRevision(high)
	if err != nil || n > current {
		return sqlGithubQuery{}, errors.New("Invalid activity watermark.")
	}
	ids := []string{}
	for _, key := range sortedGraphKeys(excluded) {
		if excluded[key] {
			ids = append(ids, key)
		}
	}
	encoded, _ := json.Marshal(ids)
	return sqlGithubQuery{ctx: ctx, tx: tx, high: high, excluded: excluded, args: []any{sqliteTimeKey(now()), strings.Repeat("0", 20-len(high)) + high, category, category, string(encoded)}}, nil
}
func (q sqlGithubQuery) ids(tail string, args ...any) ([]string, error) {
	return sqliteQueryIDs(q.ctx, q.tx, sqlGithubGroups+tail, append(append([]any{}, q.args...), args...)...)
}
func (q sqlGithubQuery) item(id string) (githubActivityItem, error) {
	var i githubActivityItem
	var raw []byte
	var relation string
	err := q.tx.QueryRowContext(q.ctx, sqlGithubGroups+`SELECT payload,project_name,seq,received,expires,emitted,relation,group_key FROM visible WHERE event_id=?`, append(append([]any{}, q.args...), id)...).Scan(&raw, &i.ProjectName, &i.ReleaseSequence, &i.ReceivedAt, &i.ExpiresAt, &i.EmittedAt, &relation, &i.GroupRelation)
	if err != nil {
		return i, err
	}
	var parsed githubActivityItem
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return i, err
	}
	parsed.ProjectName, parsed.ReleaseSequence, parsed.ReceivedAt, parsed.ExpiresAt, parsed.EmittedAt = i.ProjectName, i.ReleaseSequence, i.ReceivedAt, i.ExpiresAt, i.EmittedAt
	if i.GroupRelation != "" {
		parsed.GroupRelation = relation
		if q.excluded["workflow_job"] && strings.HasPrefix(relation, "explicit job") {
			parsed.GroupRelation = "explicit job/check deployment.id"
		}
	}
	return parsed, nil
}
func (q sqlGithubQuery) card(id string) (githubActivityItem, error) {
	var newest, state, key string
	var count, childCount int
	var expiry sql.NullString
	var filtered bool
	err := q.tx.QueryRowContext(q.ctx, sqlGithubGroups+`SELECT m.event_id,coalesce((SELECT event_id FROM states WHERE card_id=m.card_id AND state_order=1),''),m.group_key,(SELECT count(*) FROM visible WHERE card_id=m.card_id),(SELECT count(*) FROM children WHERE card_id=m.card_id AND child_order=1),(SELECT min(nullif(expires,'')) FROM visible WHERE card_id=m.card_id),(SELECT count(*) FROM grouped WHERE card_id=m.card_id)<>(SELECT count(*) FROM visible WHERE card_id=m.card_id) FROM members m WHERE m.card_id=? AND m.newest=1`, append(append([]any{}, q.args...), id)...).Scan(&newest, &state, &key, &count, &childCount, &expiry, &filtered)
	if err != nil {
		return githubActivityItem{}, err
	}
	latest, err := q.item(newest)
	if err != nil {
		return latest, err
	}
	if key == "" {
		return latest, nil
	}
	card := latest
	if state != "" {
		card, err = q.item(state)
		if err != nil {
			return card, err
		}
	} else {
		card.Result, card.Tone, card.ActionLabel = "", "muted", "Workflow activity"
		card.Title = "Workflow run"
		if card.Pipeline != nil && card.Pipeline.WorkflowName != "" {
			card.Title = card.Pipeline.WorkflowName
		}
	}
	g := &githubActivityGroup{Kind: "workflow", EventCount: count, Children: []githubActivityItem{}, Filtered: filtered, HighWatermark: q.high}
	parts := strings.Split(key, "/")
	if len(parts) >= 4 && parts[len(parts)-4] == "run" {
		g.RunID, g.Attempt = parts[len(parts)-3], parts[len(parts)-1]
	} else {
		g.Kind = "deployment"
		g.DeploymentID = parts[len(parts)-1]
	}
	rows, err := q.tx.QueryContext(q.ctx, sqlGithubGroups+`SELECT event_id,relation FROM visible WHERE card_id=? ORDER BY seq_key DESC,event_id`, append(append([]any{}, q.args...), id)...)
	if err != nil {
		return card, err
	}
	hash := sha256.New()
	for rows.Next() {
		var event, relation string
		if err := rows.Scan(&event, &relation); err != nil {
			rows.Close()
			return card, err
		}
		if q.excluded["workflow_job"] && strings.HasPrefix(relation, "explicit job") {
			relation = "explicit job/check deployment.id"
		}
		hash.Write([]byte(event + "\n" + relation + "\n"))
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return card, err
	}
	g.Composition = hex.EncodeToString(hash.Sum(nil))
	card.ID, card.Group, card.ReleaseSequence, card.ReceivedAt, card.EmittedAt, card.ExpiresAt = id, g, latest.ReleaseSequence, latest.ReceivedAt, latest.EmittedAt, expiry.String
	if g.Kind == "workflow" {
		card.Reference = "Run " + g.RunID
		if state != "" && card.Pipeline.RunNumber != "" {
			card.Reference = "Run #" + card.Pipeline.RunNumber
		}
		if g.Attempt != "" {
			card.Reference += " · Attempt " + g.Attempt
		}
		g.ChildCount = childCount
		ids, err := q.ids(`SELECT event_id FROM children WHERE card_id=? AND child_order=1 ORDER BY seq_key DESC LIMIT 8`, id)
		if err != nil {
			return card, err
		}
		bytes := 0
		for _, id := range ids {
			child, err := q.item(id)
			if err != nil {
				return card, err
			}
			encoded, _ := json.Marshal(child)
			if bytes+len(encoded) > 16<<10 {
				break
			}
			bytes += len(encoded)
			g.Children = append(g.Children, child)
		}
		g.ChildrenHasMore = len(g.Children) < g.ChildCount
	}
	if state != "" && card.Pipeline != nil {
		status := card.Pipeline.Conclusion
		if status == "" {
			status = card.Pipeline.Status
		}
		card.Result, card.Tone = activityHuman(status), pipelineTone(status)
	}
	return card, nil
}

func (a *app) readSQLGithubActivityPage(ctx context.Context, params url.Values, byteLimit, limit int, category, search string, excluded map[string]bool, filter string) (githubActivityPage, error) {
	page := githubActivityPage{Items: []githubActivityItem{}, AsOf: now()}
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		q, err := newSQLGithubQuery(ctx, tx, params.Get("high"), excluded, category)
		if err != nil {
			return err
		}
		before := "~~~~~~~~~~~~~~~~~~~~" // larger than any padded decimal key
		if raw := params.Get("cursor"); raw != "" {
			var c githubActivityCursor
			b, err := base64.RawURLEncoding.DecodeString(raw)
			if len(raw) > 1024 || err != nil || json.Unmarshal(b, &c) != nil || c.V != 2 || c.Kind != "github-activity" || c.Filter != filter {
				return errors.New("Invalid activity cursor. Refresh the feed.")
			}
			high, e1 := sqlExpectedRevision(c.High)
			position, e2 := sqlExpectedRevision(c.Before)
			max, _ := sqlExpectedRevision(q.high)
			if e1 != nil || e2 != nil || position == 0 || position > high || high > max {
				return errors.New("Invalid activity cursor.")
			}
			q, err = newSQLGithubQuery(ctx, tx, c.High, excluded, category)
			if err != nil {
				return err
			}
			before = strings.Repeat("0", 20-len(c.Before)) + c.Before
		}
		page.HighWatermark = q.high
		tail := `SELECT m.card_id FROM members m WHERE m.newest=1 AND m.seq_key<?`
		args := []any{before}
		members := params.Get("view") != ""
		if members {
			table, rank := "visible", ""
			if params.Get("view") == "children" {
				table, rank = "children", ` AND child_order=1 AND group_key LIKE '%/run/%'`
			}
			tail = `SELECT event_id FROM ` + table + ` WHERE card_id=? AND seq_key<?` + rank
			args = []any{params.Get("cardId"), before}
		} else if search != "" {
			matches := []string{}
			rows, err := tx.QueryContext(ctx, `SELECT id,x_name FROM data_projects WHERE x_removed<>'true'`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id, name string
				if err := rows.Scan(&id, &name); err != nil {
					rows.Close()
					return err
				}
				if strings.Contains(strings.ToLower(name), search) {
					matches = append(matches, id)
				}
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
			encoded, _ := json.Marshal(matches)
			tail += ` AND EXISTS(SELECT 1 FROM visible v WHERE v.card_id=m.card_id AND (instr(v.search,?)>0 OR v.project_id IN (SELECT value FROM json_each(?))))`
			args = append(args, search, string(encoded))
		}
		tail += ` ORDER BY seq_key DESC LIMIT ?`
		args = append(args, limit+1)
		ids, err := q.ids(tail, args...)
		if err != nil {
			return err
		}
		budget := byteLimit
		if budget == 0 {
			budget = 2 << 20
		}
		used := 2048
		for _, id := range ids {
			if len(page.Items) == limit {
				page.HasMore = true
				break
			}
			var item githubActivityItem
			var err error
			if members {
				item, err = q.item(id)
			} else {
				item, err = q.card(id)
			}
			if err != nil {
				return err
			}
			encoded, _ := json.Marshal(item)
			if used+len(encoded) > budget {
				if len(page.Items) == 0 {
					return errors.New("Activity item exceeds response limit.")
				}
				page.HasMore = true
				break
			}
			page.Items = append(page.Items, item)
			used += len(encoded) + 1
		}
		if page.HasMore {
			c := githubActivityCursor{V: 2, Kind: "github-activity", High: q.high, Before: page.Items[len(page.Items)-1].ReleaseSequence, Filter: filter}
			encoded, _ := json.Marshal(c)
			page.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
		}
		return nil
	})
	return page, err
}
func (a *app) readSQLGithubDetails(ctx context.Context, id string) (githubActivityDetails, error) {
	var details githubActivityDetails
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		q, err := newSQLGithubQuery(ctx, tx, "", nil, "")
		if err != nil {
			return err
		}
		event := id
		count := 0
		if strings.HasPrefix(id, "pipeline-") {
			card, err := q.card(id)
			if err != nil {
				return err
			}
			event = card.DetailEventID
			count = card.Group.EventCount
		} else if !validWebhookID(id) {
			return sql.ErrNoRows
		}
		if _, err := q.item(event); err != nil {
			return err
		}
		var e WebhookStoredEvent
		if err := decodeSQLiteRecord(ctx, tx, "webhook_events", event, 64<<10, &e); err != nil {
			return err
		}
		if err := decodeSQLiteRecord(ctx, tx, "webhook_payloads", sqliteOwnedKey(event, ""), maxStreamRecordBytes, &e.Payload); err != nil {
			return err
		}
		_, details = normalizeGithubActivity(e, true)
		details.ID = id
		if count > 0 {
			details.Fields = append(details.Fields, githubActivityField{Label: "Retained events", Value: strconv.Itoa(count)})
		}
		return nil
	})
	return details, err
}
func (a *app) readSQLGithubBookmarks(ctx context.Context, ids []string, watermark string) ([]githubActivityBookmark, error) {
	result := []githubActivityBookmark{}
	if len(ids) > 100 {
		return result, errors.New("At most 100 bookmark anchors may be resolved per request.")
	}
	for _, id := range ids {
		if !validWebhookID(id) {
			return result, errors.New("Invalid bookmark event ID.")
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		q, err := newSQLGithubQuery(ctx, tx, watermark, nil, "")
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(ids)
		rows, err := tx.QueryContext(ctx, sqlGithubGroups+`SELECT event_id,card_id,expires FROM visible WHERE event_id IN (SELECT value FROM json_each(?)) ORDER BY event_id`, append(append([]any{}, q.args...), string(encoded))...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item githubActivityBookmark
			if err := rows.Scan(&item.EventID, &item.CardID, &item.ExpiresAt); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	return result, err
}
