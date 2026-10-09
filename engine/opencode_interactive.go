package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const openCodeFrameLimit = 2 << 20
const openCodeResponseLimit = 32 << 20

//go:embed opencode-graph-plugin.mjs
var openCodeGraphPlugin string

//go:embed opencode-permissions-plugin.mjs
var openCodePermissionsPlugin string

func (p *adapter) openCodeGraphConfig() (string, func(), error) {
	config := map[string]any{}
	if existing := os.Getenv("OPENCODE_CONFIG_CONTENT"); strings.TrimSpace(existing) != "" {
		if json.Unmarshal([]byte(existing), &config) != nil || config == nil {
			return "", nil, errors.New("Cannot safely extend OPENCODE_CONFIG_CONTENT for the owned graph plugin.")
		}
	}
	dir, err := os.MkdirTemp(p.app.dir, ".opencode-graph-")
	if err != nil {
		return "", nil, errors.New("Cannot create the owned graph plugin directory.")
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	pluginPath := filepath.Join(dir, "klm-policy.mjs")
	data, _ := json.Marshal(map[string]string{"url": p.bridge.url, "token": p.bridge.token})
	plugin := strings.Replace(openCodeGraphPlugin, "/*KLM_GRAPH_CONFIG*/{}", string(data), 1)
	if !p.graphNode() {
		plugin = "export default async function () { return {}; }"
	}
	policyData, _ := json.Marshal(map[string]any{"url": p.bridge.url, "token": p.bridge.token, "yolo": p.yolo})
	plugin = strings.Replace(plugin, "export default async function", "async function graphPlugin", 1) + "\n" +
		strings.Replace(openCodePermissionsPlugin, "/*KLM_PERMISSION_CONFIG*/{}", string(policyData), 1)
	if err := os.WriteFile(pluginPath, []byte(plugin), 0600); err != nil {
		cleanup()
		return "", nil, errors.New("Cannot write the owned graph plugin.")
	}
	plugins := []any{}
	if value, ok := config["plugin"]; ok {
		var valid bool
		plugins, valid = value.([]any)
		if !valid {
			cleanup()
			return "", nil, errors.New("OpenCode plugin configuration is not a list.")
		}
	}
	path := filepath.ToSlash(pluginPath)
	if runtime.GOOS == "windows" {
		path = "/" + path
	}
	config["plugin"] = append(plugins, (&url.URL{Scheme: "file", Path: path}).String())
	encoded, err := json.Marshal(config)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return string(encoded), cleanup, nil
}

type openCodeHTTP struct {
	client    *http.Client
	base      string
	directory string
	password  string
}

type openCodeServer struct {
	mcpPrefixes  []string
	h            *openCodeHTTP
	transport    *http.Transport
	owner        *ownedProcess
	processDone  <-chan struct{}
	outputFailed <-chan error
	cleanup      func()
	once         sync.Once
}

type openCodeSubagent struct {
	adapter *adapter
	infos   map[string]map[string]any
	parts   map[string]map[string]any
	usage   *openCodeUsage
}

func (s *openCodeServer) Alive() bool {
	select {
	case <-s.processDone:
		return false
	default:
		return true
	}
}

func (s *openCodeServer) Close() {
	s.once.Do(func() {
		s.transport.CloseIdleConnections()
		_ = s.owner.Stop()
		select {
		case <-s.processDone:
		case <-time.After(5 * time.Second):
		}
		_ = s.owner.Close()
		if s.cleanup != nil {
			s.cleanup()
		}
	})
}

func (h *openCodeHTTP) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, errors.New("Could not encode the OpenCode request.")
		}
		if len(data) > openCodeResponseLimit {
			return nil, errors.New("OpenCode request exceeded the 32 MiB limit.")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, h.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("Could not construct the OpenCode request.")
	}
	query := req.URL.Query()
	query.Set("directory", h.directory)
	req.URL.RawQuery = query.Encode()
	req.SetBasicAuth("opencode", h.password)
	req.Header.Set("Accept", "application/json")
	if path == "/event" {
		req.Header.Set("Accept", "text/event-stream")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Neither diagnostics nor HTTP bodies are safe to expose as transport errors.
		return nil, errors.New("Could not communicate with the owned OpenCode server.")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("OpenCode %s request failed (HTTP %d).", method, response.StatusCode)
	}
	return response, nil
}

func (h *openCodeHTTP) json(ctx context.Context, method, path string, body, result any) error {
	_, err := h.jsonResponse(ctx, method, path, body, result)
	return err
}

var errOpenCodeResponseTooLarge = errors.New("response exceeded KLM's 32 MiB limit")

func (h *openCodeHTTP) jsonResponse(ctx context.Context, method, path string, body, result any) (http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Identify the operation without including directory/query values or response bodies.
	endpoint, _, _ := strings.Cut(path, "?")
	failure := func(err error) (http.Header, error) {
		return nil, fmt.Errorf("OpenCode %s %s: %w", method, endpoint, err)
	}
	response, err := h.request(ctx, method, path, body)
	if err != nil {
		return failure(err)
	}
	defer response.Body.Close()
	var data []byte
	if method == http.MethodGet && strings.HasPrefix(endpoint, "/session/") && strings.HasSuffix(endpoint, "/message") && result != nil {
		data, err = readOpenCodeHistoryJSON(response.Body)
	} else {
		data, err = io.ReadAll(io.LimitReader(response.Body, openCodeResponseLimit+1))
	}
	if err != nil {
		if errors.Is(err, errOpenCodeResponseTooLarge) || errors.Is(err, errOpenCodeHistoryWireLimit) || errors.Is(err, errOpenCodeHistoryJSON) {
			return failure(err)
		}
		return failure(errors.New("could not read the response"))
	}
	if len(data) > openCodeResponseLimit {
		return failure(errOpenCodeResponseTooLarge)
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			return failure(errors.New("invalid JSON response"))
		}
	}
	return response.Header, nil
}

// OpenCode 1.18.30 returns the newest page in chronological order and supplies
// X-Next-Cursor for older pages. Visit newest first so reconciliation can stop at
// the pre-turn baseline. Only one page of historical message bodies is retained.
func (h *openCodeHTTP) walkMessages(ctx context.Context, sessionID string, baseline map[string]bool, visit func(map[string]any) error) error {
	limit, before := 64, ""
	cursors := map[string]bool{}
	seen := map[string]bool{}
	for {
		query := url.Values{"limit": {strconv.Itoa(limit)}}
		if before != "" {
			query.Set("before", before)
		}
		path := "/session/" + url.PathEscape(sessionID) + "/message?" + query.Encode()
		var page []map[string]any
		headers, err := h.jsonResponse(ctx, http.MethodGet, path, nil, &page)
		if (errors.Is(err, errOpenCodeResponseTooLarge) || errors.Is(err, errOpenCodeHistoryWireLimit)) && limit > 1 {
			limit /= 2
			continue
		}
		if err != nil {
			return err
		}
		next := headers.Get("X-Next-Cursor")
		if page == nil || len(page) > limit || (next != "" && (len(page) == 0 || cursors[next])) {
			return errors.New("OpenCode returned an invalid or non-advancing history page.")
		}
		for i := len(page) - 1; i >= 0; i-- {
			message := page[i]
			info := object(message["info"])
			id := str(info, "id")
			if id == "" || str(info, "sessionID") != sessionID || seen[id] {
				return errors.New("OpenCode history contained an invalid or repeated message.")
			}
			seen[id] = true
			if baseline[id] {
				return nil
			}
			parts, ok := message["parts"].([]any)
			if !ok {
				return errors.New("OpenCode history did not contain message parts.")
			}
			for _, value := range parts {
				part := object(value)
				if str(part, "sessionID") != sessionID || str(part, "messageID") != id {
					return errors.New("OpenCode history contained an invalid message part.")
				}
			}
			if err := visit(message); err != nil {
				return err
			}
		}
		if next == "" {
			return nil
		}
		cursors[next] = true
		before = next
	}
}

// Permission/question events can precede task metadata, or already be pending
// when we subscribe. Verify native ancestry rather than dropping those requests
// or trusting every session sharing the server's directory.
func (h *openCodeHTTP) ownsSession(ctx context.Context, id string, owned map[string]bool) (bool, error) {
	var ancestors []string
	seen := map[string]bool{}
	for id != "" && !seen[id] && len(ancestors) < 32 {
		if owned[id] {
			for _, child := range ancestors {
				owned[child] = true
			}
			return true, nil
		}
		seen[id] = true
		ancestors = append(ancestors, id)
		var session map[string]any
		if err := h.json(ctx, http.MethodGet, "/session/"+url.PathEscape(id), nil, &session); err != nil {
			return false, err
		}
		if str(session, "id") != id {
			return false, errors.New("OpenCode returned a different session while resolving request ownership.")
		}
		id = str(session, "parentID")
	}
	return false, nil
}

// Image echoes get a bounded wire allowance; retained ordinary events stay at 2 MiB.
func openCodeEvents(ctx context.Context, body io.Reader, events chan<- map[string]any, failed chan<- error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), imageJSONLineLimit+1)
	var data []byte
	frameBytes := 0
	err := errors.New("OpenCode event stream disconnected before the turn completed.")
	for scanner.Scan() {
		line := scanner.Bytes()
		frameBytes += len(line) + 1
		if frameBytes > imageJSONLineLimit {
			err = errors.New("OpenCode event frame exceeded KLM's image transport limit.")
			break
		}
		if len(line) == 0 {
			if len(data) > 0 {
				var event map[string]any
				if json.Unmarshal(data, &event) != nil || str(event, "type") == "" {
					err = errors.New("OpenCode returned an invalid event frame.")
					break
				}
				omitNativeImageData(event)
				if len(data) > openCodeFrameLimit {
					retained, encodeErr := json.Marshal(event)
					if encodeErr != nil || len(retained) > openCodeFrameLimit {
						err = errors.New("OpenCode event content exceeded KLM's 2 MiB limit.")
						break
					}
				}
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}
			data = data[:0]
			frameBytes = 0
		} else if bytes.HasPrefix(line, []byte("data:")) {
			value := bytes.TrimPrefix(line[5:], []byte(" "))
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, value...)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		if errors.Is(scanErr, bufio.ErrTooLong) {
			err = fmt.Errorf("OpenCode event frame exceeded KLM's image transport limit: %w", scanErr)
		} else {
			err = fmt.Errorf("Could not read the OpenCode event stream: %w", scanErr)
		}
	}
	select {
	case failed <- err:
	case <-ctx.Done():
	}
}

func (p *adapter) startOpenCodeServer(b binary, cwd string, ctx context.Context) (_ *openCodeServer, err error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, errors.New("Could not generate OpenCode server credentials.")
	}
	h := &openCodeHTTP{directory: cwd, password: hex.EncodeToString(secret[:])}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	h.client = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("OpenCode server redirects are not allowed.")
	}}
	cleanup := func() {}
	graphConfig, cleanup, err := p.openCodeGraphConfig()
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	fail := func(err error) (*openCodeServer, error) {
		transport.CloseIdleConnections()
		cleanup()
		return nil, err
	}
	args := append(append([]string{}, b.args...), "serve", "--hostname", "127.0.0.1", "--port", "0", "--mdns=false")
	cmd := exec.Command(b.path, args...)
	cmd.Dir = cwd
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "OPENCODE_CONFIG_CONTENT") {
			continue
		}
		if !strings.EqualFold(key, "OPENCODE_SERVER_PASSWORD") && !strings.EqualFold(key, "OPENCODE_SERVER_USERNAME") &&
			!strings.EqualFold(key, "OPENCODE_ENABLE_QUESTION_TOOL") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "OPENCODE_SERVER_USERNAME=opencode", "OPENCODE_SERVER_PASSWORD="+h.password, "OPENCODE_ENABLE_QUESTION_TOOL=true")
	cmd.Env = append(cmd.Env, "OPENCODE_CONFIG_CONTENT="+graphConfig)
	var owner *ownedProcess
	if p.runtime == nil {
		owner, err = prepareOwnedProcess(cmd)
	} else {
		owner, err = p.runtime.prepare(cmd)
	}
	if err != nil {
		return fail(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = owner.Close()
		return fail(errors.New("Could not open OpenCode server output."))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = owner.Close()
		return fail(errors.New("Could not open OpenCode server diagnostics."))
	}
	announced := make(chan string, 1)
	outputFailed := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), openCodeFrameLimit+1)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if address, ok := strings.CutPrefix(line, "opencode server listening on "); ok {
				select {
				case announced <- address:
				default:
				}
			}
		}
		if scanner.Err() != nil {
			outputFailed <- errors.New("Could not read OpenCode server output within the 2 MiB line limit.")
		}
	}()
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	if err := ctx.Err(); err != nil {
		_ = owner.Close()
		return fail(err)
	}
	if err := cmd.Start(); err != nil {
		_ = owner.Close()
		return fail(errors.New("Could not start the OpenCode server. Check its installation and configuration."))
	}
	if err := owner.Attach(); err != nil {
		_ = owner.Stop()
		_ = cmd.Wait()
		_ = owner.Close()
		return fail(err)
	}
	processDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(processDone)
	}()
	server := &openCodeServer{h: h, transport: transport, owner: owner, processDone: processDone, outputFailed: outputFailed, cleanup: cleanup}
	failed := func(err error) (*openCodeServer, error) {
		server.Close()
		return nil, err
	}
	startup := time.NewTimer(30 * time.Second)
	defer startup.Stop()
	select {
	case address := <-announced:
		u, parseErr := url.Parse(address)
		if parseErr != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil ||
			u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return failed(errors.New("OpenCode announced an invalid loopback server address."))
		}
		port, parseErr := strconv.Atoi(u.Port())
		if parseErr != nil || port < 1 || port > 65535 || u.Host != net.JoinHostPort("127.0.0.1", u.Port()) {
			return failed(errors.New("OpenCode announced an invalid loopback server port."))
		}
		h.base = u.String()
	case err := <-outputFailed:
		return failed(err)
	case <-processDone:
		return failed(errors.New("OpenCode server exited before startup completed."))
	case <-startup.C:
		return failed(errors.New("OpenCode server startup timed out."))
	case <-ctx.Done():
		return failed(ctx.Err())
	}
	if p.graphNode() {
		var health struct {
			Healthy bool `json:"healthy"`
		}
		if err := h.json(ctx, http.MethodGet, "/global/health", nil, &health); err != nil {
			return failed(err)
		}
		if !health.Healthy {
			return failed(errors.New("OpenCode server health check failed."))
		}
	}
	var bridgeStatus map[string]any
	if err := h.json(ctx, http.MethodPost, "/mcp", map[string]any{"name": "klm_linked", "config": map[string]any{"type": "remote", "url": p.bridge.url, "headers": map[string]string{"Authorization": "Bearer " + p.bridge.token}, "oauth": false, "enabled": true, "timeout": linkedToolClientTimeout.Milliseconds()}}, &bridgeStatus); err != nil {
		return failed(errors.New("Could not configure the OpenCode linked-agent bridge."))
	}
	if str(object(bridgeStatus["klm_linked"]), "status") != "connected" {
		return failed(errors.New("OpenCode did not connect to the linked-agent bridge."))
	}
	server.mcpPrefixes = openCodeMCPToolPrefixes(bridgeStatus)
	if err := p.bridge.waitReady(ctx); err != nil {
		return failed(err)
	}
	if p.graphNode() {
		if err := p.waitGraphPlugin(ctx); err != nil {
			return failed(err)
		}
		var ids []string
		if err := h.json(ctx, http.MethodGet, "/experimental/tool/ids", nil, &ids); err != nil {
			return failed(err)
		}
		if len(ids) == 0 {
			return failed(errors.New("OpenCode did not identify its native tool inventory."))
		}
		p.setOpenCodeGraphTools(ids, bridgeStatus)
	}
	return server, nil
}

func (p *adapter) runOpenCode(b binary, cwd string, payload submission) (err error) {
	p.app.mu.Lock()
	s := *p.app.state.session(p.id)
	native := p.app.state.Native[p.id]
	p.app.mu.Unlock()
	t := p.turn
	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()
	p.completed = false

	server, reused := (*openCodeServer)(nil), false
	toolKey := p.runtimeToolKey()
	if p.runtime != nil {
		server, reused = p.runtime.getOpenCode(toolKey)
	}
	if !reused {
		server, err = p.startOpenCodeServer(b, cwd, ctx)
		if err != nil {
			return err
		}
		if p.runtime != nil {
			if err := p.runtime.retainOpenCode(server, toolKey); err != nil {
				server.Close()
				return err
			}
		}
	}
	h, owner := server.h, server.owner
	p.app.mu.Lock()
	p.openCodeMCPPrefixes = server.mcpPrefixes
	p.app.mu.Unlock()
	processDone, outputFailed := server.processDone, server.outputFailed
	p.processesDrained = false
	sessionID := ""
	promptAttempted := false
	abortSettled := false
	defer func() {
		// Cancel local subscriptions/replies first, then abort only our native turn.
		cancel()
		if sessionID != "" && promptAttempted && !p.completed && !(p.runtime != nil && t.ctx.Err() != nil) {
			abortCtx, abortCancel := context.WithTimeout(context.Background(), 2*time.Second)
			abortSettled = h.json(abortCtx, http.MethodPost, "/session/"+url.PathEscape(sessionID)+"/abort", nil, nil) == nil
			abortCancel()
		}
		if p.runtime == nil {
			server.Close()
			p.processesDrained = owner.Drained()
		} else {
			if err != nil && !(t.ctx.Err() != nil && abortSettled) {
				p.runtime.discardOpenCode(server)
			}
			p.processesDrained = true
		}
	}()
	p.markRuntimeInfrastructure()
	var session map[string]any
	if native.ID != "" {
		if err := h.json(ctx, http.MethodGet, "/session/"+url.PathEscape(native.ID), nil, &session); err != nil {
			return fmt.Errorf("Could not resume the stored OpenCode session: %w", err)
		}
		if str(session, "id") != native.ID {
			return errors.New("OpenCode returned a different native session than requested.")
		}
	} else if err := h.json(ctx, http.MethodPost, "/session", map[string]any{"title": s.Title}, &session); err != nil {
		return err
	}
	wanted, err := filepath.Abs(cwd)
	if err != nil {
		return errors.New("Could not resolve the OpenCode working directory.")
	}
	actual := str(session, "directory")
	if actual == "" || !filepath.IsAbs(actual) {
		return errors.New("OpenCode returned a session without an absolute working directory.")
	}
	wanted, actual = filepath.Clean(wanted), filepath.Clean(actual)
	match := wanted == actual
	if runtime.GOOS == "windows" {
		match = strings.EqualFold(wanted, actual)
	}
	if !match {
		return errors.New("Stored OpenCode session belongs to a different working directory.")
	}
	sessionID = str(session, "id")
	if sessionID == "" {
		return errors.New("OpenCode returned a session without an identifier.")
	}
	if err := p.nativeID(sessionID); err != nil {
		return err
	}
	p.setGraphNativeSession(sessionID)
	sessionPath := "/session/" + url.PathEscape(sessionID)
	baseline := map[string]bool{}
	usage := &openCodeUsage{messages: map[string]map[string]any{}, steps: map[string]map[string]map[string]any{}, windows: map[string]*int64{}}
	var providers struct {
		All []struct {
			ID     string `json:"id"`
			Models map[string]struct {
				Limit struct {
					Context float64 `json:"context"`
				} `json:"limit"`
			} `json:"models"`
		} `json:"all"`
	}
	// Model limits are optional telemetry; a failed lookup must not block execution.
	metricsCtx, metricsCancel := context.WithTimeout(ctx, 3*time.Second)
	if h.json(metricsCtx, http.MethodGet, "/provider", nil, &providers) == nil {
		for _, provider := range providers.All {
			for id, model := range provider.Models {
				usage.windows[provider.ID+"/"+id] = tokenCount(model.Limit.Context)
			}
		}
	}
	metricsCancel()
	if err := h.walkMessages(ctx, sessionID, nil, func(message map[string]any) error {
		info := object(message["info"])
		usage.message(info)
		for _, value := range message["parts"].([]any) {
			usage.part(object(value))
		}
		baseline[str(info, "id")] = true
		return nil
	}); err != nil {
		return err
	}
	if err := p.setUsage(usage.snapshot()); err != nil {
		return err
	}

	stream, err := h.request(ctx, http.MethodGet, "/event", nil)
	if err != nil {
		return err
	}
	defer stream.Body.Close()
	if !strings.HasPrefix(strings.ToLower(stream.Header.Get("Content-Type")), "text/event-stream") {
		return errors.New("OpenCode did not provide an event stream.")
	}
	events := make(chan map[string]any, 64)
	streamFailed := make(chan error, 1)
	go openCodeEvents(ctx, stream.Body, events, streamFailed)

	permissions := map[string]bool{}
	questions := map[string]*atomic.Bool{}
	ownedSessions := map[string]bool{sessionID: true}
	ownsRequest := func(id string) (bool, error) {
		if p.subagents[id] != nil || p.graphOwnsNativeSession(id) {
			ownedSessions[id] = true
		}
		return h.ownsSession(ctx, id, ownedSessions)
	}
	defer func() {
		cancel()
		for id, live := range questions {
			live.Store(false)
			_ = p.dismissQuestion(id)
		}
	}()
	requestQuestion := func(properties map[string]any) error {
		if owned, err := ownsRequest(str(properties, "sessionID")); err != nil || !owned {
			return err
		}
		return p.requestOpenCodeQuestion(ctx, h, properties, questions)
	}
	requestPermission := func(properties map[string]any) error {
		if owned, err := ownsRequest(str(properties, "sessionID")); err != nil || !owned {
			return err
		}
		encoded, err := json.Marshal(properties)
		if err != nil || len(encoded) > openCodeFrameLimit {
			return errors.New("OpenCode permission request exceeded the 2 MiB limit.")
		}
		id, kind := str(properties, "id"), str(properties, "permission")
		if id == "" || kind == "" {
			return errors.New("OpenCode returned an invalid permission request.")
		}
		if permissions[id] {
			return nil
		}
		replyPermission := func(reply string) error {
			err := h.json(ctx, http.MethodPost, "/permission/"+url.PathEscape(id)+"/reply", map[string]any{"reply": reply}, nil)
			if err == nil && reply == "reject" {
				p.openCodeCallNotStarted(str(properties, "sessionID"), str(object(properties["tool"]), "callID"))
			}
			return err
		}
		if p.graphSealed() {
			permissions[id] = true
			return replyPermission("reject")
		}
		if p.internalOpenCodeTool(kind) {
			if err := replyPermission("once"); err != nil {
				return err
			}
			permissions[id] = true
			return nil
		}
		if p.openCodePermissionApproved(properties) {
			if err := replyPermission("once"); err != nil {
				return err
			}
			permissions[id] = true
			return nil
		}
		patterns := []string{}
		values, ok := properties["patterns"].([]any)
		if !ok {
			return errors.New("OpenCode permission request has invalid patterns.")
		}
		for _, value := range values {
			pattern, ok := value.(string)
			if !ok {
				return errors.New("OpenCode permission request has an invalid pattern.")
			}
			patterns = append(patterns, pattern)
		}
		sort.Strings(patterns)
		metadata := map[string]any{}
		for key, value := range object(properties["metadata"]) {
			switch key {
			case "sessionID", "messageID", "partID", "callID", "toolCallID", "requestID", "permissionID":
				continue
			}
			metadata[key] = value
		}
		var scope map[string]any
		if len(patterns) > 0 {
			// Exact semantic equality only; never use native `always` patterns or IDs.
			scope = map[string]any{"permission": kind, "patterns": patterns, "metadata": metadata, "directory": wanted}
		}
		description := strings.Join(patterns, "\n")
		if detail := str(metadata, "description"); detail != "" {
			description = detail + "\n" + description
		}
		command := str(metadata, "command")
		if command == "" && (kind == "bash" || kind == "command") {
			command = strings.Join(patterns, "\n")
		}
		req := Permission{
			mcpTool: p.isOpenCodeMCPTool(kind),
			ID:      newID(), Harness: p.harness, Kind: kind, Title: "Allow " + kind,
			Description: strings.TrimSpace(description), Patterns: patterns,
			Details: metadata, Command: command, AllowLabel: "Allow", CreatedAt: now(), SourceID: id,
		}
		if command != "" {
			req.Path = wanted
		}
		if scope == nil {
			req.Decisions = []string{"once", "reject"}
		}
		if err := p.requestPermission(req, scope, func(allow bool) error {
			if err := t.ctx.Err(); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			reply := "reject"
			if allow && !p.graphSealed() {
				reply = "once"
			}
			return replyPermission(reply)
		}); err != nil {
			return err
		}
		permissions[id] = true
		return nil
	}
	var pending []map[string]any
	if err := h.json(ctx, http.MethodGet, "/permission", nil, &pending); err != nil {
		return err
	}
	for _, req := range pending {
		if err := requestPermission(req); err != nil {
			return err
		}
	}
	pending = nil
	if err := h.json(ctx, http.MethodGet, "/question", nil, &pending); err != nil {
		return err
	}
	for _, req := range pending {
		if err := requestQuestion(req); err != nil {
			return err
		}
	}

	infos := map[string]map[string]any{}
	parts := map[string]map[string]any{}
	children := map[string]*openCodeSubagent{}
	putPart := func(target *adapter, nativeSessionID string, targetBaseline map[string]bool, targetInfos map[string]map[string]any, part map[string]any, discoverSubagent bool) error {
		messageID, partID := str(part, "messageID"), str(part, "id")
		if targetBaseline[messageID] || str(targetInfos[messageID], "role") != "assistant" {
			return nil
		}
		if messageID == "" || partID == "" || str(part, "sessionID") != nativeSessionID {
			return errors.New("OpenCode returned an invalid message part.")
		}
		encoded, err := json.Marshal(part)
		if err != nil || len(encoded) > openCodeFrameLimit {
			return errors.New("OpenCode message part exceeded the 2 MiB limit.")
		}
		kind, title, body, status := str(part, "type"), "", str(part, "text"), "running"
		raw := map[string]any{"type": kind, "partID": partID, "messageID": messageID}
		switch kind {
		case "text", "reasoning":
			if kind == "text" {
				kind = "assistant"
			} else if strings.TrimSpace(body) == "" {
				return nil
			}
			if object(part["time"])["end"] != nil || object(infos[messageID]["time"])["completed"] != nil {
				status = "completed"
			}
			// Reasoning metadata can contain encrypted provider state; retain text only.
		case "tool":
			state := object(part["state"])
			title, body, status = str(part, "tool"), contentText(state["output"]), str(state, "status")
			if target == p && title == "klm_linked_graph_submit_choice" && status == "completed" {
				target.graphChoiceToolSettled(state["output"])
			}
			if status == "" {
				status = "pending"
			}
			if status == "error" {
				body = humanError(state["error"])
			}
			toolName := title
			if discoverSubagent && strings.EqualFold(toolName, "task") {
				kind = "subagent"
				input, metadata := object(state["input"]), object(state["metadata"])
				if description := str(input, "description"); description != "" {
					title = description
				} else {
					title = "Subagent"
				}
				nativeChildID := str(metadata, "sessionId")
				if nativeChildID != "" && (str(metadata, "parentSessionId") == "" || str(metadata, "parentSessionId") == sessionID) {
					selection := object(metadata["model"])
					model := ""
					if providerID, modelID := str(selection, "providerID"), str(selection, "modelID"); providerID != "" && modelID != "" {
						model = providerID + "/" + modelID
					}
					child, err := p.ensureSubagent(nativeChildID, title, model, str(metadata, "variant"))
					if err != nil {
						return err
					}
					if child != nil {
						raw["childSessionId"], raw["nativeAgentId"] = child.id, nativeChildID
						if children[nativeChildID] == nil {
							windows := map[string]*int64{}
							for id, window := range usage.windows {
								windows[id] = window
							}
							children[nativeChildID] = &openCodeSubagent{adapter: child, infos: map[string]map[string]any{}, parts: map[string]map[string]any{}, usage: &openCodeUsage{messages: map[string]map[string]any{}, steps: map[string]map[string]map[string]any{}, windows: windows}}
						}
						if status == "completed" || status == "error" || status == "cancelled" {
							if err := p.finishSubagent(child, status); err != nil {
								return err
							}
						}
					}
				}
			} else if title == "bash" {
				kind = "command"
				if command := str(object(state["input"]), "command"); command != "" {
					body = strings.TrimSuffix(command+"\n"+body, "\n")
				}
			} else if strings.HasPrefix(title, "mcp_") || strings.HasPrefix(title, "mcp.") || strings.Contains(title, "__") {
				kind = "mcp"
			}
			// Opaque tool names stay intact; do not guess that every custom tool is MCP.
			raw["tool"], raw["callID"], raw["state"] = toolName, part["callID"], state
		default:
			return nil
		}
		return target.put("opencode/"+messageID+"/"+partID, kind, title, body, status, false, raw)
	}

	prompt := map[string]any{"parts": payload.openCodeParts()}
	if p.model != "" {
		provider, model, ok := strings.Cut(p.model, "/")
		if !ok || provider == "" || model == "" {
			return errors.New("OpenCode model must use provider/model format.")
		}
		prompt["model"] = map[string]any{"providerID": provider, "modelID": model}
	}
	if p.effort != "" {
		prompt["variant"] = p.effort
	}
	select {
	case err := <-streamFailed:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-processDone:
		return errors.New("OpenCode server exited before prompt submission.")
	default:
	}
	promptAttempted = true
	if err := h.json(ctx, http.MethodPost, sessionPath+"/prompt_async", prompt, nil); err != nil {
		return err
	}
	// Events queued during prompt_async are processed only after submission succeeds.
	busy, ownedMessage := false, false
	steering := p.steeringChannel()
	steeringMessageID, steeringQueueID := "", ""
	latestSteeringID := ""
	steeringHTTPPending := false
	steeringResult := make(chan error, 1)
	var deferredIdle map[string]any
	steeringTimer := time.NewTimer(15 * time.Second)
	steeringTimer.Stop()
	defer steeringTimer.Stop()
	var steeringDeadline <-chan time.Time
	choiceStop := p.graphStop()
	choiceInterrupted := false
	for {
		if p.completed && !p.graphMCPPending() {
			return nil
		}
		nextEvents := events
		if deferredIdle != nil && steeringMessageID == "" && !steeringHTTPPending {
			replay := make(chan map[string]any, 1)
			replay <- deferredIdle
			deferredIdle, nextEvents = nil, replay
		}
		var inputReady <-chan struct{}
		if !steeringHTTPPending {
			inputReady = steering
		}
		select {
		case <-inputReady:
			q, payload, err := p.takeSteering()
			if err != nil {
				return err
			}
			if q == nil {
				continue
			}
			steeringQueueID = q.ID
			steeringMessageID = fmt.Sprintf("msg_%012x%s", (uint64(time.Now().UnixMilli())*0x1000+1)&0xffffffffffff, q.ID[:14])
			latestSteeringID = steeringMessageID
			steeringHTTPPending = true
			steeringTimer.Reset(15 * time.Second)
			steeringDeadline = steeringTimer.C
			nextPrompt := map[string]any{"messageID": steeringMessageID, "parts": payload.openCodeParts()}
			if model, ok := prompt["model"]; ok {
				nextPrompt["model"] = model
			}
			if variant, ok := prompt["variant"]; ok {
				nextPrompt["variant"] = variant
			}
			go func() {
				sendCtx, sendCancel := context.WithTimeout(ctx, 15*time.Second)
				defer sendCancel()
				steeringResult <- h.json(sendCtx, http.MethodPost, sessionPath+"/prompt_async", nextPrompt, nil)
			}()
		case <-steeringDeadline:
			return errors.New("OpenCode did not confirm the steering message before the delivery deadline.")
		case sendErr := <-steeringResult:
			steeringHTTPPending = false
			p.app.mu.Lock()
			p.app.wakeSteeringLocked(p.id)
			p.app.mu.Unlock()
			if sendErr != nil && steeringMessageID != "" {
				return fmt.Errorf("OpenCode message delivery was not confirmed: %w", sendErr)
			}
		case <-choiceStop:
			choiceStop = nil
			if p.completed {
				return nil
			}
			if err := h.json(ctx, http.MethodPost, sessionPath+"/abort", nil, nil); err != nil {
				return err
			}
			choiceInterrupted = true
		case <-ctx.Done():
			return ctx.Err()
		case <-processDone:
			return errors.New("OpenCode server exited before the turn completed.")
		case err := <-outputFailed:
			return err
		case err := <-streamFailed:
			return err
		case event := <-nextEvents:
			typ, properties := str(event, "type"), object(event["properties"])
			// Route human waits before the display-only child-session filter.
			// Replies remain on the owning conversation, keyed by native request ID.
			switch typ {
			case "permission.asked":
				if err := requestPermission(properties); err != nil {
					return err
				}
				continue
			case "question.asked":
				if err := requestQuestion(properties); err != nil {
					return err
				}
				continue
			case "permission.replied":
				id := str(properties, "requestID")
				if permissions[id] {
					if err := p.dismissPermission(id); err != nil {
						return err
					}
				}
				continue
			case "question.replied", "question.rejected":
				id := str(properties, "requestID")
				if live := questions[id]; live != nil {
					live.Store(false)
					if err := p.dismissQuestion(id); err != nil {
						return err
					}
				}
				continue
			}
			if typ == "message.part.updated" {
				p.observeOpenCodeMCPPart(object(properties["part"]))
			}
			scopedID := str(properties, "sessionID")
			if typ == "message.updated" {
				scopedID = str(object(properties["info"]), "sessionID")
			} else if typ == "message.part.updated" {
				scopedID = str(object(properties["part"]), "sessionID")
			} else if typ == "message.part.delta" && scopedID == "" {
				key := str(properties, "messageID") + "/" + str(properties, "partID")
				part := parts[key]
				scopedID = str(part, "sessionID")
				if scopedID == "" {
					for nativeID, child := range children {
						if child.parts[key] != nil {
							scopedID = nativeID
							break
						}
					}
				}
			}
			if child := children[scopedID]; child != nil {
				switch typ {
				case "message.updated":
					info := object(properties["info"])
					id := str(info, "id")
					if id == "" {
						return errors.New("OpenCode returned a subagent message without an identifier.")
					}
					child.infos[id] = info
					child.usage.message(info)
					if str(info, "role") == "assistant" {
						if err := child.adapter.resolvedSelection(str(info, "providerID")+"/"+str(info, "modelID"), str(info, "variant")); err != nil {
							return err
						}
					}
					if info["error"] != nil {
						if err := child.adapter.failure(map[string]any{"type": typ, "error": info["error"]}); err != nil {
							return err
						}
						if err := p.finishSubagent(child.adapter, "error"); err != nil {
							return err
						}
					}
					for _, part := range child.parts {
						if str(part, "messageID") == id {
							if err := putPart(child.adapter, scopedID, nil, child.infos, part, false); err != nil {
								return err
							}
						}
					}
					if err := child.adapter.setUsage(child.usage.snapshot()); err != nil {
						return err
					}
				case "message.part.updated":
					part := object(properties["part"])
					messageID, partID := str(part, "messageID"), str(part, "id")
					if messageID == "" || partID == "" {
						return errors.New("OpenCode returned a subagent part without identifiers.")
					}
					child.parts[messageID+"/"+partID] = part
					child.usage.part(part)
					if err := putPart(child.adapter, scopedID, nil, child.infos, part, false); err != nil {
						return err
					}
					if str(part, "type") == "step-finish" {
						if err := child.adapter.setUsage(child.usage.snapshot()); err != nil {
							return err
						}
					}
				case "message.part.delta":
					if str(properties, "field") != "text" {
						continue
					}
					part := child.parts[str(properties, "messageID")+"/"+str(properties, "partID")]
					if part == nil || (str(part, "type") != "text" && str(part, "type") != "reasoning") {
						continue
					}
					delta := str(properties, "delta")
					if len(str(part, "text"))+len(delta) > openCodeFrameLimit {
						return errors.New("OpenCode subagent text part exceeded the 2 MiB limit.")
					}
					part["text"] = str(part, "text") + delta
					if err := putPart(child.adapter, scopedID, nil, child.infos, part, false); err != nil {
						return err
					}
				case "session.error":
					if err := child.adapter.failure(map[string]any{"type": typ, "error": properties["error"]}); err != nil {
						return err
					}
					if err := p.finishSubagent(child.adapter, "error"); err != nil {
						return err
					}
				}
				continue
			}
			if scopedID != sessionID {
				continue
			}
			switch typ {
			case "session.compacted":
				for _, info := range usage.messages {
					if created := tokenCount(object(info["time"])["created"]); created != nil && *created > usage.compactedAt {
						usage.compactedAt = *created
					}
				}
				if err := p.setUsage(usage.snapshot()); err != nil {
					return err
				}
			case "session.error":
				if choiceInterrupted && str(object(properties["error"]), "name") == "MessageAbortedError" {
					continue
				}
				return p.failure(map[string]any{"type": typ, "error": properties["error"]})
			case "message.updated":
				info := object(properties["info"])
				id := str(info, "id")
				if id == "" || baseline[id] {
					continue
				}
				if id == steeringMessageID && str(info, "role") == "user" {
					if err := p.finishSteering(steeringQueueID, ""); err != nil {
						return err
					}
					steeringMessageID, steeringQueueID = "", ""
					steeringTimer.Stop()
					steeringDeadline = nil
				}
				ownedMessage = true
				infos[id] = info
				usage.message(info)
				if str(info, "role") == "assistant" {
					if err := p.resolvedSelection(str(info, "providerID")+"/"+str(info, "modelID"), str(info, "variant")); err != nil {
						return err
					}
				}
				if err := p.setUsage(usage.snapshot()); err != nil {
					return err
				}
				if str(info, "role") == "assistant" && info["error"] != nil && !(choiceInterrupted && str(object(info["error"]), "name") == "MessageAbortedError") {
					return p.failure(map[string]any{"type": typ, "error": info["error"]})
				}
				for _, part := range parts {
					if str(part, "messageID") == id {
						if err := putPart(p, sessionID, baseline, infos, part, true); err != nil {
							return err
						}
					}
				}
			case "message.part.updated":
				part := object(properties["part"])
				messageID, partID := str(part, "messageID"), str(part, "id")
				if baseline[messageID] {
					continue
				}
				if messageID == "" || partID == "" {
					return errors.New("OpenCode returned a message part without identifiers.")
				}
				parts[messageID+"/"+partID] = part
				usage.part(part)
				if str(part, "type") == "step-finish" {
					if err := p.setUsage(usage.snapshot()); err != nil {
						return err
					}
				}
				if err := putPart(p, sessionID, baseline, infos, part, true); err != nil {
					return err
				}
			case "message.part.delta":
				if str(properties, "field") != "text" {
					continue
				}
				part := parts[str(properties, "messageID")+"/"+str(properties, "partID")]
				if part == nil || (str(part, "type") != "text" && str(part, "type") != "reasoning") {
					// A missing initial part is recovered from the final snapshot.
					continue
				}
				delta := str(properties, "delta")
				if len(str(part, "text"))+len(delta) > openCodeFrameLimit {
					return errors.New("OpenCode text part exceeded the 2 MiB limit.")
				}
				part["text"] = str(part, "text") + delta
				if err := putPart(p, sessionID, baseline, infos, part, true); err != nil {
					return err
				}
			case "session.status", "session.idle":
				status := str(object(properties["status"]), "type")
				if status == "busy" || status == "retry" {
					busy = true
					continue
				}
				if (typ != "session.idle" && status != "idle") || (!busy && !ownedMessage) {
					continue
				}
				if steeringMessageID != "" || steeringHTTPPending {
					deferredIdle = event
					continue
				}
				// Retain only this turn, then reconcile oldest first for UI ordering.
				var history []map[string]any
				if err := h.walkMessages(ctx, sessionID, baseline, func(message map[string]any) error {
					history = append(history, message)
					return nil
				}); err != nil {
					return err
				}
				terminalAssistant, incomplete := false, false
				// Drain live deltas first. Reconcile final native parts in one
				// durable batch, eliding unchanged upserts in streamChanges.
				if p.stream != nil {
					if err := p.stream.flush(nil); err != nil {
						return err
					}
				}
				latestCreated, latestID := float64(-1), ""
				latestParentID := ""
				nativeFailure := false
				err := p.withFinalBatch(func() error {
					for i := len(history) - 1; i >= 0; i-- {
						message := history[i]
						info := object(message["info"])
						id := str(info, "id")
						if id == "" || baseline[id] || str(info, "role") != "assistant" {
							continue
						}
						if str(info, "sessionID") != sessionID {
							return errors.New("OpenCode history contained a different native session.")
						}
						infos[id] = info
						usage.message(info)
						if failed, err := p.openCodeFinalError(info, choiceInterrupted); failed || err != nil {
							nativeFailure = failed
							return err
						}
						completed := object(info["time"])["completed"] != nil
						if !completed {
							incomplete = true
						}
						created, _ := object(info["time"])["created"].(float64)
						if created > latestCreated || (created == latestCreated && id > latestID) {
							latestCreated, latestID = created, id
							latestParentID = str(info, "parentID")
							finish := str(info, "finish")
							terminalAssistant = completed && finish != "" && finish != "tool-calls" && finish != "unknown"
							if choiceInterrupted && completed && str(object(info["error"]), "name") == "MessageAbortedError" {
								terminalAssistant = true
							}
						}
						completeParts, ok := message["parts"].([]any)
						if !ok {
							return errors.New("OpenCode history did not contain message parts.")
						}
						for _, value := range completeParts {
							part := object(value)
							if str(part, "messageID") != id {
								return errors.New("OpenCode history contained an invalid message part.")
							}
							usage.part(part)
							if str(part, "type") == "tool" {
								state := str(object(part["state"]), "status")
								if state != "completed" && state != "error" {
									incomplete = true
								}
							}
							if err := putPart(p, sessionID, baseline, infos, part, true); err != nil {
								return err
							}
						}
					}
					return nil
				})
				if err != nil {
					return err
				}
				if nativeFailure {
					return nil
				}
				if err := p.setUsage(usage.snapshot()); err != nil {
					return err
				}
				if latestSteeringID != "" && (latestParentID != latestSteeringID || incomplete || !terminalAssistant) {
					continue
				}
				if incomplete || !terminalAssistant {
					return errors.New("OpenCode became idle without a completed assistant response.")
				}
				select {
				case err := <-streamFailed:
					return err
				case <-ctx.Done():
					return ctx.Err()
				case <-processDone:
					return errors.New("OpenCode server exited during final reconciliation.")
				default:
				}
				if err := p.put("opencode/completed", "status", "", "Turn completed.", "completed", false, map[string]any{"type": typ}); err != nil {
					return err
				}
				p.completed = true
				p.nativeSettled = true
				if !p.graphMCPPending() {
					return nil
				}
			}
		}
	}
}

func (p *adapter) openCodeFinalError(info map[string]any, choiceInterrupted bool) (bool, error) {
	if info["error"] == nil || (choiceInterrupted && str(object(info["error"]), "name") == "MessageAbortedError") {
		return false, nil
	}
	return true, p.failure(map[string]any{"type": "message.updated", "error": info["error"]})
}

// Called only after native request ownership has been verified. Questions from
// descendants live on the parent conversation, but retain their native ID.
func (p *adapter) requestOpenCodeQuestion(ctx context.Context, h *openCodeHTTP, properties map[string]any, questions map[string]*atomic.Bool) error {
	id := str(properties, "id")
	if strings.TrimSpace(id) == "" {
		return errors.New("OpenCode returned a question without an identifier.")
	}
	if questions[id] != nil {
		return nil
	}
	if p.graphSealed() {
		return h.json(ctx, http.MethodPost, "/question/"+url.PathEscape(id)+"/reject", nil, nil)
	}
	encoded, err := json.Marshal(properties["questions"])
	if err != nil || len(encoded) > openCodeFrameLimit {
		return errors.New("OpenCode question request exceeded the 2 MiB limit.")
	}
	var nativeQuestions []struct {
		Question string           `json:"question"`
		Header   string           `json:"header"`
		Options  []QuestionOption `json:"options"`
		Multiple bool             `json:"multiple"`
		Custom   *bool            `json:"custom"`
	}
	if json.Unmarshal(encoded, &nativeQuestions) != nil {
		return errors.New("OpenCode returned an invalid question request.")
	}
	req := QuestionRequest{SourceID: id}
	for index, question := range nativeQuestions {
		req.Items = append(req.Items, QuestionItem{
			ID: strconv.Itoa(index), Header: question.Header, Text: question.Question,
			Options: question.Options, Multiple: question.Multiple,
			Custom: question.Custom == nil || *question.Custom,
		})
	}
	live := &atomic.Bool{}
	live.Store(true)
	questions[id] = live
	return p.requestQuestion(req, func(answers [][]string, cancelled bool) error {
		// answerQuestion serializes submissions. A transport failure must not
		// consume the question locally and silently disable the user's Retry.
		if p.turn.ctx.Err() != nil || ctx.Err() != nil || !live.Load() {
			return errors.New("The OpenCode question is no longer active.")
		}
		path := "/question/" + url.PathEscape(id)
		var body any
		if cancelled || p.graphSealed() {
			path += "/reject"
		} else {
			path += "/reply"
			body = map[string]any{"answers": answers}
		}
		var accepted bool
		if err := h.json(ctx, http.MethodPost, path, body, &accepted); err != nil {
			return err
		}
		if !accepted {
			return errors.New("OpenCode did not accept the question response.")
		}
		live.Store(false)
		return nil
	})
}
