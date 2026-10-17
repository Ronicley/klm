# Steering message review

> Historical review below, before the follow-up implementation. The user identified
> OpenCode as the incident harness and approved a revised contract: Enter steers
> the next model interaction, pending steering is batched, and Send now interrupts
> and replaces the running turn. See `product.md` and `engine/README.md` for the
> current contract. The implementation now addresses the four source-level findings
> and bounds native acknowledgement waits; live harness validation remains pending.
>
> Independent review: `STEERING_MESSAGE_IMPLEMENTATION_REVIEW.md` identified six
> further issues. Follow-up changes retain Codex inputs until `clientId`-correlated
> native incorporation, claim Pi/OpenCode batches at owned native boundary hooks,
> discard Pi runtimes with unconfirmed inputs, avoid shared Pi preference writes,
> invalidate older browser submissions from every Send now entry point, and wait
> for complete OpenCode parts instead of user-header SSE acknowledgements.
>
> Follow-up verification: six selected Go tests passed (`ok klm/engine 4.559s`),
> two mocked OpenCode boundary/partial-payload tests passed, and
> `npx tsc -b --pretty false` passed. These checks do not reproduce the incident or
> validate a real model request. OpenCode's native parent-message bookkeeping can
> cause an additional model iteration after boundary insertion; response ordering
> remains an explicit human acceptance check. No live model task or full suite ran.

## Scope and evidence

Reviewed the current working tree: shared composer and submission transport, main/side/floating chat wiring, message API, persistent queue, scheduler, Stop/restart handling, and Pi/OpenCode/Codex adapters. Existing unrelated modifications were left intact. No implementation changes were made.

For Pi, also inspected the installed `@earendil-works/pi-coding-agent` 0.85.1 RPC documentation and implementation, including its session queue and agent loop. OpenCode/Codex conclusions below concern KLM's adapters; their live model behavior was not exercised. The harness and native history of the reported incident were not identified, so this is not an incident-level root-cause confirmation.

## What explains the initial waiting behavior

**Enter is not steering in the current implementation.**

- `clients/desktop/src/features/chat/MessageComposer.tsx:92` defaults to `queue`; both Enter and normal submit use that default. Only the explicit Send now button uses `steer`.
- `engine/api.go:966-994` persists the corresponding queued/steering state.
- `engine/message_queue.go:100-116` only offers `steering` entries to an active adapter.
- `engine/message_queue.go:170-179` refuses ordinary queue dispatch while `a.runs[sessionID]` exists.
- `engine/harness.go:349-358` removes that reservation and schedules pending messages after the entire execution finishes, not after an assistant message, tool result, or native subagent review.

This matches `product.md`, “Messages During Execution,” and `engine/README.md:1613-1647`. It explains why a message stays above the input through multiple agent steps. Making ordinary Enter affect the next model interaction would be a behavior/contract change, not merely repairing a broken RPC call.

## Findings

### 1. P1 — Pi drops durable pending-delivery tracking before native consumption

**Locations:** `engine/pi_interactive.go:260-278`, `engine/message_queue.go:137-165`, `engine/message_queue.go:37-60`, `engine/api.go:1026-1047`, `engine/harness.go:373-389`.

Pi's successful `steer` response only acknowledges insertion into its in-memory steering queue. The installed implementation's `AgentSession._queueSteer` pushes into `_steeringMessages` and calls `agent.steer`; persistence happens later when the user message is emitted through `message_end`.

KLM calls `finishSteering` immediately on that RPC response. It appends an ordinary user-history event and deletes both the queue entry and its prepared payload. It explicitly ignores `queue_update`; its Pi event mapper also does not reconcile native user `message_start`/`message_end` with the original queue item.

**Failure sequence:**

1. A long tool call or human wait is active.
2. The user selects Send now; Pi queues it and acknowledges it.
3. The message disappears from KLM's queue and appears in the timeline.
4. Stop, engine termination, or a harness crash occurs before the next native delivery boundary.
5. Pi's in-memory entry is lost before it enters native history. KLM has no remaining queue entry to pause or mark uncertain, and native resumption does not reconstruct history from KLM's displayed user events.

The early timeline insertion alone is consistent with the documented acceptance semantics. **The defect is losing the durable recovery state while the instruction can still exist only in native memory.** This is especially problematic for a user who clicks Stop after seeing that the constraint has not taken effect.

**Correction direction:** preserve a durable distinction between accepted-by-harness and incorporated into native history/context. Keep enough correlation and payload to expose unconsumed/uncertain inputs after interruption. Reconcile Pi's native queue/user-message events; never automatically replay an uncertain instruction.

### 2. P1 — A valid short client ID can crash the OpenCode execution goroutine and engine

**Locations:** `engine/api.go:829-835`, `engine/opencode_interactive.go:1003`.

The messages API accepts any nonempty alphanumeric/hyphen client ID up to 128 characters, including `"x"`. OpenCode steering unconditionally slices `q.ID[:14]` while constructing its native message ID.

**Trigger:** submit an active OpenCode message with `mode: "steer"`, `clientId: "x"`, and valid nonempty text; alternatively queue such a message and select Send now while execution remains active. Once claimed, slicing the ID panics. This is in the execution goroutine, not an HTTP handler protected by `net/http`'s handler recovery.

The standard frontend generates longer IDs, so this does not explain the ordinary UI incident. It is nonetheless a reachable implementation defect in the same feature/API, with engine-wide impact.

**Correction direction:** generate a native-safe correlation suffix independently of the externally supplied ID, or safely handle all permitted lengths. Preserve API idempotency using the original client ID.

### 3. P2 — Send now can lose priority when the active turn ends before dispatch

**Locations:** `engine/message_queue.go:170-179, 270-293`; `engine/api.go:966-995`.

Queue-item Send now sets the selected item's status to `steering`, but moves it to the front only if `a.runs[id] == nil` at the time of that request. The idle scheduler always examines `s.Queue[0]`, regardless of later steering entries.

**Failure sequence:**

1. Queue contains ordinary follow-ups A and B while a turn is running.
2. The user selects Send now on B.
3. The running turn settles before its adapter claims B.
4. Because the request observed an active turn, B was not promoted.
5. The scheduler starts A first. If the preceding entry is paused/uncertain, B does not start at all.

A related path is a new `POST /messages` with `mode: "steer"` while idle with a paused head: it always appends to the tail, and the scheduler cannot start it. There is no promotion equivalent to the queue-item endpoint.

This contradicts the intended send-ahead behavior and makes an urgent correction wait behind unrelated or blocked input.

**Correction direction:** preserve explicit steering priority through the active-to-idle transition and apply it consistently to both submission endpoints, without replaying paused/uncertain entries or disturbing FIFO among ordinary follow-ups.

### 4. P2 — A new Send now is serialized behind ordinary pending HTTP submissions

**Location:** `clients/desktop/src/App.tsx:564-596`.

`submitLocal` puts every submission, including `mode: 'steer'`, behind the same per-session `submissionTails` promise chain. The transport timeout is created only when each request reaches the head. A previous queued message with slow attachment preparation, a delayed response, or a failing connection can hold the urgent steering request locally for up to its 45-second request timeout. Multiple older requests accumulate the delay.

The composer already clears the urgent draft and displays a sending state, but the engine has not received the instruction. During that delay the agent can cross its next model boundary and begin unwanted work.

This affects **new text sent using the composer's Send now**. The Send now button on an already server-queued item uses a separate HTTP path in `MessageQueue.tsx` and does not have this particular promise-chain delay.

**Correction direction:** keep FIFO transport for ordinary queued submissions, but do not make a newly requested steering message wait behind that chain. Preserve per-message idempotency and Stop cancellation/version handling.

## Boundaries that are not themselves implementation defects

### A visible assistant sentence is not necessarily a steering boundary

For installed Pi 0.85.1, the agent loop consumes steering after the current assistant response has finished executing **all its tool calls**, before the next LLM call. Text such as “I will now start the correction” and the tool call that starts the correction can belong to the same assistant response. Seeing that sentence does not mean there is another model invocation between the sentence and the tool call.

If a review tool and a correction tool were already emitted in the same batch, Pi's current native steering cannot insert a message between them. If review completed and the agent then made a genuinely new model call, a steering message queued natively before that boundary should be included; the reported behavior would then need native trace evidence to distinguish late delivery, a different harness boundary, or model noncompliance.

A permission/question also has its own waiting tool. Steering does not answer or cancel it, and does not cancel a native subagent/tool that is already running.

### Adapter acceptance does not establish model consumption

- **Pi:** sends RPC `steer`; KLM treats its success response as accepted and ignores native `queue_update`.
- **Codex:** sends `turn/steer` with `expectedTurnId` and checks the acknowledgement's `turnId` (`engine/codex_interactive.go:218-260, 318-335`). The adapter does not attach a consumed-by-model state to the user event.
- **OpenCode:** posts `prompt_async` into the existing session and acknowledges on matching persisted user-message notification (`engine/opencode_interactive.go:1003-1018, 1200-1206`). Persistence is stronger than Pi's in-memory acknowledgement, but still does not prove inclusion in the currently running LLM request. Final reconciliation checks the latest assistant's parent against the latest steering ID; this is not a pre-tool execution barrier.

The adapters do use distinct native steering paths rather than simply invoking the ordinary next-turn scheduler in every case. It would be inaccurate to conclude that all Send now operations are implemented as follow-ups.

Steering is not a hard execution interlock. For an immediate requirement that no more local work proceed, use Stop and then issue the revised constraint; completed effects cannot be undone. Chat Stop does not stop an independent graph.

## Additional reliability gap

Pi/Codex steering acknowledgement waits have no dedicated deadline. While a turn remains active, an unacknowledged entry stays `sending`, `takeSteering` prevents all subsequent steering claims, and the UI disallows its removal. Unlike OpenCode's bounded acknowledgement wait, recovery depends on the turn ending or being stopped. This needs a bounded failure/uncertainty strategy and a focused adapter test; it was not reproduced against a live harness in this review.

## Validation performed

Ran only the existing targeted tests:

```text
cd engine && go test . -run '^(TestMessageIdentityReplay|TestTransactionRecoveryCheckpointInterleaving|TestSessionCommandScopeAndRecovery)$' -count=1
ok  klm/engine  2.757s
```

These cover durable claims/receipts, idempotency, and existing session-command recovery. They do **not** demonstrate next-model-call delivery, native ACK-versus-consumption behavior, priority races, short-ID steering, or frontend submission delay. No full suite, live model task, or browser acceptance test was run. Findings above are source-level analyses, not claims that the reported incident was reproduced.

## Recommended acceptance scenarios after corrections

1. Hold a deterministic review tool open; send a restriction; release it. Inspect the next model request and verify the restriction is present before any correction tool is requested.
2. Distinguish two tool calls in one assistant batch from two separate model calls. Do not promise a mid-batch boundary that the harness does not expose.
3. ACK a Pi steering message, stop before native consumption, and resume: the instruction must remain recoverable rather than silently disappear.
4. Select Send now on B behind A exactly as the active turn settles, including a paused/uncertain A. B must not be trapped behind A.
5. Delay an ordinary submission's HTTP response and send new steering from the composer. Steering must reach the engine without waiting for the ordinary request.
6. Exercise short valid client IDs, missing native acknowledgements, multiple steering inputs, restart, prepared attachments, and main/side/floating chat paths across the three harnesses.

The most useful missing incident detail is **which harness was running the parent agent**. The elapsed ordering of Send now, native acceptance, review completion, and the following model/tool call is needed to attribute the original failure precisely.
