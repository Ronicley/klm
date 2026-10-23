# Handoff — upstream feature integration

## Current delivery

`upstream/main` at `5921442` has been combined with the fork's `develop`
(`b402259`) and all existing local work. The integration was prepared in the
isolated worktree `../klm-upstream-integration`, then transferred to the main
working tree with conflicts resolved. The user subsequently authorized committing
and pushing all integrated work to the fork's `origin/develop`. Inspect `git status`
and the remote tracking branch for the delivery result. Runtime human validation
remains pending independently of publication.
The isolated worktree also retains its unfinished merge; do not use it to commit.

## Recovery

- Full pre-integration safety stash: `7521f2cc0f82cba25b4d446e8c00ff517a1d42ed`.
  It includes the OpenCode version selector and the previous untracked files.
- Second safety stash immediately before applying the merge:
  `2a10f3a65c01a69158e0c628b9940cfc76867291`. Both backups are retained.
- The older stash `283b1d1d58c828b6503d925bebf47ca326349648` does NOT include
  the version selector. Never apply these backups indiscriminately over this tree.
- Existing backup branch: `backup/develop-before-upstream-20261023-143034`.
- Original project instructions, `.klm` graph definitions, `.vscode` configuration,
  root documents and screenshots removed by upstream cleanup were preserved.
- Real engine data has not been opened or migrated by this integration work.
  Back it up before runtime validation; do not assume code rollback reverses data.

## Included upstream features

- Live GitHub activity, grouped feed, bookmarks and feed settings.
- Provider-backed Tasks board and project connections.
- Persistent topics and topic grouping/session creation. Upstream's durable
  folders-to-topics migration retains the legacy labels for history/receipts.
- Persistent Focus workspace, floating chats and photo backgrounds/blur/mask.
- Mobile navigation, native audio recording, Wi-Fi routing and branding.
- Skills/MCP Library, capability snapshots, gateways, OAuth and elicitation.
- Native-boundary steering for Pi/OpenCode V1, interrupting Send now and removal
  of cumulative Pi/Codex output quotas (per-frame/content limits remain).
- Editable OpenAI transcription vocabulary prompt, alongside the fork's
  OpenRouter transcription provider.

## Fork behavior retained and adapted

- OpenCode V1/V2 selection, separate executable paths, restart-only application,
  native protocol identity and cross-version resume protection.
- Session deletion/tombstones, message editing/revert and attachments.
- Chat agent selection, graph transfer/export/import, graph limits, retry,
  pause/resume/stop, plan reader/speech and question context.
- Graph node YOLO inheritance from the invoking conversation.
- Native image transport, including Pi steering images at the new boundary.
- Graph transfers include the new agent Library selections; imported selections
  still require valid destination Library IDs.
- Existing private authenticated loopback bridges are unchanged in scope.

## OpenCode compatibility boundary

- Managed Library execution uses upstream's V1 `opencode-ai 1.18.35` profile,
  config isolation, positive skill permissions and Library handshake.
- The V1 plugin also retains the fork's permission-policy readiness handshake.
- V2 keeps its existing native plugin, catalog and HTTP/SSE steering path.
  It is NOT forced through the upstream V1 package/version check.
- Nonempty saved Library selections on V2 fail preparation explicitly rather
  than being ignored or cleared. Empty selections use the legacy V2 path.
- Graphs captured with managed V1 capabilities cannot execute those snapshots on
  V2. New V2 runs without selected Library capabilities retain the existing path.
- This is not a port of the managed Library profile to the V2 native plugin API.
- A previously bound general-agent conversation still requires its original
  protocol. No cross-protocol history migration was added.

## Lightweight checks

Performed in the isolated integration worktree:

- Go dev engine build: `go build -tags dev -o <temp>/klm-upstream-integration-check.exe .`.
- Desktop TypeScript: `npx tsc --noEmit` (after integration adjustments).
- `node --check` on the V1 and V2 permission plugins.
- `git diff --check` on the resolved working changes.

These checks succeeded; they do not prove native runtime compatibility. No test
suite, model execution, real provider/OAuth flow or UI was run. Flutter/Dart are
not on PATH, so mobile compilation was not checked. Rust/Tauri and installers
were not built. The isolated frontend dependencies were installed with `npm ci
--ignore-scripts`; the main working tree may need `npm ci` for new dependencies.

## Manual validation before commit

1. Back up engine data. Start a dev engine and client; check legacy folders become
   topics without losing sessions, archived grouping, history or deletion markers.
2. Check Tasks/provider connections, real GitHub feed, Focus layouts/backgrounds
   and mobile routing/navigation/audio on their target platforms.
3. Select V1 and its compatible executable, restart, create a new chat and check
   Library skills/MCP selection, permissions, questions/elicitation, steering,
   Send now, Stop, continuation and graph Choice/tool drainage.
4. Select V2 and restart. Check empty-selection chat/graphs, catalog, attachments,
   permissions and steering; nonempty Library selections must fail explicitly.
   Switching protocols on an existing native chat must retain the recovery error
   and history; restore the original protocol to continue.
5. Exercise fork features: delete/edit/revert, agent selector, graph import/export
   with Library fields, limits, pause/resume/stop, retry and plan reading/speech.
6. Build Flutter/Tauri and validate real native behavior/installers as needed.
7. Commit/push only after explicit user approval. Do not reapply old stashes.
