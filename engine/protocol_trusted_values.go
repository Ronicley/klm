package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
)

// These readers are ONLY for ranges in an already validated immutable frame.
// They locate boundaries without building the same key set/JSON value again.
func skipValidatedString(ctx context.Context, r *bufio.Reader) error {
	slashes := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		part, err := r.ReadSlice('"')
		quoted := err == nil
		body := part
		if quoted {
			body = part[:len(part)-1]
		}
		n := 0
		for at := len(body) - 1; at >= 0 && body[at] == '\\'; at-- {
			n++
		}
		if n == len(body) {
			slashes += n
		} else {
			slashes = n
		}
		if quoted {
			if slashes%2 == 0 {
				return nil
			}
			slashes = 0
			continue
		}
		if err != bufio.ErrBufferFull {
			return err
		}
	}
}

func skipValidatedValue(ctx context.Context, r *bufio.Reader, first byte) error {
	if first == '"' {
		return skipValidatedString(ctx, r)
	}
	if first != '{' && first != '[' {
		count := 0
		for {
			count++
			if count%protocolChunkSize == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			b, err := r.ReadByte()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if b == ',' || b == '}' || b == ']' || b == ' ' || b == '\n' || b == '\r' || b == '\t' {
				return r.UnreadByte()
			}
		}
	}
	depth := 1
	count := 0
	for depth > 0 {
		count++
		if count%protocolChunkSize == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		switch b {
		case '"':
			if err := skipValidatedString(ctx, r); err != nil {
				return err
			}
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	return nil
}

func walkValidatedFields(content *protocolContent, visit func(string, *protocolContent) error) error {
	r, err := content.raw()
	if err != nil {
		return err
	}
	counter := &protocolCountingReader{input: r, bytes: content.start}
	p := &protocolJSONProjection{ctx: content.frame.ctx, r: bufio.NewReaderSize(counter, protocolChunkSize), counter: counter}
	b, err := p.nonSpace()
	if err != nil || b != '{' {
		return errProtocolJSON
	}
	for {
		b, err := p.nonSpace()
		if err != nil {
			return err
		}
		if b == '}' {
			return nil
		}
		if b != '"' {
			return errProtocolJSON
		}
		encoded, err := p.smallString(4096)
		if err != nil {
			return err
		}
		var key string
		if err := json.Unmarshal(encoded, &key); err != nil {
			return err
		}
		b, err = p.nonSpace()
		if err != nil || b != ':' {
			return errProtocolJSON
		}
		b, err = p.nonSpace()
		if err != nil {
			return err
		}
		start := counter.bytes - int64(p.r.Buffered()) - 1
		if err := skipValidatedValue(p.ctx, p.r, b); err != nil {
			return err
		}
		span := &protocolContent{frame: content.frame, start: start, bytes: counter.bytes - int64(p.r.Buffered()) - start, kind: b}
		if err := visit(key, span); err != nil {
			return err
		}
		b, err = p.nonSpace()
		if err != nil {
			return err
		}
		if b == '}' {
			return nil
		}
		if b != ',' {
			return errProtocolJSON
		}
	}
}

var errProtocolFieldFound = errors.New("validated field found")

func nativeEnvelopeObject(path []string, harness, typ string) bool {
	key := path[len(path)-1]
	if key == "result" {
		return len(path) == 1 && harness == "codex"
	}
	switch key {
	case "properties", "params", "info", "part", "state", "item", "thread", "turn", "message", "time", "tokens", "usage", "cache", "model", "metadata", "summary":
		return true
	case "event":
		return harness == "pi-child"
	case "data":
		return harness == "pi" && typ == "response"
	}
	return false
}

func projectValidatedObject(content *protocolContent, harness, typ string, path []string) (map[string]any, error) {
	result := map[string]any{}
	policy := liveProtocolPolicy(harness, typ)
	metadata := 0
	err := walkValidatedFields(content, func(key string, span *protocolContent) error {
		field := append(append([]string{}, path...), key)
		action := policy(field)
		var value any
		var err error
		switch {
		case action == projectionDiscard:
			value = nil
		case action == projectionDiffPreview && span.kind == '"' && span.bytes > openCodeDiffPreviewLimit:
			value = openCodeOmittedDiff
		case span.kind == '{' && nativeEnvelopeObject(field, harness, typ):
			value, err = projectValidatedObject(span, harness, typ, field)
		case span.bytes > protocolChunkSize:
			value = span
		default:
			r, readErr := span.raw()
			if readErr != nil {
				return readErr
			}
			d := json.NewDecoder(r)
			d.UseNumber()
			err = d.Decode(&value)
		}
		if err != nil {
			return err
		}
		metadata += len(key) + 48 + protocolValueBytes(value, map[*protocolFrame]bool{})
		if metadata > 1<<20 {
			return errProtocolMetadata
		}
		result[key] = value
		return nil
	})
	return result, err
}

func (f *protocolFrame) projectNative(harness string, limit int, scope *protocolContentScope, spans ...*protocolContent) (map[string]any, error) {
	if !f.ownerRead {
		if scope != nil && scope.ctx != nil && !scope.isClosed() {
			f.ctx = protocolReadContext{Context: scope.ctx, transport: f.ctx}
		}
		f.ctx = protocolDirectoryContext(f.ctx, f.dir)
		f.ownerRead = true
	}
	release, err := acquireProtocolParser(f.ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	value := &protocolContent{frame: f, bytes: f.size, kind: '{'}
	if len(spans) > 0 {
		value = spans[0]
	}
	typ := f.nativeType
	if !f.validated {
		r, err := value.raw()
		if err != nil {
			return nil, err
		}
		_, typ, err = projectProtocolJSONUsing(f.ctx, r, nil, 0, nil, true, nil, value.start, harness != "")
		if err != nil {
			return nil, protocolFrameError(harness, typ, f.size, err)
		}
		if len(spans) == 0 {
			f.validated = true
			f.nativeType = typ
		}
	}
	if typ == "" && harness != "" && harness != "codex" && harness != "opencode-history" && harness != "pi-child" {
		return nil, errProtocolJSON
	}
	frame, err := projectValidatedObject(value, harness, typ, nil)
	if err != nil {
		return nil, protocolFrameError(harness, typ, f.size, err)
	}
	if err := validateNativeEnvelope(harness, frame); err != nil {
		return nil, err
	}
	if err := f.retainEnvelope(frame, scope, protocolHasContent(frame), len(spans) == 0); err != nil {
		return nil, err
	}
	// Once framing/validation succeeds, cached immutable content belongs to the
	// turn, not to a completed HTTP callback or a retired harness transport.
	if scope != nil && scope.ctx != nil && !scope.isClosed() {
		f.ctx = protocolDirectoryContext(scope.ctx, f.dir)
	}
	return frame, nil
}

func validateNativeEnvelope(harness string, frame map[string]any) error {
	boundedStrings := func(m map[string]any, keys ...string) error {
		for _, key := range keys {
			if value := m[key]; value != nil {
				str, ok := value.(string)
				if !ok || len(str) > 4096 {
					return errProtocolMetadata
				}
			}
		}
		return nil
	}
	if harness != "" {
		if err := boundedStrings(frame, "type"); err != nil {
			return err
		}
	}
	if harness == "codex" {
		if err := boundedStrings(frame, "method"); err != nil {
			return err
		}
		if id, ok := frame["id"]; ok {
			switch id := id.(type) {
			case string:
				if len(id) > 256 {
					return errProtocolMetadata
				}
			case json.Number:
				if len(id) > 256 {
					return errProtocolMetadata
				}
			default:
				return errProtocolJSON
			}
		}
		params := object(frame["params"])
		if err := boundedStrings(params, "threadId", "turnId", "itemId"); err != nil {
			return err
		}
		item := object(params["item"])
		switch str(item, "type") {
		case "commandExecution", "fileChange", "mcpToolCall":
			if value := item["status"]; value != nil {
				status, ok := value.(string)
				if !ok {
					return errProtocolMetadata
				}
				switch status {
				case "inProgress", "completed", "failed", "declined":
				default:
					return errProtocolJSON
				}
			}
		}
		return nil
	}
	if harness == "pi" {
		switch str(frame, "type") {
		case "response", "extension_ui_request":
			return boundedStrings(frame, "id", "method")
		case "tool_execution_start", "tool_execution_update", "tool_execution_end":
			return boundedStrings(frame, "toolCallId", "toolName")
		}
		return nil
	}
	if harness == "opencode" || harness == "opencode-history" {
		properties := object(frame["properties"])
		if harness == "opencode-history" {
			properties = frame
		}
		if err := boundedStrings(properties, "sessionID", "messageID", "partID"); err != nil {
			return err
		}
		for _, name := range []string{"info", "part"} {
			m := object(properties[name])
			if err := boundedStrings(m, "id", "sessionID", "messageID", "role", "type"); err != nil {
				return err
			}
			times := object(m["time"])
			for _, field := range []string{"created", "completed", "start", "end"} {
				if value := times[field]; value != nil && tokenCount(value) == nil {
					return errProtocolJSON
				}
			}
		}
	}
	return nil
}
