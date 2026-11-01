package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	delegationRecordVersion = 1
	delegationMaxChildren   = 8
	delegationConcurrency   = 4
)

type DelegationBatchState string
type DelegationInvocationState string
type DelegationOutcome string
type DelegationDelivery string

const (
	DelegationAdmitted   DelegationBatchState = "admitted"
	DelegationPreparing  DelegationBatchState = "preparing"
	DelegationRunning    DelegationBatchState = "running"
	DelegationJoining    DelegationBatchState = "joining"
	DelegationCancelling DelegationBatchState = "cancelling"
	DelegationSettled    DelegationBatchState = "settled"

	DelegationInvocationAdmitted  DelegationInvocationState = "admitted"
	DelegationInvocationPreparing DelegationInvocationState = "preparing"
	DelegationInvocationRunning   DelegationInvocationState = "running"
	DelegationInvocationDraining  DelegationInvocationState = "draining"
	DelegationInvocationTerminal  DelegationInvocationState = "terminal"

	DelegationCompleted           DelegationOutcome = "completed"
	DelegationFailed              DelegationOutcome = "failed"
	DelegationCancelled           DelegationOutcome = "cancelled"
	DelegationInterrupted         DelegationOutcome = "interrupted"
	DelegationFinalityUnconfirmed DelegationOutcome = "finality_unconfirmed"

	DelegationDeliveryPending   DelegationDelivery = "pending"
	DelegationDeliverySending   DelegationDelivery = "sending"
	DelegationDeliveryDelivered DelegationDelivery = "delivered"
	DelegationDeliveryUncertain DelegationDelivery = "uncertain"
	DelegationDeliveryCancelled DelegationDelivery = "cancelled"
)

// Graph scope belongs to an activation, not merely to a reusable native session.
type DelegationGraphScope struct {
	RunID          string `json:"runId"`
	NodeID         string `json:"nodeId"`
	ActivationID   string `json:"activationId"`
	TurnID         string `json:"turnId"`
	WorkspaceID    string `json:"workspaceId"`
	SnapshotDigest string `json:"snapshotDigest"`
}

type DelegationNativeCall struct {
	ParentTurnID string             `json:"parentTurnId"`
	SessionID    string             `json:"sessionId"`
	TurnID       string             `json:"turnId,omitempty"`
	CallID       string             `json:"callId"`
	Delivery     DelegationDelivery `json:"delivery,omitempty"`
}

// Parent-scoped operation receipt. Growing membership/call aliases/results are
// separate records; the root is not an ever-growing embedded history.
type DelegationBatchRecord struct {
	Version               int                    `json:"version"`
	ID                    string                 `json:"id"`
	ParentSessionID       string                 `json:"parentSessionId"`
	OwnerConversationID   string                 `json:"ownerConversationId"`
	ParentTurnID          string                 `json:"parentTurnId"`
	OperationID           string                 `json:"operationId"`
	RequestHash           string                 `json:"requestHash"`
	Revision              string                 `json:"revision"`
	NativeCall            DelegationNativeCall   `json:"nativeCall"`
	Calls                 []DelegationNativeCall `json:"calls,omitempty"` // split child collection: retry aliases
	Graph                 *DelegationGraphScope  `json:"graph,omitempty"`
	State                 DelegationBatchState   `json:"state"`
	Delivery              DelegationDelivery     `json:"delivery"`
	DeliveryTurnID        string                 `json:"deliveryTurnId,omitempty"`
	Outcome               DelegationOutcome      `json:"outcome,omitempty"`
	ExpectedChildren      int                    `json:"expectedChildren"`
	CancellationRequested bool                   `json:"cancellationRequested,omitempty"`
	ResultDigest          string                 `json:"resultDigest,omitempty"`
	Error                 string                 `json:"error,omitempty"`
	CreatedAt             string                 `json:"createdAt"`
	UpdatedAt             string                 `json:"updatedAt"`
	EndedAt               string                 `json:"endedAt,omitempty"`
}

type DelegationUncertainCall struct {
	SessionID string `json:"sessionId"`
	TurnID    string `json:"turnId,omitempty"`
	CallID    string `json:"callId"`
	Tool      string `json:"tool"`
	Reason    string `json:"reason"`
}

type DelegationFinality struct {
	NativeSettled      bool                      `json:"nativeSettled"`
	ProcessesDrained   bool                      `json:"processesDrained"`
	ToolCallsSettled   bool                      `json:"toolCallsSettled"`
	UncertainToolCalls []DelegationUncertainCall `json:"uncertainToolCalls,omitempty"`
}

func (f DelegationFinality) Confirmed() bool {
	return f.NativeSettled && f.ProcessesDrained && f.ToolCallsSettled && len(f.UncertainToolCalls) == 0
}

// Frozen configuration reuses A's authoritative types. This is an execution
// capture, not a second saved-agent registry/binding or a source of role tools.
type DelegationInvocationRecord struct {
	Version           int                       `json:"version"`
	ID                string                    `json:"id"`
	BatchID           string                    `json:"batchId"`
	Key               string                    `json:"key"`
	Ordinal           int                       `json:"ordinal"`
	ChildSessionID    string                    `json:"childSessionId"`
	ParentSessionID   string                    `json:"parentSessionId"`
	ParentTurnID      string                    `json:"parentTurnId"`
	Graph             *DelegationGraphScope     `json:"graph,omitempty"`
	State             DelegationInvocationState `json:"state"`
	Outcome           DelegationOutcome         `json:"outcome,omitempty"`
	TaskOutcome       string                    `json:"taskOutcome,omitempty"`
	Capture           AgentExecutionCapture     `json:"capture"`
	BindingRevision   string                    `json:"bindingRevision,omitempty"`
	ExecutionCWD      string                    `json:"executionCwd"`
	Task              string                    `json:"task"`
	Result            string                    `json:"result,omitempty"`
	ResultDigest      string                    `json:"resultDigest,omitempty"`
	Error             string                    `json:"error,omitempty"`
	DispatchAttempted bool                      `json:"dispatchAttempted,omitempty"`
	NativeAccepted    bool                      `json:"nativeAccepted,omitempty"`
	NativeSessionID   string                    `json:"nativeSessionId,omitempty"`
	NativeTurnID      string                    `json:"nativeTurnId,omitempty"`
	Finality          DelegationFinality        `json:"finality"`
	CreatedAt         string                    `json:"createdAt"`
	EventStart        int64                     `json:"eventStart"`
	EventEnd          int64                     `json:"eventEnd,omitempty"`
	StartedAt         string                    `json:"startedAt,omitempty"`
	EndedAt           string                    `json:"endedAt,omitempty"`
}

// Hidden Session/native history remains authoritative. The optional saved-agent
// binding belongs to A's child Session record, never duplicated in this ledger.
type DelegationChildRecord struct {
	Version            int    `json:"version"`
	ID                 string `json:"id"` // same value as the hidden child Session.ID
	ParentSessionID    string `json:"parentSessionId"`
	Origin             string `json:"origin"` // engine; legacy native children have no row
	Harness            string `json:"harness"`
	ExecutionCWD       string `json:"executionCwd"`
	GraphRunID         string `json:"graphRunId,omitempty"`
	WorkspaceID        string `json:"workspaceId,omitempty"`
	ActiveInvocationID string `json:"activeInvocationId,omitempty"`
	Quarantined        bool   `json:"quarantined,omitempty"`
	CreatedAt          string `json:"createdAt"`
}

func validDelegationOutcome(value DelegationOutcome) bool {
	switch value {
	case DelegationCompleted, DelegationFailed, DelegationCancelled, DelegationInterrupted, DelegationFinalityUnconfirmed:
		return true
	}
	return false
}

func (s *DelegationGraphScope) Validate() error {
	if s == nil {
		return nil
	}
	if s.RunID == "" || s.NodeID == "" || s.ActivationID == "" || s.TurnID == "" || s.WorkspaceID == "" || !delegationDigest(s.SnapshotDigest) {
		return errors.New("Incomplete delegation activation/workspace/snapshot identity.")
	}
	return nil
}

func (r DelegationBatchRecord) Validate() error {
	if r.Version != delegationRecordVersion || r.ID == "" || r.ParentSessionID == "" || r.OwnerConversationID == "" || r.ParentTurnID == "" || r.CreatedAt == "" || r.UpdatedAt == "" || !delegationRevision(r.Revision) {
		return errors.New("Invalid delegation batch identity or version.")
	}
	if !delegationText(r.OperationID, 200) || !delegationDigest(r.RequestHash) || r.ExpectedChildren < 1 || r.ExpectedChildren > delegationMaxChildren || r.NativeCall.ParentTurnID != r.ParentTurnID || r.NativeCall.SessionID == "" || r.NativeCall.CallID == "" {
		return errors.New("Invalid delegation batch receipt or membership count.")
	}
	switch r.State {
	case DelegationAdmitted, DelegationPreparing, DelegationRunning, DelegationJoining, DelegationCancelling:
		if r.Outcome != "" || r.EndedAt != "" || r.Delivery != "" || r.ResultDigest != "" {
			return errors.New("An active delegation batch cannot have a terminal result or delivery.")
		}
	case DelegationSettled:
		if !validDelegationOutcome(r.Outcome) || r.EndedAt == "" || !delegationDigest(r.ResultDigest) {
			return errors.New("Settled delegation batch requires its immutable result digest.")
		}
		switch r.Delivery {
		case DelegationDeliveryPending, DelegationDeliverySending, DelegationDeliveryDelivered, DelegationDeliveryUncertain, DelegationDeliveryCancelled:
		default:
			return errors.New("Invalid delegation result delivery state.")
		}
	default:
		return errors.New("Invalid delegation batch state.")
	}
	return r.Graph.Validate()
}

func (r DelegationInvocationRecord) Validate() error {
	if r.Version != delegationRecordVersion || r.ID == "" || r.BatchID == "" || r.ChildSessionID == "" || r.ParentSessionID == "" || r.ParentTurnID == "" || r.CreatedAt == "" || !delegationText(r.Key, 80) || r.Ordinal < 0 || r.Ordinal >= delegationMaxChildren || !delegationText(r.Task, 128<<10) || !filepath.IsAbs(r.ExecutionCWD) {
		return errors.New("Invalid delegation invocation identity, task or directory.")
	}
	if !managedLibraryHarness(r.Capture.Config.Harness) || !delegationText(r.Capture.Config.Model, 256) {
		return errors.New("Delegation invocation requires resolved harness/model settings.")
	}
	selection := copySelection(r.Capture.Config.Selection)
	if err := normalizeSelection(&selection); err != nil {
		return err
	}
	if r.Capture.ExternalCapabilities != nil {
		for _, skill := range r.Capture.ExternalCapabilities.Skills {
			if skill.Internal {
				return errors.New("Child execution cannot capture internal role skills.")
			}
		}
	}
	if r.NativeAccepted && (!r.DispatchAttempted || r.NativeSessionID == "") {
		return errors.New("Native delegation acceptance lacks a dispatch/native identity.")
	}
	if r.State == DelegationInvocationRunning && (!r.NativeAccepted || r.StartedAt == "") {
		return errors.New("Running child execution requires correlated native acceptance.")
	}
	if r.TaskOutcome != "" && r.TaskOutcome != "unknown" && r.TaskOutcome != "satisfied" && r.TaskOutcome != "unsatisfied" {
		return errors.New("Invalid explicit child task outcome.")
	}
	switch r.State {
	case DelegationInvocationAdmitted, DelegationInvocationPreparing, DelegationInvocationRunning, DelegationInvocationDraining:
		if r.Outcome != "" || r.EndedAt != "" || r.ResultDigest != "" {
			return errors.New("Active child execution cannot publish a terminal result.")
		}
	case DelegationInvocationTerminal:
		if !validDelegationOutcome(r.Outcome) || r.EndedAt == "" || !delegationDigest(r.ResultDigest) {
			return errors.New("Terminal child execution requires an outcome and immutable result.")
		}
		if r.Outcome == DelegationCompleted && (!r.NativeAccepted || !r.Finality.Confirmed()) {
			return errors.New("Unaccepted or undrained child execution cannot complete successfully.")
		}
		if digest, err := delegationInvocationResultDigest(r); err != nil || digest != r.ResultDigest {
			return errors.New("Child terminal content does not match its immutable result digest.")
		}
	default:
		return errors.New("Invalid child execution state.")
	}
	return r.Graph.Validate()
}

func (r DelegationChildRecord) Validate() error {
	if r.Version != delegationRecordVersion || r.ID == "" || r.ParentSessionID == "" || r.ID == r.ParentSessionID || r.Origin != "engine" || !managedLibraryHarness(r.Harness) || !filepath.IsAbs(r.ExecutionCWD) || r.CreatedAt == "" {
		return errors.New("Invalid managed child identity or fixed execution settings.")
	}
	if (r.GraphRunID == "") != (r.WorkspaceID == "") {
		return errors.New("Graph child reuse requires both run and workspace identity.")
	}
	return nil
}

func (r DelegationChildRecord) Reusable() bool {
	return r.Validate() == nil && !r.Quarantined && r.ActiveInvocationID == ""
}

func finishDelegationChild(record DelegationChildRecord, item DelegationInvocationRecord) (DelegationChildRecord, error) {
	if err := item.Validate(); err != nil {
		return record, err
	}
	if item.State != DelegationInvocationTerminal || record.ActiveInvocationID != item.ID || record.ID != item.ChildSessionID || record.ParentSessionID != item.ParentSessionID || record.Harness != item.Capture.Config.Harness || filepath.Clean(record.ExecutionCWD) != filepath.Clean(item.ExecutionCWD) {
		return record, errors.New("Child lease does not belong to this terminal invocation.")
	}
	if !item.Finality.Confirmed() || item.Outcome == DelegationFinalityUnconfirmed {
		record.Quarantined = true // Do not release an uncertain active lease for reuse.
	} else {
		record.ActiveInvocationID, record.Quarantined = "", false
	}
	return record, record.Validate()
}

// Structural ownership only. The persistence caller additionally validates the
// actual run/activation/workspace/snapshot and reserves leases in one transaction.
func validateDelegationOwnership(child, parent Session, record DelegationChildRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if !delegationParentRole(parent.Role) || child.Role != sessionRoleSubagent || child.ID != record.ID || child.ParentID != parent.ID || record.ParentSessionID != parent.ID || child.ProjectID != parent.ProjectID || child.Harness != record.Harness || child.TopicID != "" || child.SelectedGraphID != "" || child.GraphRunID != "" || child.GraphNodeID != "" {
		return errors.New("Managed child belongs to a different parent, role or execution scope.")
	}
	if parent.Role == "" && parent.ParentID != "" || parent.Role == sessionRoleSideAgent && parent.ParentID == "" || parent.Role == sessionRoleGeneralAgent && (parent.ParentID != "" || parent.ProjectID != "" || parent.GraphRunID != "") {
		return errors.New("Invalid delegation parent role identity.")
	}
	if parent.Role == "graph_node" {
		if parent.GraphRunID == "" || record.GraphRunID != parent.GraphRunID || filepath.Clean(record.ExecutionCWD) != filepath.Clean(parent.ExecutionCWD) {
			return errors.New("Graph child is outside its parent's run/workspace.")
		}
	} else if record.GraphRunID != "" || child.GraphRunID != "" {
		return errors.New("Ordinary child cannot acquire graph ownership.")
	}
	return nil
}

func advanceDelegationInvocation(r DelegationInvocationRecord, next DelegationInvocationState) (DelegationInvocationRecord, error) {
	allowed := r.State == DelegationInvocationAdmitted && next == DelegationInvocationPreparing ||
		r.State == DelegationInvocationPreparing && next == DelegationInvocationRunning ||
		(r.State == DelegationInvocationAdmitted || r.State == DelegationInvocationPreparing || r.State == DelegationInvocationRunning) && next == DelegationInvocationDraining
	if !allowed {
		return r, fmt.Errorf("Invalid child transition %s -> %s.", r.State, next)
	}
	r.State = next
	return r, r.Validate()
}

func advanceDelegationBatch(r DelegationBatchRecord, next DelegationBatchState, stamp string) (DelegationBatchRecord, error) {
	allowed := r.State == DelegationAdmitted && next == DelegationPreparing ||
		r.State == DelegationPreparing && next == DelegationRunning ||
		(r.State == DelegationPreparing || r.State == DelegationRunning || r.State == DelegationCancelling) && next == DelegationJoining ||
		(r.State == DelegationAdmitted || r.State == DelegationPreparing || r.State == DelegationRunning || r.State == DelegationJoining) && next == DelegationCancelling
	if !allowed || strings.TrimSpace(stamp) == "" {
		return r, fmt.Errorf("Invalid delegation batch transition %s -> %s.", r.State, next)
	}
	r.State, r.UpdatedAt = next, stamp
	if next == DelegationCancelling {
		r.CancellationRequested = true
	}
	return r, r.Validate()
}

func advanceDelegationDelivery(r DelegationBatchRecord, next DelegationDelivery) (DelegationBatchRecord, error) {
	if r.State != DelegationSettled {
		return r, errors.New("Cannot deliver an unsettled delegation batch.")
	}
	allowed := r.Delivery == DelegationDeliveryPending && (next == DelegationDeliverySending || next == DelegationDeliveryCancelled) ||
		r.Delivery == DelegationDeliverySending && (next == DelegationDeliveryDelivered || next == DelegationDeliveryUncertain || next == DelegationDeliveryCancelled)
	if !allowed {
		return r, errors.New("Invalid or repeated delegation delivery transition.")
	}
	r.Delivery = next
	return r, r.Validate()
}
