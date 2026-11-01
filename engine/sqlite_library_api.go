package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func (a *app) getSQLMCPConnection(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	c, exists, err := a.mcpConnectionLocked(r.Context(), r.PathValue("connectionID"))
	a.mu.Unlock()
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	if !exists {
		fail(w, 404, "Connection not found.")
		return
	}
	respond(w, 200, publicMCPConnection(c))
}
func (a *app) getSQLLibrary(w http.ResponseWriter, r *http.Request) {
	search := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	if len(search) > 800 {
		fail(w, 400, "Library search is too long.")
		return
	}
	offset, err := sqlOffset(r)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	limit, err := sqlPageLimit(r, 50)
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	skillsOffset := 0
	if raw := r.URL.Query().Get("skillsOffset"); raw != "" {
		skillsOffset, err = strconv.Atoi(raw)
		if err != nil || skillsOffset < 0 {
			fail(w, 400, "Invalid skills offset.")
			return
		}
	}
	a.libraryMu.Lock()
	defer a.libraryMu.Unlock()
	root, skills, err := scanSkillLibrary()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if search != "" {
		matches := skills[:0]
		for _, skill := range skills {
			if strings.Contains(strings.ToLower(skill.ID+"\n"+skill.Name+"\n"+skill.Description), search) {
				matches = append(matches, skill)
			}
		}
		skills = matches
	}
	start := min(skillsOffset, len(skills))
	end := min(start+limit, len(skills))
	skillPage := skills[start:end]
	connections := []map[string]any{}
	more, pending := false, false
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&pending); err != nil {
			return err
		}
		// There are at most 64 configured connections. Match before pagination;
		// historical captures are not consulted or hydrated for Library search.
		ids, err := sqliteQueryIDs(r.Context(), tx, `SELECT id FROM data_mcp_connections ORDER BY position,id`)
		if err != nil {
			return err
		}
		matched := 0
		budget := 2 << 20
		for _, id := range ids {
			var c MCPConnection
			if err := decodeSQLiteRecord(r.Context(), tx, "mcp_connections", id, 8<<20, &c); err != nil {
				return err
			}
			matches := search == "" || strings.Contains(strings.ToLower(c.ID+"\n"+c.Name), search)
			if !matches && c.Catalog != nil {
				for _, tool := range c.Catalog.Tools {
					if tool != nil && strings.Contains(strings.ToLower(tool.Name+"\n"+tool.Description), search) {
						matches = true
						break
					}
				}
			}
			if !matches {
				continue
			}
			matched++
			if matched <= offset {
				continue
			}
			if len(connections) == limit {
				more = true
				break
			}
			view := publicMCPConnection(c)
			if c.Catalog != nil {
				view["catalog"] = map[string]any{"revision": c.Catalog.Revision, "resources": c.Catalog.Resources, "toolCount": len(c.Catalog.Tools)}
				view["detailRequired"] = true
			}
			encoded, _ := json.Marshal(view)
			if len(encoded) > budget && len(connections) > 0 {
				more = true
				break
			}
			budget -= len(encoded)
			connections = append(connections, view)
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	a.mu.Lock()
	pi, p := a.binaries["pi"]
	oc, o := a.binaries["opencode"]
	codex, c := a.binaries["codex"]
	a.mu.Unlock()
	harnesses := map[string]any{}
	for id, supported := range map[string]bool{"pi": p && validatePiLibraryBinary(pi) == nil, "opencode": o && validateOpenCodeLibraryBinary(oc) == nil, "codex": c && validateCodexLibraryBinary(codex) == nil} {
		status := "not_validated"
		if supported {
			status = "supported"
		}
		harnesses[id] = map[string]any{"managed": supported, "status": status}
	}
	respond(w, 200, map[string]any{"skillsRoot": root, "skills": skillPage, "skillsHasMore": end < len(skills), "skillsNextOffset": end, "skillsWindowStart": start, "search": search, "connections": connections, "hasMore": more, "nextOffset": offset + len(connections), "recoveryPending": pending, "harnesses": harnesses})
}
