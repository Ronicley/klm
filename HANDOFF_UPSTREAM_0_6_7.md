# Handoff — upstream 0.6.7 reconciliation

## Current outcome

The 37 textual merge conflicts are resolved and staged. The four approved fork
mutations have SQL adapters; legacy agent binding migration and SQL graph question
context are also source-complete with focused fixture evidence. **Overall rollout
acceptance remains incomplete; this is not a deployable release.** Real migration,
native harness and client execution below remain unvalidated. A later authorized
clean local installation of desktop/engine 0.6.7 is recorded below; it does not
establish migration of the user's historical profile.

Follow-ups on 2026-11-01: OpenCode discovery/installation compatibility and all four
SQL adaptation stages are delivered in source. Retry adds publication schema 6
for indexed imported-operation identity. Follow-up changes/tests/documentation
remain unstaged. The merge remains uncommitted; metrics worktree remains untouched.
The two later compatibility slices used separate implementation subagents, added
no schema version, and did not redo those four mutation adapters.

Read this document first, then `AGENTS.md`, `product.md`, and
`UPSTREAM_0_6_7_INTEGRATION.md`. The older `HANDOFF.md` describes the previous
integration and does not authorize a commit/push for this work.

## Exact Git/worktree state

- Active directory: `C:\Projetos\Projetos\pessoal\klm-upstream-0.6.7`.
- Branch: `update/upstream-0.6.7-20261101`.
- HEAD: `b4e03123a4bdee5b5a3d567e72b8b3b3ef81d55e` (fork baseline).
- MERGE_HEAD: `d1857b06df6c58284b8304f22bf05103fb47b392`
  (`samuelhk0595/klm`, `upstream/main`, tag `v0.6.7`).
- Merge was started with `git merge --no-commit --no-ff upstream/main`.
- At handoff preparation, all reconciliation changes were staged; the unmerged
  list and unstaged/untracked lists were empty. This handoff and the historical
  handoff pointer were subsequently written as documentation-only changes.
- No merge commit was created. HEAD has not advanced. No push was run.
- Original directory `C:\Projetos\Projetos\pessoal\klm` remains on `develop` with
  its original uncommitted session metrics work. Those changes were deliberately
  not imported. Do not overwrite that worktree or apply its diff indiscriminately.

Before continuing, inspect `git status --short --branch`,
`git diff --cached --stat`, and `git diff --name-only --diff-filter=U`.
Most integration work is in the index: a plain `git diff` does not show it.
Do not restart the merge, switch versions, reset the index, or abort the merge
merely because HEAD still names the old baseline.

## User intent and authorization

The original symptom was model discovery failing when changing a Pi-created
agent to OpenCode. The user cancelled instrumentation and chose to integrate
upstream KLM in an isolated worktree, then asked to reconcile conflicts.

The added instrumentation from that investigation was undone in the original
worktree. The original model-loading failure was **not reproduced or proven
fixed** by this integration. Do not diagnose it as a confirmed version mismatch.

After the handoff, the user authorized the OpenCode compatibility slice and approved
a sequential plan for SQL graph controls, node retry, message edit/revert and session
deletion. The user then explicitly requested implementation of all stages with
subagents: three implementation agents delivered the remaining stages, with shared
gates/integration/verification in the parent session. No review agents were used.
This does not authorize a commit, push, actual installation, running engine, full
suite or migration of live data. Do not infer additional permission from history.

The next request on 2026-11-01 explicitly authorized the two remaining source
compatibility slices: legacy selected-agent binding migration and SQLite graph
question context, using one implementation subagent per slice. The four SQL
mutation adapters above are already complete and must not be reimplemented.
Preserve the existing merge index and all prior working changes. Real validation
still requires separate authorization, an isolated profile/ports and verified backup.

## Delivered source changes

See `UPSTREAM_0_6_7_INTEGRATION.md` for the full reconciliation inventory.

- Retained upstream SQLite startup, client authentication, contract-3 HTTP/SSE,
  paginated history, analytics, engine switching and reusable-agent contracts.
- Reconciled V1/V2 OpenCode catalog/transport fields, native protocol identity,
  SQL-backed version settings and inventory refresh hooks.
- Added schema 5 for fork OpenCode settings, speech key, deletion receipts and
  image metadata. The additive upgrade does not invent historical records.
- Added schema 6 for `graph_runs.retryOperationId` routing/index backfill, preserving
  random imported run IDs and original content. Fresh stores use schema 6.
- Connected multipart files/images to SQL message admission, receipts, queue/user
  metadata, upload ownership and authenticated preview requests. Legacy image-ID
  submissions require explicit reselection instead of silently losing attachments.
- Preserved attachment recovery metadata in protected desktop drafts. File bytes
  are not persisted; reloading requires reselection. Acceptance compares attachments
  before clearing a draft.
- Adapted transcription provider selection and speech-key fallback to SQL.
- Reconciled graph dynamic handles, attention/measurements, activity pagination,
  transfer types, native commands and Flutter file/download channels.
- Preserved fork branding/layout and deliberate deletion of `.vscode` files.

## Delivered adaptations and remaining acceptance

### 1. OpenCode discovery and installation compatibility — source fix delivered

Relevant files: `engine/opencode_settings.go`, `harness_discovery.go`,
`harness_install.go`, `models.go`, `opencode_interactive.go`, `opencode_v1.go`,
`opencode_v2_catalog.go`, `opencode_v2.go`, and `opencode_library.go`.

The confirmed source defect was that upstream discovery could select `opencode-ai`
V1 and the later settings hook could mark that same executable as V2. Refresh also
built installation plans before applying the selected protocol; installation
finalization bypassed that settings hook entirely.

Startup now reads active settings before discovery. Startup, refresh, installation
revalidation, verification and reconciliation share protocol-aware discovery:
`opencode-ai` for V1, `@opencode/cli` for V2, then PATH, with a selected-major
version probe before admission. Saved path > version-specific environment > generic
environment precedence remains; invalid explicit overrides do not fall back.
All changes still use the restart-only active settings snapshot.

Installation plans permit the fixed V1 recipe only when V1 is active; V2 plans are
blocked rather than installing V1 under a V2 label. Broken/incompatible explicit
V1 overrides block global installation. Version/source/path metadata is generated
from the same selected executable before plan creation and final publication.

Focused check passed:
`go test -tags dev -run '^Test(OpenCodeDiscoverySelection|OpenCodeInstallPlans|HarnessInventoryEmptyHistory|HarnessLatchedStorageReadOnly|HarnessFinalizationAdmissionBoundary)$' -count=1 .`
in `engine` (5 top-level tests, 8.357s). Fixtures exercise package coexistence,
default V2, override priority/no fallback, major rejection and installation plans;
existing focused inventory/storage/admission tests also passed. No host harness
was executed and no global package was installed. The Go package compiled as part
of this check. New file: `engine/opencode_discovery_test.go`.

Actual installed executable/catalog/runtime acceptance on each OS remains pending.
Inventory availability is not proof of native protocol behavior. The authenticated
loopback bridge and native-session protocol identity checks were not changed;
unmarked native conversations still mean V2. The original model-loading symptom
has not been reproduced or proven fixed.

The installed catalog/runtime was not exercised. V1 uses the isolated Library
profile; V2 is not a port of that profile. Preserve explicit errors for unsupported
Library selections rather than silently ignoring them.

### 2. Four SQL mutation stages delivered

| Feature | SQL entry point |
| --- | --- |
| Graph pause/resume/Stop | `engine/sqlite_graph_controls.go: controlSQLGraphRun` |
| Retry from a graph node | `engine/sqlite_graph_retry.go: retrySQLGraphNode` |
| Message edit/revert | `engine/sqlite_message_edit.go: revertSQLEvent` |
| Permanent session deletion | `engine/sqlite_session_delete.go: deleteSQLSession` |

Blanket storage-upgrade 409 gates were replaced by operation-specific SQL handlers.
Ownership, activity, revision, native-history and uncertain-delivery safeguards
remain. Legacy writers must never call `commitLocked` while SQL is authoritative;
the empty `app.state` inventory is not SQL authority. No whole-database hydration
was added. See combined fixture evidence below, not inferred runtime acceptance.

Useful existing layers: `sqlite_app.go`, `sqlite_commands.go`,
`sqlite_execution_*.go`, `sqlite_graph_*.go`, `sqlite_session_repository.go`,
`sqlite_event_*.go`, and `sqlite_change_bus.go`.

#### Graph controls — approved plan stage 1 delivered (2026-11-01)

`controlGraphRun` now uses `sqlite_graph_controls.go` under SQL authority. Controls
validate owning main conversation/project inside the write transaction, preserve
idempotency and publish conversation invalidation after commit. SQL Stop records
`StopRequested` before cancelling only that graph; settlement moves its activity to
`awaiting_user` rather than normal automatic assessment. Main chat remains independent.

The SQL scheduler's reservation boundary and delivery consumer now honor pause.
Already-reserved workers can finish; paused acknowledgement waits for their drainage.
Resume consumes retained deliveries exactly once. A paused graph stays active and
is not failed merely for having no dispatchable workers. SQL run/conversation/tool
reads expose `pausing`/`paused`; reserved nodes do not appear working while paused.
Finalized runs report their terminal state even when pause flags remain recorded.

Focused command in `engine`:
`go test -tags dev -run '^(TestSQLiteGraph(Controls|PauseDispatch|PauseDelivery|ControlRollback|ChoiceSettlement|HTTPPages)|TestGraphPause.*)$' -count=1 .`
passed (9 top-level tests). The first pass took 8.723s; after including the legacy
Stop settlement rule and its assertion, the final pass took 8.768s. Coverage uses
existing temporary SQLite/handler fixtures and no real harness or server startup:
ownership, repeated controls, post-commit notice, worker drainage/reservation,
delivery retention/exactly-once resume, failed-write rollback, final Stop state,
main-chat independence and existing graph HTTP/Choice/legacy pause regressions.
No new schema or frontend changes were needed for stage 1. Real graph execution
and UI acceptance remain pending. The remaining source stages are delivered below.

#### Stages 2–4 and shared integration (2026-11-01)

- Retry ports the causal checkpoint through SQL transactions, preserves immutable
  captures/authorization, reuses completed-prefix evidence by reference, reconstructs
  Joins and starts fresh native sessions after commit. Readers expose retry reasons,
  terminal runs, previous attempts and bounded reused/terminal evidence. Retry IDs
  remain deterministic and safe for Windows artifact paths/Git branch suffixes.
- Schema 6 adds/backfills `x_retryOperationId` and indexes, including scoped uniqueness
  for nonempty operations. Existing run IDs, payloads/digests and histories stay exact;
  duplicate operation identities abort the transaction rather than choosing silently.
  Upgrades 1–6, repeat opening, layout rejection and a preserved imported random-ID
  receipt have focused fixture coverage. No live database was opened.
- Edit/revert fences admission, stages bounded streamed-content mutations, checks
  exact owning ordinals and revisions, preserves original history/attachments/
  images/sources/attribution and clears the intended queue/native continuation.
  Uncertain receipts stay deduplicated. Instrumented native resets require an exact
  new hidden completed-status archive in the same transaction; generic manifest
  mutation/deletion guards remain. No new native-history schema was needed.
  Runtime retirement is an external effect: late publication failure can leave the
  idle runtime retired while SQL rolls back, without changing its durable history.
- Deletion builds the owned ID closure, blocks active/paused/uncertain work and
  outside workspace references, atomically persists tombstones/anti-retry receipts
  and purges only owned records. Independent spawns/worktrees remain. Protected
  delegation purges require exact tombstoned owner scope; generic deletion stays
  blocked. Fixed automation destinations retain their deleted identity and are
  disabled, not replaced. Cleanup is durable/retryable, including imported legacy
  tombstones without routing projections. Clients reconcile deleted IDs via scoped
  invalidation, authenticated 404 reads and reconnect handling.

Final combined command in `engine`:
`go test -tags dev -run '^(TestSQLite(DeletedSessionPublication|DeletionRequiresTombstone|ProtectedDeletionScope|NativeResetArchive|GraphRetry(CausalCheckpoint|LoopsAndIndependentBranches|ReceiptOwnershipAndRace|Rollback|ReusedEvidencePages|HTTPAdmission|SchemaBackfill|SchemaLayout)|Message(Edit|Revert).*|SessionDeletion.*|Graph(Controls|PauseDispatch|PauseDelivery|ControlRollback|ChoiceSettlement|HTTPPages))|TestPublication(SchemaChain|RejectStandalone)|TestGraphPause.*)$' -count=1 .`
passed (39 selected top-level test functions, 79.772s). First attempt compiled but
failed three new fixture cases: missing staging directory, non-metadata Session
used in a metadata write, and reused decode target retaining an omitted boolean.
Fixtures were corrected and the same selected gate passed. Failure-injection
storage diagnostics in test output are intentional, not a live storage incident.
`npx tsc --noEmit` in `clients/desktop` also passed after client deletion changes.
No full suite, review agents, server startup, real provider/harness, installer,
desktop launch or migration of the user's data was performed.

### 3. Other compatibility/acceptance gates

- Legacy `Session.SelectedAgent` records now migrate to detached snapshot bindings
  before runtime admission, retaining the original snapshots. No current source
  UID is inferred from a slug/revision. See the focused evidence below; actual
  user-data migration and subsequent native turns still require acceptance.
- SQL graph question-context extraction is now source-complete; see the focused
  evidence below. Graph import/export with source UIDs and manual previous-attempt/
  terminal behavior still require acceptance (their SQL source/projections have
  the focused retry fixture coverage above).
- Check attachments across duplicate admission, storage uncertainty, steering,
  queue execution, restart, protected-draft reload, image previews and all harnesses.
  SQL admission changes live in `api.go` and `sqlite_execution_queue.go`; ownership
  lookup is in `chat_files.go`. No live upload/harness run was performed.
- The original metrics dashboard remains in the original worktree. Upstream
  analytics does not imply its implementation or observations have been migrated.
- Flutter/Dart are unavailable here. Mobile dependencies/channels were merged but
  not compiled. Installers, pairing, remote engines and UI behavior await acceptance.

### 4. Remaining source compatibility slices (2026-11-01)

#### Legacy selected-agent bindings — delivered

An implementation subagent added `engine/sqlite_legacy_agents.go` and
`engine/sqlite_legacy_agents_test.go`, with shared startup wiring in
`engine/sqlite_store.go` and binding/execution changes in `engine/agent_types.go`,
`engine/agent_resolution.go` and `engine/chat_agent.go`.

After existing authority/import verification, startup converts old selections
without bindings into deterministic, explicitly detached `legacy_snapshot`
bindings. It preserves the original selected-agent record/prompt, current session
harness/model/effort and independent Library selection, without following current
TOMLs by slug or inventing a proven authoring-source relationship. Existing modern
bindings, native records and history remain untouched. SQL prompt execution uses
the prepared binding, not a second legacy lookup; normal harness/model/Library
validation still applies. Legacy non-SQL prompt behavior remains unchanged.

The paged migration and `store_meta.legacy_agent_binding_version=1` marker commit
atomically. Invalid legacy identity/configuration or write failure aborts the
migration; failed writes leave no marker/partial binding and can be retried after
repair. Both existing SQL stores and verified JSON cutover use this startup hook.
No schema bump, authoring-file mutation or automatic source-following was added.

Focused command in `engine`:
`go test -tags dev -run '^(TestSQLiteLegacyAgent(Bindings|BindingRollback|BindingImport)|TestSelectedChatAgentPrompt)$' -count=1 .`
passed: four top-level tests, 6.393s. Earlier attempts compiled but exposed fixture
errors (non-metadata Session collections, null native map, null folder lists);
fixtures were corrected before the passing run. Coverage includes unchanged modern
bindings/native/history digests, exact prompt/settings preservation, replacement
source isolation, repeated startup, rollback/retry, immutable JSON backup/reopen
and unchanged legacy prompt behavior. All data belonged to temporary fixtures.

Original snapshots are retained, but there is no automatic reverse migration.
Older binaries do not understand detached bindings and can block those sessions
despite the unchanged schema version. Rollback requires an intentionally restored
compatible binary/data backup set. Actual upgrade, native turns, overrides and
cross-client UI acceptance remain separately authorized human checks.

#### SQLite graph question context — delivered

An implementation subagent changed `engine/questions.go` and
`engine/sqlite_execution_requests.go`, with regression fixtures in
`engine/sqlite_question_context_test.go`. SQL question admission now captures
visible assistant text from the node's current active activation inside the same
transaction as the pending question. It preserves the legacy 128 KiB selection
budget and chronological presentation, using paged event references and streamed
text-field reads rather than hydrating history/native payloads. No schema changed.

The snapshot remains stable after later messages and duplicate native requests.
Question descriptions, private native identity, secret-answer history, owning-chat
reply routing and failed-delivery recovery remain intact. Graph projections expose
the snapshot; private node conversation/history/pending URLs remain 404. Missing or
corrupt context aborts admission without a partially persisted question.

Focused command in `engine`:
`go test -tags dev -run '^(TestSQLiteGraphQuestionContext|TestSQLiteGraphQuestionContextBounds|TestSQLiteGraphQuestionContextReadFailure|TestSQLiteOwnedRequestsAndGrantRollback|TestSQLiteGraphHTTPPages)$' -count=1 .`
passed: five top-level tests, 13.230s. Initial run failed three fixture assumptions
(private-node HTTP expectation, staged-generation encoding, corruption-error
expectation); these were corrected before the passing rerun. Fixtures include
Unicode, published/legacy text storage, byte limits, more than one metadata page,
activation/owner isolation, stable snapshots, rollback and reply routing.

No application engine, real harness, existing profile or real data was opened.
Live native requests and cross-client graph question/reader UI acceptance remain
pending separate authorization. Source checks are not rollout acceptance.

#### Final source-only preservation check

Subsequent final build requested by the user: `go build -tags dev -o <output> .`
passed in `engine`, producing the 33,007,616-byte executable
`C:\Users\ronic\AppData\Local\Temp\opencode\klm-upstream-0.6.7-dev-9ce43ec18cae45dcb3be918e40b9a03a.exe`.
The binary was not executed. This is a dev engine build, not a release installer
or a new desktop/mobile build. Runtime/data migration acceptance remains pending.

Both implementation subagents completed their selected gates; no combined/full
suite or duplicate runtime check was added. The parent ran targeted
`git --no-optional-locks diff --check` on the touched tracked paths successfully.
The unmerged-path list remains empty. HEAD, MERGE_HEAD and branch match the exact
Git state above. The merge index SHA-256 is unchanged from session entry:
`0658FE777FF223509AAB586D03FCC5C2FF427C2A3AC52242539E7E3615D8248E`.
The four existing `sqlite_graph_controls.go`, `sqlite_graph_retry.go`,
`sqlite_message_edit.go` and `sqlite_session_delete.go` files also retain their
entry SHA-256 hashes. New changes remain unstaged/untracked; no commit/push,
engine startup, real profile/data migration or original-worktree edit occurred.

## Verification evidence

### User-authorized local installation and source publication

After reporting the isolated client experience satisfactory, the user explicitly
authorized installing desktop and engine and pushing the source to the repository.
This supersedes the earlier no-install/no-commit/no-push constraint for this delivery;
it does not authorize changing the separate metrics worktree or force-pushing.
Publication targets `origin` (`https://github.com/Ronicley/klm.git`), current branch
`update/upstream-0.6.7-20261101`, not `develop` or `main`. No remote target branch
existed at preflight. The installed versions at preflight were desktop/engine
0.3.4; the release engine reported zero running sessions and zero active graphs.

The packaging subagent successfully ran
`distribution/windows/build-release.ps1 -Version 0.6.7` once, clearing child build
`VITE_*`/`KLM*` environment overrides and setting `NODE_ENV=production`. The existing
script built desktop then engine. No existing packaging outputs were overwritten.
The parent independently confirmed the installer hashes in `dist/release`:

| Artifact | Bytes | SHA-256 |
| --- | ---: | --- |
| `KLM-Desktop-0.6.7-x64-setup.exe` | 7,290,615 | `5ec44d5a92cbe090f72aa862cccb2d4a6623f5d5bb7850b0253a3bad6c77352a` |
| `KLM-Engine-0.6.7-x64-setup.exe` | 15,636,566 | `b1838237465079e6e5c00fcff5f94f28b34f7d5211baaa0653b5e8a8a556f16b` |

`dist/release/SHA256SUMS.txt` records both. Generated release configuration is
0.6.7; tracked source manifests remain unchanged by packaging (base 0.6.0).
Vite's >500 kB chunk warning remains; installers are unsigned. Build artifacts,
backups and credentials must not enter the source commit. Backup/install/push
outcomes are recorded separately below; packaging success alone is not installation.

#### Scope changed to clean installation; no historical conversion

While the backup stage was running, the user explicitly said sessions did not need
to be saved or converted. The remaining native-history/SQLite backup stage was
instructed to stop; no complete verified native backup is claimed. Already-created
partial files remain at
`C:\Users\ronic\AppData\Roaming\klm\backups\pre-upstream-0.6.7-2026-11-01T19-32-46-120Z-86709169-f940-4376-987f-cbc6a7391529`.
Its recovery directory contains the pre-publication index and staged/unstaged
patches. Do not treat this partial backup as a completed all-history recovery set.

The old release engine was confirmed stopped. Instead of deleting or converting
its data, the complete original directory was moved without content conversion to
`C:\Users\ronic\AppData\Roaming\klm\engine-before-0.6.7-630d5c448fab4bf481339e995263160b`.
A new empty `%APPDATA%\klm\engine` directory was created. Old projects, settings,
selections and histories are not loaded in this fresh profile. No original-session
or native-history conversion was run, and the separate source metrics worktree
was not changed. No old profile/partial backup was deleted.

#### Installed local release and minimal runtime evidence

Both 0.6.7 installers were executed. Desktop silent setup returned exit code 0;
both HKCU uninstall records report 0.6.7. The installed engine SHA-256 matches
`dist/engine/klm.exe`; installed desktop product version is 0.6.7. Both installed
programs were launched and left running (observed engine PID 23132, desktop PID
30976). Windows login startup still points at the installed engine launcher.

Minimal checks: release API listens on port 7331, bundled frontend on 7332;
anonymous `/api/version` returns 401 and frontend `/` returns HTML with 200.
Read-only SQLite `quick_check` returned `ok`, schema version is 6, and counts were
zero projects, zero normal sessions and zero agent bindings. This establishes a
fresh-profile startup, not successful migration or native task execution.

The release profile requires its own client pairing; pairing the earlier dev
profile does not authorize this engine. Native tasks, legacy upgrades, broad
functional/client checks and full suites remain unvalidated. Source publication
is authorized on the current update branch; no installer/backup/credential files
belong in that commit, and no push to `main`/`develop` is authorized by this plan.

### Subsequent isolated engine launch (user-authorized)

After the source-only checks/build, the user explicitly requested execution of
the dev executable above. `start` succeeded and reported readiness at
`http://localhost:17331`; the listener was confirmed as `127.0.0.1:17331`, PID 34448.
The port was checked as free first; no existing engine was stopped. The process
was left running as requested, using this newly created empty `KLM_DEV_DATA_DIR`:
`C:\Users\ronic\AppData\Local\Temp\opencode\klm-upstream-0.6.7-runtime-2a490bccb3264285a96ad17eb4a2a5f5`.

Launch set `KLM_API_BIND_HOST=127.0.0.1` and removed the child launch environment's
`KLM_DEV_DISABLE_CLIENT_AUTH`; client authentication remains enabled. No existing
profile was copied/opened, no real data was migrated/backed up, and no model task,
desktop/UI, pairing or compatibility workflow was exercised. This proves only
fresh isolated engine startup/readiness, not acceptance of the migration/features.
Real-profile validation still requires separate authorization and verified backup.
For a later authorized stop, use the same dev binary and this exact
`KLM_DEV_DATA_DIR` with `stop`, never the default dev/release profile.

After the user reached the authenticated API instead of the UI, the worktree's
Vite client was started on `http://127.0.0.1:17332` using
`VITE_ENGINE_URL=http://127.0.0.1:17331` and
`npm run dev -- --host 127.0.0.1 --port 17332 --strictPort`. A GET returned its HTML.
A one-use five-minute pairing code was generated through the isolated profile's
local `pair` command and provided only to the user, not stored here. Authentication
was not disabled. Pairing completion and UI/feature acceptance were not verified.

Successful checks during reconciliation:

- Go dev build, output under `%LOCALAPPDATA%\Temp\opencode`, outside the repository.
- `npx tsc --noEmit` in `clients/desktop`.
- `go test -tags dev -run '^TestPublicationSchemaChain$' -count=1 .` in `engine`:
  schemas 1–5, repeat opening and preservation of a stored sentinel.
- `go test -tags dev -run '^TestMergeRouteRegistration$' -count=1 .` in `engine`:
  SQL and legacy route construction without starting a server.
- `npx vitest run src/features/chat/attachmentAcceptance.test.ts`: one test.
- `npx vite build`, required to generate `dist` for Tauri's embedded assets.
  Large-chunk warnings remain; no unrelated bundle optimization was attempted.
- `cargo check --locked` in `clients/desktop/src-tauri`, after asset generation.
- Empty unmerged-path list and successful `git diff --cached --check`.

Initial attempts failed on merge/type/baseline declarations and missing SQL root
mapping; those were corrected. Native compilation first timed out while building
dependencies, then reported missing `dist`; the Vite build satisfied that
prerequisite and the final Cargo check passed. The integration document records
the checks in more detail. Do not claim initial failures never happened.

No full suite, adversarial review, review agent, live model/provider execution,
desktop launch, Flutter build or real engine-data migration was performed.
Dependencies were installed with `npm ci --ignore-scripts --no-audit --no-fund`.
Generated `node_modules`, `dist` and Rust build artifacts are not source deliverables.

## Safe continuation and stop condition

All four requested mutation stages are source-complete. The subsequent request
authorized only the two remaining source slices: agent migration and SQL graph
question context; both are now delivered with the focused evidence above. Stop
source work here. Do not expand into runtime acceptance or unrelated integration.
Runtime acceptance must use separately authorized isolated data/ports. Keep
compatibility and rollback explicit; use focused checks, not an automatic full suite.
Do not run full suites or delegate to agents without applicable authorization.

Do not launch this engine against the existing dev profile merely because it is
called “dev”: the profile can contain real user data and startup can migrate it.
Runtime checks need explicitly isolated data/ports and separate authorization.
For a later authorized dev check, `KLM_DEV_DATA_DIR` must name an explicit absolute
isolated directory; it does not isolate native harness accounts/configuration or
change fixed ports 17331/17332. Confirm port availability and native-profile
isolation separately, without stopping another user's instance implicitly. A
pre-upgrade backup must include compatible native transcripts/workspaces and other
referenced artifacts, not just `engine.sqlite3` or the legacy JSON backups.
Before rollout, retain the pre-upgrade binary and verified data backup together;
code rollback does not reverse schema-5/6 migrations or accepted deletion. Older
schema-5 binaries refuse schema 6; restore a compatible backup/binary set intentionally.
Never delete legacy backups.

Stop after the selected slice works and its focused gate passes. Update this
handoff/integration record with actual outcomes, not inferred runtime success.
Keep the merge uncommitted and the original worktree untouched unless the user
explicitly authorizes a different delivery action.
