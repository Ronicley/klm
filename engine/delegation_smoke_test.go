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

// Opt-in central smoke: real installed Pi adapters, fixture-only parent actor,
// temporary schema3 SQLite, deterministic loopback provider; no real credentials.
func TestDelegationNativeBatchSmoke(t *testing.T) {
	if os.Getenv("KLM_RUN_DELEGATION_NATIVE_PROBE") != "1" {
		t.Skip("owned native delegation fixture is opt-in")
	}
	pkg, node := os.Getenv("KLM_DELEGATION_PI_PACKAGE"), os.Getenv("KLM_DELEGATION_NODE")
	if !filepath.IsAbs(pkg) || !filepath.IsAbs(node) {
		t.Fatal("supply exact installed Pi package and Node executable")
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "PROXY") {
			t.Setenv(key, "")
		}
	}
	t.Setenv("DO_NOT_TRACK", "1")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	home, agent := t.TempDir(), t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(key, home)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	arrived := make(chan int, 8)
	release := make([]chan struct{}, 8)
	for index := range release {
		release[index] = make(chan struct{})
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			http.Error(w, "fixture-only endpoint", 400)
			return
		}
		if value := r.Header.Get("Authorization"); value != "" && value != "Bearer fixture-only" {
			t.Error("real credential reached fixture")
			http.Error(w, "credential", 403)
			return
		}
		var body struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		for _, tool := range body.Tools {
			if delegationLeafForbiddenGo(tool.Function.Name) {
				t.Error("parent/native delegation leaked into leaf", tool.Function.Name)
			}
		}
		sequence := int(requests.Add(1))
		if sequence > len(release) {
			http.Error(w, "too many fixture requests", 500)
			return
		}
		arrived <- sequence
		select {
		case <-release[sequence-1]:
		case <-r.Context().Done():
			return
		case <-time.After(15 * time.Second):
			http.Error(w, "fixture response deadline", 504)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, delta := range []any{map[string]any{"role": "assistant", "content": fmt.Sprintf("fixture-output-%d", sequence)}, map[string]any{}} {
			finish := any(nil)
			if len(delta.(map[string]any)) == 0 {
				finish = "stop"
			}
			chunk, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("fixture-%d", sequence), "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": "fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
	model := map[string]any{"providers": map[string]any{"fixture": map[string]any{"baseUrl": server.URL + "/v1", "api": "openai-completions", "apiKey": "fixture-only", "models": []any{map[string]any{"id": "fixture", "name": "Fixture", "reasoning": true, "input": []string{"text"}, "contextWindow": 16000, "maxTokens": 1024, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}}}}}
	raw, _ := json.Marshal(model)
	if err := os.WriteFile(filepath.Join(agent, "models.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	a, parent := sqliteAutomationApp(t)
	if err := os.MkdirAll(filepath.Join(a.dir, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	a.binaries["pi"] = binary{path: node, args: []string{filepath.Join(pkg, "dist", "cli.js")}, node: node}
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
	ctx, cancel := context.WithTimeout(a.ctx, 70*time.Second)
	defer cancel()
	rootTurn := &turn{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	a.runs[parent.ID] = rootTurn
	p := &adapter{app: a, id: parent.ID, turn: rootTurn, harness: "pi", model: "fixture/fixture", effort: "off", cwd: cwd}
	a.mu.Lock()
	err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		if err := st.initializeAnalytics(); err != nil {
			return err
		}
		meta, err := st.sessionMeta(parent.ID)
		if err != nil {
			return err
		}
		meta.Status = "running"
		if err := st.putSessionMeta(meta); err != nil {
			return err
		}
		return st.putRoot("native_sessions", parent.ID, nativeSession{ID: "fixture-native-parent", Path: filepath.Join(a.dir, "sessions", parent.ID+".jsonl")})
	})
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var controller *delegationExecution
	controller, err = newDelegationExecution(p, func(ctx context.Context, item DelegationInvocationRecord) delegationChildOutcome {
		return a.executeDelegationChild(ctx, controller, item)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = controller.close() })
	awaitArrivals := func(batchID string) {
		t.Helper()
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		for count := 0; count < 2; count++ {
			select {
			case <-arrived:
			case <-timer.C:
				_ = a.store.readSnapshot(context.Background(), func(tx *sql.Tx) error {
					items, err := (&sqliteSessionTx{ctx: context.Background(), tx: tx}).delegationMembers(batchID)
					if err == nil {
						for _, item := range items {
							t.Logf("child %s state=%s error=%s", item.Key, item.State, delegationExcerpt(item.Error, 512))
						}
					}
					return err
				})
				t.Fatal("fixture provider arrival deadline")
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	prepare := func(operation, call string, children []DelegationChildRequest) DelegationAdmissionReceipt {
		t.Helper()
		reply, err := controller.prepare(DelegationRunRequest{OperationID: operation, Children: children}, call, "")
		if err != nil {
			t.Fatal(err)
		}
		for reply.Preparing {
			reply, err = controller.prepared(ctx, reply.PreparationID, call, "")
			if err != nil {
				t.Fatal(err)
			}
		}
		if reply.Receipt == nil {
			t.Fatal("no durable receipt")
		}
		return *reply.Receipt
	}
	waitResult := func(receipt DelegationAdmissionReceipt, call string) DelegationBatchResult {
		t.Helper()
		revision := ""
		for {
			reply, err := controller.wait(ctx, receipt.BatchID, revision, call, "")
			if err != nil {
				t.Fatal(err)
			}
			if reply.State == "settled" {
				return *reply.Result
			}
			if reply.StopRequired {
				t.Fatal("unexpected unconfirmed child finality")
			}
			revision = reply.Revision
		}
	}
	first := prepare("first", "call-one", []DelegationChildRequest{{Key: "one", Task: "Return fixture output."}, {Key: "two", Task: "Return fixture output."}})
	awaitArrivals(first.BatchID)
	var members []DelegationInvocationRecord
	err = a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		members, err = st.delegationMembers(first.BatchID)
		if err != nil {
			return err
		}
		batch, err := st.delegationBatch(first.BatchID)
		if err == nil && batch.State == DelegationSettled {
			return fmt.Errorf("batch settled before either fixture response")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	close(release[0])
	close(release[1])
	result := waitResult(first, "call-one")
	if len(result.Children) != 2 || result.IsError || !result.CanContinue {
		t.Fatal("bad two-child join", result)
	}
	if err := controller.resultSeen(first.BatchID, "call-one", ""); err != nil {
		t.Fatal(err)
	}
	nativeIDs := map[string]string{}
	err = a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		for _, item := range members {
			native, err := st.native(item.ChildSessionID)
			if err != nil {
				return err
			}
			nativeIDs[item.ChildSessionID] = native.ID
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reused := prepare("reuse", "call-two", []DelegationChildRequest{{Key: "one", Task: "Return a new fixture output.", ChildSessionID: members[0].ChildSessionID}, {Key: "two", Task: "Return a new fixture output.", ChildSessionID: members[1].ChildSessionID}})
	awaitArrivals(reused.BatchID)
	close(release[2])
	close(release[3])
	if result := waitResult(reused, "call-two"); len(result.Children) != 2 || result.IsError {
		t.Fatal("reuse failed", result)
	}
	if err := controller.resultSeen(reused.BatchID, "call-two", ""); err != nil {
		t.Fatal(err)
	}
	err = a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		for id, previous := range nativeIDs {
			native, err := st.native(id)
			if err != nil {
				return err
			}
			if native.ID != previous {
				return fmt.Errorf("reuse silently replaced native history")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	stopped := prepare("stop", "call-three", []DelegationChildRequest{{Key: "one", Task: "Wait for fixture response.", ChildSessionID: members[0].ChildSessionID}, {Key: "two", Task: "Wait for fixture response.", ChildSessionID: members[1].ChildSessionID}})
	awaitArrivals(stopped.BatchID)
	a.mu.Lock()
	_, err = a.stopSessionLocked(parent.ID, nil)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stopErr := controller.close()
	err = a.store.readSnapshot(context.Background(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: context.Background(), tx: tx}
		batch, err := st.delegationBatch(stopped.BatchID)
		if err != nil {
			return err
		}
		if batch.Outcome == DelegationCompleted {
			return fmt.Errorf("Stop became successful completion")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	remaining := 0
	for id := range a.runs {
		if id != parent.ID {
			remaining++
		}
	}
	delete(a.runs, parent.ID)
	a.mu.Unlock()
	if remaining != 0 {
		t.Fatal("child turns still owned after bounded close", remaining)
	}
	t.Logf("native batch2/join/reuse/Stop: providerRequests=%d; stopOutcomeError=%v; temporaryData=%s", requests.Load(), stopErr, a.dir)
}

func delegationLeafForbiddenGo(name string) bool {
	return name == "task" || name == "subagent" || name == "subagent_run" || strings.HasPrefix(name, "linked_") || strings.HasPrefix(name, "session_") || strings.HasPrefix(name, "graph_")
}
