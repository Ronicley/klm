package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type engineAccessSettings struct {
	AllowedOrigins    []string `json:"allowedOrigins"`
	TrustedTLSProxies []string `json:"trustedTlsProxies"`
}

type engineAccessSettingsView struct {
	engineAccessSettings
	RestartRequired bool `json:"restartRequired"`
	Restarting      bool `json:"restarting"`
}

func normalizeEngineAccessSettings(input engineAccessSettings) (engineAccessSettings, error) {
	if len(input.AllowedOrigins) > 32 || len(input.TrustedTLSProxies) > 32 {
		return input, errors.New("Use at most 32 origins and 32 trusted proxy CIDRs.")
	}
	next := engineAccessSettings{AllowedOrigins: []string{}, TrustedTLSProxies: []string{}}
	for _, value := range input.AllowedOrigins {
		origin := strings.TrimSpace(value)
		if origin == "" {
			continue
		}
		u, err := url.Parse(origin)
		if len(origin) > 2048 || err != nil || u.Host == "" || strings.Contains(u.Host, "*") || u.User != nil || origin != u.Scheme+"://"+u.Host || (u.Scheme != "https" && !(u.Scheme == "http" && loopbackHost(u.Host))) {
			return next, fmt.Errorf("Invalid allowed origin: %q. Use an exact HTTPS origin without a path or wildcard.", origin)
		}
		next.AllowedOrigins = append(next.AllowedOrigins, origin)
	}
	for _, value := range input.TrustedTLSProxies {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return next, errors.New("Trusted TLS proxies must be explicit IP CIDRs, such as 192.168.1.10/32.")
		}
		next.TrustedTLSProxies = append(next.TrustedTLSProxies, prefix.Masked().String())
	}
	slices.Sort(next.AllowedOrigins)
	slices.Sort(next.TrustedTLSProxies)
	next.AllowedOrigins = slices.Compact(next.AllowedOrigins)
	next.TrustedTLSProxies = slices.Compact(next.TrustedTLSProxies)
	return next, nil
}

func loadEngineAccessSettings(dir string) (engineAccessSettings, error) {
	// Environment remains the bootstrap fallback. An explicitly saved UI config
	// owns subsequent starts, including intentionally empty lists.
	settings := engineAccessSettings{
		AllowedOrigins:    strings.Split(os.Getenv("KLM_ALLOWED_ORIGINS"), ","),
		TrustedTLSProxies: strings.Split(os.Getenv("KLM_TRUSTED_TLS_PROXIES"), ","),
	}
	b, err := os.ReadFile(filepath.Join(dir, "engine-access.json"))
	if err == nil {
		if len(b) > 64<<10 {
			return settings, errors.New("Engine access settings exceed the supported size.")
		}
		settings = engineAccessSettings{}
		if err := json.Unmarshal(b, &settings); err != nil {
			return settings, errors.New("Cannot read engine access settings.")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return settings, errors.New("Cannot read engine access settings.")
	}
	return normalizeEngineAccessSettings(settings)
}

func (a *app) engineAccessSettingsView() engineAccessSettingsView {
	a.mu.Lock()
	restarting := a.restarting
	a.mu.Unlock()
	s := a.clientAuth
	s.mu.Lock()
	defer s.mu.Unlock()
	return engineAccessSettingsView{
		engineAccessSettings: s.savedAccess,
		RestartRequired:      !slices.Equal(s.activeAccess.AllowedOrigins, s.savedAccess.AllowedOrigins) || !slices.Equal(s.activeAccess.TrustedTLSProxies, s.savedAccess.TrustedTLSProxies),
		Restarting:           restarting,
	}
}

func (a *app) getEngineAccessSettings(w http.ResponseWriter, r *http.Request) {
	respond(w, 200, a.engineAccessSettingsView())
}

func (a *app) updateEngineAccessSettings(w http.ResponseWriter, r *http.Request) {
	var input engineAccessSettings
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if !decode(w, r, &input) {
		return
	}
	next, err := normalizeEngineAccessSettings(input)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.restarting || a.closing {
		fail(w, 409, "The engine is restarting.")
		return
	}
	s := a.clientAuth
	s.mu.Lock()
	err = s.saveAccessLocked(next)
	if err == nil {
		s.savedAccess = next
	}
	s.mu.Unlock()
	if err != nil {
		fail(w, 503, "Cannot persist engine access settings.")
		return
	}
	// Active policy is immutable for this process. Only restart applies saved policy.
	restartRequired := !slices.Equal(s.activeAccess.AllowedOrigins, next.AllowedOrigins) || !slices.Equal(s.activeAccess.TrustedTLSProxies, next.TrustedTLSProxies)
	respond(w, 200, engineAccessSettingsView{engineAccessSettings: next, RestartRequired: restartRequired})
}

func (a *app) restartEngineHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ConfirmInterrupt bool `json:"confirmInterrupt"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !input.ConfirmInterrupt {
		fail(w, 400, "Confirm that restarting will interrupt agents in progress.")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.restarting || a.closing {
		fail(w, 409, "The engine is already restarting.")
		return
	}
	if a.restartEngine == nil {
		fail(w, 503, "Engine restart is unavailable.")
		return
	}
	if err := a.restartEngine(); err != nil {
		fail(w, 503, "Cannot start engine restart. Check the engine log.")
		return
	}
	a.restarting = true
	respond(w, 202, map[string]bool{"restarting": true})
}
