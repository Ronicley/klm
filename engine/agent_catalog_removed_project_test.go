package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func TestAgentCatalogRemovedProjectUnavailablePreservesClaims(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.sqlite3")
	store, err := createSQLiteStore(ctx, path, "removed-project-fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	if err := store.openReaders(ctx, path); err != nil {
		t.Fatal(err)
	}
	a := &app{dir: dir, ctx: ctx, store: store, sqlChanges: &sqliteChangeBus{}}
	active := Project{ID: "active", Name: "Active", Folder: t.TempDir()}
	removed := Project{ID: "removed", Name: "Old validation", Folder: t.TempDir()}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		for _, p := range []Project{active, removed} {
			if _, _, err := st.registerProject(p, false, ""); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	uid := newID()
	definition := AgentDefinition{Name: "Reserved specialist", Enabled: true, DefaultHarness: "opencode", Model: "fixture/model", Prompt: "Fixture only."}
	body, err := toml.Marshal(agentDocument{SourceUID: uid, AgentDefinition: definition})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeConfig(removed, agentFile("reserved-specialist"), body); err != nil {
		t.Fatal(err)
	}
	if _, err := a.agentCatalog(ctx, removed.ID); err != nil {
		t.Fatal(err)
	}
	removed.Removed = true
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error { return st.putRoot("projects", removed.ID, removed) }); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(removed.Folder); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"", active.ID} {
		a.authoringMu.Lock()
		scan, err := a.scanAgentCatalogLocked(ctx, scope)
		a.authoringMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if !scan.Complete || !scan.referencesComplete || len(scan.Diagnostics) != 0 {
			t.Fatalf("removed unavailable project poisoned scope %q: %+v", scope, scan.Diagnostics)
		}
		if len(scan.Agents) != 0 {
			t.Fatal("removed specialist leaked into the visible catalog")
		}
		ref := AgentSourceRef{SourceUID: uid, ID: "reserved-specialist", Scope: "project", ProjectID: removed.ID}
		record := scan.sources[agentSourceRecordID(ref, agentFile(ref.ID))]
		if record.Source.SourceUID != uid || record.State != "source_unavailable" {
			t.Fatal("removed source reservation was lost or relabelled as deleted", record)
		}
	}
	a.authoringMu.Lock()
	_, err = a.mutateCatalogAgentLocked(ctx, Project{}, "", saveCatalogAgentBody{Agent: definition}, false)
	a.authoringMu.Unlock()
	if err == nil {
		t.Fatal("removed source's reserved slug was reused")
	}
	definition.Name = "Unrelated specialist"
	a.authoringMu.Lock()
	_, err = a.mutateCatalogAgentLocked(ctx, Project{}, "", saveCatalogAgentBody{Agent: definition}, false)
	a.authoringMu.Unlock()
	if err != nil {
		t.Fatal("unrelated global creation remained blocked", err)
	}
}
