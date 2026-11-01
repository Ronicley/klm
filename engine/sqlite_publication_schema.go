package main

import (
	"context"
	"errors"
	"fmt"
)

// Check layout before any upgrade. The independently developed agents source
// reused version numbers 2/3; those unpublished databases must not be mistaken
// for this stack's content-v2/catalog-v3/delegation-v4 sequence.
func (s *sqliteStore) validatePublicationSchemaLayout(ctx context.Context) error {
	var version int
	if err := s.writer.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	groups := [][]string{
		{"event_content_generations", "event_content_generation_chunks", "event_content_publications", "event_content_appends"},
		{"data_global_agents", "data_agent_sources", "data_agent_catalog_changes", "data_agent_bindings"},
		{"data_delegation_batches", "data_delegation_calls", "data_delegation_invocations", "data_delegation_children"},
	}
	counts := make([]int, len(groups))
	for i, tables := range groups {
		for _, table := range tables {
			var present int
			if err := s.writer.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&present); err != nil {
				return err
			}
			counts[i] += present
		}
	}
	if (version == 2 || version == 3) && counts[0] != len(groups[0]) && (counts[1] > 0 || counts[2] > 0) {
		return errors.New("Unpublished standalone agents schema/layout; explicit migration required. Database was not changed.")
	}
	if version < 1 || version > sqliteSchemaVersion {
		return errors.New("Unsupported SQLite publication schema.")
	}
	for i, tables := range groups {
		expected := 0
		if version >= i+2 {
			expected = len(tables)
		}
		if counts[i] != expected {
			return fmt.Errorf("SQLite publication schema %d has an incompatible table layout; explicit migration required.", version)
		}
	}
	return nil
}
