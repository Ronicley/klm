package main

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// GraphAdapterHooks is installed before execute starts. Callbacks run without
// app.mu. The core owns validation, durable state and scheduling. ReserveChoice
// must atomically validate and persist the reservation, never dispatch a node.
// Transition("accepted") must durably commit acceptance before returning nil;
// continuation may only be scheduled from Finished, after the native turn closes.
// A reservation survives neither a new binding nor a restart as an acceptance.
type GraphAdapterHooks struct {
	Node          bool
	Tools         []map[string]any
	Call          func(context.Context, string, json.RawMessage) (any, error)
	ReserveChoice func(context.Context, string, json.RawMessage) (any, error)
	Transition    func(context.Context, string) error
	Finished      func(GraphAdapterResult)
}

// Installed once by the graph core's init. Invoked without app.mu, before any
// catalog/bridge/harness request, only for an unbound main/side turn.
var graphConversationHooks func(*app, *turn, Session) (*GraphAdapterHooks, error)

type GraphAdapterResult struct {
	Outcome               string // completed, missing_choice, choice, failed, interrupted, finality_unconfirmed
	ChoiceState           string // open, reserved, sealed, drained, accepted
	NativeSettled         bool
	ChoiceToolSettled     bool
	ProcessesDrained      bool
	Continuable           bool
	ExternalWorkConfirmed bool // Never inferred from process exit or MCP transport closure.
	ToolCallsSettled      bool
	UncertainToolCalls    []graphMCPCall
	Error                 error
}

type GraphAdapterCapability struct {
	Harness          string `json:"harness"`
	PreExecutionGate bool   `json:"preExecutionGate"`
	OwnedContainment bool   `json:"ownedContainment"`
	ProcessSeal      bool   `json:"processSeal"`
	RuntimeValidated bool   `json:"runtimeValidated"`
	Limitation       string `json:"limitation"`
}

// GraphOwnedProcess also serves Terminal executors. Prepare before cmd.Start,
// set cmd.Cancel = owner.Stop for CommandContext, then Attach immediately after
// Start. On an Attach error: Stop, cmd.Wait, Close. On completion: cmd.Wait then
// Close and check Drained. Close has a bounded infrastructure confirmation wait;
// expiration is unconfirmed termination, never a successful graph result.
type GraphOwnedProcess interface {
	Attach() error
	Stop() error
	Close() error
	Drained() bool
}

func PrepareGraphProcess(cmd *exec.Cmd) (GraphOwnedProcess, error) {
	return prepareOwnedProcess(cmd)
}

// This reports implemented mechanisms, NOT a successful native/model exercise.
// Interrupt/idle/completed alone are not a seal. A gate or confirmed termination
// of the complete owned process domain is required before accepting a Choice.
func GraphAdapterCapabilities() []GraphAdapterCapability {
	contained := runtime.GOOS == "windows"
	return []GraphAdapterCapability{
		{Harness: "pi", PreExecutionGate: true, OwnedContainment: contained,
			Limitation: "Gate covers agent-dispatched tools in the owned extension only. No containment of remote work delegated by a command. Native continuation has not been exercised."},
		{Harness: "opencode", PreExecutionGate: true, OwnedContainment: contained,
			Limitation: "Requires the owned pre-tool plugin handshake; harness versions are not gated. Tracks tool-call responses, not server lifetime or detached remote tasks. Ambiguous transport/cancellation errors retain finality uncertainty."},
		{Harness: "codex", OwnedContainment: contained, ProcessSeal: contained,
			Limitation: "Choice stays reserved until native settlement, returned MCP calls and termination of the owned Job. Local cancellation is not a remote response. Detached tasks after a tool response are outside the guarantee."},
	}
}

// Preflight before dispatching any graph task. Do not perform work with an
// adapter already known to be unable to accept its result safely. Ordinary chat
// and graph-orchestrator tools remain available on all three harnesses.
func CheckGraphAdapter(harness string) error {
	for _, capability := range GraphAdapterCapabilities() {
		if capability.Harness == harness {
			if (!capability.PreExecutionGate && !capability.ProcessSeal) || !capability.OwnedContainment {
				return errors.New("Graph node execution is not supported by this adapter/platform: " + capability.Limitation)
			}
			return nil
		}
	}
	return errors.New("Unknown graph harness.")
}

type graphAdapterBinding struct {
	mu                   sync.Mutex
	hooks                GraphAdapterHooks
	state                string
	callID               string
	reservation          any
	reservationID        string
	stop                 chan struct{}
	stopOnce             sync.Once
	gateSeen             bool
	err                  error
	closed               atomic.Bool
	finished             bool
	nativeSession        string
	pluginReady          chan struct{}
	pluginOnce           sync.Once
	choiceAdmitted       bool
	openCodeTools        map[string]bool
	openCodeMCPPrefixes  []string
	mcpCalls             map[graphMCPCallKey]graphMCPCall
	choiceSettled        bool
	nativeSessions       map[string]bool
	codexChildren        map[string]bool // true when this activation spawned a fresh thread
	codexTurns           map[string]map[string]bool
	codexAudited         map[string]bool
	codexCoverageUnknown map[string]bool
	boundUnix            int64
}

var graphAdapterBindings sync.Map // *turn -> *graphAdapterBinding, no persisted capabilities

func BindGraphAdapter(t *turn, hooks GraphAdapterHooks) error {
	if t == nil || (hooks.Node && (hooks.ReserveChoice == nil || hooks.Transition == nil || hooks.Finished == nil)) {
		return errors.New("Graph node adapter requires reservation, transition and completion hooks.")
	}
	seen := map[string]bool{}
	for _, tool := range hooks.Tools {
		name := str(tool, "name")
		if !strings.HasPrefix(name, "graph_") || name == "graph_submit_choice" || seen[name] {
			return errors.New("Graph tool names must be unique graph_ capabilities and cannot replace graph_submit_choice.")
		}
		seen[name] = true
	}
	b := &graphAdapterBinding{hooks: hooks, state: "open", stop: make(chan struct{}), pluginReady: make(chan struct{}), boundUnix: time.Now().Unix()}
	if _, loaded := graphAdapterBindings.LoadOrStore(t, b); loaded {
		return errors.New("Graph adapter is already bound to this turn.")
	}
	return nil
}

// UnbindGraphAdapter is for a reserved turn that will never call execute.
func UnbindGraphAdapter(t *turn) { graphAdapterBindings.Delete(t) }

func graphBinding(t *turn) *graphAdapterBinding {
	value, _ := graphAdapterBindings.Load(t)
	b, _ := value.(*graphAdapterBinding)
	return b
}

func (p *adapter) graphNode() bool { return p.graph != nil && p.graph.hooks.Node }

func (p *adapter) graphSealed() bool {
	if !p.graphNode() {
		return false
	}
	return p.graph.closed.Load()
}

func (p *adapter) bridgeTools() []map[string]any {
	tools := []map[string]any{}
	if !p.graphNode() {
		tools = append(tools, linkedTools()...)
	}
	if p.graph != nil {
		tools = append(tools, p.graph.hooks.Tools...)
		if p.graphNode() {
			// Unknown/mistyped Choice fields deliberately reach core validation.
			tools = append(tools, map[string]any{"name": "graph_submit_choice", "description": "Submit the final Choice for this activation. The response reserves it; acceptance follows native settlement.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"choice": map[string]any{}, "payload": map[string]any{}}, "additionalProperties": true}})
		}
	}
	return tools
}

func (p *adapter) callGraphTool(ctx context.Context, name, callID string, raw json.RawMessage) (any, error) {
	b := p.graph
	if b == nil {
		return nil, errors.New("Graph capability is unavailable in this turn.")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return nil, errors.New("Graph adapter turn has already finished.")
	}
	if name != "graph_submit_choice" || !b.hooks.Node {
		if b.closed.Load() {
			return nil, errors.New("Graph activation is sealed against further tool calls.")
		}
		if b.hooks.Call == nil {
			return nil, errors.New("Graph tool handler is unavailable.")
		}
		return b.hooks.Call(ctx, name, raw)
	}
	if b.state != "open" {
		if b.callID == callID {
			return map[string]any{"reserved": true, "accepted": false, "state": b.state, "reservationId": b.reservationID}, b.err
		}
		return nil, errors.New("A Choice is already reserved for this activation.")
	}
	if err := p.turn.ctx.Err(); err != nil {
		return nil, err
	}
	if p.harness == "opencode" && !b.choiceAdmitted {
		return nil, errors.New("Choice did not pass through the owned OpenCode pre-execution gate.")
	}
	value, err := b.hooks.ReserveChoice(ctx, callID, raw)
	if err != nil {
		return nil, err // A rejected contract remains open for correction.
	}
	b.state, b.callID, b.reservation = "reserved", callID, value
	b.reservationID = newID()
	b.closed.Store(true)
	// Pi's pre-tool hook consults this same lock before ALL bypasses/approvals.
	// Other adapters only have an approval gate, so never label them sealed here.
	if (p.harness == "pi" || p.harness == "opencode") && b.gateSeen {
		if err := b.hooks.Transition(p.turn.ctx, "sealed"); err != nil {
			b.err = err
			return nil, err
		}
		b.state = "sealed"
	}
	return map[string]any{"reserved": true, "accepted": false, "state": b.state, "reservationId": b.reservationID, "result": value}, nil
}

func (p *adapter) setGraphNativeSession(id string) {
	if !p.graphNode() {
		return
	}
	p.graph.mu.Lock()
	p.graph.nativeSession = id
	p.graph.mu.Unlock()
}

func (p *adapter) waitGraphPlugin(ctx context.Context) error {
	if !p.graphNode() {
		return nil
	}
	select {
	case <-p.graph.pluginReady:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(15 * time.Second):
		return errors.New("Owned OpenCode graph plugin did not complete its startup handshake.")
	}
}

func (p *adapter) openCodeGraphGate(raw json.RawMessage) error {
	if !p.graphNode() || p.harness != "opencode" {
		return errors.New("This turn does not own an OpenCode graph gate.")
	}
	var request struct {
		Operation string `json:"operation"`
		SessionID string `json:"sessionID"`
		Tool      string `json:"tool"`
		CallID    string `json:"callID"`
	}
	if json.Unmarshal(raw, &request) != nil {
		return errors.New("Invalid graph gate request.")
	}
	b := p.graph
	b.mu.Lock()
	defer b.mu.Unlock()
	if request.Operation == "response" || request.Operation == "not_started" {
		if request.SessionID == "" || request.CallID == "" {
			return errors.New("Invalid tool completion correlation.")
		}
		if b.finished {
			return errors.New("Graph adapter has finished.")
		}
		b.endMCPCallLocked(graphMCPCallKey{Session: request.SessionID, Call: request.CallID}, request.Operation)
		return nil
	}
	if p.turn.ctx.Err() != nil || b.finished || b.state != "open" {
		return errors.New("Graph activation is sealed against further work.")
	}
	if request.Operation == "ready" {
		b.pluginOnce.Do(func() { close(b.pluginReady) })
		return nil
	}
	if request.Operation != "admit" || b.nativeSession == "" || request.SessionID == "" || request.CallID == "" || request.Tool == "" {
		return errors.New("Unbound or invalid OpenCode tool admission.")
	}
	if b.nativeSessions == nil {
		b.nativeSessions = map[string]bool{}
	}
	b.nativeSessions[request.SessionID] = true
	// Native tools were enumerated at startup; every other name is discovered by
	// the owned hook on the actual resolved MCP tool (not guessed from a server
	// allowlist). Unknown/custom/resource tools conservatively use the call boundary.
	external := !b.openCodeTools[request.Tool]
	for _, prefix := range b.openCodeMCPPrefixes {
		external = external || strings.HasPrefix(request.Tool, prefix)
	}
	if request.Tool == "klm_linked_graph_submit_choice" {
		external = false
	}
	if external {
		b.admitMCPCallLocked(graphMCPCallKey{Session: request.SessionID, Call: request.CallID}, request.Tool)
	}
	// All plugin callbacks belong to this dedicated owned server/job. Child task
	// sessions share the gate, but only the activation's root may submit its Choice.
	if request.Tool == "klm_linked_graph_submit_choice" {
		if request.SessionID != b.nativeSession {
			return errors.New("Only the graph activation session can submit its Choice.")
		}
		b.choiceAdmitted = true
	}
	b.gateSeen = true
	return nil
}

func (p *adapter) setOpenCodeGraphTools(ids []string, servers map[string]any) {
	p.graph.mu.Lock()
	defer p.graph.mu.Unlock()
	p.graph.openCodeTools = map[string]bool{"shell": true}
	for _, id := range ids {
		p.graph.openCodeTools[id] = true
	}
	for _, tool := range p.bridgeTools() {
		p.graph.openCodeTools["klm_linked_"+str(tool, "name")] = true
	}
	// McpCatalog.toolName in 1.18.30 uses sanitized server + '_' + tool.
	// This is classification of actually discovered servers, not an allowlist.
	// In particular a custom native tool ID colliding with an MCP name must not
	// hide the MCP call from the boundary tracker.
	for name := range servers {
		if name == "klm_linked" {
			continue
		}
		sanitized := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
				return r
			}
			return '_'
		}, name)
		p.graph.openCodeMCPPrefixes = append(p.graph.openCodeMCPPrefixes, sanitized+"_")
	}
}

// graphGate is an authenticated transport operation, not a model-visible tool.
// It is fail-closed, and shares the reservation lock to linearize tool admission.
func (p *adapter) graphGate() error {
	if !p.graphNode() || p.harness != "pi" {
		return errors.New("This turn does not own a Pi graph gate.")
	}
	p.graph.mu.Lock()
	defer p.graph.mu.Unlock()
	if p.turn.ctx.Err() != nil || p.graph.finished || p.graph.state != "open" {
		return errors.New("Graph activation is sealed against further work.")
	}
	p.graph.gateSeen = true
	return nil
}

// The adapter asks for a native interrupt only AFTER the Choice tool's terminal
// event, not when the HTTP response is written. This preserves a tool-result pair
// in native history and avoids killing a harness while its MCP call is pending.
func (p *adapter) graphReservationReceipt(result any) bool {
	if p.graph == nil {
		return false
	}
	var receipt struct {
		Reserved      bool   `json:"reserved"`
		ReservationID string `json:"reservationId"`
	}
	if json.Unmarshal([]byte(contentText(result)), &receipt) != nil || !receipt.Reserved {
		return false
	}
	p.graph.mu.Lock()
	defer p.graph.mu.Unlock()
	if receipt.ReservationID == "" || receipt.ReservationID != p.graph.reservationID {
		return false
	}
	return true
}

func (p *adapter) graphChoiceToolSettled(result any) {
	if !p.graphSealed() || !p.graphReservationReceipt(result) {
		return
	}
	p.choiceToolSettled = true
	p.graph.mu.Lock()
	p.graph.choiceSettled = true
	p.graph.maybeStopLocked()
	p.graph.mu.Unlock()
}

func (p *adapter) graphStop() <-chan struct{} {
	if !p.graphNode() {
		return nil
	}
	return p.graph.stop
}

func (p *adapter) finishGraphAdapter(runErr error) GraphAdapterResult {
	r := GraphAdapterResult{Outcome: "completed", NativeSettled: p.nativeSettled,
		ChoiceToolSettled: p.choiceToolSettled, ProcessesDrained: p.processesDrained, Error: runErr}
	if runErr != nil || p.failed {
		r.Outcome = "failed"
		if r.Error == nil {
			r.Error = errors.New("Harness reported a failed turn.")
		}
	}
	if !p.completed && r.Error == nil {
		r.Outcome, r.Error = "failed", errors.New("Harness did not report a completed turn.")
	}
	if p.graphNode() && !p.nativeSettled && r.Error == nil {
		r.Outcome, r.Error = "failed", errors.New("Harness did not confirm a settled native graph turn.")
	}
	if p.turn.ctx.Err() != nil {
		r.Outcome, r.Error = "interrupted", p.turn.ctx.Err()
	}
	if !p.graphNode() {
		if p.graph != nil {
			p.graph.mu.Lock()
			p.graph.finished = true
			p.graph.closed.Store(true)
			p.graph.mu.Unlock()
		}
		return r
	}
	b := p.graph
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finished = true
	b.closed.Store(true)
	r.ChoiceState = b.state
	r.UncertainToolCalls = b.unresolvedMCPCallsLocked()
	r.ToolCallsSettled = len(r.UncertainToolCalls) == 0
	uncertainty := mcpCallUncertainty(r.UncertainToolCalls)
	for child := range b.codexChildren {
		if !b.codexAudited[child] || b.codexCoverageUnknown[child] {
			uncertainty = errors.Join(uncertainty, &graphUnconfirmedError{cause: errors.New("MCP call coverage is unconfirmed for native subagent thread " + child + ".")})
		}
	}
	if uncertainty != nil {
		r.ToolCallsSettled = false
		r.Outcome = "finality_unconfirmed"
		r.Error = errors.Join(r.Error, uncertainty)
		return r
	}
	if b.state == "open" {
		if r.Outcome == "completed" && p.nativeSettled {
			r.Outcome = "missing_choice"
			r.Continuable = p.processesDrained
		}
		return r
	}
	if r.Outcome != "completed" {
		return r
	}
	// Codex has no dependable fail-closed pre-tool hook. Its barrier is physical:
	// leave the result RESERVED while native interrupt completes, then terminate
	// the entire owned Job. Zero live processes seals ALL already-authorized local
	// tools as well as future dispatch. Persist that evidence before acceptance.
	// No graph result is accepted during the reservation/interrupt window.
	if p.harness == "codex" && b.state == "reserved" && p.nativeSettled && p.choiceToolSettled && p.processesDrained {
		if err := b.hooks.Transition(p.turn.ctx, "sealed"); err != nil {
			r.Outcome, r.Error = "failed", err
			return r
		}
		b.state, r.ChoiceState = "sealed", "sealed"
	}
	if b.err != nil || b.state != "sealed" || !p.nativeSettled || !p.choiceToolSettled || !p.processesDrained {
		r.Outcome = "finality_unconfirmed"
		r.Error = errors.Join(b.err, errors.New("Choice was reserved but native settlement, activation sealing or owned-process drainage could not be confirmed."))
		return r
	}
	for _, state := range []string{"drained", "accepted"} {
		if err := b.hooks.Transition(p.turn.ctx, state); err != nil {
			r.Outcome, r.Error = "failed", err
			return r
		}
		b.state, r.ChoiceState = state, state
	}
	r.Outcome, r.Continuable = "choice", true
	return r
}

// CanonicalGraphDirectory must be applied to both persisted and requested cwd.
// Core must additionally enforce same run/node/harness and latest-session policy.
func CanonicalGraphDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("Graph working directory is required.")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path, nil
}
