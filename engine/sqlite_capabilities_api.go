package main

import (
	"database/sql"
	"net/http"
	"strconv"
)

func (a *app) sqlConversationCapabilities(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var expected string
	var selection CapabilitySelection
	if r.Method != http.MethodGet {
		var body struct {
			Revision  string              `json:"revision"`
			Selection CapabilitySelection `json:"selection"`
		}
		if !decode(w, r, &body) {
			return
		}
		expected, selection = body.Revision, body.Selection
		if _, err := strconv.ParseUint(expected, 10, 64); err != nil {
			fail(w, 400, "Use the returned decimal selection revision.")
			return
		}
		if err := normalizeSelection(&selection); err != nil {
			fail(w, 400, err.Error())
			return
		}
		bound, err := a.agentBindingExists(r.Context(), id)
		if err != nil {
			agentHTTPError(w, err)
			return
		}
		if bound {
			err = a.patchAgentBinding(r.Context(), id, "", expected, AgentOverridePatch{Skills: agentOverrideValue[[]string]{Present: true, Value: &selection.Skills}, MCPConnections: agentOverrideValue[[]MCPSelection]{Present: true, Value: &selection.MCPConnections}}, nil)
			if err != nil {
				agentHTTPError(w, err)
				return
			}
			read := r.Clone(r.Context())
			read.Method = http.MethodGet
			a.sqlConversationCapabilities(w, read)
			return
		}
		a.libraryMu.Lock()
		a.mu.Lock()
		err = a.writeSQLLocked(r.Context(), func(st *sqliteSessionTx) error {
			s, err := st.sessionMeta(id)
			if err != nil {
				return err
			}
			if s.Role == sessionRoleSubagent || s.GraphRunID != "" {
				return controlError(404, "Conversation not found.")
			}
			if run, err := st.activeAutomation(id); err != nil {
				return err
			} else if run != nil {
				return controlError(409, "Automation run settings are read-only until the run ends.")
			}
			var pending bool
			if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&pending); err != nil {
				return err
			}
			if pending {
				return controlError(409, "A library deletion needs recovery before editing selections.")
			}
			key := sqliteOwnedKey(id, "")
			var c ConversationCapabilities
			if err := decodeSQLiteRecord(st.ctx, st.tx, "session_capabilities", key, 64<<10, &c); err != nil {
				return err
			}
			if sqliteRevision(c.Revision) != expected || c.Revision == ^uint64(0) {
				return controlError(409, "Selection changed. Reload before saving.")
			}
			c.Revision++
			if err := st.putExistingRecord("session_capabilities", key, c); err != nil {
				return err
			}
			if err := st.putSingleChild("capability_selections", key, selection); err != nil {
				return err
			}
			s.UpdatedAt = now()
			return st.putSessionMeta(s)
		})
		a.mu.Unlock()
		a.libraryMu.Unlock()
		if err != nil {
			sqlHTTPError(w, err)
			return
		}
	}
	var c ConversationCapabilities
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		s, err := a.storeSessionRead(r, tx, id)
		if err != nil {
			return err
		}
		if s.Role == sessionRoleSubagent || s.GraphRunID != "" {
			return sql.ErrNoRows
		}
		key := sqliteOwnedKey(id, "")
		if err := decodeSQLiteRecord(r.Context(), tx, "session_capabilities", key, 64<<10, &c); err != nil {
			return err
		}
		if err := decodeSQLiteRecord(r.Context(), tx, "capability_selections", sqliteOwnedKey(key, ""), 8<<20, &c.Desired); err != nil {
			return err
		}
		if err := readSQLCapabilityView(r.Context(), tx, "capability_applied", sqliteOwnedKey(key, ""), &c.Applied); err != nil {
			return err
		}
		return readSQLCapabilityView(r.Context(), tx, "capability_preparing", sqliteOwnedKey(key, ""), &c.Preparing)
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	respond(w, 200, map[string]any{"revision": sqliteRevision(c.Revision), "desired": c.Desired, "applied": c.Applied, "preparing": c.Preparing})
}

func (a *app) storeSessionRead(r *http.Request, tx *sql.Tx, id string) (Session, error) {
	var s Session
	err := decodeSQLiteRecord(r.Context(), tx, "sessions", id, 1<<20, &s)
	return s, err
}
