// Standalone, disposable SDK/OAuth compatibility probe. It does not start the
// engine, call a model, read credentials or write the user's skill library.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

func check(ok bool, message string) {
	if !ok {
		panic(message)
	}
}
func require(err error) {
	if err != nil {
		panic(err)
	}
}

func fixtureServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "disposable-fixture", Version: "1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, args struct {
		Text string `json:"text"`
	}) (*mcp.CallToolResult, any, error) { return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args.Text}}, StructuredContent: map[string]any{"text": args.Text}}, nil, nil })
	s.AddResource(&mcp.Resource{URI: "fixture://document", Name: "Document"}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "fixture://document", Text: "fixture document"}}}, nil
	})
	return s
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "stdio" {
		require(fixtureServer().Run(context.Background(), &mcp.StdioTransport{}))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return fixtureServer() }, nil))
	defer httpServer.Close()
	self, err := os.Executable()
	require(err)
	transports := []mcp.Transport{&mcp.StreamableClientTransport{Endpoint: httpServer.URL, MaxRetries: -1}, &mcp.CommandTransport{Command: exec.Command(self, "stdio")}}
	for _, transport := range transports {
		client := mcp.NewClient(&mcp.Implementation{Name: "fixture-client", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
		session, err := client.Connect(ctx, transport, nil)
		require(err)
		tools, err := session.ListTools(ctx, nil)
		require(err)
		check(len(tools.Tools) == 1 && tools.Tools[0].Name == "echo", "unexpected tools")
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "fixture"}})
		require(err)
		check(!result.IsError && result.Content[0].(*mcp.TextContent).Text == "fixture" && result.StructuredContent != nil, "result was flattened")
		resources, err := session.ListResources(ctx, nil)
		require(err)
		check(len(resources.Resources) == 1, "missing resources")
		content, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "fixture://document"})
		require(err)
		check(content.Contents[0].Text == "fixture document", "resource mismatch")
		require(session.Close())
	}
	probeOAuth(ctx)
	fmt.Println("Disposable SDK probe passed: stdio, Streamable HTTP, structured tools/resources; PKCE exchange + refresh with a non-loopback HTTPS callback URI routed to an owned fixture. No production services, model calls or credentials.")
}

func probeOAuth(ctx context.Context) {
	const callbackURI = "https://engine.example.invalid/api/library/mcp/oauth/callback"
	state, verifier := oauth2.GenerateVerifier(), oauth2.GenerateVerifier()
	code := make(chan string, 1)
	callback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		check(r.Host == "engine.example.invalid", "callback host mismatch")
		check(r.URL.Query().Get("state") == state, "state mismatch")
		code <- r.URL.Query().Get("code")
		_, _ = w.Write([]byte("Fixture callback received"))
	}))
	defer callback.Close()
	challenge := make(chan string, 1)
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": provider.URL, "authorization_endpoint": provider.URL + "/authorize", "token_endpoint": provider.URL + "/token", "registration_endpoint": provider.URL + "/register", "code_challenge_methods_supported": []string{"S256"}})
		case "/register":
			var metadata map[string]any
			require(json.NewDecoder(r.Body).Decode(&metadata))
			check(metadata["redirect_uris"].([]any)[0] == callbackURI, "provider rejected callback")
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "fixture-client", "redirect_uris": []string{callbackURI}, "token_endpoint_auth_method": "none"})
		case "/authorize":
			q := r.URL.Query()
			check(q.Get("redirect_uri") == callbackURI && q.Get("code_challenge_method") == "S256", "invalid PKCE request")
			challenge <- q.Get("code_challenge")
			destination, _ := url.Parse(callbackURI)
			query := destination.Query()
			query.Set("code", "fixture-code")
			query.Set("state", q.Get("state"))
			destination.RawQuery = query.Encode()
			http.Redirect(w, r, destination.String(), http.StatusFound)
		case "/token":
			require(r.ParseForm())
			if r.Form.Get("grant_type") == "authorization_code" {
				sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				check(base64.RawURLEncoding.EncodeToString(sum[:]) == <-challenge, "PKCE verification failed")
				check(r.Form.Get("resource") == provider.URL+"/mcp", "wrong token audience")
			} else {
				check(r.Form.Get("grant_type") == "refresh_token" && r.Form.Get("refresh_token") == "fixture-refresh", "invalid refresh")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access", "refresh_token": "fixture-refresh", "token_type": "Bearer", "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	registration, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.URL+"/register", strings.NewReader(`{"redirect_uris":["`+callbackURI+`"]}`))
	require(err)
	registration.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(registration)
	require(err)
	_ = response.Body.Close()
	check(response.StatusCode == 200, "registration failed")
	config := oauth2.Config{ClientID: "fixture-client", RedirectURL: callbackURI, Endpoint: oauth2.Endpoint{AuthURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}
	// A disposable browser transport maps just the fictional engine hostname to
	// our TLS fixture, using its trusted certificate and certificate server name.
	transport := callback.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	certificate := callback.Certificate()
	if len(certificate.DNSNames) > 0 {
		transport.TLSClientConfig.ServerName = certificate.DNSNames[0]
	} else {
		transport.TLSClientConfig.ServerName = certificate.IPAddresses[0].String()
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "engine.example.invalid:443" {
			address = callback.Listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	browser := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	authURL := config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", provider.URL+"/mcp"))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	require(err)
	response, err = browser.Do(request)
	require(err)
	_ = response.Body.Close()
	check(response.StatusCode == 200, "callback failed")
	token, err := config.Exchange(ctx, <-code, oauth2.VerifierOption(verifier), oauth2.SetAuthURLParam("resource", provider.URL+"/mcp"))
	require(err)
	check(token.Valid(), "invalid token")
	token.Expiry = time.Now().Add(-time.Hour)
	refreshed, err := config.TokenSource(ctx, token).Token()
	require(err)
	check(refreshed.Valid(), "refresh failed")
}
