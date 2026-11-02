# Session metrics — native telemetry contract (Phase 1)

## Status and boundaries

This is a source-backed contract and next-phase implementation specification, **not implemented collection or runtime acceptance**. Only this document was authored in this task. Read with `SESSION_METRICS_DASHBOARD_PLAN.md`, `SESSION_METRICS_IMPLEMENTATION_PROGRESS.md` and `SESSION_METRICS_HANDOFF.md`.

Inspected `AGENTS.md`, all of `product.md`, those three metrics documents, the current working diff, the existing usage/adapters and installed definitions below. No builds, tests, live prompts, review agents, commits or application-code edits were performed. Codex's offline schema exporter was invoked to inspect its installed protocol, not to start an app-server conversation. Generated files were in temporary directories and removed. No credentials or user conversation databases/transcripts were read.

**Decisions:**

- Keep `Session.Usage`, `setUsage` and status-bar normalization unchanged. They describe native conversation lifetime snapshots, not engine-execution increments.
- Introduce separate optional execution accounting. Missing identity, baseline, category, currency or terminal evidence means unavailable/partial, not zero or a guessed delta.
- Parent native totals are **not proven exclusive** of native delegation. Do not label them direct/own usage, or sum them with native child usage.
- Initial implementable slice: Pi identity-bound pre/post statistics; OpenCode V1/V2 baseline inventories and identified final usage records; Codex identity-bound total deltas only where a real baseline exists. Cost can remain unavailable for a protocol while tokens ship.
- Installed package versions do not match the managed profile in `product.md`. Optional features discovered in a newer/older installation are not promises about managed versions. Store the actual version/protocol with evidence and gate optional collection by supported shape; execution must not fail because optional metrics are absent.

## 1. Evidence index

Repository paths are relative to the repository root. Line numbers are inspection anchors and may move; symbols/definition names are the stable lookup keys.

| ID | Exact path / anchor | What it establishes |
| --- | --- | --- |
| R1 | `engine/usage.go`: `SessionUsage`, `tokenCount`, `setUsage`, `piSessionUsage`, `codexSessionUsage`, `openCodeTokenCounts`, `openCodeUsage.message/part/snapshot` | Current lifetime replacement semantics, null/overflow validation, category normalization and message/step deduplication. |
| R2 | `engine/pi_interactive.go`: `piInteractive`, lines 198–289, 327–340, 502–552 | `get_state` identity validation; correlated latest stats request; optional final stats timeout; stats currently check path, and the pre-prompt stats request is **not** a baseline barrier. |
| R3 | `engine/opencode_interactive.go`: `walkMessages`, lines 303–367; native setup/baseline lines 784–829; event/final history processing lines 1285–1557 | Full paginated initial inventory, native IDs, scoped child dispatch and final reconciliation. `baseline` already identifies historical message IDs for display, but is not a durable execution accounting ledger. |
| R4 | `engine/opencode_v1.go`: `v1Request`, `validateVersion`; `engine/opencode_settings.go`: version selection | Protocol routing/major-version gate. V1 and V2 histories must not be cross-resumed. |
| R5 | `engine/opencode_v2.go`: `openCodeV2Info`, `openCodeV2Part`, `openCodeV2Message`, `openCodeV2Projection.event` | V2's own message/model/tokens/time projection, step events and tool identities. It does **not** copy a monetary field, tool interval timestamps, or a complete raw execution identity envelope. |
| R6 | `engine/codex_interactive.go`: start/resume setup lines 219–235, result/model processing around 435, `codexInteractive.frame` lines 715–805, `childFrame`, `item` | Thread/turn-bound notifications, separate child dispatch, item upserts, total usage normalization, model at start/resume. No money is consumed. |
| R7 | `engine/session_metrics.go`: `startExecutionMetrics`, `finishExecutionMetrics`, `interruptExecutionMetrics`; `engine/harness.go`: `execute`, `finishTurnStateLocked`; `engine/models.go`: `resolvedSelection` | Existing attempted engine-turn boundary, durable settlement/restart rules and observed model list. No execution token/cost fields yet. |
| R8 | `engine/store.go`: `now`; `engine/session_metrics_api.go` | UTC RFC3339Nano engine timestamps and current all-time projection. |

Installed root abbreviations below expand to these **exact absolute paths**:

```text
N = C:/Users/ronic/AppData/Local/nvm/v24.13.1/node_modules
P = N/@earendil-works/pi-coding-agent
A = P/node_modules/@earendil-works/pi-ai
O = C:/Users/ronic/.config/opencode/node_modules/@opencode-ai/sdk
C = N/@openai/codex
CX = C/node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/bin/codex.exe
```

| ID | Exact expanded-path suffix / symbol | Evidence and version limitation |
| --- | --- | --- |
| P1 | `P/package.json`; `P/dist/core/agent-session.js:3440–3500`, `getSessionStats` | Installed Pi **1.4.0**; stats aggregate **all session entries**, including compacted-away history: usage records, compaction/branch-summary usage, tool-result usage and assistant usage. Returns `sessionFile`, `sessionId`, four token counters, total, cost and context. This is broader than just assistant messages. Managed profile is Pi 0.85.1. |
| P2 | `P/dist/core/usage-totals.js`: `createUsageTotals`, `addUsageToTotals`, `getUsageCostBreakdown`; `P/dist/core/session-manager.js`: `getEntries`, `appendUsage` | Stats add `usage.cost.total`; model-attributed entries and unattributed tools/summaries can coexist. Session entries, not stream message events, are the accounting store. |
| P3 | `A/dist/types.d.ts:285–305,368–399`: `Usage`, `AssistantMessage`; `A/dist/models.js:672–691`: `calculateCost`; `A/dist/telemetry.js`: `cost.total`, unit `USD`; `A/README.md` metric table `pi.ai.cost` | Pi cost is USD and can be calculated from catalog rates per million category tokens (including tier/cache-retention handling), not necessarily a provider charge. Newer definitions expose optional reasoning and request duration. |
| O1 | `O/package.json`; `O/dist/gen/types.gen.d.ts:98–128,211–300`: `AssistantMessage`, tool states, `StepFinishPart` | SDK **1.17.7**, **V1 wire shapes**: assistant and step cost numbers, split tokens, message and tool times. This is not source proof for managed 1.18.35 producer internals or fork V2. In particular `O/dist/v2/...` is an SDK API generation label, not proof of KLM's OpenCode-major-V2 contract. |
| O2 | `N/@opencode/cli/package.json` | Installed fork V2 CLI **2.0.27**, binary distribution. No inspectable matching V2 cost producer or currency definition was located. R5, not the V1 SDK, is the available V2 field evidence. |
| C1 | `C/package.json`; `CX` offline `app-server generate-json-schema --out <temporary-directory>` | Installed Codex **0.139.0**, versus managed 0.153.4. Export completed successfully without prompts. Definitions below refer to the exporter output's relative paths; temporary copies were removed, so reproduction uses this exact executable. |
| C2 | Export `v2/ThreadTokenUsageUpdatedNotification.json`, root and `#/definitions/ThreadTokenUsage`, `#/definitions/TokenUsageBreakdown` | Required `threadId`, `turnId`, `tokenUsage`; required `total` and `last`; breakdown has `inputTokens`, `cachedInputTokens`, `outputTokens`, `reasoningOutputTokens`, `totalTokens`. Optional/null model context window. No cache-write or monetary field in this usage definition. |
| C3 | Export `v2/ThreadResumeResponse.json`, `#/definitions/Thread`, `Turn`, `ThreadItem` (variants `CommandExecutionThreadItem`, `McpToolCallThreadItem`) | Resume model/provider; Thread ID distinct from shared session-tree `sessionId`; optional native turn start/end in epoch **seconds**, optional turn duration ms; command/MCP item optional duration ms. Resume response has no total-usage baseline field. |

Do not turn a source comment such as Pi's “actually billed” into a financial claim: P3 proves catalog arithmetic exists, and neither its stats nor these adapters supply a billing receipt. No source here establishes OpenCode V1 cost currency or V2 monetary availability. Those monetary metrics remain unavailable.

## 2. Identity and durable observation rules

An engine execution is one actual `execute` entry, including failed preparation (R7). It is not one model response, tool call, queued input, SSE frame or retained runtime. Steering remains in the same execution. Persist a separate `executionId` even when the same native conversation resumes.

Minimum identity tuple:

```text
engine sessionId + executionId
harness + protocol + actual harness version + accounting schema version
native conversation identity + accounting epoch
optional native turnId / messageId / partId / itemId
```

- **Pi:** native `get_state.sessionId` plus normalized `sessionFile`; validate both against the stored binding. Stats must match both, not only path. RPC request ID identifies a read, not consumption. File branch/fork/new session, differing ID/path or incompatible history is a new accounting epoch.
- **OpenCode V1:** protocol + native session ID; usage keys `(sessionID, messageID, stepPartID)` when actual step records exist. Otherwise a completed assistant message key `(sessionID, messageID)` is the single fallback record. Never count both representations. `callID` identifies a tool call, not model usage.
- **OpenCode V2:** same scoped session/message keys, but derive them only from R5's V2 fields (`assistantMessageID` on step events, message `id` in history). Synthetic text ordinals/tool part IDs are not billable step identities. Current projection does not manufacture step-finish usage parts; completed message snapshots are the narrow fallback. Preserve raw step/execution IDs if a verified future protocol supplies them; do not invent them from receipt order.
- **Codex:** native **threadId**, **turnId**, optional **itemId**. Do not use the thread's shared tree `sessionId` as the accounting identity. A turn can contain several usage notifications/model interactions. Match the accepted turn ID from start; drop late parent-turn events and route children separately (R6).

### Baseline / resume / repeated events / reset

1. Persist the execution start before native dispatch. Acquire baseline **after native resume/identity validation but before prompt delivery**. A previous dashboard/status-bar snapshot is not a baseline: it may be stale, belong to a different identity or lack the last response.
2. Persist baseline provenance and its request/inventory identity. An accepted zero baseline needs positive empty-history/counter evidence; absence of a value is not zero. A resumed runtime is not empty merely because a new OS process was created.
3. Repeated cumulative snapshots **replace** the observed endpoint. Identified message/step/item records **upsert**, never append consumption on every notification. Atomically associate the evidence with the execution through existing durable commits/journal. Replay must not duplicate an execution or endpoint contribution.
4. Subtract only comparable, nonnegative, complete counters of the same epoch/category semantics. Validate every required category, including sums/overflow as in R1. Never clamp a negative delta to zero or take `abs`; never carry old costs into new identity/model epochs.
5. Any counter decrease, changed native binding, rollback/deletion/history rewrite, unorderable conflicting snapshot or schema change invalidates cumulative reconciliation. Close that segment partial/unavailable with reason; do not silently adopt the lower value and claim a complete execution. A later observed baseline may start a new **partial** segment, leaving the gap explicit.
6. Terminal usage may arrive after a terminal status frame or be missing on Stop/error. Use a bounded optional final read/drain where already supported; an acknowledgement is not final evidence. Missing final data preserves known observations as partial. No post-crash subtraction spanning downtime or another execution. Outcome success does not make telemetry complete.
7. Late corrections update the evidence key's original execution, if attribution is durable and unambiguous; otherwise mark a gap/conflict. Do not put historical-message corrections into the currently running execution. Different conflicting terminal payloads without a proven revision ordering cannot be resolved by taking the largest counter.
8. No native history backfill into invented engine executions. Baseline/history can preserve lifetime display without assigning its consumption to the new turn.

## 3. Native availability and per-execution narrow path

### Pi RPC

**Categories:** stats expose uncached input, cache read and cache write separately; normalized input = their sum; output is the native output counter. P3 optional `reasoning` is a **subset of output**, not added again. The stats aggregation P1/P2 does not preserve that breakdown, so reasoning from stats is unavailable even when individual new-version assistant messages have it. Native `tokens.total` is a cross-check, not another additive category. Context occupancy is separate.

**Safe first implementation:** add a bounded, correlated pre-prompt stats barrier independent of status-bar processing (R2 currently sends the prompt without waiting). Save raw categories, cost when usable, native ID/path and version. Read final stats for that same identity after normal settlement. Subtract comparable endpoints once. On pre-read timeout, send the user's prompt normally but mark execution usage unavailable; do not subtract the first mid-turn stats from zero. On final timeout/cancellation retain only proven partial observations. No final snapshot means no complete execution amount.

This captures native summaries/compaction/tool-result usage included by Pi, not just visible assistant messages. Summing only `message_end.usage` is therefore **not** a complete replacement for stats. Optional provider response IDs are not guaranteed stable IDs for every charge-bearing entry. Do not implement message-based Pi deduplication by timestamp/text.

**Model:** `get_state` reports active configuration; the current observed-model list is not token attribution. A stats delta covering multiple models/summaries is `model attribution unavailable/mixed`. Do not assign it all to the final/current model. P2 offers model-attributed history in the inspected version, but using it requires a separate entry-identity contract compatible with the managed version.

**Native timing:** new installed `AssistantMessage.timestamp` is request-start epoch ms and optional `durationMs` is monotonic request duration (P3); not total engine-turn/tool/wait time and not automatically supported by Pi 0.85.1. Current adapter does not collect these. Stats have no execution timing. Tool event receipt times can support future **engine-observed** timing, not retroactive native durations.

### OpenCode V1

**Categories:** preserve the existing R1 interpretation: input + cache.read + cache.write; output + reasoning. Reasoning is not added a second time to the resulting normalized output. The SDK structurally exposes those separate counters; the exact installed producer's category calculation is not present, so this contract does not redefine existing status-bar semantics or introduce new price calculations from them.

**Safe first implementation:** R3 already walks all history before dispatch. Save a successful complete baseline inventory of message IDs and identified step IDs. Collect only new assistant usage keys caused after dispatch, scoped to the native session. Maintain per-message representation precedence: identified final step records replace message fallback as a set; never add the message fallback to them. If existing historical records change, quarantine that change instead of attributing it to this turn. No complete baseline inventory means execution usage unavailable.

Run a bounded final history reconciliation through `walkMessages` at settlement before claiming complete coverage. Interim step inventory may be incomplete even if one step is visible. A final assistant message fallback is usable only when no identified steps exist and the existing completed-message semantics apply. Mark missing/invalid categories or unsettled records partial; a failed/cancelled assistant can still have real recorded consumption. Compaction assistant usage is not free and should not be dropped solely because `summary` is true. Snapshot/history removal invalidates the affected accounting segment, not the lifetime status-bar policy.

**Cost:** SDK exposes `AssistantMessage.cost` and `StepFinishPart.cost` as numbers, but this alone does not establish whether they duplicate/accumulate, their currency, price-table provenance or completeness. Do not sum both or assume USD. Keep executable cost and average cost **null** for V1 pending matching producer/currency evidence. Raw optional cost evidence can be retained without being exposed as a monetary amount.

**Native timing:** SDK tool states have `time.start/end`, text/reasoning have interval fields and assistant messages have created/completed (O1). Numeric fields alone do not prove epoch units in the matching 1.18.35 producer; native interval collection is conditional on that confirmation. Current normalized UI events are not a durable timing ledger. Do not subtract KLM event creation times and label the result native tool time.

### OpenCode V2

**Categories/identity:** R5 forwards `message.tokens` or `session.step.ended/failed.data.tokens` into completed assistant-message usage. Existing normalization is shared with V1, but this is evidence of **adapter behavior**, not independent proof of the V2 producer's reasoning/cache accounting. Preserve it for compatibility; do not claim newly verified category billing semantics. Use the new-message baseline/final reconciliation path above, with V2's own history and identities. If a step end arrives without a known start, R5 drops it; final history reconciliation is necessary before completeness can be claimed.

**Cost:** neither `openCodeV2Info` nor step-end projection forwards a cost. No matching inspectable V2 source/currency contract was found in the installed binary package O2. Cost remains unavailable. Never borrow V1 `cost` assumptions or reinterpret `tokens.total` as money.

**Native timing:** R5 exposes step `data.started` and envelope `created` as message created/completed, but does not establish their units or preserve tool start/end. Tool called/success/failure events are correlatable by native tool ID; that allows future engine-receipt intervals after instrumentation, not a claim of native tool wall-clock time. Envelope execution IDs/times must be retained and verified in a later adapter change if required. Until then native tool/wait/first-response duration is unavailable.

### Codex app-server

**Categories:** C2 exposes cumulative `total` and latest `last`. R1 already treats cached input as included in input and reasoning as included in output; do not add either again. Retain cached/reasoning as optional detail if validated, cache-write unavailable. `last` is **not a whole turn** and must not be summed across repeated notifications. Native total is a check against input + output, not an extra addend. The generated schema proves structure, not counter reset/compaction internals; monotonic comparability must be checked on observations.

**Safe narrow path:** retain a durable last authoritative total at the previous turn's final observed notification, with thread/turn/epoch identity and coverage. It is a baseline for the next turn **only** if continuity is positively established and no unobserved work/history mutation occurred between them. The current managed fresh-process resume is not itself that proof. C3 resume has no token total; do not infer one from `turn.items`, an empty process, thread creation time, `last`, or the old status-bar value.

When no proven boundary baseline exists (including first observation of a resumed conversation), full execution tokens are unavailable. An initial in-turn notification can anchor a subsequent observed delta, explicitly **partial after that observation**, excluding the first unknown interaction. It must not be shown as the full execution. If even total continuity is uncertain, leave all execution token amounts null. A truly new empty thread could use zero only after positive empty/new-thread semantics and that version's counter behavior are established; this source inspection does not declare that shortcut verified.

Within a bound turn, replace repeated total snapshots; never sum them or sum `last`. Decreases invalidate the segment. Turn completion alone carries no token baseline/final total in the inspected schema; retain partial coverage if a final observation cannot be established. Do not add child-thread totals to the parent. Store start/resume model as reported configuration; usage notifications provide no per-call model identity, so multi-model token splits remain unavailable.

**Cost:** no monetary value in C2/C3 or R6. No subscription-rate-limit or account quota may be converted to cost. Cost/currency/average remain null; no local model-price estimate is authorized by this evidence.

**Native timing:** C3 `Turn.startedAt/completedAt` are epoch seconds, `durationMs` nullable ms. Command and MCP thread items have nullable native `durationMs` and stable item IDs. These can be preserved as native optional evidence in future; they are not currently used by metrics. A command/MCP duration without endpoints supports a **sum of known call durations**, not interval union or placement on a time axis. Native turn duration excludes engine preparation and must not replace engine execution duration. Other tool types/first-visible-content/wait intervals have no verified duration here.

## 4. Monetary representation and provenance

For future usable monetary evidence, persist at least:

```text
amount: canonical decimal string (never float sum)
currency: explicit ISO 4217 code
source: harness-reported | klm-estimated
valuation: harness-catalog-estimate | provider-reported | unknown
harness/protocol/version + native evidence keys
priceSource/version/date: known value or null, not invented
coverage: complete | partial | unavailable; reason
```

- Pi's supported evidence can be `source=harness-reported`, `valuation=harness-catalog-estimate`, `currency=USD`. “Harness-reported” describes transport; it does not imply invoiced/billed. For a version without the P3 currency/provenance guarantee, leave money unavailable until that version is confirmed. Pi aggregate stats do not include price-table version: store actual harness version, observation time and **null** price snapshot if inaccessible; never silently reprice historical amounts.
- Preserve the raw numeric JSON lexeme with `json.Number`/`UseNumber` or equivalent before any float conversion; store canonical decimal and perform decimal subtraction/addition. Native JS already calculated rounded binary numbers: exact decimal ingestion avoids **additional** drift, not recovery of nonexistent financial precision. Existing float-decoded frames cannot retroactively prove original precision. Do not round each event to cents. Display rounding happens only after aggregation, without changing stored evidence.
- Reject nonfinite/negative/malformed values. A source-provided zero can be a zero catalog estimate, not “free”; missing currency never becomes USD. OpenCode/Codex remain monetary-unavailable under the evidence above.
- Aggregate only compatible currency and provenance/valuation buckets. No FX conversion. Separate estimated/provider-reported coverage; average = sum of usable comparable execution costs / count of executions with usable cost in that bucket. Show partial coverage and never divide by all turns with unknown costs. No estimates from total tokens times the currently selected model's price.

## 5. UTC intervals, attribution and timezone contract

These are **next-phase API conventions**, not claims that current `all_time` API already supports filters.

- Store/return instants as UTC RFC3339/RFC3339Nano strings ending `Z`. Preserve native unit metadata before converting (Codex thread/turn seconds vs duration ms; Pi request epoch ms). No guessed epoch units. Reject invalid or reversed native intervals.
- Historical API accepts explicit `from` inclusive and `to` exclusive UTC instants: **`[from, to)`**. Require offsets on input and normalize to UTC; date-only/local ambiguous timestamps are invalid. Return normalized bounds and the effective timezone in every filtered response.
- Client sends an IANA timezone (`America/Sao_Paulo`, for example) for calendar buckets; default `UTC` if omitted, never engine-local implicit timezone. Reject unknown zones. Daily buckets are local midnight to next local midnight mapped to UTC; weekly buckets start **Monday 00:00** in that zone. DST days can have 23/25 hours; never implement local days/weeks as fixed UTC durations. Return each bucket's UTC start/end and local date label. If engine timezone data is unavailable, reject that zone rather than silently falling back.
- Execution-count cohort uses `startedAt in [from,to)`. Outcome-rate denominators must specify terminated observed executions in that cohort, separating completed/failed/cancelled/interrupted and excluding ongoing. Do not quietly mix completion-cohort counts with start-cohort counts.
- Consumption trend uses a verified native **usage completion instant** when available. Otherwise a whole execution amount is assigned once to known engine `endedAt`, tagged `timeAttribution=execution_end_approximation`; never smear it across days or use session creation time. Interrupted/open records with no proven amount timestamp have no dated consumption bucket. Retain known undated/partial coverage separately; do not invent `endedAt` on restart.
- Historical duration-in-period, if offered, is the union/clip of each known execution interval against `[from,to)`, summed across executions; concurrent executions remain additive accumulated time, not project elapsed time. Native duration without endpoints cannot be clipped: either show it only in a separately named execution-cohort measure or mark interval duration unavailable. Current all-time duration behavior remains unchanged.
- First visible response is first user-visible nonempty assistant content after execution start; status/metadata/tool starts do not count. If future UI elects to count exposed reasoning as response, make that a separately named measure, not an implicit change. Engine receipt timing is labeled observed latency; historical native creation timestamps are not first-delivery proof.
- Waiting duration is the **union** of execution-clipped pending user-action intervals; multiple concurrent permission/question cards do not multiply it. Tool wall-clock occupancy likewise uses the union of verified call intervals, whereas tool work duration sums call durations and may exceed execution elapsed time. These metrics remain unavailable until correlated instrumentation exists.
- Current working/waiting inventory is sampled at response `updatedAt`, not filtered by historical interval. Waiting takes precedence and each eligible session counts once. Last activity remains the relevant conversation event timestamp policy in progress/R7, never a metrics refresh timestamp.

## 6. Delegation and aggregation safety

Initial rows remain ordinary top-level project conversations, including independent spawns, with `scope=main_native_totals`. KLM Agent, side chats, native children and graph nodes are not silently added. Archived history inclusion, removed-project exclusion and permanent deletion follow the owning session; no external metrics retention store.

Use explicit accounting relation on any future parent-child edge:

```text
included | excluded | unknown
```

Default **unknown** for every native harness in this inspection. Pi tool-result/usage-summary entries show why parent totals need not equal visible assistant work; neither parent IDs nor distinct child native IDs prove exclusivity for any harness. Unknown is not excluded.

- Never sum parent and native descendants into a consolidated total while the edge relation is unknown. Show independent observations with non-additivity and coverage; do not label parent native totals “own”, “direct” or “exclusive”.
- KLM-owned side sessions, graph nodes and independent spawns are distinct execution units, but a grouped tree must deduplicate each execution/native accounting key and still respect unknown native rollups. A spawned session already in global top-level inventory is not counted again through its parent's delegated subtotal.
- Graphs have independent lifetime/results; chat Stop/completion does not settle independent graph work. KLM Agent gets its own nonproject category, not a synthetic project. Effective model is evidence-bound, not today's composer selection.
- Lifetime native token totals and newly observed execution totals must be separate measures. Never add one to the other or present their mismatch as lost consumption without evidence of aligned observation scopes.

## 7. Minimal durable fields and implementation seams

Add optional execution accounting fields without migrating unknown old history:

```text
accountingVersion, protocol, harnessVersion
nativeIdentity, accountingEpoch, nativeTurnIds
baseline { evidenceKey, observedAt, categories, source, coverage }
usageEvidence[] { stableKey, representation, nativeIdentity, model?,
                  observedAt, consumedAt?, rawCategories, cost?, coverage }
usage { inputTokens?, outputTokens?, cacheReadTokens?, cacheWriteTokens?,
        reasoningTokens?, coverage, reason, timeAttribution }
cost?, accountingObservedSince
```

This is conceptual: for Pi a pre/final pair suffices; do not build a universal analytics store. OpenCode needs a small execution-owned keyed contribution set to replace message/step representations idempotently. Codex needs a proved endpoint/baseline plus explicit partial-gap metadata. All null categories remain individually nullable; “some evidence” is not complete coverage.

Next-phase seams, in safe order:

1. Extend optional `ExecutionMetrics` in `engine/session_metrics.go` with evidence/coverage and identity dimensions. Persist through existing transaction/journal and owning-session deletion; do not put execution history into ordinary `sessionView`.
2. In `engine/pi_interactive.go`, bind pre/final correlated stats to the engine metrics ID, add bounded optional baseline barrier and ID+path checks. Do not route execution deltas through `setUsage`. Pi USD can follow only on supported version/provenance evidence.
3. In `engine/opencode_interactive.go`, capture the already-read baseline inventory and final reconciliation into separate execution accounting; preserve message/step evidence before R1 drops details. V2 collection belongs at the native projection boundary in `engine/opencode_v2.go`, not by assuming V1-only fields. Leave V1/V2 monetary values null.
4. In `engine/codex_interactive.go`, retain total/cache/reasoning evidence after exact thread/turn matching; explicitly withhold full-turn usage when resume lacks a baseline. Optional native turn/item durations are separate from engine timing. Leave money null.
5. Extend `engine/session_metrics_api.go` with independent execution-metric coverage, then period/bucket fields only after their evidence exists. Preserve current native lifetime totals and status bar. Models must remain unallocated/mixed unless a usage record actually identifies its model.
6. Future timing instrumentation: settlement/first-content delivery, owning pending-request lifecycle union and keyed tool lifecycle. Do not attempt timing reconstruction from normalized display events or current `updatedAt`.

**Phase 1 closure:** identity, unknown/reset behavior, category compatibility, money availability, timing availability, period semantics and non-additivity now have explicit safe contracts. Matching-version OpenCode producer currency/category/timestamp semantics, Codex boundary-counter restoration and native delegated inclusion remain **unverified capabilities**, not silently promised later metrics. Implementation can proceed narrowly without waiting for them by returning null/partial with honest coverage. Human harness/version/UI validation remains pending.
