package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

// These plugins use the V1 plugin API; never load them into a V2 runtime.
//
//go:embed opencode-v1-graph-plugin.mjs
var openCodeV1GraphPlugin string

//go:embed opencode-v1-permissions-plugin.mjs
var openCodeV1PermissionsPlugin string

func (p *adapter) openCodeV1Config(config map[string]any, dir string, cleanup func()) (string, func(), error) {
	data, _ := json.Marshal(map[string]string{"url": p.bridge.url, "token": p.bridge.token})
	plugin := strings.Replace(openCodeV1GraphPlugin, "/*KLM_GRAPH_CONFIG*/{}", string(data), 1)
	if !p.graphNode() {
		plugin = "export default async function () { return {}; }"
	}
	policy, _ := json.Marshal(map[string]any{"url": p.bridge.url, "token": p.bridge.token, "yolo": p.yolo, "graphNode": p.graphNode(), "library": p.capabilities != nil})
	plugin = strings.Replace(plugin, "export default async function", "async function graphPlugin", 1) + "\n" + strings.Replace(openCodeV1PermissionsPlugin, "/*KLM_PERMISSION_CONFIG*/{}", string(policy), 1)
	pluginPath := filepath.Join(dir, "klm-policy.mjs")
	if err := os.WriteFile(pluginPath, []byte(plugin), 0600); err != nil {
		cleanup()
		return "", nil, err
	}
	plugins := []any{}
	if value, ok := config["plugin"]; ok {
		var valid bool
		plugins, valid = value.([]any)
		if !valid {
			cleanup()
			return "", nil, errors.New("OpenCode V1 plugin configuration is not a list.")
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

// The shared execution loop uses V2 route names. Only wire-level differences
// belong here; ownership, permissions, history and graph controls stay shared.
func (h *openCodeHTTP) v1Request(method, path string, body any) (string, string, any) {
	path = strings.TrimPrefix(path, "/api")
	switch path {
	case "/permission/request":
		path = "/permission"
	case "/form":
		path = "/question"
	}
	if strings.HasPrefix(path, "/session/") {
		if strings.HasSuffix(path, "/interrupt") {
			path = strings.TrimSuffix(path, "/interrupt") + "/abort"
		}
		if strings.HasSuffix(path, "/prompt") {
			path += "_async"
		}
		if index := strings.Index(path, "/permission/"); index >= 0 {
			path = path[index:]
			body = map[string]any{"reply": object(body)["decision"]}
		}
	}
	if path == "/session" && method == http.MethodPost {
		body = map[string]any{"title": object(body)["title"]}
	}
	return method, path, body
}

func (h *openCodeHTTP) checkVersion(ctx context.Context) error {
	endpoint, expected := "/api/info", "2."
	var info struct {
		Version string `json:"version"`
		Healthy bool   `json:"healthy"`
	}
	if h.v1 {
		endpoint, expected = "/global/health", "1."
	}
	if err := h.json(ctx, http.MethodGet, endpoint, nil, &info); err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimPrefix(info.Version, "v"), expected) || h.v1 && !info.Healthy {
		return errors.New("OpenCode version does not match Settings > OpenCode. Select the matching executable and restart the engine.")
	}
	return nil
}

func (h *openCodeHTTP) prompt(payload submission, model, effort string) map[string]any {
	if !h.v1 {
		return payload.openCodeV2Prompt()
	}
	prompt := map[string]any{"parts": payload.openCodeParts()}
	if model != "" {
		provider, id, _ := strings.Cut(model, "/")
		prompt["model"] = map[string]any{"providerID": provider, "modelID": id}
	}
	if effort != "" {
		prompt["variant"] = effort
	}
	return prompt
}

func (h *openCodeHTTP) v1Catalog(ctx context.Context) (*ModelCatalog, error) {
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
	if err := h.json(ctx, http.MethodGet, "/provider", nil, &providers); err != nil {
		return nil, err
	}
	var config map[string]any
	if err := h.json(ctx, http.MethodGet, "/config", nil, &config); err != nil {
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

func (p *adapter) requestOpenCodeV1Question(ctx context.Context, h *openCodeHTTP, properties map[string]any, questions map[string]*atomic.Bool) error {
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
