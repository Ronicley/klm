# Upstream 0.6.8 reconciliation

## Sources and status

- Branch: `update/upstream-0.6.8-20261111`.
- Fork baseline: `7b54fca` (`origin/main`).
- Incoming: `096d3ae39c9775bb15172fcc47b9303b29785368`
  (`upstream/main`, tag `v0.6.8`).
- Merge prepared with `git merge --no-commit --no-ff upstream/main`.
- All six conflicted files are reconciled. No commit or push was performed.
- Source reconciliation did not start an engine or modify an existing user profile.
  The subsequent user-authorized installation is recorded below.

## Reconciliation

- Included upstream GitHub automation event/action selection, automatic 25-item
  pagination and compact subagent activity rows.
- Kept the fork dashboard navigation and session deletion alongside automatic
  sidebar pagination and complete-list empty-state handling.
- Kept message revert actions alongside native/owned subagent navigation.
- Graph activity combines automatic activation/event pagination with previous
  attempts, retry controls, captured output and the plan reader. Attempt changes
  reset pagination; reads and delegation links use the inspected run identity.
- SQL graph activity retains ownership checks and reference-based reused evidence,
  with the requested event limit and offset metadata required by the new client.
- Fork OpenCode V1/V2 selection, uploaded attachments, transcription, request alerts,
  graph controls and existing SQLite compatibility code remain in the source tree.
- No dependency, persistence schema or version-manifest changes were introduced.
  Existing automation records retain the upstream default `pull_request/opened`.

## Lightweight checks

- `npx tsc --noEmit` passed in `clients/desktop`.
- The following targeted check passed in `engine` (5.476s):

  ```powershell
  go test -tags dev -run '^(TestSQLiteGraphRetryReusedEvidencePages|TestSQLiteGraphHTTPPages|TestAutomationWebhookRouting|TestAutomationWebhookConfiguration|TestAutomationWebhookPendingTriggerEdit|TestAutomationWebhookRunReferences)$' -count=1 .
  ```

- The reused-evidence fixture now checks 25-item pages and exact offset metadata,
  while retaining output, previous-attempt and complete-history assertions.
- No full suites, review agents, native builds or runtime/UI checks were run.

## Manual acceptance and recovery

Validate scroll pagination/reading anchors, sidebars and pickers, subagent navigation,
graph previous/current attempts and plan reading, background request alerts, message
revert/deletion, and GitHub automation selection/legacy defaults before deployment.
Earlier fork runtime limitations remain; compilation does not establish acceptance.

Source rollback before commit is `git merge --abort`, provided no additional work
has been added during the merge. The baseline commit remains available. This merge
does not authorize upgrading a running installation or opening/migrating real data.

## Subsequent user-authorized local installation (2026-11-11)

The user requested local installation. Both Windows release packages were built
with `distribution/windows/build-release.ps1 -Version 0.6.10`, retaining source
manifest versions. The local package version advances the installed 0.6.9; it does
not rename the incoming upstream v0.6.8 tag. Build succeeded with the existing Vite
large-chunk warnings. No commit, push or release publication was performed.

The installed engine was stopped through its CLI, and only the installed desktop
process was closed. Installed binaries, the stopped engine profile and desktop
roaming drafts were copied to:
`%APPDATA%\klm\backups\pre-reconcile-0.6.10-20261111-145208-30d8dcd7`.
The database copy matched SHA-256 and passed read-only `quick_check`. Junctions
and external native-harness data are outside this backup's scope.

Both installers were applied. Initial UI Automation timed out on the engine's
welcome page; native window controls completed that same installer. Its attached
process exit code was unavailable, so installation was verified independently by
registry version, executable hash and running process path. Desktop silent setup
returned 0. Both registrations report 0.6.10. Engine bytes match the release build;
desktop bytes differ only in Tauri's three-byte NSIS marker (`NSS` versus `UNK`).

Both programs were launched and left running. API/frontend listeners use 7331/7332;
anonymous API version access returns 401 and frontend root returns 200 with HTML
identical to the newly built bundle. Read-only SQLite `quick_check` returned `ok`.
Before/after counts match: 3 projects, 98 sessions, 20,119 session events, 13 graph
runs and zero automations. No history import, profile replacement or deletion was
performed. These checks establish installation/startup and retained inventory,
not functional acceptance of harnesses, UI, LAN access or login startup.

### OpenCode V2 plugin-readiness fix and engine-only update

The user reported HTTP 400 from `klm.policy/inventory` and authorized correction
and an engine-only reinstall. An isolated OpenCode 2.2.7 server reproduced
`rpc.unavailable` before plugin activation and HTTP 200 afterward with the same
plugin. Logs also contain the failure before the 0.6.10 installation.

V2 startup now polls the authenticated, directory-scoped plugin status until
`klm.policy` is active before requesting inventory. The wait is bounded to 30
seconds and honors cancellation. Failed activation and HTTP errors still stop
startup without exposing native error bodies or bypassing the permission gate.
V1 behavior is unchanged; no failed prompt or side-effecting RPC is replayed.

`go test -tags dev -run '^(TestOpenCodeV2PolicyReadiness|TestOpenCodeV2MCPConnection|TestOpenCodeV2CatalogInitialization)$' -count=1 .`
passed (0.882s). The readiness fixture covers delayed activation, unrelated plugins,
activation failure, caller deadline, authentication and terminal HTTP errors. An
isolated native probe reached activation after five reads (1,516ms), then obtained
HTTP 200 with 64 tools and the owned session-move restriction still applied. No
model prompt or real project task was submitted.

Engine 0.6.11 packaging and installation succeeded; installer exit code was 0.
The previous binary and stopped engine profile were backed up under
`%APPDATA%\klm\backups\pre-plugin-readiness-0.6.11-20261111-151301`.
Database copy hashes matched and backup/live read-only `quick_check` returned `ok`.
Before/after counts match: 3 projects, 98 sessions, 20,121 session events and 13
graph runs. Installed engine bytes match the fixed build and it is running.
Desktop 0.6.10 remained open, with unchanged executable hash. API/frontend checks
still return 401/200. A real user retry remains the end-to-end acceptance gate;
no full suite, commit or push was performed.

### Managed OpenCode V2 Library source implementation (2026-11-11)

The handed-off implementation session failed at the provider before editing. This
continuation implements managed Library preparation for `@opencode/cli` **2.2.7**.
The existing Library snapshot format is protocol-neutral; capture, graph/automation
snapshots, MCP routes and turn retirement are reused. Native session protocol checks
remain unchanged. No installed engine/desktop was stopped, replaced or restarted.

- `engine/capabilities.go`, `engine/graph_snapshot.go` and
  `engine/opencode_library.go` admit the validated V2 binary, retain V1 admission,
  and share configured instruction-path resolution.
- `engine/opencode_v2_library.go` projects native V2 provider/model, built-in agent,
  permission/policy and instruction configuration into a private discovery profile.
  It validates exact skill paths, root/child permissions, owned plugin provenance,
  gateway catalogs and the native version before model submission.
- `engine/opencode_interactive.go` connects both authenticated loopback gateways
  with V2 contracts, retains the plugin-readiness wait, seals/attests the Library
  catalog, and selects the managed root through V2's session-agent API.
- `engine/opencode-permissions-plugin.mjs` filters skill/MCP registries, keeps
  internal skills and role tools away from native children (also under YOLO), and
  removes native administration tools. Its final transforms register through the
  inventory RPC after built-in configuration plugins initialize; a metadata probe
  showed that registering these restrictions during setup alone lets later native
  configuration append broader permission rules.
- `engine/opencode_permission_gate.go` understands V2's skill `id` input and keeps
  role and administration restrictions independent of permission approvals.
- `engine/opencode_v2_library_test.go` contains focused projection, admission,
  catalog-rejection and plugin-role checks, plus an opt-in disposable native probe.

Scope is deliberately bounded: native V2 configuration only, native build/plan
roots, built-in providers/agents and exact 2.2.7. Legacy-format provider/permission/
agent settings, file-based agents, custom roots, organization/well-known remote
configuration and administrator discovery sources stop preparation explicitly.
No source config, saved selection, native history or credential is rewritten.
No dependency, database schema or installer changes were added.

Native metadata evidence uses disposable home/config/data/cache/state directories,
random authenticated loopback ports and synthetic Library tools. It verifies
selected external/internal skill paths, root/child permission lists, no unselected
global/project plugin or skill, exactly two gateways, native administration-tool
removal, build/non-YOLO, plan/YOLO, an empty selection, and a selected-skill native
denial. A disposable native session ID and execution directory survive private
profile replacement. No model prompt, paid task or real native profile was used.

Final focused check passed in `engine` (8.346s), compiling the package and running
only these eight tests. The JavaScript role self-check also exercises child
catalog filtering and rejection of a child impersonating the root:

```powershell
$env:KLM_RUN_LIBRARY_NATIVE_PROBES='1'
$env:KLM_OPENCODE_V2_PROBE_BINARY='C:\nvm4w\nodejs\node_modules\@opencode\cli\bin\opencode.exe'
go test -tags dev -run '^(TestOpenCodeV2LibraryProjection|TestOpenCodeV2LibraryInventoryRejectsLeaks|TestOpenCodeV2LibraryAdmission|TestOpenCodeV2LibraryPluginRoles|TestOpenCodeV2LibraryProbe|TestOpenCodeV2PolicyReadiness|TestOpenCodeV2MCPConnection|TestOpenCodeLibraryProjectionPreservesNativePolicy)$' -count=1 -v .
```

Remaining human acceptance: real selected MCP calls/resources and permission
replies; skill loading; native/owned delegation; graph/automation execution; real
history continuation; and selection changes, steering and Stop across complete
turns. Metadata probes and compilation do not establish those behaviors. Engine
0.6.11 and desktop 0.6.10 remain installed unchanged. No full suite, review agent,
installation, commit or push is included in this source-only implementation.
