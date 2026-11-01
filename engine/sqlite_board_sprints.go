package main

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (a *app) sqlBoardSprints(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(100 * time.Second))
	project := r.URL.Query().Get("projectId")
	choices := map[string]activityBoardSprint{}
	sources := []boardSprintSource{}
	seen := map[string]bool{}
	needsRefresh := false
	var revision, projection uint64
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		if project != "" {
			available, err := st.projectAvailable(project)
			if err != nil {
				return err
			}
			if !available {
				return sql.ErrNoRows
			}
		}
		config, err := st.boardConfig()
		if err != nil {
			return err
		}
		revision, projection = config.Revision, config.Projection
		return st.eachMatching("board_connections", `x_provider='azure' AND (?='' OR project_id=?)`, []any{project, project}, func(id string) error {
			c, err := st.boardConnection(id)
			if err != nil {
				return err
			}
			available, err := st.projectAvailable(c.ProjectID)
			if err != nil {
				return err
			}
			if !available {
				return nil
			}
			sync, err := st.boardSync(id)
			if err != nil {
				return err
			}
			key := c.Scope.OrganizationID + ":" + c.Scope.ProjectID
			if !seen[key] {
				seen[key] = true
				var blob []byte
				if err := decodeSQLiteRecord(st.ctx, tx, "board_credentials", id, 1<<20, &blob); err != nil {
					return err
				}
				sources = append(sources, boardSprintSource{connection: c, credential: blob})
			}
			if sync.SnapshotPending || !c.OnlyAssignedToMe {
				needsRefresh = true
				return nil
			}
			paths, err := sqliteQueryIDs(st.ctx, tx, `SELECT DISTINCT x_iterationPath FROM data_board_items WHERE x_connectionId=? ORDER BY x_iterationPath`, id)
			if err != nil {
				return err
			}
			for _, path := range paths {
				if path == "" {
					needsRefresh = true
					continue
				}
				s := boardSprintFromPath(c, path)
				choices[s.ID] = s
			}
			return nil
		})
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	if needsRefresh {
		choices = map[string]activityBoardSprint{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	currentError := ""
	for _, source := range sources {
		token, err := boardCredential(source.credential, false)
		var current []activityBoardSprint
		if err == nil {
			current, err = boardAzureCurrentSprints(ctx, source.connection, string(token))
		}
		if err != nil {
			if currentError == "" {
				currentError = "Could not identify the current sprint for " + source.connection.Scope.Organization + " / " + source.connection.Scope.Project + ". " + err.Error()
			}
			continue
		}
		for _, s := range current {
			choices[s.ID] = s
		}
	}
	err = a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: r.Context(), tx: tx}
		config, err := st.boardConfig()
		if err != nil {
			return err
		}
		if config.Revision != revision || config.Projection != projection {
			return controlError(409, "Tasks changed. Retry loading sprints.")
		}
		for _, source := range sources {
			available, err := st.projectAvailable(source.connection.ProjectID)
			if err != nil {
				return err
			}
			if !available {
				return controlError(409, "Tasks changed. Retry loading sprints.")
			}
		}
		return nil
	})
	if err != nil {
		sqlHTTPError(w, err)
		return
	}
	items := []activityBoardSprint{}
	for _, s := range choices {
		items = append(items, s)
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Current != b.Current {
			return a.Current
		}
		l, r := strings.ToLower(a.Organization+"/"+a.Project+"/"+a.Path), strings.ToLower(b.Organization+"/"+b.Project+"/"+b.Path)
		if l == r {
			return a.ID < b.ID
		}
		return l < r
	})
	respond(w, 200, map[string]any{"items": items, "needsRefresh": needsRefresh, "currentError": currentError})
}
