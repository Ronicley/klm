package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Project V2's messages/events into the existing KLM display and usage records.
// Native identifiers stay unchanged so stored conversations can resume after migration.
func openCodeV2Info(sessionID string, message map[string]any) map[string]any {
	model := object(message["model"])
	info := map[string]any{
		"id": message["id"], "sessionID": sessionID, "role": message["type"],
		"time": message["time"], "finish": message["finish"], "tokens": message["tokens"],
		"providerID": model["providerID"], "modelID": model["id"], "variant": model["variant"],
	}
	if message["error"] != nil {
		info["error"] = openCodeV2Error(object(message["error"]))
	}
	return info
}

func openCodeV2Error(value map[string]any) map[string]any {
	name := str(value, "type")
	if name == "aborted" {
		name = "MessageAbortedError"
	}
	return map[string]any{"name": name, "message": str(value, "message")}
}

func openCodeV2Part(sessionID, messageID, id string, content map[string]any, completed any) map[string]any {
	kind := str(content, "type")
	part := map[string]any{"id": id, "sessionID": sessionID, "messageID": messageID, "type": kind}
	if kind == "tool" {
		state := object(content["state"])
		part["tool"], part["callID"] = content["name"], content["id"]
		projected := map[string]any{"status": state["status"], "input": state["input"], "metadata": state["metadata"], "output": contentText(state["content"])}
		if state["error"] != nil {
			projected["error"] = openCodeV2Error(object(state["error"]))
		}
		if str(state, "status") == "streaming" {
			projected["status"] = "pending"
		}
		part["state"] = projected
	} else {
		part["text"] = content["text"]
		if completed != nil {
			part["time"] = map[string]any{"end": completed}
		}
	}
	return part
}

func openCodeV2Message(sessionID string, message map[string]any) (map[string]any, error) {
	id, kind := str(message, "id"), str(message, "type")
	if id == "" || kind == "" {
		return nil, errors.New("OpenCode V2 returned an invalid message.")
	}
	parts := []any{}
	if kind == "assistant" {
		content, ok := message["content"].([]any)
		if !ok {
			return nil, errors.New("OpenCode V2 returned a message without content.")
		}
		ordinals := map[string]int{}
		for _, value := range content {
			item := object(value)
			partKind := str(item, "type")
			partID := fmt.Sprintf("%s/%d", partKind, ordinals[partKind])
			ordinals[partKind]++
			if partKind == "tool" {
				partID = "tool/" + str(item, "id")
			}
			parts = append(parts, openCodeV2Part(sessionID, id, partID, item, object(message["time"])["completed"]))
		}
	}
	return map[string]any{"info": openCodeV2Info(sessionID, message), "parts": parts}, nil
}

func openCodeV2Permission(value map[string]any) map[string]any {
	return map[string]any{
		"id": value["id"], "sessionID": value["sessionID"], "permission": value["action"],
		"patterns": value["resources"], "metadata": value["metadata"],
		"tool": map[string]any{"callID": object(value["source"])["id"]},
	}
}

func (h *openCodeHTTP) mcpStatus(ctx context.Context) (map[string]any, error) {
	var servers []map[string]any
	if err := h.json(ctx, http.MethodGet, "/api/mcp", nil, &servers); err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, server := range servers {
		result[str(server, "name")] = server["status"]
	}
	return result, nil
}

func (h *openCodeHTTP) waitMCPConnected(ctx context.Context, name string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		servers, err := h.mcpStatus(ctx)
		if err != nil {
			return nil, err
		}
		switch str(object(servers[name]), "status") {
		case "connected":
			return servers, nil
		case "", "pending":
		default:
			return nil, errors.New("OpenCode could not connect to the linked-agent bridge.")
		}
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return nil, errors.New("OpenCode linked-agent bridge connection timed out.")
			}
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

type openCodeV2Projection struct {
	infos map[string]map[string]any
	parts map[string]map[string]any
}

func (p *openCodeV2Projection) event(event map[string]any) []map[string]any {
	typ, data := str(event, "type"), object(event["data"])
	sessionID, messageID := str(data, "sessionID"), str(data, "assistantMessageID")
	wrap := func(typ string, properties map[string]any) []map[string]any {
		return []map[string]any{{"type": typ, "properties": properties}}
	}
	switch typ {
	case "permission.asked":
		return wrap(typ, openCodeV2Permission(data))
	case "permission.replied":
		return wrap(typ, data)
	case "form.created":
		return wrap("question.asked", object(data["form"]))
	case "form.replied":
		return wrap("question.replied", map[string]any{"requestID": data["id"], "sessionID": sessionID})
	case "form.cancelled":
		return wrap("question.rejected", map[string]any{"requestID": data["id"], "sessionID": sessionID})
	case "session.inbox.delivered":
		return wrap("message.updated", map[string]any{"info": map[string]any{"id": data["inboxID"], "sessionID": sessionID, "role": "user"}})
	case "session.execution.started":
		return wrap("session.status", map[string]any{"sessionID": sessionID, "status": map[string]any{"type": "busy"}})
	case "session.execution.succeeded", "session.execution.interrupted":
		return wrap("session.idle", map[string]any{"sessionID": sessionID, "outcome": typ})
	case "session.execution.failed":
		return wrap("session.error", map[string]any{"sessionID": sessionID, "error": openCodeV2Error(object(data["error"]))})
	case "session.compaction.ended":
		return wrap("session.compacted", data)
	case "session.step.started":
		model := object(data["model"])
		info := map[string]any{"id": messageID, "sessionID": sessionID, "role": "assistant", "time": map[string]any{"created": data["started"]}, "providerID": model["providerID"], "modelID": model["id"], "variant": model["variant"]}
		p.infos[messageID] = info
		return wrap("message.updated", map[string]any{"info": info})
	case "session.step.ended", "session.step.failed":
		info := copyOpenCodeMap(p.infos[messageID])
		if info == nil {
			return nil
		}
		time := copyOpenCodeMap(object(info["time"]))
		time["completed"] = event["created"]
		info["time"], info["finish"], info["tokens"] = time, data["finish"], data["tokens"]
		if data["error"] != nil {
			info["error"] = openCodeV2Error(object(data["error"]))
		}
		p.infos[messageID] = info
		return wrap("message.updated", map[string]any{"info": info})
	}
	if !strings.HasPrefix(typ, "session.text.") && !strings.HasPrefix(typ, "session.reasoning.") && !strings.HasPrefix(typ, "session.tool.") {
		return nil
	}
	if sessionID == "" || messageID == "" {
		return nil
	}
	kind := strings.Split(typ, ".")[1]
	partID := fmt.Sprintf("%s/%v", kind, data["ordinal"])
	if kind == "tool" {
		partID = "tool/" + str(data, "id")
	}
	key := messageID + "/" + partID
	part := copyOpenCodeMap(p.parts[key])
	if strings.HasSuffix(typ, ".delta") {
		if kind == "tool" {
			return nil
		}
		return wrap("message.part.delta", map[string]any{"sessionID": sessionID, "messageID": messageID, "partID": partID, "field": "text", "delta": data["delta"]})
	}
	if kind == "tool" {
		if typ == "session.tool.input.started" {
			part = map[string]any{"id": partID, "sessionID": sessionID, "messageID": messageID, "type": kind, "callID": data["id"], "tool": data["name"], "state": map[string]any{"status": "pending", "input": map[string]any{}}}
		} else if part != nil {
			state := copyOpenCodeMap(object(part["state"]))
			switch typ {
			case "session.tool.called":
				state["status"], state["input"] = "running", data["input"]
			case "session.tool.progress":
				state["metadata"] = data["metadata"]
			case "session.tool.success":
				state["status"], state["output"], state["metadata"] = "completed", contentText(data["content"]), data["metadata"]
			case "session.tool.failed":
				state["status"], state["error"], state["metadata"] = "error", openCodeV2Error(object(data["error"])), data["metadata"]
			default:
				return nil
			}
			part["state"] = state
		}
	} else if strings.HasSuffix(typ, ".started") {
		part = map[string]any{"id": partID, "sessionID": sessionID, "messageID": messageID, "type": kind, "text": ""}
	} else if strings.HasSuffix(typ, ".ended") {
		if part == nil {
			part = map[string]any{"id": partID, "sessionID": sessionID, "messageID": messageID, "type": kind}
		}
		part["text"], part["time"] = data["text"], map[string]any{"end": event["created"]}
	}
	if part == nil {
		return nil
	}
	p.parts[key] = part
	// The consumer appends deltas to its own copy; never share mutable parts.
	return wrap("message.part.updated", map[string]any{"part": copyOpenCodeMap(part)})
}

func copyOpenCodeMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	copy := make(map[string]any, len(value))
	for key, item := range value {
		copy[key] = item
	}
	return copy
}
