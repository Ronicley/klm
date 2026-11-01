package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestContentDownloadTickets(t *testing.T) {
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "")
	store, _ := sqliteSessionStore(t, 0)
	a := &app{store: store}
	var err error
	a.clientAuth, err = newClientAuth(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client, secret := authTestCredential(t, a.clientAuth)
	_, err = store.sessionTransaction(context.Background(), func(st *sqliteSessionTx) error {
		_, err := st.appendEvent("general", Event{ID: "download-test", Type: "assistant", Text: "complete content", CreatedAt: now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var digest string
	if err := store.readers.QueryRow(`SELECT digest FROM data_session_events WHERE owner_id='general' AND position=0`).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	h := a.routes()
	issue := func() string {
		t.Helper()
		r := authTestRequest("POST", "/api/sessions/general/content-downloads", secret)
		r.Header.Set("X-KLM-Read-Contract", "3")
		r.Body = io.NopCloser(strings.NewReader(`{"ordinal":"0","digest":"` + digest + `"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("ticket: %d %s", w.Code, w.Body.String())
		}
		var result struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Path
	}
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := authTestRequest("GET", path, "") // Navigation carries no bearer header.
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := issue()
	w := get(path)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(w.Body.String(), `"text":"complete content"`) {
		t.Fatalf("attachment: %d %s", w.Code, w.Body.String())
	}
	if w := get(path); w.Code != 404 {
		t.Fatal("ticket was reusable", w.Code)
	}
	exportRequest := authTestRequest("POST", "/api/sessions/general/export-downloads", secret)
	exportRequest.Header.Set("X-KLM-Read-Contract", "3")
	exportReply := httptest.NewRecorder()
	h.ServeHTTP(exportReply, exportRequest)
	if exportReply.Code != 200 {
		t.Fatalf("export ticket: %d %s", exportReply.Code, exportReply.Body.String())
	}
	var exportTicket struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(exportReply.Body.Bytes(), &exportTicket); err != nil {
		t.Fatal(err)
	}
	exported := get(exportTicket.Path)
	if exported.Code != 200 || !strings.Contains(exported.Body.String(), `"events":[`) || !strings.Contains(exported.Body.String(), "complete content") {
		t.Fatalf("streamed conversation export: %d %s", exported.Code, exported.Body.String())
	}
	path = issue()
	a.contentDownloads.Lock()
	for key, item := range a.contentDownloads.items {
		item.Expires = time.Now().Add(-time.Second)
		a.contentDownloads.items[key] = item
	}
	a.contentDownloads.Unlock()
	if w := get(path); w.Code != 404 {
		t.Fatal("expired ticket accepted", w.Code)
	}
	path = issue()
	if err := a.clientAuth.revoke(client.ID); err != nil {
		t.Fatal(err)
	}
	if w := get(path); w.Code != 401 {
		t.Fatal("revoked client ticket accepted", w.Code)
	}
}
