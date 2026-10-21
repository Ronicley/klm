# Topics — implementation progress

## Delivery status

Implemented in the Go engine and the shared React/TypeScript client. Go dev and
TypeScript compilation pass. Native harness execution, migration/restart behavior
and visual acceptance have **not** been runtime-tested in this implementation turn.
The manual checklist below remains the acceptance gate.

Reference: `TOPICS_IMPLEMENTATION_PLAN.md`, `AGENTS.md`, `product.md`, and the
user's `C:/Users/Samuel/Downloads/topics.png` mockup, all read before editing.

## Engine

- `Project.Topics` stores `{id,name,color,createdAt,archived?}`. A normal main
  session's `topicId` is the authoritative association. Topic names are not keys;
  equal names do not merge groups. Initial color is `neutral`.
- `topicSchema:1` is committed through `commitLocked` at engine startup **after
  both journal segments replay**, before normal work is served/scheduled. Each
  legacy folder becomes a distinct topic in its project, retaining archive state.
  Legacy empty/Ungrouped labels stay unassigned. Existing legacy session labels
  outside the folder list are also preserved as groups instead of dropping them.
  The migration does not read conversation content, start agents, or touch project
  execution directories. It applies to removed projects too, preserving restoration.
- The migration marker, topic records and memberships form one synced transaction.
  Generic transaction cloning/diff/replay/checkpoint handles the new fields;
  topic validation runs at commit, replay and checkpoint boundaries. The migration
  is skipped after its durable marker is present.
- Historical `folders`, `archivedFolders`, `workspace`, and accepted spawn request
  shapes remain readable. They are not competing grouping controls. New folder
  API mutations return 410; session workspace/position mutations are rejected.
  Historical spawn request comparison/idempotency is unchanged.
- `session_topic_set({name})` accepts only a trimmed, nonempty, control-free UTF-8
  label of at most 60 characters. Its identity comes from the authenticated active
  adapter/turn. Only a normal project main conversation receives it; side, general,
  graph-node and native-subagent roles cannot assign topics. Creation and association
  commit together. Repeated/concurrent calls return the existing canonical topic
  without renaming or recoloring it, including after a user edit.
- The shared role-tool catalog/dispatch exposes this tool through Pi's generated
  extension and the OpenCode/Codex private MCP transport. It is internal to
  `klm_linked`, separate from the external `klm_library` capability. Current topic
  JSON and initial-assignment instructions accompany ordinary turns through the
  shared execution path; no extra classification turns are scheduled. Saved queued
  inputs keep static guidance so assignment while they wait does not leave a stale
  no-topic instruction in their prepared input.
- Spawn copies the source main's topic ID inside the child-creation transaction,
  including side-origin spawn and further descendants. A normal source without a
  topic receives recoverable `topic_pending` before creation; a side agent is told
  the main must assign it. Spawn revalidates effective topic/availability under the
  lock after model-catalog I/O. Accepted identical retries still return their prior
  receipt before new creation validation. New grouping overrides are rejected.
- General-agent remains without a topic. Its project children start unassigned
  and classify themselves in their normal turn. Its catalog no longer offers
  `session_move` or folder creation/restore destinations; session title rename and
  other existing controls remain. Discovery/details expose effective topic data;
  its session list supports `topicId` filtering.
- New UI API: `PATCH /api/projects/{projectId}/topics/{topicId}` with any of
  `{name?,color?,archived?,position?:{beforeId}}`, returning the canonical project. Name/color can be
  updated atomically. Palette keys are `neutral`, `blue`, `purple`, `green`,
  `amber`, `rose`. This uses the existing personal-use public network contract;
  no new claim of authenticated human identity is made. Agent tools do not expose
  topic rename/color edits.
- User-approved follow-up: topic headers can be reordered and active normal main
  sessions can change topic through UI. `Project.topicOrder` is optional and
  durable through normal clone/commit/replay/checkpoint/SSE; absence retains the
  initial chronological group order. The first reorder records the effective order,
  including archived topics, then positions the requested topic. New unpositioned
  topics precede the saved order. IDs are validated against the same project and
  duplicates are rejected; removed-project restoration preserves this field.
- `PATCH /api/sessions/{id}` accepts `topicId` for a user move to an existing active
  topic in the same project. Source must be a normal active top-level session;
  empty/foreign/archived destinations and combined move+archive requests are rejected.
  Only that main's association changes; the linked side resolves it dynamically.
  Existing descendants/receipts, history, execution directory, runtime and applied
  Library selections remain intact. Future spawns inherit its current topic. A
  topic move during model-catalog validation rejects new acceptance with recoverable
  `topic_changed`; a concurrently accepted identical operation still returns its
  existing receipt. Agents receive no reassignment or topic-reorder tool.
- Availability resolves topic archive state, including side conversations through
  their main. Archived history is readable; new messages, sends, consultations and
  queued execution require effective restoration. Archiving does not cancel an
  already-running turn. Restoring a member under an archived topic requires explicit
  restoration of the topic first, without changing membership.
- Topic IDs appear in full session and summary HTTP/SSE projections. Side summaries
  resolve the main's effective topic without creating an independent persisted side
  association. Project registration/restoration preserves topics. Runtime disposal,
  Stop, session settings, spawn authorization/receipts and applied Library inheritance
  retain their existing mechanisms. Graph execution workspaces are unchanged.

Primary engine files: `topics.go`, `store.go`, `main.go`, `transactions.go`,
`stream_journal.go`, `history.go`, `runtime.go`, `api.go`, `session_controls.go`,
`session_collaboration.go`, `session_spawn.go`, `message_queue.go`,
`project_registration.go`, `graph_adapter.go`, `linked_bridge.go`, `harness.go`,
`general_agent.go`, `general_agent_tools.go`, and the collaboration/general prompts.

## Shared client

- The sessions sidebar now has a right-edge horizontal resize handle on desktop
  and wide browser/Focus layouts. Preferred width is stored locally per origin/
  engine (`klm.sessions.width.v1`), shared across projects; default 256px, bounds
  200–520px with a further available-space limit reserving 400px for the main pane
  where space permits and accounting for a docked side chat. Pointer capture keeps
  dragging active outside the edge; Left/Right, Home/End and double-click reset are
  supported. The handle has separator semantics and a visible hover/focus/drag
  indicator. Temporary window constraints do not overwrite the preference, and
  narrow navigation/mobile host layouts retain their existing presentation.
- Sidebar uses topic blocks with a 5px colored left border, transparent other
  borders and compact name headers, following the user's visual adjustments.
  Session rows have square corners, no lateral list inset, and one hover/selection
  background spanning their text/status and More button. Clicking a
  header collapses/expands its members with `aria-expanded`. Normal row selection,
  action menus, runtime LEDs and mobile runtime/recency subtitles remain. There is
  no duplicate per-row topic subtitle. Tasks/Agents/Graphs/project settings remain.
- Desktop/browser session rows now include a recency subtitle below the title,
  reusing the mobile last-user/assistant-message timestamp and date formatting.
  It appears for active, inactive and archived rows, with the runtime LED retained
  separately. Relative labels refresh every minute while visible and on focus/
  visibility return; no extra engine requests or ordering changes are introduced.
- Members sort newest-created first, preserving RFC3339Nano precision, with stable
  session-ID ties. Topic blocks initially sort by their newest-created **persisted**
  member, including archived members, with topic-ID ties; a saved manual order takes
  precedence for positioned topics. Ungrouped remains a neutral
  section for sessions awaiting assignment. Messages, activity and runtime state
  do not affect this ordering. Happening now's independent manual ordering remains.
- Search matches topic names and session titles; searching temporarily expands
  groups without changing saved collapse state. `collapsedTopics` uses topic IDs
  inside existing origin/engine-scoped navigation storage and per-project records;
  legacy `collapsedFolders` names are deliberately ignored. Other saved navigation
  is retained.
- The navigation's folder selector is replaced by a topic tag. Clicking it opens
  `TopicDialog` to edit Name and Color for the whole group. Save is pending/disabled
  while in flight, errors stay recoverable, and Cancel does not apply changes.
  Color choices are English-labeled selectable chips with pressed state and a check
  on the selected color. Theme-aware semantic colors
  affect only headers, outlines, tags and editor swatches; session surfaces and
  runtime LED colors remain unchanged. Long names truncate with full-text titles.
- Global New session posts no grouping override; folder-create/create-in-folder and
  within-topic session reorder controls remain absent. New unassigned sessions
  appear immediately.
- Topic headers are draggable, with before/after insertion markers. Session rows
  are draggable to active topics, including collapsed groups; successful moves
  expand the destination. Dropping does not open a chat or toggle collapse. Failed
  mutations retain the canonical inventory and display a retryable error. Header
  reordering is disabled during search; archive sections and Ungrouped are not drop
  destinations. Menus provide Move up/down and Move to topic for keyboard/touch use.
  Native drag-and-drop and touch behavior still require runtime acceptance.
- Archived members and topics appear under Archived. Their history can be selected
  in the main chat. An archived topic offers explicit Restore topic; members retain
  their independent archive flags, and the composer cannot send until restoration.
  Sidebar restore failures and editor failures are visible without losing edits.
- Project Rail, Happening now, overlays, Focus floating-window reconciliation,
  mobile sheets and session-spawn cards use topic-based availability. Navigation
  to an active session expands its topic by ID. Shared code serves desktop, browser
  Focus and Flutter-hosted WebView; no native Flutter changes were needed.

Primary client files: `engine.ts`, `App.tsx`, `WorkspaceSidebar.tsx`,
`CreateSessionDialog.tsx`, `TopicDialog.tsx`, `topics.ts`, `ProjectRail.tsx`,
`KlmAgentWorkspace.tsx`, `SessionSpawnEvent.tsx`, `styles.css`, and
`design-system/tokens.css`.

## Verification evidence

Only lightweight compilation checks were used:

1. From `engine`:
   ```powershell
   go build -tags dev -o "C:\Users\Samuel\AppData\Local\Temp\opencode\klm-topics-dev.exe" .
   ```
   Final result: exit 0, no compiler output. The temporary binary was not executed.
2. From `clients/desktop`, using the same `tsc -b` phase as its actual `build` script:
   ```powershell
   npm exec -- tsc -b --pretty false
   ```
   Final result: exit 0, no TypeScript errors. Vite bundling/installer generation
   was not run.

Changed Go files were formatted with `gofmt`. The initial compile attempt caught
missing topic model fields and removal of a shared helper still used by project
ordering; these were fixed and both checks passed. After the final topic timestamp,
UI error handling and nanosecond-ordering adjustments, both checks passed again.

After the session-rail horizontal resizing adjustment, the same TypeScript command
(`npm exec -- tsc -b --pretty false` from `clients/desktop`) passed with exit 0 and
no output. No Go files changed for this adjustment. Pointer, keyboard, persistence
and responsive layout behavior remain manual acceptance checks.

After the user-approved topic reordering and session move follow-up, both commands
above were run again: Go dev build exited 0 without output, and TypeScript exited 0
without errors. Changed Go files were formatted with `gofmt`. The temporary engine
binary was not executed; drag/drop, menu actions, persistence across restart and
concurrent move/spawn behavior remain human runtime acceptance checks.

The initial implementation used no full suites, new integration/complex tests,
runtime probes or review agents. The user subsequently requested a native-subagent
review, corrections and commit/push. That review found four issues, now corrected:
archived continuation entry points, stale async collapse updates, narrow-browser
drawer sizing and collapsed Ungrouped after creation. The focused archive-deferral
and existing spawn checks, Go dev build and TypeScript check passed after correction;
see `TOPICS_IMPLEMENTATION_REVIEW.md` for findings, fixture adjustments, exact
commands and remaining runtime acceptance. No full suites, installers, graphs or
installed-data probes were run. Compilation/source review is not human acceptance.

## Manual acceptance checklist — not yet performed

- [ ] Drag active topic headers before/after one another, expanded and collapsed;
      confirm insertion markers, preserved selection/collapse and persistent order
      after reload/restart/project restoration. New topics appear ahead of the saved
      order. Validate Move up/down by keyboard and touch.
- [ ] Drag active sessions between active topics and from Ungrouped into a topic,
      including a collapsed destination. Confirm expansion, chronological member
      order, title/subtitle/LED/More styling, and no accidental chat navigation.
      Validate Move to topic via menu and failed-request recovery.
- [ ] Reject foreign/archived/empty destinations and non-main sources. Archive/
      restore must preserve positions and memberships. Move a running main with a
      side chat, verify the side follows it, existing children stay put, and future
      authorized spawns inherit the current topic. Exercise a move concurrent with
      spawn validation, then retry an already accepted receipt unchanged.
- [ ] Resize the sessions sidebar in native desktop and browser Focus. Check
      bounds, pointer release/cancel, keyboard controls and double-click reset.
      Switch projects/reload and verify the preferred width persists. Resize the
      outer Focus window and open/resize the side agent; check the available-space
      limit and restoration of the preference. Narrow/mobile navigation retains
      its existing layout without the handle.
- [ ] On Pi, OpenCode and Codex, create a normal independent session and send a
      real task/clarification. It starts under Ungrouped and assigns a short neutral
      topic during that ordinary turn, without another automatic turn.
- [ ] Repeat the assignment call, then rename/recolor in UI and run another turn.
      The same ID, user-selected name and color remain; no duplicate topic appears.
- [ ] Explicitly request a child, descendant and side-origin child. Confirm the
      exact source main topic ID is inherited, with normal attribution, Library
      selection, harness/model/effort/YOLO and idempotent receipt behavior.
- [ ] Try authorized spawn while the main still has no topic: it returns
      `topic_pending`, creates nothing, and the main can assign then retry. Side
      cannot assign on its behalf. General-agent project children classify themselves.
- [ ] Verify tool visibility/scope on all three harnesses: no assignment for
      general, side, graph-node or native-subagent roles; no agent rename/color/move
      control and no external Library token access to the private assignment tool.
- [ ] Global New session does not inherit the selected topic. Equal topic names
      remain separate identities. The selected session/LEDs stay intact during
      assignment and edits.
- [ ] Create multiple same-topic sessions close together. Confirm newest-created
      ordering, continuous blocks, stable ties and unchanged positions after
      messages, Stop, runtime disposal, archive changes and engine restart.
- [ ] Edit name/color; validate shared header/tag updates, Cancel, API-error retry,
      long-name truncation, keyboard/focus/chip controls and light/dark contrast.
- [ ] Collapse/reload, search by title/topic, clear search and open a session from
      Happening now or a spawn card. Its topic expands without overwriting other
      collapse preferences or Happening now's saved order.
- [ ] With a disposable legacy data copy through normal engine startup, validate
      active/archived folder migration, no-topic Ungrouped, removed-project restore,
      original session IDs/history/native metadata/queues/receipts and checkpoint +
      journal replay after restart. Reopening does not duplicate topic identities.
- [ ] Archive/read/restore sessions and topics. Restore the topic explicitly before
      a member; membership and individually archived states remain intact. New
      execution is unavailable while archived, and archive does not act as Stop.
- [ ] Validate archive availability in rail, Happening now, overlays, spawn cards,
      browser Focus windows and mobile sheets. Preserve drafts, history, requests,
      composer/Stop controls and mobile navigation.

## Scope boundaries

No retrospective LLM classification, topic merging, agent-driven reassignment, manual
topic creation, automatic subject changes, free RGB palette, graph lifecycle work
or redesign beyond the requested grouping/editor. Pre-existing unrelated local
changes were preserved with targeted edits. `code.html` was not changed.
