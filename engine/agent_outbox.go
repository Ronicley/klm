package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type AgentAuthoringWrite struct {
	Project  Project           `json:"project"`
	Expected map[string]string `json:"expected"`
	Writes   map[string][]byte `json:"writes"`
}

type AgentCatalogChange struct {
	ID           string                `json:"id"`
	Files        []AgentAuthoringWrite `json:"files"`
	Global       *GlobalAgentRecord    `json:"global,omitempty"`
	RemoveGlobal string                `json:"removeGlobal,omitempty"`
	Sources      []AgentSourceRecord   `json:"sources"`
}

func validateAgentCatalogChange(change AgentCatalogChange) error {
	if !validAgentSourceUID(change.ID) {
		return errors.New("Invalid catalog transaction identity.")
	}
	if change.Global != nil && (!validAgentSourceUID(change.Global.ID) || !validAuthoringID(change.Global.Slug) || change.RemoveGlobal != "") {
		return errors.New("Invalid global agent transaction.")
	}
	for _, root := range change.Files {
		if root.Project.ID == "" || root.Project.Folder == "" || len(root.Expected) != len(root.Writes) {
			return errors.New("Invalid catalog authoring root.")
		}
		for path := range root.Writes {
			if !(strings.HasPrefix(path, ".klm/agents/") && strings.HasSuffix(path, ".toml") || strings.HasPrefix(path, ".klm/graphs/") && strings.HasSuffix(path, ".yaml")) {
				return errors.New("Invalid catalog authoring path.")
			}
			if _, exists := root.Expected[path]; !exists {
				return errors.New("Missing catalog write precondition.")
			}
			if _, err := configPath(root.Project, path, false); err != nil {
				return err
			}
		}
	}
	for _, source := range change.Sources {
		if err := validateAgentSourceRecord(source); err != nil {
			return err
		}
	}
	return nil
}

func applyAgentCatalogFiles(change AgentCatalogChange) error {
	if err := validateAgentCatalogChange(change); err != nil {
		return err
	}
	for _, root := range change.Files {
		if err := recoverAuthoring(root.Project); err != nil {
			return err
		}
		for path, expected := range root.Expected {
			data, err := readConfig(root.Project, path)
			current := ""
			if err == nil {
				current = revision(data)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			wanted := ""
			if root.Writes[path] != nil {
				wanted = revision(root.Writes[path])
			}
			if current != expected && current != wanted {
				return fmt.Errorf("Agent catalog file %s changed externally; restore the expected revision before recovery.", path)
			}
		}
		if err := commitAuthoring(root.Project, root.Writes); err != nil {
			return err
		}
	}
	return nil
}

func (st *sqliteSessionTx) finishAgentCatalogChange(change AgentCatalogChange) error {
	if change.Global != nil {
		if err := st.putRoot("global_agents", change.Global.ID, *change.Global); err != nil {
			return err
		}
	}
	if change.RemoveGlobal != "" {
		if err := st.deleteRecord("global_agents", change.RemoveGlobal); err != nil {
			return err
		}
	}
	for _, source := range change.Sources {
		if err := st.putRoot("agent_sources", source.ID, source); err != nil {
			return err
		}
		if source.Source.ProjectID != "" {
			st.changes.Projects[source.Source.ProjectID] = true
		}
	}
	for _, root := range change.Files {
		st.changes.Projects[root.Project.ID] = true
	}
	return st.deleteRecord("agent_catalog_changes", change.ID)
}

// Runs only after storage authority + schema upgrade are confirmed, before any
// workers/readiness. It rolls forward configuration bytes, never agent execution.
func (s *sqliteStore) recoverAgentCatalogChanges(ctx context.Context) error {
	for {
		var id string
		err := s.writer.QueryRowContext(ctx, `SELECT id FROM data_agent_catalog_changes ORDER BY position LIMIT 1`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var change AgentCatalogChange
		if err := decodeSQLiteRecord(ctx, s.writer, "agent_catalog_changes", id, 8<<20, &change); err != nil {
			return err
		}
		if err := applyAgentCatalogFiles(change); err != nil {
			return err
		}
		if _, err := s.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.finishAgentCatalogChange(change) }); err != nil {
			return err
		}
	}
}

// authoringMu is held, never app.mu during filesystem work. An accepted intent
// owns its paths until roll-forward is complete; uncertain writes freeze storage
// just like existing graph-authoring recovery, preventing conflicting mutations.
func (a *app) commitAgentCatalogChange(ctx context.Context, change AgentCatalogChange) error {
	if err := validateAgentCatalogChange(change); err != nil {
		return err
	}
	// Preflight every root before reserving an intent or writing the first root.
	// External ACL changes may still make a later write fail; the outbox owns that
	// uncertainty and never treats it as a successfully completed mutation.
	for _, root := range change.Files {
		for path, expected := range root.Expected {
			data, err := readConfig(root.Project, path)
			current := ""
			if err == nil {
				current = revision(data)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if current != expected {
				return agentProblem(409, "source_changed", "revision", "Configuration changed during catalog preflight. Reload before saving.")
			}
			absolute, err := configPath(root.Project, path, false)
			if err != nil {
				return err
			}
			if info, err := os.Stat(absolute); err == nil && info.Mode().Perm()&0222 == 0 {
				return agentProblem(409, "source_read_only", "agent", "The agent or graph source is read-only. Restore write access before saving.")
			}
		}
	}
	raw, err := json.Marshal(change)
	if err != nil {
		return err
	}
	if len(raw) > 8<<20 {
		return agentProblem(409, "change_too_large", "agent", "The catalog change exceeds the bounded transaction size.")
	}
	a.mu.Lock()
	err = a.writeSQLLocked(ctx, func(st *sqliteSessionTx) error {
		for _, root := range change.Files {
			p, err := st.project(root.Project.ID)
			if err != nil {
				return err
			}
			if !sameProjectFolder(p.Folder, root.Project.Folder) {
				return errors.New("Catalog project changed before saving.")
			}
		}
		return st.putRoot("agent_catalog_changes", change.ID, change)
	})
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if err := applyAgentCatalogFiles(change); err != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.failStorageLocked(fmt.Errorf("Agent catalog recovery is required before further writes: %w", err))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// Once the intent is durable, finish it using the engine lifetime rather than
	// a disconnected HTTP request. Startup can recover if shutdown interrupts it.
	finishCtx := a.ctx
	if finishCtx == nil {
		finishCtx = context.Background()
	}
	finishCtx, cancel := context.WithTimeout(finishCtx, 15*time.Second)
	defer cancel()
	return a.writeSQLLocked(finishCtx, func(st *sqliteSessionTx) error { return st.finishAgentCatalogChange(change) })
}
