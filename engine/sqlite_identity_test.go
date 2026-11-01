package main

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

func TestSQLiteClientIdentityBinding(t *testing.T) {
	if !developmentBuild {
		t.Skip("dev auth fixture")
	}
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "1")
	a, s := sqliteAutomationApp(t)
	var err error
	a.clientAuth, err = newClientAuthStorage(a.dir, a.store)
	if err != nil {
		t.Fatal(err)
	}
	var identity string
	if err := a.store.readers.QueryRow(`SELECT value FROM store_meta WHERE key='database_id'`).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	handler := a.routes()
	for _, c := range []struct {
		method, path, identity string
		status                 int
	}{
		{"POST", "/api/projects", "different-database", 409},
		{"GET", "/api/state", "different-database", 200},
		{"GET", "/api/sessions/" + s.ID, identity, 200},
	} {
		req := httptest.NewRequest(c.method, "http://localhost"+c.path, bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-KLM-Read-Contract", "3")
		req.Header.Set("X-KLM-Engine-ID", c.identity)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != c.status {
			t.Fatalf("%s %s: %d %s", c.method, c.path, res.Code, res.Body.String())
		}
	}
}
