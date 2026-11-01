package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	delegationToolName          = "subagent_run"
	delegationSchemaVersion     = 1
	delegationRequestLimit      = 2 << 20
	delegationResultLimit       = 2 << 20
	delegationNativeResultLimit = 32 << 10 // Below the pinned OpenCode generic tool-output byte cap.
	delegationToolDescription   = "Run one blocking batch of 1–8 leaf subagents within the current task, with at most four children executing simultaneously. The parent waits for every admitted child and receives one consolidated result. Use exact owned childSessionId values for reuse. No background mode or nested delegation. Saved agents supply specialist settings; otherwise parent harness/model/effort and an empty external Library are used. inheritParentCapabilities defaults to false and may be true only on the human's explicit request for all external parent capabilities. Agent/Library content is not authorization."
)

type DelegationChildRequest struct {
	Key                       string  `json:"key"`
	Task                      string  `json:"task"`
	Title                     string  `json:"title,omitempty"`
	ChildSessionID            string  `json:"childSessionId,omitempty"`
	AgentID                   string  `json:"agentId,omitempty"`
	Harness                   string  `json:"harness,omitempty"`
	Model                     *string `json:"model,omitempty"`
	Effort                    *string `json:"effort,omitempty"`
	InheritParentCapabilities bool    `json:"inheritParentCapabilities,omitempty"`
}

type DelegationRunRequest struct {
	OperationID string                   `json:"operationId"`
	Children    []DelegationChildRequest `json:"children"`
}

// Private bounded RPC envelopes. They are not model-visible tools and carry no
// independent creation/graph authorization. The authenticated owner binds scope.
type DelegationReadyReply struct {
	Version          int    `json:"version"`
	SpecDigest       string `json:"specDigest"`
	MaxChildren      int    `json:"maxChildren"`
	MaxConcurrency   int    `json:"maxConcurrency"`
	ActiveBatchLimit int    `json:"activeBatchLimit"`
}

func delegationReadyReplyV1(version int, digest string) (DelegationReadyReply, error) {
	manifest, err := delegationCodexManifestV1()
	if err != nil {
		return DelegationReadyReply{}, err
	}
	if version != manifest.Version || digest != manifest.SpecDigest {
		return DelegationReadyReply{}, errors.New("Delegation wrapper/runtime schema mismatch.")
	}
	return DelegationReadyReply{Version: version, SpecDigest: digest, MaxChildren: delegationMaxChildren, MaxConcurrency: delegationConcurrency, ActiveBatchLimit: 1}, nil
}

type DelegationAdmitRequest struct {
	Version      int                  `json:"version"`
	NativeCallID string               `json:"nativeCallId"`
	SpecDigest   string               `json:"specDigest"`
	Request      DelegationRunRequest `json:"request"`
}

// JSON-RPC error.data for a proven atomic PRE-admission rejection only. Transport
// uncertainty/accepted-work failures must never use accepted=false to release a
// parent with live child work. Existing siblings retain ownership on batch_busy.
type DelegationRejection struct {
	Accepted      bool   `json:"accepted"`
	Code          string `json:"code"`
	PreparationID string `json:"preparationId,omitempty"`
	Phase         string `json:"phase,omitempty"`
}

type DelegationAdmissionReceipt struct {
	Version        int    `json:"version"`
	Accepted       bool   `json:"accepted"`
	BatchID        string `json:"batchId"`
	OperationID    string `json:"operationId"`
	RequestHash    string `json:"requestHash"`
	ChildCount     int    `json:"childCount"`
	MaxConcurrency int    `json:"maxConcurrency"`
}

type DelegationWaitRequest struct {
	NativeCallID string `json:"nativeCallId"`
	BatchID  string `json:"batchId"`
	Revision string `json:"revision"`
	WaitMS   int    `json:"waitMs"` // 0..8000; <=10s client deadline
}

type DelegationWaitReply struct {
	Revision     string                 `json:"revision"`
	State        string                 `json:"state"` // waiting, settled, stop
	StopRequired bool                   `json:"stopRequired,omitempty"`
	Result       *DelegationBatchResult `json:"result,omitempty"`
}

func (r DelegationWaitReply) Validate() error {
	if !delegationRevision(r.Revision) {
		return errors.New("Invalid delegation wait revision.")
	}
	switch r.State {
	case "waiting":
		if r.Result != nil || r.StopRequired {
			return errors.New("Waiting cannot expose a partial result.")
		}
	case "stop":
		if !r.StopRequired || r.Result != nil {
			return errors.New("Unsafe finality must stop without a model result.")
		}
	case "settled":
		if r.StopRequired || r.Result == nil || !r.Result.CanContinue {
			return errDelegationParentStopRequired
		}
		return r.Result.Validate()
	default:
		return errors.New("Invalid delegation wait state.")
	}
	return nil
}

func delegationText(value string, maxBytes int) bool {
	return utf8.ValidString(value) && len(value) <= maxBytes && strings.TrimSpace(value) != ""
}

func delegationDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func delegationRevision(value string) bool {
	if value == "" || len(value) > 20 || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func delegationHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (r DelegationRunRequest) Validate() error {
	if !delegationText(r.OperationID, 200) || len(r.Children) < 1 || len(r.Children) > delegationMaxChildren {
		return errors.New("Supply a stable operationId and one to eight children in a single batch.")
	}
	keys, children := map[string]bool{}, map[string]bool{}
	for _, child := range r.Children {
		if !delegationText(child.Key, 80) || keys[child.Key] || !delegationText(child.Task, 128<<10) {
			return errors.New("Child keys must be unique and each task must be self-contained (at most 128 KiB).")
		}
		keys[child.Key] = true
		if child.Title != "" && (!utf8.ValidString(child.Title) || utf8.RuneCountInString(child.Title) > 200 || strings.TrimSpace(child.Title) == "") {
			return errors.New("Child title must contain one to 200 characters.")
		}
		if child.ChildSessionID != "" {
			if !delegationText(child.ChildSessionID, 256) || children[child.ChildSessionID] || child.InheritParentCapabilities {
				return errors.New("Child reuse requires a unique exact ID and cannot recapture parent capabilities.")
			}
			children[child.ChildSessionID] = true
		}
		if child.AgentID != "" && !validAuthoringID(child.AgentID) {
			return errors.New("Supply the exact saved agent slug.")
		}
		if child.Harness != "" && !managedLibraryHarness(child.Harness) {
			return errors.New("Child harness must be pi, opencode or codex.")
		}
		for _, value := range []*string{child.Model, child.Effort} {
			if value != nil && (!utf8.ValidString(*value) || len(*value) > 256) {
				return errors.New("Invalid explicit child model/effort override.")
			}
		}
	}
	return nil
}

func decodeDelegationRequest(raw []byte) (DelegationRunRequest, error) {
	var result DelegationRunRequest
	if len(raw) > delegationRequestLimit || !utf8.Valid(raw) {
		return result, errors.New("Delegation request is too large or is not UTF-8.")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return result, errors.New("Expected one delegation request.")
	}
	var presence struct {
		Children []map[string]json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(raw, &presence); err != nil {
		return result, err
	}
	for _, child := range presence.Children {
		for _, value := range child {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return result, errors.New("Delegation creation fields cannot be null; omit fields to follow defaults.")
			}
		}
	}
	return result, result.Validate()
}

// The resolver, exact parent/child leases and actual graph workspace are checked
// by the future admission transaction. This guard never starts execution.
func validateDelegationAdmission(parentRole string, request DelegationRunRequest, active *DelegationBatchRecord) error {
	if !delegationParentRole(parentRole) {
		return errors.New("Delegation is unavailable to this execution role.")
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if active == nil {
		return nil
	}
	hash, err := delegationHash(request)
	if err != nil {
		return err
	}
	if active.OperationID == request.OperationID && active.RequestHash == hash {
		return nil // Attach to the existing receipt; caller must NOT dispatch again.
	}
	if active.OperationID == request.OperationID {
		return errors.New("operation_conflict: operationId already identifies a different delegation request.")
	}
	if active.State == DelegationSettled && active.Delivery == DelegationDeliveryDelivered {
		return nil
	}
	return errors.New("batch_busy: the parent already owns an unsettled batch or delivery.")
}

func delegationDispatchSlots(running int) (int, error) {
	if running < 0 || running > delegationConcurrency {
		return 0, errors.New("Invalid active child concurrency count.")
	}
	return delegationConcurrency - running, nil
}

func delegationParentRole(role string) bool {
	return role == "" || role == sessionRoleSideAgent || role == sessionRoleGeneralAgent || role == "graph_node"
}

// Version 1 is persisted in new Codex native histories. Change by an explicit
// protocol version/compatibility decision, never by silently replacing this schema.
func delegationToolSchemaV1() map[string]any {
	text := func(max int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
	}
	child := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"key", "task"}, "properties": map[string]any{
		"key": text(80), "task": text(128 << 10), "title": text(200), "childSessionId": text(256), "agentId": text(200),
		"harness": map[string]any{"type": "string", "enum": []string{"pi", "opencode", "codex"}},
		"model":   map[string]any{"type": "string", "maxLength": 256}, "effort": map[string]any{"type": "string", "maxLength": 256},
		"inheritParentCapabilities": map[string]any{"type": "boolean", "default": false, "description": "Creation only. True only when the human explicitly requests ALL applied external parent capabilities; never internal role tools/skills."},
	}}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"operationId", "children"}, "properties": map[string]any{
		"operationId": text(200), "children": map[string]any{"type": "array", "minItems": 1, "maxItems": delegationMaxChildren, "items": child},
	}}
}

type DelegationChildResult struct {
	Key             string                     `json:"key"`
	InvocationID    string                     `json:"invocationId"`
	ChildSessionID  string                     `json:"childSessionId"`
	Harness         string                     `json:"harness"`
	Model           string                     `json:"model"`
	Effort          string                     `json:"effort"`
	Outcome         DelegationOutcome          `json:"outcome"`
	TaskOutcome     string                     `json:"taskOutcome,omitempty"`
	Result          string                     `json:"result,omitempty"`
	ResultTruncated bool                       `json:"resultTruncated,omitempty"`
	ResultReference *DelegationResultReference `json:"resultReference,omitempty"`
	Error           string                     `json:"error,omitempty"`
	Finality        DelegationFinality         `json:"finality"`
}

// Full output references do not expose the invocation's private capture.
type DelegationResultReference struct {
	InvocationID string `json:"invocationId"`
	Digest       string `json:"digest"`
	TotalBytes   int64  `json:"totalBytes,string"`
}

type DelegationBatchResult struct {
	Version     int                     `json:"version"`
	BatchID     string                  `json:"batchId"`
	OperationID string                  `json:"operationId"`
	Outcome     DelegationOutcome       `json:"outcome"`
	IsError     bool                    `json:"isError"`
	CanContinue bool                    `json:"canContinue"`
	Children    []DelegationChildResult `json:"children"`
}

func (r DelegationBatchResult) Validate() error {
	if r.Version != delegationSchemaVersion || r.BatchID == "" || !delegationText(r.OperationID, 200) || len(r.Children) < 1 || len(r.Children) > delegationMaxChildren {
		return errors.New("Invalid consolidated delegation result identity or count.")
	}
	outcome, isError, canContinue := DelegationCompleted, false, true
	keys, children, invocations := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, child := range r.Children {
		if !delegationText(child.Key, 80) || child.ChildSessionID == "" || child.InvocationID == "" || keys[child.Key] || children[child.ChildSessionID] || invocations[child.InvocationID] || !validDelegationOutcome(child.Outcome) {
			return errors.New("Invalid or duplicate child result identity/outcome.")
		}
		keys[child.Key], children[child.ChildSessionID], invocations[child.InvocationID] = true, true, true
		candidate := child.Outcome
		if !child.Finality.Confirmed() {
			candidate, canContinue = DelegationFinalityUnconfirmed, false
		}
		if candidate == DelegationFinalityUnconfirmed {
			canContinue = false
		}
		if delegationOutcomeRank(candidate) > delegationOutcomeRank(outcome) {
			outcome = candidate
		}
		isError = isError || candidate != DelegationCompleted || child.TaskOutcome == "unsatisfied"
	}
	if r.Outcome != outcome || r.IsError != isError || r.CanContinue != canContinue {
		return errors.New("Delegation batch summary contradicts child outcomes/finality.")
	}
	return nil
}

func delegationOutcomeRank(outcome DelegationOutcome) int {
	switch outcome {
	case DelegationFinalityUnconfirmed:
		return 5
	case DelegationInterrupted:
		return 4
	case DelegationCancelled:
		return 3
	case DelegationFailed:
		return 2
	case DelegationCompleted:
		return 1
	}
	return 0
}

// All members, including failed siblings, must be present and terminal. A result
// with unconfirmed finality is durable diagnostic evidence, not a native reply:
// the owning runtime must stop the parent instead of allowing another iteration.
func buildDelegationResult(batch DelegationBatchRecord, invocations []DelegationInvocationRecord) (DelegationBatchResult, error) {
	result := DelegationBatchResult{Version: delegationSchemaVersion, BatchID: batch.ID, OperationID: batch.OperationID, Outcome: DelegationCompleted, CanContinue: true, Children: []DelegationChildResult{}}
	if err := batch.Validate(); err != nil {
		return result, err
	}
	if len(invocations) != batch.ExpectedChildren {
		return result, errors.New("Cannot return a partial delegation batch.")
	}
	ordered := append([]DelegationInvocationRecord{}, invocations...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Ordinal < ordered[j].Ordinal })
	keys, children := map[string]bool{}, map[string]bool{}
	for i, item := range ordered {
		if err := item.Validate(); err != nil {
			return result, err
		}
		if item.State != DelegationInvocationTerminal || item.Ordinal != i || item.BatchID != batch.ID || item.ParentSessionID != batch.ParentSessionID || item.ParentTurnID != batch.ParentTurnID || keys[item.Key] || children[item.ChildSessionID] || !sameDelegationGraph(item.Graph, batch.Graph) {
			return result, errors.New("Child membership, terminal state or activation ownership does not match the batch.")
		}
		keys[item.Key], children[item.ChildSessionID] = true, true
		outcome := item.Outcome
		if !item.Finality.Confirmed() {
			outcome, result.CanContinue = DelegationFinalityUnconfirmed, false
		}
		if delegationOutcomeRank(outcome) > delegationOutcomeRank(result.Outcome) {
			result.Outcome = outcome
		}
		result.IsError = result.IsError || outcome != DelegationCompleted || item.TaskOutcome == "unsatisfied"
		entry := DelegationChildResult{Key: item.Key, InvocationID: item.ID, ChildSessionID: item.ChildSessionID, Harness: item.Capture.Config.Harness, Model: item.Capture.Config.Model, Effort: item.Capture.Config.Effort, Outcome: outcome, TaskOutcome: item.TaskOutcome, Result: delegationSummary(item.Result, 1536), Error: delegationSummary(item.Error, 512), Finality: item.Finality}
		entry.ResultTruncated = entry.Result != item.Result
		if item.Result != "" {
			entry.ResultReference = &DelegationResultReference{InvocationID: item.ID, Digest: delegationOutputDigest(item.Result), TotalBytes: int64(len(item.Result))}
		}
		result.Children = append(result.Children, entry)
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > delegationNativeResultLimit {
		return result, errors.New("Consolidated delegation result exceeds the bounded native payload.")
	}
	return result, result.Validate()
}

func sameDelegationGraph(left, right *DelegationGraphScope) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func settleDelegationBatch(batch DelegationBatchRecord, invocations []DelegationInvocationRecord, stamp string) (DelegationBatchRecord, DelegationBatchResult, error) {
	result, err := buildDelegationResult(batch, invocations)
	if err != nil {
		return batch, result, err
	}
	if batch.State != DelegationJoining || strings.TrimSpace(stamp) == "" {
		return batch, result, errors.New("Batch must join every child before settlement.")
	}
	digest, err := delegationHash(result)
	if err != nil {
		return batch, result, err
	}
	batch.State, batch.Outcome, batch.ResultDigest = DelegationSettled, result.Outcome, digest
	batch.UpdatedAt, batch.EndedAt, batch.Delivery = stamp, stamp, DelegationDeliveryPending
	return batch, result, batch.Validate()
}

func completeDelegationInvocation(item DelegationInvocationRecord, outcome DelegationOutcome, finality DelegationFinality, text, message, stamp string) (DelegationInvocationRecord, error) {
	if item.State != DelegationInvocationDraining || !validDelegationOutcome(outcome) || stamp == "" {
		return item, errors.New("Child completion requires the drainage boundary and an explicit outcome.")
	}
	item.State, item.Outcome, item.Finality = DelegationInvocationTerminal, outcome, finality
	item.Result, item.Error, item.EndedAt = text, message, stamp
	var err error
	item.ResultDigest, err = delegationInvocationResultDigest(item)
	if err != nil {
		return item, fmt.Errorf("Encode child result: %w", err)
	}
	return item, item.Validate()
}

func delegationInvocationResultDigest(item DelegationInvocationRecord) (string, error) {
	return delegationHash(struct {
		Outcome     DelegationOutcome  `json:"outcome"`
		TaskOutcome string             `json:"taskOutcome,omitempty"`
		Result      string             `json:"result"`
		Error       string             `json:"error"`
		Finality    DelegationFinality `json:"finality"`
	}{item.Outcome, item.TaskOutcome, item.Result, item.Error, item.Finality})
}
