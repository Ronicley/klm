package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Review regression: a copied source UID is not an unambiguous lifetime, even
// when its public slug differs. No providers or real engine data are involved.
func TestAgentCatalogManagerRejectsDuplicateLifetimeMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := t.TempDir()
	store, err := createSQLiteStore(ctx, filepath.Join(dir, "fixture.sqlite3"), "manager-agent-identity-probe")
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	project := Project{ID: "identity-project", Name: "Identity fixture", Folder: dir}
	if _, err := store.sessionTransaction(ctx, func(st *sqliteSessionTx) error {
		_, _, err := st.registerProject(project, false, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.openReaders(ctx, filepath.Join(dir, "fixture.sqlite3")); err != nil {
		t.Fatal(err)
	}
	a := &app{dir: dir, ctx: ctx, store: store, sqlChanges: &sqliteChangeBus{}}
	uid := newID()
	for _, name := range []string{"First identity", "Copied identity"} {
		body, err := toml.Marshal(agentDocument{SourceUID: uid, AgentDefinition: AgentDefinition{
			Name: name, Enabled: true, DefaultHarness: "opencode", Model: "fixture/model", Prompt: "Fixture only.",
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err := writeConfig(project, agentFile(authoringSlug(name)), body); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := a.agentCatalog(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Agents) != 2 {
		t.Fatalf("expected two conflicted identities, got %d", len(catalog.Agents))
	}
	for _, row := range catalog.Agents {
		if row.Available || row.Status != "conflict" {
			t.Fatalf("expected unavailable conflict: %+v", row.Source)
		}
	}
	row := catalog.Agents[0]
	definition := row.AgentDefinition
	definition.Prompt = "Changed prompt."
	a.authoringMu.Lock()
	_, err = a.mutateCatalogAgentLocked(ctx, project, row.ID, saveCatalogAgentBody{Agent: definition, SourceUID: uid, Revision: row.Revision}, false)
	a.authoringMu.Unlock()
	var problem *AgentConfigProblem
	if !errors.As(err, &problem) || problem.status != 409 {
		t.Fatalf("ambiguous lifetime mutation must reject with 409; got %v", err)
	}
	for _, remove := range []bool{false, true} {
		definition.Name = "Renamed identity"
		a.authoringMu.Lock()
		_, err = a.mutateCatalogAgentLocked(ctx, project, row.ID, saveCatalogAgentBody{Agent: definition, SourceUID: uid, Revision: row.Revision}, remove)
		a.authoringMu.Unlock()
		if !errors.As(err, &problem) || problem.status != 409 {
			t.Fatalf("ambiguous lifetime rename/delete must reject with 409; remove=%v err=%v", remove, err)
		}
	}
	for _, original := range catalog.Agents {
		data, err := readConfig(project, agentFile(original.ID))
		if err != nil || revision(data) != original.Revision {
			t.Fatalf("rejected lifetime mutation changed an authoring source: %v", err)
		}
	}
}

func TestAgentCatalogManagerValidatesEffectiveSelectionAfterOverrides(t *testing.T) {
	base := ResolvedAgentConfig{Harness: "opencode", Model: "fixture/model", Selection: CapabilitySelection{}}
	inherited := CapabilitySelection{}
	for i := 0; i < 100; i++ {
		base.Selection.Skills = append(base.Selection.Skills, fmt.Sprintf("source-%d", i))
		inherited.Skills = append(inherited.Skills, fmt.Sprintf("inherited-%d", i))
	}
	empty := []string{}
	resolved, err := resolveAgentConfig(base, "opencode", AgentConfigOverrides{Skills: &empty}, inherited)
	if err != nil {
		t.Fatalf("an explicit empty override must replace the oversized intermediate union: %v", err)
	}
	if len(resolved.Selection.Skills) != 0 {
		t.Fatal("explicit empty override was lost")
	}
	if _, err := resolveAgentConfig(base, "opencode", AgentConfigOverrides{}, inherited); err == nil {
		t.Fatal("unoverridden skill union overflow was ignored")
	}
	for i := 0; i < 32; i++ {
		base.Selection.MCPConnections = append(base.Selection.MCPConnections, MCPSelection{ConnectionID: fmt.Sprintf("%032x", i+1), Tools: []string{"fixture"}})
		inherited.MCPConnections = append(inherited.MCPConnections, MCPSelection{ConnectionID: fmt.Sprintf("%032x", i+33), Tools: []string{"fixture"}})
	}
	emptyMCPs := []MCPSelection{}
	if _, err := resolveAgentConfig(base, "opencode", AgentConfigOverrides{Skills: &empty}, inherited); err == nil {
		t.Fatal("skills override incorrectly hid unoverridden MCP union overflow")
	}
	if _, err := resolveAgentConfig(base, "opencode", AgentConfigOverrides{MCPConnections: &emptyMCPs}, inherited); err == nil {
		t.Fatal("MCP override incorrectly hid unoverridden skill union overflow")
	}
	resolved, err = resolveAgentConfig(base, "opencode", AgentConfigOverrides{Skills: &empty, MCPConnections: &emptyMCPs}, inherited)
	if err != nil || len(resolved.Selection.Skills) != 0 || len(resolved.Selection.MCPConnections) != 0 {
		t.Fatalf("independent empty overrides did not replace both oversized unions: %v", err)
	}
}
