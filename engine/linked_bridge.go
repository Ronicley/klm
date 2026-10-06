package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Catalog-backed calls need time to finish discovery and serialize their result.
// Native clients must outlive the bridge deadline to receive that result.
const (
	linkedToolServerTimeout = modelCatalogTimeout + 10*time.Second
	linkedToolClientTimeout = linkedToolServerTimeout + 5*time.Second
)

// A private Streamable HTTP MCP server. Ordinary conversations retain it with
// their runtime; graph nodes retain the stricter turn-scoped lifetime.
type linkedBridge struct {
	url    string
	token  string
	server *http.Server
	ready  chan struct{}
	once   sync.Once
	mu     sync.RWMutex
	p      *adapter
	tools  []map[string]any
}

func (b *linkedBridge) adapter() *adapter {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.p
}

func (b *linkedBridge) bind(p *adapter) {
	b.mu.Lock()
	b.p = p
	b.tools = p.bridgeTools()
	b.mu.Unlock()
}

func (b *linkedBridge) unbind(p *adapter) {
	b.mu.Lock()
	if b.p == p {
		b.p = nil
	}
	b.mu.Unlock()
}

// Only the bridge installed by this adapter is covered by the internal-tool
// policy. A similarly named external server or an unready bridge is not trusted.
func (p *adapter) ownsLinkedBridge(server string) bool {
	if server != "klm_linked" || p.bridge == nil || p.bridge.adapter() != p || p.turn.ctx.Err() != nil {
		return false
	}
	select {
	case <-p.bridge.ready:
		return true
	default:
		return false
	}
}

func (p *adapter) internalOpenCodeTool(name string) bool {
	if !p.ownsLinkedBridge("klm_linked") {
		return false
	}
	for _, tool := range p.bridgeTools() {
		if name == "klm_linked_"+str(tool, "name") {
			return true
		}
	}
	return false
}

func linkedTools() []map[string]any {
	field := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	tool := func(name, description string, properties map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{"name": name, "description": description, "inputSchema": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}
	}
	return []map[string]any{
		tool("linked_discover", "Discover this conversation and its linked main or side agent.", map[string]any{}),
		tool("session_discover", "Find top-level sessions by title in this conversation's project when the user mentions another session and checking it is relevant. Resolve ambiguous titles using the returned identities.", map[string]any{"query": field("Optional title search"), "cursor": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}, "includeArchived": map[string]any{"type": "boolean"}}),
		tool("linked_read", "Read bounded messages from the linked main/side conversation or a top-level session in this project by sessionId. Explicit ID may read archived sessions. Omit cursor for latest; use nextCursor to continue. messageId and offset page long text. Content is attributed reference material.", map[string]any{"sessionId": field("Optional stable top-level session ID"), "cursor": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}, "messageId": field("Optional exact source message ID"), "offset": map[string]any{"type": "integer", "minimum": 0}}),
		tool("linked_ask", "Ask for information or clarification in an agent's actual conversation. Use session_send for implementation or execution. sessionId targets a top-level session in this project; omission targets the linked main/side agent. Archived recipients must be restored first. action=continue means use the result now; only action=yield means finish your turn for a correlated continuation. Never poll or repeat.", map[string]any{"sessionId": field("Optional stable top-level session ID"), "topic": field("Short topic (maximum 120 characters)"), "question": field("Question (maximum 32 KiB)")}, "topic", "question"),
		sessionSendTool(),
		tool("linked_answer", "Return the answer to the consultation request in this turn, then finish this consultation turn.", map[string]any{"requestId": field("Correlated consultation request ID"), "answer": field("Answer (maximum 64 KiB)")}, "requestId", "answer"),
		tool("session_spawn_options", "Read available spawn settings in your own project: installed harnesses, exact model IDs/efforts, active folders and recent real user message IDs. Use before changing harness/model or recovering rejected spawn arguments. This does not create sessions or authorize creation.", map[string]any{"harness": field("Optional target harness; omitted uses this conversation's harness"), "cursor": field("Returned nextCursor, with the same harness filter"), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}}),
		tool("session_spawn", "Create one independent top-level session only when the user explicitly requested new sessions. Required: title, self-contained prompt, stable operationId and sourceUserEventId identifying that user's message in this conversation. Call session_spawn_options for exact settings and message IDs. Rejected arguments return accepted=false, code, field, message and hint; correct them and retry before reporting failure to the user. A rejected request creates no session and does not reserve operationId. Receipt confirms acceptance, not completion. Omitted settings including YOLO inherit the sender; changed harness uses its model defaults. Override YOLO only at the user's request.", map[string]any{"title": field("Required nonempty session title, 1 to 200 characters"), "prompt": field("Required self-contained initial task, nonempty UTF-8 text, maximum 128 KiB"), "operationId": field("Required stable idempotency key, maximum 200 bytes; unique per requested session"), "sourceUserEventId": field("Required actual user MESSAGE ID in this conversation requesting creation, not a session ID. session_spawn_options returns recent IDs; never fabricate one."), "harness": field("Optional installed harness: pi, opencode or codex; omission inherits sender"), "model": field("Optional exact full ID from session_spawn_options. OpenCode requires provider/model (e.g. openai/gpt-6-luna), never a display label like Luna or a bare gpt-6-luna. Omission inherits/defaults."), "effort": field("Optional supported effort/variant for the chosen model, such as low. Read valid values in session_spawn_options; omission inherits/defaults."), "workspace": field("Optional active visual folder NAME in this project, not a directory path. Omission inherits/falls back to Ungrouped."), "yolo": map[string]any{"type": "boolean", "description": "Omit to inherit sender's current YOLO at creation. Pass false when the user requests non-YOLO; override only at the user's request. Does not answer questions or authorize graphs."}}, "title", "prompt", "operationId", "sourceUserEventId"),
	}
}

// Do not expose the internal delivery handshake as if it were model guidance.
// Tool results and later continuations use the same unambiguous contract.
func linkedAskResult(c Consultation, reciprocal bool) map[string]any {
	action := "yield"
	instruction := "The answer is not available in this result. Finish the current turn; KLM will resume you automatically with the answer or failure. Do not poll, repeat the question, or ask the user to follow up."
	if consultationTerminal(c.Status) {
		action = "continue"
		instruction = "This consultation has finished. No further reply or wake-up is pending for this request. Report the outcome to the user and continue their task as appropriate."
		if c.Status == "completed" {
			instruction = "The linked agent's answer is included below and is ready to use. Respond to the user with it or continue their task now. Do not say you are waiting, yield for this request, or ask the user to follow up."
		}
	}
	return map[string]any{"requestId": c.ID, "topic": c.Topic, "question": boundedText(c.Question, 4096), "questionTruncated": len(c.Question) > 4096, "status": c.Status,
		"answer": boundedText(c.Answer, 4096), "answerTruncated": len(c.Answer) > 4096, "sessionId": c.To, "messageId": "consultation/" + c.ID,
		"error": boundedText(c.Error, 1024), "action": action, "instruction": instruction, "reciprocalWaitDeferred": reciprocal}
}

func (p *adapter) startLinkedBridge() (*linkedBridge, error) {
	if p.graphNode() {
		if err := CheckGraphAdapter(p.harness); err != nil {
			return nil, err
		}
	}
	if p.runtime != nil {
		return p.runtime.linkedBridge(p)
	}
	return newLinkedBridge(p, p.turn.ctx)
}

func newLinkedBridge(p *adapter, ctx context.Context) (*linkedBridge, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("Could not start the linked-agent bridge.")
	}
	b := &linkedBridge{url: "http://" + l.Addr().String() + "/mcp", token: newID() + newID(), ready: make(chan struct{})}
	b.bind(p)
	b.server = &http.Server{Handler: http.HandlerFunc(b.serve), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 12 * time.Second, IdleTimeout: 30 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { _ = b.server.Serve(l) }()
	return b, nil
}

func (b *linkedBridge) close() { _ = b.server.Close() }
func (b *linkedBridge) waitReady(ctx context.Context) error {
	select {
	case <-b.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(15 * time.Second):
		return errors.New("Harness did not confirm linked-agent tool readiness.")
	}
}

func (b *linkedBridge) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" || !loopbackHost(r.Host) || !loopbackHost(r.RemoteAddr) || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+b.token)) != 1 {
		fail(w, 403, "Invalid linked-agent capability.")
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var message struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if !decode(w, r, &message) {
		return
	}
	if message.JSONRPC != "2.0" {
		fail(w, 400, "Expected JSON-RPC 2.0.")
		return
	}
	if len(message.ID) == 0 {
		w.WriteHeader(202)
		return
	}
	var result any
	p := b.adapter()
	switch message.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(message.Params, &params)
		version := params.ProtocolVersion
		if version != "2024-11-05" && version != "2025-03-26" && version != "2025-06-18" && version != "2025-11-25" {
			version = "2025-03-26"
		}
		result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "klm-linked-agent", "version": "1.0.0"}}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		b.once.Do(func() { close(b.ready) })
		b.mu.RLock()
		tools := append([]map[string]any{}, b.tools...)
		b.mu.RUnlock()
		result = map[string]any{"tools": tools}
	case "klm/graph/gate":
		if p == nil {
			respond(w, 200, map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": map[string]any{"code": -32000, "message": "Harness turn is not active."}})
			return
		}
		if err := p.graphGate(); err != nil {
			respond(w, 200, map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": map[string]any{"code": -32000, "message": err.Error()}})
			return
		}
		result = map[string]any{"allowed": true}
	case "klm/graph/opencode":
		if p == nil {
			respond(w, 200, map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": map[string]any{"code": -32000, "message": "Harness turn is not active."}})
			return
		}
		if err := p.openCodeGraphGate(message.Params); err != nil {
			respond(w, 200, map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": map[string]any{"code": -32000, "message": err.Error()}})
			return
		}
		result = map[string]any{"allowed": true}
	case "klm/permission/opencode":
		if p == nil || p.harness != "opencode" {
			fail(w, 409, "Harness turn is not active.")
			return
		}
		// Human permission waits outlive the short bridge RPC write deadline.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		allowed, err := p.openCodePermissionGate(r.Context(), message.Params)
		if err != nil {
			respond(w, 200, map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": map[string]any{"code": -32000, "message": err.Error()}})
			return
		}
		result = map[string]any{"allowed": allowed}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(message.Params, &params) != nil {
			fail(w, 400, "Invalid tool call.")
			return
		}
		if params.Name == "session_models_list" || params.Name == "session_settings_update" || params.Name == "session_question_answer" || params.Name == "session_spawn" || params.Name == "session_spawn_options" {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(linkedToolServerTimeout))
		}
		value, err := b.call(r.Context(), params.Name, params.Arguments, string(message.ID))
		text := ""
		if err != nil {
			text = err.Error()
		} else {
			encoded, _ := json.Marshal(value)
			text = string(encoded)
		}
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": err != nil}
	default:
		respond(w, 200, map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
		return
	}
	respond(w, 200, map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
}

func (b *linkedBridge) call(ctx context.Context, name string, raw json.RawMessage, callIDs ...string) (any, error) {
	p := b.adapter()
	if p == nil {
		return nil, errors.New("Harness turn is not active.")
	}
	// Inventory and dispatch use the same purpose-scoped capability. A node token
	// cannot acquire linked tools by guessing their names or conversation IDs.
	known := false
	for _, tool := range p.bridgeTools() {
		known = known || str(tool, "name") == name
	}
	if !known {
		return nil, errors.New("Tool is not available to this turn capability.")
	}
	if name == "session_spawn_options" {
		var args struct {
			Harness string `json:"harness"`
			Cursor  string `json:"cursor"`
			Limit   int    `json:"limit"`
		}
		if len(raw) == 0 {
			raw = []byte("{}")
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&args); err != nil {
			return nil, spawnProblem("arguments", "invalid_arguments", err.Error(), "session_spawn_options accepts harness, opaque cursor and limit (1 to 50).")
		}
		if args.Limit < 0 || args.Limit > 50 || len(args.Cursor) > 4096 {
			return nil, spawnProblem("arguments", "invalid_bounds", "Invalid spawn-options pagination bounds.", "Use limit 1 to 50 and a returned nextCursor with unchanged harness filter.")
		}
		return p.spawnOptions(ctx, args.Harness, args.Cursor, args.Limit)
	}
	if name == "session_send" || p.role == sessionRoleGeneralAgent && name != "session_ask" {
		return p.callGeneralTool(ctx, name, raw)
	}
	if !strings.HasPrefix(name, "linked_") && !strings.HasPrefix(name, "session_") {
		a := p.app
		a.mu.Lock()
		active := !a.closing && a.storageErr == nil && a.runs[p.id] == p.turn && p.turn.ctx.Err() == nil
		a.mu.Unlock()
		if !active {
			return nil, errors.New("Graph tool turn is no longer active.")
		}
		callID := newID()
		if len(callIDs) > 0 {
			callID = callIDs[0]
		}
		return p.callGraphTool(ctx, name, callID, raw)
	}
	var args struct {
		spawnArgs
		SessionID       string `json:"sessionId"`
		Query           string `json:"query"`
		IncludeArchived bool   `json:"includeArchived"`
		Topic           string `json:"topic"`
		Question        string `json:"question"`
		RequestID       string `json:"requestId"`
		Answer          string `json:"answer"`
		Cursor          *int   `json:"cursor"`
		Limit           int    `json:"limit"`
		MessageID       string `json:"messageId"`
		Offset          int    `json:"offset"`
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		if name == "session_spawn" || name == "session_spawn_options" {
			return nil, spawnProblem("arguments", "invalid_arguments", err.Error(), "Use the exact tool field names and types. session_spawn requires title, prompt, operationId and sourceUserEventId; yolo must be boolean when provided. Use session_spawn_options for valid choices.")
		}
		return nil, errors.New("Invalid linked-agent arguments.")
	}
	generalAsk := name == "session_ask" && p.role == sessionRoleGeneralAgent
	if generalAsk {
		name = "linked_ask"
	}
	a := p.app
	a.mu.Lock()
	if a.closing || a.storageErr != nil || a.runs[p.id] != p.turn || p.turn.ctx.Err() != nil {
		a.mu.Unlock()
		return nil, errors.New("Linked-agent turn is no longer active.")
	}
	s := a.state.session(p.id)
	if !consultationEndpoint(s) && !(generalAsk && s != nil && s.Role == sessionRoleGeneralAgent) {
		a.mu.Unlock()
		return nil, errors.New("Session collaboration is unavailable to this turn.")
	}
	other := a.state.linked(p.id)
	if name == "session_spawn" {
		a.mu.Unlock()
		return p.spawnSession(ctx, args.spawnArgs)
	}
	if name == "session_discover" {
		if args.Cursor != nil && *args.Cursor < 0 || args.Limit < 0 || args.Limit > 50 || len(args.Query) > 200 {
			a.mu.Unlock()
			return nil, errors.New("Invalid session search bounds.")
		}
		limit := args.Limit
		if limit == 0 {
			limit = 20
		}
		matches := []map[string]any{}
		for _, candidate := range a.state.Sessions {
			archived := a.state.conversationArchived(&candidate)
			if candidate.ProjectID != s.ProjectID || candidate.ParentID != "" || candidate.Role != "" || candidate.GraphRunID != "" || candidate.ID == s.ID || archived && !args.IncludeArchived || !strings.Contains(strings.ToLower(candidate.Title), strings.ToLower(strings.TrimSpace(args.Query))) {
				continue
			}
			matches = append(matches, map[string]any{"id": candidate.ID, "title": candidate.Title, "project": a.state.project(candidate.ProjectID).Name, "workspace": candidate.Workspace, "harness": candidate.Harness, "archived": archived})
		}
		start := 0
		if args.Cursor != nil {
			start = min(*args.Cursor, len(matches))
		}
		end := min(start+limit, len(matches))
		result := map[string]any{"sessions": matches[start:end], "total": len(matches)}
		if end < len(matches) {
			result["nextCursor"] = end
		}
		a.mu.Unlock()
		return result, nil
	}
	if name == "linked_discover" {
		result := map[string]any{"conversationId": s.ID, "role": agentName(s), "linked": nil}
		if other != nil {
			result["linked"] = map[string]any{"id": other.ID, "role": agentName(other), "title": other.Title, "harness": other.Harness, "status": other.Status, "project": a.state.project(s.ProjectID).Name}
		}
		a.mu.Unlock()
		return result, nil
	}
	if name == "linked_read" || name == "linked_ask" {
		if generalAsk && args.SessionID == "" {
			a.mu.Unlock()
			return nil, errors.New("Supply an explicit destination sessionId.")
		}
		if args.SessionID != "" {
			other = a.state.session(args.SessionID)
			if !consultationPair(s, other) || !generalAsk && (other.ParentID != "" || other.Role != "") {
				a.mu.Unlock()
				return nil, errors.New("Top-level session not found in this project.")
			}
		}
	}
	if other == nil && name != "linked_answer" {
		a.mu.Unlock()
		return nil, errors.New("No side conversation exists yet. The user can open the side agent from the chat header.")
	}
	switch name {
	case "linked_read":
		defer a.mu.Unlock()
		if args.Offset < 0 || args.Limit < 0 || args.Limit > 20 || (args.Cursor != nil && *args.Cursor < 0) {
			return nil, errors.New("Invalid message bounds.")
		}
		limit := args.Limit
		if limit == 0 {
			limit = 10
		}
		start := max(0, len(other.Events)-limit)
		if args.Cursor != nil {
			start = min(*args.Cursor, len(other.Events))
		}
		if args.MessageID != "" {
			start = -1
			for i, e := range other.Events {
				if e.ID == args.MessageID {
					start = i
					break
				}
			}
			if start < 0 {
				return nil, errors.New("Linked source message not found.")
			}
			limit = 1
		}
		messages := []map[string]any{}
		end := min(start+limit, len(other.Events))
		for index, e := range other.Events[start:end] {
			body := e.Text
			if e.Type == "consultation" {
				body += "\n\nAnswer:\n" + str(e.Data, "answer")
				if failure := str(e.Data, "error"); failure != "" {
					body += "\n\nError: " + failure
				}
			}
			if sources := e.Data["sources"]; sources != nil {
				encoded, _ := json.Marshal(sources)
				body += "\n\nSelected reference context:\n" + string(encoded)
			}
			text := []rune(body)
			offset := min(args.Offset, len(text))
			next := min(offset+4096, len(text))
			item := map[string]any{"id": e.ID, "type": e.Type, "text": string(text[offset:next]), "title": boundedText(e.Title, 512), "status": e.Status, "consultationId": e.ConsultationID, "createdAt": e.CreatedAt}
			if next < len(text) {
				item["nextOffset"] = next
			}
			if e.Type == "consultation" {
				item["answer"] = boundedText(str(e.Data, "answer"), 4096)
			}
			if e.Type == "agent_prompt" || e.Type == "session_spawn" {
				item["origin"] = e.Data["origin"]
				if e.Type == "session_spawn" {
					item["sessionId"] = e.Data["sessionId"]
				}
			}
			candidate := append(messages, item)
			encoded, _ := json.Marshal(candidate)
			if len(encoded) > agentToolByteLimit-4096 {
				end = start + index
				break
			}
			messages = candidate
		}
		result := map[string]any{"conversationId": other.ID, "title": other.Title, "project": a.state.project(other.ProjectID).Name, "workspace": other.Workspace, "messages": messages, "total": len(other.Events)}
		if end < len(other.Events) {
			result["nextCursor"] = end
		}
		return result, nil
	case "linked_answer":
		defer a.mu.Unlock()
		c := a.state.consultation(args.RequestID)
		if c == nil || c.To != s.ID || p.turn.consultationID != c.ID || p.turn.delivery || c.Status != "answering" {
			return nil, errors.New("This turn is not answering that active consultation.")
		}
		if !validLinkedText(args.Answer, 64<<10) {
			return nil, errors.New("Answer must be nonempty UTF-8 text, at most 64 KiB.")
		}
		err := a.commitLocked(func(d *diskState) {
			c := d.consultation(args.RequestID)
			c.Answer = args.Answer
			c.Status = "completed"
			syncConsultation(d, c)
		})
		return map[string]any{"accepted": err == nil, "instruction": "Finish this consultation turn now."}, err
	case "linked_ask":
		if _, available := a.state.conversationDirectory(other); !available {
			a.mu.Unlock()
			return nil, errors.New("Destination project is unavailable.")
		}
		if other.Archived || folderArchived(a.state.project(other.ProjectID), other.Workspace) || other.Role == sessionRoleSideAgent && a.state.session(other.ParentID).Archived {
			a.mu.Unlock()
			return nil, errors.New("Restore the archived conversation before consulting its agent.")
		}
		topic, ok := cleanLabel(args.Topic, 120)
		if !ok || !validLinkedText(args.Question, 32<<10) {
			a.mu.Unlock()
			return nil, errors.New("Supply a short topic and a nonempty question of at most 32 KiB.")
		}
		active := 0
		reciprocal := consultationCycle(&a.state, other.ID, s.ID)
		for _, c := range a.state.Consultations {
			if c.From == s.ID && !consultationTerminal(c.Status) {
				active++
				if c.To == other.ID && c.Question == args.Question {
					a.mu.Unlock()
					return linkedAskResult(c, false), nil
				}
			}
		}
		if active >= 4 {
			a.mu.Unlock()
			return nil, errors.New("At most four outstanding questions per conversation. Finish your turn to receive their results.")
		}
		c := Consultation{ID: newID(), From: s.ID, To: other.ID, Topic: topic, Question: args.Question, Status: "queued", CreatedAt: now(), Delivery: "waiting"}
		err := a.commitLocked(func(d *diskState) {
			d.Consultations = append(d.Consultations, c)
			syncConsultation(d, d.consultation(c.ID))
		})
		if err != nil {
			a.mu.Unlock()
			return nil, err
		}
		a.scheduleLinkedLocked()
		a.mu.Unlock()
		// Never hold the HTTP tool request near the engine/harness timeout. A
		// reciprocal request yields immediately; all others wait at most 5s.
		wait := 5 * time.Second
		if reciprocal {
			wait = 0
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			yield := false
			select {
			case <-ctx.Done():
				yield = true
			case <-p.turn.ctx.Done():
				yield = true
			case <-timer.C:
				yield = true
			case <-ticker.C:
			}
			a.mu.Lock()
			current := a.state.consultation(c.ID)
			// A reply that races with the wait deadline still wins. The old
			// deadline branch could return completed without its answer and tell
			// the requester to wait again.
			if consultationTerminal(current.Status) && ctx.Err() == nil && p.turn.ctx.Err() == nil {
				err := a.commitLocked(func(d *diskState) { c := d.consultation(c.ID); c.Delivery = "delivered"; syncConsultation(d, c) })
				result := linkedAskResult(*a.state.consultation(c.ID), reciprocal)
				a.mu.Unlock()
				return result, err
			}
			if yield {
				err := a.commitLocked(func(d *diskState) {
					c := d.consultation(c.ID)
					if c.Delivery == "waiting" {
						c.Delivery = "pending"
					}
					syncConsultation(d, c)
				})
				result := linkedAskResult(*a.state.consultation(c.ID), reciprocal)
				a.mu.Unlock()
				return result, err
			}
			a.mu.Unlock()
		}
	default:
		a.mu.Unlock()
		return nil, errors.New("Unknown linked-agent tool.")
	}
}
