package main

import (
	"github.com/pelletier/go-toml/v2"
)

func (st *sqliteSessionTx) removeAgentLibraryReferences(kind, id string) error {
	for _, table := range []string{"global_agents", "agent_bindings"} {
		for {
			keys, err := sqliteQueryIDs(st.ctx, st.tx, `SELECT record_key FROM selection_references WHERE domain=? AND kind=? AND item=? LIMIT 100`, table, kind, id)
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				break
			}
			for _, key := range keys {
				if table == "global_agents" {
					var global GlobalAgentRecord
					if err := decodeSQLiteRecord(st.ctx, st.tx, table, key, 8<<20, &global); err != nil {
						return err
					}
					selection := removeLibraryReference(agentConfig(global.Definition).Selection, kind, id)
					global.Definition.Skills, global.Definition.MCPConnections = selection.Skills, selection.MCPConnections
					data, err := toml.Marshal(global.Definition)
					if err != nil {
						return err
					}
					global.Revision = revision(data)
					if err := st.putRoot(table, key, global); err != nil {
						return err
					}
					source := AgentSourceRef{SourceUID: global.ID, ID: global.Slug, Scope: "global"}
					recordID := agentSourceRecordID(source, "")
					var record AgentSourceRecord
					if err := decodeSQLiteRecord(st.ctx, st.tx, "agent_sources", recordID, 64<<10, &record); err == nil {
						record.Revision = global.Revision
						if err := st.putRoot("agent_sources", recordID, record); err != nil {
							return err
						}
					}
				} else {
					var binding *SessionAgentBinding
					if err := decodeSQLiteRecord(st.ctx, st.tx, table, key, 8<<20, &binding); err != nil {
						return err
					}
					if binding == nil {
						continue
					}
					binding.LastCaptured.Config.Selection = removeLibraryReference(binding.LastCaptured.Config.Selection, kind, id)
					binding.InheritedCapabilities = removeLibraryReference(binding.InheritedCapabilities, kind, id)
					binding.Desired.Selection = removeLibraryReference(binding.Desired.Selection, kind, id)
					if binding.Overrides.Skills != nil {
						selection := removeLibraryReference(CapabilitySelection{Skills: *binding.Overrides.Skills}, kind, id)
						binding.Overrides.Skills = &selection.Skills
					}
					if binding.Overrides.MCPConnections != nil {
						selection := removeLibraryReference(CapabilitySelection{MCPConnections: *binding.Overrides.MCPConnections}, kind, id)
						binding.Overrides.MCPConnections = &selection.MCPConnections
					}
					binding.Revision, err = nextAgentRevision(binding.Revision)
					if err != nil {
						return err
					}
					var owner string
					if err := st.tx.QueryRowContext(st.ctx, `SELECT owner_id FROM data_agent_bindings WHERE id=?`, key).Scan(&owner); err != nil {
						return err
					}
					res := AgentResolution{Config: binding.Desired, Capture: binding.LastCaptured, SourceStatus: binding.SourceStatus, Blocked: binding.Blocked, Token: binding.Revision}
					if err := st.saveAgentConfiguration(owner, *binding, res, true); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
