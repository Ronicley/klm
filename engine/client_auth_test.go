package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func authTestApp(t *testing.T) *app {
	t.Helper()
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "")
	t.Setenv("KLM_ALLOWED_ORIGINS", "https://klm.codemob.com.br")
	t.Setenv("KLM_TRUSTED_TLS_PROXIES", "127.0.0.1/32")
	dir := t.TempDir()
	s, err := newClientAuth(dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &app{dir: dir, clientAuth: s, state: d, ctx: context.Background()}
}

func TestClientAuthDevFlagOnlyWorksInDevBuild(t *testing.T) {
	a := authTestApp(t)
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "1")
	var err error
	a.clientAuth, err = newClientAuth(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.clientAuth.devOpenAccess != developmentBuild {
		t.Fatal("debug flag escaped its build-tag gate")
	}
	calls := 0
	a.restartEngine = func() error { calls++; return nil } // Never restart an actual process.
	for _, test := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/auth/session", "", 200},
		{"GET", "/api/state", "", 200},
		{"OPTIONS", "/api/state", "", 204},
		{"POST", "/api/engine/restart", `{"confirmInterrupt":true}`, 202},
	} {
		r := authTestRequest(test.method, test.path, "")
		r.Host = "192.168.1.20:17331"
		r.RemoteAddr = "192.168.1.30:5555"
		r.Header.Set("Origin", "http://192.168.1.30:7332")
		r.Body = io.NopCloser(strings.NewReader(test.body))
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		want := test.want
		if !developmentBuild {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("%s %s: got %d, want %d", test.method, test.path, w.Code, want)
		}
		if developmentBuild && (w.Header().Get("Access-Control-Allow-Origin") != r.Header.Get("Origin") || w.Header().Get("Access-Control-Allow-Credentials") != "true") {
			t.Fatal("open dev client cannot use credentialed browser transport")
		}
	}
	if developmentBuild && calls != 1 || !developmentBuild && calls != 0 {
		t.Fatal("wrong build admitted restart without authentication")
	}
}

func authTestCredential(t *testing.T, s *clientAuth) (pairedClient, string) {
	t.Helper()
	code, err := s.issuePair()
	if err != nil {
		t.Fatal(err)
	}
	c, secret, err := s.redeem(code, "Test client")
	if err != nil {
		t.Fatal(err)
	}
	return c, secret
}

func authTestRequest(method, path, credential string) *http.Request {
	r := httptest.NewRequest(method, "http://localhost:7331"+path, strings.NewReader("{}"))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	if credential != "" {
		r.Header.Set("Authorization", "Bearer "+credential)
	}
	return r
}

func TestClientAuthBoundary(t *testing.T) {
	a := authTestApp(t)
	h := a.routes()
	_, secret := authTestCredential(t, a.clientAuth)
	for _, path := range []string{"/api/state", "/api/health", "/api/version", "/api/updates", "/api/sessions/x/export", "/api/library", "/api/projects", "/api/transcriptions"} {
		method := "GET"
		if path == "/api/projects" || path == "/api/transcriptions" {
			method = "POST"
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authTestRequest(method, path, ""))
		if w.Code != 401 {
			t.Errorf("unauthenticated %s %s: %d", method, path, w.Code)
		}
	}
	for _, tc := range []struct {
		origin, csrf          string
		cookie, remote, proxy bool
		want                  int
	}{
		{want: 200},
		{origin: "https://klm.codemob.com.br", want: 200},
		{origin: "https://evil.codemob.com.br", want: 403},
		{origin: "null", want: 403},
		{remote: true, want: 403},
		{proxy: true, want: 200},
	} {
		r := authTestRequest("GET", "/api/auth/session", secret)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		if tc.remote {
			r.RemoteAddr = "192.168.1.2:4444"
			r.Header.Set("X-Forwarded-Proto", "https")
		}
		if tc.proxy {
			r.Host = "klmengine.codemob.com.br"
			r.Header.Set("X-Forwarded-Proto", "https")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("origin/proxy %+v: %d %s", tc, w.Code, w.Body.String())
		}
		if tc.origin == "https://klm.codemob.com.br" && (w.Header().Get("Access-Control-Allow-Origin") != tc.origin || w.Header().Get("Access-Control-Allow-Credentials") != "true") {
			t.Fatal("missing credentialed exact-origin CORS")
		}
	}
	for _, tc := range []struct {
		origin, csrf string
		want         int
	}{
		{"", "", 403}, {"https://klm.codemob.com.br", "", 403}, {"https://klm.codemob.com.br", "1", 200},
	} {
		r := authTestRequest("DELETE", "/api/auth/devices/nonexistent", "")
		r.AddCookie(&http.Cookie{Name: localClientCookie, Value: secret})
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-KLM-CSRF", tc.csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("CSRF %+v: %d", tc, w.Code)
		}
	}
	r := authTestRequest("OPTIONS", "/api/projects", "")
	r.Header.Set("Origin", "https://klm.codemob.com.br")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("preflight requires credentials")
	}
	if len(a.state.Projects) != 0 {
		t.Fatal("preflight executed operation")
	}
}

func TestClientAuthPairingAtomicAndDurable(t *testing.T) {
	a := authTestApp(t)
	s := a.clientAuth
	code, _ := s.issuePair()
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.redeem(code, "Concurrent"); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || len(s.disk.Clients) != 1 {
		t.Fatal("pairing code consumed more than once")
	}
	code, _ = s.issuePair()
	s.pairUntil = time.Now().Add(-time.Second)
	if _, _, err := s.redeem(code, "Expired"); err == nil {
		t.Fatal("expired code accepted")
	}
	code, _ = s.issuePair()
	dir := s.dir
	s.dir = filepath.Join(dir, "missing")
	if _, _, err := s.redeem(code, "Failed write"); err == nil {
		t.Fatal("persistence failure reported success")
	}
	s.dir = dir
	if _, _, err := s.redeem(code, "Retry"); err != nil {
		t.Fatal("failed persistence consumed the code")
	}
	b, err := os.ReadFile(filepath.Join(dir, "client-auth.json"))
	if err != nil || strings.Contains(string(b), code) {
		t.Fatal("raw pairing code persisted")
	}
	code, _ = s.issuePair()
	r := authTestRequest("POST", "/api/auth/pair", "")
	r.Host = "klmengine.codemob.com.br"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("Origin", "https://klm.codemob.com.br")
	r.Header.Set("X-KLM-CSRF", "1")
	r.Body = io.NopCloser(strings.NewReader(`{"code":"` + code + `","name":"Browser"}`))
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || strings.Contains(w.Body.String(), "credential") {
		t.Fatalf("browser pairing did not use cookie-only transport: %d", w.Code)
	}
	s.attempts = 0
	for range 21 {
		r := authTestRequest("POST", "/api/auth/pair", "")
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if s.attempts == 21 && w.Code != 429 {
			t.Fatal("pairing attempts are not bounded")
		}
	}
}

func TestClientAuthSlidingRestartAndCookie(t *testing.T) {
	a := authTestApp(t)
	c, secret := authTestCredential(t, a.clientAuth)
	s := a.clientAuth
	// Old creation time is deliberately irrelevant; there is no absolute expiry.
	s.disk.Clients[0].CreatedAt = time.Now().Add(-365 * 24 * time.Hour)
	s.disk.Clients[0].LastUsedAt = time.Now().Add(-clientIdleLifetime + time.Hour)
	if err := s.saveLocked(s.disk); err != nil {
		t.Fatal(err)
	}
	r := authTestRequest("GET", "/api/auth/session", "")
	r.Host = "klmengine.codemob.com.br"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.AddCookie(&http.Cookie{Name: clientCookie, Value: secret})
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("active old credential rejected: %d", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != clientCookie || cookies[0].Domain != "" || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge != 604800 {
		t.Fatal("incorrect secure sliding cookie")
	}
	reloaded, err := newClientAuth(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.disk.Clients[0].ID != c.ID || time.Since(reloaded.disk.Clients[0].LastUsedAt) > time.Minute {
		t.Fatal("sliding use lost on restart")
	}
	b, _ := os.ReadFile(filepath.Join(s.dir, "client-auth.json"))
	if strings.Contains(string(b), secret) {
		t.Fatal("raw credential persisted")
	}
	reloaded.disk.Clients[0].LastUsedAt = time.Now().Add(-clientIdleLifetime - time.Second)
	if err := reloaded.saveLocked(reloaded.disk); err != nil {
		t.Fatal(err)
	}
	a.clientAuth, err = newClientAuth(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, authTestRequest("GET", "/api/auth/session", secret))
	if w.Code != 401 {
		t.Fatal("expired credential revived after restart")
	}
}

func TestClientAuthRevocationClosesStream(t *testing.T) {
	a := authTestApp(t)
	c, secret := authTestCredential(t, a.clientAuth)
	server := httptest.NewServer(a.routes())
	defer server.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	r, _ := http.NewRequest("GET", server.URL+"/api/updates", nil)
	r.Header.Set("Authorization", "Bearer "+secret)
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("stream: %d", response.StatusCode)
	}
	if err := a.clientAuth.revoke(c.ID); err != nil {
		t.Fatal(err)
	}
	// Immediate revocation may interrupt chunk framing rather than send a final
	// chunk. Both EOF forms prove closure; a timeout would fail this check.
	if _, err := io.ReadAll(response.Body); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("revoked stream failed to close: %v", err)
	}
	reloaded, err := newClientAuth(a.dir)
	if err != nil || len(reloaded.disk.Clients) != 0 {
		t.Fatal("revocation not durable")
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, authTestRequest("GET", "/api/state", secret))
	if w.Code != 401 {
		t.Fatal("revoked request admitted")
	}
}

func TestClientAuthSignedIngressAndOAuthException(t *testing.T) {
	a := authTestApp(t)
	id, projectID := newID(), newID()
	a.state.Projects = []Project{{ID: projectID, Name: "Test", Folder: a.dir, Folders: []string{}}}
	a.state.WebhookConnections[id] = WebhookConnection{ID: id, ProjectID: projectID, Provider: "github", Name: "Test", Enabled: true, Secret: "test-secret-not-a-real-credential", Retention: "1d", CreatedAt: now(), UpdatedAt: now()}
	// Use the actual signature-validation handler through the protected router.
	payload := `{"zen":"auth boundary test"}`
	mac := hmac.New(sha256.New, []byte(a.state.WebhookConnections[id].Secret))
	_, _ = mac.Write([]byte(payload))
	r := authTestRequest("POST", "/api/webhooks/github/"+id, "")
	r.Body = io.NopCloser(strings.NewReader(payload))
	r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	r.Header.Set("X-GitHub-Event", "ping")
	r.Header.Set("X-GitHub-Delivery", "auth-test")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatalf("signed ingress rejected: %d %s", w.Code, w.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if result["accepted"] != true {
		t.Fatal("signed ingress not durably accepted")
	}
	r = authTestRequest("POST", "/api/webhooks/github/"+id, "")
	r.Header.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "signature") {
		t.Fatal("unsigned ingress bypassed signature validation")
	}
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, authTestRequest("GET", "/api/library/mcp/oauth/callback?state=invalid", ""))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "OAuth") {
		t.Fatal("OAuth callback did not retain state validation")
	}
}
