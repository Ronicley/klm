package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLibraryGatewayRoleSeparation(t *testing.T) {
	p := &adapter{turn: &turn{ctx: context.Background()}, mcp: &mcpExecution{tools: []map[string]any{{"name": "ext_fixture", "external": true, "inputSchema": map[string]any{"type": "object"}}}, routes: map[string]mcpRoute{"ext_fixture": {}}}}
	b, err := newLinkedBridge(p, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	call := func(path, token, method, name string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "fixture", "method": method, "params": map[string]any{"name": name, "arguments": map[string]any{}}})
		r := httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader(string(data)))
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		b.serve(w, r)
		return w
	}
	for _, scope := range []struct {
		path, token string
		library     bool
	}{{"/mcp", b.token, false}, {"/library", b.libraryToken, true}} {
		response := call(scope.path, scope.token, "tools/list", "")
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		var body struct {
			Result struct {
				Tools []map[string]any `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, tool := range body.Result.Tools {
			if truth(tool, "external") != scope.library {
				t.Fatal("role and Library catalogs were mixed")
			}
		}
		if len(body.Result.Tools) == 0 {
			t.Fatal("expected fixture catalog")
		}
	}
	for _, response := range []*httptest.ResponseRecorder{call("/mcp", b.token, "tools/call", "ext_fixture"), call("/library", b.libraryToken, "tools/call", "linked_discover"), call("/library", b.token, "tools/list", "")} {
		if response.Code != 403 {
			t.Fatalf("cross-scope capability was accepted: %d", response.Code)
		}
	}
	if response := call("/library", b.libraryToken, "klm/graph/gate", ""); !strings.Contains(response.Body.String(), "-32601") {
		t.Fatal("Library token reached a privileged transport operation")
	}
}
