package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDelegationOutputSummaries(t *testing.T) {
	batch := DelegationBatchRecord{Version: 1, ID: "batch", ParentSessionID: "parent", OwnerConversationID: "parent", ParentTurnID: "turn", OperationID: "operation", RequestHash: strings.Repeat("a", 64), Revision: "0", State: DelegationJoining, ExpectedChildren: 8, NativeCall: DelegationNativeCall{ParentTurnID: "turn", SessionID: "native", CallID: "call"}, CreatedAt: "fixture", UpdatedAt: "fixture"}
	finality := DelegationFinality{NativeSettled: true, ProcessesDrained: true, ToolCallsSettled: true}
	members := make([]DelegationInvocationRecord, 0, 8)
	for index := 0; index < 8; index++ {
		key := fmt.Sprintf("child-%d", index)
		item := DelegationInvocationRecord{Version: 1, ID: key, BatchID: batch.ID, Key: key, Ordinal: index, ChildSessionID: key, ParentSessionID: batch.ParentSessionID, ParentTurnID: batch.ParentTurnID, State: DelegationInvocationDraining, Capture: AgentExecutionCapture{Config: ResolvedAgentConfig{Harness: "pi", Model: "fixture", Prompt: "PRIVATE_CAPTURE_SENTINEL"}}, ExecutionCWD: t.TempDir(), Task: "Fixture task", DispatchAttempted: true, NativeAccepted: true, NativeSessionID: "native-" + key, CreatedAt: "fixture"}
		outcome := DelegationCompleted
		if index == 3 {
			outcome = DelegationFailed
		}
		item, err := completeDelegationInvocation(item, outcome, finality, strings.Repeat("quoted \"line\"\n🚀\x00", 8192), strings.Repeat("fixture error \n", 1024), "ended")
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, item)
	}
	result, err := buildDelegationResult(batch, members)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > delegationNativeResultLimit || len(result.Children) != 8 || !result.IsError || !result.CanContinue {
		t.Fatal("all-member bounded aggregate failed", len(raw), err)
	}
	for index, child := range result.Children {
		if child.ResultReference == nil || !child.ResultTruncated || child.ResultReference.TotalBytes != int64(len(members[index].Result)) {
			t.Fatal("missing full-output reference", index)
		}
		page, err := delegationOutputPage(members[index], child.ResultReference.Digest, 0, 1024)
		if err != nil || !utf8.ValidString(page.Content) || len(page.Content) > 1024 || !page.HasMore {
			t.Fatal("invalid bounded UTF-8 output", index, err)
		}
		encoded, _ := json.Marshal(page)
		if strings.Contains(string(encoded), "PRIVATE_CAPTURE_SENTINEL") {
			t.Fatal("private capture leaked through an output continuation")
		}
		if _, err := delegationOutputPage(members[index], strings.Repeat("f", 64), 0, 1024); err == nil {
			t.Fatal("stale digest accepted")
		}
	}
}
