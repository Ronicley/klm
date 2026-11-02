# Session metrics — Phase 4 focused distribution/trends slice

## Delivery and ownership

Changed only `engine/session_metrics_api.go`, new `engine/session_metrics_analytics.go`, and metrics frontend files (`api.ts`, `SessionMetricsDashboard.tsx`, `metrics.css`, new `ExecutionAnalytics.tsx`), plus this note. Existing unrelated changes were preserved. No adapter, durable schema, route registration, native status-bar, product or other documentation changes.

A separate **Period execution analytics** section sits above the explicitly labeled **All-time inventory · Current activity** section. It uses simple existing-style tables, without chart libraries/dependencies. Period controls do not alter lifetime cards, session rows, execution-detail pagination, working-now or waiting-now counts.

## API

Use the existing route in a separate projection mode:

```text
GET /api/metrics/sessions?view=analytics&from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00Z&timezone=America/Sao_Paulo&bucket=daily
```

- `from` and `to` are required RFC3339 instants with explicit offsets, normalized and returned as UTC, with inclusive `from` and exclusive `to`. Date-only, missing and reversed bounds are rejected.
- `timezone` defaults to `UTC`. Named IANA area/zone identifiers and `UTC` are accepted; unknown zones, host-relative `Local`, and bare fixed-offset aliases are rejected. Standard-library embedded timezone data avoids reliance on Windows zone files; no third-party dependency.
- `bucket` defaults to `daily`; `weekly` starts Monday. Boundaries use local calendar midnight and `AddDate`, not fixed 24/168-hour durations. Ordinary DST transitions produce 23/25-hour daily buckets. If Go normalizes a nonexistent midnight, produces a nonadvancing date, or selects an ambiguous repeated midnight boundary, the request is rejected rather than silently shifting the bucket. Such historical midnight-transition cases are deliberately not promised as supported.
- Period bounds are limited to 1970–9999 and at most five years; at most 400 buckets. Longer daily selections can use weekly or a shorter period.
- Bucket `from`/`to` expose nominal full-calendar UTC boundaries; aggregation is clipped to the intersection with the requested period. Edge buckets are not expanded beyond the requested interval.
- Without `view=analytics`, period parameters are rejected rather than silently filtering an all-time dashboard incorrectly. Unknown `view` values are rejected.

The response contains `updatedAt`, `scope=main_recorded_executions`, and `analytics` with normalized `period`, `summary`, project groups, execution-harness groups, buckets, matching-session/history counts, undated-usage count, and attribution labels. Aggregates span the entire matching inventory, not its current page. Group order is stable by identity; no cost ordering.

Existing project/harness/title/archive/current-state/last-result filters select **conversations**, as in the inventory. They do not become execution-result or historical effective-harness filters. The UI explicitly distinguishes that selection rule. Historical grouping itself uses each recorded execution's harness; missing harness is unallocated. Removed projects and non-top-level/side/native-child/graph/general conversations remain outside the existing inventory scope. There is no additional retention store.

## Measures and coverage

Each summary/group/bucket has independent measures:

- `executions`: recorded execution starts in `[from,to)`; never inferred native turns.
- `durationMs` and `durationCoverage`: known observed execution intervals clipped to the period, accumulated across executions (parallel work is additive, not project elapsed time). Settled records use their known end; recovered interrupted records use the observed boundary, without inventing an end; live records use the response snapshot and remain partial. Invalid known intervals in the start cohort count unavailable. Duration coverage counts intersecting observed intervals, not the token or execution-start denominator. Integer overflow withholds the duration amount.
- `usage`: known execution input/output/total tokens and complete/partial/unavailable execution coverage, using the Phase 3 checked aggregation. Its cohort is records with a valid end inside the period, even if they started earlier. Whole execution amounts are assigned once at engine end with `timeAttribution=execution_end_approximation`; this is not a native consumption timestamp or a daily split of a long execution.
- `undatedUsageExecutions`: known noninvalidated usage records with no engine end, counted across matching all-time history and excluded from dated totals. Live/recovered amounts are not dated by restart or request time. The UI labels this count's all-time scope.
- `sessionsWithHistory / matchedSessions`: visibility into sessions with recorded metrics; unknown pre-observation turns are outside every recorded-execution denominator.

Execution-start, intersecting-duration and ended-token coverage denominators are intentionally different and visible in table headers/coverage. No usable execution token evidence remains null; positively recorded zero remains zero. Partial/unavailable coverage marks numeric totals partial. No native lifetime `Session.Usage` value enters these analytics.

Model token attribution is explicitly **unallocated** for all current execution amounts. Neither requested/current model nor the observed-model list supplies allocation evidence. No model ranking/split is invented. Project and harness tables compare independent recorded observations, not proven exclusive direct/delegated work.

## Frontend

- Explicit instant text controls, IANA timezone, daily/weekly choice and Apply period. Initial interval is an explicit trailing 30-day snapshot, not a moving implicit period. Applied bounds are normalized to UTC; subsequent refresh keeps those bounds.
- Period total, project, recorded execution harness, and calendar bucket tables display execution starts, clipped observed hours, execution-end tokens and coverage.
- Uses existing inventory snapshot refreshes while visible, plus Retry; retains and labels the last snapshot on refresh failure. Changing the applied query clears prior data. Current activity remains independently all time.
- All-time lifetime/native totals remain separate and labeled; all-time observed execution usage is also explicitly labeled. No native/execution amounts are added together.

## Deliberate omissions

No cost or average-cost collection, pricing, currency assumptions, cost ranking, effective-model allocation, native-consumption timestamp collection, native descendants/delegated rollups, auxiliary/general-agent inventory, extra timing/retry instrumentation, native backfill, new transport, export, chart library, or analytics persistence. Existing all-time duration sorting remains available; no separate period session ranking or drilldown was added. Existing execution details remain all-time, under the all-time inventory.

## Lightweight validation and remaining acceptance

Ran `gofmt` on the two owned Go files. Ran exactly one `npx tsc --noEmit` in `clients/desktop`; it completed with no diagnostics. A subsequent small frontend follow-up tightened the explicit-instant regex and ignored aborted responses; no overlapping check was rerun. No Go build was run: consolidated Go compilation is left to the coordinating agent. No tests/suites, review agents, live prompts, commits or pushes.

Human validation remains pending: real Pi known/partial/unavailable execution amounts; old histories; period inclusivity/exclusivity and offset normalization; DST/calendar-week boundaries and explicit rejection of unsupported historical midnight boundaries; execution starts and ends straddling periods; additive overlapping durations; restart records with undated usage; archive/project removal/deletion and shared inventory filters; current activity unaffected by period; refresh/error recovery; desktop/Focus/mobile presentation. TypeScript compilation is not runtime or telemetry acceptance.
