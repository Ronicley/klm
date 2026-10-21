# Topics — requested native-subagent review

## Scope

The user requested one native subagent to review the Topics implementation and all
subsequent adjustments, followed by corrections and commit/push. The reviewer was
read-only and reviewed the current engine, shared frontend, Topics plan/progress
and product contracts. It did not execute harnesses, graphs, tests or builds and
did not change files. Pre-existing unrelated feed/adapter/development changes were
kept distinct from the Topics review.

## Findings and corrections

### 1. P1 — Archived continuation turn entry points

Linked-answer delivery skipped effective archival for the answering continuation,
and graph-result notification delivery checked project removal but not session/topic
archive state. Both schedulers now consult `conversationArchived` before reserving
a turn. Pending answers and notices stay pending until effective restoration;
already-running work is not cancelled. The existing scheduler ticker can deliver
them after restoration.

Files: `engine/linked.go`, `engine/graph_scheduler.go`.

Regression check: `TestArchivedTopicDefersContinuationTurns` exercises both entry
points and asserts no turn reservation, delivery attempt or durable mutation while
the topic is archived. It uses no execution context or runnable harness.

### 2. P2 — Async move overwrote newer collapse preferences

Destination expansion previously filtered the collapse array captured before the
request. It now uses a functional update through the parent's current navigation
state, removing only the destination ID. Other collapse changes made while the
request was pending are retained.

Files: `WorkspaceSidebar.tsx`, `App.tsx`.

### 3. P2 — Desktop rail sizing leaked into narrow navigation

The narrow browser drawer now explicitly retains its 256px width independently of
the desktop custom-property width and available-chat-space calculation. The saved
wide-layout preference is preserved. Flutter's existing full-width navigation
override remains in place; the resize handle stays hidden in narrow/mobile layouts.

File: `clients/desktop/src/styles.css`.

### 4. P2 — New session hidden in collapsed Ungrouped

Successful global creation now removes the new session's group key from that
project's collapse state in the same functional update that selects the session.
Other projects and group preferences are preserved.

File: `clients/desktop/src/App.tsx`.

## Focused verification after corrections

- `go test -tags dev -run '^TestArchivedTopicDefersContinuationTurns$' -count=1 .`
  from `engine`: passed (`ok klm/engine`). No harness or graph execution.
- `go test -tags dev -run '^TestSessionSpawn' -count=1 .` from `engine`: passed
  after adapting the pre-Topics fixtures to an assigned source topic and normal
  startup defaults. Existing fixture recovery checks initially exposed missing
  Activity Board/Library normalization in the fixture, which was corrected rather
  than changing production persistence. The existing spawn checks now also assert
  child/receipt topic identity, persistence on reload and identical-retry stability
  after a parent's topic changes. Harness scheduling stays frozen in these fixtures.
- `go build -tags dev -o "C:\Users\Samuel\AppData\Local\Temp\opencode\klm-topics-dev.exe" .`
  from `engine`: passed. The binary was not executed.
- `npm exec -- tsc -b --pretty false` from `clients/desktop`: passed.
- Changed Go files were formatted with `gofmt`.

No full test suites, installer builds, installed-data probes or second review agent
were run. Compilation and source review are not human runtime acceptance.

## Remaining human acceptance

- Pending linked answers and graph-result notifications stay deferred while a
  session/topic is archived and deliver after restoration, without stopping the
  independent work.
- A delayed move expands only its destination, preserving collapse changes made
  during the request.
- New independent sessions reveal Ungrouped immediately, including after reload.
- Wide rail preferences survive switching to a narrow browser drawer and back;
  Flutter navigation remains full-width.
- The broader migration/restart, three-harness role-tool, concurrent move/spawn,
  drag/drop, touch menu and theme checks remain in `TOPICS_IMPLEMENTATION_PROGRESS.md`.
