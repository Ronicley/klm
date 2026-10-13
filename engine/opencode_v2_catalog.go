package main

import (
	"context"
	"net/http"
	"sort"
)

func (h *openCodeHTTP) v2DefaultModel(ctx context.Context) (map[string]any, error) {
	var entries []map[string]any
	if err := h.json(ctx, http.MethodGet, "/api/config", nil, &entries); err != nil {
		return nil, err
	}
	agent := "build"
	for _, entry := range entries {
		if value := str(object(entry["info"]), "default_agent"); value != "" {
			agent = value
		}
	}
	var agents []map[string]any
	if err := h.json(ctx, http.MethodGet, "/api/agent", nil, &agents); err != nil {
		return nil, err
	}
	// Configuration discovery starts plugin activation. Model reads await it;
	// provider reads alone can still return the empty boot-time snapshot.
	var selected map[string]any
	if err := h.json(ctx, http.MethodGet, "/api/model/default", nil, &selected); err != nil {
		return nil, err
	}
	ref := map[string]any{"providerID": selected["providerID"], "id": selected["id"]}
	for _, item := range agents {
		if str(item, "id") == agent && object(item["model"]) != nil {
			ref = object(item["model"])
		}
	}
	return ref, nil
}

func (h *openCodeHTTP) v2Catalog(ctx context.Context) (*ModelCatalog, error) {
	selected, err := h.v2DefaultModel(ctx)
	if err != nil {
		return nil, err
	}
	var providers, models, integrations []map[string]any
	for _, request := range []struct {
		path   string
		result any
	}{
		{"/api/model", &models}, {"/api/provider", &providers}, {"/api/integration", &integrations},
	} {
		if err := h.json(ctx, http.MethodGet, request.path, nil, request.result); err != nil {
			return nil, err
		}
	}
	c := &ModelCatalog{Models: []ModelOption{}, EffortLabel: "Variant", ConnectedProviders: []ConnectedProvider{}, providerOptions: map[string]map[string]any{}}
	if str(selected, "id") != "" {
		c.DefaultModel = str(selected, "providerID") + "/" + str(selected, "id")
	}
	c.DefaultEffort = str(selected, "variant")
	names := map[string]string{}
	for _, provider := range providers {
		id := str(provider, "id")
		names[id] = str(provider, "name")
		authType := "configured"
		for _, integration := range integrations {
			if str(integration, "id") != str(provider, "integrationID") {
				continue
			}
			connections, _ := integration["connections"].([]any)
			for _, value := range connections {
				connection := object(value)
				if object(connection["status"]) != nil {
					continue
				}
				authType = str(connection, "method")
				if str(connection, "type") == "env" {
					authType = "apiKey"
				}
			}
		}
		if authType == "" || authType == "credential" {
			authType = "configured"
		}
		if authType == "key" {
			authType = "apiKey"
		}
		c.ConnectedProviders = append(c.ConnectedProviders, ConnectedProvider{ID: id, Name: names[id], AuthType: authType})
		c.providerOptions[id] = object(provider["settings"])
	}
	for _, model := range models {
		if !truth(model, "enabled") {
			continue
		}
		id, provider := str(model, "id"), str(model, "providerID")
		efforts := []string{}
		variants, _ := model["variants"].([]any)
		for _, value := range variants {
			efforts = append(efforts, str(object(value), "id"))
		}
		sort.Strings(efforts)
		settings := object(model["settings"])
		c.Models = append(c.Models, ModelOption{ID: provider + "/" + id, Name: str(model, "name"), Provider: provider, ProviderName: names[provider], Efforts: efforts, endpoint: str(settings, "baseURL"), customAuth: hasAuthHeaders(object(model["headers"])) || settings["apiKey"] != nil})
	}
	return c, nil
}
