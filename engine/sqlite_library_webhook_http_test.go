package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSQLiteLibraryDeletionAndWebhookHTTP(t *testing.T) {
	if !developmentBuild {
		t.Skip("dev auth bypass fixture")
	}
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "1")
	a, s := sqliteAutomationApp(t)
	var err error
	a.clientAuth, err = newClientAuthStorage(a.dir, a.store)
	if err != nil {
		t.Fatal(err)
	}
	connection := MCPConnection{ID: newID(), Name: "Fixture", Revision: "revision", Config: MCPConnectionConfig{Transport: "http", Auth: "none", URL: "http://127.0.0.1:1"}}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("mcp_connections", connection.ID, connection); err != nil {
			return err
		}
		return st.putSingleChild("capability_selections", sqliteOwnedKey(s.ID, ""), CapabilitySelection{MCPConnections: []MCPSelection{{ConnectionID: connection.ID}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := a.routes()
	call := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		encoded, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-KLM-Read-Contract", sqliteReadContract)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, res.Code, res.Body.String())
		}
		var value map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	path := "/api/library/mcp/" + connection.ID
	preview := call("GET", path+"/deletion?limit=1", nil, 200)
	if len(preview["references"].([]any)) != 1 {
		t.Fatal("selection reference missing")
	}
	call("DELETE", path, map[string]any{"revision": preview["revision"]}, 200)
	var desired CapabilitySelection
	if err := decodeSQLiteRecord(a.ctx, a.store.readers, "capability_selections", sqliteOwnedKey(sqliteOwnedKey(s.ID, ""), ""), 1<<20, &desired); err != nil || len(desired.MCPConnections) != 0 {
		t.Fatal("deletion did not clear future selection", err)
	}
	secret := "synthetic-webhook-secret-32-bytes"
	webhook := call("POST", "/api/projects/"+s.ProjectID+"/webhooks", map[string]string{"provider": "github", "name": "Fixture", "secret": secret, "retention": "forever"}, 201)
	id := webhook["id"].(string)
	post := func(want int) string {
		t.Helper()
		payload := []byte(`{"zen":"signed fixture"}`)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(payload)
		req := httptest.NewRequest("POST", "http://localhost/api/webhooks/github/"+id, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Delivery", "fixture-once")
		req.Header.Set("X-GitHub-Event", "ping")
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatal("ingress", res.Code, res.Body.String())
		}
		var receipt sqliteWebhookReceipt
		if err := json.Unmarshal(res.Body.Bytes(), &receipt); err != nil {
			t.Fatal(err)
		}
		return receipt.EventID
	}
	eventID := post(202)
	if post(200) != eventID {
		t.Fatal("duplicate webhook created another event")
	}
	if _, _, err := a.store.releaseWebhookBatch(a.ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	base := "/api/projects/" + s.ProjectID + "/webhooks/" + id
	call("GET", base+"/events", nil, 200)
	detail := call("GET", base+"/events/"+eventID, nil, 200)
	if detail["payload"] != `{"zen":"signed fixture"}` {
		t.Fatal("raw payload changed", detail)
	}
	releases := call("GET", "/api/projects/"+s.ProjectID+"/webhook-events?after=0", nil, 200)
	if len(releases["items"].([]any)) != 1 {
		t.Fatal("release missing", releases)
	}
	call("PATCH", base, map[string]bool{"enabled": false}, 200)
	call("DELETE", base, nil, 200)
	var count int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_webhook_events`).Scan(&count); err != nil || count != 0 {
		t.Fatal("connection delete left retained events", err)
	}
	if a.storageErr != nil || len(a.state.WebhookEvents) != 0 {
		t.Fatal("legacy fallback", a.storageErr)
	}
}
