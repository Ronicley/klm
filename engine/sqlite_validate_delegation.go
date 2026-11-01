package main

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
)

func delegationCanonicalDirectory(value string) bool {
	return filepath.IsAbs(value) && value == filepath.Clean(value)
}
func delegationSameDirectory(left, right string) bool {
	return sameProjectFolder(filepath.Clean(left), filepath.Clean(right))
}

// History validation uses the ledger's immutable workspace, not a later current
// cwd of a reusable node. Active invocation validation checks its actual epoch.
func (v sqliteValidation) validateManagedDelegationChild(id string) error {
	var record DelegationChildRecord
	if err := v.get("delegation_children", id, &record); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if record.ID != id || !delegationCanonicalDirectory(record.ExecutionCWD) {
		return errors.New("Invalid managed child identity/canonical directory.")
	}
	child, err := v.session(id)
	if err != nil {
		return err
	}
	parent, err := v.session(record.ParentSessionID)
	if err != nil {
		return err
	}
	parentForShape := parent
	if parent.Role == "graph_node" {
		parentForShape.ExecutionCWD = record.ExecutionCWD
	}
	if err := validateDelegationOwnership(child, parentForShape, record); err != nil {
		return err
	}
	if child.ExecutionCWD != "" && !delegationSameDirectory(child.ExecutionCWD, record.ExecutionCWD) {
		return errors.New("Managed child session directory differs from its ownership ledger.")
	}
	if parent.Role == sessionRoleSideAgent {
		main, err := v.session(parent.ParentID)
		if err != nil {
			return err
		}
		if main.Role != "" || main.ParentID != "" || main.ProjectID != parent.ProjectID || main.GraphRunID != "" {
			return errors.New("Managed side-parent relationship is invalid.")
		}
	}
	if parent.Role != "graph_node" {
		if parent.GraphRunID != "" || parent.GraphNodeID != "" {
			return errors.New("Ordinary delegation parent cannot own a graph execution.")
		}
		allowed := parent.ExecutionCWD
		if parent.Role != sessionRoleGeneralAgent {
			var project Project
			if err := v.get("projects", parent.ProjectID, &project); err != nil {
				return err
			}
			allowed = project.Folder
		}
		if !delegationSameDirectory(record.ExecutionCWD, allowed) {
			return errors.New("Child directory is outside its owning conversation.")
		}
	} else {
		if parent.ParentID != "" || parent.GraphNodeID == "" {
			return errors.New("Invalid graph node parent identity.")
		}
		var workspace WorkspaceRecord
		if err := v.get("graph_workspaces", record.WorkspaceID, &workspace); err != nil {
			return err
		}
		run, err := v.run(record.GraphRunID)
		if err != nil {
			return err
		}
		if run.ProjectID != child.ProjectID || workspace.ProjectID != run.ProjectID || !delegationSameDirectory(workspace.Directory, record.ExecutionCWD) {
			return errors.New("Managed graph child workspace ownership differs from its run.")
		}
		var used bool
		if err := v.tx.QueryRowContext(v.ctx, `SELECT EXISTS(SELECT 1 FROM data_graph_workspace_uses WHERE x_runId=? AND x_workspaceId=?)`, run.ID, record.WorkspaceID).Scan(&used); err != nil {
			return err
		}
		if !used {
			return errors.New("Graph child workspace is not associated with its owning run.")
		}
	}
	var spawn bool
	if err := v.tx.QueryRowContext(v.ctx, `SELECT EXISTS(SELECT 1 FROM data_session_spawns WHERE session_id=? OR x_from=?)`, id, id).Scan(&spawn); err != nil {
		return err
	}
	if spawn {
		return errors.New("Managed child cannot own an independent-session spawn receipt.")
	}
	var activeIDs []string
	activeIDs, err = sqliteQueryIDs(v.ctx, v.tx, `SELECT id FROM data_delegation_invocations WHERE x_childSessionId=? AND x_state<>'terminal' LIMIT 2`, id)
	if err != nil {
		return err
	}
	if len(activeIDs) > 1 || len(activeIDs) == 1 && record.ActiveInvocationID != activeIDs[0] {
		return errors.New("Managed child active lease disagrees with its invocation.")
	}
	if record.ActiveInvocationID == "" {
		if record.Quarantined {
			return errors.New("Quarantined child must retain its uncertain lease.")
		}
		return nil
	}
	var invocation DelegationInvocationRecord
	if err := v.get("delegation_invocations", record.ActiveInvocationID, &invocation); err != nil {
		return err
	}
	if invocation.ChildSessionID != id || invocation.ParentSessionID != parent.ID || invocation.Capture.Config.Harness != record.Harness || !delegationSameDirectory(invocation.ExecutionCWD, record.ExecutionCWD) {
		return errors.New("Managed child lease points outside its fixed ownership.")
	}
	if invocation.State == DelegationInvocationTerminal && !invocation.Finality.Confirmed() && !record.Quarantined {
		return errors.New("Unconfirmed terminal finality must quarantine its lease.")
	}
	if record.Quarantined && invocation.State != DelegationInvocationTerminal {
		return errors.New("A nonterminal child lease cannot be released as quarantined completion.")
	}
	return nil
}

func (v sqliteValidation) validateDelegationGraph(parent Session, scope *DelegationGraphScope, active bool) (string, error) {
	if parent.Role != "graph_node" {
		if scope != nil {
			return "", errors.New("Only graph-node parents may carry activation ownership.")
		}
		return parent.ID, nil
	}
	if scope == nil {
		return "", errors.New("Graph delegation requires exact activation ownership.")
	}
	if err := scope.Validate(); err != nil {
		return "", err
	}
	run, err := v.run(scope.RunID)
	if err != nil {
		return "", err
	}
	activation, err := v.activation(scope.ActivationID)
	if err != nil {
		return "", err
	}
	workspace, err := v.workspace(scope.WorkspaceID)
	if err != nil {
		return "", err
	}
	if parent.GraphRunID != run.ID || parent.ProjectID != run.ProjectID || parent.GraphNodeID != scope.NodeID || activation.RunID != run.ID || activation.NodeID != scope.NodeID || activation.SessionID != parent.ID || activation.WorkspaceID != workspace.ID || workspace.ProjectID != run.ProjectID {
		return "", errors.New("Delegation graph parent/run/activation/workspace ownership mismatch.")
	}
	if active && (activation.CurrentTurnID != scope.TurnID || !delegationSameDirectory(parent.ExecutionCWD, workspace.Directory)) {
		return "", errors.New("Active graph delegation belongs to a different turn/directory epoch.")
	}
	var snapshot struct {
		Revision string `json:"revision"`
	}
	if err := v.get("graph_snapshots", sqliteOwnedKey(run.ID, ""), &snapshot); err != nil {
		return "", err
	}
	if snapshot.Revision != scope.SnapshotDigest {
		return "", errors.New("Delegation graph snapshot digest differs from its run capture.")
	}
	owner, err := v.session(run.ConversationID)
	if err != nil {
		return "", err
	}
	if owner.ProjectID != run.ProjectID || owner.Role != "" || owner.ParentID != "" {
		return "", errors.New("Graph delegation has no valid owning visible conversation.")
	}
	return owner.ID, nil
}

func (v sqliteValidation) validateDelegationInvocation(id string, batch DelegationBatchRecord) error {
	var item DelegationInvocationRecord
	if err := v.get("delegation_invocations", id, &item); err != nil {
		return err
	}
	if item.ID != id {
		return errors.New("Delegation invocation record identity mismatch.")
	}
	if err := item.Validate(); err != nil {
		return err
	}
	if item.BatchID != batch.ID || item.ParentSessionID != batch.ParentSessionID || item.ParentTurnID != batch.ParentTurnID || !reflect.DeepEqual(item.Graph, batch.Graph) || !delegationCanonicalDirectory(item.ExecutionCWD) {
		return errors.New("Delegation invocation membership/epoch mismatch.")
	}
	var child DelegationChildRecord
	if err := v.get("delegation_children", item.ChildSessionID, &child); err != nil {
		return err
	}
	if child.ParentSessionID != batch.ParentSessionID || child.Harness != item.Capture.Config.Harness || !delegationSameDirectory(child.ExecutionCWD, item.ExecutionCWD) {
		return errors.New("Invocation violates the child fixed parent/harness/directory.")
	}
	if item.Graph == nil {
		if child.GraphRunID != "" {
			return errors.New("Ordinary invocation cannot reuse a graph-owned child.")
		}
	} else if child.GraphRunID != item.Graph.RunID || child.WorkspaceID != item.Graph.WorkspaceID {
		return errors.New("Invocation cannot reuse a child across graph run/workspace.")
	}
	if item.State != DelegationInvocationTerminal && child.ActiveInvocationID != item.ID {
		return errors.New("Nonterminal invocation does not hold its child lease.")
	}
	if item.State == DelegationInvocationTerminal && !item.Finality.Confirmed() && (child.ActiveInvocationID != item.ID || !child.Quarantined) {
		return errors.New("Uncertain terminal child lost its retained quarantine lease.")
	}
	if item.Capture.ExternalCapabilities != nil {
		external := item.Capture.ExternalCapabilities
		copy := *external
		sealCapabilitySnapshot(&copy)
		if external.Version != 1 || external.Adapter != libraryAdapterID(item.Capture.Config.Harness) || copy.Revision != external.Revision || !reflect.DeepEqual(copySelection(external.Selection), copySelection(item.Capture.Config.Selection)) {
			return errors.New("Invocation external capture does not match its frozen effective configuration.")
		}
	} else if item.DispatchAttempted {
		return errors.New("Dispatch intent requires an explicit managed external capture, even when empty.")
	}
	if item.Graph != nil && item.State != DelegationInvocationTerminal {
		parent, err := v.session(batch.ParentSessionID)
		if err != nil {
			return err
		}
		if _, err := v.validateDelegationGraph(parent, item.Graph, true); err != nil {
			return err
		}
	}
	return nil
}

func (v sqliteValidation) validateDelegationBatch(id string) error {
	var batch DelegationBatchRecord
	if err := v.get("delegation_batches", id, &batch); err != nil {
		return err
	}
	if batch.ID != id {
		return errors.New("Delegation batch record identity mismatch.")
	}
	if err := batch.Validate(); err != nil {
		return err
	}
	parent, err := v.session(batch.ParentSessionID)
	if err != nil {
		return err
	}
	if !delegationParentRole(parent.Role) || parent.Role == "" && parent.ParentID != "" {
		return errors.New("A delegation batch cannot belong to a leaf or hidden ordinary conversation.")
	}
	owner, err := v.validateDelegationGraph(parent, batch.Graph, batch.State != DelegationSettled)
	if err != nil {
		return err
	}
	if owner != batch.OwnerConversationID {
		return errors.New("Delegation batch visible owner does not match its parent/run.")
	}
	ids, err := sqliteQueryIDs(v.ctx, v.tx, `SELECT id FROM data_delegation_invocations WHERE x_batchId=? ORDER BY CAST(x_ordinal AS INTEGER),id LIMIT 9`, id)
	if err != nil {
		return err
	}
	if len(ids) != batch.ExpectedChildren {
		return errors.New("Delegation batch has missing/extra members.")
	}
	seen := map[string]bool{}
	for ordinal, invID := range ids {
		var item DelegationInvocationRecord
		if err := v.get("delegation_invocations", invID, &item); err != nil {
			return err
		}
		if item.Ordinal != ordinal || seen[item.ChildSessionID] {
			return errors.New("Delegation membership must be contiguous and contain distinct children.")
		}
		seen[item.ChildSessionID] = true
		if err := v.validateDelegationInvocation(invID, batch); err != nil {
			return err
		}
		if err := v.validateManagedDelegationChild(item.ChildSessionID); err != nil {
			return err
		}
		if batch.State == DelegationSettled && item.State != DelegationInvocationTerminal {
			return errors.New("Settled delegation batch still has active members.")
		}
	}
	initial := false
	after := ""
	for {
		keys, err := sqliteQueryIDs(v.ctx, v.tx, `SELECT id FROM data_delegation_calls WHERE owner_id=? AND id>? ORDER BY id LIMIT 100`, id, after)
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			break
		}
		for _, key := range keys {
			var call DelegationNativeCall
			if err := v.get("delegation_calls", key, &call); err != nil {
				return err
			}
			if err := validateDelegationCall(call); err != nil {
				return err
			}
			if sameDelegationStoredCallIdentity(call, batch.NativeCall) {
				initial = true
			}
			after = key
		}
	}
	if !initial {
		return errors.New("Delegation batch is missing its initial native call alias.")
	}
	return nil
}

// Exhaustive streaming validation is for import/startup diagnostics; normal
// transactions validate only changed batches/children and their bounded members.
func (v sqliteValidation) validateDelegationRecords() error {
	if err := v.rejectRows("delegation orphan invocation", `SELECT 1 FROM data_delegation_invocations i LEFT JOIN data_delegation_batches b ON b.id=i.x_batchId WHERE b.id IS NULL`); err != nil {
		return err
	}
	if err := v.rejectRows("delegation orphan alias", `SELECT 1 FROM data_delegation_calls c LEFT JOIN data_delegation_batches b ON b.id=c.owner_id WHERE b.id IS NULL`); err != nil {
		return err
	}
	if err := v.each("delegation_batches", nil, v.validateDelegationBatch); err != nil {
		return err
	}
	if err := v.each("delegation_children", nil, v.validateManagedDelegationChild); err != nil {
		return err
	}
	return v.each("native_sessions", nil, func(id string) error {
		var native nativeSession
		if err := v.get("native_sessions", id, &native); err != nil {
			return err
		}
		if err := validateNativeDelegationShape(native); err != nil {
			return err
		}
		if native.Delegation != nil {
			var owner string
			if err := v.tx.QueryRowContext(v.ctx, `SELECT map_key FROM data_native_sessions WHERE id=?`, id).Scan(&owner); err != nil {
				return err
			}
			session, err := v.session(owner)
			if err != nil {
				return err
			}
			if session.Harness != "codex" || !delegationParentRole(session.Role) {
				return errors.New("Native delegation metadata belongs to an incompatible/leaf role.")
			}
		}
		return nil
	})
}

func (st *sqliteSessionTx) validateManagedSessionUpdate(next Session) error {
	var managed bool
	if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_delegation_children WHERE record_id=? OR x_parentSessionId=?)`, next.ID, next.ID).Scan(&managed); err != nil {
		return err
	}
	if !managed {
		return nil
	}
	previous, err := st.sessionMeta(next.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if previous.Role != next.Role || previous.ParentID != next.ParentID || previous.ProjectID != next.ProjectID || previous.Harness != next.Harness || previous.GraphRunID != next.GraphRunID || previous.GraphNodeID != next.GraphNodeID {
		return errors.New("Managed delegation session identity/role/harness cannot change.")
	}
	if next.Role == sessionRoleSubagent && (next.TopicID != "" || next.SelectedGraphID != "") {
		return errors.New("Managed child cannot acquire a topic or graph selector.")
	}
	if previous.ExecutionCWD != next.ExecutionCWD {
		if next.Role != "graph_node" {
			return errors.New("Managed execution directory is fixed.")
		}
		var active bool
		if err := st.tx.QueryRowContext(st.ctx, `SELECT EXISTS(SELECT 1 FROM data_delegation_children WHERE x_parentSessionId=? AND (x_activeInvocationId<>'' OR x_quarantined='true'))`, next.ID).Scan(&active); err != nil {
			return err
		}
		if active {
			return errors.New("Graph parent cannot move directory while a child lease remains unsettled.")
		}
	}
	return nil
}
