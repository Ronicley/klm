package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type QuotaWindow struct {
	Name        string  `json:"name"`
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    int64   `json:"resetsAt"`
}
type QuotaSnapshot struct {
	Source     string        `json:"source"`
	ObservedAt string        `json:"observedAt"`
	Windows    []QuotaWindow `json:"windows"`
	Stale      bool          `json:"stale,omitempty"`
}
type quotaCache struct {
	snapshot *QuotaSnapshot
	expires  time.Time
}

// Credentials are read only, kept inside the engine, and never returned/logged.
// Refresh-token rotation is left to the CLI which owns the credentials.
func privateJSON(path string) map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 2<<20))
	if err != nil {
		return nil
	}
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		return nil
	}
	return result
}

func configHome(env string, parts ...string) string {
	if value := os.Getenv(env); value != "" {
		return value
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(append([]string{home}, parts...)...)
}

func storedHarnessCredential(harness, provider string) map[string]any {
	var path string
	switch harness {
	case "pi":
		path = filepath.Join(configHome("PI_CODING_AGENT_DIR", ".pi", "agent"), "auth.json")
	case "opencode":
		if content := os.Getenv("OPENCODE_AUTH_CONTENT"); content != "" {
			var auth map[string]any
			if json.Unmarshal([]byte(content), &auth) == nil {
				return object(auth[provider])
			}
		}
		path = filepath.Join(configHome("XDG_DATA_HOME", ".local", "share"), "opencode", "auth.json")
	default:
		return nil
	}
	return object(privateJSON(path)[provider])
}

func harnessCredential(harness, provider string) map[string]any {
	credential := storedHarnessCredential(harness, provider)
	if str(credential, "type") != "oauth" {
		return nil
	}
	return credential
}

func harnessAuthType(harness, provider string) string {
	switch str(storedHarnessCredential(harness, provider), "type") {
	case "oauth":
		return "oauth"
	case "api", "api_key":
		return "api_key"
	default:
		return "configured"
	}
}

func providerDisplayName(provider string) string {
	switch provider {
	case "openai":
		return "OpenAI"
	case "openai-codex", "chatgpt-codex":
		return "OpenAI (ChatGPT)"
	case "openrouter":
		return "OpenRouter"
	case "anthropic":
		return "Anthropic"
	default:
		return provider
	}
}

func hasAuthHeaders(headers map[string]any) bool {
	for key := range headers {
		switch strings.ToLower(key) {
		case "authorization", "proxy-authorization", "x-api-key", "api-key", "chatgpt-account-id", "openai-organization", "openai-project":
			return true
		}
	}
	return false
}

func oauthPlaceholder(value, harness string) bool {
	return value == "oauth" || (harness == "opencode" && value == "opencode-oauth-dummy-key")
}

func quotaFamily(m *ModelOption) string {
	if m == nil {
		return ""
	}
	switch m.Provider {
	case "openai", "openai-codex", "chatgpt-codex":
		return "openai"
	case "anthropic":
		return "anthropic"
	}
	// Native protocol metadata can identify a custom-named subscription provider.
	// Its route and OAuth account are still checked before any quota is shown.
	if m.api == "openai-codex-responses" {
		return "openai"
	}
	return ""
}

func piSubscriptionCredential(ctx context.Context, b binary, cwd string, m *ModelOption) map[string]any {
	release, err := b.use()
	if err != nil {
		return nil
	}
	defer release()
	if m.api != "openai-codex-responses" {
		return nil
	}
	// Resolve configured credential helpers through Pi itself. A helper-backed
	// subscription may have no OAuth entry under its custom provider ID.
	args := append(append([]string{}, b.args...), "auth", "check", "--json", "--credentials", "--no-refresh", "--provider", m.Provider, "--model", strings.TrimPrefix(m.ID, m.Provider+"/"))
	cmd := exec.CommandContext(ctx, b.path, args...)
	cmd.Env = runtimeEnvironment(os.Environ(), b.node)
	cmd.Dir = cwd
	cmd.Stderr = &cappedBuffer{}
	configureProcess(cmd)
	cmd.Cancel = func() error { return killTree(cmd.Process) }
	cmd.WaitDelay = 3 * time.Second
	var output limitedOutput
	cmd.Stdout = &output
	if cmd.Run() != nil {
		return nil
	}
	var result map[string]any
	if json.Unmarshal(output.Bytes(), &result) != nil || str(result, "status") != "ready" || str(result, "provider") != m.Provider {
		return nil
	}
	access := str(result, "credentials")
	account := oauthAccount(access)
	if account == "" {
		return nil
	}
	return map[string]any{"type": "oauth", "access": access, "accountId": account}
}

func oauthAccount(access string) string {
	parts := strings.Split(access, ".")
	if len(parts) != 3 {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(data, &claims) != nil {
		return ""
	}
	// Identity matching only, not token validation/authentication.
	if id := str(claims, "chatgpt_account_id"); id != "" {
		return id
	}
	if id := str(object(claims["https://api.openai.com/auth"]), "chatgpt_account_id"); id != "" {
		return id
	}
	if organizations, ok := claims["organizations"].([]any); ok && len(organizations) == 1 {
		return str(object(organizations[0]), "id")
	}
	return ""
}

func directQuotaModel(c *ModelCatalog, m *ModelOption, harness string) bool {
	if m == nil || m.customAuth {
		return false
	}
	family := quotaFamily(m)
	if family == "" {
		return false
	}
	options := c.providerOptions[m.Provider]
	for _, key := range []string{"apiKey", "api_key", "env_key", "apiKeyHelper"} {
		if value := str(options, key); value != "" && !oauthPlaceholder(value, harness) {
			return false
		}
	}
	oauthLoader := oauthPlaceholder(str(options, "apiKey"), harness) && harnessCredential(harness, m.Provider) != nil
	if str(options, "_source") == "env" && !oauthLoader {
		return false
	}
	if family == "anthropic" && (os.Getenv("ANTHROPIC_BASE_URL") != "" || os.Getenv("ANTHROPIC_AUTH_TOKEN") != "" || os.Getenv("ANTHROPIC_API_KEY") != "") && !oauthLoader {
		return false
	}
	if family == "openai" && os.Getenv("OPENAI_BASE_URL") != "" {
		return false
	}
	if m.Provider == "openai" && harness != "codex" && os.Getenv("OPENAI_API_KEY") != "" && !oauthLoader {
		return false
	}
	endpoint := m.endpoint
	for _, key := range []string{"baseURL", "base_url"} {
		if value := str(options, key); value != "" {
			endpoint = value
		}
	}
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" || u.User != nil {
			return false
		}
		host := u.Hostname()
		if family == "anthropic" {
			if host != "api.anthropic.com" {
				return false
			}
		} else if host != "api.openai.com" && host != "chatgpt.com" {
			return false
		}
	}
	// Pi's custom provider auth/headers are not represented in Model's public schema.
	if harness == "pi" {
		config := privateJSON(filepath.Join(configHome("PI_CODING_AGENT_DIR", ".pi", "agent"), "models.json"))
		p := object(object(config["providers"])[m.Provider])
		if (p["apiKey"] != nil && m.api != "openai-codex-responses") || hasAuthHeaders(object(p["headers"])) || p["auth"] != nil {
			return false
		}
	}
	return true
}

func (a *app) getQuota(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(55 * time.Second))
	s, cwd, ok := a.sessionForRead(w, r)
	if !ok {
		return
	}
	c, err := a.catalog(r.Context(), s, cwd, false)
	if err != nil {
		respond(w, 200, map[string]any{"quota": nil})
		return
	}
	id := s.Model
	if id == "" {
		id = c.DefaultModel
	}
	if id == "" {
		id = s.ResolvedModel
	}
	m := c.find(id)
	if !directQuotaModel(c, m, s.Harness) {
		respond(w, 200, map[string]any{"quota": nil, "reason": "connection_not_eligible"})
		return
	}
	quota, reason := a.readQuota(r.Context(), s, cwd, m)
	respond(w, 200, map[string]any{"quota": quota, "reason": reason})
}

func (a *app) readQuota(ctx context.Context, s Session, cwd string, m *ModelOption) (*QuotaSnapshot, string) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var source, target map[string]any
	claude := quotaFamily(m) == "anthropic"
	var b binary
	if claude {
		path, installed := executable("claude")
		if !installed {
			return nil, "claude_not_installed"
		}
		_ = path
		source = object(privateJSON(filepath.Join(configHome("CLAUDE_CONFIG_DIR", ".claude"), ".credentials.json"))["claudeAiOauth"])
		if source == nil || str(source, "accessToken") == "" {
			return nil, "claude_oauth_unavailable"
		}
		target = harnessCredential(s.Harness, m.Provider)
		if target == nil {
			return nil, "harness_oauth_unavailable"
		}
	} else {
		a.mu.Lock()
		b = a.binaries["codex"]
		a.mu.Unlock()
		if b.path == "" {
			return nil, "codex_not_installed"
		}
		auth := privateJSON(filepath.Join(configHome("CODEX_HOME", ".codex"), "auth.json"))
		if str(auth, "auth_mode") == "apikey" {
			return nil, "codex_subscription_unavailable"
		}
		source = object(auth["tokens"])
		if source == nil {
			return nil, "codex_subscription_unavailable"
		}
		if s.Harness != "codex" {
			target = harnessCredential(s.Harness, m.Provider)
			if target == nil && s.Harness == "pi" {
				a.mu.Lock()
				pi := a.binaries["pi"]
				a.mu.Unlock()
				if pi.path != "" {
					target = piSubscriptionCredential(ctx, pi, cwd, m)
				}
			}
			if target == nil {
				return nil, "harness_oauth_unavailable"
			}
			sourceID := str(source, "account_id")
			if sourceID == "" {
				sourceID = oauthAccount(str(source, "access_token"))
			}
			targetID := str(target, "accountId")
			if targetID == "" {
				targetID = oauthAccount(str(target, "access"))
			}
			if sourceID == "" || targetID != sourceID {
				return nil, "account_mismatch"
			}
		}
	}
	fingerprint, _ := json.Marshal([]any{s.ProjectID, s.Harness, m.ID, source, target})
	hash := sha256.Sum256(fingerprint)
	key := hex.EncodeToString(hash[:])
	a.quotaMu.Lock()
	defer a.quotaMu.Unlock()
	entry := a.quotas[key]
	if time.Now().Before(entry.expires) {
		return entry.snapshot, ""
	}
	var snapshot *QuotaSnapshot
	if claude {
		snapshot, err := claudeQuota(ctx, source, target, m)
		if err == nil {
			entry.snapshot = snapshot
		} else if entry.snapshot != nil {
			copy := *entry.snapshot
			copy.Stale = true
			entry.snapshot = &copy
		}
	} else {
		rpc, e := newCatalogRPC(ctx, b, cwd, false)
		if e == nil {
			defer rpc.process.Close()
			account, accountErr := rpc.call("account/read", map[string]any{"refreshToken": false})
			if accountErr == nil && str(object(account["account"]), "type") == "chatgpt" {
				result, readErr := rpc.call("account/rateLimits/read", nil)
				if readErr == nil {
					snapshot = codexQuota(result)
					entry.snapshot = snapshot
				} else {
					e = readErr
				}
			} else {
				entry.snapshot = nil
			}
		}
		if e != nil && entry.snapshot != nil {
			copy := *entry.snapshot
			copy.Stale = true
			entry.snapshot = &copy
		}
	}
	entry.expires = time.Now().Add(2 * time.Minute)
	if a.quotas == nil {
		a.quotas = map[string]quotaCache{}
	}
	a.quotas[key] = entry
	if entry.snapshot == nil {
		return nil, "quota_unavailable"
	}
	return entry.snapshot, ""
}

func numeric(value any) (float64, bool) {
	var n float64
	switch v := value.(type) {
	case float64:
		n = v
	case json.Number:
		var err error
		n, err = v.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0
}

func codexQuota(result map[string]any) *QuotaSnapshot {
	bucket := object(result["rateLimits"])
	if buckets := object(result["rateLimitsByLimitId"]); buckets != nil {
		if codex := object(buckets["codex"]); codex != nil {
			bucket = codex
		} else if id := str(bucket, "limitId"); id != "" && id != "codex" {
			return nil
		}
	}
	q := &QuotaSnapshot{Source: "Codex account", ObservedAt: now(), Windows: []QuotaWindow{}}
	for _, key := range []string{"primary", "secondary"} {
		window := object(bucket[key])
		used, valid := numeric(window["usedPercent"])
		reset := tokenCount(window["resetsAt"])
		if !valid || reset == nil || *reset <= time.Now().Unix() {
			continue
		}
		name := "Quota"
		if minutes := tokenCount(window["windowDurationMins"]); minutes != nil {
			duration := time.Duration(*minutes) * time.Minute
			if duration >= 24*time.Hour {
				name = duration.String() + " window"
			} else {
				name = duration.String() + " window"
			}
		}
		q.Windows = append(q.Windows, QuotaWindow{Name: name, UsedPercent: used, ResetsAt: *reset})
	}
	if len(q.Windows) == 0 {
		return nil
	}
	return q
}

func anthropicRead(ctx context.Context, path, access string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/api/oauth/"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "klm/0.1.0")
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Redirect refused.") }}
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Quota request failed.")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("Quota is unavailable.")
	}
	var result map[string]any
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if decoder.Decode(&result) != nil {
		return nil, errors.New("Invalid quota response.")
	}
	return result, nil
}

func claudeQuota(ctx context.Context, source, target map[string]any, m *ModelOption) (*QuotaSnapshot, error) {
	access, other := str(source, "accessToken"), str(target, "access")
	if access == "" || other == "" {
		return nil, nil
	}
	// Opaque Anthropic tokens require account + organization identity, not a model-name guess.
	if access != other {
		left, err := anthropicRead(ctx, "profile", access)
		if err != nil {
			return nil, err
		}
		right, err := anthropicRead(ctx, "profile", other)
		if err != nil {
			return nil, err
		}
		account := str(object(left["account"]), "uuid")
		org := str(object(left["organization"]), "uuid")
		if account == "" || org == "" || account != str(object(right["account"]), "uuid") || org != str(object(right["organization"]), "uuid") {
			return nil, nil
		}
	}
	data, err := anthropicRead(ctx, "usage", access)
	if err != nil {
		return nil, err
	}
	q := &QuotaSnapshot{Source: "Claude Code account", ObservedAt: now(), Windows: []QuotaWindow{}}
	keys := []string{"five_hour", "seven_day"}
	if strings.Contains(m.ID, "sonnet") {
		keys = append(keys, "seven_day_sonnet")
	}
	if strings.Contains(m.ID, "opus") {
		keys = append(keys, "seven_day_opus")
	}
	for _, key := range keys {
		window := object(data[key])
		used, valid := numeric(window["utilization"])
		reset, err := time.Parse(time.RFC3339Nano, str(window, "resets_at"))
		if !valid || err != nil || !reset.After(time.Now()) {
			continue
		}
		q.Windows = append(q.Windows, QuotaWindow{Name: strings.ReplaceAll(key, "_", " "), UsedPercent: used, ResetsAt: reset.Unix()})
	}
	if len(q.Windows) == 0 {
		return nil, nil
	}
	return q, nil
}
