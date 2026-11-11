# Automatic pagination restoration

## Plan and scope

Implemented from `AUTOMATIC_PAGINATION_FIX_PLAN.md`, authored by the saved Plan
Writer child. The initial child execution was interrupted by an engine restart;
the parent recovered the recorded child and resent the complete context before
receiving the completed plan. No independent conversation was created.

The earlier history observer was introduced by `d3a38c3` and removed in `a255836`.
The correction uses current SQLite cursors rather than reverting those files.

## Implemented

- Shared near-edge detection, 160 px margin, real scroll-root discovery, resize
  and visibility wakeup, cursor-attempt suppression and error recovery. The
  observer only acts on visible surfaces. A failed cursor retains Retry even if
  background revalidation clears the feature's transient error.
- Explicit 25-item requests across the converted lists. History items are
  persisted events; grouping and response byte limits are preserved.
- Chat before/after cursors, directional contiguous/overlapping merge, per-event
  revision reconciliation and the existing 200-event bound. Revalidation reads
  the retained range in bounded pages instead of `startIndex + 50`.
- Reading-anchor markers and restoration; following streaming requires the real
  tail. Shared conversation reads are serialized between mounted viewers.
  Main, general, side, Focus/mobile and subagent views use the shared behavior.
- Session/topic metadata accumulation, removal of four-page rotation, retained
  boundary revalidation, sidebar anchor preservation and incomplete-empty-state
  handling. Collapsed groups do not initiate session pagination.
- Happening now, feed and grouped activity use near-edge paging. Feed boundary,
  retention, bookmarks and explicit grouped-activity Update semantics remain in
  their existing feature-owned readers.
- Graph activation picker and bidirectional activity, Tasks columns, webhook
  inventory, automation inventory/run history, topic/session/agent pickers,
  agent references, Library skills/deletion references, queues and request pages.
- Paged native select use cases now use SearchSelect with feature-owned search
  and pagination footers. Generic UI primitives do not own endpoint behavior.
- Go handlers accept bounded page limits for projected graph requests,
  activations/events, automation runs, pending items and delegated requests.
  Existing defaults/maxima and response byte protection remain; session detail
  requests may request a 25-item pending prefix without changing mutation defaults.
- Existing grouped-activity test fixtures no longer depend on production Load
  more buttons. The history merge test includes bounded bidirectional traversal
  and rejection of a disjoint page.

The restored compact subagent row remains the rendered projection. Pagination
does not load unopened child transcripts. Single-event byte/chunk navigation,
Show more/Show less presentation controls and analytics period navigation retain
their own semantics.

## Lightweight checks performed

- `npx tsc -b` in `clients/desktop`: passed after correcting two introduced type
  errors (optional graph access and an unreachable abort).
- `go build -tags dev -o "C:\Users\Samuel\AppData\Local\Temp\opencode\klm-pagination-check.exe" .`
  in `engine`: passed. The executable was not started or installed.
- `npx --no-install vitest run src/features/chat/sessionState.test.ts`: passed,
  10 tests. No full test suite or adversarial review was run.
- `npx --no-install vitest run src/features/klm-agent/GithubActivityCard.review.test.tsx -t "keeps page two"`:
  passed, one selected existing test; six other cases skipped. This verifies
  second-page retention and explicit Update with the edge-trigger fixture, not
  browser IntersectionObserver geometry.
- Final `npx tsc -b` and the scoped `git diff --check`: passed.

## Human acceptance still pending

Use the detailed matrix in the plan. In particular:

1. Scroll chat above and below a 200-event boundary, with grouped tools and
   concurrent streaming; check the visible line stays in place.
2. Display the same chat in main and Focus; change session/project/engine and
   filters while requests are pending.
3. Navigate beyond four pages of sessions and topics, including collapsed and
   archived groups, search and insertions/removals during revalidation.
4. Exercise every converted surface in desktop, Focus and a mobile viewport;
   verify Network requests use 25 and observers belong to the actual scroller.
5. Fail a pagination request while its edge remains visible; confirm no automatic
   retry loop and that Retry resumes. Confirm end-of-list stops requests.
6. Verify a short viewport fills sequentially, while collapsed work groups do not
   drain a hidden transcript. At the history bound further deliberate scrolling
   can advance without automatic opposite-edge ping-pong.
7. Validate graph polling/activation selection, pending request resolution in
   another view, automation session reuse, feed retention and explicit Update.

Compilation and the targeted merge tests are not browser/runtime acceptance.
