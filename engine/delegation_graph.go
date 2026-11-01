package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
)

type graphDelegationBinding struct {
	Snapshot GraphSnapshot
	Scope    DelegationGraphScope
	OwnerID  string
}

func (c *delegationExecution) graphScope() *DelegationGraphScope {
	if binding := c.parent.turn.graphDelegation; binding != nil {
		scope := binding.Scope
		return &scope
	}
	return nil
}

func (c *delegationExecution) ownsBatch(batch DelegationBatchRecord) bool {
	return batch.ParentSessionID == c.parent.id && reflect.DeepEqual(batch.Graph, c.graphScope())
}

func (st *sqliteSessionTx) checkGraphDelegationAdmission(parentID string, scope *DelegationGraphScope) error {
	if scope == nil {
		return nil
	}
	parent, err := st.sessionMeta(parentID)
	if err != nil {
		return err
	}
	if _, err := (sqliteValidation{ctx: st.ctx, tx: st.tx}).validateDelegationGraph(parent, scope, true); err != nil {
		return err
	}
	run, err := st.graphRunRecord(scope.RunID)
	if err != nil {
		return err
	}
	x, err := st.graphActivation(scope.ActivationID)
	if err != nil {
		return err
	}
	if run.Status != "running" || x.Status != "running" || x.AdapterState != "open" || x.Submission != nil {
		return errors.New("Graph activation no longer admits child work.")
	}
	return nil
}

// Choice and admission use the existing app mutex + SQL writer, not a new lock
// hierarchy. A preparation reserves that same turn before asynchronous discovery.
func (a *app) checkGraphChildrenBeforeChoice(st *sqliteSessionTx, x GraphActivation) error {
	if t := a.runs[x.SessionID]; t != nil && t.delegation != nil && t.delegation.preparing != nil {
		return errors.New("Wait for child preparation and consolidated delivery before Choice.")
	}
	active, err := st.activeDelegation(x.SessionID)
	if err != nil {
		return err
	}
	if active != nil {
		return errors.New("Wait for every child, owned drainage and result delivery before Choice.")
	}
	return nil
}

// A graph node never refreshes live models or Library. New captures include public
// model catalogs; older v2 artifacts retain their captured effective pairs only.
func frozenGraphModel(snapshot GraphSnapshot, config *ResolvedAgentConfig) error {
	if snapshot.DelegationCatalog != nil {
		if catalog, ok := snapshot.DelegationCatalog.ModelCatalogs[config.Harness]; ok {
			if config.Model == "" {
				config.Model = catalog.DefaultModel
			}
			model := catalog.find(config.Model)
			if model == nil {
				return errors.New("Requested model is not present in the frozen run catalog.")
			}
			if config.Effort == "" {
				config.Effort = model.DefaultEffort
				if config.Model == catalog.DefaultModel && catalog.DefaultEffort != "" {
					config.Effort = catalog.DefaultEffort
				}
			}
			if len(model.Efforts) > 0 && !slices.Contains(model.Efforts, config.Effort) || len(model.Efforts) == 0 && config.Effort != "" {
				return errors.New("Invalid child model/effort in the frozen run catalog.")
			}
			return nil
		}
	}
	match := func(harness, model, effort string) bool {
		return config.Harness == harness && config.Model == model && config.Effort == effort
	}
	for _, agent := range snapshot.EffectiveAgents {
		if match(agent.Harness, agent.Model, agent.Effort) {
			return nil
		}
	}
	if snapshot.DelegationCatalog != nil {
		for _, agent := range snapshot.DelegationCatalog.Agents {
			c := agent.Execution.Config
			if match(c.Harness, c.Model, c.Effort) {
				return nil
			}
		}
	}
	return errors.New("Requested child model/effort was not validated in this frozen run. Use a captured configuration.")
}

func (p *adapter) prepareFrozenGraphChildren(ctx context.Context, request DelegationRunRequest) ([]preparedDelegationChild, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	binding := p.turn.graphDelegation
	if binding == nil || !p.graphNode() {
		return nil, errors.New("Frozen graph delegation is unavailable.")
	}
	cwd, err := CanonicalGraphDirectory(p.cwd)
	if err != nil {
		return nil, err
	}
	var parent Session
	if err := p.app.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		var err error
		parent, err = st.sessionMeta(p.id)
		return err
	}); err != nil {
		return nil, err
	}
	prepared := make([]preparedDelegationChild, 0, len(request.Children))
	for _, entry := range request.Children {
		child := preparedDelegationChild{Request: entry, Session: Session{ProjectID: parent.ProjectID}, CWD: cwd}
		if entry.ChildSessionID != "" {
			err := p.app.store.readSnapshot(ctx, func(tx *sql.Tx) error {
				st := &sqliteSessionTx{ctx: ctx, tx: tx}
				owned, err := st.delegationChild(entry.ChildSessionID)
				if err != nil {
					return err
				}
				if !owned.Reusable() || owned.ParentSessionID != p.id || owned.GraphRunID != binding.Scope.RunID || owned.WorkspaceID != binding.Scope.WorkspaceID || owned.ExecutionCWD != cwd {
					return errors.New("Child cannot be reused by this graph parent/run/workspace.")
				}
				child.Child = &owned
				child.Session, err = st.sessionMeta(owned.ID)
				if err != nil {
					return err
				}
				var id string
				if err := tx.QueryRowContext(ctx, `SELECT id FROM data_delegation_invocations WHERE x_childSessionId=? AND x_state='terminal' ORDER BY created_at DESC,id DESC LIMIT 1`, owned.ID).Scan(&id); err != nil {
					return err
				}
				previous, err := st.delegationInvocation(id)
				if err != nil {
					return err
				}
				child.Capture = cloneDelegationCapture(previous.Capture)
				return nil
			})
			if err != nil {
				return nil, err
			}
			if entry.Harness != "" && entry.Harness != child.Child.Harness {
				return nil, errors.New("A reusable child's harness is fixed.")
			}
			if entry.AgentID != "" && (child.Capture.Source == nil || child.Capture.Source.Source.ID != authoringSlug(entry.AgentID)) {
				return nil, errors.New("A reusable graph child cannot change specialist.")
			}
		} else if entry.AgentID != "" {
			child.Capture, err = composeFrozenGraphAgent(binding.Snapshot, entry.AgentID, "", binding.Scope.NodeID, entry.Harness, AgentConfigOverrides{Model: entry.Model, Effort: entry.Effort}, entry.InheritParentCapabilities)
			if err != nil {
				return nil, err
			}
		} else {
			config := ResolvedAgentConfig{Harness: p.harness, Model: p.model, Effort: p.effort, Selection: copySelection(CapabilitySelection{})}
			if entry.Harness != "" && entry.Harness != config.Harness {
				config.Harness, config.Model, config.Effort = entry.Harness, "", ""
			}
			external := &CapabilitySnapshot{Version: 1, Adapter: libraryAdapterID(config.Harness), Selection: copySelection(config.Selection), Skills: []SkillSnapshot{}, Connections: []MCPConnectionSnapshot{}, Diagnostics: []string{}}
			if entry.InheritParentCapabilities {
				raw, _ := json.Marshal(binding.Snapshot.Capabilities[binding.Scope.NodeID])
				if err := json.Unmarshal(raw, &external); err != nil || external == nil {
					return nil, errors.New("Parent frozen Library is unavailable.")
				}
				external.Adapter = libraryAdapterID(config.Harness)
				config.Selection = copySelection(external.Selection)
			}
			sealCapabilitySnapshot(external)
			child.Capture = AgentExecutionCapture{Config: config, ExternalCapabilities: external}
		}
		if entry.Model != nil {
			child.Capture.Config.Model = *entry.Model
		}
		if entry.Effort != nil {
			child.Capture.Config.Effort = *entry.Effort
		}
		if err := CheckGraphAdapter(child.Capture.Config.Harness); err != nil {
			return nil, err
		}
		if err := frozenGraphModel(binding.Snapshot, &child.Capture.Config); err != nil {
			return nil, err
		}
		child.Capture.Revision = agentConfigDigest(child.Capture.Config)
		prepared = append(prepared, child)
	}
	return prepared, nil
}

func (p *adapter) frozenAgentDiscovery(name string, raw json.RawMessage) (any, error) {
	var args struct {
		Agent, Query, Cursor string
		Limit                int
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	binding := p.turn.graphDelegation
	if binding == nil {
		return nil, errors.New("Frozen catalog unavailable.")
	}
	page := generalToolArgs{SessionID: p.id, Query: args.Query, Cursor: args.Cursor, Limit: args.Limit}
	if name == "agent_get" {
		entry, err := lookupFrozenGraphAgent(binding.Snapshot, args.Agent, "")
		if err != nil {
			return nil, err
		}
		page.Query = args.Agent
		return agentValuePage(name, page, map[string]any{"agent": entry.Definition, "source": entry.Source, "revision": entry.SourceRevision}, revisionForItems(binding.Snapshot.DelegationCatalog.Digest))
	}
	items := []any{}
	for _, id := range sortedGraphKeys(binding.Snapshot.DelegationCatalog.Agents) {
		entry := binding.Snapshot.DelegationCatalog.Agents[id]
		if args.Query != "" && !strings.Contains(strings.ToLower(entry.Definition.Name+" "+entry.Definition.Description), strings.ToLower(args.Query)) {
			continue
		}
		items = append(items, map[string]any{"id": id, "name": entry.Definition.Name, "source": entry.Source, "harness": entry.Execution.Config.Harness, "model": entry.Execution.Config.Model, "effort": entry.Execution.Config.Effort})
	}
	return agentItemsPage(name, page, items, map[string]any{"complete": binding.Snapshot.DelegationCatalog.Complete, "diagnostics": binding.Snapshot.DelegationCatalog.Diagnostics}, revisionForItems(binding.Snapshot.DelegationCatalog.Digest))
}

func (a *app) validateGraphChildRead(ctx context.Context, id string) error {
	return a.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		st := &sqliteSessionTx{ctx: ctx, tx: tx}
		child, err := st.delegationChild(id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if child.GraphRunID == "" {
			return nil
		}
		var invocation string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM data_delegation_invocations WHERE x_childSessionId=? ORDER BY created_at DESC,id DESC LIMIT 1`, id).Scan(&invocation); err != nil {
			return err
		}
		item, err := st.delegationInvocation(invocation)
		if err != nil {
			return err
		}
		if item.Graph == nil || item.Graph.RunID != child.GraphRunID || item.Graph.WorkspaceID != child.WorkspaceID {
			return sql.ErrNoRows
		}
		parent, err := st.sessionMeta(child.ParentSessionID)
		if err != nil {
			return err
		}
		_, err = (sqliteValidation{ctx: ctx, tx: tx}).validateDelegationGraph(parent, item.Graph, false)
		return err
	})
}
