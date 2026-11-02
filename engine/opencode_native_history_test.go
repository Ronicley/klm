package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOpenCodeNativeHistoryBoundedMessages(t *testing.T) {
	ctx := context.Background()
	dir := protocolTestDir(t)
	scope := &protocolContentScope{ctx: ctx, dir: dir}
	defer scope.Close()
	message := func() io.Reader {
		return io.MultiReader(strings.NewReader(`{"info":{"id":"msg_1","sessionID":"native_1","role":"assistant","time":{"created":1,"completed":2},"finish":"stop","summary":{"diffs":[{"patch":"`), &openCodeRepeatedBytes{25 << 20}, strings.NewReader(`"}]}},"parts":[{"id":"part_1","messageID":"msg_1","sessionID":"native_1","type":"text","text":"`), &openCodeRepeatedBytes{25 << 20}, strings.NewReader(`"}]}`))
	}
	h := &openCodeHTTP{v1: true, scope: scope, base: "http://opencode.invalid", client: &http.Client{Transport: openCodeHistoryTransport(func(r *http.Request) (*http.Response, error) {
		var body io.Reader = message()
		if strings.HasSuffix(r.URL.Path, "/message") {
			body = io.MultiReader(strings.NewReader("["), body, strings.NewReader("]"))
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(body)}, nil
	})}}
	count := 0
	if err := h.walkMessages(ctx, "native_1", nil, func(value map[string]any) error {
		count++
		if object(object(value["info"])["summary"])["diffs"] != nil {
			t.Fatal("summary diff retained")
		}
		return nil
	}); err != nil || count != 1 {
		t.Fatalf("native history: count=%d err=%v", count, err)
	}
	value, release, err := h.nativeMessage(ctx, "native_1", "msg_1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := protocolArrayEach(value["parts"], func(value any) error {
		source, ok := object(value)["text"].(*protocolContent)
		if !ok {
			t.Fatal("native history materialized text")
		}
		reader, err := source.text()
		if err != nil {
			return err
		}
		n, err := io.Copy(io.Discard, reader)
		if err == nil && n != 25<<20 {
			t.Fatalf("history text bytes: %d", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeV2NativeHistoryMessage(t *testing.T) {
	for _, id := range []string{"msg_1", "msg_other"} {
		t.Run(id, func(t *testing.T) {
			ctx := context.Background()
			scope := &protocolContentScope{ctx: ctx, dir: protocolTestDir(t)}
			defer scope.Close()
			h := &openCodeHTTP{scope: scope, base: "http://opencode.invalid", client: &http.Client{Transport: openCodeHistoryTransport(func(r *http.Request) (*http.Response, error) {
				body := `<!doctype html><html></html>`
				if r.URL.Path == "/api/session/native_1/message/msg_1" {
					body = `{"data":{"id":"` + id + `","type":"assistant","time":{"created":1,"completed":2},"finish":"stop","content":[{"type":"text","text":"reply"}]}}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			value, release, err := h.nativeMessage(ctx, "native_1", "msg_1")
			if release != nil {
				defer release()
			}
			if id != "msg_1" {
				if err == nil {
					t.Fatal("accepted a different native message")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			info := object(value["info"])
			parts := value["parts"].([]any)
			if str(info, "id") != id || str(info, "sessionID") != "native_1" || str(info, "finish") != "stop" || len(parts) != 1 || str(object(parts[0]), "text") != "reply" {
				t.Fatalf("final message was not projected: %+v", value)
			}
		})
	}
}
