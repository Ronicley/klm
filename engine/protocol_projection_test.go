package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func protocolTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := prepareProtocolStaging(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestProtocolLargeOpenCodeSummary(t *testing.T) {
	for _, size := range []int{25 << 20, 100 << 20} {
		t.Run(stringSize(size), func(t *testing.T) {
			dir := protocolTestDir(t)
			// Late type, escaped keys, CRLF and multiple data lines. The generated
			// payload is never constructed as a fixture-sized string or []byte.
			body := io.MultiReader(
				strings.NewReader(": heartbeat\r\nid: native-id\r\ndata: {\"properties\":{\"info\":{\"id\":\"msg_1\",\"sessionID\":\"ses_1\",\"summary\":{\"d\\u0069ffs\":[{\"patch\":\""),
				&openCodeRepeatedBytes{size},
				strings.NewReader("\"}],\"title\":\"kept\"}}},\r\ndata: \"type\":\"message.updated\"}\r\n\r\ndata: {\"type\":\"session.error\",\"properties\":{\"sessionID\":\"ses_1\"}}\n\n"),
			)
			var events []map[string]any
			err := readProjectedSSE(context.Background(), body, dir, func(raw []byte) error {
				if len(raw) > 512 {
					t.Fatal("summary was retained")
				}
				var event map[string]any
				if err := json.Unmarshal(raw, &event); err != nil {
					return err
				}
				events = append(events, event)
				return nil
			})
			if err != nil || len(events) != 2 || str(events[1], "type") != "session.error" {
				t.Fatalf("following control event lost: %v", err)
			}
			info := object(object(events[0]["properties"])["info"])
			if str(info, "id") != "msg_1" || str(object(info["summary"]), "title") != "kept" || object(info["summary"])["diffs"] != nil {
				t.Fatal("projection changed non-summary content")
			}
			assertProtocolStagingEmpty(t, dir)
		})
	}
}

func stringSize(size int) string {
	if size == 25<<20 {
		return "25MiB"
	}
	return "100MiB"
}

func TestProtocolLargePiAggregates(t *testing.T) {
	for _, typ := range []string{"agent_end", "turn_end"} {
		t.Run(typ, func(t *testing.T) {
			dir := protocolTestDir(t)
			var got []map[string]any
			lines := &jsonLines{cancel: func() { t.Fatal("valid aggregate cancelled") }, projection: &protocolLineInput{ctx: context.Background(), dir: dir, harness: "pi"}, consume: func(raw []byte) error {
				if len(raw) > 256 {
					t.Fatal("aggregate retained")
				}
				var event map[string]any
				err := json.Unmarshal(raw, &event)
				got = append(got, event)
				return err
			}}
			key := "messages"
			if typ == "turn_end" {
				key = "message"
			}
			body := io.MultiReader(strings.NewReader(`{"`+key+`":[{"text":"`), &openCodeRepeatedBytes{25 << 20}, strings.NewReader(`"}],"type":"`+typ+`","id":"keep","toolResults":[]}`+"\n"+`{"type":"agent_settled"}`+"\n"))
			if _, err := io.CopyBuffer(lines, body, make([]byte, protocolChunkSize)); err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || str(got[0], "id") != "keep" || str(got[1], "type") != "agent_settled" {
				t.Fatal("correlation/settlement changed")
			}
			assertProtocolStagingEmpty(t, dir)
		})
	}
}

func TestProtocolValidationAndExactPolicies(t *testing.T) {
	for _, input := range []string{
		`{"messages":["bad\x"],"type":"agent_end"}`,
		`{"messages":["` + string([]byte{0xff}) + `"],"type":"agent_end"}`,
		`{"messages":[true,],"type":"agent_end"}`,
		`{"messages":[01],"type":"agent_end"}`,
		`{"messages":[1e],"type":"agent_end"}`,
		`{"messages":[],"type":"agent_end","t\u0079pe":"message_end"}`,
		`{"messages":[],"type":"agent_end"} {}`,
		`{"messages":["truncated`,
	} {
		_, _, err := projectProtocolJSON(context.Background(), strings.NewReader(input), liveProtocolPolicy("pi", "agent_end"), 4096, errProtocolMetadata, false)
		if err == nil {
			t.Fatalf("malformed/ambiguous input accepted: %q", input)
		}
	}
	for _, input := range []string{
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"keep é 🐱\n\\\""}]}}`,
		`{"type":"tool_execution_end","result":{"content":[{"text":"keep"}]},"isError":false}`,
		`{"type":"permission.asked","properties":{"info":{"summary":{"diffs":[{"patch":"approval"}]}}}}`,
		`{"type":"message.updated","properties/info/summary/diffs":{"patch":"not the policy path"}}`,
	} {
		var expected, actual any
		_ = json.Unmarshal([]byte(input), &expected)
		typ := str(object(expected), "type")
		projected, _, err := projectProtocolJSON(context.Background(), strings.NewReader(input), liveProtocolPolicy("pi", typ), 4096, errProtocolMetadata, false)
		if strings.HasPrefix(typ, "permission") || typ == "message.updated" {
			projected, _, err = projectProtocolJSON(context.Background(), strings.NewReader(input), liveProtocolPolicy("opencode", typ), 4096, errProtocolMetadata, false)
		}
		if err != nil || json.Unmarshal(projected, &actual) != nil {
			t.Fatalf("valid input rejected: %v", err)
		}
		a, _ := json.Marshal(expected)
		b, _ := json.Marshal(actual)
		if !bytes.Equal(a, b) {
			t.Fatalf("useful/decision content changed: %s", projected)
		}
	}
}

func TestProtocolUsefulContentLimitAndCancellation(t *testing.T) {
	dir := protocolTestDir(t)
	frame := &protocolFrame{ctx: context.Background(), dir: dir}
	body := io.MultiReader(strings.NewReader(`{"type":"message_end","message":{"text":"`), &openCodeRepeatedBytes{5 << 20}, strings.NewReader(`"}}`))
	if _, err := io.CopyBuffer(frame, body, make([]byte, protocolChunkSize)); err != nil {
		t.Fatal(err)
	}
	if _, err := frame.project("pi", 4<<20); err == nil {
		t.Fatal("useful content was silently discarded")
	}
	if err := frame.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	frame = &protocolFrame{ctx: ctx, dir: dir}
	if _, err := frame.Write(bytes.Repeat([]byte("x"), 2*protocolChunkSize)); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, _, err := projectProtocolJSON(ctx, strings.NewReader(`{"type":"agent_end","messages":[]}`), nil, 4096, errProtocolMetadata, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("projection cancellation ignored: %v", err)
	}
	if _, err := frame.Write([]byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if err := frame.Close(); err != nil {
		t.Fatal(err)
	}
	assertProtocolStagingEmpty(t, dir)
}

func TestProtocolSSEFraming(t *testing.T) {
	dir := protocolTestDir(t)
	for _, separator := range []string{"\n", "\r\n", "\r"} {
		count := 0
		input := strings.ReplaceAll("data\ndata: {\"type\":\"session.status\",\ndata: \"text\":\"é 🐱\\n\"}\n\ndata: {\"type\":\"incomplete\"}", "\n", separator)
		err := readProjectedSSE(context.Background(), strings.NewReader(input), dir, func(raw []byte) error {
			count++
			if !json.Valid(raw) {
				t.Fatal("invalid multiline JSON")
			}
			return nil
		})
		if err != nil || count != 1 {
			t.Fatalf("framing %q: count=%d err=%v", separator, count, err)
		}
	}
	assertProtocolStagingEmpty(t, dir)
}

func TestProtocolHistorySummary(t *testing.T) {
	body := io.MultiReader(strings.NewReader(`[{"info":{"id":"msg_1","role":"user","summary":{"diffs":[{"patch":"`), &openCodeRepeatedBytes{25 << 20}, strings.NewReader(`"}],"title":"keep"}},"parts":[]}]`))
	data, err := readOpenCodeHistoryJSON(body)
	if err != nil || len(data) > 256 || !bytes.Contains(data, []byte(`"title":"keep"`)) || !bytes.Contains(data, []byte(`"diffs":null`)) {
		t.Fatalf("history summary not projected: %v", err)
	}
}

func assertProtocolStagingEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir + "/ingestion-staging")
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging not cleaned: %d entries; %v", len(entries), err)
	}
}

func TestProtocolStagingCapacity(t *testing.T) {
	protocolStagingBudget.Lock()
	before := protocolStagingBudget.bytes
	protocolStagingBudget.bytes = protocolStagingLimit - 1
	protocolStagingBudget.Unlock()
	defer func() {
		protocolStagingBudget.Lock()
		protocolStagingBudget.bytes = before
		protocolStagingBudget.Unlock()
	}()
	f := &protocolFrame{ctx: context.Background(), dir: protocolTestDir(t)}
	if _, err := f.Write([]byte("{}")); !errors.Is(err, errProtocolStaging) || f.size != 0 {
		t.Fatalf("shared staging budget not enforced: %v", err)
	}
}

func TestProtocolWideUsefulObject(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		dir := protocolTestDir(t)
		ctx := context.Background()
		scope := &protocolContentScope{dir: dir, ctx: ctx}
		frame := &protocolFrame{ctx: ctx, dir: dir}
		w := bufio.NewWriterSize(frame, protocolChunkSize)
		_, _ = io.WriteString(w, `{"type":"tool_execution_end","toolCallId":"wide","toolName":"read","result":{`)
		for index := 0; index < 20000; index++ {
			if index > 0 {
				_ = w.WriteByte(',')
			}
			_, _ = fmt.Fprintf(w, `"key%d":%d`, index, index)
		}
		if duplicate {
			_, _ = io.WriteString(w, `,"key19999":true`)
		}
		_, _ = io.WriteString(w, `}}`)
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		value, err := frame.projectNative("pi", 4<<20, scope)
		if duplicate {
			if err == nil {
				t.Fatal("duplicate key in spilled object accepted")
			}
		} else if err != nil {
			t.Fatal(err)
		} else if _, ok := value["result"].(*protocolContent); !ok {
			t.Fatal("wide useful object was materialized")
		}
		_ = scope.Close()
		if !frame.retained {
			_ = frame.Close()
		}
		assertProtocolStagingEmpty(t, dir)
	}
}
