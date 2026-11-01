package main

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type contentDownload struct {
	Client, Session, Domain, Key, Digest string
	Expires                              time.Time
}

type contentDownloadTickets struct {
	sync.Mutex
	items  map[string]contentDownload
	active int
}

func (a *app) createContentDownload(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Ordinal string `json:"ordinal"`
		Domain  string `json:"domain"`
		Key     string `json:"key"`
		Digest  string `json:"digest"`
	}
	if !decode(w, r, &input) {
		return
	}
	d := contentDownload{Client: clientIdentity(r), Session: r.PathValue("id"), Domain: input.Domain, Key: input.Key, Digest: input.Digest, Expires: time.Now().Add(time.Minute)}
	if d.Domain == "" {
		ordinal, err := strconv.ParseInt(input.Ordinal, 10, 64)
		if err != nil || ordinal < 0 {
			fail(w, 400, "Invalid event ordinal.")
			return
		}
		d.Domain, d.Key = "session_events", sqliteOwnedKey(d.Session, strconv.FormatInt(ordinal, 10))
	}
	if !sqliteKnownTable(d.Domain) || len(d.Key) > 4096 || len(d.Digest) != 64 {
		fail(w, 400, "Invalid content reference.")
		return
	}
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error { return checkContentDownload(r.Context(), tx, d) })
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.issueContentDownload(w, d)
}

func (a *app) issueContentDownload(w http.ResponseWriter, d contentDownload) {
	secret, err := authSecret()
	if err != nil {
		fail(w, 503, "Could not create download.")
		return
	}
	a.contentDownloads.Lock()
	if a.contentDownloads.items == nil {
		a.contentDownloads.items = map[string]contentDownload{}
	}
	for key, ticket := range a.contentDownloads.items {
		if !time.Now().Before(ticket.Expires) {
			delete(a.contentDownloads.items, key)
		}
	}
	// Bound outstanding capabilities; repeated clicks must not grow an engine cache.
	if len(a.contentDownloads.items) >= 256 {
		a.contentDownloads.Unlock()
		fail(w, 429, "Too many pending downloads. Retry shortly.")
		return
	}
	a.contentDownloads.items[authHash(secret)] = d
	a.contentDownloads.Unlock()
	respond(w, 200, map[string]string{"path": "/api/content-downloads/" + secret + "?contract=" + sqliteReadContract})
}

func (a *app) createSessionExportDownload(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decode(w, r, &input) {
		return
	}
	d := contentDownload{Client: clientIdentity(r), Session: r.PathValue("id"), Domain: "session_export", Key: r.PathValue("id"), Expires: time.Now().Add(time.Minute)}
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(r.Context(), `SELECT revision FROM data_sessions WHERE id=?`, d.Session).Scan(&d.Digest); err != nil {
			return err
		}
		return checkContentDownload(r.Context(), tx, d)
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.issueContentDownload(w, d)
}

func checkContentDownload(ctx context.Context, tx *sql.Tx, d contentDownload) error {
	st := &sqliteSessionTx{ctx: ctx, tx: tx}
	if d.Domain == "session_export" {
		s, err := st.sessionMeta(d.Session)
		if err != nil {
			return err
		}
		if s.GraphRunID != "" {
			return sql.ErrNoRows
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
		var revision string
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM data_sessions WHERE id=?`, d.Session).Scan(&revision); err != nil {
			return err
		}
		if revision != d.Digest {
			return errSQLiteContentChanged
		}
		return nil
	}
	if d.Domain == "session_events" {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT owner_id FROM data_session_events WHERE id=?`, d.Key).Scan(&owner); err != nil {
			return err
		}
		if owner == d.Session {
			meta, err := st.sessionMeta(owner)
			if err != nil {
				return err
			}
			if meta.GraphRunID != "" {
				return sql.ErrNoRows
			}
			if meta.ProjectID != "" {
				ok, err := st.projectAvailable(meta.ProjectID)
				if err != nil {
					return err
				}
				if !ok {
					return sql.ErrNoRows
				}
			}
		} else if err := st.graphContentOwned(d.Session, d.Domain, d.Key); err != nil {
			return err
		}
	} else if err := st.graphContentOwned(d.Session, d.Domain, d.Key); err != nil {
		return err
	}
	var digest string
	if err := tx.QueryRowContext(ctx, `SELECT digest FROM data_`+d.Domain+` WHERE id=?`, d.Key).Scan(&digest); err != nil {
		return err
	}
	if digest != d.Digest {
		return errSQLiteContentChanged
	}
	return nil
}

func (a *app) consumeContentDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "Use GET to download content.")
		return
	}
	secret := r.PathValue("ticket")
	if len(secret) != 43 {
		fail(w, 404, "Download expired or unavailable.")
		return
	}
	a.contentDownloads.Lock()
	d, ok := a.contentDownloads.items[authHash(secret)]
	delete(a.contentDownloads.items, authHash(secret)) // Single use, including failed ownership/digest checks.
	a.contentDownloads.Unlock()
	if !ok || !time.Now().Before(d.Expires) {
		fail(w, 404, "Download expired or unavailable.")
		return
	}
	request, cleanup, ok := a.clientAuth.admitContentDownload(w, r, d.Client)
	if !ok {
		return
	}
	defer cleanup()
	r = request
	a.contentDownloads.Lock()
	if a.contentDownloads.active >= 2 {
		a.contentDownloads.Unlock()
		fail(w, 429, "Two downloads are already active. Retry when one finishes.")
		return
	}
	a.contentDownloads.active++
	a.contentDownloads.Unlock()
	defer func() { a.contentDownloads.Lock(); a.contentDownloads.active--; a.contentDownloads.Unlock() }()
	tx, err := a.store.readers.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	defer tx.Rollback()
	if err := checkContentDownload(r.Context(), tx, d); err != nil {
		sqlHTTPError(w, err)
		return
	}
	if d.Domain == "session_export" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="conversation.json"`)
		w.Header().Set("Referrer-Policy", "no-referrer")
		stop := context.AfterFunc(r.Context(), func() { _ = http.NewResponseController(w).SetWriteDeadline(time.Now()) })
		defer stop()
		if err := emitSQLiteDomainRecord(r.Context(), tx, contentDownloadWriter{ctx: r.Context(), w: w}, sqliteDomainByTable()["sessions"], d.Session); err != nil {
			return
		}
		_ = tx.Commit()
		return
	}
	var size int64
	if err := tx.QueryRowContext(r.Context(), `SELECT payload_bytes FROM data_`+d.Domain+` WHERE id=?`, d.Key).Scan(&size); err != nil {
		sqlHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="event.json"`)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Referrer-Policy", "no-referrer")
	// A snapshot pins immutable SQL versions for the download, even if a newer
	// event generation is committed concurrently. No JS Blob or full-body buffer.
	stop := context.AfterFunc(r.Context(), func() { _ = http.NewResponseController(w).SetWriteDeadline(time.Now()) })
	defer stop()
	if err := writeSQLiteRecordContent(r.Context(), tx, contentDownloadWriter{ctx: r.Context(), w: w}, d.Domain, d.Key); err != nil {
		return
	}
	_ = tx.Commit()
}

type contentDownloadWriter struct {
	ctx context.Context
	w   http.ResponseWriter
}

func (w contentDownloadWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	// Progress-based write deadline replaces the server's fixed response deadline.
	_ = http.NewResponseController(w.w).SetWriteDeadline(time.Now().Add(30 * time.Second))
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(p)
}

func (s *clientAuth) admitContentDownload(w http.ResponseWriter, r *http.Request, client string) (*http.Request, func(), bool) {
	if s.devOpenAccess && client == "" {
		return r, func() {}, true
	}
	s.mu.Lock()
	valid := false
	for _, c := range s.disk.Clients {
		if c.ID == client && time.Now().Before(c.LastUsedAt.Add(clientIdleLifetime)) {
			valid = true
			break
		}
	}
	if !valid {
		s.mu.Unlock()
		fail(w, 401, "Download client was revoked or expired.")
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(r.Context())
	if s.requests[client] == nil {
		s.requests[client] = map[*http.Request]context.CancelFunc{}
	}
	s.requests[client][r] = cancel
	s.mu.Unlock()
	go func() {
		for {
			s.mu.Lock()
			var expiry time.Time
			for _, c := range s.disk.Clients {
				if c.ID == client {
					expiry = c.LastUsedAt.Add(clientIdleLifetime)
					break
				}
			}
			s.mu.Unlock()
			if !time.Now().Before(expiry) {
				cancel()
				return
			}
			timer := time.NewTimer(time.Until(expiry))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return r.WithContext(ctx), func() {
		cancel()
		s.mu.Lock()
		delete(s.requests[client], r)
		if len(s.requests[client]) == 0 {
			delete(s.requests, client)
		}
		s.mu.Unlock()
	}, true
}
