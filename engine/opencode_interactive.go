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
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
	if existing := os.Getenv("OPENCODE_CONFIG_CONTENT"); strings.TrimSpace(existing) != "" && !(p.capabilities != nil && p.harness == "opencode") {
		if json.Unmarshal([]byte(existing), &config) != nil || config == nil {
			return "", nil, errors.New("Cannot safely extend OPENCODE_CONFIG_CONTENT for the owned graph plugin.")
		}
	}
	dir, err := os.MkdirTemp(p.app.dir, ".opencode-graph-")
	if err != nil {
		return "", nil, errors.New("Cannot create the owned graph plugin directory.")
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if p.openCodeV1 && p.capabilities != nil {
		p.openCodeLibraryEnv, err = p.configureOpenCodeLibrary(config, dir)
		if err != nil {
			cleanup()
			return "", nil, err
		}
	}
	if !p.openCodeV1 && p.capabilities != nil {
		p.openCodeLibraryEnv, err = p.configureOpenCodeV2Library(config, dir)
		if err != nil {
			cleanup()
			return "", nil, err
		}
	}
	if p.openCodeV1 {
		return p.openCodeV1Config(config, dir, cleanup)
	}
	pluginPath := filepath.Join(dir, "index.js")
	data, _ := json.Marshal(map[string]string{"url": p.bridge.url, "token": p.bridge.token})
	plugin := strings.Replace(openCodeGraphPlugin, "/*KLM_GRAPH_CONFIG*/{}", string(data), 1)
	if !p.graphNode() {
		plugin = "async function graphPlugin() {}"
	}
	policy := map[string]any{"url": p.bridge.url, "token": p.bridge.token, "yolo": p.yolo}
	if p.capabilities != nil {
		skills := []map[string]any{}
		for _, skill := range p.capabilities.Skills {
			skills = append(skills, map[string]any{"id": skill.ID, "path": filepath.Join(p.app.skillSnapshotPath(skill), "SKILL.md"), "internal": skill.Internal})
		}
		policy["library"] = map[string]any{"root": p.openCodeManagedAgent(), "skills": skills}
	}
	policyData, _ := json.Marshal(policy)
	plugin = plugin + "\n" +
		strings.Replace(openCodePermissionsPlugin, "/*KLM_PERMISSION_CONFIG*/{}", string(policyData), 1)
	_, delegationPlugin, assetErr := p.writeDelegationAssets(dir)
	if assetErr != nil {
		cleanup()
		return "", nil, assetErr
	}
	if delegationPlugin != "" {
		plugin = "import { setupV2 as delegationPlugin } from './delegation-opencode.mjs';\n" + plugin
	} else {
		plugin = "async function delegationPlugin() {}\n" + plugin
	}
	if err := os.WriteFile(pluginPath, []byte(plugin), 0600); err != nil {
		cleanup()
		return "", nil, errors.New("Cannot write the owned graph plugin.")
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"klm-policy","type":"module","exports":{".":"./index.js"}}`), 0600); err != nil {
		cleanup()
		return "", nil, errors.New("Cannot write the owned plugin manifest.")
	}
	plugins := []any{}
	value, ok := config["plugins"]
	if !ok {
		value, ok = config["plugin"]
	}
	if ok {
		var valid bool
		plugins, valid = value.([]any)
		if !valid {
			cleanup()
			return "", nil, errors.New("OpenCode plugin configuration is not a list.")
		}
		for i, value := range plugins {
			if legacy, ok := value.([]any); ok && len(legacy) == 2 {
				plugins[i] = map[string]any{"package": legacy[0], "options": legacy[1]}
			}
		}
	}
	delete(config, "plugin")
	path := filepath.ToSlash(dir)
	if runtime.GOOS == "windows" {
		path = "/" + path
	}
	config["plugins"] = append(plugins, (&url.URL{Scheme: "file", Path: path}).String())
	if p.yolo {
		config["permissions"] = []any{map[string]any{"action": "*", "resource": "*", "effect": "allow"}}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return string(encoded), cleanup, nil
}

type openCodeHTTP struct {
	v1        bool
	scope     *protocolContentScope
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
	taskKey string
	tasks   map[string]string
}

func (child *openCodeSubagent) updateTask(p *adapter, key, status string) error {
	if child.tasks == nil {
		child.tasks = map[string]string{}
	}
	if child.tasks[key] == status {
		return nil
	}
	active := status == "running" || status == "pending" || status == "started"
	if active {
		// A native child can serve several task calls. Reopening its session
		// belongs to the new call, not to repeated updates of an older call.
		if previous := child.tasks[key]; previous == "completed" || previous == "error" || previous == "cancelled" {
			return nil
		}
		if err := p.startSubagent(child.adapter); err != nil {
			return err
		}
		child.taskKey = key
	} else if status == "completed" || status == "error" || status == "cancelled" {
		// message.updated and final reconciliation replay old task parts.
		// Their terminal state must never settle a newer invocation.
		if child.taskKey == "" || child.taskKey == key {
			if err := p.finishSubagentInvocation(child.adapter, status, p.keys[key]); err != nil {
				return err
			}
			child.taskKey = key
		}
	}
	child.tasks[key] = status
	return nil
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
	if h.v1 {
		method, path, body = h.v1Request(method, path, body)
	}
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
	if h.v1 {
		query.Set("directory", h.directory)
	} else {
		query.Set("location[directory]", h.directory)
	}
	req.URL.RawQuery = query.Encode()
	req.SetBasicAuth("opencode", h.password)
	req.Header.Set("Accept", "application/json")
	if path == "/api/event" || path == "/event" {
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
		var transportError net.Error
		if errors.As(err, &transportError) && transportError.Timeout() {
			return nil, errors.New("OpenCode server request timed out while waiting for a response.")
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
	if method == http.MethodGet && (strings.HasPrefix(endpoint, "/api/session/") || strings.HasPrefix(endpoint, "/session/")) && strings.HasSuffix(endpoint, "/message") && result != nil {
		data, err = readOpenCodeHistoryJSONContext(ctx, response.Body)
	} else {
		limit := openCodeResponseLimit
		if h.scope != nil {
			limit = 1 << 20
		}
		data, err = io.ReadAll(io.LimitReader(response.Body, int64(limit+1)))
		if len(data) > limit && h.scope != nil {
			return failure(errors.New("control response exceeded the 1 MiB metadata capacity"))
		}
	}
	if err != nil {
		if errors.Is(err, errOpenCodeResponseTooLarge) || errors.Is(err, errOpenCodeHistoryWireLimit) || errors.Is(err, errOpenCodeHistoryJSON) {
			return failure(err)
		}
		if ctx.Err() != nil {
			return failure(ctx.Err())
		}
		return failure(errors.New("could not read the response"))
	}
	if len(data) > openCodeResponseLimit {
		return failure(errOpenCodeResponseTooLarge)
	}
	if result != nil {
		if !h.v1 && !(method == http.MethodGet && strings.HasSuffix(endpoint, "/message")) {
			var envelope map[string]json.RawMessage
			if json.Unmarshal(data, &envelope) == nil && envelope["data"] != nil {
				data = envelope["data"]
			}
		}
		if err := json.Unmarshal(data, result); err != nil {
			return failure(errors.New("invalid JSON response"))
		}
	}
	return response.Header, nil
}

// OpenCode 1.18.30 returns the newest page in chronological order and supplies
// X-Next-Cursor for older pages. Visit newest first so reconciliation can stop at
// the pre-turn baseline. Only one page of historical message bodies is retained.
func (h *openCodeHTTP) walkMessages(ctx context.Context, sessionID string, baseline any, visit func(map[string]any) error) error {
	if h.scope != nil && h.v1 {
		return h.walkNativeMessages(ctx, sessionID, baseline, visit)
	}
	limit, before := 64, ""
	cursors := map[string]bool{}
	seen := map[string]bool{}
	for {
		query := url.Values{"limit": {strconv.Itoa(limit)}}
		if before != "" {
			if h.v1 {
				query.Set("before", before)
			} else {
				query.Set("cursor", before)
			}
		} else if !h.v1 {
			query.Set("order", "desc")
		}
		path := "/api/session/" + url.PathEscape(sessionID) + "/message?" + query.Encode()
		var response struct {
			Data   []map[string]any `json:"data"`
			Cursor struct {
				Next string `json:"next"`
			} `json:"cursor"`
		}
		var err error
		if h.v1 {
			var headers http.Header
			headers, err = h.jsonResponse(ctx, http.MethodGet, path, nil, &response.Data)
			if err == nil {
				response.Cursor.Next = headers.Get("X-Next-Cursor")
				slices.Reverse(response.Data)
			}
		} else {
			_, err = h.jsonResponse(ctx, http.MethodGet, path, nil, &response)
		}
		if (errors.Is(err, errOpenCodeResponseTooLarge) || errors.Is(err, errOpenCodeHistoryWireLimit)) && limit > 1 {
			limit /= 2
			continue
		}
		if err != nil {
			return err
		}
		page, next := response.Data, response.Cursor.Next
		if page == nil || len(page) > limit || (next != "" && (len(page) == 0 || cursors[next])) {
			return errors.New("OpenCode returned an invalid or non-advancing history page.")
		}
		for _, value := range page {
			message := value
			if !h.v1 {
				message, err = openCodeV2Message(sessionID, value)
				if err != nil {
					return err
				}
			}
			info := object(message["info"])
			id := str(info, "id")
			if id == "" || str(info, "sessionID") != sessionID || seen[id] {
				return errors.New("OpenCode history contained an invalid or repeated message.")
			}
			seen[id] = true
			old, err := protocolIDSeen(baseline, id)
			if err != nil {
				return err
			}
			if old {
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
		if err := h.json(ctx, http.MethodGet, "/api/session/"+url.PathEscape(id), nil, &session); err != nil {
			return false, err
		}
		if str(session, "id") != id {
			return false, errors.New("OpenCode returned a different session while resolving request ownership.")
		}
		id = str(session, "parentID")
	}
	return false, nil
}

// Bound retained content independently of redundant wire data. Complete JSON and
// SSE framing are validated before an event reaches the adapter state machine.
func openCodeEvents(ctx context.Context, body io.Reader, dir string, events chan<- map[string]any, failed chan<- error, scopes ...*protocolContentScope) {
	openCodeVersionEvents(ctx, body, dir, events, failed, true, scopes...)
}

func openCodeVersionEvents(ctx context.Context, body io.Reader, dir string, events chan<- map[string]any, failed chan<- error, v1 bool, scopes ...*protocolContentScope) {
	projection := openCodeV2Projection{infos: map[string]map[string]any{}, parts: map[string]map[string]any{}}
	deliver := func(event map[string]any) error {
		projected := []map[string]any{event}
		if !v1 {
			projected = projection.event(event)
		}
		for _, item := range projected {
			select {
			case events <- item:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	var native []func(*protocolFrame) error
	var lastNative *protocolFrame
	if len(scopes) > 0 && scopes[0] != nil {
		native = append(native, func(frame *protocolFrame) error {
			event, err := frame.projectNative("opencode", openCodeFrameLimit, scopes[0])
			if err != nil {
				return err
			}
			err = deliver(event)
			if err == nil {
				if lastNative != nil && lastNative.scope != nil {
					lastNative.scope.ack(lastNative)
				}
				lastNative = frame
			}
			return err
		})
	}
	err := readProjectedSSE(ctx, body, dir, func(data []byte) error {
		var event map[string]any
		if json.Unmarshal(data, &event) != nil || str(event, "type") == "" {
			return errProtocolJSON
		}
		return deliver(event)
	}, native...)
	if err == nil {
		err = errors.New("OpenCode event stream disconnected before the turn completed.")
	}
	select {
	case failed <- err:
	case <-ctx.Done():
	}
}

func (p *adapter) startOpenCodeServer(b binary, cwd string, ctx context.Context) (_ *openCodeServer, err error) {
	release, admissionErr := b.use()
	if admissionErr != nil {
		return nil, admissionErr
	}
	retained := false
	defer func() {
		if !retained {
			release()
		}
	}()
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, errors.New("Could not generate OpenCode server credentials.")
	}
	h := &openCodeHTTP{directory: cwd, password: hex.EncodeToString(secret[:]), v1: b.openCodeV1}
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
	args := append(append([]string{}, b.args...), "serve", "--hostname", "127.0.0.1", "--port", "0")
	if h.v1 {
		args = append(args, "--mdns=false")
	}
	cmd := exec.Command(b.path, args...)
	cmd.Dir = cwd
	for _, entry := range runtimeEnvironment(os.Environ(), b.node) {
		key, _, _ := strings.Cut(entry, "=")
		if len(p.openCodeLibraryEnv) > 0 && (strings.HasPrefix(strings.ToUpper(key), "OPENCODE_") || strings.EqualFold(key, "XDG_CONFIG_HOME")) {
			continue
		}
		if strings.EqualFold(key, "OPENCODE_CONFIG_CONTENT") {
			continue
		}
		if !strings.EqualFold(key, "OPENCODE_PASSWORD") && !strings.EqualFold(key, "OPENCODE_SERVER_PASSWORD") && !strings.EqualFold(key, "OPENCODE_SERVER_USERNAME") &&
			!strings.EqualFold(key, "OPENCODE_ENABLE_QUESTION_TOOL") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, openCodeServerEnv(h.v1, h.password)...)
	cmd.Env = append(cmd.Env, "OPENCODE_CONFIG_CONTENT="+graphConfig)
	cmd.Env = append(cmd.Env, p.openCodeLibraryEnv...)
	cmd.Env = skillExecutionEnv(cmd.Env)
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
			if address, ok := strings.CutPrefix(line, openCodeServerPrefix(h.v1)); ok {
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
		return fail(errors.New("Could not start OpenCode. Check the executable in Settings > OpenCode and restart the engine."))
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
	server := &openCodeServer{h: h, transport: transport, owner: owner, processDone: processDone, outputFailed: outputFailed, cleanup: func() { cleanup(); release() }}
	retained = true
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
	if err := h.checkVersion(ctx); err != nil {
		return failed(err)
	}
	if p.capabilities != nil && h.v1 {
		if err := p.verifyOpenCodeLibrary(h); err != nil {
			return failed(err)
		}
		if !p.openCodeLibraryReady.Load() {
			return failed(errors.New("Owned OpenCode Library policy did not complete its handshake."))
		}
	}
	var bridgeStatus map[string]any
	if h.v1 {
		if err := h.json(ctx, http.MethodPost, "/mcp", map[string]any{"name": "klm_linked", "config": map[string]any{"type": "remote", "url": p.bridge.url, "headers": map[string]string{"Authorization": "Bearer " + p.bridge.token}, "oauth": false, "enabled": true, "timeout": linkedToolClientTimeout.Milliseconds()}}, &bridgeStatus); err != nil {
			return failed(errors.New("Could not configure the OpenCode linked-agent bridge."))
		}
		if str(object(bridgeStatus["klm_linked"]), "status") != "connected" {
			return failed(errors.New("OpenCode did not connect to the linked-agent bridge."))
		}
	} else {
		bridgeStatus, err = h.addV2Gateway(ctx, "klm_linked", p.bridge.url, p.bridge.token, linkedToolClientTimeout.Milliseconds())
		if err != nil {
			return failed(err)
		}
	}
	if p.capabilities != nil {
		if h.v1 {
			if err := h.json(ctx, http.MethodPost, "/mcp", map[string]any{"name": "klm_library", "config": map[string]any{"type": "remote", "url": p.bridge.libraryURL, "headers": map[string]string{"Authorization": "Bearer " + p.bridge.libraryToken}, "oauth": false, "enabled": true, "timeout": 330000}}, &bridgeStatus); err != nil {
				return failed(err)
			}
		} else {
			bridgeStatus, err = h.addV2Gateway(ctx, "klm_library", p.bridge.libraryURL, p.bridge.libraryToken, 330000)
			if err != nil {
				return failed(err)
			}
		}
		if len(bridgeStatus) != 2 || str(object(bridgeStatus["klm_library"]), "status") != "connected" {
			return failed(errors.New("OpenCode gateway-only Library connection was not confirmed."))
		}
	}
	server.mcpPrefixes = openCodeMCPToolPrefixes(bridgeStatus)
	if err := p.bridge.waitReady(ctx); err != nil {
		return failed(err)
	}
	if p.graphNode() {
		if err := p.waitGraphPlugin(ctx); err != nil {
			return failed(err)
		}
	}
	var ids []string
	if h.v1 {
		if err := h.json(ctx, http.MethodGet, "/experimental/tool/ids", nil, &ids); err != nil {
			return failed(err)
		}
		if !p.openCodePolicyReady.Load() {
			return failed(errors.New("OpenCode V1 did not activate the owned KLM permission plugin."))
		}
	} else {
		if err := h.waitPolicyReady(ctx); err != nil {
			return failed(err)
		}
		var inventory struct {
			Output struct {
				Tools []string `json:"tools"`
			} `json:"output"`
		}
		if err := h.json(ctx, http.MethodPost, "/api/rpc/klm.policy/inventory", map[string]any{"input": map[string]any{}}, &inventory); err != nil {
			log.Printf("OpenCode permission inventory failed: %v", err)
			return failed(fmt.Errorf("OpenCode did not activate the owned KLM permission plugin: %w", err))
		}
		ids = inventory.Output.Tools
		if p.capabilities != nil {
			if err := p.verifyOpenCodeV2Library(h); err != nil {
				return failed(err)
			}
			if err := p.verifyOpenCodeV2LibraryTools(ids); err != nil {
				return failed(err)
			}
		}
	}
	if len(ids) == 0 {
		return failed(errors.New("OpenCode did not identify its native tool inventory."))
	}
	if p.graphNode() {
		p.setOpenCodeGraphTools(ids, bridgeStatus)
	}
	return server, nil
}

func validateOpenCodeDirectory(actual, cwd string) error {
	wanted, err := filepath.Abs(cwd)
	if err != nil {
		return errors.New("Could not resolve the OpenCode working directory.")
	}
	if actual == "" || !filepath.IsAbs(actual) {
		return errors.New("OpenCode returned no absolute working directory.")
	}
	if !piSamePath(wanted, actual) {
		return fmt.Errorf("Working directory mismatch: expected %q, received %q. Do not move the KLM-managed native session; access worktrees with absolute paths and an explicit tool workdir when supported.", filepath.Clean(wanted), filepath.Clean(actual))
	}
	return nil
}

func (p *adapter) runOpenCode(b binary, cwd string, payload submission) (err error) {
	p.openCodeV1 = b.openCodeV1
	p.app.mu.Lock()
	var s Session
	var native nativeSession
	if p.app.store != nil {
		var readErr error
		s, native, _, readErr = p.app.store.readTurnSession(p.turn.ctx, p.id)
		if readErr != nil {
			p.app.mu.Unlock()
			return readErr
		}
	} else {
		s = *p.app.state.session(p.id)
		native = p.app.state.Native[p.id]
	}
	p.app.mu.Unlock()
	version := "v2"
	if b.openCodeV1 {
		version = "v1"
	}
	storedVersion := native.OpenCodeVersion
	if storedVersion == "" {
		storedVersion = "v2"
	}
	if native.ID != "" && storedVersion != version {
		return fmt.Errorf("This conversation uses OpenCode %s. Select that version in Settings and restart the engine, or create a new conversation for %s.", storedVersion, version)
	}
	t := p.turn
	ctx, cancel := context.WithCancel(t.ctx)
	defer cancel()
	p.completed = false

	server, reused := (*openCodeServer)(nil), false
	toolKey := p.runtimeToolKey() + "/" + version
	if p.runtime != nil && p.capabilities == nil {
		server, reused = p.runtime.getOpenCode(toolKey)
	}
	if !reused {
		server, err = p.startOpenCodeServer(b, cwd, ctx)
		if err != nil {
			return err
		}
		if p.runtime != nil {
			if err := p.runtime.retainOpenCode(server, toolKey, p.capabilities != nil); err != nil {
				server.Close()
				return err
			}
		}
	}
	h, owner := server.h, server.owner
	h.scope = p.turn.protocolScope
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
			abortSettled = h.json(abortCtx, http.MethodPost, "/api/session/"+url.PathEscape(sessionID)+"/interrupt", nil, nil) == nil
			abortCancel()
		}
		if p.runtime != nil && p.capabilities != nil {
			// A successful ordinary turn keeps its real idle runtime visible.
			// beginSessionRuntime retires this managed profile before the next turn;
			// Stop during execution and the normal idle timeout still dispose it.
			if err != nil || p.failed || !p.completed || t.ctx.Err() != nil || !server.Alive() {
				shutdownErr := p.runtime.shutdown()
				p.processesDrained = shutdownErr == nil
				err = errors.Join(err, shutdownErr)
			}
		} else if p.runtime == nil {
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
		if err := h.json(ctx, http.MethodGet, "/api/session/"+url.PathEscape(native.ID), nil, &session); err != nil {
			return fmt.Errorf("Could not resume the stored OpenCode session: %w", err)
		}
		if str(session, "id") != native.ID {
			return errors.New("OpenCode returned a different native session than requested.")
		}
	} else if err := h.json(ctx, http.MethodPost, "/api/session", map[string]any{"title": s.Title, "location": map[string]any{"directory": cwd}}, &session); err != nil {
		return err
	}
	wanted, err := filepath.Abs(cwd)
	if err != nil {
		return errors.New("Could not resolve the OpenCode working directory.")
	}
	actual := str(object(session["location"]), "directory")
	if h.v1 {
		actual = str(session, "directory")
	}
	if err := validateOpenCodeDirectory(actual, wanted); err != nil {
		kind := "New"
		if native.ID != "" {
			kind = "Stored"
		}
		return fmt.Errorf("%s OpenCode session: %w", kind, err)
	}
	sessionID = str(session, "id")
	if sessionID == "" {
		return errors.New("OpenCode returned a session without an identifier.")
	}
	if err := p.nativeID(sessionID); err != nil {
		return err
	}
	p.setGraphNativeSession(sessionID)
	p.analyticsBindNative(sessionID, "")
	sessionPath := "/api/session/" + url.PathEscape(sessionID)
	baseline := newProtocolIDSet(ctx, p.app.dir)
	defer baseline.Close()
	usage := &openCodeUsage{messages: map[string]map[string]any{}, steps: map[string]map[string]map[string]any{}, windows: map[string]*int64{}}
	var models []map[string]any
	// Model limits are optional telemetry; a failed lookup must not block execution.
	metricsCtx, metricsCancel := context.WithTimeout(ctx, 3*time.Second)
	if h.json(metricsCtx, http.MethodGet, "/api/model", nil, &models) == nil {
		for _, model := range models {
			usage.windows[str(model, "providerID")+"/"+str(model, "id")] = tokenCount(object(model["limit"])["context"])
		}
	}
	metricsCancel()
	if err := h.walkMessages(ctx, sessionID, nil, func(message map[string]any) error {
		info := object(message["info"])
		one := &openCodeUsage{messages: map[string]map[string]any{}, steps: map[string]map[string]map[string]any{}, windows: usage.windows}
		one.message(info)
		if err := protocolArrayEach(message["parts"], func(value any) error { one.part(object(value)); return nil }); err != nil {
			return err
		}
		usage.addBaseline(info, one.snapshot())
		if err := baseline.Add(str(info, "id")); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	initialUsage := usage.snapshot()
	if initialUsage == nil {
		zero := int64(0)
		initialUsage = &SessionUsage{InputTokens: &zero, OutputTokens: &zero}
	}
	if err := p.setUsageBaseline(initialUsage); err != nil {
		return err
	}

	stream, err := h.request(ctx, http.MethodGet, "/api/event", nil)
	if err != nil {
		return err
	}
	defer stream.Body.Close()
	if !strings.HasPrefix(strings.ToLower(stream.Header.Get("Content-Type")), "text/event-stream") {
		return errors.New("OpenCode did not provide an event stream.")
	}
	events := make(chan map[string]any)
	streamFailed := make(chan error, 1)
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		openCodeVersionEvents(ctx, stream.Body, p.app.dir, events, streamFailed, h.v1, p.turn.protocolScope)
	}()
	defer func() {
		cancel()
		_ = stream.Body.Close()
		<-streamDone // Release staged files before the turn/data-directory owner exits.
	}()

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
			err := h.json(ctx, http.MethodPost, "/api/session/"+url.PathEscape(str(properties, "sessionID"))+"/permission/"+url.PathEscape(id)+"/reply", map[string]any{"decision": reply}, nil)
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
		if command == "" && (kind == "bash" || kind == "shell" || kind == "command") {
			command = strings.Join(patterns, "\n")
		}
		req := Permission{
			analyticsOrigin: analyticsHash(p.id, p.harness, str(properties, "sessionID"), ""),
			mcpTool:         p.isOpenCodeMCPTool(kind),
			ID:              newID(), Harness: p.harness, Kind: kind, Title: "Allow " + kind,
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
	if err := h.json(ctx, http.MethodGet, "/api/permission/request", nil, &pending); err != nil {
		return err
	}
	for _, req := range pending {
		if !h.v1 {
			req = openCodeV2Permission(req)
		}
		if err := requestPermission(req); err != nil {
			return err
		}
	}
	pending = nil
	if err := h.json(ctx, http.MethodGet, "/api/form", nil, &pending); err != nil {
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
	putPart := func(target *adapter, nativeSessionID string, targetBaseline any, targetInfos map[string]map[string]any, part map[string]any, discoverSubagent bool) error {
		if p.turn.protocolScope != nil {
			release, err := acquireProtocolStage(ctx, protocolNormalizeSlots)
			if err != nil {
				return err
			}
			defer release()
		}
		if truth(part, "$klmPersisted") {
			return nil
		}
		messageID, partID := str(part, "messageID"), str(part, "id")
		old, err := protocolIDSeen(targetBaseline, messageID)
		if err != nil {
			return err
		}
		if old || str(targetInfos[messageID], "role") != "assistant" {
			return nil
		}
		if messageID == "" || partID == "" || str(part, "sessionID") != nativeSessionID {
			return errors.New("OpenCode returned an invalid message part.")
		}
		kind, title, status := str(part, "type"), "", "running"
		body := displayNativeText(part["text"])
		key := "opencode/" + messageID + "/" + partID
		var taskChild *openCodeSubagent
		raw := map[string]any{"type": kind, "partID": partID, "messageID": messageID}
		switch kind {
		case "text", "reasoning":
			if kind == "text" {
				kind = "assistant"
			} else if text, plain := body.(string); plain && strings.TrimSpace(text) == "" {
				return nil
			}
			if object(part["time"])["end"] != nil || object(targetInfos[messageID]["time"])["completed"] != nil {
				status = "completed"
			}
			// Reasoning metadata can contain encrypted provider state; retain text only.
		case "tool":
			state := object(part["state"])
			title, body, status = str(part, "tool"), displayNativeText(state["output"]), str(state, "status")
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
			if target == p && toolName == delegationToolName && (status == "completed" || status == "error") && p.delegation != nil {
				if err := p.delegation.nativeResultSeen(str(part, "callID"), ""); err != nil {
					return err
				}
			}
			if discoverSubagent && (strings.EqualFold(toolName, "subagent") || h.v1 && strings.EqualFold(toolName, "task")) {
				kind = "subagent"
				input, metadata := object(state["input"]), object(state["metadata"])
				if description := str(input, "description"); description != "" {
					title = description
				} else {
					title = "Subagent"
				}
				nativeChildID := str(metadata, "sessionID")
				if h.v1 {
					nativeChildID = str(metadata, "sessionId")
				}
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
						taskChild = children[nativeChildID]
					}
				}
			} else if title == "shell" || h.v1 && title == "bash" {
				kind = "command"
				command, err := nativeField(state["input"], "command")
				if err != nil {
					return err
				}
				if nativeStringPresent(command) {
					body = trimNativeNewline(joinNativeValues(command, "\n", body))
				}
			} else if strings.HasPrefix(title, "mcp_") || strings.HasPrefix(title, "mcp.") || strings.Contains(title, "__") {
				kind = "mcp"
			}
			// Opaque tool names stay intact; do not guess that every custom tool is MCP.
			raw["tool"], raw["callID"], raw["state"] = toolName, part["callID"], state
			if label, metadata, ok := p.libraryToolPresentation(toolName); ok {
				kind, title = "mcp", label
				for key, value := range metadata {
					raw[key] = value
				}
			}
		default:
			return nil
		}
		if err := target.put(key, kind, title, body, status, false, raw); err != nil {
			return err
		}
		if status == "completed" || status == "error" {
			ownedParts := parts
			if target != p {
				ownedParts = nil
				if child := children[nativeSessionID]; child != nil {
					ownedParts = child.parts
				}
			}
			if ownedParts != nil {
				compact := map[string]any{"id": partID, "messageID": messageID, "sessionID": nativeSessionID, "type": part["type"], "$klmPersisted": true, "state": map[string]any{"status": status}}
				if err := cacheNativeMap(p.turn.protocolScope, "opencode-part/"+target.id, ownedParts, messageID+"/"+partID, compact); err != nil {
					return err
				}
			}
		}
		if taskChild != nil {
			taskStatus := status
			// V2 can complete the tool response while its child keeps running.
			if status == "completed" && str(object(object(part["state"])["metadata"]), "status") == "running" {
				taskStatus = "running"
			}
			return taskChild.updateTask(p, key, taskStatus)
		}
		return nil
	}

	putTextDelta := func(target *adapter, nativeID string, targetBaseline any, targetInfos map[string]map[string]any, part map[string]any, delta any, discover bool) error {
		if truth(part, "$klmPersisted") {
			return errors.New("OpenCode emitted a text delta after native part completion.")
		}
		if _, ok := delta.(string); !ok {
			content, ok := delta.(*protocolContent)
			if !ok || content.kind != '"' {
				return errProtocolJSON
			}
		}
		accumulated, err := appendNativeText(ctx, p.turn.protocolScope, part["text"], delta)
		if err != nil {
			return err
		}
		part["text"] = accumulated
		if err := p.turn.protocolScope.cacheValue("opencode-part/"+target.id+"/"+str(part, "messageID")+"/"+str(part, "id"), part); err != nil {
			return err
		}
		if source, ok := accumulated.(*protocolAccumulatedText); ok {
			p.turn.protocolScope.ack(source.frame)
		}
		messageID, partID := str(part, "messageID"), str(part, "id")
		old, err := protocolIDSeen(targetBaseline, messageID)
		if err != nil {
			return err
		}
		if old || str(targetInfos[messageID], "role") != "assistant" {
			return nil
		}
		key := "opencode/" + messageID + "/" + partID
		if target.keys[key] == "" {
			return putPart(target, nativeID, targetBaseline, targetInfos, part, discover)
		}
		kind := str(part, "type")
		if kind == "text" {
			kind = "assistant"
		}
		return target.put(key, kind, "", displayNativeText(delta), "running", true, map[string]any{"type": part["type"], "partID": partID, "messageID": messageID})
	}

	prompt := h.prompt(payload, p.model, p.effort)
	if p.delegation != nil || p.turn.delegationChild != nil {
		prompt["tools"] = map[string]bool{"task": false}
	}
	if p.capabilities != nil {
		if h.v1 {
			prompt["agent"] = p.openCodeManagedAgent()
		} else if err := h.json(ctx, http.MethodPost, sessionPath+"/agent", map[string]any{"agent": p.openCodeManagedAgent()}, nil); err != nil {
			return err
		}
		if err := p.markCapabilitiesApplied(); err != nil {
			return err
		}
	}
	if !h.v1 {
		model := p.model
		defaultVariant := ""
		if model == "" {
			var selected map[string]any
			selected, err = h.v2DefaultModel(ctx)
			if err != nil {
				return err
			}
			model = str(selected, "providerID") + "/" + str(selected, "id")
			defaultVariant = str(selected, "variant")
		}
		provider, modelID, ok := strings.Cut(model, "/")
		if !ok || provider == "" || modelID == "" {
			return errors.New("OpenCode model must use provider/model format.")
		}
		selection := map[string]any{"providerID": provider, "id": modelID}
		if p.effort != "" {
			selection["variant"] = p.effort
		}
		if p.effort == "" && defaultVariant != "" {
			selection["variant"] = defaultVariant
		}
		if err := h.json(ctx, http.MethodPost, sessionPath+"/model", map[string]any{"model": selection}, nil); err != nil {
			return err
		}
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
	dispatchedAt, dispatchErr := p.analyticsDispatch()
	if dispatchErr != nil {
		return dispatchErr
	}
	p.delegationPromptAttempted()
	if err := h.json(ctx, http.MethodPost, sessionPath+"/prompt", prompt, nil); err != nil {
		return err
	}
	if err := p.delegationNativeStarted(""); err != nil {
		return err
	}
	if err := p.analyticsStartAt(dispatchedAt); err != nil {
		return err
	}
	// Events queued during prompt_async are processed only after submission succeeds.
	busy, ownedMessage := false, false
	steering := p.steeringChannel()
	steeringMessageID, steeringQueueID := "", ""
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
		if !h.v1 && !steeringHTTPPending {
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
			steeringHTTPPending = true
			steeringTimer.Reset(15 * time.Second)
			steeringDeadline = steeringTimer.C
			nextPrompt := h.prompt(payload, p.model, p.effort)
			if h.v1 {
				nextPrompt["messageID"] = steeringMessageID
			} else {
				nextPrompt["id"] = steeringMessageID
			}
			go func() {
				sendCtx, sendCancel := context.WithTimeout(ctx, 15*time.Second)
				defer sendCancel()
				steeringResult <- h.json(sendCtx, http.MethodPost, sessionPath+"/prompt", nextPrompt, nil)
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
			if err := h.json(ctx, http.MethodPost, sessionPath+"/interrupt", nil, nil); err != nil {
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
				case "session.idle":
					if err := h.walkMessages(ctx, scopedID, nil, func(message map[string]any) error {
						info := object(message["info"])
						child.infos[str(info, "id")] = info
						child.usage.message(info)
						if err := child.adapter.openCodeStepError(info, false); err != nil {
							return err
						}
						for _, value := range message["parts"].([]any) {
							if err := putPart(child.adapter, scopedID, nil, child.infos, object(value), false); err != nil {
								return err
							}
						}
						return nil
					}); err != nil {
						return err
					}
					status := "completed"
					if str(properties, "outcome") == "session.execution.interrupted" {
						status = "cancelled"
					}
					if err := p.finishSubagent(child.adapter, status); err != nil {
						return err
					}
				case "message.updated":
					info := object(properties["info"])
					id := str(info, "id")
					if id == "" {
						return errors.New("OpenCode returned a subagent message without an identifier.")
					}
					info = compactOpenCodeInfo(info)
					if err := cacheNativeMap(p.turn.protocolScope, "opencode-info/"+child.adapter.id, child.infos, id, info); err != nil {
						return err
					}
					child.usage.message(info)
					if str(info, "role") == "assistant" {
						if child.taskKey != "" {
							if err := p.analyticsChildInvocation(child.adapter, child.taskKey); err != nil {
								return err
							}
						}
						if err := child.adapter.resolvedSelection(str(info, "providerID")+"/"+str(info, "modelID"), str(info, "variant")); err != nil {
							return err
						}
					}
					if err := child.adapter.openCodeStepError(info, false); err != nil {
						return err
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
					if err := cacheNativeMap(p.turn.protocolScope, "opencode-part/"+child.adapter.id, child.parts, messageID+"/"+partID, part); err != nil {
						return err
					}
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
					if err := putTextDelta(child.adapter, scopedID, nil, child.infos, part, properties["delta"], false); err != nil {
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
				if created := tokenCount(object(usage.baselineLatest["time"])["created"]); created != nil && *created > usage.compactedAt {
					usage.compactedAt = *created
				}
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
				old, err := baseline.Has(id)
				if err != nil {
					return err
				}
				if id == "" || old {
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
				info = compactOpenCodeInfo(info)
				if err := cacheNativeMap(p.turn.protocolScope, "opencode-info/"+p.id, infos, id, info); err != nil {
					return err
				}
				usage.message(info)
				if str(info, "role") == "assistant" {
					if err := p.resolvedSelection(str(info, "providerID")+"/"+str(info, "modelID"), str(info, "variant")); err != nil {
						return err
					}
				}
				if err := p.setUsage(usage.snapshot()); err != nil {
					return err
				}
				if err := p.openCodeStepError(info, choiceInterrupted); err != nil {
					return err
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
				old, err := baseline.Has(messageID)
				if err != nil {
					return err
				}
				if old {
					continue
				}
				if messageID == "" || partID == "" {
					return errors.New("OpenCode returned a message part without identifiers.")
				}
				if err := cacheNativeMap(p.turn.protocolScope, "opencode-part/"+p.id, parts, messageID+"/"+partID, part); err != nil {
					return err
				}
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
				if err := putTextDelta(p, sessionID, baseline, infos, part, properties["delta"], true); err != nil {
					return err
				}
			case "session.status", "session.idle":
				if str(properties, "outcome") == "session.execution.interrupted" && !choiceInterrupted {
					return errors.New("OpenCode execution was interrupted before the turn completed.")
				}
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
				idleObservedAt := time.Now()
				p.analyticsIdleCandidate(idleObservedAt)
				// Retain only this turn, then reconcile oldest first for UI ordering.
				var history []map[string]any
				if err := h.walkMessages(ctx, sessionID, baseline, func(message map[string]any) error {
					if str(object(message["info"]), "role") != "assistant" {
						return nil
					}
					if h.scope != nil {
						message = map[string]any{"info": compactOpenCodeInfo(object(message["info"]))}
					}
					if err := p.turn.protocolScope.cacheValue("opencode-history/"+str(object(message["info"]), "id"), message); err != nil {
						return err
					}
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
				nativeFailure := false
				err := p.withFinalBatch(func() error {
					var release func()
					defer func() {
						if release != nil {
							release()
						}
					}()
					for i := len(history) - 1; i >= 0; i-- {
						if release != nil {
							release()
							release = nil
						}
						message := history[i]
						history[i] = nil
						if h.scope != nil {
							var readErr error
							message, release, readErr = h.nativeMessage(ctx, sessionID, str(object(message["info"]), "id"))
							if readErr != nil {
								return readErr
							}
						}
						info := object(message["info"])
						id := str(info, "id")
						if id == "" || str(info, "role") != "assistant" {
							continue
						}
						if str(info, "sessionID") != sessionID {
							return errors.New("OpenCode history contained a different native session.")
						}
						info = compactOpenCodeInfo(info)
						if err := cacheNativeMap(p.turn.protocolScope, "opencode-info/"+p.id, infos, id, info); err != nil {
							return err
						}
						usage.message(info)
						if err := p.openCodeStepError(info, choiceInterrupted); err != nil {
							return err
						}
						completed := object(info["time"])["completed"] != nil
						if !completed {
							incomplete = true
						}
						created, _ := object(info["time"])["created"].(float64)
						if number, ok := object(info["time"])["created"].(json.Number); ok {
							created, _ = number.Float64()
						}
						if created > latestCreated || (created == latestCreated && id > latestID) {
							latestCreated, latestID = created, id
							finish := str(info, "finish")
							terminalAssistant = completed && finish != "" && finish != "tool-calls" && finish != "unknown"
							if choiceInterrupted && completed && str(object(info["error"]), "name") == "MessageAbortedError" {
								terminalAssistant = true
							}
						}
						if err := protocolArrayEach(message["parts"], func(value any) error {
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
							return nil
						}); err != nil {
							return err
						}
						if err := p.turn.protocolScope.cacheValue("opencode-history/"+id, nil); err != nil {
							return err
						}
					}
					// Earlier failed steps can precede a successful native continuation.
					var err error
					nativeFailure, err = p.openCodeFinalError(infos[latestID], choiceInterrupted)
					return err
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
				if choiceInterrupted && str(properties, "outcome") == "session.execution.interrupted" {
					terminalAssistant = true
				}
				if incomplete || !terminalAssistant {
					return errors.New("OpenCode became idle without a completed assistant response.")
				}
				if err := p.setUsageFinal(usage.snapshot()); err != nil {
					return err
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
				if err := p.analyticsEndAt(idleObservedAt); err != nil {
					return err
				}
				if !p.graphMCPPending() {
					return nil
				}
			}
		}
	}
}

// A failed model step can be followed by OpenCode's own retry or continuation.
// Only execution terminal events (or final reconciliation) fail the KLM turn.
func (p *adapter) openCodeStepError(info map[string]any, choiceInterrupted bool) error {
	if str(info, "role") != "assistant" || info["error"] == nil || (choiceInterrupted && str(object(info["error"]), "name") == "MessageAbortedError") {
		return nil
	}
	text := humanError(map[string]any{"error": info["error"]})
	if text == "" {
		text = "OpenCode step failed."
	}
	return p.put("opencode/step-error/"+str(info, "id"), "status", "OpenCode step failed", text, "error", false, map[string]any{"type": "session.step.failed", "messageID": info["id"]})
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
	if h.v1 {
		return p.requestOpenCodeV1Question(ctx, h, properties, questions)
	}
	id := str(properties, "id")
	if strings.TrimSpace(id) == "" {
		return errors.New("OpenCode returned a question without an identifier.")
	}
	if questions[id] != nil {
		return nil
	}
	if p.graphSealed() {
		return h.json(ctx, http.MethodDelete, "/api/session/"+url.PathEscape(str(properties, "sessionID"))+"/form/"+url.PathEscape(id), nil, nil)
	}
	encoded, err := json.Marshal(properties["fields"])
	if err != nil || len(encoded) > openCodeFrameLimit {
		return errors.New("OpenCode question request exceeded the 2 MiB limit.")
	}
	var fields []map[string]any
	if json.Unmarshal(encoded, &fields) != nil || len(fields) == 0 {
		return errors.New("OpenCode returned an invalid question request.")
	}
	req := QuestionRequest{SourceID: id, analyticsOrigin: analyticsHash(p.id, p.harness, str(properties, "sessionID"), "")}
	var visible []map[string]any
	for _, field := range fields {
		if truth(field, "hidden") {
			continue
		}
		kind := str(field, "type")
		if kind == "external" {
			return errors.New("OpenCode requested an external form. Open it in the OpenCode client.")
		}
		item := QuestionItem{ID: str(field, "key"), Header: str(properties, "title"), Text: str(field, "title"), Description: str(field, "description"), Multiple: kind == "multiselect", Custom: true}
		if item.Text == "" {
			item.Text = str(field, "description")
			item.Description = ""
		}
		if item.Text == "" {
			item.Text = item.ID
		}
		options, _ := field["options"].([]any)
		for _, value := range options {
			option := object(value)
			item.Options = append(item.Options, QuestionOption{Label: str(option, "label"), Description: str(option, "description")})
		}
		if len(options) > 0 {
			item.Custom = truth(field, "custom")
		}
		if kind == "boolean" {
			item.Options = []QuestionOption{{Label: "True"}, {Label: "False"}}
			item.Custom = false
		}
		visible = append(visible, field)
		req.Items = append(req.Items, item)
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
		path := "/api/session/" + url.PathEscape(str(properties, "sessionID")) + "/form/" + url.PathEscape(id)
		var body any
		method := http.MethodPost
		if cancelled || p.graphSealed() {
			method = http.MethodDelete
		} else {
			path += "/reply"
			if len(answers) != len(visible) {
				return errors.New("Answer every OpenCode form field.")
			}
			answer := map[string]any{}
			for _, field := range fields {
				if truth(field, "hidden") && field["default"] != nil {
					answer[str(field, "key")] = field["default"]
				}
			}
			for i, field := range visible {
				values := append([]string(nil), answers[i]...)
				options, _ := field["options"].([]any)
				for j, value := range values {
					for _, raw := range options {
						option := object(raw)
						if value == str(option, "label") {
							values[j] = str(option, "value")
							break
						}
					}
				}
				key, kind := str(field, "key"), str(field, "type")
				if kind == "multiselect" {
					answer[key] = values
					continue
				}
				if len(values) != 1 {
					return errors.New("Select one answer per OpenCode form field.")
				}
				switch kind {
				case "boolean":
					answer[key] = values[0] == "True"
				case "integer":
					value, err := strconv.ParseInt(values[0], 10, 64)
					if err != nil {
						return errors.New("Enter a valid integer.")
					}
					answer[key] = value
				case "number":
					value, err := strconv.ParseFloat(values[0], 64)
					if err != nil {
						return errors.New("Enter a valid number.")
					}
					answer[key] = value
				default:
					answer[key] = values[0]
				}
			}
			body = map[string]any{"answer": answer}
		}
		if err := h.json(ctx, method, path, body, nil); err != nil {
			return err
		}
		live.Store(false)
		return nil
	})
}
