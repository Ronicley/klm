package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const clientIdleLifetime = 7 * 24 * time.Hour

// Cookies are not port-scoped by browsers. Distinct names prevent dev/release
// pairing from overwriting each other when both use the same loopback host.
const clientCookie = "__Host-klm-client-" + apiPort
const localClientCookie = "klm-loopback-client-" + apiPort

type pairedClient struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Hash       string    `json:"hash"`
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
}

type clientAuthDisk struct {
	Clients []pairedClient `json:"clients"`
}

// Independent, bounded credential storage avoids rewriting application/history
// state for authentication. Each admitted request durably records use; SSE output
// never renews it. A failed write fails closed and leaves the previous state intact.
type clientAuth struct {
	store         *sqliteStore // Non-nil after SQL cutover; no legacy dual-writing.
	mu            sync.Mutex
	devOpenAccess bool // Only a dev-tag build can honor the explicit debug flag.
	dir           string
	disk          clientAuthDisk
	activeAccess  engineAccessSettings
	savedAccess   engineAccessSettings
	pairHash      string
	pairUntil     time.Time
	attemptWindow time.Time
	attempts      int
	origins       map[string]bool
	proxies       []netip.Prefix
	requests      map[string]map[*http.Request]context.CancelFunc
}

func newClientAuth(dir string) (*clientAuth, error) {
	return newClientAuthStorage(dir, nil)
}

func newClientAuthStorage(dir string, store *sqliteStore) (*clientAuth, error) {
	s := &clientAuth{dir: dir, origins: map[string]bool{}, requests: map[string]map[*http.Request]context.CancelFunc{},
		devOpenAccess: developmentBuild && os.Getenv("KLM_DEV_DISABLE_CLIENT_AUTH") == "1", store: store}
	for _, origin := range []string{"http://localhost:7332", "http://127.0.0.1:7332", "http://localhost:17332", "http://127.0.0.1:17332", "http://localhost:5173", "http://127.0.0.1:5173", "http://tauri.localhost", "https://tauri.localhost", "tauri://localhost"} {
		s.origins[origin] = true
	}
	var settings engineAccessSettings
	var err error
	if store != nil {
		settings, err = store.loadEngineAccess(context.Background())
	} else {
		settings, err = loadEngineAccessSettings(dir)
	}
	if err != nil {
		return nil, err
	}
	s.activeAccess, s.savedAccess = settings, settings
	for _, origin := range settings.AllowedOrigins {
		s.origins[origin] = true
	}
	for _, value := range settings.TrustedTLSProxies {
		prefix, _ := netip.ParsePrefix(value) // Validated before publishing the active policy.
		s.proxies = append(s.proxies, prefix)
	}
	if store != nil {
		s.disk, err = store.loadPairedClients(context.Background())
		if err != nil {
			return nil, errors.New("cannot read SQLite client authentication storage")
		}
		return s, nil
	}
	b, err := os.ReadFile(filepath.Join(dir, "client-auth.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, errors.New("cannot read client authentication storage")
	}
	if err := json.Unmarshal(b, &s.disk); err != nil {
		return nil, errors.New("invalid client authentication storage")
	}
	if len(s.disk.Clients) > 128 {
		return nil, errors.New("client authentication storage exceeds capacity")
	}
	return s, nil
}

func authSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func authHash(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

func (s *clientAuth) saveLocked(next clientAuthDisk) error {
	var err error
	if s.store != nil {
		err = s.store.savePairedClients(context.Background(), s.disk, next)
	} else {
		err = writeClientAuthJSON(s.dir, "client-auth.json", next)
	}
	if err != nil {
		return errors.New("cannot persist client authentication")
	}
	s.disk = next
	return nil
}

func (s *clientAuth) saveAccessLocked(next engineAccessSettings) error {
	if s.store != nil {
		return s.store.saveEngineAccess(context.Background(), next)
	}
	return writeClientAuthJSON(s.dir, "engine-access.json", next)
}

func writeClientAuthJSON(dir, filename string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".client-auth-*")
	if err != nil {
		return errors.New("cannot persist client authentication")
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = replaceFile(name, filepath.Join(dir, filename))
	}
	if err != nil {
		return errors.New("cannot persist client authentication")
	}
	return nil
}

func (s *clientAuth) issuePair() (string, error) {
	secret, err := authSecret()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pairHash, s.pairUntil = authHash(secret), time.Now().Add(5*time.Minute)
	return secret, nil
}

func (s *clientAuth) redeem(code, name string) (pairedClient, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stamp := time.Now().UTC()
	if len(code) != 43 || s.pairHash == "" || !stamp.Before(s.pairUntil) || authHash(code) != s.pairHash {
		return pairedClient{}, "", errors.New("Pairing code is invalid or expired.")
	}
	secret, err := authSecret()
	if err != nil {
		return pairedClient{}, "", err
	}
	c := pairedClient{ID: newID(), Name: name, Hash: authHash(secret), CreatedAt: stamp, LastUsedAt: stamp}
	next := clientAuthDisk{Clients: []pairedClient{}}
	for _, old := range s.disk.Clients {
		if stamp.Before(old.LastUsedAt.Add(clientIdleLifetime)) {
			next.Clients = append(next.Clients, old)
		}
	}
	if len(next.Clients) >= 128 {
		return pairedClient{}, "", errors.New("Connected device limit reached. Revoke a device first.")
	}
	next.Clients = append(next.Clients, c)
	if err := s.saveLocked(next); err != nil {
		return pairedClient{}, "", err
	}
	s.pairHash, s.pairUntil = "", time.Time{}
	return c, secret, nil
}

func (s *clientAuth) secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, proxy := range s.proxies {
		if proxy.Contains(ip.Unmap()) && len(r.Header.Values("X-Forwarded-Proto")) == 1 && r.Header.Get("X-Forwarded-Proto") == "https" {
			return true
		}
	}
	return false
}

func (s *clientAuth) transportAllowed(r *http.Request) bool {
	if s.secure(r) {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback() && loopbackHost(r.Host) && r.Header.Get("X-Forwarded-Proto") == "" && r.Header.Get("Forwarded") == ""
}

func (s *clientAuth) setCookie(w http.ResponseWriter, r *http.Request, secret string) {
	secure := s.secure(r)
	name := clientCookie
	if !secure {
		name = localClientCookie
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: secret, Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(clientIdleLifetime.Seconds()), Expires: time.Now().Add(clientIdleLifetime)})
}

type clientIdentityKey struct{}

func clientIdentity(r *http.Request) string {
	id, _ := r.Context().Value(clientIdentityKey{}).(string)
	return id
}

func (s *clientAuth) admit(w http.ResponseWriter, r *http.Request) (*http.Request, func(), bool) {
	secret := ""
	bearer := r.Header.Get("Authorization")
	if strings.HasPrefix(bearer, "Bearer ") {
		secret = strings.TrimPrefix(bearer, "Bearer ")
	}
	fromCookie := bearer == ""
	if fromCookie {
		name := clientCookie
		if !s.secure(r) {
			name = localClientCookie
		}
		if cookie, err := r.Cookie(name); err == nil {
			secret = cookie.Value
		}
	}
	s.mu.Lock()
	stamp := time.Now().UTC()
	index := -1
	if len(secret) == 43 {
		hash := authHash(secret)
		for i, c := range s.disk.Clients {
			if c.Hash == hash && stamp.Before(c.LastUsedAt.Add(clientIdleLifetime)) {
				index = i
				break
			}
		}
	}
	if index < 0 {
		s.mu.Unlock()
		fail(w, 401, "Pair this client to access the engine.")
		return nil, nil, false
	}
	if fromCookie && r.Method != "GET" && r.Method != "HEAD" && (r.Header.Get("Origin") == "" || r.Header.Get("X-KLM-CSRF") != "1") {
		s.mu.Unlock()
		fail(w, 403, "A trusted Origin and CSRF header are required.")
		return nil, nil, false
	}
	next := clientAuthDisk{Clients: append([]pairedClient(nil), s.disk.Clients...)}
	next.Clients[index].LastUsedAt = stamp
	if err := s.saveLocked(next); err != nil {
		s.mu.Unlock()
		fail(w, 503, err.Error())
		return nil, nil, false
	}
	id := next.Clients[index].ID
	ctx, cancel := context.WithCancel(context.WithValue(r.Context(), clientIdentityKey{}, id))
	if s.requests[id] == nil {
		s.requests[id] = map[*http.Request]context.CancelFunc{}
	}
	s.requests[id][r] = cancel
	s.mu.Unlock()
	if fromCookie {
		s.setCookie(w, r, secret)
	}
	// A stream does not renew itself. Check inactivity even while it stays open.
	stopDeadline := func() bool { return true }
	var deadlineDone chan struct{}
	if r.URL.Path == "/api/updates" || strings.HasSuffix(r.URL.Path, "/events") {
		deadlineDone = make(chan struct{})
		stopDeadline = context.AfterFunc(ctx, func() {
			defer close(deadlineDone)
			// Interrupt a blocked SSE write as well as the handler's select loop.
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
		})
		go func() {
			timer := time.NewTimer(time.Until(stamp.Add(clientIdleLifetime)))
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					s.mu.Lock()
					var expiry time.Time
					for _, c := range s.disk.Clients {
						if c.ID == id {
							expiry = c.LastUsedAt.Add(clientIdleLifetime)
							break
						}
					}
					s.mu.Unlock()
					if !time.Now().Before(expiry) {
						cancel()
						return
					}
					timer.Reset(time.Until(expiry))
				}
			}
		}()
	}
	return r.WithContext(ctx), func() {
		if !stopDeadline() {
			<-deadlineDone
		}
		cancel()
		s.mu.Lock()
		delete(s.requests[id], r)
		if len(s.requests[id]) == 0 {
			delete(s.requests, id)
		}
		s.mu.Unlock()
	}, true
}

func (s *clientAuth) revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clientAuthDisk{Clients: []pairedClient{}}
	for _, c := range s.disk.Clients {
		if id != "" && c.ID != id {
			next.Clients = append(next.Clients, c)
		}
	}
	if err := s.saveLocked(next); err != nil {
		return err
	}
	for key, requests := range s.requests {
		if id == "" || key == id {
			for _, cancel := range requests {
				cancel()
			}
		}
	}
	if id == "" {
		s.pairHash, s.pairUntil = "", time.Time{}
	}
	return nil
}

func (a *app) redeemClient(w http.ResponseWriter, r *http.Request) {
	s := a.clientAuth
	s.mu.Lock()
	if time.Since(s.attemptWindow) >= time.Minute {
		s.attemptWindow, s.attempts = time.Now(), 0
	}
	s.attempts++
	limited := s.attempts > 20
	s.mu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "Too many pairing attempts. Retry in one minute.")
		return
	}
	var input struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if !decode(w, r, &input) {
		return
	}
	name, valid := cleanLabel(input.Name, 80)
	if !valid {
		fail(w, 400, "Enter a client name (up to 80 characters).")
		return
	}
	c, secret, err := a.clientAuth.redeem(strings.TrimSpace(input.Code), name)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if r.Header.Get("X-KLM-Native") == "1" {
		respond(w, 200, map[string]string{"clientId": c.ID, "credential": secret})
	} else {
		a.clientAuth.setCookie(w, r, secret)
		respond(w, 200, map[string]string{"clientId": c.ID})
	}
}

func (a *app) connectedClients(w http.ResponseWriter, r *http.Request) {
	type device struct {
		ID         string    `json:"id"`
		Name       string    `json:"name"`
		CreatedAt  time.Time `json:"createdAt"`
		LastUsedAt time.Time `json:"lastUsedAt"`
		Current    bool      `json:"current"`
	}
	devices := []device{}
	a.clientAuth.mu.Lock()
	for _, c := range a.clientAuth.disk.Clients {
		if time.Now().Before(c.LastUsedAt.Add(clientIdleLifetime)) {
			devices = append(devices, device{c.ID, c.Name, c.CreatedAt, c.LastUsedAt, c.ID == clientIdentity(r)})
		}
	}
	a.clientAuth.mu.Unlock()
	respond(w, 200, devices)
}

func (a *app) revokeClient(w http.ResponseWriter, r *http.Request) {
	if err := a.clientAuth.revoke(r.PathValue("clientId")); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, map[string]bool{"revoked": true})
}

func (s *clientAuth) protect(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, pattern := mux.Handler(r)
		// These exact handlers retain independent HMAC/state validation.
		if pattern == "POST /api/webhooks/github/{webhookId}" || pattern == "GET /api/library/mcp/oauth/callback" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !s.devOpenAccess && !s.origins[origin] {
				fail(w, 403, "Origin is not allowed.")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == "OPTIONS" {
			if origin == "" && !s.devOpenAccess {
				fail(w, 403, "Origin is required.")
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-KLM-CSRF, X-KLM-Native, X-KLM-Read-Contract, X-KLM-Engine-ID")
			w.WriteHeader(204)
			return
		}
		if s.devOpenAccess {
			// Public client access only: private harness/Library capabilities and
			// independently validated ingress above retain their own boundaries.
			next.ServeHTTP(w, r)
			return
		}
		if !s.transportAllowed(r) {
			fail(w, 403, "Use HTTPS to connect to the engine.")
			return
		}
		if origin != "" && r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-KLM-CSRF") != "1" {
			fail(w, 403, "CSRF header is required.")
			return
		}
		if pattern == "POST /api/auth/pair" {
			next.ServeHTTP(w, r)
			return
		}
		if pattern == "GET /api/content-downloads/{ticket}" {
			// The handler consumes a short-lived, content-scoped capability and
			// rechecks its owning client. A navigation cannot attach bearer auth.
			next.ServeHTTP(w, r)
			return
		}
		request, cleanup, ok := s.admit(w, r)
		if !ok {
			return
		}
		defer cleanup()
		next.ServeHTTP(w, request)
	})
}
