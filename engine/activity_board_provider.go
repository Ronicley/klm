package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var boardRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var boardOrganizationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
var boardHTTP = &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

type boardProviderError struct {
	status  int
	message string
}

func (e *boardProviderError) Error() string { return e.message }

// Never return provider bodies, request URLs, transport errors or credentials.
func boardAPI(ctx context.Context, c activityBoardConnection, token, method, path string, body any, result any) error {
	base := "https://api.github.com"
	if c.Provider == "azure" {
		base = "https://dev.azure.com/" + url.PathEscape(c.Scope.Organization)
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return errors.New("Invalid provider path.")
	}
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return errors.New("Cannot encode provider request.")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(raw))
	if err != nil {
		return errors.New("Invalid provider request.")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "KLM-ActivityBoard")
	if c.Provider == "azure" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+token)))
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		if c.Provider == "azure" && method == "PATCH" {
			req.Header.Set("Content-Type", "application/json-patch+json")
		}
	}
	resp, err := boardHTTP.Do(req)
	if err != nil {
		return errors.New("Provider request interrupted or timed out. Refresh to reconcile before another update.")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message := fmt.Sprintf("Provider request failed (HTTP %d).", resp.StatusCode)
		switch resp.StatusCode {
		case 301, 302, 307, 308:
			message = "Provider scope moved. Reconnect using its current location."
		case 401, 403:
			message = "Provider denied access. Check the token, permissions and rate limits, then refresh."
		case 404:
			message = "Provider resource unavailable. Check scope and access, then refresh."
		case 409, 412:
			message = "Item changed. Refresh and try again."
		case 400, 422:
			message = "Provider rejected the update. Check mappings, transition rules and required fields in the provider."
		case 429:
			message = "Provider rate limit reached. Wait before refreshing."
		}
		return &boardProviderError{resp.StatusCode, message}
	}
	if result == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if err != nil || len(b) > 16<<20 {
		return errors.New("Provider response incomplete or too large. Snapshot was not replaced.")
	}
	if json.Unmarshal(b, result) != nil {
		return errors.New("Provider returned an invalid response.")
	}
	return nil
}

func boardValidateToken(token string) error {
	if len(token) < 1 || len(token) > 8192 || strings.ContainsAny(token, "\r\n\x00") {
		return errors.New("A valid provider token is required.")
	}
	return nil
}
func boardDiscover(ctx context.Context, c activityBoardConnection, token string) (activityBoardConnection, []activityBoardValue, error) {
	if err := boardValidateToken(token); err != nil {
		return c, nil, err
	}
	if c.Provider == "github" {
		return boardDiscoverGithub(ctx, c, token)
	}
	if c.Provider == "azure" {
		return boardDiscoverAzure(ctx, c, token)
	}
	return c, nil, errors.New("Choose GitHub or Azure DevOps.")
}
func boardResolveMappings(phases []activityBoardPhase, mappings map[string][]activityBoardValue, catalog []activityBoardValue) error {
	if err := validateActivityBoardMappings(phases, mappings); err != nil {
		return err
	}
	byID := map[string]activityBoardValue{}
	for _, v := range catalog {
		byID[v.ID] = v
	}
	for phase, values := range mappings {
		for i, v := range values {
			actual, ok := byID[v.ID]
			if !ok {
				return errors.New("Mapping required. A selected provider value no longer exists.")
			}
			mappings[phase][i] = actual
		}
	}
	return nil
}
func boardReadItem(ctx context.Context, c activityBoardConnection, token string, item activityBoardItem) (activityBoardItem, error) {
	if c.Provider == "github" {
		return boardReadGithubItem(ctx, c, token, item)
	}
	return boardReadAzureItem(ctx, c, token, item)
}
func boardScan(ctx context.Context, c activityBoardConnection, token string) (map[string]activityBoardItem, error) {
	if c.Provider == "github" {
		return boardScanGithub(ctx, c, token)
	}
	return boardScanAzure(ctx, c, token)
}

func boardSafeNumber(s string) bool {
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil && s != "0"
}
