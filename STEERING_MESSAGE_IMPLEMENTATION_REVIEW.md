# Steering-message implementation: independent review

## Result

**Changes requested: two P1 and four P2 findings.** The implementation improves batching and interrupt/restart coordination, but it does not yet establish the requested next-model-interaction and recovery contract across all three adapters.

Reviewed the uncommitted steering changes on `release/0.3.0`, HEAD `db3bf5d`. Read `AGENTS.md`, `product.md`, the specified implementation/tests/contracts, and relevant delivery/lifecycle helpers. Unrelated GitHub activity, graph-layout, and native diagnostic changes are not attributed to this feature. `STEERING_MESSAGE_REVIEW.md` was treated as historical context, not independent validation.

Findings below are source-confirmed behavior or reachable interleavings, **not claims of live reproduction**. No real-model task was run, and the original OpenCode incident was not reproduced.

## Findings

### 1. P1 — Codex still deletes recoverable input on an in-memory steering ACK

**Evidence:** `engine/codex_interactive.go:327-345`, especially the unconditional successful `finishSteering` at line 344; `engine/message_queue.go:243-265`, `96-107`, `109-130`.

A matching `turnId` in the `turn/steer` response is treated as completed delivery. `finishSteering` appends every original batch input to the public timeline and removes its queue entry and prepared payload. This is not evidence that Codex has recorded or sampled the input.

The installed executable reports `codex-cli 0.153.4`. Its version-matched upstream source confirms the distinction:

- `codex-rs/app-server/src/request_processors/turn_processor.rs:1030-1040,1123` returns `TurnSteerResponse` after `steer_turn` accepts the input.
- `codex-rs/core/src/session/turn_input.rs:613-645` adds it to turn-local pending input and returns the active ID.
- `codex-rs/core/src/session/input_queue.rs:257-267` only extends an in-memory vector; `204-208` clears pending input during cancellation cleanup.
- `codex-rs/core/src/session/turn.rs:305-330` drains and records pending input later, before a model request.

Source URLs use `https://raw.githubusercontent.com/openai/codex/rust-v0.153.4/` plus the paths above.

**Minimal trigger:** Hold a native tool/model cycle open; submit steering A (or a batch A/B/C); receive a successful steering ACK; use Stop or Send now for D before Codex drains A. The owned process is terminated. A is absent from native durable history and from KLM's recoverable queue, despite its visible user bubble. With batching, the entire acknowledged batch is affected.

**Correction direction:** Separate Codex acceptance from consumption/history incorporation, as the Pi path now attempts to do. Retain payloads after ACK, use native user-message correlation (the installed protocol exposes `clientUserMessageId`), and settle only on suitable native evidence. Preserve unmatched accepted inputs as uncertain after interruption. Do not automatically retry them.

**Attribution:** The ACK-as-delivery assumption predates this diff; it remains in the reviewed steering path and now applies to default Enter and whole batches. The added timeout does not repair it.

### 2. P1 — OpenCode's first outstanding batch can hold later pending steering past the next model boundary

**Evidence:** `engine/opencode_interactive.go:979-1001,1015-1018,1027-1032,1220-1226`; `engine/message_queue.go:179-185`.

The new priority select helps only while `inputReady` is enabled. After dispatching batch A, it is disabled until both the HTTP request and matching SSE user-message notification have been processed. The shared queue also refuses another claim while any item is `sending`. Consequently, inputs B/C accepted before the next native boundary can remain in the engine while OpenCode advances with A alone. Combining the entries available at one transport dispatch does not combine all entries pending at a later model boundary.

The supplied OpenCode reference `C:/Users/Samuel/AppData/Local/Temp/klm-opencode-prompt-review.ts:1052-1070,1088-1096,1254-1287` creates/persists prompts separately from the running loop, which snapshots native messages and sends them to the model. There is no wait in that loop for KLM to process its SSE backlog or send the remaining engine queue.

**Minimal trigger:** While a native review/subagent tool is held open, dispatch A. Delay handling its matching SSE `message.updated` behind existing output, while allowing OpenCode to persist A. Submit B and C so both are durably pending in KLM. Release the tool before the delayed SSE acknowledgement is processed. OpenCode's next model request sees A but not B/C. Once the acknowledgement is processed, B/C are dispatched for a later interaction. The 15-second deadline only bounds the failure; it does not ensure the next boundary receives the pending inputs.

**Correction direction:** Decouple sending additional pending inputs from consumption/display acknowledgements, with explicit per-submission correlation instead of a single outstanding slot. If the product promises all engine-pending inputs at the next model boundary, coordinate draining with a native pre-model boundary; transport batching alone cannot establish that promise. Validate with a deterministic delayed-ACK/SSE scenario, not just a test that calls `takeSteering` after all three inputs have already accumulated.

**Attribution:** The earlier acknowledgement coupling remains; the new priority branch and batching do not eliminate it. This is a concrete gap in the newly required contract, not proof of the original incident's exact timing.

### 3. P2 — A late Pi steering input can be marked uncertain and then silently consumed by the retained runtime

**Evidence:** `engine/pi_interactive.go:155-165,230-251,332-343,545-550`; `engine/harness.go:383-385`.

The adapter may dispatch steering while a native `agent_settled` event is already buffered but not processed. Installed Pi accepts `steer` while idle by adding to its native queue. Processing the old settled event then returns success and retains the process, without draining/clearing outstanding steering. Finalization marks the durable input uncertain, but does not remove it from Pi's live queue.

Installed Pi source establishes the remaining behavior:

- `dist/core/agent-session.js:1016-1024,1046-1058`: `steer` queues without an active-run requirement.
- `dist/core/agent-session.js:821-949`: the next ordinary prompt does not clear steering.
- `node_modules/@earendil-works/pi-agent-core/dist/agent-loop.js:78-119`: the next run drains pending steering before its first model request.

These paths are under `C:/Users/Samuel/AppData/Roaming/npm/node_modules/@earendil-works/pi-coding-agent/`.

**Minimal trigger:** Native Pi settles; before KLM reads that event, a steering wake wins the select and dispatches A. KLM then reads the earlier settled event, retains Pi, and marks A uncertain. Send unrelated B with unchanged runtime settings. Pi drains A automatically alongside B, although the user never chose recovery of A. The new adapter's empty `steeringInputs` map cannot reconcile A, so it may remain visibly uncertain even after actual consumption.

**Correction direction:** Do not retain a Pi runtime with unresolved steering after settlement. Correlate/reconcile or explicitly clear its native queue, or discard that runtime while retaining durable uncertainty. Check pending native state before reuse; never allow uncertain input to be carried invisibly into the next run.

**Attribution:** An integration gap between the existing warm-runtime lifecycle and the new accepted/uncertain recovery logic.

### 4. P2 — Queue-row Send now does not invalidate older local submissions

**Evidence:** `clients/desktop/src/features/chat/MessageQueue.tsx:11-16`; `clients/desktop/src/App.tsx:571-585,924`.

Local stop-version increment, request abortion, and promise-tail invalidation occur only for a newly composed `mode: 'interrupt'` submission (and the separate Stop handler). The queue-row Send now performs its own request and only returns a snapshot. It never invokes that local interruption logic.

**Minimal trigger:** Have selectable server-queued A. Hold the HTTP response for another submission X, and submit Y so Y waits locally behind X. Click Send now on A and allow the replacement turn to start. Release X's response. Y's old promise still passes the unchanged client stop version and starts a new HTTP request. Since that request begins after the server interruption, it captures the new server version and is accepted into A's replacement execution. The older pending input was not paused as promised.

**Correction direction:** Route every UI Send now entry point through a shared per-conversation interruption callback that invalidates local pending sends before making the queue-control request. Preserve aborted/unsent drafts for recovery. Wire main, side, and floating presentations consistently. Updating only the composer's interrupt branch is insufficient.

**Attribution:** Newly missing integration caused by changing queue-row Send now into an interrupt/replacement operation while leaving its transport independent of the local submission coordinator.

### 5. P2 — Setting Pi steering mode changes the user's global Pi preference

**Evidence:** `engine/pi_interactive.go:197-200`; launch arguments at `119-126`; inherited process environment at `engine/interactive.go:26-33`.

Every Pi turn now sends `set_steering_mode: all`, including turns with no steering. This is not a process-local setting in installed Pi 0.85.1:

- `dist/modes/rpc/rpc-mode.js:408-410` calls `session.setSteeringMode`.
- `dist/core/agent-session.js:1436-1438` updates the agent and calls the settings manager.
- `dist/core/settings-manager.js:480-483` changes `globalSettings.steeringMode`; `401-412` persists the global settings file.

KLM launches Pi against the inherited agent configuration, not an isolated settings store. Its own session JSONL path does not isolate global preferences.

**Minimal trigger:** Set standalone Pi's steering preference to `one-at-a-time`, then start any Pi conversation through KLM. The global preference becomes `all`, affecting subsequent standalone Pi sessions without user authorization to change their settings.

**Correction direction:** Configure all-message delivery through a runtime-only mechanism or an appropriately isolated settings store that preserves required auth/model/resource discovery. Do not write and later restore a shared global preference: concurrent Pi processes make that unsafe.

**Attribution:** Introduced by the new unconditional mode-setting RPC.

### 6. P2 — OpenCode deletes the batch before its text/file parts are confirmed persisted

**Evidence:** `engine/opencode_interactive.go:1220-1226`; `engine/message_queue.go:262-265,96-107`.

A matching user `message.updated` header is enough to remove all batch payloads. The supplied OpenCode reference explicitly saves the header first and then each part separately: `klm-opencode-prompt-review.ts:1046-1048` calls `sessions.updateMessage(info)` followed by `sessions.updatePart(part)` in a loop. The header event is therefore not proof that the submitted text and native file parts are durably present. The adapter neither checks parts nor retains recovery data until that stronger condition.

**Minimal trigger:** Submit a batch with text and file parts. Process its matching user-header notification, then interrupt/terminate OpenCode before its remaining part updates complete. KLM has already deleted the entire prepared batch; native history can contain only the header or a partial set of parts. The incomplete inputs cannot be recovered from the queue.

**Correction direction:** Keep accepted payloads until complete native message/part evidence is reconciled, with correlation for the expected text and attachment materialization. Treat shutdown before that evidence as uncertain. Do not use either the early `prompt_async` response or a user header alone as complete-payload acknowledgement.

**Attribution:** The earlier header-as-complete-delivery assumption remains in the revised batching path. This is a partial-payload recovery defect, separate from finding 2's next-boundary dispatch delay.

## Coverage and non-findings

- **Queue batching:** `combineSubmissions`/`takeSteering` include all currently `steering` entries, retain original per-input payload metadata, and map a successful batch to individual timeline events. This verifies engine-side grouping, not native model consumption.
- **Pi consumption correlation:** The literal unique prefix avoids command/template expansion and distinguishes identical text. Successful queue ACK changes status to `accepted`; normal matching native user events settle the batch. This improves the earlier loss-on-ACK path, subject to finding 3 and normal persistence/crash uncertainty.
- **Interrupt lifecycle:** Selection is persisted before cancellation; other pending queue entries are paused/uncertain; replacement release is gated on owned shutdown success. Explicit Stop clears `restartMessageID`. Failed shutdown and engine closing prevent release. These state transitions are covered by the targeted test below, not by live process-tree validation.
- **Identity/repeated actions:** The message endpoint retains durable identity/fingerprint checks. A retry of an already accepted identical interrupt returns its receipt rather than interrupting twice. Different Send now requests while cancellation is active conflict instead of creating parallel turns. A repeated queue-row request can return not-found after consumption/ID replacement; do not interpret that alone as failed first delivery.
- **Attachments/sources/settings:** Per-input payloads survive the engine batch and are used for original mention-preparation history. Pi/Codex inline prepared context and OpenCode file parts take different native paths. Restart dispatch checks prepared harness/directory against current conversation ownership. Steering uses active native model/effort rather than attempting to mutate live settings. The confirmed global-setting exception is finding 5.
- **Short IDs:** The OpenCode suffix is now generated independently; the earlier `q.ID[:14]` panic is removed.
- **Deadlines:** Pi/Codex now have steering response deadlines, and OpenCode has a confirmation deadline. This is recovery bounding, not a consumption guarantee. Pi's synchronous stdin write itself is not covered by an ACK timer; no live stalled-pipe failure was tested.
- **Native boundaries:** Pi's `all` mode drains all entries already in its native queue at its polling boundary. Codex's version-matched source drains pending input together but expressly defers it in startup/auto-compaction continuation cases. OpenCode snapshots native messages on each loop iteration. None proves that an engine queue ACK or timeline bubble means the model consumed the restriction. Already emitted tool calls and completed effects cannot be undone by ordinary Enter.
- **Contracts:** Updated product/README/general-agent text consistently distinguishes steering, interrupt, and explicit FIFO queue mode. The stronger next-boundary and recovery promises are not yet fully implemented because of the findings above.

## Verification performed

One targeted existing check was run during this independent review:

```text
cd engine && go test . -run '^(TestSteeringBatchConsumptionAndRecovery|TestSendNowContinuationRequiresConfirmedStop)$' -count=1
ok  klm/engine  4.381s
```

Also performed read-only source/protocol inspection, checked branch/HEAD, and obtained `codex --version` (`0.153.4`). Native references were the installed Pi 0.85.1 RPC documentation/runtime source, the supplied OpenCode prompt/ID reference files for 1.18.35, and version-matched Codex upstream source fetched to stdout only. One initial upstream source fetch timed out; the required Codex evidence was subsequently obtained from the explicit release tag.

No full suite, additional review agent, graph, browser test, or live real-model execution was run. The parent's earlier Go/TypeScript results were not represented as independent native validation. No implementation file was changed; this report is the only authored file.

## Recommended focused acceptance after fixes

1. Hold a tool open, accept steering in multiple transport batches, delay native ACK/SSE handling, then release the tool. Inspect the actual next model request for every pending instruction.
2. ACK Codex steering, interrupt before native recording, and confirm recoverable uncertainty with no silent replay.
3. Deliver Pi steering just after native settlement but before KLM processes settlement; reuse the runtime and prove uncertain input cannot execute implicitly.
4. Trigger queue-row Send now while another request response and a later local promise are blocked; only the selected replacement may proceed.
5. Start a KLM Pi turn and confirm standalone global preferences are unchanged.
6. Interrupt OpenCode between user-header and part persistence; preserve complete recovery data for the incomplete batch.

The parent owns fixes, arbitration, commit, and push. This review does not approve the current implementation as satisfying the cross-harness contract.
