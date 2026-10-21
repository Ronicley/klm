# Tasks — two review/correction rounds

The user explicitly requested two rounds with one native subagent reviewing and
the main agent correcting. Both reviews were read-only and static. No third
review, full suite, complex/integration tests or real-provider probes were run.

## Round 1

The reviewer identified five medium-severity issues:

1. **Concurrent draft overwrite:** connection and phase forms used the latest
   configuration revision with an older draft. Forms now capture the revision at
   edit/create time; save/disconnect uses that revision. Conflicts, newer config
   and uncertain mutation results require an explicit state/draft reload.
2. **Global phases blocked by a removed project:** global phase discovery now uses
   a dedicated read-only `phase-mappings` stage for retained connections, including
   removed projects. It requires an existing connection and forbids label creation;
   ordinary discovery, connection management, inventory and moves retain their
   project availability gates.
3. **Missing receipt blocked all moves indefinitely:** missing receipts are
   distinguished from unavailable receipt reads. The user can explicitly cancel
   the known local unaccepted operation. Under the same lock as move acceptance,
   the engine either returns the existing receipt or persists a terminal receipt
   reserving the ID against a delayed original POST. Accepted/provider work is not
   cancelled, rolled back or replayed. Terminal receipt retention remains 30 days.
4. **Mutation deadlines were not applied:** Tasks now supplies timeout signals for
   POST/PATCH/DELETE/discovery. Uncertain configuration outcomes block another save
   until explicit reload; uncertain moves query their receipt, never resend blindly.
5. **Older GET could erase Refresh syncing:** local generations and abortable GETs
   invalidate reads before Refresh and after authoring saves. Reads cannot run ahead
   of the Refresh POST, and a fresh GET follows its settlement.

After correction, TypeScript passed. A Go dev build of the shared working tree
was blocked by concurrent Topics work outside this scope (`TopicID`, `TopicSchema`
and `Topics` referenced before corresponding model fields were present).

## Round 2

The same reviewer reread the current files. Findings 1–4 were confirmed resolved.
Finding 5 was resolved for the original active-view scenario, but a remaining
Focus lifecycle case was found: minimizing/restoring discarded ownership of an
in-transit Refresh POST, allowing a new GET ahead of its acceptance.

The main agent corrected the remaining case: hiding cancels/invalidates reads but
retains the pending Refresh identity. Restoration reflects that pending request,
waits for it to settle and then performs a fresh read. A request settling while
hidden is read after restoration. Explicit saves still invalidate older reads.

The second review reported no other concrete actionable defects. This is the
reviewer's static result, not a guarantee of runtime/provider correctness. The
final lifecycle correction was not followed by a third review.

## Final lightweight checks

- `npx tsc -b` in `clients/desktop`: passed after the final correction.
- `go build -tags dev -o C:\Users\Samuel\AppData\Local\Temp\opencode\klm-board-check.exe .`
  in `engine`: passed after the concurrent Topics model fields became available.
  No Topics code was changed by this task to repair the earlier build blocker.

These are compilation checks, not execution/acceptance tests.

## Human validation still required

Verify multi-client edit conflicts/reload, new phases with a retained removed
project, bounded requests during stalled networking, receipt-missing cancellation
and a delayed original POST, accepted receipt passthrough, Refresh with late GETs,
and Focus hide/restore before/after Refresh acceptance. Existing GitHub/Azure,
assignment, sprint/current-sprint and client acceptance checks remain pending as
documented in `ACTIVITY_BOARD_IMPLEMENTATION_PROGRESS.md`.
