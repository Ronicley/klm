package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The new central seam: real root wrapper -> private bridge -> native children
// -> consolidated native result -> next parent model request. Local provider only.
func TestDelegationNativeParentSmoke(t *testing.T) {
	if os.Getenv("KLM_RUN_DELEGATION_NATIVE_PROBE") != "1" {
		t.Skip("opt-in disposable native fixture")
	}
	pkg, node := os.Getenv("KLM_DELEGATION_PI_PACKAGE"), os.Getenv("KLM_DELEGATION_NODE")
	if !filepath.IsAbs(pkg) || !filepath.IsAbs(node) {
		t.Fatal("supply exact installed package/node")
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "PROXY") {
			t.Setenv(key, "")
		}
	}
	home, agent := t.TempDir(), t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(key, home)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("KLM_USER_SKILLS_DIR", filepath.Join(home, "skills"))
	t.Setenv("DO_NOT_TRACK", "1")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	var total, roots, leaves atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "fixture-only", 400)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		root := false
		for _, v := range body["tools"].([]any) {
			name := str(object(object(v)["function"]), "name")
			if name == delegationToolName {
				root = true
			}
		}
		sequence := total.Add(1)
		delta := map[string]any{"role": "assistant", "content": "fixture-child-result"}
		finish := "stop"
		if root {
			if roots.Add(1) == 1 {
				request := DelegationRunRequest{OperationID: "native-parent", Children: []DelegationChildRequest{{Key: "one", Task: "Return fixture child output."}, {Key: "two", Task: "Return fixture child output."}}}
				raw, _ := json.Marshal(request)
				delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "fixture-parent-call", "type": "function", "function": map[string]any{"name": delegationToolName, "arguments": string(raw)}}}}
				finish = "tool_calls"
			} else {
				if leaves.Load() != 2 {
					t.Errorf("parent continued before both children: %d", leaves.Load())
				}
				raw, _ := json.Marshal(body["messages"])
				if !strings.Contains(string(raw), "fixture-child-result") || !strings.Contains(string(raw), "childSessionId") {
					t.Error("parent did not receive consolidated child result")
				}
				delta["content"] = "fixture-parent-done"
			}
		} else {
			for _, v := range body["tools"].([]any) {
				if delegationLeafForbiddenGo(str(object(object(v)["function"]), "name")) {
					t.Error("administrative tool leaked into child")
				}
			}
			leaves.Add(1)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for i, part := range []any{delta, map[string]any{}} {
			var reason any
			if i == 1 {
				reason = finish
			}
			chunk, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("fixture-%d", sequence), "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": "fixture", "choices": []any{map[string]any{"index": 0, "delta": part, "finish_reason": reason}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
	models := map[string]any{"providers": map[string]any{"fixture": map[string]any{"baseUrl": server.URL + "/v1", "api": "openai-completions", "apiKey": "fixture-only", "models": []any{map[string]any{"id": "fixture", "name": "Fixture", "reasoning": true, "input": []string{"text"}, "contextWindow": 16000, "maxTokens": 1024, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}}}}}
	raw, _ := json.Marshal(models)
	if err := os.WriteFile(filepath.Join(agent, "models.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	a, parent := sqliteAutomationApp(t)
	t.Cleanup(a.shutdownSessionRuntimes)
	if err := os.MkdirAll(filepath.Join(a.dir, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	b := binary{path: node, args: []string{filepath.Join(pkg, "dist", "cli.js")}, node: node}
	a.binaries["pi"] = b
	project, err := a.store.readProjectMeta(a.ctx, parent.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := CanonicalGraphDirectory(project.Folder)
	if err != nil {
		t.Fatal(err)
	}
	key, err := modelCatalogCacheKey(Session{ProjectID: project.ID, Harness: "pi"}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	a.catalogs = map[string]catalogCache{key: {catalog: &ModelCatalog{DefaultModel: "fixture/fixture", DefaultEffort: "off", Models: []ModelOption{{ID: "fixture/fixture", Efforts: []string{"off"}, DefaultEffort: "off"}}}, expires: time.Now().Add(time.Hour)}}
	ctx, cancel := context.WithTimeout(a.ctx, 35*time.Second)
	defer cancel()
	root := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	a.mu.Lock()
	err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		if err := st.initializeAnalytics(); err != nil {
			return err
		}
		meta, err := st.sessionMeta(parent.ID)
		if err != nil {
			return err
		}
		meta.Harness, meta.Model, meta.Effort, meta.Status = "pi", "fixture/fixture", "off", "running"
		parent = meta
		return st.putSessionMeta(meta)
	})
	a.runs[parent.ID] = root
	a.wg.Add(1)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	go a.execute(root, parent, nativeSession{}, b, cwd, submission{Text: "Run fixture child batch and report its result."})
	select {
	case <-root.done:
	case <-ctx.Done():
		t.Fatal("native parent deadline")
	}
	if roots.Load() != 2 || leaves.Load() != 2 {
		t.Errorf("root/leaf requests=%d/%d", roots.Load(), leaves.Load())
	}
	err = a.store.readSnapshot(context.Background(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: context.Background(), tx: tx}
		meta, err := st.sessionMeta(parent.ID)
		if err != nil {
			return err
		}
		if meta.Status != "idle" {
			keys, _ := sqliteQueryIDs(context.Background(), tx, `SELECT id FROM data_session_events WHERE owner_id=? AND kind='error' ORDER BY position`, parent.ID)
			for _, key := range keys {
				var e Event
				_ = decodeSQLiteRecord(context.Background(), tx, "session_events", key, 1<<20, &e)
				t.Log(e.Text)
			}
			return fmt.Errorf("native parent status %s", meta.Status)
		}
		batch, err := st.existingDelegation(parent.ID, "native-parent")
		if err != nil {
			return err
		}
		if batch == nil || batch.Delivery != DelegationDeliveryDelivered {
			return fmt.Errorf("native delivery not confirmed: %+v", batch)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("real root wrapper/bridge/two native children/consolidated next iteration: %d local requests", total.Load())
}
