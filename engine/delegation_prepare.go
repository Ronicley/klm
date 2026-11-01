package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type preparedDelegationChild struct {
	Request                 DelegationChildRequest
	Session                 Session
	Child                   *DelegationChildRecord
	Binding                 *SessionAgentBinding
	PreviousBindingRevision string
	Resolution              *AgentResolution
	Capture                 AgentExecutionCapture
	CWD                     string
}

func cloneDelegationCapture(value AgentExecutionCapture) AgentExecutionCapture {
	raw, _ := json.Marshal(value)
	var result AgentExecutionCapture
	_ = json.Unmarshal(raw, &result)
	return result
}

// Discovery/capture happens outside app.mu and SQL writer transactions. The
// admission transaction still rechecks owner/Stop/source/binding/child leases.
func (p *adapter) prepareDelegationChildren(ctx context.Context, request DelegationRunRequest) ([]preparedDelegationChild, error) {
	if p.turn.graphDelegation != nil {
		return p.prepareFrozenGraphChildren(ctx, request)
	}
	if p.role == "graph_node" || !delegationParentRole(p.role) || p.role == sessionRoleSubagent || p.app.store == nil {
		return nil, errors.New("Ordinary parent delegation is unavailable to this role.")
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	cwd, err := CanonicalGraphDirectory(p.cwd)
	if err != nil {
		return nil, err
	}
	var parent Session
	if err := p.app.store.readSnapshot(ctx, func(tx *sql.Tx) error {
		var readErr error
		parent, readErr = (&sqliteSessionTx{ctx: ctx, tx: tx}).sessionMeta(p.id)
		return readErr
	}); err != nil {
		return nil, err
	}
	prepared := make([]preparedDelegationChild, 0, len(request.Children))
	for _, entry := range request.Children {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		child := preparedDelegationChild{Request: entry, CWD: cwd, Session: Session{ProjectID: parent.ProjectID}}
		inherited := CapabilitySelection{}
		if entry.InheritParentCapabilities && p.capabilities != nil {
			inherited = copySelection(p.capabilities.Selection)
		}
		if entry.ChildSessionID != "" {
			err := p.app.store.readSnapshot(ctx, func(tx *sql.Tx) error {
				st := &sqliteSessionTx{ctx: ctx, tx: tx}
				stored, err := st.delegationChild(entry.ChildSessionID)
				if err != nil {
					return err
				}
				if stored.ParentSessionID != p.id || !stored.Reusable() || stored.GraphRunID != "" || stored.ExecutionCWD != cwd {
					return errors.New("Child is busy, quarantined or owned by another parent/directory.")
				}
				child.Child = &stored
				child.Session, err = st.sessionMeta(stored.ID)
				if err != nil {
					return err
				}
				if err := validateDelegationOwnership(child.Session, parent, stored); err != nil {
					return err
				}
				child.Binding, err = st.agentBinding(stored.ID)
				if err != nil {
					return err
				}
				if child.Binding != nil {
					child.PreviousBindingRevision = child.Binding.Revision
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			if entry.Harness != "" && entry.Harness != child.Child.Harness {
				return nil, errors.New("A reusable child's harness is fixed.")
			}
			if entry.AgentID != "" && (child.Binding == nil || child.Binding.Source.ID != entry.AgentID) {
				return nil, errors.New("A reusable child cannot be rebound to another specialist.")
			}
		}
		if child.Binding != nil {
			updated := cloneAgentBinding(*child.Binding)
			if entry.Model != nil {
				updated.Overrides.Model = entry.Model
			}
			if entry.Effort != nil {
				updated.Overrides.Effort = entry.Effort
			}
			res, err := p.app.resolveBoundAgent(ctx, BoundAgentRequest{Binding: updated, FixedHarness: child.Child.Harness, ProjectID: parent.ProjectID, ExecutionCWD: cwd})
			if err != nil {
				return nil, err
			}
			if res.Blocked != nil {
				return nil, res.Blocked
			}
			updated.Revision, err = nextAgentRevision(updated.Revision)
			if err != nil {
				return nil, err
			}
			child.Binding, child.Resolution = &updated, &res
			child.Capture.Config, child.Capture.Source = res.Config, &res.Capture
		} else if entry.AgentID != "" {
			overrides := AgentConfigOverrides{Model: entry.Model, Effort: entry.Effort}
			res, err := p.app.resolveNewAgent(ctx, NewAgentRequest{ProjectID: parent.ProjectID, Agent: entry.AgentID, Harness: entry.Harness, Overrides: overrides, InheritedCapabilities: inherited, ExecutionCWD: cwd})
			if err != nil {
				return nil, err
			}
			child.Resolution = &res
			child.Binding = &SessionAgentBinding{Source: res.Capture.Source, Revision: "0", LastCaptured: res.Capture, Overrides: overrides, InheritedCapabilities: inherited, Desired: res.Config, SourceStatus: res.SourceStatus}
			child.Capture.Config, child.Capture.Source = res.Config, &res.Capture
		} else {
			config := ResolvedAgentConfig{Harness: p.harness, Model: p.model, Effort: p.effort, Selection: inherited}
			if child.Child != nil {
				config.Harness, config.Model, config.Effort = child.Child.Harness, child.Session.Model, child.Session.Effort
				err := p.app.store.readSnapshot(ctx, func(tx *sql.Tx) error {
					var readErr error
					config.Selection, readErr = (&sqliteSessionTx{ctx: ctx, tx: tx}).inheritedSelection(child.Child.ID)
					return readErr
				})
				if err != nil {
					return nil, err
				}
			} else if entry.Harness != "" && entry.Harness != config.Harness {
				config.Harness, config.Model, config.Effort = entry.Harness, "", ""
			}
			if entry.Model != nil {
				config.Model = *entry.Model
			}
			if entry.Effort != nil {
				config.Effort = *entry.Effort
			}
			config, err = p.app.validateResolvedAgent(ctx, config, parent.ProjectID, cwd)
			if err != nil {
				return nil, err
			}
			child.Capture.Config = config
		}
		p.app.libraryMu.Lock()
		external, captureErr := p.app.captureCapabilities(ctx, child.Capture.Config.Selection, child.Capture.Config.Harness)
		p.app.libraryMu.Unlock()
		if captureErr != nil {
			return nil, captureErr
		}
		if external == nil {
			return nil, errors.New("Child execution requires its own managed external snapshot.")
		}
		child.Capture.ExternalCapabilities, child.Capture.Revision = external, agentConfigDigest(child.Capture.Config)
		child.Capture = cloneDelegationCapture(child.Capture)
		prepared = append(prepared, child)
	}
	return prepared, nil
}

// authoringMu/libraryMu/app.mu held in that order by the admission owner.
func (st *sqliteSessionTx) admitPreparedDelegation(parent Session, batch DelegationBatchRecord, prepared []preparedDelegationChild, dataDir string) ([]DelegationInvocationRecord, error) {
	if !delegationParentRole(parent.Role) || batch.ParentSessionID != parent.ID || batch.Graph == nil && batch.OwnerConversationID != parent.ID {
		return nil, errors.New("Ordinary delegation owner scope is invalid.")
	}
	if err := st.checkGraphDelegationAdmission(parent.ID, batch.Graph); err != nil {
		return nil, err
	}
	if parent.Role != sessionRoleGeneralAgent {
		available, err := st.projectAvailable(parent.ProjectID)
		if err != nil {
			return nil, err
		}
		if !available {
			return nil, errors.New("Parent project is unavailable.")
		}
	}
	if len(prepared) != batch.ExpectedChildren {
		return nil, errors.New("Prepared child set is incomplete.")
	}
	var recovery bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_library_deletions)`).Scan(&recovery); err != nil {
		return nil, err
	}
	if recovery {
		return nil, errors.New("Finish Library recovery before admitting children.")
	}
	members := make([]DelegationInvocationRecord, 0, len(prepared))
	for index, item := range prepared {
		child := item.Session
		if item.Child == nil {
			child = Session{ID: newID(), ParentID: parent.ID, ProjectID: parent.ProjectID, Role: sessionRoleSubagent, Title: subagentTitle(item.Request.Title), Workspace: parent.Workspace, Harness: item.Capture.Config.Harness, Model: item.Capture.Config.Model, Effort: item.Capture.Config.Effort, YOLO: parent.YOLO, Status: "idle", CreatedAt: now(), UpdatedAt: now()}
			if batch.Graph != nil {
				child.ExecutionCWD = item.CWD
			}
			if err := st.createSession(child, dataDir); err != nil {
				return nil, err
			}
		} else {
			stored, err := st.delegationChild(child.ID)
			if err != nil {
				return nil, err
			}
			if !stored.Reusable() || stored.ParentSessionID != parent.ID || stored.Harness != item.Capture.Config.Harness || stored.ExecutionCWD != item.CWD {
				return nil, errors.New("Child lease/settings changed before batch acceptance.")
			}
		}
		current, err := st.agentBinding(child.ID)
		if err != nil {
			return nil, err
		}
		if item.PreviousBindingRevision != "" && (current == nil || current.Revision != item.PreviousBindingRevision) {
			return nil, errors.New("Child specialist settings changed before acceptance.")
		}
		if item.PreviousBindingRevision == "" && current != nil {
			return nil, errors.New("Child binding changed during preparation.")
		}
		if item.Binding != nil {
			if err := st.saveAgentConfiguration(child.ID, *item.Binding, *item.Resolution, false); err != nil {
				return nil, err
			}
		} else {
			if err := st.putSingleChild("capability_selections", sqliteOwnedKey(child.ID, ""), copySelection(item.Capture.Config.Selection)); err != nil {
				return nil, err
			}
		}
		child, err = st.sessionMeta(child.ID)
		if err != nil {
			return nil, err
		}
		child.Model, child.Effort, child.YOLO, child.UpdatedAt = item.Capture.Config.Model, item.Capture.Config.Effort, parent.YOLO, now()
		if err := st.putSessionMeta(child); err != nil {
			return nil, err
		}
		// Reuse the existing operational snapshot projection while this child is
		// admitted/queued, so credential retention sees its frozen contribution
		// before a native slot starts. Invocation.Capture remains authoritative.
		if err := st.putSingleChild("capability_preparing", sqliteOwnedKey(child.ID, ""), item.Capture.ExternalCapabilities); err != nil {
			return nil, err
		}
		var start int64
		if err := st.tx.QueryRowContext(st.ctx, `SELECT size FROM record_collections WHERE parent_table='sessions' AND parent_id=? AND field='events'`, child.ID).Scan(&start); err != nil {
			return nil, err
		}
		invocation := DelegationInvocationRecord{Version: 1, ID: newID(), BatchID: batch.ID, Key: item.Request.Key, Ordinal: index, ChildSessionID: child.ID, ParentSessionID: parent.ID, ParentTurnID: batch.ParentTurnID, State: DelegationInvocationAdmitted, Capture: item.Capture, ExecutionCWD: item.CWD, Task: item.Request.Task, CreatedAt: now(), EventStart: start}
		invocation.Graph = batch.Graph
		if item.Binding != nil {
			invocation.BindingRevision = item.Binding.Revision
		}
		if err := st.putDelegationInvocation(invocation); err != nil {
			return nil, err
		}
		owned := DelegationChildRecord{Version: 1, ID: child.ID, ParentSessionID: parent.ID, Origin: "engine", Harness: child.Harness, ExecutionCWD: item.CWD, ActiveInvocationID: invocation.ID, CreatedAt: child.CreatedAt}
		if batch.Graph != nil {
			owned.GraphRunID, owned.WorkspaceID = batch.Graph.RunID, batch.Graph.WorkspaceID
		}
		if err := st.putRoot("delegation_children", child.ID, owned); err != nil {
			return nil, err
		}
		e := event("agent_prompt", item.Request.Task)
		e.Data = map[string]any{"batchId": batch.ID, "invocationId": invocation.ID, "origin": map[string]any{"sessionId": parent.ID, "role": "agent"}}
		if _, err := st.appendEvent(child.ID, e); err != nil {
			return nil, err
		}
		members = append(members, invocation)
	}
	if err := st.putDelegationBatch(&batch); err != nil {
		return nil, err
	}
	return members, st.syncDelegationCards(batch, members)
}
