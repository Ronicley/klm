# Develop consolidation — 2026-11-02

## Primary source

Continue work in `C:\Projetos\Projetos\pessoal\klm` on `develop`.

`develop` was fast-forwarded from `b4e03123a4bdee5b5a3d567e72b8b3b3ef81d55e`
to `d0b522d129a4e6a9d8f76f22bbdd3e3e75d1399f`, already shared by
`origin/develop` and `update/upstream-0.6.7-20261101`. No new commit or push was
created. The 37 upstream commits are now available in this directory.

The original local metrics work was reapplied and its 11 textual conflicts were
resolved while retaining SQLite startup, authentication, paging, analytics,
protected drafts and native delegation. Changes already supplied by upstream
were retained once, not duplicated. The source worktree's pending OpenCode V2
plugin/delegation fixes and final-message history fix, including their tests,
were brought into `develop` as uncommitted changes.

The former upstream worktree and branch remain intact as recovery references;
they were not removed or cleaned. Do not continue parallel feature work there.
The installed engine and running processes were not updated or restarted.

## Metrics compatibility boundary

The local Session Metrics Dashboard used the old in-memory/session-journal
storage. Its source and documents are preserved, but this consolidation does
not implement a SQLite metrics migration. The engine retains SQLite startup;
legacy execution hooks do not replace upstream's SQL analytics.

Both legacy metrics endpoints return HTTP 501 on SQLite instead of silently
returning misleading empty totals. The upstream `/api/analytics` remains
unchanged. Port collection, durable observations, recovery and reads before
claiming that the preserved dashboard works with the current engine.

## Recovery copies

Before changing Git state, modified/untracked source files and binary diffs from
both worktrees were copied and file hashes checked under:

`C:\Users\ronic\AppData\Local\Temp\opencode\klm-develop-consolidation-20261102-114820-c71c5b28`

The original develop changes also remain in stash object
`aead74bea5990e7bcdbae8bd32eca943ab60538a`.
The stash was applied, not popped or dropped. Original HEAD and status snapshots
are included in the recovery directory. Do not blindly reapply this stash over
the consolidated files. Recover into a separate directory and compare first.
No live application database was migrated or modified.

## Focused checks

- `go test -tags dev . -run '^Test(OpenCodeV2NativeHistoryMessage|OpenCodeNativeHistoryBoundedMessages|LegacySessionMetricsRejectSQLite)$' -count=1`
  in `engine`: passed; package compiled and three focused tests ran.
- `node engine/opencode-delegation-v2.test.mjs`: passed.
- `npx tsc --noEmit` in `clients/desktop`: passed after removing a duplicate
  `max_traversals` field introduced by automatic reapplication.

No full suite, live harness prompt, application installation, data migration or
UI acceptance was performed. Native OpenCode completion and the reconciled Pi
stats flow still require human validation with an updated engine.
