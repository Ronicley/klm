package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	mcpFrameLimit         = 4 << 20
	mcpHTTPCleanupTimeout = 2 * time.Second
)

type mcpLifecycleError struct{ cause error }

func (e *mcpLifecycleError) Error() string {
	return "MCP connection shutdown is unconfirmed: " + e.cause.Error()
}
func (e *mcpLifecycleError) Unwrap() error { return e.cause }

type mcpNotSentError struct{ cause error }

func (e *mcpNotSentError) Error() string { return "MCP request was not sent: " + e.cause.Error() }
func (e *mcpNotSentError) Unwrap() error { return e.cause }

type mcpLiveConnection struct {
	catalogChanged atomic.Bool
	session        *mcp.ClientSession
	owner          *ownedProcess
	cmd            *exec.Cmd
	stop           func() bool
	once           sync.Once
	closeErr       error
	http           *mcpHTTPTransport
}

func (c *mcpLiveConnection) close() error {
	if c == nil {
		return nil
	}
	c.once.Do(func() {
		if c.stop != nil {
			c.stop()
		}
		if c.owner != nil {
			c.closeErr = c.owner.Stop()
		}
		// SDK v1.4 detaches its HTTP connection context and only cancels it
		// after DELETE. Bound that cleanup, including a Close waiting on an
		// in-flight request, without limiting normal SSE/tool request duration.
		var cleanupTimer *time.Timer
		var cleanupExpired atomic.Bool
		if c.http != nil {
			cleanupTimer = time.AfterFunc(mcpHTTPCleanupTimeout, func() {
				cleanupExpired.Store(true)
				c.http.cancel()
			})
		}
		if c.session != nil {
			c.closeErr = errors.Join(c.closeErr, c.session.Close())
		}
		if c.http != nil {
			cleanupTimer.Stop()
			c.http.cancel()
			c.http.transport.CloseIdleConnections()
			if cleanupExpired.Load() {
				c.closeErr = errors.Join(c.closeErr, errors.New("MCP HTTP cleanup deadline exceeded; remote session termination is unconfirmed."))
			}
		}
		if c.cmd != nil {
			_ = c.cmd.Wait()
		}
		if c.owner != nil {
			c.closeErr = errors.Join(c.closeErr, c.owner.Close())
			if !c.owner.Drained() {
				c.closeErr = errors.Join(c.closeErr, errors.New("MCP server process shutdown could not be confirmed."))
			}
		}
	})
	if c.closeErr != nil {
		return &mcpLifecycleError{cause: c.closeErr}
	}
	return nil
}

// Both stdio JSON and HTTP/SSE lines are bounded without truncating a message.
type mcpLineReader struct {
	source  io.ReadCloser
	scanner *bufio.Scanner
	pending []byte
}

func boundedMCPLines(source io.ReadCloser) io.ReadCloser {
	s := bufio.NewScanner(source)
	s.Buffer(make([]byte, 4096), mcpFrameLimit)
	return &mcpLineReader{source: source, scanner: s}
}
func (r *mcpLineReader) Read(p []byte) (int, error) {
	if len(r.pending) == 0 {
		if !r.scanner.Scan() {
			if err := r.scanner.Err(); err != nil {
				return 0, errors.New("MCP message exceeds transport limit or stream failed.")
			}
			return 0, io.EOF
		}
		r.pending = append(append([]byte{}, r.scanner.Bytes()...), '\n')
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}
func (r *mcpLineReader) Close() error { return r.source.Close() }

type mcpHTTPTransport struct {
	app         *app
	connection  MCPConnectionSnapshot
	lifetime    context.Context
	cancel      context.CancelFunc
	transport   *http.Transport
	authMu      sync.Mutex
	authHeaders http.Header // Last dispatched auth, used only for bounded DELETE cleanup.
}

func (t *mcpHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(t.lifetime, cancel)
	if req.Method == http.MethodDelete {
		var deadlineCancel context.CancelFunc
		ctx, deadlineCancel = context.WithTimeout(ctx, mcpHTTPCleanupTimeout)
		requestCancel := cancel
		cancel = func() { deadlineCancel(); requestCancel() }
	}
	cleanup := func() { stop(); cancel() }
	if t.lifetime.Err() != nil {
		cancel()
	}
	// Never follow redirects or forward credentials to a different destination.
	if req.URL.String() != t.connection.Config.URL {
		cleanup()
		return nil, &mcpNotSentError{cause: errors.New("MCP destination changed.")}
	}
	var authHeaders http.Header
	if req.Method == http.MethodDelete {
		// Cleanup must not wait on the shared credential/refresh mutex or start
		// another OAuth exchange. Use the last dispatched auth for best-effort
		// session termination; ordinary calls always resolve live credentials.
		t.authMu.Lock()
		authHeaders = t.authHeaders.Clone()
		t.authMu.Unlock()
	} else {
		credential, err := t.app.mcpHTTPSecrets(ctx, t.connection)
		if err != nil {
			cleanup()
			return nil, &mcpNotSentError{cause: err}
		}
		authHeaders = make(http.Header)
		for key, value := range credential.Headers {
			authHeaders.Set(key, value)
		}
		if credential.Bearer != "" {
			authHeaders.Set("Authorization", "Bearer "+credential.Bearer)
		}
		t.authMu.Lock()
		t.authHeaders = authHeaders.Clone()
		t.authMu.Unlock()
	}
	r := req.Clone(ctx)
	r.Header = req.Header.Clone()
	for key, values := range authHeaders {
		r.Header[key] = append([]string(nil), values...)
	}
	response, err := t.transport.RoundTrip(r)
	if err != nil {
		cleanup()
		return nil, errors.New("MCP HTTP transport failed.")
	}
	if req.Method == http.MethodDelete {
		// The SDK does not read or close the DELETE response body. No body
		// content is needed for session termination, so never wait to drain it.
		err := response.Body.Close()
		response.Body = http.NoBody
		cleanup()
		if err != nil {
			return nil, errors.New("MCP HTTP cleanup response failed.")
		}
		if response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusMethodNotAllowed && (response.StatusCode < 200 || response.StatusCode >= 300) {
			return nil, errors.New("MCP HTTP session termination was not confirmed by the server.")
		}
	} else {
		response.Body = &mcpHTTPBody{ReadCloser: boundedMCPLines(response.Body), cleanup: cleanup}
	}
	return response, nil
}

type mcpHTTPBody struct {
	io.ReadCloser
	cleanup func()
}

func (b *mcpHTTPBody) Close() error {
	err := b.ReadCloser.Close()
	b.cleanup()
	return err
}

func (a *app) connectMCP(ctx context.Context, c MCPConnectionSnapshot, cwd string, p *adapter) (*mcpLiveConnection, error) {
	live := &mcpLiveConnection{}
	opts := &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	opts.ToolListChangedHandler = func(context.Context, *mcp.ToolListChangedRequest) { live.catalogChanged.Store(true) }
	// Form and URL questions are always routed to a human, including under YOLO.
	opts.Capabilities.Elicitation = &mcp.ElicitationCapabilities{Form: &mcp.FormElicitationCapabilities{}, URL: &mcp.URLElicitationCapabilities{}}
	opts.ElicitationHandler = func(requestCtx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		if p == nil {
			return &mcp.ElicitResult{Action: "cancel"}, nil
		}
		return p.mcpElicit(requestCtx, c, req)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "klm-engine", Version: "1.0.0"}, opts)
	var transport mcp.Transport
	if c.Config.Transport == "http" {
		lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
		live.http = &mcpHTTPTransport{app: a, connection: c, lifetime: lifetime, cancel: cancel, transport: http.DefaultTransport.(*http.Transport).Clone()}
		transport = &mcp.StreamableClientTransport{Endpoint: c.Config.URL, MaxRetries: -1, HTTPClient: &http.Client{Transport: live.http, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	} else {
		credential, err := a.readMCPCredential(c.CredentialID)
		if err != nil {
			return nil, err
		}
		resolved, ok := executable(c.Config.Command)
		if !ok {
			return nil, errors.New("MCP executable is unavailable; use an executable such as node.exe with separate arguments, not a shell shim.")
		}
		if c.Config.CWD != "" {
			cwd = c.Config.CWD
		}
		cmd := exec.Command(resolved, c.Config.Args...)
		cmd.Dir = cwd
		// Inherit only the minimal OS/runtime environment, not harness auth/config.
		for _, key := range []string{"SystemRoot", "WINDIR", "PATH", "PATHEXT", "TEMP", "TMP", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "COMSPEC"} {
			if value := os.Getenv(key); value != "" {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
		for key, value := range credential.Environment {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		owner, err := prepareOwnedProcess(cmd)
		if err != nil {
			return nil, err
		}
		live.owner = owner
		live.cmd = cmd
		stdin, err := cmd.StdinPipe()
		if err != nil {
			_ = owner.Close()
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			_ = owner.Close()
			return nil, err
		}
		cmd.Stderr = io.Discard
		cmd.WaitDelay = 3 * time.Second
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			_ = owner.Close()
			return nil, errors.New("Cannot start MCP server.")
		}
		if err := owner.Attach(); err != nil {
			return nil, errors.Join(err, live.close())
		}
		live.stop = context.AfterFunc(ctx, func() { _ = owner.Stop() })
		transport = &mcp.IOTransport{Reader: boundedMCPLines(stdout), Writer: stdin}
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		cleanupErr := live.close()
		return nil, errors.Join(errors.New("MCP initialization failed; check connection and authentication."), cleanupErr)
	}
	live.session = session
	return live, nil
}

func resolveMCPSchema(value any) (*jsonschema.Resolved, error) {
	data, err := json.Marshal(value)
	if err != nil || len(data) > 128<<10 {
		return nil, errors.New("MCP schema exceeds 128 KiB.")
	}
	var schema jsonschema.Schema
	if json.Unmarshal(data, &schema) != nil || schema.Type != "object" {
		return nil, errors.New("MCP input schema must describe an object.")
	}
	// Resolving remote $refs is deliberately unsupported: scanning is not a fetch.
	return schema.Resolve(nil) // A nil Loader refuses external schema references.
}

func discoverMCPCatalog(ctx context.Context, session *mcp.ClientSession) (*MCPCatalog, error) {
	catalog := &MCPCatalog{Tools: []*mcp.Tool{}}
	caps := session.InitializeResult().Capabilities
	catalog.Resources = caps.Resources != nil
	if caps.Tools != nil {
		cursor := ""
		seenPages := map[string]bool{}
		seenNames := map[string]bool{}
		total := 0
		for page := 0; page < 32; page++ {
			result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
			if err != nil {
				return nil, err
			}
			for _, tool := range result.Tools {
				if tool == nil || len(tool.Name) == 0 || len(tool.Name) > 200 || seenNames[tool.Name] || len(tool.Description) > 16384 {
					return nil, errors.New("Invalid or duplicate MCP tool.")
				}
				if _, err := resolveMCPSchema(tool.InputSchema); err != nil {
					return nil, err
				}
				seenNames[tool.Name] = true
				catalog.Tools = append(catalog.Tools, tool)
				data, _ := json.Marshal(tool)
				total += len(data)
				if len(catalog.Tools) > 512 || total > 2<<20 {
					return nil, errors.New("MCP catalog exceeds 512 tools or 2 MiB.")
				}
			}
			cursor = result.NextCursor
			if len(cursor) > 4096 {
				return nil, errors.New("MCP discovery cursor exceeds 4096 bytes.")
			}
			if cursor == "" {
				break
			}
			if seenPages[cursor] || page == 31 {
				return nil, errors.New("MCP catalog pagination exceeds limits.")
			}
			seenPages[cursor] = true
		}
	}
	sort.Slice(catalog.Tools, func(i, j int) bool { return catalog.Tools[i].Name < catalog.Tools[j].Name })
	data, _ := json.Marshal(catalog)
	catalog.Revision = revision(data)
	return catalog, nil
}

type mcpRoute struct {
	connection MCPConnectionSnapshot
	original   string
	operation  string
	schema     *jsonschema.Resolved
}
type mcpExecution struct {
	callIDs     map[string]bool
	closeErr    error
	activeCalls map[string]string
	mu          sync.Mutex
	closed      bool
	wg          sync.WaitGroup
	live        map[string]*mcpLiveConnection
	routes      map[string]mcpRoute
	tools       []map[string]any
	cancel      context.CancelFunc
	once        sync.Once
}

func mcpToolName(id, original string) string {
	return "ext_" + id[:16] + "_" + revision([]byte(original))[:24]
}

func (p *adapter) prepareMCP() error {
	if p.capabilities == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(p.turn.ctx)
	x := &mcpExecution{live: map[string]*mcpLiveConnection{}, routes: map[string]mcpRoute{}, activeCalls: map[string]string{}, callIDs: map[string]bool{}, cancel: cancel}
	p.mcp = x
	startupDeadline := time.Now().Add(20 * time.Second)
	for index, c := range p.capabilities.Connections {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !validLibraryConnectionID(c.ID) || c.Selection.ConnectionID != c.ID {
			return errors.New("Corrupted MCP connection snapshot.")
		}
		if time.Now().After(startupDeadline) {
			p.capabilities.Diagnostics = append(p.capabilities.Diagnostics, c.Name+" was not connected before the startup deadline. Selection is retained.")
			continue
		}
		connectCtx, connectCancel := context.WithDeadline(ctx, minTime(startupDeadline, time.Now().Add(8*time.Second)))
		live, err := p.app.connectMCP(connectCtx, c, p.cwd, p)
		var catalog *MCPCatalog
		if err == nil {
			catalog, err = discoverMCPCatalog(connectCtx, live.session)
			if err == nil && live.catalogChanged.Load() {
				err = errors.New("MCP catalog changed during capture.")
			}
		}
		// Stop only the startup deadline. Stdio lifetime follows the turn, not it.
		if live != nil && live.stop != nil {
			live.stop()
			live.stop = context.AfterFunc(ctx, func() { _ = live.owner.Stop() })
		}
		connectCancel()
		if err != nil {
			if live != nil {
				err = errors.Join(err, live.close())
			}
			var lifecycle *mcpLifecycleError
			if errors.As(err, &lifecycle) {
				p.processDrainFailed = true
				return err
			}
			p.capabilities.Diagnostics = append(p.capabilities.Diagnostics, fmt.Sprintf("%s is unavailable. Selection is retained; check connection or authenticate in Library.", c.Name))
			continue
		}
		if p.role != "graph_node" {
			c.Catalog = catalog
			p.capabilities.Connections[index].Catalog = catalog
		} else if c.Catalog == nil {
			if err := live.close(); err != nil {
				p.processDrainFailed = true
				return err
			}
			p.capabilities.Diagnostics = append(p.capabilities.Diagnostics, c.Name+" had no known catalog when this run started; no tools are injected during the run.")
			continue
		}
		x.live[c.ID] = live
		capturedTools, actualTools := map[string]*mcp.Tool{}, map[string]*mcp.Tool{}
		for _, tool := range c.Catalog.Tools {
			capturedTools[tool.Name] = tool
		}
		for _, tool := range catalog.Tools {
			actualTools[tool.Name] = tool
		}
		// Iterate the saved positive selection, not discovery: a selected tool
		// absent from a refreshed chat catalog still needs an explicit warning.
		for _, selectedName := range c.Selection.Tools {
			tool, actual := capturedTools[selectedName], actualTools[selectedName]
			if tool == nil || actual == nil || permissionHash(actual) != permissionHash(tool) {
				p.capabilities.Diagnostics = append(p.capabilities.Diagnostics, c.Name+" / "+selectedName+" changed or disappeared from the captured catalog. Selection is retained.")
				continue
			}
			schema, err := resolveMCPSchema(tool.InputSchema)
			if err != nil {
				return err
			}
			name := mcpToolName(c.ID, "tool/"+tool.Name)
			if _, exists := x.routes[name]; exists {
				return errors.New("MCP technical tool name collision.")
			}
			x.routes[name] = mcpRoute{connection: c, original: tool.Name, operation: "call", schema: schema}
			x.tools = append(x.tools, map[string]any{"name": name, "description": c.Name + " / " + tool.Name + "\nExternal server content: " + tool.Description, "inputSchema": tool.InputSchema, "external": true})
		}
		if c.Catalog.Resources && catalog.Resources {
			for _, operation := range []string{"list", "templates", "read"} {
				name := mcpToolName(c.ID, "resource/"+operation)
				if _, exists := x.routes[name]; exists {
					return errors.New("MCP tool/resource technical name collision.")
				}
				properties := map[string]any{"cursor": map[string]any{"type": "string", "maxLength": 4096}}
				required := []string{}
				if operation == "read" {
					properties = map[string]any{"uri": map[string]any{"type": "string", "minLength": 1, "maxLength": 8192}}
					required = []string{"uri"}
				}
				input := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
				schema, _ := resolveMCPSchema(input)
				x.routes[name] = mcpRoute{connection: c, original: "resources/" + operation, operation: operation, schema: schema}
				x.tools = append(x.tools, map[string]any{"name": name, "description": c.Name + ": resources " + operation + ". URIs are opaque server identifiers. Use returned cursors for bounded pages.", "inputSchema": input, "external": true})
			}
		}
	}
	sealCapabilitySnapshot(p.capabilities)
	for _, diagnostic := range p.capabilities.Diagnostics {
		if err := p.capabilityWarning(diagnostic); err != nil {
			return err
		}
	}
	return nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func slicesContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (p *adapter) capabilityWarning(message string) error {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	return p.app.commitLocked(func(d *diskState) {
		s := d.session(p.id)
		s.Events = append(s.Events, event("status", "Library: "+message))
		s.UpdatedAt = now()
	})
}

func (p *adapter) closeMCP() error {
	if p.mcp == nil {
		return nil
	}
	x := p.mcp
	x.once.Do(func() {
		x.mu.Lock()
		x.closed = true
		x.cancel()
		x.mu.Unlock()
		for _, live := range x.live {
			x.closeErr = errors.Join(x.closeErr, live.close())
		}
		x.wg.Wait()
	})
	if x.closeErr != nil {
		p.processDrainFailed = true
	}
	return x.closeErr
}

func (p *adapter) externalMCPTool(name string) bool {
	if p.mcp == nil {
		return false
	}
	_, exists := p.mcp.routes[name]
	return exists
}

func (p *adapter) callExternalMCP(ctx context.Context, name string, raw json.RawMessage, callID string) (*mcp.CallToolResult, error) {
	x := p.mcp
	if x == nil {
		return nil, errors.New("MCP execution is unavailable.")
	}
	route, exists := x.routes[name]
	if !exists {
		return nil, errors.New("MCP tool is not selected for this execution.")
	}
	var arguments map[string]any
	if len(raw) > 256<<10 || json.Unmarshal(raw, &arguments) != nil || arguments == nil {
		return nil, errors.New("Invalid MCP arguments.")
	}
	if err := route.schema.Validate(arguments); err != nil {
		return nil, errors.New("Arguments do not match the captured MCP tool schema.")
	}
	x.mu.Lock()
	if x.closed || p.turn.ctx.Err() != nil || x.callIDs[callID] {
		x.mu.Unlock()
		return nil, errors.New("MCP execution ended.")
	}
	x.callIDs[callID] = true
	x.wg.Add(1)
	x.mu.Unlock()
	defer x.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	stop := context.AfterFunc(p.turn.ctx, cancel)
	defer stop()
	if err := p.authorizeExternalMCP(ctx, route, name, callID, arguments); err != nil {
		return nil, err
	}
	live := x.live[route.connection.ID]
	if live.catalogChanged.Load() {
		return nil, errors.New("MCP catalog changed. This call was not sent; start a new turn to capture the new catalog.")
	}
	x.mu.Lock()
	if x.closed || x.activeCalls[route.connection.ID] != "" {
		x.mu.Unlock()
		return nil, errors.New("This connection already has an active call or has ended.")
	}
	x.activeCalls[route.connection.ID] = callID
	x.mu.Unlock()
	defer func() { x.mu.Lock(); delete(x.activeCalls, route.connection.ID); x.mu.Unlock() }()
	key := graphMCPCallKey{Session: p.id, Call: callID}
	if p.graphNode() {
		p.graph.mu.Lock()
		if p.graph.state != "open" || p.graph.finished || p.turn.ctx.Err() != nil {
			p.graph.mu.Unlock()
			return nil, errors.New("Graph activation is sealed.")
		}
		p.graph.admitMCPCallLocked(key, route.connection.ID+"/"+route.original)
		p.graph.mu.Unlock()
	}
	var result *mcp.CallToolResult
	var err error
	if route.operation == "call" {
		result, err = live.session.CallTool(ctx, &mcp.CallToolParams{Name: route.original, Arguments: arguments})
	} else {
		var value any
		switch route.operation {
		case "list":
			value, err = live.session.ListResources(ctx, &mcp.ListResourcesParams{Cursor: str(arguments, "cursor")})
		case "templates":
			value, err = live.session.ListResourceTemplates(ctx, &mcp.ListResourceTemplatesParams{Cursor: str(arguments, "cursor")})
		case "read":
			value, err = live.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: str(arguments, "uri")})
		}
		if err == nil {
			encoded, _ := json.Marshal(value)
			result = &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}, StructuredContent: value}
		}
	}
	if p.graphNode() {
		p.graph.mu.Lock()
		state := "response"
		var protocolError *jsonrpc.Error
		var notSent *mcpNotSentError
		if errors.As(err, &notSent) {
			state = "not_started"
		} else if err != nil && !errors.As(err, &protocolError) {
			state = "uncertain"
		}
		p.graph.endMCPCallLocked(key, state)
		p.graph.mu.Unlock()
	}
	if err != nil {
		var notSent *mcpNotSentError
		if errors.As(err, &notSent) {
			return nil, errors.New("MCP request was not sent. Check authentication in Library before trying again.")
		}
		var protocolError *jsonrpc.Error
		if errors.As(err, &protocolError) {
			return nil, errors.New("The MCP server returned a protocol error. Its response was received; the call was not repeated.")
		}
		return nil, errors.New("MCP call failed or completion is unconfirmed. It was not retried. Check the server before repeating the operation.")
	}
	if result != nil && live.catalogChanged.Load() {
		result.IsError = true
		result.Content = append(result.Content, &mcp.TextContent{Text: "The server catalog changed during this call. The returned response is preserved; the call was not repeated. Refresh capabilities on the next turn."})
	}
	return translateMCPResult(result)
}

func (p *adapter) authorizeExternalMCP(ctx context.Context, route mcpRoute, name, callID string, arguments map[string]any) error {
	answer := make(chan bool, 1)
	source := "mcp/" + callID
	err := p.requestPermission(Permission{mcpTool: true, Kind: "mcp", Title: "Allow " + route.connection.Name + " / " + route.original + "?", Description: route.connection.Name + " / " + route.original, SourceID: source, Details: map[string]any{"toolName": name, "connectionId": route.connection.ID}}, map[string]any{"connectionId": route.connection.ID, "operation": route.operation, "tool": route.original, "arguments": arguments}, func(allow bool) error {
		select {
		case answer <- allow:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})
	if err != nil {
		return err
	}
	defer p.dismissPermission(source)
	select {
	case allowed := <-answer:
		if !allowed {
			return errors.New("MCP call denied.")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func translateMCPResult(result *mcp.CallToolResult) (*mcp.CallToolResult, error) {
	if result == nil {
		return nil, errors.New("MCP server returned no result.")
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 || len(result.Content) > 100 {
		return nil, errors.New("MCP response exceeds the adapter limit (1 MiB / 100 blocks); response was returned by the server and must not be repeated automatically.")
	}
	translated := &mcp.CallToolResult{IsError: result.IsError, StructuredContent: result.StructuredContent, Content: []mcp.Content{}}
	for _, block := range result.Content {
		switch block.(type) {
		case *mcp.TextContent:
			translated.Content = append(translated.Content, block)
		case *mcp.ImageContent:
			image := block.(*mcp.ImageContent)
			if !slicesContains([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}, image.MIMEType) {
				return nil, errors.New("The MCP server returned an unsupported image format. Its response completed; the call was not repeated.")
			}
			translated.Content = append(translated.Content, block)
		case *mcp.ResourceLink, *mcp.EmbeddedResource:
			// Resource blocks retain their full representation as marked JSON; binary
			// data stays explicit rather than being silently interpreted as a file.
			data, _ := json.Marshal(block)
			translated.Content = append(translated.Content, &mcp.TextContent{Text: "MCP resource block (JSON): " + string(data)})
		default:
			return nil, errors.New("MCP returned a block unsupported by this adapter; the server response completed, but its content could not be delivered.")
		}
	}
	if result.StructuredContent != nil {
		data, _ := json.Marshal(result.StructuredContent)
		translated.Content = append(translated.Content, &mcp.TextContent{Text: "Structured content (JSON): " + string(data)})
	}
	return translated, nil
}
