package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenCodeV2MCPConnection(t *testing.T) {
	for _, terminal := range []string{"connected", "failed"} {
		t.Run(terminal, func(t *testing.T) {
			requests := 0
			h := &openCodeHTTP{base: "http://opencode.invalid", client: &http.Client{Transport: openCodeHistoryTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				status := "pending"
				if requests > 1 {
					status = terminal
				}
				body := fmt.Sprintf(`{"data":[{"name":"klm_linked","status":{"status":%q}}]}`, status)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			servers, err := h.waitMCPConnected(context.Background(), "klm_linked")
			if requests != 2 || (err == nil) != (terminal == "connected") {
				t.Fatalf("requests=%d err=%v", requests, err)
			}
			if err == nil && str(object(servers["klm_linked"]), "status") != terminal {
				t.Fatal(servers)
			}
		})
	}
}

func TestOpenCodeV2CatalogInitialization(t *testing.T) {
	modelLoaded := false
	h := &openCodeHTTP{base: "http://opencode.invalid", client: &http.Client{Transport: openCodeHistoryTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/api/config":
			body = `[{"type":"document","info":{"default_agent":"build"}}]`
		case "/api/agent":
			body = `{"data":[{"id":"build"}]}`
		case "/api/model/default":
			body = `{"data":{"providerID":"openai","id":"model"}}`
		case "/api/model":
			modelLoaded = true
			body = `{"data":[{"id":"model","providerID":"openai","name":"Model","enabled":true,"variants":[{"id":"high"}]}]}`
		case "/api/provider":
			if !modelLoaded {
				t.Fatal("provider snapshot requested before model/plugin initialization")
			}
			body = `{"data":[{"id":"openai","name":"OpenAI"}]}`
		case "/api/integration":
			body = `{"data":[]}`
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	c, err := h.v2Catalog(context.Background())
	if err != nil || len(c.Models) != 1 || len(c.ConnectedProviders) != 1 || c.DefaultModel != "openai/model" {
		t.Fatalf("catalog=%+v err=%v", c, err)
	}
}

func TestOpenCodeV2Projection(t *testing.T) {
	p := openCodeV2Projection{infos: map[string]map[string]any{}, parts: map[string]map[string]any{}}
	project := func(kind string, data map[string]any) map[string]any {
		t.Helper()
		data["sessionID"], data["assistantMessageID"] = "ses_root", "msg_reply"
		values := p.event(map[string]any{"type": kind, "created": float64(10), "data": data})
		if len(values) != 1 {
			t.Fatalf("%s: missing projection", kind)
		}
		return object(values[0]["properties"])
	}
	project("session.step.started", map[string]any{"started": float64(1), "model": map[string]any{"providerID": "openai", "id": "model", "variant": "high"}})
	part := object(project("session.text.started", map[string]any{"ordinal": float64(0)})["part"])
	part["text"] = "consumer-owned delta"
	final := object(project("session.text.ended", map[string]any{"ordinal": float64(0), "text": "reply"})["part"])
	if str(final, "id") != "text/0" || str(final, "text") != "reply" {
		t.Fatal(final)
	}
	project("session.tool.input.started", map[string]any{"id": "call", "name": "shell"})
	project("session.tool.called", map[string]any{"id": "call", "input": map[string]any{"command": "echo ok"}})
	tool := object(project("session.tool.success", map[string]any{"id": "call", "content": []any{map[string]any{"type": "text", "text": "ok"}}})["part"])
	if str(tool, "tool") != "shell" || str(object(tool["state"]), "output") != "ok" {
		t.Fatal(tool)
	}
	message, err := openCodeV2Message("ses_root", map[string]any{"id": "msg_reply", "type": "assistant", "model": map[string]any{"providerID": "openai", "id": "model"}, "time": map[string]any{"created": float64(1), "completed": float64(10)}, "content": []any{map[string]any{"type": "text", "text": "reply"}}, "finish": "stop"})
	if err != nil || str(object(message["parts"].([]any)[0]), "id") != str(final, "id") {
		t.Fatalf("history: %v, %v", message, err)
	}
	permission := openCodeV2Permission(map[string]any{"action": "shell", "resources": []any{"echo ok"}, "source": map[string]any{"id": "call"}})
	if str(permission, "permission") != "shell" || str(object(permission["tool"]), "callID") != "call" {
		t.Fatal(permission)
	}
	prompt := (submission{Text: "task", Context: "context", Files: []map[string]any{{"url": "file:///file.txt", "filename": "file.txt"}}}).openCodeV2Prompt()
	if str(prompt, "text") != "task\n\ncontext" || prompt["parts"] != nil {
		t.Fatal(prompt)
	}
	projected, err := readOpenCodeHistoryJSON(strings.NewReader(`{"data":[{"id":"msg_user","type":"user","files":[{"data":"base64","mime":"image/png"}]}],"cursor":{}}`))
	if err != nil || strings.Contains(string(projected), "base64") {
		t.Fatalf("attachment projection: %s, %v", projected, err)
	}
}
