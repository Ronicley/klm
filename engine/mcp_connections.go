package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

type MCPConnectionConfig struct {
	Transport   string   `json:"transport"`
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
	CWD         string   `json:"cwd,omitempty"` // empty means the actual execution directory
	URL         string   `json:"url,omitempty"`
	Auth        string   `json:"auth"` // none, manual, oauth
	ClientID    string   `json:"clientId,omitempty"`
	RedirectURI string   `json:"redirectUri,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
}

type MCPCatalog struct {
	Tools     []*mcp.Tool `json:"tools"`
	Resources bool        `json:"resources"`
	Revision  string      `json:"revision"`
}

type MCPConnection struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Revision      string              `json:"revision"`
	Config        MCPConnectionConfig `json:"config"`
	CredentialID  string              `json:"credentialId,omitempty"`
	Catalog       *MCPCatalog         `json:"catalog,omitempty"`
	LastTestAt    string              `json:"lastTestAt,omitempty"`
	LastTestError string              `json:"lastTestError,omitempty"`
}

// Only reference protected credentials, never copy tokens into run snapshots.
type MCPConnectionSnapshot struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Revision     string              `json:"revision"`
	Config       MCPConnectionConfig `json:"config"`
	CredentialID string              `json:"credentialId,omitempty"`
	Selection    MCPSelection        `json:"selection"`
	Catalog      *MCPCatalog         `json:"catalog,omitempty"`
}

type mcpCredential struct {
	Environment  map[string]string `json:"environment,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Bearer       string            `json:"bearer,omitempty"`
	ClientSecret string            `json:"clientSecret,omitempty"`
	Token        *oauth2.Token     `json:"token,omitempty"`
	OAuthConfig  *oauth2.Config    `json:"oauthConfig,omitempty"`
	Resource     string            `json:"resource,omitempty"`
}

var connectionIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func validLibraryConnectionID(id string) bool { return connectionIDPattern.MatchString(id) }

func snapshotMCPConnection(c MCPConnection, selection MCPSelection) MCPConnectionSnapshot {
	data, _ := json.Marshal(c)
	var clone MCPConnection
	_ = json.Unmarshal(data, &clone)
	return MCPConnectionSnapshot{ID: clone.ID, Name: clone.Name, Revision: clone.Revision, Config: clone.Config, CredentialID: clone.CredentialID, Selection: selection, Catalog: clone.Catalog}
}

func safeMCPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("Use an HTTP(S) endpoint without credentials, query or fragment; put credentials in secret headers.")
	}
	return u, nil
}

func validateMCPConfig(c MCPConnectionConfig) error {
	if c.Auth != "none" && c.Auth != "manual" && c.Auth != "oauth" {
		return errors.New("Select none, manual or oauth authentication.")
	}
	if len(c.Args) > 128 || len(c.Scopes) > 64 || len(c.ClientID) > 2048 {
		return errors.New("MCP configuration exceeds limits.")
	}
	for _, arg := range c.Args {
		if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
			return errors.New("Invalid command argument.")
		}
	}
	if c.CWD != "" {
		if !filepath.IsAbs(c.CWD) {
			return errors.New("MCP directory must be absolute or empty for the execution directory.")
		}
		if info, err := os.Stat(c.CWD); err != nil || !info.IsDir() {
			return errors.New("MCP directory is unavailable on the engine computer.")
		}
	}
	switch c.Transport {
	case "stdio":
		if c.Command == "" || len(c.Command) > 4096 || strings.ContainsRune(c.Command, 0) || c.Auth == "oauth" {
			return errors.New("Stdio requires an executable and does not use HTTP OAuth.")
		}
	case "http":
		if _, err := safeMCPURL(c.URL); err != nil {
			return err
		}
		if c.Auth == "oauth" {
			u, err := safeMCPURL(c.RedirectURI)
			if err != nil || (u.Scheme != "https" && !loopbackHost(u.Host)) || !strings.HasSuffix(u.Path, "/api/library/mcp/oauth/callback") {
				return errors.New("OAuth redirect must reach this engine's /api/library/mcp/oauth/callback via HTTPS (HTTP only on loopback) and be accepted by the provider.")
			}
		}
	default:
		return errors.New("Select stdio or Streamable HTTP.")
	}
	return nil
}

func (a *app) readMCPCredential(id string) (mcpCredential, error) {
	var result mcpCredential
	if id == "" {
		return result, nil
	}
	a.mu.Lock()
	blob, exists := a.state.MCPCredentials[id]
	a.mu.Unlock()
	if !exists {
		return result, errors.New("MCP credentials were disconnected or removed; authenticate again.")
	}
	data, err := unprotectMCPCredential(blob)
	if err != nil || json.Unmarshal(data, &result) != nil {
		return result, errors.New("Cannot decrypt MCP credentials for this engine user.")
	}
	return result, nil
}

func protectCredential(c mcpCredential) ([]byte, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return protectMCPCredential(data)
}

func publicMCPConnection(c MCPConnection) map[string]any {
	return map[string]any{"id": c.ID, "name": c.Name, "revision": c.Revision, "config": c.Config, "credentialsConfigured": c.CredentialID != "", "catalog": c.Catalog, "lastTestAt": c.LastTestAt, "lastTestError": c.LastTestError}
}

func (a *app) saveMCPConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          string              `json:"name"`
		Revision      string              `json:"revision"`
		Config        MCPConnectionConfig `json:"config"`
		Secrets       *mcpCredential      `json:"secrets"`
		RemoveSecrets bool                `json:"removeSecrets"`
	}
	if !decode(w, r, &body) {
		return
	}
	if _, ok := cleanLabel(body.Name, 100); !ok {
		fail(w, 400, "Enter a connection name.")
		return
	}
	if err := validateMCPConfig(body.Config); err != nil {
		fail(w, 400, err.Error())
		return
	}
	// Tokens and discovered endpoints can only be produced by OAuth, not injected through CRUD.
	if body.Secrets != nil {
		if body.Config.Transport == "http" && body.Config.Auth == "none" && (len(body.Secrets.Headers) > 0 || body.Secrets.Bearer != "" || body.Secrets.ClientSecret != "") {
			fail(w, 400, "Select manual or OAuth authentication before setting HTTP secrets.")
			return
		}
		if body.Secrets.Token != nil || body.Secrets.OAuthConfig != nil || body.Secrets.Resource != "" {
			fail(w, 400, "OAuth tokens cannot be supplied through connection editing.")
			return
		}
		if len(body.Secrets.Environment) > 100 || len(body.Secrets.Headers) > 50 {
			fail(w, 400, "Too many environment variables or headers.")
			return
		}
		for key, value := range body.Secrets.Environment {
			if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
				fail(w, 400, "Invalid environment variable.")
				return
			}
		}
		for key, value := range body.Secrets.Headers {
			if !regexp.MustCompile(`^[A-Za-z0-9-]+$`).MatchString(key) || strings.ContainsAny(value, "\r\n\x00") || slices.Contains([]string{"host", "content-length", "connection", "cookie", "mcp-session-id", "mcp-protocol-version"}, strings.ToLower(key)) {
				fail(w, 400, "Invalid or reserved secret header.")
				return
			}
		}
	}
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	a.mu.Lock()
	old, exists := a.state.MCPConnections[r.PathValue("connectionID")]
	connectionCount := len(a.state.MCPConnections)
	recovering := len(a.state.LibraryDeletions) > 0
	a.mu.Unlock()
	if recovering {
		fail(w, 409, "A library deletion needs recovery before editing connections.")
		return
	}
	if !exists && connectionCount >= 64 {
		fail(w, 409, "Library supports at most 64 MCP connections.")
		return
	}
	if r.PathValue("connectionID") != "" && (!exists || old.Revision != body.Revision) {
		fail(w, 409, "Connection changed. Reload before saving.")
		return
	}
	c := MCPConnection{ID: old.ID, Name: strings.TrimSpace(body.Name), Config: body.Config, CredentialID: old.CredentialID}
	if !exists {
		c.ID = newID()
	}
	if body.RemoveSecrets || exists && (old.Config.URL != c.Config.URL || old.Config.Auth != c.Config.Auth || old.Config.ClientID != c.Config.ClientID || old.Config.RedirectURI != c.Config.RedirectURI) {
		c.CredentialID = ""
	}
	var blob []byte
	var err error
	if body.Secrets != nil {
		blob, err = protectCredential(*body.Secrets)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		c.CredentialID = newID()
	}
	c.Revision = newID()
	a.mu.Lock()
	defer a.mu.Unlock()
	err = a.commitLocked(func(d *diskState) {
		if d.MCPConnections == nil {
			d.MCPConnections = map[string]MCPConnection{}
		}
		if d.MCPCredentials == nil {
			d.MCPCredentials = map[string][]byte{}
		}
		d.MCPConnections[c.ID] = c
		if blob != nil {
			d.MCPCredentials[c.CredentialID] = blob
		}
		pruneMCPCredentials(d)
	})
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, publicMCPConnection(c))
}

func (a *app) testMCPConnection(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
	a.mu.Lock()
	c, exists := a.state.MCPConnections[r.PathValue("connectionID")]
	a.mu.Unlock()
	if !exists {
		fail(w, 404, "Connection not found.")
		return
	}
	// Testing never infers an execution directory from the selected client project.
	if c.Config.Transport == "stdio" && c.Config.CWD == "" {
		fail(w, 400, "Set a fixed directory to test this local server; turns otherwise use their execution directory.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	live, err := a.connectMCP(ctx, snapshotMCPConnection(c, MCPSelection{ConnectionID: c.ID}), c.Config.CWD, nil)
	var catalog *MCPCatalog
	if err == nil {
		catalog, err = discoverMCPCatalog(ctx, live.session)
		if err == nil && live.catalogChanged.Load() {
			err = errors.New("MCP catalog changed during discovery.")
		}
		err = errors.Join(err, live.close())
	}
	message := ""
	if err != nil {
		message = "Connection test failed. Check the executable, endpoint and authentication on the engine computer."
	}
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if current, ok := a.state.MCPConnections[c.ID]; !ok || current.Revision != c.Revision {
		fail(w, 409, "Connection changed during the test. Reload.")
		return
	}
	if err := a.commitLocked(func(d *diskState) {
		current := d.MCPConnections[c.ID]
		current.LastTestAt = now()
		current.LastTestError = message
		if catalog != nil {
			current.Catalog = catalog
		}
		d.MCPConnections[c.ID] = current
	}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, publicMCPConnection(a.state.MCPConnections[c.ID]))
}
