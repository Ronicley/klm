package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Targeted pure contract checks only; no harness, SQLite or model is started.
func TestDelegationContracts(t *testing.T) {
	request := DelegationRunRequest{OperationID: "probe-operation", Children: []DelegationChildRequest{{Key: "one", Task: "Return the fixture result."}, {Key: "two", Task: "Return another fixture result."}}}
	raw, _ := json.Marshal(request)
	if _, err := decodeDelegationRequest(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"operationId":"x","children":[]}`,
		`{"operationId":"x","children":[{"key":"one","task":"x","async":true}]}`,
		`{"operationId":"x","children":[{"key":"one","task":"x","model":null}]}`,
		`{"operationId":"x","children":[{"key":"one","task":"x"},{"key":"one","task":"y"}]}`,
	} {
		if _, err := decodeDelegationRequest([]byte(bad)); err == nil {
			t.Fatalf("accepted invalid request %s", bad)
		}
	}
	hash, _ := delegationHash(request)
	batch := DelegationBatchRecord{Version: 1, ID: "batch", ParentSessionID: "parent", OwnerConversationID: "parent", ParentTurnID: "epoch", OperationID: request.OperationID, RequestHash: hash, Revision: "0", NativeCall: DelegationNativeCall{ParentTurnID: "epoch", SessionID: "native-parent", CallID: "call"}, State: DelegationJoining, ExpectedChildren: 2, CreatedAt: "fixture-time", UpdatedAt: "fixture-time"}
	if err := validateDelegationAdmission("", request, &batch); err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.OperationID = "another-operation"
	if err := validateDelegationAdmission("", changed, &batch); err == nil {
		t.Fatal("independent batch passed join barrier")
	}
	changed.OperationID = request.OperationID
	changed.Children = []DelegationChildRequest{{Key: "one", Task: "Changed task"}}
	if err := validateDelegationAdmission("", changed, &batch); err == nil {
		t.Fatal("operationId payload conflict accepted")
	}
	if err := validateDelegationAdmission(sessionRoleSubagent, request, nil); err == nil {
		t.Fatal("leaf admitted another batch")
	}
	if slots, err := delegationDispatchSlots(3); err != nil || slots != 1 {
		t.Fatal("wrong dispatch capacity", slots, err)
	}
	if _, err := delegationDispatchSlots(5); err == nil {
		t.Fatal("over-capacity child count accepted")
	}

	finality := DelegationFinality{NativeSettled: true, ProcessesDrained: true, ToolCallsSettled: true}
	children := []DelegationInvocationRecord{}
	for i, child := range request.Children {
		item := DelegationInvocationRecord{Version: 1, ID: child.Key, BatchID: batch.ID, Key: child.Key, Ordinal: i, ChildSessionID: "child-" + child.Key, ParentSessionID: batch.ParentSessionID, ParentTurnID: batch.ParentTurnID, State: DelegationInvocationDraining, Capture: AgentExecutionCapture{Config: ResolvedAgentConfig{Harness: "pi", Model: "fixture", Selection: CapabilitySelection{}}}, ExecutionCWD: t.TempDir(), Task: child.Task, DispatchAttempted: true, NativeAccepted: true, NativeSessionID: "native-" + child.Key, CreatedAt: "fixture-time"}
		outcome := DelegationCompleted
		if i == 1 {
			outcome = DelegationFailed
		}
		item, err := completeDelegationInvocation(item, outcome, finality, "fixture-result", "", "fixture-ended")
		if err != nil {
			t.Fatal(err)
		}
		children = append(children, item)
	}
	if _, err := buildDelegationResult(batch, children[:1]); err == nil {
		t.Fatal("partial batch was returned")
	}
	settled, result, err := settleDelegationBatch(batch, children, "fixture-ended")
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !result.CanContinue || len(result.Children) != 2 || result.Outcome != DelegationFailed {
		t.Fatal("mixed batch was misreported", result)
	}
	response, err := delegationCodexResponse(result)
	if err != nil || response["success"] != false {
		t.Fatal("native failed batch became success", response, err)
	}
	if _, err := advanceDelegationDelivery(batch, DelegationDeliverySending); err == nil {
		t.Fatal("active batch delivered")
	}
	if _, err := advanceDelegationDelivery(settled, DelegationDeliveryDelivered); err == nil {
		t.Fatal("delivery skipped native sending boundary")
	}
	corrupt := children[0]
	corrupt.Result = "changed after terminal settlement"
	if corrupt.Validate() == nil {
		t.Fatal("changed terminal content retained a valid digest")
	}
	large := children[0]
	large.State, large.Outcome, large.EndedAt, large.ResultDigest = DelegationInvocationDraining, "", "", ""
	large, err = completeDelegationInvocation(large, DelegationCompleted, finality, strings.Repeat("x", delegationNativeResultLimit), "", "fixture-ended")
	if err != nil {
		t.Fatal(err)
	}
	longResult, err := buildDelegationResult(batch, []DelegationInvocationRecord{large, children[1]})
	if err != nil || len(longResult.Children) != 2 || !longResult.Children[0].ResultTruncated || longResult.Children[0].ResultReference == nil || longResult.Children[0].ResultReference.TotalBytes != int64(len(large.Result)) {
		t.Fatal("long result lost a member or its full-output reference", err)
	}
	page, err := delegationOutputPage(large, longResult.Children[0].ResultReference.Digest, 0, 1024)
	if err != nil || len(page.Content) != 1024 || !page.HasMore {
		t.Fatal("bounded output continuation failed", err)
	}
	children[1].State, children[1].Outcome, children[1].EndedAt, children[1].ResultDigest = DelegationInvocationDraining, "", "", ""
	unconfirmed := finality
	unconfirmed.ProcessesDrained = false
	children[1], err = completeDelegationInvocation(children[1], DelegationFailed, unconfirmed, "fixture-result", "Shutdown unconfirmed", "fixture-ended")
	if err != nil {
		t.Fatal(err)
	}
	uncertain, err := buildDelegationResult(batch, children)
	if err != nil {
		t.Fatal(err)
	}
	if uncertain.CanContinue || uncertain.Outcome != DelegationFinalityUnconfirmed {
		t.Fatal("uncertain child released parent")
	}
	if _, err := delegationCodexResponse(uncertain); err == nil {
		t.Fatal("unsafe native response generated")
	}
	lease := DelegationChildRecord{Version: 1, ID: children[1].ChildSessionID, ParentSessionID: batch.ParentSessionID, Origin: "engine", Harness: "pi", ExecutionCWD: children[1].ExecutionCWD, ActiveInvocationID: children[1].ID, CreatedAt: "fixture-time"}
	lease, err = finishDelegationChild(lease, children[1])
	if err != nil || !lease.Quarantined || lease.Reusable() {
		t.Fatal("uncertain child lease was released", err)
	}
	manifest, err := delegationCodexManifestV1()
	if err != nil {
		t.Fatal(err)
	}
	frame, _ := json.Marshal(DelegationCodexCall{ThreadID: "thread", TurnID: "turn", CallID: "call", Tool: delegationToolName, Arguments: raw})
	if _, decoded, err := decodeDelegationCodexCall(frame, "thread", "turn", manifest); err != nil || decoded.OperationID != request.OperationID {
		t.Fatal("native dynamic call lost its correlation", err)
	}
	if _, _, err := decodeDelegationCodexCall(frame, "thread", "different-turn", manifest); err == nil {
		t.Fatal("dynamic call escaped its native turn binding")
	}
	if err := (DelegationWaitReply{Revision: "1", State: "waiting", Result: &result}).Validate(); err == nil {
		t.Fatal("waiting reply exposed a result")
	}
	if err := (DelegationWaitReply{Revision: "1", State: "stop", StopRequired: true}).Validate(); err != nil {
		t.Fatal(err)
	}
	if delegationAvailability(sessionRoleGeneralAgent, "codex", "old-thread", nil).Available {
		t.Fatal("legacy general thread was silently upgraded")
	}
	if !delegationAvailability("", "codex", "new-thread", &manifest).Available {
		t.Fatal("matching native manifest rejected")
	}
	manifest.Version++
	if delegationAvailability("", "codex", "new-thread", &manifest).Available {
		t.Fatal("schema mismatch accepted")
	}
	prompt, err := composeDelegationPrompt("Specialist instructions", "Assigned task")
	if err != nil || !strings.Contains(prompt, "leaf child") || !strings.Contains(prompt, "Assigned task") {
		t.Fatal("child prompt missing role/task", err)
	}

	// Optional fixture materialization for the explicitly owned protocol probes.
	if parent := os.Getenv("KLM_DELEGATION_PROBE_OUT"); parent != "" {
		if !filepath.IsAbs(parent) {
			t.Fatal("probe parent must be absolute")
		}
		info, err := os.Stat(parent)
		if err != nil || !info.IsDir() {
			t.Fatal("probe parent unavailable")
		}
		dir, err := os.MkdirTemp(parent, "delegation-assets-")
		if err != nil {
			t.Fatal(err)
		}
		files, err := delegationAssetFiles(DelegationAssetConfig{Enabled: true, BridgeURL: "http://127.0.0.1:1/mcp", BridgeToken: "fixture-owned-delegation-probe"})
		if err != nil {
			t.Fatal(err)
		}
		for name, data := range files {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		metadata, _ := json.Marshal(map[string]any{"schema": delegationToolSchemaV1(), "manifest": mustDelegationManifest(t)})
		if err := os.WriteFile(filepath.Join(dir, "metadata.json"), metadata, 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("DELEGATION_ASSET_DIR=" + dir)
	}
}

func mustDelegationManifest(t *testing.T) DelegationCodexManifest {
	t.Helper()
	manifest, err := delegationCodexManifestV1()
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}
