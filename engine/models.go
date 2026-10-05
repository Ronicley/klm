package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ModelOption struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Provider      string   `json:"provider"`
	ProviderName  string   `json:"providerName"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
	endpoint      string
	customAuth    bool
	api           string
}

type ConnectedProvider struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	AuthType string `json:"authType"`
}

type ModelCatalog struct {
	Models             []ModelOption       `json:"models"`
	DefaultModel       string              `json:"defaultModel,omitempty"`
	DefaultEffort      string              `json:"defaultEffort,omitempty"`
	EffortLabel        string              `json:"effortLabel"`
	ConnectedProviders []ConnectedProvider `json:"connectedProviders"`
	// Effective provider options remain private; never publish credentials/config.
	providerOptions map[string]map[string]any
}

type catalogCache struct {
	catalog *ModelCatalog
	expires time.Time
}

type catalogFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	catalog *ModelCatalog
	err     error
}

func (c *ModelCatalog) find(id string) *ModelOption {
	for i := range c.Models {
		if c.Models[i].ID == id {
			return &c.Models[i]
		}
	}
	return nil
}

func (a *app) catalog(ctx context.Context, s Session, cwd string, refresh bool) (*ModelCatalog, error) {
	a.catalogMu.Lock()
	key := s.ProjectID + "/" + s.Harness
	if entry, ok := a.catalogs[key]; ok && !refresh && time.Now().Before(entry.expires) {
		a.catalogMu.Unlock()
		return entry.catalog, nil
	}
	flight := a.catalogFlights[key]
	if flight == nil {
		background := a.ctx
		if background == nil {
			background = context.Background()
		}
		flightCtx, cancel := context.WithTimeout(background, 45*time.Second)
		flight = &catalogFlight{done: make(chan struct{}), cancel: cancel}
		if a.catalogFlights == nil {
			a.catalogFlights = map[string]*catalogFlight{}
		}
		a.catalogFlights[key] = flight
		go func() {
			catalog, err := a.discoverCatalog(flightCtx, s, cwd)
			a.catalogMu.Lock()
			flight.catalog, flight.err = catalog, err
			if err == nil && a.catalogFlights[key] == flight {
				if a.catalogs == nil {
					a.catalogs = map[string]catalogCache{}
				}
				a.catalogs[key] = catalogCache{catalog, time.Now().Add(2 * time.Minute)}
			}
			if a.catalogFlights[key] == flight {
				delete(a.catalogFlights, key)
			}
			close(flight.done)
			a.catalogMu.Unlock()
			cancel()
		}()
	}
	flight.waiters++
	a.catalogMu.Unlock()
	select {
	case <-ctx.Done():
		a.catalogMu.Lock()
		flight.waiters--
		if flight.waiters == 0 {
			if a.catalogFlights[key] == flight {
				delete(a.catalogFlights, key)
			}
			flight.cancel()
		}
		a.catalogMu.Unlock()
		return nil, ctx.Err()
	case <-flight.done:
		return flight.catalog, flight.err
	}
}

func (a *app) discoverCatalog(ctx context.Context, s Session, cwd string) (*ModelCatalog, error) {
	a.mu.Lock()
	b, installed := a.binaries[s.Harness]
	a.mu.Unlock()
	if !installed {
		return nil, errors.New("Harness is not installed.")
	}
	var catalog *ModelCatalog
	var err error
	switch s.Harness {
	case "pi":
		catalog, err = piCatalog(ctx, b, cwd)
	case "opencode":
		catalog, err = openCodeCatalog(ctx, b, cwd)
	case "codex":
		catalog, err = codexCatalog(ctx, b, cwd)
	default:
		err = errors.New("Unsupported harness.")
	}
	if err != nil {
		return nil, err
	}
	// The connected-provider list is the boundary, not model vendor/name prefixes.
	connected := map[string]bool{}
	for _, provider := range catalog.ConnectedProviders {
		connected[provider.ID] = true
	}
	catalog.Models = slices.DeleteFunc(catalog.Models, func(model ModelOption) bool { return !connected[model.Provider] })
	if catalog.find(catalog.DefaultModel) == nil {
		catalog.DefaultModel = ""
		catalog.DefaultEffort = ""
	}
	if catalog.ConnectedProviders == nil {
		catalog.ConnectedProviders = []ConnectedProvider{}
	}
	sort.Slice(catalog.Models, func(i, j int) bool {
		l, r := catalog.Models[i], catalog.Models[j]
		if l.Provider != r.Provider {
			return l.Provider < r.Provider
		}
		return l.Name < r.Name
	})
	return catalog, nil
}

func (a *app) sessionForRead(w http.ResponseWriter, r *http.Request) (Session, string, bool) {
	a.mu.Lock()
	s := a.state.session(r.PathValue("id"))
	if s == nil {
		a.mu.Unlock()
		fail(w, 404, "Session not found.")
		return Session{}, "", false
	}
	view := *s
	folder, available := a.state.conversationDirectory(s)
	if !available {
		a.mu.Unlock()
		fail(w, 404, "Conversation directory unavailable.")
		return Session{}, "", false
	}
	a.mu.Unlock()
	return view, folder, true
}

func (a *app) getModels(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(55 * time.Second))
	s, cwd, ok := a.sessionForRead(w, r)
	if !ok {
		return
	}
	catalog, err := a.catalog(r.Context(), s, cwd, r.URL.Query().Get("refresh") == "true")
	if err != nil {
		fail(w, 502, "Could not load harness models. Check the harness configuration and retry.")
		return
	}
	respond(w, 200, catalog)
}

// Read-only control sessions never submit a prompt, execute tools, or create a thread.
type catalogRPC struct {
	process *interactiveProcess
	ctx     context.Context
	seq     int
	pi      bool
}

func newCatalogRPC(ctx context.Context, b binary, cwd string, pi bool) (*catalogRPC, error) {
	args := []string{"app-server", "--listen", "stdio://"}
	if pi {
		args = []string{"--mode", "rpc", "--no-extensions", "--no-session"}
	}
	t := &turn{ctx: ctx}
	process, err := startInteractive(t, b, args, cwd, nil)
	if err != nil {
		return nil, err
	}
	rpc := &catalogRPC{process: process, ctx: ctx, pi: pi}
	if !pi {
		_, err = rpc.call("initialize", map[string]any{"clientInfo": map[string]any{"name": "klm-catalog", "version": "0.1.0"}})
		if err == nil {
			err = process.Send(map[string]any{"method": "initialized"})
		}
		if err != nil {
			process.Close()
			return nil, err
		}
	}
	return rpc, nil
}

func (p *catalogRPC) call(method string, params map[string]any) (map[string]any, error) {
	p.seq++
	id := "catalog-" + strconv.Itoa(p.seq)
	request := map[string]any{"id": id, "method": method, "params": params}
	if p.pi {
		request = map[string]any{"id": id, "type": method}
		for k, v := range params {
			request[k] = v
		}
	}
	if err := p.process.Send(request); err != nil {
		return nil, err
	}
	for {
		select {
		case <-p.ctx.Done():
			return nil, p.ctx.Err()
		case frame, ok := <-p.process.Frames:
			if !ok {
				return nil, errors.New("Harness catalog connection closed.")
			}
			if str(frame, "id") != id {
				continue
			}
			if p.pi {
				if !truth(frame, "success") {
					return nil, errors.New("Harness catalog request failed.")
				}
				return object(frame["data"]), nil
			}
			if frame["error"] != nil {
				return nil, errors.New("Harness catalog request failed.")
			}
			return object(frame["result"]), nil
		}
	}
}

func codexCatalog(ctx context.Context, b binary, cwd string) (*ModelCatalog, error) {
	rpc, err := newCatalogRPC(ctx, b, cwd, false)
	if err != nil {
		return nil, err
	}
	defer rpc.process.Close()
	configResult, err := rpc.call("config/read", map[string]any{"includeLayers": false, "cwd": cwd})
	if err != nil {
		return nil, err
	}
	config := object(configResult["config"])
	provider := str(config, "model_provider")
	if provider == "" {
		provider = "openai"
	}
	c := &ModelCatalog{Models: []ModelOption{}, DefaultModel: str(config, "model"), DefaultEffort: str(config, "model_reasoning_effort"), EffortLabel: "Thinking effort", providerOptions: map[string]map[string]any{provider: object(object(config["model_providers"])[provider])}}
	authType := "configured"
	if provider == "openai" {
		account, accountErr := rpc.call("account/read", map[string]any{"refreshToken": false})
		if accountErr != nil || object(account["account"]) == nil {
			return c, nil
		}
		switch str(object(account["account"]), "type") {
		case "chatgpt":
			authType = "oauth"
		case "apiKey":
			authType = "api_key"
		default:
			return c, nil
		}
	}
	c.ConnectedProviders = []ConnectedProvider{{ID: provider, Name: providerDisplayName(provider), AuthType: authType}}
	cursor := ""
	seen := map[string]bool{}
	for {
		params := map[string]any{"limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := rpc.call("model/list", params)
		if err != nil {
			return nil, err
		}
		items, _ := result["data"].([]any)
		for _, item := range items {
			m := object(item)
			if truth(m, "hidden") {
				continue
			}
			id := str(m, "model")
			if id == "" {
				continue
			}
			efforts := []string{}
			choices, _ := m["supportedReasoningEfforts"].([]any)
			for _, choice := range choices {
				if effort := str(object(choice), "reasoningEffort"); effort != "" {
					efforts = append(efforts, effort)
				}
			}
			c.Models = append(c.Models, ModelOption{ID: id, Name: str(m, "displayName"), Provider: provider, ProviderName: provider, Efforts: efforts, DefaultEffort: str(m, "defaultReasoningEffort")})
			if c.DefaultModel == "" && truth(m, "isDefault") {
				c.DefaultModel = id
			}
		}
		cursor = str(result, "nextCursor")
		if cursor == "" {
			break
		}
		if seen[cursor] {
			return nil, errors.New("Repeated model catalog cursor.")
		}
		seen[cursor] = true
	}
	return c, nil
}

func piCatalog(ctx context.Context, b binary, cwd string) (*ModelCatalog, error) {
	rpc, err := newCatalogRPC(ctx, b, cwd, true)
	if err != nil {
		return nil, err
	}
	defer rpc.process.Close()
	state, err := rpc.call("get_state", nil)
	if err != nil {
		return nil, err
	}
	result, err := rpc.call("get_available_models", nil)
	if err != nil {
		return nil, err
	}
	items, _ := result["models"].([]any)
	// Ask the installed Pi model library for capabilities, including custom models.
	if len(b.args) == 0 {
		return nil, errors.New("Pi model discovery requires its Node CLI installation.")
	}
	const script = `import {pathToFileURL} from 'node:url'; const lib=await import(import.meta.resolve('@earendil-works/pi-ai',pathToFileURL(process.argv[1]).href)); let input=''; for await(const chunk of process.stdin) input+=chunk; console.log(JSON.stringify(JSON.parse(input).map(m=>lib.getSupportedThinkingLevels(m))));`
	data, _ := json.Marshal(items)
	cmd := exec.CommandContext(ctx, b.path, "--experimental-import-meta-resolve", "--input-type=module", "-e", script, b.args[0])
	cmd.Dir = cwd
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stderr = &cappedBuffer{}
	configureProcess(cmd)
	cmd.Cancel = func() error { return killTree(cmd.Process) }
	cmd.WaitDelay = 3 * time.Second
	var output limitedOutput
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, errors.New("Could not read Pi thinking capabilities.")
	}
	var levels [][]string
	if json.Unmarshal(output.Bytes(), &levels) != nil || len(levels) != len(items) {
		return nil, errors.New("Invalid Pi thinking capabilities.")
	}
	current := object(state["model"])
	c := &ModelCatalog{Models: []ModelOption{}, DefaultEffort: str(state, "thinkingLevel"), EffortLabel: "Thinking effort"}
	connected := map[string]bool{}
	if str(current, "id") != "" {
		c.DefaultModel = str(current, "provider") + "/" + str(current, "id")
	}
	for i, item := range items {
		m := object(item)
		provider, id := str(m, "provider"), str(m, "id")
		if provider == "" || id == "" {
			continue
		}
		// get_available_models is Pi's credential-filtered snapshot, not its full catalog.
		if !connected[provider] {
			connected[provider] = true
			c.ConnectedProviders = append(c.ConnectedProviders, ConnectedProvider{ID: provider, Name: providerDisplayName(provider), AuthType: harnessAuthType("pi", provider)})
		}
		efforts := levels[i]
		if !truth(m, "reasoning") {
			efforts = []string{}
		}
		defaultEffort := c.DefaultEffort
		if !slices.Contains(efforts, defaultEffort) {
			defaultEffort = ""
			if len(efforts) > 0 {
				defaultEffort = efforts[0]
			}
		}
		c.Models = append(c.Models, ModelOption{ID: provider + "/" + id, Name: str(m, "name"), Provider: provider, ProviderName: providerDisplayName(provider), Efforts: efforts, DefaultEffort: defaultEffort, endpoint: str(m, "baseUrl"), customAuth: hasAuthHeaders(object(m["headers"])), api: str(m, "api")})
	}
	return c, nil
}

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > openCodeResponseLimit {
		return 0, errors.New("Harness output too large.")
	}
	return b.Buffer.Write(p)
}

// Short-lived authenticated loopback server used only for catalog/config reads.
func openCodeCatalog(ctx context.Context, b binary, cwd string) (*ModelCatalog, error) {
	h := &openCodeHTTP{directory: cwd, password: newID()}
	transport := &http.Transport{ResponseHeaderTimeout: 15 * time.Second}
	defer transport.CloseIdleConnections()
	h.client = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Redirect refused.") }}
	cmd := exec.CommandContext(ctx, b.path, append(append([]string{}, b.args...), "serve", "--hostname", "127.0.0.1", "--port", "0", "--mdns=false")...)
	cmd.Dir = cwd
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "OPENCODE_SERVER_PASSWORD") && !strings.EqualFold(key, "OPENCODE_SERVER_USERNAME") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "OPENCODE_SERVER_USERNAME=opencode", "OPENCODE_SERVER_PASSWORD="+h.password)
	configureProcess(cmd)
	cmd.Cancel = func() error { return killTree(cmd.Process) }
	cmd.WaitDelay = 3 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = &cappedBuffer{}
	if err = cmd.Start(); err != nil {
		return nil, errors.New("Could not start OpenCode catalog server.")
	}
	address := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), openCodeFrameLimit)
		for scanner.Scan() {
			if value, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "opencode server listening on "); ok {
				select {
				case address <- value:
				default:
				}
			}
		}
		_ = cmd.Wait()
		close(done)
	}()
	defer func() { _ = killTree(cmd.Process); <-done }()
	select {
	case value := <-address:
		u, e := url.Parse(value)
		if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("Invalid catalog server address.")
		}
		h.base = u.String()
	case <-done:
		return nil, errors.New("OpenCode catalog server exited.")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var providers struct {
		All []struct {
			ID      string         `json:"id"`
			Name    string         `json:"name"`
			Source  string         `json:"source"`
			Options map[string]any `json:"options"`
			Models  map[string]struct {
				Name    string         `json:"name"`
				Headers map[string]any `json:"headers"`
				Options map[string]any `json:"options"`
				API     struct {
					URL string `json:"url"`
				} `json:"api"`
				Variants map[string]json.RawMessage `json:"variants"`
			} `json:"models"`
		} `json:"all"`
		Connected []string          `json:"connected"`
		Default   map[string]string `json:"default"`
	}
	if err = h.json(ctx, http.MethodGet, "/provider", nil, &providers); err != nil {
		return nil, err
	}
	var config map[string]any
	if err = h.json(ctx, http.MethodGet, "/config", nil, &config); err != nil {
		return nil, err
	}
	c := &ModelCatalog{Models: []ModelOption{}, DefaultModel: str(config, "model"), EffortLabel: "Variant", providerOptions: map[string]map[string]any{}}
	for _, p := range providers.All {
		if !slices.Contains(providers.Connected, p.ID) {
			continue
		}
		// Native "connected" also includes automatically available/free catalogs.
		// Keep authenticated providers or an explicitly configured connection only.
		configured := object(object(config["provider"])[p.ID]) != nil
		authType := harnessAuthType("opencode", p.ID)
		if p.Source != "api" && p.Source != "env" && !configured && authType == "configured" {
			continue
		}
		c.ConnectedProviders = append(c.ConnectedProviders, ConnectedProvider{ID: p.ID, Name: p.Name, AuthType: authType})
		if p.Options == nil {
			p.Options = map[string]any{}
		}
		p.Options["_source"] = p.Source
		c.providerOptions[p.ID] = p.Options
		for id, m := range p.Models {
			efforts := []string{}
			for variant := range m.Variants {
				efforts = append(efforts, variant)
			}
			sort.Strings(efforts)
			c.Models = append(c.Models, ModelOption{ID: p.ID + "/" + id, Name: m.Name, Provider: p.ID, ProviderName: p.Name, Efforts: efforts, endpoint: m.API.URL, customAuth: hasAuthHeaders(m.Headers) || m.Options["apiKey"] != nil || m.Options["baseURL"] != nil})
		}
	}
	// Respect the active agent's model/variant overrides as part of effective defaults.
	agent := str(config, "default_agent")
	if agent == "" {
		agent = "build"
	}
	settings := object(object(config["agent"])[agent])
	if model := str(settings, "model"); model != "" {
		c.DefaultModel = model
	}
	c.DefaultEffort = str(settings, "variant")
	return c, nil
}

func (p *adapter) resolvedSelection(model, effort string) error {
	p.app.mu.Lock()
	defer p.app.mu.Unlock()
	s := p.app.state.session(p.id)
	if s == nil || (s.ResolvedModel == model && s.ResolvedEffort == effort) {
		return nil
	}
	return p.app.commitLocked(func(d *diskState) {
		s := d.session(p.id)
		s.ResolvedModel = model
		s.ResolvedEffort = effort
		s.UpdatedAt = now()
	})
}

func (a *app) updateModelSettings(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(55 * time.Second))
	var body struct {
		Model  *string `json:"model"`
		Effort *string `json:"effort"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Model == nil || body.Effort == nil {
		fail(w, 400, "Provide model and effort.")
		return
	}
	s, cwd, ok := a.sessionForRead(w, r)
	if !ok {
		return
	}
	if s.Status == "running" {
		fail(w, 409, "Wait for the current turn before changing models.")
		return
	}
	catalog, err := a.catalog(r.Context(), s, cwd, false)
	if err != nil {
		fail(w, 502, "Could not verify available models. Retry loading the model list.")
		return
	}
	id := *body.Model
	if id == "" {
		id = catalog.DefaultModel
		if id == "" {
			id = s.ResolvedModel
		}
	}
	option := catalog.find(id)
	if *body.Model != "" && option == nil {
		fail(w, 400, "Model is not available from a connected provider.")
		return
	}
	if *body.Effort != "" && (option == nil || !slices.Contains(option.Efforts, *body.Effort)) {
		fail(w, 400, "This model does not support the selected effort or variant.")
		return
	}
	a.mu.Lock()
	current := a.state.session(s.ID)
	if current == nil {
		a.mu.Unlock()
		fail(w, 404, "Session not found.")
		return
	}
	if a.runs[s.ID] != nil {
		a.mu.Unlock()
		fail(w, 409, "Wait for the current turn before changing models.")
		return
	}
	if current.Harness != s.Harness {
		a.mu.Unlock()
		fail(w, 409, "Harness changed. Retry the model selection.")
		return
	}
	if err := a.commitLocked(func(d *diskState) {
		s := d.session(s.ID)
		modelChanged := s.Model != *body.Model
		s.Model = *body.Model
		s.Effort = *body.Effort
		s.ResolvedEffort = ""
		if modelChanged {
			s.ResolvedModel = ""
			if s.Usage != nil {
				s.Usage.Context = nil
			}
		}
		s.UpdatedAt = now()
	}); err != nil {
		a.mu.Unlock()
		fail(w, 503, err.Error())
		return
	}
	response := a.currentSessionUpdateLocked(s.ID)
	a.mu.Unlock()
	respond(w, 200, response)
}
