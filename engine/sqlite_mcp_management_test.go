package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSQLiteMCPManagementAndDisconnect(t *testing.T) {
	if !developmentBuild {
		t.Skip("dev-only auth bypass")
	}
	t.Setenv("KLM_DEV_DISABLE_CLIENT_AUTH", "1")
	a, s := sqliteAutomationApp(t)
	var err error
	a.clientAuth, err = newClientAuthStorage(a.dir, a.store)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fixture unavailable", http.StatusNotFound)
	}))
	defer server.Close()
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
		if strings.Contains(res.Body.String(), "synthetic-private-bearer") {
			t.Fatal("upstream secret leaked in public response")
		}
		var value map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	created := call("POST", "/api/library/mcp", map[string]any{"name": "Fixture server", "config": MCPConnectionConfig{Transport: "http", URL: server.URL, Auth: "manual"}, "secrets": map[string]string{"bearer": "synthetic-private-bearer"}}, 200)
	id := created["id"].(string)
	if !created["credentialsConfigured"].(bool) {
		t.Fatal("manual credential was not attached")
	}
	call("GET", "/api/library/mcp/"+id, nil, 200)
	listed := call("GET", "/api/library?limit=1", nil, 200)
	if len(listed["connections"].([]any)) != 1 {
		t.Fatal("library omitted connection")
	}
	tested := call("POST", "/api/library/mcp/"+id+"/test", map[string]any{}, 200)
	if tested["lastTestError"] == "" {
		t.Fatal("failed owned endpoint probe was not persisted")
	}
	call("POST", "/api/library/mcp/"+id+"/oauth", map[string]any{}, 400)
	oldCredential := newID()
	blob, err := protectCredential(mcpCredential{Bearer: "synthetic-old-capture"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.sessionTransaction(a.ctx, func(st *sqliteSessionTx) error {
		if err := st.putRoot("mcp_credentials", oldCredential, blob); err != nil {
			return err
		}
		return st.putSingleChild("capability_applied", sqliteOwnedKey(s.ID, ""), &CapabilitySnapshot{Version: 1, Connections: []MCPConnectionSnapshot{{ID: id, CredentialID: oldCredential}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	disconnected := call("POST", "/api/library/mcp/"+id+"/disconnect", map[string]any{}, 200)
	if disconnected["credentialsConfigured"].(bool) {
		t.Fatal("disconnect left a current credential")
	}
	var count int
	if err := a.store.readers.QueryRow(`SELECT count(*) FROM data_mcp_credentials`).Scan(&count); err != nil || count != 0 {
		t.Fatal("disconnect left current/captured credentials", count, err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://localhost/api/library/mcp/oauth/callback?state=invalid", nil))
	if response.Code != 400 {
		t.Fatal("OAuth callback was blocked by the client read contract", response.Code)
	}
	if len(a.state.MCPConnections) != 0 || a.storageErr != nil {
		t.Fatal("legacy fallback", a.storageErr)
	}
}
