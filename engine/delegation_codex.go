package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Golden digest of the COMPLETE v1 function spec (including its description).
// A schema change needs an explicit new version/compatibility handler.
const delegationCodexSpecV1Digest = "2b9a6f688babd8f22a33a631ab0a707c77c050a9ad6a3214c9d390b4a83902c0"

// This manifest is created ONLY with a fresh native thread. Absence on an
// existing thread means normal chat without delegation, including general chat.
type DelegationCodexManifest struct {
	Version    int    `json:"version"`
	ToolName   string `json:"toolName"`
	SpecDigest string `json:"specDigest"`
}

type DelegationCodexSpec struct {
	Type         string         `json:"type"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	DeferLoading bool           `json:"deferLoading"`
}

func delegationCodexSpecV1() DelegationCodexSpec {
	return DelegationCodexSpec{Type: "function", Name: delegationToolName, Description: delegationToolDescription, InputSchema: delegationToolSchemaV1(), DeferLoading: false}
}

func delegationCodexManifestV1() (DelegationCodexManifest, error) {
	digest, err := delegationHash(delegationCodexSpecV1())
	if err == nil && digest != delegationCodexSpecV1Digest {
		return DelegationCodexManifest{}, errors.New("Codex v1 delegation specification changed without a protocol version change.")
	}
	return DelegationCodexManifest{Version: delegationSchemaVersion, ToolName: delegationToolName, SpecDigest: digest}, err
}

// A compatibility check, not an admission/platform/role grant. It does not mutate
// or register anything on a resumed history. Keep the v1 implementation stable.
func validateDelegationCodexManifest(manifest DelegationCodexManifest) error {
	expected, err := delegationCodexManifestV1()
	if err != nil {
		return err
	}
	if manifest != expected {
		return errors.New("Persisted Codex delegation schema is incompatible with this handler.")
	}
	return nil
}

type DelegationAvailability struct {
	Available bool   `json:"available"`
	Code      string `json:"code,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

func delegationAvailability(role, harness, nativeID string, manifest *DelegationCodexManifest) DelegationAvailability {
	if !delegationParentRole(role) || !managedLibraryHarness(harness) {
		return DelegationAvailability{Code: "role_unavailable", Detail: "Delegation is unavailable for this execution role."}
	}
	if harness == "codex" && nativeID != "" {
		if manifest == nil {
			return DelegationAvailability{Code: "native_history_without_delegation", Detail: "Delegation is unavailable in this existing Codex history."}
		}
		if err := validateDelegationCodexManifest(*manifest); err != nil {
			return DelegationAvailability{Code: "native_schema_incompatible", Detail: err.Error()}
		}
	}
	return DelegationAvailability{Available: true}
}

type DelegationCodexCall struct {
	ThreadID  string          `json:"threadId"`
	TurnID    string          `json:"turnId"`
	CallID    string          `json:"callId"`
	Namespace *string         `json:"namespace,omitempty"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// Invoke from the owning native reader, which authenticates the process and
// keeps consuming frames. This helper performs no app dispatch or blocking wait.
func decodeDelegationCodexCall(raw []byte, threadID, turnID string, manifest DelegationCodexManifest) (DelegationCodexCall, DelegationRunRequest, error) {
	var call DelegationCodexCall
	var request DelegationRunRequest
	if err := validateDelegationCodexManifest(manifest); err != nil {
		return call, request, err
	}
	if len(raw) > delegationRequestLimit || threadID == "" || turnID == "" {
		return call, request, errors.New("Invalid Codex delegation frame or binding.")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&call); err != nil {
		return call, request, err
	}
	if decoder.Decode(new(any)) != io.EOF || call.ThreadID != threadID || call.TurnID != turnID || !delegationText(call.CallID, 256) || call.Tool != delegationToolName || call.Namespace != nil && *call.Namespace != "" {
		return call, request, errors.New("Dynamic tool call does not belong to this bound native turn.")
	}
	request, err := decodeDelegationRequest(call.Arguments)
	return call, request, err
}

var errDelegationParentStopRequired = errors.New("Delegation finality is unconfirmed; stop the parent without returning a native tool result.")

// Caller retains the native JSON-RPC response ID (including numeric 0). This is
// only the response body; no result is sent until ALL children/drain are settled.
func delegationCodexResponse(result DelegationBatchResult) (map[string]any, error) {
	if err := result.Validate(); err != nil {
		return nil, err
	}
	if !result.CanContinue {
		return nil, errDelegationParentStopRequired
	}
	if result.Version != delegationSchemaVersion || result.BatchID == "" || len(result.Children) < 1 || len(result.Children) > delegationMaxChildren || !validDelegationOutcome(result.Outcome) {
		return nil, errors.New("Invalid consolidated delegation response.")
	}
	for _, child := range result.Children {
		if !child.Finality.Confirmed() || !validDelegationOutcome(child.Outcome) {
			return nil, errDelegationParentStopRequired
		}
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > delegationNativeResultLimit {
		return nil, errors.New("Delegation response exceeds the native transport limit.")
	}
	return map[string]any{"success": !result.IsError, "contentItems": []any{map[string]any{"type": "inputText", "text": string(raw)}}}, nil
}
