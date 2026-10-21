package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

type mcpOAuthMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
}

type mcpOAuthTransaction struct {
	ReturnURL    string
	ID           string
	ConnectionID string
	Revision     string
	CredentialID string
	State        string
	Verifier     string
	Expires      time.Time
	Config       oauth2.Config
	Resource     string
	Issuer       string
}

func oauthHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func validOAuthEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Hostname() != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || u.Scheme == "http" && loopbackHost(u.Host))
}

func oauthJSON(ctx context.Context, method, endpoint string, input, output any) error {
	if !validOAuthEndpoint(endpoint) {
		return errors.New("OAuth endpoint must use HTTPS (HTTP only on loopback).")
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := oauthHTTPClient().Do(req)
	if err != nil {
		return errors.New("OAuth metadata request failed.")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("OAuth endpoint rejected metadata or registration.")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 256<<10+1))
	if err != nil || len(data) > 256<<10 || json.Unmarshal(data, output) != nil {
		return errors.New("Invalid or oversized OAuth response.")
	}
	return nil
}

func discoverMCPOAuth(ctx context.Context, endpoint string) (oauthex.ProtectedResourceMetadata, mcpOAuthMetadata, error) {
	var resource oauthex.ProtectedResourceMetadata
	var issuer mcpOAuthMetadata
	u, err := safeMCPURL(endpoint)
	if err != nil || !validOAuthEndpoint(endpoint) {
		return resource, issuer, errors.New("OAuth requires an HTTPS MCP endpoint (HTTP only on loopback).")
	}
	base := u.Scheme + "://" + u.Host
	paths := []struct{ URL, Resource string }{{base + "/.well-known/oauth-protected-resource" + u.EscapedPath(), endpoint}, {base + "/.well-known/oauth-protected-resource", base}}
	// A server can advertise a non-default RFC 9728 metadata location in its
	// unauthenticated challenge. No upstream credentials accompany discovery.
	probe, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if response, requestErr := oauthHTTPClient().Do(probe); requestErr == nil {
		challenge := regexp.MustCompile(`(?i)\bresource_metadata="([^"]+)"`).FindStringSubmatch(response.Header.Get("WWW-Authenticate"))
		_ = response.Body.Close()
		if len(challenge) == 2 && validOAuthEndpoint(challenge[1]) {
			paths = append([]struct{ URL, Resource string }{{challenge[1], endpoint}}, paths...)
		}
	}
	found := false
	for _, candidate := range paths {
		var value oauthex.ProtectedResourceMetadata
		if oauthJSON(ctx, http.MethodGet, candidate.URL, nil, &value) == nil && value.Resource == candidate.Resource && len(value.AuthorizationServers) > 0 {
			resource = value
			found = true
			break
		}
	}
	if !found {
		return resource, issuer, errors.New("MCP protected-resource metadata is unavailable or its resource does not match the server.")
	}
	authority := resource.AuthorizationServers[0]
	if !validOAuthEndpoint(authority) {
		return resource, issuer, errors.New("Invalid OAuth issuer.")
	}
	parsed, _ := url.Parse(authority)
	if parsed.RawQuery != "" {
		return resource, issuer, errors.New("OAuth issuer cannot contain a query.")
	}
	origin := parsed.Scheme + "://" + parsed.Host
	suffix := strings.TrimSuffix(parsed.EscapedPath(), "/")
	urls := []string{origin + "/.well-known/oauth-authorization-server" + suffix, origin + "/.well-known/openid-configuration" + suffix}
	if suffix != "" {
		urls = append(urls, origin+suffix+"/.well-known/openid-configuration")
	}
	for _, location := range urls {
		var value mcpOAuthMetadata
		if oauthJSON(ctx, http.MethodGet, location, nil, &value) == nil && value.Issuer == authority && validOAuthEndpoint(value.AuthorizationEndpoint) && validOAuthEndpoint(value.TokenEndpoint) && slices.Contains(value.CodeChallengeMethodsSupported, "S256") {
			issuer = value
			return resource, issuer, nil
		}
	}
	return resource, issuer, errors.New("OAuth issuer metadata must match the issuer and support PKCE S256.")
}

func (a *app) beginMCPOAuth(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
	var body struct {
		ReturnURL string `json:"returnUrl"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.ReturnURL != "" {
		u, err := url.Parse(body.ReturnURL)
		if len(body.ReturnURL) > 4096 || err != nil || u.User != nil || u.Hostname() == "" || u.Scheme != "https" && u.Scheme != "http" || u.Scheme+"://"+u.Host != r.Header.Get("Origin") {
			fail(w, 400, "OAuth return URL must belong to the initiating web client origin.")
			return
		}
	}
	a.mu.Lock()
	connection, exists := a.state.MCPConnections[r.PathValue("connectionID")]
	a.mu.Unlock()
	if !exists || connection.Config.Auth != "oauth" {
		fail(w, 400, "Select an OAuth connection.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	resource, metadata, err := discoverMCPOAuth(ctx, connection.Config.URL)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	credential, err := a.readMCPCredential(connection.CredentialID)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	clientID, secret := connection.Config.ClientID, credential.ClientSecret
	authStyle := oauth2.AuthStyleInParams
	if secret != "" && slices.Contains(metadata.TokenEndpointAuthMethodsSupported, "client_secret_basic") {
		authStyle = oauth2.AuthStyleInHeader
	}
	if secret != "" && !slices.Contains(metadata.TokenEndpointAuthMethodsSupported, "client_secret_basic") && !slices.Contains(metadata.TokenEndpointAuthMethodsSupported, "client_secret_post") {
		fail(w, 400, "OAuth server does not support the configured client authentication method.")
		return
	}
	if clientID == "" {
		if metadata.RegistrationEndpoint == "" {
			fail(w, 400, "This provider requires a registered client ID. Configure it before authenticating.")
			return
		}
		var registration struct {
			ClientID     string   `json:"client_id"`
			ClientSecret string   `json:"client_secret"`
			Method       string   `json:"token_endpoint_auth_method"`
			RedirectURIs []string `json:"redirect_uris"`
		}
		err := oauthJSON(ctx, http.MethodPost, metadata.RegistrationEndpoint, map[string]any{"client_name": "KLM", "redirect_uris": []string{connection.Config.RedirectURI}, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"}, &registration)
		if err != nil || registration.ClientID == "" || !slices.Contains(registration.RedirectURIs, connection.Config.RedirectURI) {
			fail(w, 400, "OAuth client registration failed or did not accept the configured redirect URI.")
			return
		}
		clientID, secret = registration.ClientID, registration.ClientSecret
		if registration.Method == "client_secret_basic" {
			authStyle = oauth2.AuthStyleInHeader
		} else if registration.Method != "" && registration.Method != "none" && registration.Method != "client_secret_post" {
			fail(w, 400, "OAuth client registration selected an unsupported authentication method.")
			return
		}
	}
	tx := &mcpOAuthTransaction{ID: newID(), ConnectionID: connection.ID, Revision: connection.Revision, CredentialID: connection.CredentialID, State: newID() + newID(), Verifier: oauth2.GenerateVerifier(), Expires: time.Now().Add(10 * time.Minute), Resource: resource.Resource, Issuer: metadata.Issuer}
	tx.ReturnURL = body.ReturnURL
	tx.Config = oauth2.Config{ClientID: clientID, ClientSecret: secret, RedirectURL: connection.Config.RedirectURI, Scopes: connection.Config.Scopes, Endpoint: oauth2.Endpoint{AuthURL: metadata.AuthorizationEndpoint, TokenURL: metadata.TokenEndpoint, AuthStyle: authStyle}}
	a.oauthMu.Lock()
	if a.oauthTransactions == nil {
		a.oauthTransactions = map[string]*mcpOAuthTransaction{}
	}
	for state, previous := range a.oauthTransactions {
		if time.Now().After(previous.Expires) || previous.ConnectionID == connection.ID {
			delete(a.oauthTransactions, state)
		}
	}
	if len(a.oauthTransactions) >= 16 {
		a.oauthMu.Unlock()
		fail(w, 409, "Too many OAuth requests. Finish an existing request first.")
		return
	}
	a.oauthTransactions[tx.State] = tx
	a.oauthMu.Unlock()
	options := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(tx.Verifier), oauth2.SetAuthURLParam("resource", tx.Resource)}
	respond(w, 200, map[string]any{"transactionId": tx.ID, "authorizationUrl": tx.Config.AuthCodeURL(tx.State, options...), "expiresAt": tx.Expires.UTC().Format(time.RFC3339)})
}

func (a *app) completeMCPOAuth(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	state := r.URL.Query().Get("state")
	a.oauthMu.Lock()
	tx := a.oauthTransactions[state]
	if tx != nil {
		delete(a.oauthTransactions, state)
	}
	a.oauthMu.Unlock()
	if tx == nil || time.Now().After(tx.Expires) || len(r.URL.Query()["state"]) != 1 {
		fail(w, 400, "OAuth request is invalid, expired or already used.")
		return
	}
	// Reject issuer mix-up and mismatched callback destinations; reverse proxies
	// preserve Host. Configured redirect paths may include an ingress prefix.
	redirect, _ := url.Parse(tx.Config.RedirectURL)
	if !strings.EqualFold(r.Host, redirect.Host) || !strings.HasSuffix(redirect.Path, r.URL.Path) {
		fail(w, 400, "OAuth callback does not match the configured engine redirect URI.")
		return
	}
	if issuer := r.URL.Query().Get("iss"); issuer != "" && issuer != tx.Issuer {
		fail(w, 400, "OAuth callback issuer mismatch.")
		return
	}
	code := r.URL.Query().Get("code")
	if r.URL.Query().Get("error") != "" || code == "" || len(code) > 8192 || len(r.URL.Query()["code"]) != 1 {
		fail(w, 400, "OAuth authorization was declined or returned no valid code.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, oauthHTTPClient())
	token, err := tx.Config.Exchange(ctx, code, oauth2.VerifierOption(tx.Verifier), oauth2.SetAuthURLParam("resource", tx.Resource))
	if err != nil || token == nil || token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") {
		fail(w, 400, "OAuth token exchange failed. Authenticate again from Library.")
		return
	}
	a.credentialMu.Lock()
	defer a.credentialMu.Unlock()
	credential, err := a.readMCPCredential(tx.CredentialID)
	if err != nil {
		fail(w, 409, "Credentials changed during OAuth. Authenticate again.")
		return
	}
	credential.Token, credential.OAuthConfig, credential.Resource = token, &tx.Config, tx.Resource
	blob, err := protectCredential(credential)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	connection, exists := a.state.MCPConnections[tx.ConnectionID]
	if !exists || connection.Revision != tx.Revision || connection.CredentialID != tx.CredentialID {
		fail(w, 409, "Connection changed during OAuth. Authenticate again.")
		return
	}
	id := connection.CredentialID
	if id == "" {
		id = newID()
	}
	if err := a.commitLocked(func(d *diskState) {
		if d.MCPCredentials == nil {
			d.MCPCredentials = map[string][]byte{}
		}
		d.MCPCredentials[id] = blob
		c := d.MCPConnections[connection.ID]
		c.CredentialID = id
		d.MCPConnections[c.ID] = c
	}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	if tx.ReturnURL != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html lang="en"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Authentication complete</title><h1>Authentication complete</h1><p><a href="`+html.EscapeString(tx.ReturnURL)+`">Return to Library</a></p></html>`)
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "Authentication complete. Return to Library and refresh the connection.")
	}
}

func (a *app) disconnectMCPOAuth(w http.ResponseWriter, r *http.Request) {
	a.credentialMu.Lock()
	defer a.credentialMu.Unlock()
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	a.oauthMu.Lock()
	for state, tx := range a.oauthTransactions {
		if tx.ConnectionID == r.PathValue("connectionID") {
			delete(a.oauthTransactions, state)
		}
	}
	a.oauthMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	c, exists := a.state.MCPConnections[r.PathValue("connectionID")]
	if !exists {
		fail(w, 404, "Connection not found.")
		return
	}
	if err := a.commitLocked(func(d *diskState) {
		disconnectMCPCredentials(d, c.ID, c.CredentialID)
		c.CredentialID = ""
		c.Revision = newID()
		d.MCPConnections[c.ID] = c
	}); err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 200, publicMCPConnection(c))
}

func disconnectMCPCredentials(d *diskState, connectionID, currentID string) {
	delete(d.MCPCredentials, currentID)
	remove := func(snapshot *CapabilitySnapshot) {
		if snapshot != nil {
			for _, c := range snapshot.Connections {
				if c.ID == connectionID {
					delete(d.MCPCredentials, c.CredentialID)
				}
			}
		}
	}
	for _, s := range d.Sessions {
		remove(s.Capabilities.Applied)
		remove(s.Capabilities.Preparing)
	}
	for _, run := range d.GraphRuns {
		for _, snapshot := range run.Snapshot.Capabilities {
			remove(snapshot)
		}
	}
}

// Serialize refresh across conversations. Never retry an MCP operation on a 401;
// re-authentication is an explicit Library action.
func (a *app) mcpHTTPSecrets(ctx context.Context, c MCPConnectionSnapshot) (mcpCredential, error) {
	if c.Config.Auth == "none" {
		return mcpCredential{}, nil
	}
	if err := a.lockMCPCredentials(ctx); err != nil {
		return mcpCredential{}, err
	}
	defer a.credentialMu.Unlock()
	credential, err := a.readMCPCredential(c.CredentialID)
	if err != nil {
		return credential, err
	}
	if c.Config.Auth != "oauth" {
		return credential, nil
	}
	if credential.Token == nil || credential.OAuthConfig == nil {
		return credential, errors.New("Authenticate this connection in Library.")
	}
	if !credential.Token.Valid() {
		if credential.Token.RefreshToken == "" {
			return credential, errors.New("OAuth token expired; authenticate again.")
		}
		client := oauthHTTPClient()
		client.Transport = &oauthResourceTransport{endpoint: credential.OAuthConfig.Endpoint.TokenURL, resource: credential.Resource}
		refreshCtx := context.WithValue(ctx, oauth2.HTTPClient, client)
		token, err := credential.OAuthConfig.TokenSource(refreshCtx, credential.Token).Token()
		if err != nil {
			return credential, errors.New("OAuth refresh failed; authenticate again.")
		}
		credential.Token = token
		blob, err := protectCredential(credential)
		if err != nil {
			return credential, err
		}
		a.mu.Lock()
		if _, exists := a.state.MCPCredentials[c.CredentialID]; !exists {
			a.mu.Unlock()
			return credential, errors.New("OAuth credentials disconnected during refresh.")
		}
		err = a.commitLocked(func(d *diskState) { d.MCPCredentials[c.CredentialID] = blob })
		a.mu.Unlock()
		if err != nil {
			return credential, err
		}
	}
	credential.Bearer = credential.Token.AccessToken
	return credential, nil
}

// Owned HTTP cancellation must also interrupt a request waiting behind another
// connection's refresh; a plain Mutex.Lock would outlive that request context.
func (a *app) lockMCPCredentials(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.credentialMu.TryLock() {
		return nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if a.credentialMu.TryLock() {
				return nil
			}
		}
	}
}

type oauthResourceTransport struct{ endpoint, resource string }

func (t *oauthResourceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != t.endpoint {
		return nil, errors.New("OAuth token destination changed.")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()
	form, err := url.ParseQuery(string(data))
	if err != nil {
		return nil, err
	}
	form.Set("resource", t.resource)
	clone := r.Clone(r.Context())
	body := form.Encode()
	clone.Body = io.NopCloser(strings.NewReader(body))
	clone.ContentLength = int64(len(body))
	return http.DefaultTransport.RoundTrip(clone)
}
