# Handoff — Session Metrics Dashboard

## 2026-11-02 — develop consolidation supersedes runtime status below

All source work is now centralized in `develop` at `C:\Projetos\Projetos\pessoal\klm`,
including upstream 0.6.7 and the uncommitted OpenCode V2 fixes. The older metrics
implementation below is preserved, but it has **not been ported to SQLite**.
Both `/api/metrics/sessions` endpoints return HTTP 501 on SQLite instead of
reporting empty legacy-state totals. Upstream's `/api/analytics` remains unchanged.
Legacy startup recovery is no longer the engine startup path. A SQLite adaptation
must establish metrics collection, recovery and reads before this dashboard can
be accepted. See `DEVELOP_CONSOLIDATION.md` for backups and focused checks.

## Status — latest Phase 6 consolidation

Uncommitted Phase 1–5 slices are delivered; **the whole plan is not complete**. Phase 6 compiler/documentation work is delivered, but **human acceptance remains pending**. No commits/pushes, tests/full suites, live harness prompts, runtime probes or review agents were run in this consolidation.

Read `AGENTS.md`, `product.md`, `SESSION_METRICS_DASHBOARD_PLAN.md`, `SESSION_METRICS_IMPLEMENTATION_PROGRESS.md`, `SESSION_METRICS_TELEMETRY_CONTRACT.md`, all `SESSION_METRICS_PHASE*_NOTES.md` and the current diff before continuing. Notes describe their individual historical tasks; use progress for consolidated status. Preserve unrelated changes; no broad cleanup, tests/reviews or commits without explicit request. The plan was already untracked at the initial handoff; do not delete it or assume one task authored everything.

## Delivered slices

- **Phase 1:** source-backed native identity/reset/category/monetary/period/non-additivity contract. Not live version validation or proof of every native capability.
- **Phase 2:** durable attempted engine-turn records, outcomes, observed effective models, duration and interrupted-startup recovery. Pi-only identity-bound pre-prompt baseline and replaced endpoint token accounting, isolated from lifetime usage. OpenCode uninstrumented; Codex lacks trustworthy boundary evidence; all cost null.
- **Phase 3:** global Dashboard, all-time filtered summary/table, pagination, chat navigation and execution details; safe public accounting projection and separate observed-execution token totals/coverage.
- **Phase 4:** explicit period analytics with project/historical execution-harness groups and daily/weekly calendar tables. Separate start-count, clipped-duration and engine-end token cohorts; unallocated models, no cost rankings or separate period session ranking.
- **Phase 5 (partial):** first nonempty assistant engine-emission latency, journaled with content; explicit recorded interruption reason counts; role on new executions. No waiting union, tools, retry relations, expanded role inventory or delegated totals.

### Preserve these boundaries

- Rows: normal top-level project conversations including independent spawns; no removed projects, KLM Agent, side/native-child/graph rows. Archived history optional and excluded from current activity. Waiting wins over working; retained idle runtime is not work.
- Executions start at common `execute` entry before preparation, not queue acceptance. Steering is not a new turn. Technical completion is not user approval. Independent graphs keep their own lifecycle.
- Metrics live with Sessions in existing transactions/journal and follow deletion. No historical turn/usage/timing backfill. Restart leaves `endedAt=null`, interrupted/partial observed duration excluding downtime; no duration heartbeat.
- Lifetime snapshots, observed execution amounts and period measures are separate. Parent usage is **not verified exclusive** of delegation; no own/direct or consolidated parent-child totals. Observed-model lists are not token allocations.
- Null means unavailable, never implicit zero/free. Coverage distinguishes sessions (lifetime inventory) from recorded executions (accounting) and period-specific denominators. Unknown older turns are outside observation coverage.
- Pi `proc.Send` transport is unchanged after follow-up; `engine/interactive.go` has no diff. Only baseline/final **response waits** have a 2 s budget after Send succeeds; no bounded-write guarantee, deadline requirement, optional-send redesign or added write goroutine. Identity/reset/malformed evidence invalidates accounting; missing final evidence retains only proven partial amounts. Costs remain null even for Pi; actual matching-runtime currency/valuation is not verified.
- First-response latency measures the first changed nonempty assistant content at engine stream commit before journal append/Sync/publication; excludes reasoning/tools and subsequent persistence/network/render time. No historical scan/native-generation/human-receipt claim. Interruptions may overlap outcome counts; no inferred retry count.
- Public accounting excludes native paths/identities, raw counters and evidence keys.

## API / period contract

- `GET /api/metrics/sessions`: all-time native inventory (`scope=main_native_totals`, `period=all_time`), separate `summary.observedExecutionUsage` and all-time interruptions. Filters: project, current harness, title search, current state, latest outcome, include archived. Descending activity/tokens/duration/executions, stable ID ties, unknown last. Page 1+, size 25 default / 100 max. Summary spans matches before pagination.
- `GET /api/metrics/sessions/{id}/executions`: newest-first all-time recorded details, same pagination; accounting/timing and nullable legacy role. Unavailable/out-of-scope/removed/deleted conversations return 404. Normal session views omit execution history.
- `GET /api/metrics/sessions?view=analytics&from=...&to=...&timezone=...&bucket=daily|weekly`: explicit offset-bearing RFC3339 **[from,to)**, returned normalized UTC. Bounds 1970–9999, at most five years / 400 buckets. UTC default; named IANA area/zone identifiers with embedded data, not host-relative Local/bare offset aliases. Daily local midnight; weekly Monday midnight. DST uses calendar boundaries, not fixed durations; unsupported normalized/ambiguous/nonadvancing midnight cases reject. Full calendar bucket bounds are returned; measures clip to request intersection.
- Analytics `scope=main_recorded_executions`: starts by start instant, observed duration clipped/additive across executions, usable tokens assigned once at engine end (`execution_end_approximation`), not actual consumption date. Different denominators remain explicit. Known amounts without end stay undated and outside dated tokens; undated count spans all matching history. No lifetime tokens in analytics.
- Inventory filters select conversations, not historical execution-result/harness cohorts; grouping uses recorded execution harness. Period parameters without analytics view and unknown views reject. Period controls never alter all-time rows, details or current activity.
- Frontend uses shared HTTP/endpoint resolution, visible-surface inventory polling and snapshot-triggered analytics refresh; stale snapshots are labeled on failures. Applied bounds remain fixed until changed. Cross-client behavior awaits acceptance.

## Current compiler outcomes — exactly one each

1. From `engine`: `go build -tags dev -o <temporary-directory>/klm-session-metrics.exe .` — **exit 0, no diagnostics**; temporary output outside repository removed.
2. From `clients/desktop`: `npx tsc --noEmit` — **exit 0, no diagnostics**.

No fixes/reruns were needed in Phase 6. Compilation is not telemetry/runtime acceptance.

**Historical failures, superseded by the latest consolidation:** initial Go build plus `-gcflags=-e` diagnostic rerun failed on graph Stop/retry/reused-activation fields and missing `chatFilePath`; initial TypeScript failed on unsupported GraphView/ChatMessage props and FileChoice `max_traversals`. Original attempt timestamps were not recorded; these preceded Phase 6, not current failures. Earlier phase successes/follow-up limits remain recorded in phase notes and progress.

## Working-tree file map

Metrics engine files: `engine/session_metrics{,_accounting,_analytics,_api,_timing}.go`; hooks in `engine/{api,harness,main,models,pi_interactive,store,stream,stream_journal}.go`; session-view omission in `engine/graph_records.go`.

Frontend: `clients/desktop/src/features/metrics/{api.ts,SessionMetricsDashboard.tsx,ExecutionAnalytics.tsx,metrics.css}`, navigation in `clients/desktop/src/App.tsx` and `features/projects/ProjectRail.tsx`.

Docs: plan/progress/handoff, telemetry contract, Phase 2–5 notes, `engine/README.md`, `product.md`. This Phase 6 task changed **only** progress, plan final progress sections, handoff, README metrics section and product metrics section.

### Stabilization already present, separate from metrics

Preserved on entry, not authored/reviewed in Phase 6:

- `engine/graph_records.go`: graph Stop/retry/reused-activation durable fields (also contains metrics session-view change).
- `engine/chat_files.go`: shared owned attachment lookup for existing edit/serving paths.
- `clients/desktop/src/App.tsx`: remove unsupported GraphView callback (also contains Dashboard routing).
- `clients/desktop/src/features/chat/SessionEvent.tsx`: remove unsupported ChatMessage props.
- `clients/desktop/src/features/graphs/files.ts`: optional `max_traversals` contract.

Compiler success includes these changes; graph/edit/attachment runtime acceptance is not established.

## Next gate and remaining scope

Use the pending human route in progress: real Pi normal/resumed/cache/partial/timeout/reset accounting and unchanged status bar; unavailable OpenCode/Codex execution data; queue/steering/failure/Stop/replacement/shutdown/restart; emission latency persistence/no-response/legacy cases across adapters; period offsets/DST/Monday/straddling/undated/parallel semantics; filters/pagination; archive/removal/deletion; stale/hidden refresh; desktop/Focus/mobile navigation and layout. No additional automatic checks are implied by this handoff.

Remaining approved implementation: safe OpenCode/Codex accounting, monetary provenance where actually available, model allocation, cost/period rankings, request-union/tool timing, explicit retries, auxiliary/general inventory and verified own/delegated accounting. Do not invent unavailable native capabilities or claim all phases accepted. Human validation is the next recommended gate; do not claim it happened or use it as authorization for full suites/reviews.

## Resume instructions for the next session

The user explicitly requested **subagents for each plan phase**. This session used separate Pi CLI child processes for stabilization and Phases 1–6, plus a Phase 2 corrective follow-up; no dedicated subagent tool was available. Preserve this delegation requirement when continuing. Use available subagent tooling, or isolated CLI child sessions if necessary; do not silently replace delegation with inline work. These were implementation/documentation agents, not review agents.

1. Re-read project instructions, product, this handoff, consolidated progress and the native contract; inspect `git status --short` and the current diff before editing. There may be newer user changes.
2. Report which phases remain partial and choose the next coherent slice from the remaining approved scope. Do not treat the dashboard's presence or compiler success as completion.
3. Prefer the pending human acceptance route first. If asked to continue implementation instead, use focused phase-specific subagents with disjoint file ownership, sequentially when their changes depend on earlier phases.
4. Preserve lifetime usage/status-bar semantics, null/coverage rules and non-additivity. Never infer missing costs, native baselines, model attribution, timing or retries.
5. Update progress/handoff with actual implementation and check evidence. No commits/pushes, full suites, complex tests or adversarial review agents without explicit authorization.

The final consolidated Go build and TypeScript check both passed before this handoff-only edit. No runtime acceptance was performed. All source/documentation changes remain uncommitted; no application code was changed for the handoff request.
