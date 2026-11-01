package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

// CP1a v2 stores may already contain global definitions. Backfill ONLY the new
// mutable reference projections once, in the normal authority transaction after
// schema/manifest verification; no history/snapshot/receipt is rebuilt. The next
// user_version upgrade remains reserved for the coordinated B domain handoff.
func (s *sqliteStore) upgradeAgentReferenceIndexes(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var done string
		err := tx.QueryRowContext(ctx, `SELECT value FROM store_meta WHERE key='agent_binding_reference_version'`).Scan(&done)
		if err == nil && done == "1" {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		for _, table := range []string{"global_agents", "agent_bindings"} {
			after := ""
			for {
				ids, err := sqliteQueryIDs(ctx, tx, "SELECT id FROM data_"+table+" WHERE id>? ORDER BY id LIMIT 100", after)
				if err != nil {
					return err
				}
				if len(ids) == 0 {
					break
				}
				for _, id := range ids {
					var raw json.RawMessage
					if err := decodeSQLiteRecord(ctx, tx, table, id, 8<<20, &raw); err != nil {
						return err
					}
					if err := refreshSQLiteLibraryReferences(ctx, tx, table, id, raw); err != nil {
						return err
					}
					after = id
				}
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO store_meta(key,value) VALUES('agent_binding_reference_version','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value`)
		return err
	})
}

func validateAgentSourceRecord(source AgentSourceRecord) error {
	if !validAgentSourceUID(source.Source.SourceUID) || !validAuthoringID(source.Source.ID) || source.ID != agentSourceRecordID(source.Source, source.Path) {
		return errors.New("Invalid persisted agent source identity.")
	}
	switch source.Source.Scope {
	case "global":
		if source.Source.ProjectID != "" || source.Path != "" {
			return errors.New("Invalid global agent source scope.")
		}
	case "project":
		if source.Source.ProjectID == "" || source.Path != agentFile(source.Source.ID) {
			return errors.New("Invalid project agent source scope.")
		}
	default:
		return errors.New("Invalid agent source scope.")
	}
	switch source.State {
	case "linked", "disabled", "deleted", "source_unavailable", "source_invalid", "conflict":
		return nil
	}
	return errors.New("Invalid persisted agent source lifecycle state.")
}

// Optional storage foundation only; runtime callers must obtain a fully validated
// resolution and an explicitly released admission path before writing a binding.
func (st *sqliteSessionTx) putAgentBinding(id string, binding *SessionAgentBinding) error {
	if _, err := st.sessionMeta(id); err != nil {
		return err
	}
	if err := validateAgentBinding(binding); err != nil {
		return err
	}
	if binding != nil {
		if _, err := strconv.ParseUint(binding.Revision, 10, 64); err != nil {
			return errors.New("Invalid agent binding revision.")
		}
	}
	return st.putSingleChild("agent_bindings", id, binding)
}
