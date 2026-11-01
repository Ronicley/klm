package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngineAccessSettingsPersistUntilRestart(t *testing.T) {
	a := authTestApp(t)
	_, credential := authTestCredential(t, a.clientAuth)
	send := func(body string) *httptest.ResponseRecorder {
		r := authTestRequest("PATCH", "/api/settings/access", credential)
		r.Body = io.NopCloser(strings.NewReader(body))
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		return w
	}
	if w := send(`{"allowedOrigins":["https://*.example.com"],"trustedTlsProxies":[]}`); w.Code != 400 {
		t.Fatalf("wildcard accepted: %d", w.Code)
	}
	if w := send(`{"allowedOrigins":[],"trustedTlsProxies":["192.168.10.200"]}`); w.Code != 400 {
		t.Fatalf("non-CIDR accepted: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(a.dir, "engine-access.json")); !os.IsNotExist(err) {
		t.Fatal("invalid settings persisted")
	}
	w := send(`{"allowedOrigins":["https://new.example.com"],"trustedTlsProxies":["192.168.10.200/32"]}`)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var view engineAccessSettingsView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.RestartRequired || view.Restarting {
		t.Fatal("saved settings did not require restart")
	}
	if a.clientAuth.origins["https://new.example.com"] || !a.clientAuth.origins["https://klm.codemob.com.br"] {
		t.Fatal("save changed the live origin policy")
	}
	r := authTestRequest("GET", "/api/auth/session", credential)
	r.RemoteAddr = "192.168.10.200:4567"
	r.Header.Set("X-Forwarded-Proto", "https")
	if a.clientAuth.secure(r) {
		t.Fatal("save changed the live proxy policy")
	}
	t.Setenv("KLM_ALLOWED_ORIGINS", "https://environment.example.com")
	reloaded, err := newClientAuth(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.origins["https://new.example.com"] || reloaded.origins["https://environment.example.com"] || !reloaded.secure(r) {
		t.Fatal("restart did not apply the saved policy over environment")
	}
	if reloaded.disk.Clients[0].Hash != a.clientAuth.disk.Clients[0].Hash {
		t.Fatal("settings changed client credentials")
	}
	a.clientAuth = reloaded
	if a.engineAccessSettingsView().RestartRequired {
		t.Fatal("restart-required flag did not clear")
	}
}

func TestEngineAccessRestartRequiresAuthenticatedConfirmation(t *testing.T) {
	a := authTestApp(t)
	_, credential := authTestCredential(t, a.clientAuth)
	calls := 0
	a.restartEngine = func() error { calls++; return nil }
	send := func(secret, body string) *httptest.ResponseRecorder {
		r := authTestRequest("POST", "/api/engine/restart", secret)
		r.Body = io.NopCloser(strings.NewReader(body))
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		return w
	}
	if w := send("", `{"confirmInterrupt":true}`); w.Code != 401 || calls != 0 {
		t.Fatal("unauthenticated restart admitted")
	}
	if w := send(credential, `{}`); w.Code != 400 || calls != 0 {
		t.Fatal("unconfirmed restart admitted")
	}
	r := authTestRequest("POST", "/api/engine/restart", "")
	r.Header.Set("Origin", "https://klm.codemob.com.br")
	r.AddCookie(&http.Cookie{Name: localClientCookie, Value: credential})
	r.Body = io.NopCloser(strings.NewReader(`{"confirmInterrupt":true}`))
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatal("cookie restart without CSRF admitted")
	}
	if w := send(credential, `{"confirmInterrupt":true}`); w.Code != 202 || calls != 1 {
		t.Fatalf("restart not admitted: %d", w.Code)
	}
	if w := send(credential, `{"confirmInterrupt":true}`); w.Code != 409 || calls != 1 {
		t.Fatal("duplicate restart dispatched")
	}
	if !a.engineAccessSettingsView().Restarting {
		t.Fatal("restart status missing")
	}
	a.restarting = false
	a.restartEngine = func() error { return errors.New("test launch failure") }
	if w := send(credential, `{"confirmInterrupt":true}`); w.Code != 503 || a.restarting {
		t.Fatal("failed launch reported as restarting")
	}
}
