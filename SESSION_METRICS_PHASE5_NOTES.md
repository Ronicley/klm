# Session metrics — Phase 5 observed timing/recovery slice

## Status and ownership

This is a **partial Phase 5 delivery**, not Phase 5 completion or runtime acceptance. Read with `SESSION_METRICS_TELEMETRY_CONTRACT.md` and Phase 2–4 notes.

Changed only:

- `engine/session_metrics.go`
- new `engine/session_metrics_timing.go`
- `engine/stream.go`
- `engine/stream_journal.go`
- `engine/session_metrics_api.go`
- `clients/desktop/src/features/metrics/api.ts`
- `clients/desktop/src/features/metrics/SessionMetricsDashboard.tsx`
- this note

Preserved existing working-tree changes. No adapter-specific accounting, harness settlement, request-control behavior, analytics, product, routing or styling changes were made.

## First nonempty assistant response

New execution records have optional `timing` with:

```text
source: engine_visible_emit
firstResponseAt: UTC timestamp or null
firstResponseLatencyMs: integer milliseconds or null
```

The common `adapter.commitStreamUpdates` seam observes the first changed, nonempty (after whitespace trimming) `assistant` event belonging to that execution. Reasoning, tools, status/metadata and empty assistant content do not qualify. The timestamp is sampled immediately before the durable journal append/Sync and subsequent publication; latency is from the existing engine execution start. It includes preparation and stream batching before this seam, not the subsequent journal-write duration, network transit or client rendering. It is **engine-observed emission latency**, not native model generation time, proof of when a human saw the content, or time to task completion. Final reconciliation can supply the first content if no earlier content was exposed; its emission time, not a native historical timestamp, is used.

The first-response execution ID and timestamp are stored in the **same stream journal frame** as that content, then applied in memory after successful persistence. Journal replay/checkpoint applies only the explicit observation. There is no scan of historical assistant events and no timing reconstruction from `CreatedAt`, `UpdatedAt`, native timestamps or prior text. Repeated updates cannot replace a recorded first response. Missing execution identity, legacy timing, terminal execution, or a backwards/invalid clock interval cannot supply a latency. Zero milliseconds is possible only from a qualifying observed interval, never from missing content.

Existing executions remain without timing; no legacy backfill occurs. Newly observed executions with no qualifying response retain null values, including failed/cancelled/restart-interrupted executions. A response already observed remains usable after failure, Stop or restart; its timing does not claim complete execution duration. Native children sharing a parent's turn cannot write the parent's timing because lookup is scoped to the emitting conversation's execution record.

The public execution projection copies the optional timing values while holding the state lock. Execution details show **First assistant response**, a millisecond/second latency, and its engine-emission timestamp. Missing data says Unavailable; active instrumented executions without content say Not observed yet. No mean, percentile or timing total is invented.

## Explicit interruption counts

The all-time filtered inventory summary now includes nullable `interruptions`:

```text
total, stop, replacement, restart, shutdown, other
```

Each recorded terminal execution is counted at most once, classified from its durable `interruptionReason`:

- `stop` → Stop
- `replacement` → replacement
- `engine_restart` → restart
- `engine_shutdown` → shutdown
- another/absent reason with actual `outcome=interrupted` → other

An open execution is not an interruption. A cancellation without an explicit supported reason does not become Stop or replacement. Counts are derived on demand from existing records, not maintained as another durable counter and not inferred from similar prompts, queue actions, history text or a session's current state. Existing outcome counts remain unchanged: a reasoned cancelled execution may also appear in the interruption measure, so these are **not disjoint additive outcome categories**.

The summary spans every matching main conversation before pagination, with the same existing archive/project/harness/title/current-state/last-result filters. It is all-time recorded history, independent of period controls. No recorded metrics in the matched inventory produces `interruptions: null`; known observation history with no recorded interruptions produces explicit zeros. Unknown earlier native turns are outside the denominator. The UI adds **Recorded interruptions** and the reason breakdown. There is no cancellation-rate or historical completeness claim.

## Explicit execution role; unchanged safe scope

New executions persist `role`: ordinary conversations use `main`; existing explicit roles (including `general_agent`, `side_agent` and `graph_node`) are preserved, with a graph-run fallback to `graph_node`. Legacy records without a role return `role: null`, rather than inventing a historical role. Details display the role when available.

No additional execution is created for native child event streams, which currently share their parent's engine turn. No role inventory projection or auxiliary/general-agent dashboard rows were added. Existing main-conversation inventory and execution-detail access rules remain unchanged. KLM Agent remains engine-owned, with no fictitious project. Side/native-child/graph/general usage is not added to main totals. Independent spawned main sessions remain independent inventory units. Graphs retain their own lifecycle; counting a stopped chat does not stop or settle an independent graph.

## Deliberate omissions

- **Pending-user-action union duration remains unavailable.** No request or UI-derived timing was added. Native-child requests can be routed through owning conversations, graph requests are projected independently, and MCP elicitation has its own lifecycle. A coherent union needs all owning add/remove/reply-failure/cancellation/restart transitions, not a partial hook or a count of visible cards. Current waiting-now inventory is unchanged and is not a historical duration.
- Tool occupancy/work duration remains unavailable. No native tool starts/ends, command durations, parallel interval union or adapter timing compatibility changes.
- Retry counts/relationships remain unavailable. No subsequent message, replacement, graph correction, uncertain delivery or similar text is inferred to be a retry. No retry zero is reported.
- Delegated accounting remains pending. No native parent/child addition, own/direct attribution, descendant traversal or consolidated consumption total; inclusion relations remain unknown under the telemetry contract.
- No new OpenCode/Codex accounting, money, model allocation, price assumptions or historical timing/usage backfill.
- No timing/recovery period aggregation, ranking or auxiliary-role inventory; Phase 4 analytics remain unchanged.

## Formatting and remaining validation

Ran **only `gofmt -w`** on the five changed Go files. No builds, type checks, tests, suites, reviews, live prompts, commits or pushes were run, as requested. Go/TypeScript compilation is deferred to the Phase 6 consolidated check; formatting is not compilation or behavior validation.

Human validation remains pending for real Pi/OpenCode V1/V2/Codex emissions; empty/reasoning-only/late final content; repeated updates; multi-turn/resumed sessions; journal/checkpoint/restart persistence; no-response failure/Stop; Stop/replacement/restart/shutdown reason counts across filtering/pagination; legacy unavailability; independent graphs; and desktop/Focus/mobile detail rendering. Delegated/native-tool/request-union coverage must not be marked complete on the basis of this slice.
