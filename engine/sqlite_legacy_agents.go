package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Run after authority/import verification, before readers and workers. Keep the
// original snapshot; neither its slug nor revision proves a current source UID.
func (s *sqliteStore) migrateLegacyAgentBindings(ctx context.Context) error {
	_, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		var version string
		err := st.tx.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='legacy_agent_binding_version'`).Scan(&version)
		if err == nil {
			if version != "1" {
				return errors.New("Unsupported legacy agent binding migration version.")
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		after := ""
		for {
			ids, err := sqliteQueryIDs(ctx, st.tx, `SELECT id FROM data_sessions WHERE id>? ORDER BY id LIMIT 100`, after)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
			for _, id := range ids {
				if err := st.migrateLegacyAgentBinding(id); err != nil {
					return fmt.Errorf("Migrate legacy agent for session %s: %w", id, err)
				}
				after = id
			}
		}
		_, err = st.tx.ExecContext(ctx, `INSERT INTO store_meta(key,value) VALUES('legacy_agent_binding_version','1')`)
		return err
	})
	return err
}

func (st *sqliteSessionTx) migrateLegacyAgentBinding(id string) error {
	binding, err := st.agentBinding(id)
	if err != nil || binding != nil {
		return err
	}
	s, err := st.sessionMeta(id)
	if err != nil || s.SelectedAgent == nil {
		return err
	}
	definition := s.SelectedAgent.AgentDefinition
	if !validAuthoringID(s.SelectedAgent.ID) || s.ProjectID == "" || s.Role != "" || s.ParentID != "" {
		return errors.New("Invalid legacy selected-agent session identity.")
	}
	if err := validateAgentDefinition(&definition); err != nil {
		return err
	}
	if s.Harness != "pi" && s.Harness != "opencode" && s.Harness != "codex" {
		return errors.New("Invalid legacy selected-agent session harness.")
	}
	var selection CapabilitySelection
	if err := decodeSQLiteRecord(st.ctx, st.tx, "capability_selections", sqliteOwnedKey(sqliteOwnedKey(id, ""), ""), 8<<20, &selection); err != nil {
		return err
	}
	// Session Library settings were independent of the legacy specialist prompt.
	selection = copySelection(selection)
	overrides := AgentConfigOverrides{Model: &s.Model, Effort: &s.Effort, Skills: &selection.Skills, MCPConnections: &selection.MCPConnections}
	identity, err := json.Marshal([]any{s.ProjectID, id, s.SelectedAgent})
	if err != nil {
		return err
	}
	source := AgentSourceRef{SourceUID: "legacy-" + revision(identity), ID: s.SelectedAgent.ID, Scope: "project", ProjectID: s.ProjectID}
	capture := AgentCapture{Name: s.SelectedAgent.Name, Source: source, Revision: s.SelectedAgent.Revision, Config: agentConfig(s.SelectedAgent.AgentDefinition)}
	config, err := resolveAgentConfig(capture.Config, s.Harness, overrides, CapabilitySelection{})
	if err != nil {
		return err
	}
	res := AgentResolution{Capture: capture, Config: config, SourceStatus: "legacy_snapshot"}
	binding = bindingForResolution(res, overrides, CapabilitySelection{})
	binding.LegacySnapshot = true
	if err := st.putAgentBinding(id, binding); err != nil {
		return err
	}
	s.AgentConfig = agentSummary(*binding, res)
	return st.putSessionMeta(s)
}
