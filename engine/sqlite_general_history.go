package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type sqlAgentHistoryCursor struct {
	Key, Version, Digest, Revision string
	Position, Offset               int64
}

func (a *app) readSQLGeneralHistory(ctx context.Context, name, id string, args generalToolArgs) (any, error) {
	if args.Limit > 20 {
		return nil, errors.New("Read at most 20 messages/events per call.")
	}
	if name == "session_messages_search" && strings.TrimSpace(args.Query) == "" {
		return nil, errors.New("Supply a nonempty literal query.")
	}
	if args.SinceRevision != nil && (args.EventID != "" || args.MessageID != "" || args.Offset != 0) {
		return nil, errors.New("Use a delta cursor for fragments; exact reads use eventId/offset.")
	}
	limit := args.Limit
	if limit == 0 {
		limit = 10
	}
	c := sqlAgentHistoryCursor{Key: agentCursorKey(name, args), Position: 0, Offset: int64(args.Offset)}
	if args.Cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(args.Cursor)
		if err != nil || json.Unmarshal(b, &c) != nil || c.Key != agentCursorKey(name, args) || c.Position < 0 || c.Offset < 0 {
			return nil, errors.New("Invalid history cursor.")
		}
	}
	items := []any{}
	result := map[string]any{"sessionId": id, "observedAt": now(), "resetRequired": false}
	more := false
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		if _, err := st.generalTarget(id); err != nil {
			return err
		}
		var version, revision string
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM data_sessions WHERE id=?`, id).Scan(&version); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='revision'`).Scan(&revision); err != nil {
			return err
		}
		if args.Cursor != "" && c.Version != version {
			return errors.New("History changed; restart the read to avoid mixing event versions.")
		}
		c.Version, c.Revision = version, revision
		result["revision"] = revision
		where := `owner_id=? AND position>=?`
		values := []any{id, c.Position}
		if name != "session_events_read" {
			where += ` AND kind IN ('user','assistant','agent_prompt','consultation')`
		}
		exact := args.EventID
		if exact == "" {
			exact = args.MessageID
		}
		if exact != "" {
			where += ` AND record_id=?`
			values = append(values, exact)
		}
		if args.SinceRevision != nil {
			since := sqliteRevision(*args.SinceRevision)
			upper, _ := strconv.ParseUint(revision, 10, 64)
			var imported string
			err := tx.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='legacy:graphRevision'`).Scan(&imported)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			floor, _ := strconv.ParseUint(imported, 10, 64)
			if *args.SinceRevision > upper || *args.SinceRevision < floor {
				result["resetRequired"] = true
			} else {
				where += ` AND (length(revision)>length(?) OR length(revision)=length(?) AND revision>?)`
				values = append(values, since, since, since)
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,record_id,position,payload_bytes,digest,revision,kind,created_at,status FROM data_session_events WHERE `+where+` ORDER BY position LIMIT 101`, values...)
		if err != nil {
			return err
		}
		type ref struct {
			key, id, digest, revision, kind, created, status string
			position, size                                   int64
		}
		refs := []ref{}
		for rows.Next() {
			var v ref
			if err := rows.Scan(&v.key, &v.id, &v.position, &v.size, &v.digest, &v.revision, &v.kind, &v.created, &v.status); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, v)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if exact != "" && len(refs) == 0 {
			return errors.New("Source message/event not found.")
		}
		more = len(refs) > 100
		if more {
			refs = refs[:100]
		}
		budget := agentToolByteLimit - 4096
		for index, v := range refs {
			if len(items) >= limit {
				more = true
				break
			}
			entry := map[string]any{"id": v.id, "index": strconv.FormatInt(v.position, 10), "revision": v.revision, "type": v.kind, "status": v.status, "createdAt": v.created}
			if name == "session_messages_search" {
				excerpt, match, err := searchSQLiteEvent(ctx, tx, v.key, args.Query)
				if err != nil {
					return err
				}
				if !match {
					c.Position = v.position + 1
					continue
				}
				entry["text"], entry["excerpt"] = excerpt, true
			} else if v.size <= 12<<10 && c.Offset == 0 {
				var e Event
				if err := decodeSQLiteRecord(ctx, tx, "session_events", v.key, 12<<10, &e); err != nil {
					return err
				}
				if name == "session_events_read" {
					entry["event"] = e
				} else {
					entry["text"], entry["title"], entry["origin"], entry["consultationId"] = e.Text, e.Title, e.Data["origin"], e.ConsultationID
					if e.Type == "consultation" {
						entry["text"] = e.Text + "\n\nAnswer:\n" + str(e.Data, "answer") + "\nError: " + str(e.Data, "error")
					}
				}
			} else {
				if len(items) > 0 && budget < 24<<10 {
					more = true
					break
				}
				if c.Offset > 0 && c.Digest != "" && c.Digest != v.digest {
					return errors.New("Event changed; restart the exact read.")
				}
				part, err := readSQLiteContent(ctx, tx, "session_events", v.key, v.digest, c.Offset, 16<<10)
				if err != nil {
					return err
				}
				entry["encoding"], entry["fragment"], entry["offset"], entry["digest"] = "base64-json", part.Data, strconv.FormatInt(c.Offset, 10), v.digest
				if part.HasMore {
					entry["nextOffset"] = strconv.FormatInt(part.NextOffset, 10)
					c.Position, c.Offset, c.Digest = v.position, part.NextOffset, v.digest
					items = append(items, entry)
					more = true
					break
				}
			}
			encoded, _ := json.Marshal(entry)
			if len(encoded) > budget && len(items) > 0 {
				more = true
				break
			}
			budget -= len(encoded)
			items = append(items, entry)
			c.Position, c.Offset, c.Digest = v.position+1, 0, ""
			if index == len(refs)-1 && !more {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result["items"], result["truncated"] = items, more
	if more {
		encoded, _ := json.Marshal(c)
		result["nextCursor"] = base64.RawURLEncoding.EncodeToString(encoded)
	}
	if args.SinceRevision != nil {
		result["consumedRevision"] = sqliteRevision(*args.SinceRevision)
		if !more {
			result["consumedRevision"] = c.Revision
		}
	}
	return result, nil
}
func (a *app) exportSQLSession(w http.ResponseWriter, r *http.Request) {
	started := false
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		s, err := st.sessionMeta(r.PathValue("id"))
		if err != nil {
			return err
		}
		if s.ProjectID != "" {
			available, err := st.projectAvailable(s.ProjectID)
			if err != nil {
				return err
			}
			if !available {
				return sql.ErrNoRows
			}
		}
		w.Header().Set("Content-Type", "application/json")
		started = true
		return emitSQLiteDomainRecord(st.ctx, tx, w, sqliteDomainByTable()["sessions"], s.ID)
	})
	if err != nil && !started {
		sqlHTTPError(w, err)
	}
}
