package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// A call boundary, not the lifetime of an MCP server or of a task returned by it.
// "admitted" deliberately does not claim that bytes were sent: OpenCode's before
// hook precedes native permission checks. Without a response or proof of no send,
// interruption of an admitted call is uncertain, never successful cancellation.
type graphMCPCallKey struct{ Session, Turn, Call string }
type graphMCPCall struct {
	Session string `json:"sessionId"`
	Turn    string `json:"turnId,omitempty"`
	Call    string `json:"callId"`
	Tool    string `json:"tool"`
	State   string `json:"state"`
}

func (b *graphAdapterBinding) admitMCPCallLocked(key graphMCPCallKey, tool string) {
	if b.mcpCalls == nil {
		b.mcpCalls = map[graphMCPCallKey]graphMCPCall{}
	}
	if _, exists := b.mcpCalls[key]; !exists {
		b.mcpCalls[key] = graphMCPCall{Session: key.Session, Turn: key.Turn, Call: key.Call, Tool: tool, State: "admitted"}
	}
}

func (b *graphAdapterBinding) endMCPCallLocked(key graphMCPCallKey, state string) {
	call, exists := b.mcpCalls[key]
	if !exists {
		return
	} // A rejected before hook did not admit/send a call.
	if call.State == "response" || call.State == "not_started" {
		return
	}
	// A late real response can resolve an earlier local cancellation/transport doubt.
	call.State = state
	b.mcpCalls[key] = call
	b.maybeStopLocked()
}

func (b *graphAdapterBinding) maybeStopLocked() {
	pending := false
	for _, call := range b.mcpCalls {
		pending = pending || call.State == "admitted"
	}
	if b.choiceSettled && !pending {
		b.stopOnce.Do(func() { close(b.stop) })
	}
}

func (b *graphAdapterBinding) unresolvedMCPCallsLocked() []graphMCPCall {
	var calls []graphMCPCall
	for _, call := range b.mcpCalls {
		if call.State == "admitted" || call.State == "uncertain" {
			calls = append(calls, call)
		}
	}
	sort.Slice(calls, func(i, j int) bool {
		return calls[i].Session+"/"+calls[i].Turn+"/"+calls[i].Call < calls[j].Session+"/"+calls[j].Turn+"/"+calls[j].Call
	})
	return calls
}

func mcpCallUncertainty(calls []graphMCPCall) error {
	if len(calls) == 0 {
		return nil
	}
	labels := make([]string, 0, len(calls))
	for _, call := range calls {
		labels = append(labels, fmt.Sprintf("%s session=%s turn=%s call=%s (%s)", call.Tool, call.Session, call.Turn, call.Call, call.State))
	}
	return &graphUnconfirmedError{cause: errors.New("MCP call completion is unconfirmed: " + strings.Join(labels, "; ") + ". Closing the client does not confirm cancellation; detached tasks after a returned response are outside this boundary.")}
}

func (p *adapter) openCodeCallNotStarted(session, call string) {
	if !p.graphNode() || call == "" {
		return
	}
	p.graph.mu.Lock()
	defer p.graph.mu.Unlock()
	p.graph.endMCPCallLocked(graphMCPCallKey{Session: session, Call: call}, "not_started")
}

func (p *adapter) graphOwnsNativeSession(id string) bool {
	if !p.graphNode() || id == "" {
		return false
	}
	p.graph.mu.Lock()
	defer p.graph.mu.Unlock()
	_, child := p.graph.codexChildren[id]
	return id == p.graph.nativeSession || p.graph.nativeSessions[id] || child
}

func (p *adapter) graphMCPPending() bool {
	if !p.graphNode() {
		return false
	}
	p.graph.mu.Lock()
	defer p.graph.mu.Unlock()
	for _, call := range p.graph.mcpCalls {
		if call.State == "admitted" {
			return true
		}
	}
	return false
}

func (p *adapter) observeOpenCodeMCPPart(part map[string]any) {
	if !p.graphNode() || str(part, "type") != "tool" {
		return
	}
	key := graphMCPCallKey{Session: str(part, "sessionID"), Call: str(part, "callID")}
	status := str(object(part["state"]), "status")
	if status != "error" {
		return
	} // Only the awaited plugin after hook proves response.
	p.graph.mu.Lock()
	defer p.graph.mu.Unlock()
	p.graph.endMCPCallLocked(key, "uncertain")
}

// Observe every thread carried by the dedicated app-server, before root-only UI
// filtering. Thread+turn+item keys keep parallel/subagent calls independent.
func (p *adapter) observeCodexMCP(method string, params map[string]any) {
	if !p.graphNode() || (method != "item/started" && method != "item/completed") {
		return
	}
	item := object(params["item"])
	p.noteCodexChildren(item)
	if str(item, "type") != "mcpToolCall" || str(item, "server") == "klm_linked" {
		return
	}
	key := graphMCPCallKey{Session: str(params, "threadId"), Turn: str(params, "turnId"), Call: str(item, "id")}
	if key.Session == "" || key.Turn == "" || key.Call == "" {
		return
	}
	responseReturned := false
	if method == "item/completed" {
		content, err := nativeField(item["result"], "content")
		if err == nil && content != nil {
			if source, ok := content.(*protocolContent); !ok || source.kind != 'n' {
				responseReturned = true
			}
		}
	}
	b := p.graph
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.nativeSessions == nil {
		b.nativeSessions = map[string]bool{}
	}
	b.nativeSessions[key.Session] = true
	if b.codexTurns == nil {
		b.codexTurns = map[string]map[string]bool{}
	}
	if b.codexTurns[key.Session] == nil {
		b.codexTurns[key.Session] = map[string]bool{}
	}
	b.codexTurns[key.Session][key.Turn] = true
	b.admitMCPCallLocked(key, str(item, "server")+"/"+str(item, "tool"))
	if method == "item/completed" {
		state := "uncertain"
		// A result envelope (also isError=true) is a real returned tool response.
		// Failed/cancelled + error text alone cannot distinguish remote RPC errors
		// from local aborts, timeouts or disconnected transports in this protocol.
		if responseReturned {
			state = "response"
		}
		b.endMCPCallLocked(key, state)
	}
}

func (p *adapter) noteCodexChildren(item map[string]any) {
	if !p.graphNode() || str(item, "type") != "collabAgentToolCall" {
		return
	}
	ids, err := nativeIDs(item["receiverThreadIds"])
	p.graph.mu.Lock()
	defer p.graph.mu.Unlock()
	if err != nil {
		if p.graph.codexCoverageUnknown == nil {
			p.graph.codexCoverageUnknown = map[string]bool{}
		}
		p.graph.codexCoverageUnknown[""] = true
		return
	}
	if p.graph.codexChildren == nil {
		p.graph.codexChildren = map[string]bool{}
	}
	for _, id := range ids {
		if id != "" && id != p.graph.nativeSession {
			p.graph.codexChildren[id] = p.graph.codexChildren[id] || str(item, "tool") == "spawnAgent"
		}
	}
}

func (p *adapter) codexChildIDs() []string {
	p.graph.mu.Lock()
	defer p.graph.mu.Unlock()
	var ids []string
	for id := range p.graph.codexChildren {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (p *adapter) auditCodexChildTurn(thread string, turn map[string]any) {
	id := str(turn, "id")
	p.graph.mu.Lock()
	known := p.graph.codexChildren[thread] || p.graph.codexTurns[thread][id]
	if number, ok := turn["startedAt"].(json.Number); ok {
		seconds, err := number.Int64()
		known = known || (err == nil && seconds >= p.graph.boundUnix)
	}
	p.graph.mu.Unlock()
	if !known {
		// A legacy/resumed child's turn can lack timestamps and live observation.
		// Do not silently claim coverage of an unreturned MCP item in that case.
		err := protocolArrayEach(turn["items"], func(value any) error {
			item := object(value)
			content, readErr := nativeField(item["result"], "content")
			if str(item, "type") == "mcpToolCall" && str(item, "server") != "klm_linked" && (content == nil || readErr != nil) {
				p.graph.mu.Lock()
				if p.graph.codexCoverageUnknown == nil {
					p.graph.codexCoverageUnknown = map[string]bool{}
				}
				p.graph.codexCoverageUnknown[thread] = true
				p.graph.mu.Unlock()
			}
			return nil
		})
		if err != nil {
			p.graph.mu.Lock()
			if p.graph.codexCoverageUnknown == nil {
				p.graph.codexCoverageUnknown = map[string]bool{}
			}
			p.graph.codexCoverageUnknown[thread] = true
			p.graph.mu.Unlock()
		}
		return
	}
	err := protocolArrayEach(turn["items"], func(value any) error {
		item := object(value)
		p.observeCodexMCP("item/completed", map[string]any{"threadId": thread, "turnId": id, "item": item})
		return nil
	})
	if err != nil {
		p.graph.mu.Lock()
		if p.graph.codexCoverageUnknown == nil {
			p.graph.codexCoverageUnknown = map[string]bool{}
		}
		p.graph.codexCoverageUnknown[thread] = true
		p.graph.mu.Unlock()
	}
}
