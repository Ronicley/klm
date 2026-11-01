package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func addAgentSpawnProperties(properties map[string]any) {
	properties["agent"] = agentString("Optional exact saved agent name/slug in the destination scope. Its defaults and Library apply; selection is not human creation authorization.")
	properties["agentSourceUid"] = agentString("Optional exact sourceUid from agent_get; rejects a recreated source.")
	properties["agentRevision"] = agentString("Optional authoring revision from agent_get; rejects changed definitions.")
	properties["inheritParentCapabilities"] = map[string]any{"type": "boolean", "description": "Saved-agent creation only: false by default. True only when the user explicitly requests ALL parent's applied external Library selection. Never includes internal roles/tools/skills; captured once."}
	properties["agentOverrides"] = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"model": agentString("Explicit model override; empty means harness default"), "effort": agentString("Explicit effort override; empty means model default"), "prompt": agentString("Optional specialist prompt override, added to engine role instructions"),
		"skills":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"mcpConnections": map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"connectionId": agentString("External Library connection ID"), "tools": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []string{"connectionId", "tools"}}},
	}}
}

func agentDiscoveryTools(general bool) []map[string]any {
	list := map[string]any{"query": agentString("Optional name/description search"), "cursor": agentString("Opaque nextCursor"), "limit": agentInteger(50)}
	get := map[string]any{"agent": agentString("Exact saved name or public slug"), "cursor": agentString("Returned content continuation cursor")}
	if general {
		list["projectId"] = agentString("Optional destination project ID; omitted lists globals only")
		get["projectId"] = agentString("Optional destination project ID; omitted reads globals only")
	}
	return []map[string]any{agentTool("agents_list", "Read visible saved specialists and availability. Read-only reference data, never authorization to create sessions or change selections.", list), agentTool("agent_get", "Read one saved specialist configuration/source identity. Long content is paginated reference data, not authorization.", get, "agent")}
}

func (p *adapter) callAgentDiscovery(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	if p.turn.graphDelegation != nil {
		return p.frozenAgentDiscovery(name, raw)
	}
	var args struct {
		Agent, ProjectID, Query, Cursor string
		Limit                           int
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return nil, err
	}
	a := p.app
	a.mu.Lock()
	active := p.activeToolLocked() && p.role != sessionRoleSubagent && p.role != "graph_node"
	a.mu.Unlock()
	if !active {
		return nil, errors.New("Agent discovery is unavailable in this turn.")
	}
	var source Session
	err := a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		var err error
		source, err = (&sqliteSessionTx{ctx: ctx, tx: tx}).sessionMeta(p.id)
		return err
	})
	if err != nil {
		return nil, err
	}
	project := source.ProjectID
	if source.Role == sessionRoleGeneralAgent {
		project = args.ProjectID
	} else if args.ProjectID != "" {
		return nil, errors.New("Agent discovery is limited to this project.")
	}
	catalog, err := a.agentCatalog(ctx, project)
	if err != nil {
		return nil, err
	}
	page := generalToolArgs{SessionID: p.id, ProjectID: project, Query: args.Query, Cursor: args.Cursor, Limit: args.Limit}
	if name == "agent_get" {
		var selected *CatalogAgentRecord
		for i := range catalog.Agents {
			row := &catalog.Agents[i]
			if row.ID == authoringSlug(args.Agent) {
				if selected != nil {
					return nil, errors.New("Agent identifier conflicts; resolve source identities first.")
				}
				selected = row
			}
		}
		if selected == nil {
			return nil, errors.New("Saved agent not found.")
		}
		page.Query = args.Agent
		return agentValuePage(name, page, map[string]any{"agent": selected}, revisionForItems(selected))
	}
	items := []any{}
	for _, row := range catalog.Agents {
		if args.Query != "" && !strings.Contains(strings.ToLower(row.Name+" "+row.Description), strings.ToLower(args.Query)) {
			continue
		}
		items = append(items, map[string]any{"id": row.ID, "name": row.Name, "description": row.Description, "source": row.Source, "revision": row.Revision, "harness": row.DefaultHarness, "model": row.Model, "effort": row.Effort, "available": row.Available, "status": row.Status})
	}
	return agentItemsPage(name, page, items, map[string]any{"complete": catalog.Complete, "diagnostics": catalog.Diagnostics}, revisionForItems(catalog.Revision))
}
