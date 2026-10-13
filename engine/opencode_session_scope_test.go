package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenCodeRequestOwnership(t *testing.T) {
	parents := map[string]string{"child": "root", "grandchild": "child", "unrelated": "", "cycle": "cycle"}
	requests := 0
	h := &openCodeHTTP{base: "http://opencode.invalid", client: &http.Client{Transport: openCodeHistoryTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		id := strings.TrimPrefix(r.URL.Path, "/api/session/")
		parent, ok := parents[id]
		if !ok {
			t.Fatalf("unexpected session lookup: %q", id)
		}
		body := fmt.Sprintf(`{"data":{"id":%q,"parentID":%q}}`, id, parent)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	owned := map[string]bool{"root": true}
	for _, test := range []struct {
		id   string
		want bool
	}{
		{"grandchild", true}, // No task metadata/display registration yet.
		{"child", true},
		{"root", true},
		{"unrelated", false},
		{"cycle", false},
		{"", false},
	} {
		got, err := h.ownsSession(context.Background(), test.id, owned)
		if err != nil || got != test.want {
			t.Fatalf("ownership of %q = %v, %v; want %v", test.id, got, err, test.want)
		}
	}
	if requests != 4 || owned["unrelated"] || owned["cycle"] {
		t.Fatalf("invalid ancestry cache: requests=%d owned=%v", requests, owned)
	}
}
