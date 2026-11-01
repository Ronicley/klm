package main

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Maintained only when the owning current selection or immutable capture is
// written/imported. Deletion/recovery never opens historical captured payloads.
func refreshSQLiteLibraryReferences(ctx context.Context, tx *sql.Tx, table, id string, raw []byte) error {
	if table == "capability_applied" || table == "capability_preparing" {
		var view *struct {
			Version     int                 `json:"version"`
			Revision    string              `json:"revision"`
			Selection   CapabilitySelection `json:"selection"`
			Adapter     string              `json:"adapter"`
			Diagnostics []string            `json:"diagnostics"`
		}
		if err := json.Unmarshal(raw, &view); err != nil {
			return err
		}
		if view != nil {
			if len(view.Diagnostics) > 100 {
				view.Diagnostics = view.Diagnostics[:100]
			}
			for i := range view.Diagnostics {
				view.Diagnostics[i] = boundedText(view.Diagnostics[i], 2048)
			}
		}
		encoded, err := json.Marshal(view)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO capability_views(domain,record_key,payload) VALUES(?,?,?) ON CONFLICT(domain,record_key) DO UPDATE SET payload=excluded.payload`, table, id, encoded); err != nil {
			return err
		}
	}
	if table == "capability_selections" || table == "automations" || table == "global_agents" || table == "agent_bindings" {
		var selection CapabilitySelection
		if table == "global_agents" {
			var agent GlobalAgentRecord
			if err := json.Unmarshal(raw, &agent); err != nil {
				return err
			}
			selection = agentConfig(agent.Definition).Selection
		} else if table == "agent_bindings" {
			var binding *SessionAgentBinding
			if err := json.Unmarshal(raw, &binding); err != nil {
				return err
			}
			if binding != nil {
				for _, part := range []CapabilitySelection{binding.LastCaptured.Config.Selection, binding.Desired.Selection, binding.InheritedCapabilities} {
					selection.Skills = append(selection.Skills, part.Skills...)
					selection.MCPConnections = append(selection.MCPConnections, part.MCPConnections...)
				}
				if binding.Overrides.Skills != nil {
					selection.Skills = append(selection.Skills, (*binding.Overrides.Skills)...)
				}
				if binding.Overrides.MCPConnections != nil {
					selection.MCPConnections = append(selection.MCPConnections, (*binding.Overrides.MCPConnections)...)
				}
			}
		} else if table == "automations" {
			var a struct {
				Selection CapabilitySelection `json:"selection"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return err
			}
			selection = a.Selection
		} else if err := json.Unmarshal(raw, &selection); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM selection_references WHERE domain=? AND record_key=?`, table, id); err != nil {
			return err
		}
		for _, skill := range selection.Skills {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO selection_references VALUES(?,?,'skills',?)`, table, id, skill); err != nil {
				return err
			}
		}
		for _, connection := range selection.MCPConnections {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO selection_references VALUES(?,?,'mcp',?)`, table, id, connection.ConnectionID); err != nil {
				return err
			}
		}
	}
	var credentials []string
	links := map[string][]string{}
	type reference struct{ kind, item string }
	refs := []reference{}
	type capture struct {
		Selection   CapabilitySelection `json:"selection"`
		Connections []struct {
			ID           string `json:"id"`
			CredentialID string `json:"credentialId"`
		} `json:"connections"`
	}
	add := func(c *capture) {
		if c != nil {
			for _, skill := range c.Selection.Skills {
				refs = append(refs, reference{"skills", skill})
			}
			for _, connection := range c.Selection.MCPConnections {
				refs = append(refs, reference{"mcp", connection.ConnectionID})
			}
			for _, connection := range c.Connections {
				if connection.CredentialID != "" {
					credentials = append(credentials, connection.CredentialID)
					links[connection.ID] = append(links[connection.ID], connection.CredentialID)
				}
			}
		}
	}
	switch table {
	case "mcp_connections":
		var c struct {
			CredentialID string `json:"credentialId"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		if c.CredentialID != "" {
			credentials = append(credentials, c.CredentialID)
			links[id] = append(links[id], c.CredentialID)
		}
	case "capability_applied", "capability_preparing":
		var c *capture
		if err := json.Unmarshal(raw, &c); err != nil {
			return err
		}
		add(c)
	case "automation_runs":
		var r struct {
			Capabilities *capture `json:"capabilities"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		add(r.Capabilities)
	case "delegation_invocations":
		var invocation struct {
			Capture struct {
				ExternalCapabilities *capture `json:"externalCapabilities"`
			} `json:"capture"`
		}
		if err := json.Unmarshal(raw, &invocation); err != nil {
			return err
		}
		add(invocation.Capture.ExternalCapabilities)
	case "graph_snapshots":
		var s struct {
			Capabilities      map[string]*capture `json:"capabilities"`
			DelegationCatalog *struct {
				Agents map[string]struct {
					Execution struct {
						ExternalCapabilities *capture `json:"externalCapabilities"`
					} `json:"execution"`
				} `json:"agents"`
			} `json:"delegationCatalog"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		for _, c := range s.Capabilities {
			add(c)
		}
		if s.DelegationCatalog != nil {
			for _, agent := range s.DelegationCatalog.Agents {
				add(agent.Execution.ExternalCapabilities)
			}
		}
	default:
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM credential_references WHERE domain=? AND record_key=?`, table, id); err != nil {
		return err
	}
	for _, credential := range credentials {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO credential_references VALUES(?,?,?)`, table, id, credential); err != nil {
			return err
		}
	}
	for _, tableName := range []string{"credential_connections", "capture_references"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+tableName+` WHERE domain=? AND record_key=?`, table, id); err != nil {
			return err
		}
	}
	for connection, ids := range links {
		for _, credential := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO credential_connections VALUES(?,?,?,?)`, table, id, connection, credential); err != nil {
				return err
			}
		}
	}
	for _, ref := range refs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO capture_references VALUES(?,?,?,?)`, table, id, ref.kind, ref.item); err != nil {
			return err
		}
	}
	return nil
}

func (s legacyStage) importLibraryReferences(table, id string, node int64) error {
	// Each capture/selection is the existing bounded single-record import unit;
	// no collection/history is hydrated and references contain no secret values.
	switch table {
	case "capability_selections", "automations", "mcp_connections", "capability_applied", "capability_preparing", "automation_runs", "graph_snapshots", "delegation_invocations":
	default:
		return nil
	}
	var raw json.RawMessage
	if err := s.decodeNode(node, &raw); err != nil {
		return err
	}
	return refreshSQLiteLibraryReferences(s.ctx, s.tx, table, id, raw)
}
