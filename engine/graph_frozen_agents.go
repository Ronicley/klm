package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/pelletier/go-toml/v2"
)

const graphFrozenAgentLimit = 256
const graphFrozenCatalogByteLimit = 16 << 20

type GraphFrozenAgent struct {
	Definition     AgentDefinition       `json:"definition"`
	Source         AgentSourceRef        `json:"source"`
	SourceRevision string                `json:"sourceRevision"`
	SourceLocator  string                `json:"sourceLocator"`
	SourceText     string                `json:"sourceText"`
	Execution      AgentExecutionCapture `json:"execution"`
}
type GraphFrozenAgentCatalog struct {
	ModelCatalogs        map[string]ModelCatalog     `json:"modelCatalogs,omitempty"`
	Agents               map[string]GraphFrozenAgent `json:"agents"`
	Complete             bool                        `json:"complete"`
	Diagnostics          []AgentDiagnostic           `json:"diagnostics"`
	DiagnosticsTruncated bool                        `json:"diagnosticsTruncated,omitempty"`
	CatalogRevision      string                      `json:"catalogRevision"`
	Digest               string                      `json:"digest"`
}

func sealGraphFrozenCatalog(catalog *GraphFrozenAgentCatalog) {
	catalog.Digest = ""
	raw, _ := json.Marshal(catalog)
	catalog.Digest = revision(raw)
}
func graphSnapshotV2Revision(snapshot GraphSnapshot) string {
	digest := ""
	if snapshot.DelegationCatalog != nil {
		digest = snapshot.DelegationCatalog.Digest
	}
	raw, _ := json.Marshal(struct {
		Version       int
		ProjectID     string
		Sources       map[string]GraphSourceSnapshot
		Globals       map[string]GraphSourceSnapshot
		Origins       map[string]AgentSourceRef
		CatalogDigest string
	}{2, snapshot.ProjectID, snapshot.Sources, snapshot.GlobalSources, snapshot.AgentOrigins, digest})
	return revision(raw)
}
func graphCapturedCapabilities(snapshot GraphSnapshot) []*CapabilitySnapshot {
	items := []*CapabilitySnapshot{}
	for _, key := range sortedGraphKeys(snapshot.Capabilities) {
		items = append(items, snapshot.Capabilities[key])
	}
	if snapshot.Version == 2 && snapshot.DelegationCatalog != nil {
		for _, id := range sortedGraphKeys(snapshot.DelegationCatalog.Agents) {
			items = append(items, snapshot.DelegationCatalog.Agents[id].Execution.ExternalCapabilities)
		}
	}
	return items
}

func frozenAgentLocator(source AgentSourceRef) string {
	if source.Scope == "global" {
		return "global/agents/" + source.ID + ".toml"
	}
	return agentFile(source.ID)
}

func validateFrozenExternalCapture(config ResolvedAgentConfig, captured *CapabilitySnapshot) error {
	if !managedLibraryHarness(config.Harness) || captured == nil || captured.Version != 1 || captured.Adapter != libraryAdapterID(config.Harness) {
		return errors.New("Frozen specialist lacks an explicit managed Library capture.")
	}
	copy := *captured
	sealCapabilitySnapshot(&copy)
	if copy.Revision != captured.Revision || !reflect.DeepEqual(copySelection(config.Selection), copySelection(captured.Selection)) {
		return errors.New("Frozen specialist configuration/capability hash mismatch.")
	}
	for _, skill := range captured.Skills {
		if skill.Internal {
			return errors.New("Frozen graph specialists cannot acquire internal role skills.")
		}
	}
	return nil
}

func validateGraphSnapshotV2Catalog(snapshot GraphSnapshot) error {
	catalog := snapshot.DelegationCatalog
	if catalog == nil || catalog.Agents == nil || len(catalog.Agents) > graphFrozenAgentLimit || len(catalog.Diagnostics) > 100 {
		return errors.New("Invalid bounded frozen graph specialist catalog.")
	}
	if snapshot.ProjectID == "" {
		return errors.New("Frozen graph catalog has no owning project identity.")
	}
	copy := *catalog
	sealGraphFrozenCatalog(&copy)
	if copy.Digest != catalog.Digest {
		return errors.New("Frozen graph specialist catalog digest mismatch.")
	}
	modelsJSON, _ := json.Marshal(catalog.ModelCatalogs)
	contentBytes := len(modelsJSON)
	if contentBytes > graphFrozenCatalogByteLimit {
		return errors.New("Frozen model catalog exceeds 16 MiB.")
	}
	if len(catalog.ModelCatalogs) > 3 {
		return errors.New("Invalid frozen harness model catalogs.")
	}
	for harness := range catalog.ModelCatalogs {
		if !managedLibraryHarness(harness) {
			return errors.New("Unknown frozen model harness.")
		}
	}
	for id, entry := range catalog.Agents {
		if !validAuthoringID(id) || entry.Source.ID != id || !validAgentSourceUID(entry.Source.SourceUID) || entry.SourceLocator != frozenAgentLocator(entry.Source) || revision([]byte(entry.SourceText)) != entry.SourceRevision {
			return errors.New("Invalid frozen specialist source identity/locator.")
		}
		if entry.Source.Scope != "global" && entry.Source.Scope != "project" || entry.Source.Scope == "global" && entry.Source.ProjectID != "" || entry.Source.Scope == "project" && entry.Source.ProjectID == "" {
			return errors.New("Invalid frozen specialist scope.")
		}
		document, err := readAgentDocument([]byte(entry.SourceText))
		if err != nil || !equalCapturedAgentDefinitions(document.AgentDefinition, entry.Definition) || authoringSlug(entry.Definition.Name) != id || !entry.Definition.Enabled {
			return errors.New("Frozen specialist differs from its captured source definition.")
		}
		if entry.Source.Scope == "project" && (entry.Source.ProjectID != snapshot.ProjectID || document.SourceUID != "" && document.SourceUID != entry.Source.SourceUID) {
			return errors.New("Frozen local source has another project/marker identity.")
		}
		if entry.Source.Scope == "global" {
			canonical, err := toml.Marshal(entry.Definition)
			if err != nil || !bytes.Equal(canonical, []byte(entry.SourceText)) {
				return errors.New("Global specialist source is not canonical.")
			}
		}
		if entry.Execution.Source == nil || !reflect.DeepEqual(entry.Execution.Source.Source, entry.Source) || entry.Execution.Source.Revision != entry.SourceRevision || !reflect.DeepEqual(entry.Execution.Source.Config, agentConfig(entry.Definition)) {
			return errors.New("Frozen specialist base provenance mismatch.")
		}
		config := entry.Execution.Config
		if config.Harness != entry.Definition.DefaultHarness || config.Model != entry.Definition.Model || config.Prompt != entry.Definition.Prompt || entry.Definition.Effort != "" && config.Effort != entry.Definition.Effort || !reflect.DeepEqual(copySelection(config.Selection), agentConfig(entry.Definition).Selection) || entry.Execution.Revision != agentConfigDigest(config) {
			return errors.New("Frozen specialist effective defaults differ from its captured definition.")
		}
		if err := validateFrozenExternalCapture(entry.Execution.Config, entry.Execution.ExternalCapabilities); err != nil {
			return err
		}
		raw, _ := json.Marshal(entry)
		contentBytes += len(raw)
		if contentBytes > graphFrozenCatalogByteLimit {
			return errors.New("Frozen specialist catalog exceeds 16 MiB.")
		}
	}
	if len(snapshot.AgentOrigins) != len(snapshot.Agents) {
		return errors.New("Graph node origin manifest is incomplete.")
	}
	for _, record := range snapshot.Agents {
		source, ok := snapshot.AgentOrigins[record.ID]
		if !ok || source.ID != record.ID || !validAgentSourceUID(source.SourceUID) {
			return errors.New("Invalid graph node source origin.")
		}
		if source.Scope == "global" {
			raw, ok := snapshot.GlobalSources[record.ID]
			if !ok || raw.Revision != record.Revision {
				return errors.New("Global graph source is missing from its canonical capture.")
			}
			canonical, err := toml.Marshal(record.AgentDefinition)
			if err != nil || !bytes.Equal(canonical, []byte(raw.Text)) || source.ProjectID != "" {
				return errors.New("Global node source is not canonical.")
			}
		} else if source.Scope != "project" || source.ProjectID != snapshot.ProjectID {
			return errors.New("Unknown/foreign graph node source scope.")
		} else {
			document, err := readAgentDocument([]byte(snapshot.Sources[agentFile(record.ID)].Text))
			if err != nil || document.SourceUID != "" && document.SourceUID != source.SourceUID {
				return errors.New("Local graph node source marker differs from its manifest.")
			}
		}
	}
	for id, source := range snapshot.GlobalSources {
		origin, ok := snapshot.AgentOrigins[id]
		if !ok || origin.Scope != "global" || revision([]byte(source.Text)) != source.Revision {
			return errors.New("Unowned/noncanonical global graph source.")
		}
	}
	return nil
}

// Snapshot-only lookup: never reads the live catalog, disk, credentials or an app.
// Deep copy prevents a caller mutating the run's sealed in-memory artifact.
func lookupFrozenGraphAgent(snapshot GraphSnapshot, agent, sourceUID string) (GraphFrozenAgent, error) {
	if snapshot.Version != 2 || snapshot.DelegationCatalog == nil {
		return GraphFrozenAgent{}, agentProblem(409, "delegation_catalog_unavailable", "agent", "This run has no v2 specialist catalog; live fallback is unavailable.")
	}
	if err := validateGraphSnapshotV2Catalog(snapshot); err != nil {
		return GraphFrozenAgent{}, err
	}
	id := authoringSlug(agent)
	entry, ok := snapshot.DelegationCatalog.Agents[id]
	if !ok {
		return GraphFrozenAgent{}, agentProblem(409, "agent_not_captured", "agent", fmt.Sprintf("Specialist %s was not eligible/captured when this run started.", id))
	}
	if sourceUID != "" && sourceUID != entry.Source.SourceUID {
		return GraphFrozenAgent{}, agentProblem(409, "source_identity_mismatch", "agent", "Requested specialist UID differs from the frozen run source.")
	}
	raw, _ := json.Marshal(entry)
	var copy GraphFrozenAgent
	_ = json.Unmarshal(raw, &copy)
	return copy, nil
}
