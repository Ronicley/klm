# Pi/Codex streaming output limit — focused review

## Verdict

**No blocking or other actionable regression findings in the scoped fix.** Removing the aggregate raw-stdout quota is consistent with a long-lived streaming RPC transport; the existing per-line protection and backpressure remain. No implementation correction is requested.

## Scope and attribution

Read `AGENTS.md` and `product.md`. Reviewed the current uncommitted changes to `engine/harness.go` (`jsonLines`), removal of the reader/reset plumbing in `engine/interactive.go` and `engine/runtime.go`, the Output safety paragraph in `engine/README.md`, and `engine/harness_output_test.go`. Inspected adjacent Pi/Codex adapter and runtime shutdown paths to assess reuse and cancellation.

The existing exit metadata/error-propagation edits in `engine/interactive.go`, other diagnostics, GitHub activity work, and committed steering work are not attributed to this fix. OpenCode's separate transport limits are outside the change. Only this report was written; no implementation files were changed.

## Assessment

- **Aggregate quota removal — `engine/harness.go:163–196`:** The reader no longer accumulates raw stdout across frames. Ignored events and repeated snapshots therefore cannot exhaust the former 32 MiB budget. The regression test sends 800 approximately 50 KiB frames (over 39 MiB) and verifies every delivered frame without retaining them. The change removes the identified cancellation condition rather than merely increasing its threshold.
- **Per-event protection — `engine/harness.go:171–223`:** The size check still precedes appending each fragment and enforces a maximum 4 MiB line, including fragmented input. Fatal errors remain sticky. Oversized lines and consumer failures still invoke cancellation, and the current write stops delivering further frames. Pending storage is reused rather than accumulated across completed lines.
- **Backpressure and Stop — `engine/interactive.go:35–70,87–108,131–138`; `engine/harness.go:237–243`; `engine/runtime.go:229–249,358–376`:** The 64-frame channel and synchronous decode/delivery remain unchanged. A blocked frame send selects on the process context, so cancellation can release it without a consumer draining the channel first. Turn cancellation still independently shuts down the runtime, and runtime shutdown signals the owned process container before graceful draining/cleanup. Removing byte accounting does not alter those paths. This is source inspection, not live Stop validation.
- **Retained-process reuse — `engine/runtime.go:145–169`:** The deleted reset only cleared the removed aggregate counter; it did not clear pending bytes, errors, frames, or protocol state. Removing its field, method, and call therefore leaves framing and sticky-error semantics intact. Retained-process liveness/settings checks and discard/close paths are unchanged. Repository search found no remaining reset references.
- **Documentation — `engine/README.md:1393–1404`:** The paragraph accurately distinguishes Pi/Codex's per-line/channel bounds from OpenCode's separate bounds and explicitly disclaims a history quota. The adjacent unbounded-history statement is preserved.

## Existing limitations, not regressions

A bounded wire line and bounded channel do **not** establish a bound on total engine memory or durable history. Decoded JSON has allocation overhead; downstream adapter state and retained events/history can grow. Removing the aggregate quota intentionally allows more output to reach those existing consumers. This review does not claim a total-memory bound, a history-retention policy, or a resource-exhaustion guarantee.

An individual snapshot larger than 4 MiB will still fail. The fix addresses cumulative valid streaming, not oversized individual events. Cancelled-process/exit diagnostics may still accompany genuine protocol failures; their preexisting implementation is not part of this patch.

## Validation and remaining checks

Independently ran exactly one narrow test command:

```text
cd engine && go test . -run '^TestJSONLines(LongStream|OversizedFragmentedLine|ConsumerFailure)$' -count=1 -timeout 30s -v
```

Result: **exit 0; all three tests passed; `ok klm/engine 6.044s`.** Coverage includes valid streaming beyond 32 MiB, fragmented oversize rejection with sticky failure, and cancellation/error propagation when the consumer fails.

These tests exercise `jsonLines` directly, not a real child-process transport. They do not validate a saturated channel's shutdown timing, native retained-process reuse across turns, or a live long-running Pi/Codex task. Those remain human validation items: run past the former threshold, confirm Stop under active output, and submit a subsequent turn against a retained runtime. No full suite, race run, or real-model task was run. The parent's earlier timed-out attempt is not treated as a passing check.
