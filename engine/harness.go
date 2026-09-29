package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type Harness struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}

type binary struct {
	path string
	args []string
}

func executable(path string) (string, bool) {
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", false
	}
	// Never execute Windows npm shell shims, or inspect/evaluate their contents.
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(resolved), ".exe") {
		return "", false
	}
	resolved, err = filepath.Abs(resolved)
	return resolved, err == nil
}

func discoverHarnesses() ([]Harness, map[string]binary) {
	harnesses := []Harness{{ID: "pi", Name: "Pi"}, {ID: "opencode", Name: "OpenCode"}, {ID: "codex", Name: "Codex"}}
	binaries := map[string]binary{}
	npmRoots := []string{}
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		npmRoots = append(npmRoots, filepath.Join(appdata, "npm", "node_modules"))
	}
	for _, shim := range []string{"pi", "opencode"} {
		if path, err := exec.LookPath(shim); err == nil {
			npmRoots = append(npmRoots, filepath.Join(filepath.Dir(path), "node_modules"))
		}
	}
	for i := range harnesses {
		h := &harnesses[i]
		override := os.Getenv("KLM_" + strings.ToUpper(h.ID) + "_BIN")
		candidates := []string{}
		if override != "" {
			candidates = append(candidates, override)
		} else {
			if h.ID == "pi" {
				for _, root := range npmRoots {
					candidates = append(candidates, filepath.Join(root, "@earendil-works", "pi-coding-agent", "dist", "cli.js"))
				}
			} else if h.ID == "opencode" {
				for _, root := range npmRoots {
					candidates = append(candidates, filepath.Join(root, "opencode-ai", "bin", "opencode.exe"))
				}
			} else if local := os.Getenv("LOCALAPPDATA"); local != "" {
				candidates = append(candidates, filepath.Join(local, "Programs", "OpenAI", "Codex", "bin", "codex.exe"))
			}
			if runtime.GOOS == "windows" {
				candidates = append(candidates, h.ID+".exe")
			} else {
				candidates = append(candidates, h.ID)
			}
		}
		for _, candidate := range candidates {
			if h.ID == "pi" && strings.EqualFold(filepath.Ext(candidate), ".js") {
				info, err := os.Stat(candidate)
				if err != nil || !info.Mode().IsRegular() {
					continue
				}
				node, ok := executable("node")
				if !ok {
					continue
				}
				path, err := filepath.Abs(candidate)
				if err != nil {
					continue
				}
				binaries[h.ID] = binary{path: node, args: []string{path}}
				h.Available = true
				break
			}
			if path, ok := executable(candidate); ok {
				binaries[h.ID] = binary{path: path}
				h.Available = true
				break
			}
		}
		if !h.Available {
			h.Error = "Executable not found. Install the harness or configure KLM_" + strings.ToUpper(h.ID) + "_BIN and restart the engine."
			if h.ID == "pi" {
				h.Error += " Pi's cli.js also requires Node.js on PATH."
			}
		}
	}
	return harnesses, binaries
}

func commandArgs(s Session, native nativeSession) []string {
	var args []string
	switch s.Harness {
	case "pi":
		args = []string{"--mode", "json", "--print", "--no-extensions", "--session", native.Path}
		if s.Model != "" {
			args = append(args, "--model", s.Model)
		}
	case "opencode":
		args = []string{"run", "--format", "json", "--thinking"}
		if native.ID != "" {
			args = append(args, "--session", native.ID)
		}
		if s.Model != "" {
			args = append(args, "--model", s.Model)
		}
	case "codex":
		args = []string{"exec", "--json", "--color", "never", "--sandbox", "read-only", "--skip-git-repo-check"}
		if s.Model != "" {
			args = append(args, "--model", s.Model)
		}
		if native.ID != "" {
			args = append(args, "resume", native.ID)
		}
		args = append(args, "-")
	}
	return args
}

// stdout and stderr have independent os/exec copy goroutines. Stderr is bounded
// and intentionally never returned or logged, since CLI diagnostics can contain credentials.
type cappedBuffer struct {
	data      []byte
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (16 << 10) - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

func (b *cappedBuffer) String() string { return string(b.data) }

type jsonLines struct {
	mu      sync.Mutex
	pending []byte
	total   int
	err     error
	consume func([]byte) error
	cancel  func()
}

func (l *jsonLines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return 0, l.err
	}
	n := len(p)
	l.total += n
	if l.total > 32<<20 {
		return 0, l.reject("Harness output exceeded the 32 MiB per-turn limit.")
	}
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		part := p
		if end >= 0 {
			part = p[:end]
		}
		if len(l.pending)+len(part) > 2<<20 {
			return 0, l.reject("Harness JSON line exceeded the 2 MiB limit.")
		}
		l.pending = append(l.pending, part...)
		if end < 0 {
			break
		}
		if err := l.flushLocked(); err != nil {
			return 0, err
		}
		p = p[end+1:]
	}
	return n, nil
}

func (l *jsonLines) reject(text string) error {
	l.err = errors.New(text)
	l.cancel()
	return l.err
}

func (l *jsonLines) flush() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.flushLocked()
}

func (l *jsonLines) flushLocked() error {
	if l.err != nil {
		return l.err
	}
	line := bytes.TrimSpace(bytes.TrimPrefix(l.pending, []byte{0xef, 0xbb, 0xbf}))
	if len(line) > 0 {
		if err := l.consume(line); err != nil {
			return l.reject(err.Error())
		}
	}
	l.pending = l.pending[:0]
	return nil
}

func (l *jsonLines) resetTurn() {
	l.mu.Lock()
	l.total = 0
	l.mu.Unlock()
}

func (a *app) execute(t *turn, s Session, native nativeSession, b binary, cwd string, payload submission) {
	defer a.wg.Done()
	defer t.cancel()
	p := &adapter{app: a, turn: t, id: s.ID, harness: s.Harness, role: s.Role, yolo: s.YOLO, cwd: cwd,
		keys: map[string]string{}, toolNames: map[string]string{}, commands: map[string]string{}, processesDrained: true}
	var stopCancellation func() bool
	if s.Role != "graph_node" {
		p.runtime = a.beginSessionRuntime(s.ID)
		// Independent of the adapter's event loop and native abort response.
		// A cancelled turn retires the entire conversation process container.
		stopCancellation = context.AfterFunc(t.ctx, func() { _ = p.runtime.shutdown() })
	}
	p.graph = graphBinding(t)
	defer UnbindGraphAdapter(t)
	p.stream = newStreamBatch(p)
	var err error
	if t.prompt == "" {
		t.prompt = payload.Text
	}
	if p.graph == nil && s.Role == "graph_node" {
		err = errors.New("Private graph node requires its activation adapter binding.")
	}
	if p.graph == nil && s.Role != "graph_node" && graphConversationHooks != nil {
		var hooks *GraphAdapterHooks
		hooks, err = graphConversationHooks(a, t, s)
		if hooks != nil {
			err = errors.Join(err, BindGraphAdapter(t, *hooks))
			p.graph = graphBinding(t)
		}
	}
	if t.prompt != "" {
		payload.Text = t.prompt
	}
	if err == nil && s.Role != "graph_node" && s.Role != sessionRoleSubagent {
		var guidance string
		guidance, err = sessionCollaborationPrompt()
		if err == nil {
			payload.Text = guidance + "\n\n" + payload.Text
		}
	}
	// Resolve defaults from the harness catalog, not a previous native turn's override.
	p.model, p.effort = s.Model, s.Effort
	if err == nil {
		if catalog, catalogErr := a.catalog(t.ctx, s, cwd, false); catalogErr == nil {
			if p.model == "" {
				p.model = catalog.DefaultModel
			}
			if p.effort == "" {
				if option := catalog.find(p.model); option != nil {
					p.effort = option.DefaultEffort
				}
				if p.model == catalog.DefaultModel && catalog.DefaultEffort != "" {
					p.effort = catalog.DefaultEffort
				}
			}
		}
		bridge, bridgeErr := p.startLinkedBridge()
		if bridgeErr != nil {
			err = bridgeErr
		} else {
			p.bridge = bridge
			switch s.Harness {
			case "opencode":
				err = p.runOpenCode(b, cwd, payload)
			case "pi":
				err = p.runPi(b, cwd, payload.piText())
			case "codex":
				err = p.runCodex(b, cwd, payload)
			default:
				err = errors.New("Unsupported harness.")
			}
			if p.runtime != nil {
				bridge.unbind(p)
			} else {
				bridge.close()
			}
		}
	}
	// Drain text before final results, consultation answers and graph completion
	// read the durable session, including when the harness failed or was stopped.
	err = errors.Join(err, p.closeSubagents(err != nil || p.failed, t.ctx.Err() != nil))
	err = errors.Join(err, p.stream.close())
	graphResult := p.finishGraphAdapter(err)
	if p.graphNode() && graphResult.Error != nil {
		err = graphResult.Error
	}
	if p.runtime != nil {
		p.runtime.finishTurn()
	}
	a.mu.Lock()
	if stopCancellation != nil {
		// Serialize normal completion against the Stop endpoint. Disarm before
		// deferred t.cancel so a successful turn can retain its warm runtime.
		stopCancellation()
		if t.ctx.Err() != nil {
			a.mu.Unlock()
			t.stopErr = p.runtime.shutdown()
			a.mu.Lock()
		}
	}
	finalErr := p.finishTurnStateLocked(err)
	if finalErr != nil {
		// Failed persistence never publishes an in-memory terminal success.
		p.failed = true
	}
	delete(a.runs, s.ID)
	for _, c := range a.state.Consultations {
		if c.From == s.ID && c.Status == "cancelled" {
			if recipient := a.runs[c.To]; recipient != nil && recipient.consultationID == c.ID {
				recipient.cancel()
			}
		}
	}
	t.approvals = nil
	t.questions = nil
	close(t.done)
	a.scheduleLinkedLocked()
	a.mu.Unlock()
	if finalErr != nil {
		graphResult.Outcome, graphResult.Error = "failed", finalErr
	}
	if p.graph != nil && p.graph.hooks.Finished != nil {
		p.graph.hooks.Finished(graphResult)
	} else if t.graphNotificationID != "" {
		// Prompt loading may fail before the factory can construct its hooks.
		// Still settle the core's already-reserved notification outbox entry.
		a.graphNotificationFinished(t.graphNotificationID, graphResult)
	}
}

// Called by execute only after native settlement/drain and stream flush. Kept as
// one transaction so queue uncertainty and linked completion cannot split.
func (p *adapter) finishTurnStateLocked(err error) error {
	t := p.turn
	return p.app.commitLocked(func(d *diskState) {
		s := d.session(p.id)
		s.Status, s.UpdatedAt = "idle", now()
		s.Permissions = nil
		s.Questions = nil
		if t.ctx.Err() != nil || err != nil || p.failed || !p.completed {
			pauseMessageQueue(s, "Execution stopped. Send queued messages when ready.")
		} else {
			for i := range s.Queue {
				if s.Queue[i].Status == "sending" {
					s.Queue[i].Status, s.Queue[i].Error = "uncertain", "Delivery was not confirmed. Check the conversation before sending again."
				}
			}
		}
		var last Event
		switch {
		case t.stopErr != nil:
			s.Status = "error"
			last = event("error", "Could not confirm all session processes stopped: "+t.stopErr.Error())
		case t.ctx.Err() != nil:
			last = event("status", "Execution stopped.")
			last.Status = "cancelled"
		case err != nil:
			s.Status = "error"
			last = event("error", err.Error())
		case p.failed:
			s.Status = "error"
		case !p.completed:
			s.Status = "error"
			last = event("error", "Harness exited without reporting a completed turn.")
		}
		if last.ID != "" {
			last.ConsultationID = t.consultationID
			s.Events = append(s.Events, last)
		}
		// A killed process cannot send terminal updates for its in-flight items.
		for i := range s.Events {
			e := s.Events[i]
			if e.Status == "running" || e.Status == "pending" {
				if s.Status == "error" {
					e.Status = "error"
				} else {
					e.Status = "cancelled"
				}
				d.setEvent(s, i, e)
			}
		}
		if t.consultationID != "" {
			c := d.consultation(t.consultationID)
			if t.delivery {
				if c.Delivery != "cancelled" {
					c.Delivery = "delivered"
					if t.ctx.Err() != nil || s.Status == "error" {
						c.Delivery = "failed"
					}
				}
			} else if !consultationTerminal(c.Status) {
				c.Status = "failed"
				c.Error = "Agent finished without returning a consultation answer."
				if err != nil {
					c.Error = err.Error()
				}
				if t.ctx.Err() != nil {
					c.Status = "cancelled"
					c.Error = "Consultation stopped."
				}
			}
			syncConsultation(d, c)
		}
		// A failed/stopped requester must not cause an unattended continuation.
		if t.ctx.Err() != nil || s.Status == "error" {
			for i := range d.Consultations {
				c := &d.Consultations[i]
				if c.From == s.ID && (c.Delivery == "waiting" || c.Delivery == "pending") {
					c.Delivery = "cancelled"
					if !consultationTerminal(c.Status) {
						c.Status = "cancelled"
						c.Error = "Requesting turn stopped."
					}
					syncConsultation(d, c)
				}
			}
		}
	})
}

type adapter struct {
	role                string
	yolo                bool
	cwd                 string
	gatedPermissions    map[string]string // Protected by app.mu; native call -> tool.
	openCodeMCPPrefixes []string          // Protected by app.mu; discovered server namespaces.
	stream              *streamBatch
	finalBatch          []streamUpdate // non-nil only during OpenCode final reconciliation
	graph               *graphAdapterBinding
	nativeSettled       bool
	choiceToolSettled   bool
	processesDrained    bool
	processDrainFailed  bool
	bridge              *linkedBridge
	runtime             *sessionRuntime
	app                 *app
	turn                *turn
	id                  string
	harness             string
	keys                map[string]string
	toolNames           map[string]string
	commands            map[string]string
	subagents           map[string]*adapter
	subagent            bool
	piMessage           int
	failed              bool
	completed           bool
	model               string
	effort              string
}

func object(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func truth(m map[string]any, key string) bool {
	b, _ := m[key].(bool)
	return b
}

func humanError(value any) string {
	if text, ok := value.(string); ok {
		if len(text) > 4096 {
			return strings.ToValidUTF8(text[:4096], "") + " [truncated]"
		}
		return text
	}
	if m := object(value); m != nil {
		for _, key := range []string{"message", "errorMessage", "error", "data"} {
			if text := humanError(m[key]); text != "" {
				return text
			}
		}
		return str(m, "name")
	}
	return ""
}

func contentText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if m := object(value); m != nil {
		if text := str(m, "text"); text != "" {
			return text
		}
		return contentText(m["content"])
	}
	if parts, ok := value.([]any); ok {
		texts := []string{}
		for _, part := range parts {
			if text := contentText(part); text != "" {
				texts = append(texts, text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

// Stable keys upsert harness items in first-observed order. Native deltas append
// text; accumulated tool results and completed messages replace text.
func (p *adapter) put(key, kind, title, text, status string, appendText bool, raw map[string]any) error {
	id := p.keys[key]
	if key == "" || id == "" {
		id = newID()
		if key != "" {
			p.keys[key] = id
		}
	}
	data := map[string]any{"harness": p.harness}
	for k, v := range raw {
		data[k] = v
	}
	// Native adapters reuse maps (notably OpenCode parts). The queued update and
	// published history must own their payload before the native reader continues.
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, &data); err != nil {
		return err
	}
	update := streamUpdate{event: Event{ID: id, Type: kind, Title: title, Text: text,
		Status: status, ConsultationID: p.turn.consultationID, CreatedAt: now(), Data: data}, appendText: appendText}
	if p.finalBatch != nil {
		p.finalBatch = append(p.finalBatch, update)
		return nil
	}
	if p.stream != nil {
		if key != "" && status == "running" && (kind == "assistant" || kind == "reasoning" || appendText) {
			return p.stream.enqueue(update)
		}
		// Lifecycle/tool events and final replacements also persist earlier text,
		// so a delayed batch can never overwrite completion or reorder events.
		return p.stream.flush(&update)
	}
	return p.commitStreamUpdates([]streamUpdate{update})
}

// Persist valid reconciliation events even when a later native part fails.
// The batch is disabled before flushing so follow-up diagnostics cannot be lost.
func (p *adapter) withFinalBatch(reconcile func() error) error {
	p.finalBatch = []streamUpdate{}
	reconcileErr := reconcile()
	batch := p.finalBatch
	p.finalBatch = nil
	if len(batch) == 0 {
		return reconcileErr
	}
	var flushErr error
	if p.stream != nil {
		flushErr = p.stream.flushUpdates(batch)
	} else {
		flushErr = p.commitStreamUpdates(batch)
	}
	return errors.Join(reconcileErr, flushErr)
}

func (p *adapter) nativeID(id string) error {
	if id == "" {
		return nil
	}
	if len(id) > 256 || strings.HasPrefix(id, "-") || strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) != -1 {
		return errors.New("Harness returned an invalid native session identifier.")
	}
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	if p.app.state.Native[p.id].ID == id {
		return nil
	}
	return p.app.commitLocked(func(d *diskState) {
		n := d.Native[p.id]
		n.ID = id
		d.Native[p.id] = n
	})
}

func (p *adapter) failure(raw map[string]any) error {
	p.failed = true
	text := humanError(raw)
	if text == "" {
		text = "Harness reported an error."
	}
	// Retain the event type but not arbitrary diagnostic payloads, which can
	// contain authentication headers or other credentials.
	return p.put("", "error", "", text, "error", false, map[string]any{"type": raw["type"]})
}

func (p *adapter) consume(line []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil || raw == nil || str(raw, "type") == "" {
		return errors.New("Harness returned invalid JSON event output.")
	}
	switch p.harness {
	case "pi":
		return p.pi(raw)
	case "opencode":
		return p.opencode(raw)
	case "codex":
		return p.codex(raw)
	}
	return errors.New("Unknown harness protocol.")
}

func (p *adapter) pi(raw map[string]any) error {
	typ := str(raw, "type")
	switch typ {
	case "session":
		return p.nativeID(str(raw, "id"))
	case "message_start":
		if str(object(raw["message"]), "role") == "assistant" {
			p.piMessage++
		}
		return nil
	case "message_update":
		delta := object(raw["assistantMessageEvent"])
		kind := str(delta, "type")
		if kind != "text_delta" && kind != "thinking_delta" {
			return nil
		}
		target := "assistant"
		if kind == "thinking_delta" {
			target = "reasoning"
		}
		index, _ := delta["contentIndex"].(float64)
		if number, ok := delta["contentIndex"].(json.Number); ok {
			index, _ = number.Float64()
		}
		key := fmt.Sprintf("message/%d/%d", p.piMessage, int(index))
		// Keep delta metadata, not the repeatedly growing partial message copy.
		return p.put(key, target, "", str(delta, "delta"), "running", true,
			map[string]any{"type": typ, "contentIndex": index})
	case "message_end":
		message := object(raw["message"])
		if str(message, "role") != "assistant" {
			return nil
		}
		status := "completed"
		stop := str(message, "stopReason")
		if stop == "error" || stop == "aborted" {
			status = "error"
		}
		parts, _ := message["content"].([]any)
		for index, value := range parts {
			part := object(value)
			kind, text := "assistant", str(part, "text")
			if str(part, "type") == "thinking" {
				kind, text = "reasoning", str(part, "thinking")
			} else if str(part, "type") != "text" {
				continue
			}
			if err := p.put(fmt.Sprintf("message/%d/%d", p.piMessage, index), kind, "", text, status, false,
				map[string]any{"type": typ, "contentIndex": index, "stopReason": stop, "model": message["model"], "usage": message["usage"]}); err != nil {
				return err
			}
		}
		if status == "error" && !(stop == "aborted" && p.graphSealed() && p.choiceToolSettled) {
			return p.failure(map[string]any{"type": typ, "message": message["errorMessage"]})
		}
		return nil
	case "tool_execution_start", "tool_execution_update", "tool_execution_end":
		key := "tool/" + str(raw, "toolCallId")
		name := str(raw, "toolName")
		if name != "" {
			p.toolNames[key] = name
		} else {
			name = p.toolNames[key]
		}
		if args := object(raw["args"]); args != nil {
			p.commands[key] = str(args, "command")
		}
		kind, text, status := "tool", "", "running"
		if name == "bash" {
			kind = "command"
		} else if strings.HasPrefix(name, "mcp") {
			kind = "mcp"
		}
		if typ == "tool_execution_update" {
			text = contentText(raw["partialResult"])
		} else if typ == "tool_execution_end" {
			text, status = contentText(raw["result"]), "completed"
			if truth(raw, "isError") {
				status = "error"
			}
		}
		if command := p.commands[key]; command != "" {
			text = command + "\n" + text
		}
		return p.put(key, kind, name, strings.TrimSuffix(text, "\n"), status, false, raw)
	case "agent_settled":
		p.completed = true
		return p.put("", "status", "", "Agent settled.", "completed", false, raw)
	case "agent_end":
		// agent_end includes complete messages already represented above.
		return nil
	case "turn_start", "turn_end":
		return nil
	case "error":
		return p.failure(raw)
	default:
		return p.put("", "status", typ, typ, "", false, raw)
	}
}

func (p *adapter) opencode(raw map[string]any) error {
	if err := p.nativeID(str(raw, "sessionID")); err != nil {
		return err
	}
	typ := str(raw, "type")
	part := object(raw["part"])
	key := typ + "/" + str(part, "id")
	switch typ {
	case "text", "reasoning":
		kind := "assistant"
		if typ == "reasoning" {
			kind = "reasoning"
			if strings.TrimSpace(str(part, "text")) == "" {
				return nil
			}
		}
		return p.put(key, kind, "", str(part, "text"), "completed", false,
			map[string]any{"type": typ, "partID": part["id"]})
	case "tool_use":
		state := object(part["state"])
		name := str(part, "tool")
		kind, text := "tool", contentText(state["output"])
		status := str(state, "status")
		if status == "error" {
			text = humanError(state["error"])
		}
		if name == "bash" {
			kind = "command"
			if command := str(object(state["input"]), "command"); command != "" {
				text = command + "\n" + text
			}
		} else if strings.HasPrefix(name, "mcp_") || strings.HasPrefix(name, "mcp.") {
			kind = "mcp"
		}
		return p.put(key, kind, name, strings.TrimSuffix(text, "\n"), status, false, raw)
	case "step_start", "step_finish":
		status := "started"
		p.completed = false
		if typ == "step_finish" {
			status = "completed"
			p.completed = str(part, "reason") != "tool-calls"
		}
		return p.put("", "status", typ, typ, status, false, raw)
	case "error":
		return p.failure(raw)
	default:
		return p.put("", "status", typ, typ, "", false, raw)
	}
}

func (p *adapter) codex(raw map[string]any) error {
	typ := str(raw, "type")
	switch typ {
	case "thread.started":
		return p.nativeID(str(raw, "thread_id"))
	case "turn.failed", "error":
		return p.failure(raw)
	case "turn.completed":
		p.completed = true
		return p.put("", "status", "", "Turn completed.", "completed", false, raw)
	case "item.started", "item.updated", "item.completed":
		item := object(raw["item"])
		kind, title, text := "tool", str(item, "type"), str(item, "text")
		status := str(item, "status")
		if status == "" || status == "in_progress" {
			status = "running"
			if typ == "item.completed" {
				status = "completed"
			}
		} else if status == "failed" {
			status = "error"
		}
		switch title {
		case "agent_message":
			kind, title = "assistant", ""
		case "reasoning":
			kind, title = "reasoning", ""
		case "command_execution":
			kind, title = "command", "Command"
			text = str(item, "command")
			if output := str(item, "aggregated_output"); output != "" {
				text += "\n" + output
			}
			if code, ok := item["exit_code"].(float64); ok && code != 0 {
				status = "error"
			}
		case "mcp_tool_call":
			kind, title = "mcp", str(item, "server")+" / "+str(item, "tool")
			text = contentText(item["result"])
			if message := humanError(item["error"]); message != "" {
				text, status = message, "error"
			}
		case "error":
			// Item errors are nonfatal diagnostics; only turn.failed/error fails the turn.
			kind, status, title = "status", "warning", "Harness warning"
			if text == "" {
				text = humanError(item)
			}
		}
		return p.put("item/"+str(item, "id"), kind, title, text, status, false, raw)
	default:
		return p.put("", "status", typ, typ, "", false, raw)
	}
}
