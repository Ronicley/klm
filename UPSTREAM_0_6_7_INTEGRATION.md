# Upstream 0.6.7 integration

## Status

The 37 textual merge conflicts have been reconciled. Source compilation is not
functional acceptance: **the full fork integration is not ready for deployment**.
The merge remains uncommitted. Do not start this engine against existing data or
publish an installer until remaining compatibility and runtime acceptance is complete.
All four requested SQL mutation adaptations are now source-complete; fixture and
compilation evidence does not establish overall product acceptance. The two later
source slices, legacy agent bindings and SQL graph question context, are also
delivered below; actual migration/native/client acceptance remains pending.
A later user-authorized clean installation of desktop/engine 0.6.7 succeeded
locally; the existing historical profile was preserved, not converted.

## Isolation and sources

- Worktree: `C:\Projetos\Projetos\pessoal\klm-upstream-0.6.7`.
- Branch: `update/upstream-0.6.7-20261101`.
- Fork baseline: `b4e0312` (`develop`).
- Incoming source: `upstream/main`, `d1857b06df6c58284b8304f22bf05103fb47b392`
  (`samuelhk0595/klm`, tag `v0.6.7`).
- Merge command: `git merge --no-commit --no-ff upstream/main`.
- The original `klm` worktree and its uncommitted session metrics implementation
  remain untouched. Those uncommitted changes are not included in this worktree.
- No commit, push, engine restart, installation, or user-data migration was run.

## Reconciliation delivered

This is not a safe blanket “take upstream” merge. The incoming SQLite storage,
authenticated contract-3 HTTP/SSE interfaces, paginated history, global harness
installation, and reusable agent bindings intersect with fork-only behavior.

- Upstream SQLite startup, authentication, scoped updates, paginated history,
  analytics, protected drafts, engine switching, and reusable-agent contracts were
  retained. The duplicate PATCH session-agent route was reconciled.
- Fork OpenCode V1/V2 fields and selection endpoints remain. Settings now have an
  SQL reader/writer and startup/inventory-refresh hooks. Native SQL identities
  retain their protocol marker. V1 catalog discovery uses the upstream isolated
  Library profile; V2 retains its own catalog/projection paths. Actual versions,
  installers and native execution still require acceptance.
- An additive, transaction-scoped schema-5 upgrade preserves imported fork roots:
  OpenCode settings, speech key, deletion receipts and image metadata. Version 4
  databases receive empty new domains, without inferred history or replay.
  Schema-5 layout checks and downgrade rejection remain explicit. New databases
  and existing schemas 1–4 are covered by the focused schema-chain check below.
- SQL message admission no longer reads the empty legacy session inventory first.
  Multipart attachment context, image dispatch payloads, fingerprints, queue/user
  metadata, ownership checks and upload retention were connected to SQL admission.
  Duplicate uploads are discarded; uncertain storage failures retain their bytes.
  Legacy uploaded-image-ID submissions fail explicitly and require reselection.
  Preview requests use authenticated client transport.
- OpenRouter/OpenAI transcription selection and speech-key fallback have SQL paths.
- Composer attachment selection, queued attachment display and recovery metadata
  were combined with protected native drafts. File bytes remain in memory only;
  persisted metadata becomes a reselection requirement after reload. Delivery
  acceptance no longer clears a newer draft with different attachments.
- Native URL/download validation, notification commands and protected credentials/
  drafts commands were combined. Both Flutter file/download channels and their
  dependencies remain. Graph dynamic handles, fork attention/measurement behavior,
  paginated activity and transfer types were reconciled.
- Fork graph layout and deliberate removal of VS Code configuration were retained.

## Remaining integration gates

- Graph Pause/Resume/Stop, node retry, message edit/revert and permanent session
  deletion now have SQL adapters. Blanket upgrade 409s are removed; owner/activity/
  revision/native/uncertainty protections remain. Their real runtime/UI acceptance
  is pending; combined source/fixture evidence appears below.
- Legacy selected-agent snapshots now migrate to detached snapshot bindings before
  runtime, without guessing current authoring-source identity. Originals and modern
  bindings remain intact. Focused evidence is below; real migration and native/client
  acceptance remain separate gates.
- Graph question context now has a transactional SQL capture with bounded text
  reads; focused fixture evidence appears below. Live question/reader UI behavior,
  previous-attempt/terminal graph projections, graph transfers with reusable-agent
  identities, and saved draft recovery still need runtime acceptance.
- OpenCode discovery now selects the active protocol's npm package/override before
  probing its major version. Startup, refresh and installer verification share this
  path; V2 installation plans cannot install the fixed V1 recipe. Source regression
  evidence is below; real installed catalog/runtime acceptance on each OS remains
  pending. Unmarked native IDs still mean V2.
- The original worktree's uncommitted metrics dashboard was deliberately not
  imported. Do not claim its data or UI was migrated to upstream analytics.

## Verification and rollout gate

- Go dev build succeeded during reconciliation. Later source changes compiled as
  part of the targeted Go checks; no application engine process was started.
- `npx tsc --noEmit` succeeded after frontend reconciliation.
- `go test -tags dev -run '^TestPublicationSchemaChain$' -count=1 .` passed,
  including versions 1–5 and preservation of the existing sentinel value.
- `go test -tags dev -run '^TestMergeRouteRegistration$' -count=1 .` passed for
  SQL and legacy route registration, without starting an HTTP server.
- `npx vitest run src/features/chat/attachmentAcceptance.test.ts` passed (one test).
- Initial compiler attempts exposed shared-validator/type conflicts and missing
  baseline graph/attachment declarations; those compilation errors were fixed.
- `npx vite build` succeeded, producing the bundled assets required by native
  compilation. Vite reported large-chunk warnings; no bundle optimization was added.
- `cargo check --locked` succeeded after asset generation. Its initial cold-cache
  attempt timed out; the next attempt exposed the missing `dist` prerequisite,
  which was satisfied by the Vite build. No desktop application was launched.
- Flutter/Dart are not installed; the mobile dependency/bridge merge was not built.
- Do not run full suites or launch review agents without explicit authorization.
- Do not start an engine to validate compilation: startup can migrate real data.
- Before manual runtime validation, use an explicit isolated data/port profile and
  follow upstream's SQLite backup/rollback documentation. Keep the old engine
  binary and pre-migration data backup together; do not assume an older engine can
  open the migrated store. Starting or migrating the user's current instance is a
  separate action, not implied by this source integration.
- Leave the merge uncommitted until the user requests a commit.

The follow-up request on 2026-11-01 authorizes source implementation of the two
remaining compatibility items (legacy selected-agent bindings and SQLite graph
question context), with one implementation subagent per item and focused fixture
checks. It does not authorize redoing the four SQL mutations, modifying the merge
index, running an engine, or migrating real data. Actual validation remains a
separate authorized step with an isolated profile/ports and verified backup.

### OpenCode compatibility follow-up (2026-11-01)

The user authorized the bounded discovery/installation source slice after the
handoff. Previously, the settings hook could relabel an upstream-discovered V1
binary as V2 without replacing it; refresh built plans before that hook and final
installation publication bypassed it. The hook was replaced by shared selection
inside discovery, using the restart-only active settings. Selected package,
override, version, inventory and installation plan now agree before publication.
V1 automatic installation remains pinned; V2 installation remains manual.

`go test -tags dev -run '^Test(OpenCodeDiscoverySelection|OpenCodeInstallPlans|HarnessInventoryEmptyHistory|HarnessLatchedStorageReadOnly|HarnessFinalizationAdmissionBoundary)$' -count=1 .`
passed in `engine` (8.357s). The two new tests cover coexistence, default V2,
override priority/no fallback, wrong-major rejection and protocol-aware plans;
three existing inventory/storage/admission tests passed in the same run.
No full suite, installed harness execution, engine startup, global installation or
real-data migration was performed. The original model-discovery symptom remains
unreproduced. Follow-up edits are unstaged; the prior merge index was preserved.

### SQL graph controls — stage 1 (2026-11-01)

The user approved sequential adaptation of the four gated operations. Stage 1
replaces the blanket graph-control 409 with a transaction-scoped ownership check
and SQL writes. Pause stops future reservations/delivery consumption without killing
reserved workers. Paused acknowledgement waits for drainage; resume consumes retained
deliveries once. Stop records user intent before graph-only cancellation and settles
the activity as `awaiting_user`. Controls, pause acknowledgement and completions
invalidate the owning conversation after commit. SQL readers share the pause status
projection and do not show reserved nodes as working while paused.

`go test -tags dev -run '^(TestSQLiteGraph(Controls|PauseDispatch|PauseDelivery|ControlRollback|ChoiceSettlement|HTTPPages)|TestGraphPause.*)$' -count=1 .`
passed in `engine`: 9 top-level tests, final run 8.768s. An earlier pass was 8.723s;
the final run added the stopped-activity settlement rule/assertion. Tests use
temporary SQLite databases and handlers; no application server or real harness
was launched. Source compiled in the same check. No schema, TypeScript or client
changes were needed for stage 1. Later stages are delivered below; manual graph
execution/UI acceptance and overall rollout are not established.

### Remaining SQL stages implemented with subagents (2026-11-01)

Three implementation subagents delivered node retry, edit/revert and deletion;
the parent integrated shared persistence/native/deletion guards and consumer fixes.
No adversarial review or review agents were launched.

- Retry uses a transactional causal checkpoint, preserved snapshots/authorization,
  fresh native histories, Join correlation and reference-based retained evidence.
  Projections/readers expose retry reasons, terminal runs and paginated previous
  attempt/terminal evidence. Deterministic IDs use path-safe 32-character hex.
- Additive publication schema 6 backfills/indexes `graph_runs.retryOperationId` in
  bounded metadata pages. It retains imported random IDs, payloads/digests and
  histories. Scoped uniqueness rejects conflicting imported operations atomically.
  Fresh databases and upgrade/reopen paths use 6; old binaries cannot downgrade it.
- Edit/revert stages exact-ordinal content generations and publishes against the
  owning session revision. Original text/data/history, files/images/sources and
  agent attribution remain. Queue cleanup retains uncertain delivery receipts.
  A hidden new completed-status event archives native identity before an explicit
  instrumented reset; ordinary manifest mutation/deletion stays forbidden. A late
  publication failure can retire an idle runtime while SQL history rolls back.
- Deletion atomically tombstones the owned session/graph/native/delegation closure,
  preserves outside sessions/workspaces, prevents operation resurrection and purges
  only explicitly authorized immutable records. Removed owners still invalidate
  their project/conversation. Cleanup retries/startup recover managed files, including
  unprojected imported tombstones; backups/project files are untouched. Reusable
  automations are disabled while retaining the deleted destination identity.
  Client 404/reconnect reconciliation removes stale sessions without global history.

Final focused gate (engine):
`go test -tags dev -run '^(TestSQLite(DeletedSessionPublication|DeletionRequiresTombstone|ProtectedDeletionScope|NativeResetArchive|GraphRetry(CausalCheckpoint|LoopsAndIndependentBranches|ReceiptOwnershipAndRace|Rollback|ReusedEvidencePages|HTTPAdmission|SchemaBackfill|SchemaLayout)|Message(Edit|Revert).*|SessionDeletion.*|Graph(Controls|PauseDispatch|PauseDelivery|ControlRollback|ChoiceSettlement|HTTPPages))|TestPublication(SchemaChain|RejectStandalone)|TestGraphPause.*)$' -count=1 .`
passed, 39 selected top-level test functions, 79.772s. Initial attempt compiled but
failed three fixture cases (staging setup, metadata-only Session shape, omitted-field
decode into a reused target); fixtures were fixed before the successful rerun.
Intentional failure-injection diagnostics are not live storage failures.
`npx tsc --noEmit` passed in `clients/desktop` after deletion consumer changes.

Schema 1–6 chain/reopen/sentinel preservation, schema-6 layout rejection and old
random-ID retry backfill are covered by that selected gate. No full suite, actual
engine startup, real harness/provider execution, installer/desktop launch or live
data migration was run. Keep compatible binary/data backups before any later rollout;
code rollback cannot undo schema 6 or accepted permanent deletion. Other compatibility
items listed above and human runtime acceptance remain outside these four stages.

### Legacy selected-agent binding follow-up (2026-11-01)

An implementation subagent added `sqlite_legacy_agents.go` and its focused fixture
tests, wired shared verified startup in `sqlite_store.go`, and adapted
`agent_types.go`, `agent_resolution.go` and `chat_agent.go`. Legacy selections
without bindings become deterministic detached `legacy_snapshot` bindings. Current
TOML slugs/revisions never prove source lifetime; even a matching source UID cannot
replace this saved snapshot. Original prompt, current harness/model/effort and
independent Library selection remain; modern bindings/history/native records are
untouched. Normal model/harness/Library checks still apply, without a duplicate SQL
legacy-prompt lookup. Non-SQL prompt behavior is unchanged.

Migration uses bounded session-ID pages and atomically commits bindings plus
`store_meta.legacy_agent_binding_version=1`; invalid snapshots/write failures
abort without partial conversion or a completion marker. Existing SQL and verified
JSON-import startup share the hook. No schema bump, authoring-file rewrite or
automatic source-following/reverse migration was added. Older binaries can block
these detached sessions even with the same schema version; intentional rollback
requires compatible binary/data backups, not just replacing the executable.

`go test -tags dev -run '^(TestSQLiteLegacyAgent(Bindings|BindingRollback|BindingImport)|TestSelectedChatAgentPrompt)$' -count=1 .`
passed in `engine`: four top-level tests, 6.393s. Earlier runs compiled but exposed
fixture shape errors (non-metadata Session collections, null native map and null
folder lists); fixtures were fixed before passing. Tests cover exact preservation,
modern binding/native/history digests, replacement-identity isolation, repeated
startup, rollback/retry, verified JSON cutover with immutable backup/reopen and
legacy prompt behavior. Only temporary fixture data was used. Actual upgrade,
native turns, override editing and cross-client UI remain human acceptance gates.

### SQLite graph question context follow-up (2026-11-01)

An implementation subagent added activation-scoped assistant-text capture to SQL
question admission (`questions.go`, `sqlite_execution_requests.go`), with fixtures
in `sqlite_question_context_test.go`. Capture and question persistence share one
transaction. Paged metadata and streamed text fields retain the legacy 128 KiB
budget/chronological presentation without whole-history/native-data hydration.
Missing/corrupt context fails admission atomically. Existing descriptions, secret
answers, private native identity, owner projections and native reply routing remain;
private node conversation/history/pending reads still return 404. No schema changed.

`go test -tags dev -run '^(TestSQLiteGraphQuestionContext|TestSQLiteGraphQuestionContextBounds|TestSQLiteGraphQuestionContextReadFailure|TestSQLiteOwnedRequestsAndGrantRollback|TestSQLiteGraphHTTPPages)$' -count=1 .`
passed in `engine`: five top-level tests, 13.230s. Initial fixture assumptions about
private-node HTTP visibility, generation encoding and corruption errors were fixed
before the passing rerun. No application engine, harness or existing data/profile
was used. Live native requests and cross-client question/reader UI acceptance remain
separate human gates; focused fixtures do not establish rollout acceptance.

Final source-only check: targeted tracked-path `git --no-optional-locks diff --check`
passed, unmerged paths remain empty, and HEAD/MERGE_HEAD/branch are unchanged.
The merge index retains entry SHA-256
`0658FE777FF223509AAB586D03FCC5C2FF427C2A3AC52242539E7E3615D8248E`.
All four previously delivered SQL adapter files retain their entry hashes. The two
compatibility slices and documentation remain unstaged/untracked. No full suite,
application engine, real migration, commit/push or original-worktree edit was run.

The user subsequently requested the final build. `go build -tags dev -o <output> .`
passed in `engine`, writing 33,007,616 bytes to
`C:\Users\ronic\AppData\Local\Temp\opencode\klm-upstream-0.6.7-dev-9ce43ec18cae45dcb3be918e40b9a03a.exe`.
The executable was not started. No release installer or new desktop/mobile build
was produced; actual runtime/data acceptance remains separately authorized.

### Subsequent isolated engine launch (user-authorized)

The user then explicitly requested execution. The dev binary's `start` completed
with readiness at `http://localhost:17331`; the listener was confirmed on
`127.0.0.1:17331` (PID 34448). Port availability was checked first; no existing
engine was stopped. It was left running with a newly created empty profile:
`C:\Users\ronic\AppData\Local\Temp\opencode\klm-upstream-0.6.7-runtime-2a490bccb3264285a96ad17eb4a2a5f5`.
Launch explicitly set `KLM_DEV_DATA_DIR` to that directory and
`KLM_API_BIND_HOST=127.0.0.1`, with the auth-disable flag removed. No existing
profile/data was copied, opened, migrated or backed up; no model, UI, pairing or
feature acceptance was exercised. This establishes fresh isolated startup only.
Real-profile checks still require separate authorization and verified backup.

The worktree's Vite UI was subsequently started on `http://127.0.0.1:17332`, with
`VITE_ENGINE_URL=http://127.0.0.1:17331` and explicit loopback/strict port arguments;
one GET returned frontend HTML. The isolated engine's local `pair` command issued
a one-use five-minute code for the user. No code/credential is recorded here;
authentication remains enabled. Pairing completion and UI behavior remain unverified.

The resolved merge was staged; `git diff --name-only --diff-filter=U` was empty.
`git diff --cached --check` passed after removing one incoming EOF blank line.
Resolving/staging paths does not create a merge commit or advance this branch's HEAD.

### User-authorized installation/publication and packaging

After the isolated client check, the user explicitly authorized desktop+engine
installation and source commit/push. Target is the current
`update/upstream-0.6.7-20261101` branch at `origin` (`Ronicley/klm`), not `main` or
`develop`; this does not authorize touching the metrics worktree or force-pushing.
An implementation subagent ran the existing combined Windows release build once
with version 0.6.7 and no inherited dev endpoint/profile overrides. Desktop and
engine packaging succeeded, and the parent independently verified these hashes:

| Installer in `dist/release` | Bytes | SHA-256 |
| --- | ---: | --- |
| `KLM-Desktop-0.6.7-x64-setup.exe` | 7,290,615 | `5ec44d5a92cbe090f72aa862cccb2d4a6623f5d5bb7850b0253a3bad6c77352a` |
| `KLM-Engine-0.6.7-x64-setup.exe` | 15,636,566 | `b1838237465079e6e5c00fcff5f94f28b34f7d5211baaa0653b5e8a8a556f16b` |

The generated release override is 0.6.7; tracked source manifests retain base 0.6.0.
Vite reported the existing large-chunk warning; installers remain unsigned.
Packages/backups/credentials are not source deliverables. Backup, installation and
publication results are separate from build success.

The user then removed session backup/conversion from scope. The remaining native
backup stage was instructed to stop; its partial output under
`%APPDATA%\klm\backups\pre-upstream-0.6.7-2026-11-01T19-32-46-120Z-86709169-f940-4376-987f-cbc6a7391529`
is not claimed as a complete verified all-history backup. The stopped original
engine data directory was preserved without conversion by renaming it to
`%APPDATA%\klm\engine-before-0.6.7-630d5c448fab4bf481339e995263160b`.
A new empty `%APPDATA%\klm\engine` profile was used; no old projects/settings/
histories were imported or deleted, and the metrics source worktree was untouched.

Both installers completed; desktop silent setup returned 0, and both HKCU install
records report 0.6.7. Installed engine bytes match `dist/engine/klm.exe`; installed
desktop product version is 0.6.7. Both programs were launched and left running
(observed PIDs 23132/30976). Release API/frontend listeners use 7331/7332; anonymous
API version access returns 401, while frontend root serves HTML with 200. Read-only
SQLite `quick_check` returned `ok`, schema 6, with zero projects, normal sessions
and agent bindings. Login startup still uses the installed engine launcher.

This is clean local startup evidence only. The release client needs its own
pairing; dev-profile pairing does not transfer. Legacy-data upgrade, native model
tasks, broad UI/client acceptance and full suites remain unverified. The authorized
source delivery remains on the update branch, not `main`/`develop`; installers,
backups and credentials stay outside Git.
