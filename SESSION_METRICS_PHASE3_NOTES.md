# Session metrics — Phase 3 accounting projection

## Changes and ownership

Changed only `engine/session_metrics_api.go` and `clients/desktop/src/features/metrics/{api.ts,SessionMetricsDashboard.tsx,metrics.css}`, plus this note. Existing unrelated working-tree changes were preserved. No adapter, durable schema, status-bar, routing, documentation or product changes were made.

## API additions

### Existing `GET /api/metrics/sessions/{id}/executions`

Every row now uses an explicit public projection of the existing execution fields and includes `accounting` (null for legacy records without accounting). Existing fields, newest-first order, pagination and live duration calculation remain intact.

Non-null `accounting` exposes:

- `version`, `protocol`, optional `harnessVersion`.
- `coverage` (`complete`, `partial`, `unavailable`) and optional machine-readable `reason` from durable accounting.
- `usage`: null or normalized `inputTokens`, `outputTokens`, `cacheReadTokens`, `cacheWriteTokens`, nullable `reasoningTokens`.
- `cost: null`, `costCoverage: unavailable`, durable `costReason`.
- `modelAttribution`.
- `source`: `pi.get_session_stats` only when Pi statistics evidence exists; otherwise null.
- `baselineObservedAt`, `endpointObservedAt`: observation timestamps or null.

The public projection excludes accounting epoch, native IDs, native file paths, RPC/evidence keys, baseline/endpoint counters and raw evidence. It does not serialize `ExecutionAccounting` directly. Known usage is copied while the state lock is held. No additional native data is collected.

### Existing `GET /api/metrics/sessions`

Added `summary.observedExecutionUsage`, independent of the existing native lifetime token fields:

```text
inputTokens, outputTokens, tokens: nullable integer
coverage: { complete, partial, unavailable, total }
cost: null
averageCost: null
costCoverage: { complete: 0, partial: 0, unavailable: total, total }
reason?: counter_overflow
```

The cohort is every recorded execution of every session matching the existing filters, before session pagination. It is all-time observed execution history, not reconstructed native lifetime history. Legacy executions without accounting count as unavailable. Only noninvalidated complete/partial usage contributes; partial amounts remain partial. No usable amounts produces null, whereas positively observed zero remains zero. Counts distinguish complete, partial and unavailable executions. Checked token addition withholds all aggregate token amounts on overflow and returns `reason=counter_overflow` without changing the evidence coverage counts.

The frontend marks a numeric aggregate partial whenever any execution is partial or unavailable. Unknown pre-observation turns are outside this recorded-execution denominator. The existing session execution-coverage/since fields remain unchanged.

No native lifetime field is replaced, subtracted or added to execution amounts. No native child, side chat, graph node or general-agent usage is added. These remain native parent observations, not proven exclusive/direct usage. The existing `scope=main_native_totals`, `period=all_time`, filters, sorting, summary scope, session and execution pagination are unchanged.

## UI additions

- Existing session summary/table tokens are explicitly labeled **Lifetime tokens**.
- A separate **Observed execution usage** group shows tokens, input, output, unavailable cost and execution coverage counts across the filtered cohort. Numeric partial totals carry a visible `partial` suffix.
- Each execution's existing details row now has an accounting panel with normalized input/output, cache read/write labeled as included in input, nullable reasoning, coverage, concise English reason, cost unavailability/reason, source, protocol/version, baseline/endpoint observation times and model-allocation availability.
- The execution model column says **observed models**, avoiding a claim that the listed models have allocated token amounts.
- Missing numeric metrics and cost display **Unavailable**, not zero. Valid zero amounts remain visible. Partial execution token/category amounts carry a visible suffix independently of duration coverage.
- Concise English reason labels are mapped from known accounting codes; unrecognized reasons use a neutral fallback rather than rendering raw evidence.
- Existing design tokens and Metric rendering are reused; panels wrap on compact layouts. Existing chat navigation, filter behavior, pagination, retry and polling are unchanged.

## Deliberate omissions

- No OpenCode/Codex collection, Pi adapter changes, native-version validation or currency/provenance verification.
- Costs and average costs remain null, including for Pi. No decimal arithmetic, pricing tables, estimated billing, currency assumption or monetary aggregation.
- No per-session execution-usage aggregate or execution-usage sort/filter; only the filtered summary aggregate and paginated execution details are added.
- No per-model token allocation, delegated totals, backfill, date filtering, buckets/trends, timing breakdowns or retry inference.
- No raw-evidence inspector, native path/identity exposure or frontend reconstruction of deltas.

## Lightweight validation

Ran `gofmt -w engine/session_metrics_api.go` and exactly one TypeScript check: `npx tsc --noEmit` from `clients/desktop`, which completed successfully with no diagnostics. No Go build, tests, reviews, commits or pushes were run.

Human validation remains pending for real Pi multi-turn/cache usage, partial/final timeout and cancellation amounts, restart/legacy unavailability, unchanged native lifetime totals, filtered aggregate coverage across pages, and desktop/Focus/mobile layout. Go compilation is left to the later consolidated check as requested.
