package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type openCodeRepeatedBytes struct{ remaining int }

func (r *openCodeRepeatedBytes) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	for i := range p[:n] {
		p[i] = 'x'
	}
	r.remaining -= n
	return n, nil
}

func TestOpenCodeHistoryLargeDiffMetadata(t *testing.T) {
	// Generate 44 MiB incrementally, matching the observed metadata.diff and
	// metadata.files[].patch shape without allocating those strings in the test.
	body := io.MultiReader(
		strings.NewReader(`{"data":[{"id":"msg_big","type":"assistant","tokens":{"input":1,"output":2,"reasoning":0,"cache":{"read":0,"write":0}},"content":[{"id":"call_test","type":"tool","name":"apply_patch","state":{"status":"completed","input":{"patchText":"small patch"},"content":[{"type":"text","text":"Applied changes."}],"metadata":{"diff":"`),
		&openCodeRepeatedBytes{22 << 20},
		strings.NewReader(`","files":[{"filePath":"C:/project/file.txt","relativePath":"file.txt","type":"update","patch":"`),
		&openCodeRepeatedBytes{22 << 20},
		strings.NewReader(`","additions":3,"deletions":2}],"diagnostics":{},"truncated":false,"interrupted":true}},"time":{"created":1,"completed":2}}]}],"cursor":{}}`),
	)
	requests, visited := 0, 0
	h := &openCodeHTTP{base: "http://opencode.invalid", client: &http.Client{Transport: openCodeHistoryTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(body)}, nil
	})}}
	err := h.walkMessages(context.Background(), "ses_test", nil, func(message map[string]any) error {
		visited++
		parts := message["parts"].([]any)
		tool := object(parts[0])
		state := object(tool["state"])
		meta := object(state["metadata"])
		file := object(meta["files"].([]any)[0])
		if str(meta, "diff") != openCodeOmittedDiff || str(file, "patch") != openCodeOmittedDiff {
			t.Fatal("large previews were not replaced")
		}
		if str(tool, "callID") != "call_test" || str(state, "status") != "completed" ||
			str(object(state["input"]), "patchText") != "small patch" || str(state, "output") != "Applied changes." ||
			str(file, "filePath") != "C:/project/file.txt" || file["additions"] != float64(3) || file["deletions"] != float64(2) ||
			!truth(meta, "interrupted") || meta["truncated"] != false || object(meta["diagnostics"]) == nil ||
			object(object(message["info"])["tokens"])["output"] != float64(2) {
			t.Fatal("non-preview data changed")
		}
		encoded, err := json.Marshal(message)
		if err != nil || len(encoded) > 2048 {
			t.Fatalf("unexpected retained message size: %d, %v", len(encoded), err)
		}
		return nil
	})
	if err != nil || requests != 1 || visited != 1 {
		t.Fatalf("large metadata history failed: requests=%d visited=%d err=%v", requests, visited, err)
	}
}

func TestOpenCodeHistorySmallPreviews(t *testing.T) {
	input := `[{"parts":[{"state":{"input":{"patchText":"original"},"metadata":{"d\u0069ff":"small\n\"diff\" \u00e9","files":[{"patch":"small patch","additions":1}],"other":[true,false,null,1.25e-2]}}}]}]`
	projected, err := readOpenCodeHistoryJSON(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	var original, result any
	if json.Unmarshal([]byte(input), &original) != nil || json.Unmarshal(projected, &result) != nil {
		t.Fatal("invalid JSON")
	}
	a, _ := json.Marshal(original)
	b, _ := json.Marshal(result)
	if string(a) != string(b) {
		t.Fatalf("small previews changed: %s", projected)
	}
}

func TestOpenCodeHistoryLargeNonPreviewStillLimited(t *testing.T) {
	body := io.MultiReader(
		strings.NewReader(`[{"parts":[{"state":{"input":{"patchText":"`),
		&openCodeRepeatedBytes{openCodeResponseLimit + 1},
		strings.NewReader(`"}}}]}]`),
	)
	_, err := readOpenCodeHistoryJSON(body)
	if !errors.Is(err, errOpenCodeResponseTooLarge) {
		t.Fatalf("non-preview response limit was lost: %v", err)
	}
}

func TestOpenCodeHistoryDiscardedStringValidation(t *testing.T) {
	body := io.MultiReader(
		strings.NewReader(`[{"parts":[{"state":{"metadata":{"diff":"`),
		&openCodeRepeatedBytes{openCodeDiffPreviewLimit + 1},
		strings.NewReader(`\x"}}}]}]`),
	)
	_, err := readOpenCodeHistoryJSON(body)
	if !errors.Is(err, errOpenCodeHistoryJSON) {
		t.Fatalf("invalid escape in omitted text was accepted: %v", err)
	}
}
