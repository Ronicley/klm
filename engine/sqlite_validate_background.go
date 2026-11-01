package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (v sqliteValidation) legacyUint(key string) (uint64, error) {
	var value string
	err := v.tx.QueryRowContext(v.ctx, `SELECT coalesce((SELECT value FROM store_meta WHERE key=?),'0')`, "legacy:"+key).Scan(&value)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(value, 10, 64)
}

func (v sqliteValidation) validateWebhooks() error {
	for _, check := range []struct{ label, query string }{
		{"webhook owner", `SELECT 1 FROM data_webhook_connections c LEFT JOIN data_projects p ON p.id=c.project_id WHERE p.id IS NULL`},
		{"webhook delivery dedupe", `SELECT x_webhookId,x_deliveryId FROM data_webhook_events GROUP BY x_webhookId,x_deliveryId HAVING count(*)>1`},
		{"webhook receipt sequence", `SELECT x_receivedSequence FROM data_webhook_events GROUP BY x_receivedSequence HAVING count(*)>1`},
		{"webhook release sequence", `SELECT x_releaseSequence FROM data_webhook_releases GROUP BY x_releaseSequence HAVING count(*)>1`},
		{"webhook pending owner", `SELECT 1 FROM data_webhook_pending p LEFT JOIN data_webhook_events e ON e.id=p.map_key WHERE e.id IS NULL`},
		{"webhook release owner", `SELECT 1 FROM data_webhook_releases r LEFT JOIN data_webhook_events e ON e.id=r.map_key WHERE e.id IS NULL`},
	} {
		if err := v.rejectRows(check.label, check.query); err != nil {
			return err
		}
	}
	if err := v.each("webhook_connections", nil, func(id string) error {
		var c WebhookConnection
		if err := v.get("webhook_connections", id, &c); err != nil {
			return err
		}
		return validateWebhookConnection(c)
	}); err != nil {
		return err
	}
	receivedSequence, err := v.legacyUint("webhookReceivedSequence")
	if err != nil {
		return err
	}
	releaseSequence, err := v.legacyUint("webhookReleaseSequence")
	if err != nil {
		return err
	}
	return v.each("webhook_events", nil, func(id string) error {
		var e WebhookStoredEvent
		if err := v.get("webhook_events", id, &e); err != nil {
			return err
		}
		if err := v.get("webhook_payloads", sqliteOwnedKey(id, ""), &e.Payload); err != nil {
			return err
		}
		var c WebhookConnection
		if err := v.get("webhook_connections", e.WebhookID, &c); err != nil {
			return err
		}
		received, receivedOK := webhookTime(e.ReceivedAt)
		if !validWebhookID(id) || e.ProjectID != c.ProjectID || e.Provider != "github" || !webhookASCII(e.DeliveryID, 128, false) || !webhookASCII(e.EventType, 100, true) || e.IsPing != (e.EventType == "ping") || !receivedOK || e.PayloadBytes != len(e.Payload) || e.PayloadBytes == 0 || e.PayloadBytes > maxWebhookPayloadBytes || e.ReceivedSequence == 0 || e.ReceivedSequence > receivedSequence {
			return errors.New("invalid webhook event")
		}
		created, _ := webhookTime(c.CreatedAt)
		last, lastOK := webhookTime(c.LastReceivedAt)
		if received.Before(created) || !lastOK || received.After(last) {
			return errors.New("invalid webhook receipt time")
		}
		if e.ExpiresAt != "" {
			expires, ok := webhookTime(e.ExpiresAt)
			validTTL := false
			for _, retention := range []string{"1d", "3d", "1w", "1mo", "3mo", "6mo", "1y"} {
				if webhookExpiry(received, retention) == e.ExpiresAt {
					validTTL = true
					break
				}
			}
			if !ok || !expires.After(received) || !validTTL {
				return errors.New("invalid webhook expiration")
			}
		}
		action, err := webhookPayloadAction(e.Payload)
		if err != nil || action != e.Action {
			return errors.New("invalid stored webhook payload")
		}
		pending, err := v.exists("webhook_pending", id)
		if err != nil {
			return err
		}
		released, err := v.exists("webhook_releases", id)
		if err != nil {
			return err
		}
		if pending == released {
			return errors.New("webhook must be pending or released")
		}
		if pending {
			var p WebhookPending
			if err := v.get("webhook_pending", id, &p); err != nil {
				return err
			}
			if p.EventID != id || p.ReceivedSequence != e.ReceivedSequence {
				return errors.New("invalid webhook pending entry")
			}
		}
		if released {
			var r WebhookRelease
			if err := v.get("webhook_releases", id, &r); err != nil {
				return err
			}
			emitted, ok := webhookTime(r.EmittedAt)
			if r.EventID != id || r.ReleaseSequence == 0 || r.ReleaseSequence > releaseSequence || !ok || emitted.Before(received) || webhookExpired(e, emitted) {
				return errors.New("invalid webhook release")
			}
		}
		return nil
	})
}

func (v sqliteValidation) validateBoard() error {
	var config activityBoardConfig
	if err := v.get("board_config", "settings", &config); err != nil {
		return err
	}
	if config.Schema != 1 {
		return errors.New("unsupported board schema")
	}
	// Phase uniqueness/mapping coverage is checked relationally. No global
	// phase/connection/item cache is constructed for validation.
	if err := v.rejectRows("board phase identity", `SELECT record_id FROM data_board_phases GROUP BY record_id HAVING record_id='' OR count(*)>1`); err != nil {
		return err
	}
	if _, err := v.tx.ExecContext(v.ctx, `CREATE TEMP TABLE validation_board_scopes(scope TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	if err := v.each("board_phases", nil, func(id string) error {
		var p activityBoardPhase
		if err := v.get("board_phases", id, &p); err != nil {
			return err
		}
		if strings.TrimSpace(p.Name) == "" {
			return errors.New("invalid board phase")
		}
		return nil
	}); err != nil {
		return err
	}
	var phases int
	if err := v.tx.QueryRowContext(v.ctx, `SELECT count(*) FROM data_board_phases`).Scan(&phases); err != nil {
		return err
	}
	if err := v.each("board_connections", nil, func(id string) error {
		var c activityBoardConnection
		if err := v.get("board_connections", id, &c); err != nil {
			return err
		}
		if exists, err := v.exists("projects", c.ProjectID); err != nil {
			return err
		} else if !exists {
			return errors.New("board project missing")
		}
		var credential []byte
		if err := v.get("board_credentials", id, &credential); err != nil {
			return err
		}
		if len(credential) == 0 {
			return errors.New("board credential missing")
		}
		if c.Provider != "github" && c.Provider != "azure" || c.OnlyAssignedToMe && c.Provider != "azure" {
			return errors.New("invalid board provider")
		}
		if c.Provider == "github" && (c.Scope.RepositoryID == "" || !boardRepositoryPattern.MatchString(c.Scope.Repository)) {
			return errors.New("invalid board repository")
		}
		if c.Provider == "azure" && (!boardOrganizationPattern.MatchString(c.Scope.Organization) || c.Scope.OrganizationID == "" || c.Scope.ProjectID == "" || c.Scope.WorkItemType == "") {
			return errors.New("invalid Azure board scope")
		}
		if _, err := v.tx.ExecContext(v.ctx, `INSERT INTO validation_board_scopes(scope) VALUES(?)`, boardScopeKey(c)); err != nil {
			return errors.New("duplicate board scope")
		}
		if len(c.Mappings) != phases {
			return errors.New("board phase mapping incomplete")
		}
		used := map[string]bool{}
		for phase, values := range c.Mappings {
			var exists bool
			if err := v.tx.QueryRowContext(v.ctx, `SELECT EXISTS(SELECT 1 FROM data_board_phases WHERE record_id=?)`, phase).Scan(&exists); err != nil {
				return err
			}
			if !exists || len(values) == 0 {
				return errors.New("board phase mapping missing")
			}
			for _, value := range values {
				if value.ID == "" || value.Name == "" || used[value.ID] {
					return errors.New("invalid board value mapping")
				}
				used[value.ID] = true
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := v.each("board_items", nil, func(id string) error {
		var item activityBoardItem
		if err := v.get("board_items", id, &item); err != nil {
			return err
		}
		exists, err := v.exists("board_connections", item.ConnectionID)
		if err != nil {
			return err
		}
		if id != item.Key || item.ExternalID == "" || !exists || item.Version == "" {
			return errors.New("invalid board item")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := v.each("board_moves", nil, func(id string) error {
		var m activityBoardMove
		if err := v.get("board_moves", id, &m); err != nil {
			return err
		}
		if id != m.OperationID || id == "" || m.Key == "" {
			return errors.New("invalid board receipt")
		}
		switch m.Status {
		case "pending", "unconfirmed", "confirmed", "failed":
		default:
			return errors.New("invalid board move status")
		}
		if boardMoveUnresolved(m) {
			if exists, err := v.exists("board_connections", m.ConnectionID); err != nil {
				return err
			} else if !exists {
				return errors.New("orphan board intent")
			}
		}
		return nil
	}); err != nil {
		return err
	}
	_, err := v.tx.ExecContext(v.ctx, `DROP TABLE validation_board_scopes`)
	return err
}

func (v sqliteValidation) eachOrdinal(table, owner string, visit func(string) error) error {
	if !sqliteKnownTable(table) {
		return errors.New("unknown validation table")
	}
	var after int64 = -1
	for {
		var id string
		var pos int64
		err := v.tx.QueryRowContext(v.ctx, `SELECT id,position FROM data_`+table+` WHERE owner_id=? AND position>? ORDER BY position LIMIT 1`, owner, after).Scan(&id, &pos)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := visit(id); err != nil {
			return err
		}
		after = pos
	}
}

func (v sqliteValidation) validateAnalytics() error {
	exists, err := v.exists("analytics_settings", "settings")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	var a *AnalyticsState
	if err := v.get("analytics_settings", "settings", &a); err != nil {
		return err
	}
	if a == nil {
		return nil
	}
	if a.Version != 1 || analyticsTime(a.Started).IsZero() {
		return errors.New("invalid analytics schema")
	}
	for _, field := range []string{"executions", "counters", "graphs"} {
		var kind string
		if err := v.tx.QueryRowContext(v.ctx, `SELECT kind FROM record_collections WHERE parent_table='analytics_settings' AND parent_id='settings' AND field=?`, field).Scan(&kind); err != nil {
			return err
		}
		if kind != "object" {
			return errors.New("missing analytics collection")
		}
	}
	if err := v.each("analytics_executions", nil, func(key string) error {
		var e AnalyticsExecution
		if err := v.get("analytics_executions", key, &e); err != nil {
			return err
		}
		var mapKey string
		if err := v.tx.QueryRowContext(v.ctx, `SELECT map_key FROM data_analytics_executions WHERE id=?`, key).Scan(&mapKey); err != nil {
			return err
		}
		exists, err := v.exists("sessions", e.SessionID)
		if err != nil {
			return err
		}
		if e.ID != mapKey || !exists || analyticsTime(e.Reserved).IsZero() || analyticsTime(e.Last).IsZero() {
			return errors.New("invalid analytics execution")
		}
		spans, err := v.collectionCount("analytics_executions", key, "spans")
		if err != nil {
			return err
		}
		usage, err := v.collectionCount("analytics_executions", key, "usage")
		if err != nil {
			return err
		}
		waits, err := v.collectionCount("analytics_executions", key, "waits")
		if err != nil {
			return err
		}
		unknown, err := v.collectionCount("analytics_executions", key, "unknownWaits")
		if err != nil {
			return err
		}
		if spans > 100000 || usage > 100000 || waits+unknown > 1024 {
			return errors.New("analytics execution capacity exceeded")
		}
		previous := time.Time{}
		if err := v.eachOrdinal("analytics_spans", key, func(id string) error {
			var span AnalyticsSpan
			if err := v.get("analytics_spans", id, &span); err != nil {
				return err
			}
			start, end := analyticsTime(span.Start), analyticsTime(span.End)
			if start.IsZero() || end.Before(start) || start.Before(previous) || (span.Kind != "working" && span.Kind != "blocked" && span.Kind != "unknown") {
				return errors.New("invalid analytics span")
			}
			previous = end
			return nil
		}); err != nil {
			return err
		}
		var sequence uint64
		return v.eachOrdinal("analytics_usage", key, func(id string) error {
			var delta AnalyticsUsageDelta
			if err := v.get("analytics_usage", id, &delta); err != nil {
				return err
			}
			if delta.Sequence <= sequence || delta.Sequence > e.Sequence || analyticsTime(delta.At).IsZero() || !analyticsValidCount(delta.Input) || !analyticsValidCount(delta.Output) {
				return errors.New("invalid analytics delta")
			}
			sequence = delta.Sequence
			return nil
		})
	}); err != nil {
		return err
	}
	if err := v.each("analytics_counters", nil, func(id string) error {
		var c AnalyticsCounter
		if err := v.get("analytics_counters", id, &c); err != nil {
			return err
		}
		var exists bool
		if err := v.tx.QueryRowContext(v.ctx, `SELECT EXISTS(SELECT 1 FROM data_analytics_executions WHERE map_key=?)`, c.ExecutionID).Scan(&exists); err != nil {
			return err
		}
		if !exists || !analyticsValidCount(c.Input) || !analyticsValidCount(c.Output) {
			return errors.New("invalid analytics counter")
		}
		return nil
	}); err != nil {
		return err
	}
	return v.each("analytics_graphs", nil, func(id string) error {
		var g AnalyticsGraphTiming
		if err := v.get("analytics_graphs", id, &g); err != nil {
			return err
		}
		var runID string
		if err := v.tx.QueryRowContext(v.ctx, `SELECT map_key FROM data_analytics_graphs WHERE id=?`, id).Scan(&runID); err != nil {
			return err
		}
		exists, err := v.exists("graph_runs", runID)
		if err != nil {
			return err
		}
		if !exists || analyticsTime(g.Last).IsZero() || g.End != "" && analyticsTime(g.End).IsZero() {
			return fmt.Errorf("invalid analytics graph timing")
		}
		return nil
	})
}
