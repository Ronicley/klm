package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type openCodeHistoryTransport func(*http.Request) (*http.Response, error)

func (f openCodeHistoryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestOpenCodeHistoryPagination(t *testing.T) {
	// Forty 1 MiB messages exceed the former whole-history response limit.
	// A transport stub exercises HTTP decoding without a server or native harness.
	const count = 40
	padding := strings.Repeat("x", 1<<20)
	requests := 0
	var limits []int
	h := &openCodeHTTP{base: "http://opencode.invalid", directory: "C:/project & history"}
	h.client = &http.Client{Transport: openCodeHistoryTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		q := r.URL.Query()
		if r.URL.Path != "/api/session/ses_test/message" || q.Get("location[directory]") != h.directory {
			t.Fatalf("incorrect history request: %s", r.URL)
		}
		limit, err := strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 {
			t.Fatalf("invalid page size: %q", q.Get("limit"))
		}
		limits = append(limits, limit)
		end := count
		if cursor := q.Get("cursor"); cursor != "" {
			end, err = strconv.Atoi(strings.TrimPrefix(cursor, "older/"))
			if err != nil {
				t.Fatalf("invalid cursor: %q", cursor)
			}
		}
		start := max(0, end-limit)
		page := make([]map[string]any, 0, end-start)
		for i := end - 1; i >= start; i-- {
			id := fmt.Sprintf("msg_%03d", i)
			page = append(page, map[string]any{
				"id": id, "type": "assistant", "time": map[string]any{"created": i, "completed": i + 1},
				"tokens":  map[string]any{"input": 1, "output": 2, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}},
				"content": []any{map[string]any{"type": "text", "text": padding}},
			})
		}
		next := ""
		if start > 0 {
			next = fmt.Sprintf("older/%d", start)
		}
		data, err := json.Marshal(map[string]any{"data": page, "cursor": map[string]any{"next": next}})
		if err != nil {
			t.Fatal(err)
		}
		headers := http.Header{}
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	usage := &openCodeUsage{messages: map[string]map[string]any{}, steps: map[string]map[string]map[string]any{}, windows: map[string]*int64{}}
	var visited []string
	baseline := map[string]bool{}
	err := h.walkMessages(context.Background(), "ses_test", nil, func(message map[string]any) error {
		info := object(message["info"])
		id := str(info, "id")
		visited = append(visited, id)
		baseline[id] = true
		usage.message(info)
		for _, part := range message["parts"].([]any) {
			usage.part(object(part))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(visited) != count || visited[0] != "msg_039" || visited[count-1] != "msg_000" {
		t.Fatalf("history was omitted or out of order: %v", visited)
	}
	snapshot := usage.snapshot()
	if snapshot == nil || snapshot.InputTokens == nil || *snapshot.InputTokens != count || snapshot.OutputTokens == nil || *snapshot.OutputTokens != 2*count {
		t.Fatalf("history token totals changed: %+v", snapshot)
	}
	if fmt.Sprint(limits) != "[64 32 16 16 16]" {
		t.Fatalf("unexpected page reduction/traversal: %v", limits)
	}

	// The latest three messages represent a new turn. Stop at an old message
	// within the first successful page, without fetching any older page.
	for i := count - 3; i < count; i++ {
		delete(baseline, fmt.Sprintf("msg_%03d", i))
	}
	requests = 0
	visited = nil
	err = h.walkMessages(context.Background(), "ses_test", baseline, func(message map[string]any) error {
		visited = append(visited, str(object(message["info"]), "id"))
		return nil
	})
	if err != nil || fmt.Sprint(visited) != "[msg_039 msg_038 msg_037]" || requests != 3 {
		t.Fatalf("turn boundary failed: messages=%v requests=%d err=%v", visited, requests, err)
	}
}

func TestOpenCodeHistoryRepeatedCursor(t *testing.T) {
	requests := 0
	h := &openCodeHTTP{base: "http://opencode.invalid", client: &http.Client{Transport: openCodeHistoryTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		body := fmt.Sprintf(`{"data":[{"id":"msg_%d","type":"user"}],"cursor":{"next":"same-cursor"}}`, requests)
		headers := http.Header{}
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	err := h.walkMessages(context.Background(), "ses_test", nil, func(map[string]any) error { return nil })
	if err == nil || requests != 2 || !strings.Contains(err.Error(), "non-advancing") {
		t.Fatalf("repeated cursor did not stop: requests=%d err=%v", requests, err)
	}
}

func TestOpenCodeResponseDiagnostic(t *testing.T) {
	h := &openCodeHTTP{base: "http://opencode.invalid", directory: "private-directory", client: &http.Client{Transport: openCodeHistoryTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.Repeat(" ", openCodeResponseLimit+1)))}, nil
	})}}
	err := h.json(context.Background(), http.MethodGet, "/session/ses_test/message?before=private-cursor", nil, nil)
	if !errors.Is(err, errOpenCodeResponseTooLarge) || !strings.Contains(err.Error(), "GET /session/ses_test/message") || strings.Contains(err.Error(), "private-") {
		t.Fatalf("incorrect response diagnostic: %v", err)
	}
}
