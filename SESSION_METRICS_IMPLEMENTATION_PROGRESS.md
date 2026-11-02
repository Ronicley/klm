# Session metrics — implementation progress

## 2026-11-02 consolidation update

Source is preserved in `develop` alongside upstream 0.6.7. The historical delivery
and compiler evidence below describe the pre-SQLite implementation, not current
runtime availability. The legacy metrics endpoints explicitly return HTTP 501
with SQLite; collection/recovery/reads still require a port. Upstream analytics
is unchanged. Current consolidation checks and recovery copies are recorded in
`DEVELOP_CONSOLIDATION.md`.

## Delivery status — 2026-10-23 Phase 6 consolidation

Phase 1–5 **slices are implemented**, not the whole approved plan. The consolidated Go dev build and TypeScript check passed. Phase 6 documentation/compiler work is delivered; **human harness/UI acceptance remains pending**.

| Phase | Actual delivery | Still unavailable / pending |
| --- | --- | --- |
| 1 | Source-backed identity, categories, reset, money, period and non-additivity contract. | Matching-runtime monetary provenance, some native producer semantics and parent inclusion are unverified. |
| 2 | Durable engine-turn start/settlement, outcomes, observed models, duration/restart recovery; identity-bound Pi baseline/endpoint token accounting. | OpenCode V1/V2 execution accounting, trustworthy Codex boundary baseline, all costs and model token allocation. |
| 3 | Global Dashboard, aggregate inventory, paginated executions, public accounting projection, separate observed-execution usage/coverage, filters/sort/chat navigation. | Cost/average cost, model/role filters and expanded role inventory. |
| 4 | Explicit period analytics, project and recorded-harness groups, daily/weekly calendar tables, clipped durations and execution-end token attribution. | Model allocation, cost rankings, separate period session rankings/drilldown. All-time duration sorting exists. |
| 5 | Journaled first nonempty assistant emission latency, explicit recorded interruption counts/reasons, role on new execution records. | Historical waiting union, tool intervals/occupancy, explicit retries, auxiliary/general-agent rows and delegated accounting. |
| 6 | Consolidated compiler checks and reconciled documentation. | All human acceptance below; no claim of whole-plan completion. |

Details/evidence: `SESSION_METRICS_TELEMETRY_CONTRACT.md` and `SESSION_METRICS_PHASE2_NOTES.md` through `SESSION_METRICS_PHASE5_NOTES.md`. Their historical task/check statements describe those individual slices, not current global status.

## Adopted scope and persistence

- Inventory rows are normal top-level project conversations, including independent spawns; removed projects, KLM Agent, linked sides, native children and graph nodes are excluded. Archived history is optional; it never contributes working/waiting counts. Waiting takes precedence over working; retained idle runtime is not work.
- Native lifetime `Session.Usage`/status-bar normalization is unchanged. Native parent totals are **not verified exclusive** of delegated work. Neither lifetime plus execution amounts nor parent plus child amounts are added together. No direct/own/consolidated delegated claim.
- An execution starts at actual common `execute` entry, before preparation. Failed preparation counts; queued-but-unstarted input and steering do not create extra executions. Technical `completed` is not user-approved task success. Chat controls and independent graph lifetime remain unchanged.
- Optional metrics live in the owning Session through existing transactions/journal/checkpoints and follow permanent deletion. Old histories remain readable without invented turns, usage or timing. Observation begins at `since`/`observedSince`, not native conversation creation.
- Duration is accumulated observed engine-turn milliseconds including tools/waits; parallel sessions are additive, not project wall-clock time. Live duration is a partial read-only projection. Restart closes open records as interrupted/partial with null `endedAt`; the last persisted observation bounds duration and excludes downtime. No periodic durable heartbeat: quiet interrupted work can have a small lower bound.
- Last activity uses relevant persisted event creation times (user/agent prompt, assistant, reasoning, tool/command/MCP/error), not metadata, refreshes or every streamed fragment.

## Accounting, timing and null coverage

| Measure | Implemented semantics |
| --- | --- |
| Lifetime tokens | Existing native snapshots counted once per matching session, not summed stream frames. Input/output/total have known/total **session** coverage. Cache/reasoning are not added again. |
| Pi execution tokens | Validated native ID and absolute path; one correlated pre-prompt stats baseline, replaced interim/final endpoint and recomputed delta. Normalized input includes cache read/write; output is native output; reasoning is null. Missing/invalid/overflow/reset/identity evidence withholds amounts; usable nonterminal amounts remain partial. Non-completed settlement cannot retain complete accounting. |
| Pi transport | Follow-up restored the existing synchronous `proc.Send` mutex/encoder transport; `engine/interactive.go` has no remaining diff. Baseline/final **response waits** are 2 s after Send succeeds; writes themselves have no bounded-write guarantee, deadline requirement or transport redesign. Late/failed request correlations cannot supply evidence. |
| OpenCode V1/V2 / Codex | No execution accounting collection added. OpenCode remains adapter-not-instrumented; Codex lacks a trustworthy boundary baseline. Existing lifetime usage stays available independently. |
| Observed execution usage | Sum only usable complete/partial accounting, never lifetime snapshots. Coverage counts complete/partial/unavailable **recorded executions**, excluding unknown earlier native turns. No usable amounts = null; observed zero = zero. Numeric partial totals are labeled. Checked token overflow withholds aggregate amounts. |
| Cost / average cost | Null everywhere, zero usable coverage; no currency assumption, estimate, pricing arithmetic or financial billing claim, including Pi. Runtime currency/valuation provenance is not verified. |
| Models | Distinct reported effective IDs and requested setting retained separately. Lists are not token allocation; analytics model attribution is unallocated. |
| First response | New records only: first changed nonempty assistant content at the common stream commit seam. Reasoning/tools/status do not count. Observation is journaled with content immediately before append/Sync/publication. Latency includes preparation/batching, not subsequent persistence/network/render time; it is engine emission latency, not native generation or human receipt. Missing evidence remains null; no historical scan. |
| Interruptions | All-time terminal recorded reasons: Stop, replacement, restart, shutdown, other actual interruptions. Null without observation history; known history with none yields zero. May overlap cancelled/interrupted outcome counts; not disjoint categories. |
| Roles / other timing | New records retain explicit role; legacy role is null. No extra role rows. Waiting duration, tool time, retries and delegated totals remain unavailable, not inferred zeros. |

Public accounting exposes normalized categories, coverage/reason, protocol/optional version, source and observation times; it excludes native identities/paths, RPC keys and raw baseline/endpoint evidence. Launch-package Pi version metadata is not proof of a retained process's version after replacement or matching monetary provenance.

## HTTP API and periods

### All-time inventory

`GET /api/metrics/sessions`

- Filters: `projectId`, `harness`, title `search` (case-insensitive), `state=working|waiting|idle`, latest `outcome=completed|failed|cancelled|interrupted`, `includeArchived=true`.
- Sort descending: `activity` (default), `tokens` (lifetime), `duration`, `executions`; unavailable last, stable ID ties.
- `page` starts at 1; `pageSize` defaults to 25, maximum 100. Summary/coverage span all matches before pagination.
- Response: UTC `updatedAt`, `scope=main_native_totals`, `period=all_time`, summary, rows and pagination. `summary.observedExecutionUsage` is separate all-time recorded usage; `summary.interruptions` is independent of period controls.
- Without `view=analytics`, period parameters are rejected. Unsupported view values are rejected.

`GET /api/metrics/sessions/{id}/executions` returns newest-first paginated recorded executions with accounting, optional timing and nullable legacy role, using the same page conventions. Unavailable/deleted/removed-project/out-of-scope conversations return 404. Ordinary session projections omit execution history. Details remain all time.

### Period analytics

`GET /api/metrics/sessions?view=analytics&from=2026-10-01T00:00:00Z&to=2026-11-01T00:00:00Z&timezone=America/Sao_Paulo&bucket=daily`

- Required `from` inclusive and `to` exclusive: **[from,to)** RFC3339 instants with explicit offsets, returned normalized UTC. Missing/date-only/reversed bounds are rejected. Period limited to 1970–9999 and at most five years; maximum 400 buckets.
- `timezone` defaults to `UTC`; named IANA area/zone identifiers accepted with embedded Go timezone data. Unknown zones, host-relative Local and bare offset aliases rejected. `bucket=daily` by default or `weekly` (Monday).
- Calendar midnight boundaries use the selected timezone, not fixed UTC day/week durations. Ordinary DST produces 23/25-hour days. Unsupported normalized/nonadvancing/ambiguous midnight boundaries are rejected. Returned bucket bounds are full calendar UTC bounds; measures are clipped to their intersection with the requested period.
- Response: UTC `updatedAt`, `scope=main_recorded_executions`, `analytics` containing normalized period, summary, project/historical-execution-harness groups, buckets, history coverage and undated-usage count. No lifetime tokens enter analytics.
- Existing inventory filters select **conversations**, including current state/current harness/latest outcome, not historical execution-result/harness cohorts. Grouping uses each execution's recorded harness.
- Starts count `startedAt in [from,to)`. Durations clip observed intervals and have an intersecting-interval coverage denominator. Tokens use records with valid engine `endedAt in [from,to)` and their own accounting denominator, assigned once as `execution_end_approximation`, not actual consumption timestamps or a daily split. These denominators intentionally differ.
- Known usage without an engine end is excluded from dated totals; `undatedUsageExecutions` counts such observations across matching **all-time** history. No restart/request-time dating. Model attribution remains unallocated; cost remains null.

Dashboard uses shared endpoint resolution/HTTP, existing design tokens and visible-surface inventory polling every 15 s after response. Analytics reuses snapshot refreshes; failed refresh retains/labels the prior snapshot timestamp. Applied period bounds are fixed until changed and never filter lifetime inventory or working/waiting-now counts. Desktop/Focus/mobile behavior is not yet accepted.

## Compiler evidence

### Current — 2026-10-23 Phase 6

Exactly one consolidated `go build -tags dev -o <temporary-directory>/klm-session-metrics.exe .` from `engine`: **exit 0, no diagnostics**. Output was outside the repository and removed.

Exactly one `npx tsc --noEmit` from `clients/desktop`: **exit 0, no diagnostics**. No compiler/import/type fixes were needed in Phase 6; no reruns.

No tests/full suites, live harness prompts, runtime probes, adversarial reviews/review agents, commits or pushes were performed in this consolidation. Compiler success is not telemetry or UI acceptance.

### Previous initial-slice failures — superseded as of 2026-10-23

Original attempt timestamps were not recorded; these failures occurred **before** this Phase 6 check, not in it. Initial Go build plus a diagnostic `-gcflags=-e` rerun failed on missing `GraphRun.StopRequested`, retry IDs, `GraphActivation.ReusedFromActivationID`, and `app.chatFilePath` in `message_edit.go`. Initial TypeScript attempt failed on GraphView `onGraphChange`, ChatMessage `editBlocked`/`queuedCount`, and FileChoice `max_traversals` mismatches. Neither initial check passed. Phase 2 later recorded a successful Go build before its follow-up; Phases 3/4 recorded successful TypeScript checks before later edits; Phase 5 only formatted. The current consolidated checks cover the combined state including follow-ups.

## Stabilization already present (separate from metrics)

These focused changes were in the working diff on entry to Phase 6 and were preserved, not authored/reviewed here:

- `engine/graph_records.go`: restore graph Stop/retry/reused-activation durable fields; also contains metrics history omission from ordinary session views.
- `engine/chat_files.go`: shared owned-attachment path lookup used by existing message-edit code and serving.
- `clients/desktop/src/App.tsx`: remove unsupported GraphView callback (alongside metrics navigation changes).
- `clients/desktop/src/features/chat/SessionEvent.tsx`: remove unsupported ChatMessage props.
- `clients/desktop/src/features/graphs/files.ts`: restore optional `max_traversals` contract.

Compilation covers their combined types/imports, not graph/edit/attachment runtime acceptance.

## Remaining human acceptance / work

1. Real Pi multi-turn/resumed baseline/final/cache values; partial timeouts/cancellation/restart, missing/reset/identity evidence and unchanged Windows lifetime/status-bar refresh. Check actual runtime versions. OpenCode V1/V2/Codex must continue to show unavailable execution tokens without losing lifetime usage.
2. Normal/failing turns, steering vs unstarted queue, Stop/replacement/shutdown/restart reasons, technical completion and duration lower bounds. No duplicate records/replay and no independent graph lifecycle changes.
3. First assistant emission across all adapters: empty/reasoning-only/final-only content, repeated updates, no-response failure and journal/checkpoint/restart persistence. Legacy timing/role remain unknown.
4. Summary/table/group coverage across pages and filters; period inclusivity/offsets, DST/Monday boundaries, unsupported midnight errors, straddling starts/ends, additive parallel duration and undated usage. Current activity must remain independent of periods.
5. Legacy history, archive/topic archive, project removal/restoration, permanent deletion, reconnect/stale recovery and hidden/visible refresh; desktop, Focus and Flutter-hosted WebView rendering/navigation.

Remaining approved implementation: safe OpenCode/Codex execution accounting; verified monetary provenance if available; model allocation; cost/period rankings; waiting/tool correlated intervals; explicit retry relationships; auxiliary/general-agent inventory and proven own/delegated accounting. No speculative backfill, pricing or promise of native capabilities. Human acceptance remains the next gate, not another automatic verification workflow.
