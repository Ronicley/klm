# GitHub activity grouping — projection v2

Activation update (2026-10-16): isolated dev validation succeeded, then installed engine/desktop were updated through one-shot local maintenance using the official engine stop/start CLI and backed-up binaries/prompts. Post-restart verification confirmed **106 original events preserved as 39 cards / 6 groups**, watermark 116, unchanged payload hashes and event metadata. Coordinator also checked the installed HTTP frontend with Playwright (history/Details/jobs/deployment/skipped/failure; zero console errors). See `GITHUB_ACTIVITY_GROUPING_ACTIVATION.md`. Historical pending statements below describe earlier stages; personal visual acceptance remains manual.

Review status (2026-10-16): both coordinator review/correction rounds are closed. The final targeted frontend run passed all seven tests, including partial expiry through the parent feed. **Live activation remains pending target selection and the procedure below; visual acceptance remains a human check.** Historical implementation-stage status statements below describe those earlier stages, not an activated deployment.

Implementation delivered for coordinator review. **Activation of the existing feed is pending both review/correction rounds and human validation.** No live state, running engine, installed binaries or frontend bundles were changed during implementation.

## Projection and correlation contract

- Payloads, stored event IDs, delivery deduplication, release sequence, pending releases, receipt/expiry and retention remain unchanged. There is no new persisted metadata, schema migration or backfill. Structured metadata is extracted in the existing bounded summary cache; groups are reconstructed from eligible retained releases on every read. Restart naturally invalidates that process-local cache.
- Workflow identity is `project / webhook / github / repository.id / run / workflow_run.id or workflow_job.run_id / run_attempt`. An absent attempt is **not** attempt 1. All provider IDs and release sequences are decimal strings; large IDs never pass through a JSON floating-point number.
- Deployments use the same origin scope plus `deployment.id`. A `workflow_job` with explicit run/attempt identity and a top-level `deployment.id` proves its association. A `check_run.deployment.id` can also prove it, but only after the check is explicitly resolved to a workflow job.
- A check joins only when `check_run.url` exactly equals a retained job's explicit `workflow_job.check_run_url` in the same origin/repository scope. Equal IDs across job/check types, names, branch, SHA and timing do not create links.
- A suite requires an explicit run `check_suite_id` **and** an explicit matching `check_suite.run_attempt`; otherwise it stays individual. A suite ID alone can span attempts. Conflicting retained relationship evidence is conservatively left ungrouped (or deployment-only), rather than selecting the newest relationship.
- Child state comes only from actual `workflow_job` or deployment/status events. Check/suite results remain in Activity and do not overwrite workflow/job/deployment results. Completed run attempts/jobs do not regress to a late queued/in-progress receipt. Conflicting terminal results use source timestamps, then release sequence for deterministic ties. Deployments prefer actual status events over the creation event and compare source timestamps, allowing `success → inactive`. Missing source times remain missing; release order is only the deterministic fallback, not a claimed GitHub timestamp.
- Stable card IDs are SHA-256 hashes of their typed, scoped identity (`pipeline-…`). Original events retain their original IDs. New attempts and deployments have separate identities. Card order uses the newest **visible** member release, not the selected state's receipt.
- There is no invented start/end or claim of complete provider history: only retained events contribute. TTL/deletion/removal are applied before correlation. Groups cannot retain evidence from removed sources or incorporate future releases beyond the cursor watermark.

## API and agent reads

`GET /api/activity/github` now pages **cards**, not deliveries. Other event families stay one-event cards. Grouping occurs across the entire eligible source snapshot **before** card pagination. Cursor version is **2**; v1 cursors return the existing refresh-required error. A fresh request fixes a release high watermark, reused by subsequent pages. Future releases cannot supply even correlation evidence to an older snapshot.

Filtering is applied after identities are resolved, but only visible members supply text, result, children, order, count and history. `group.filtered` identifies a partial presentation. Search matches any visible member, then returns its visible card, not just matching transitions. No hidden job titles/results or hidden member counts are displayed. Category/event preferences continue to affect only that client, not the general agent's view.

Group fields:

- `group.kind`, `runId`, `attempt` or `deploymentId`: identity;
- `eventCount`: visible retained original members;
- `children`: bounded preview (up to eight jobs/deployments and 16 KiB serialized child content);
- `childCount` / `childrenHasMore`: complete visible child count and explicit pagination affordance;
- `highWatermark`: member/child snapshot boundary;
- `composition`: hash of visible retained member IDs and relationship provenance, independent of the newest release/count; changes also detect expiry/removal;
- `detailEventId`: representative original event; `groupRelation` on member events records the relationship used.

Full retained Activity and children use the **same** endpoint:

```text
GET /api/activity/github?cardId=<card-id>&view=events&high=<highWatermark>&limit=20
GET /api/activity/github?cardId=<card-id>&view=children&high=<highWatermark>&limit=20
```

Keep category/search/exclude filters and `high` unchanged on subsequent `cursor=<nextCursor>` requests. Activity pages contain original event IDs, actions, source timestamps and compact metadata; all events remain independently readable through `GET /api/activity/github/<original-event-id>`. That endpoint also accepts a grouped card ID for representative Details plus its retained event count. UI Activity exposes Details for each member, with no raw payload download. The existing project-scoped raw-payload API is unchanged.

`feed_read` retains its default 20/max 50 page size, shared visibility/TTL, byte-limited pagination, `postId` Details and fragment behavior. Its list now contains cards. To read every original event, follow card pages, then call `{cardId,view:"events",high,limit?,cursor?}` for each grouped card. `{cardId,view:"children",high,...}` pages all actual children. Ungrouped card IDs are already original event IDs. Do not combine `postId` with `cardId`/`view`/`high`. Reads remain nonconsuming reference access, not monitoring or authorization.

## UI behavior

Cards show overall workflow state separately from real job and deployment states; no percentage. A workflow lacking visible run-state evidence does not borrow a child result as its overall result. Deployment rows retain their own identity and state, with specific job provenance only when proven. Failure, cancelled, skipped and parallel jobs remain separate states/rows. Only active statuses pulse; reduced-motion disables the pulse.

`Activity` loads paginated history on demand; `Details` remains available for original member events. Larger child lists have explicit pagination. Refresh keeps the existing contiguous-window strategy and DOM anchor preservation, now using stable group IDs. Expanded Activity/children readers retain their loaded window and watermark across transitions. Composition changes revalidate their contiguous loaded window at that same watermark, preserving surviving rows/Details and the visible member anchor. `Update activity` explicitly advances to the latest watermark, retaining the already-loaded depth rather than replacing it with page one.

Loaded members/children are filtered by engine-adjusted time at render and removed from local reader state at expiry; removing a row unmounts its cached Details. A composition change reconciles removals even when release, count or expiry of the displayed card does not change. A failed reader revalidation hides cached rows/Details until Retry, rather than leaving potentially removed content visible.

For a **group**, the earliest member `expiresAt` is a **revalidation deadline**, not definitive card expiry. At that deadline the feed immediately requests a fresh contiguous window (or queues that refresh behind a request already in flight). It keeps the same card/reader identities mounted using a client-only `retentionPending` shell: representative text, result, timestamps and links are discarded, preview children are filtered by their own TTL, and old counts are not displayed. Activity/children readers keep their loaded pages and independently remove expired rows/Details. A successful response reconciles the surviving composition in place; an empty eligible group is removed. A failed feed request leaves only the scrubbed shell plus unexpired reader content and the normal feed error/Retry path; it does not redisplay expired snippets. No 15-second disappearance/reappearance is needed to reconstruct survivors. Individual posts still disappear at their own TTL. Layout/scroll behavior on desktop, Focus and mobile WebView still requires human validation.

Bookmarks remain in-memory but anchor to a representative **original event ID**, not only a mutable projected card ID. The client resolves just its selected anchors through `GET /api/activity/github/bookmarks?eventId=<original-id>&high=<displayed-watermark>` (repeat `eventId`, maximum 100 per request; larger selections are batched). The bounded response is `{items:[{eventId,cardId,expiresAt?}]}`, without event bodies or history. This maps a standalone deployment/check bookmark to its run card after explicit late correlation at the same snapshot used by the feed. Resolution ignores client display filters, so hiding a family does not delete bookmark intent. Expired/removed/unavailable anchors are omitted and discarded; transport failure preserves intent for the next feed refresh. Removing a merged card's bookmark removes all selected anchors currently mapped to it. Bookmarks are not durable across reload/activation, as before.

## Read-only evidence gathered on 2026-10-15

Inspected only feed summaries and the 58 pipeline payloads via the existing API at `127.0.0.1:7331`. No raw bodies, credentials or private application content are reproduced here. That port is the installed-engine default; it does not select the eventual activation target.

- Population remained 80 cards/events, high watermark `90`: 13 workflow-run events, 20 workflow-job events, 16 check-run events, four suite events, one deployment and four deployment-status events, plus 22 other individual events.
- All 20 jobs carried run IDs and `run_attempt=1`, covering four runs/eight jobs.
- All 16 check-run event URLs exactly matched explicit retained job `check_run_url` values in their origin/repository scopes. This is stronger evidence than their numerically equal IDs.
- Job `116118161233` explicitly carried top-level deployment `7077863283` and run `38639669364`, attempt `1`; check-run events also carried that deployment ID. The job-specific deployment relationship is available without inferring from SHA/branch/log URL.
- All four suite events lacked `run_attempt`. Despite matching run `check_suite_id` values, these remain individual under v2.
- Expected count for this inspected population under these rules: **30 cards = four workflow cards representing 54 events + four standalone suite events + 22 other events**. This is an evidence-based expectation, **not a count measured from an activated v2 feed**. The earlier 26/27-card estimate depended on suite relationships that cannot safely identify an attempt.

## Final activation procedure (after both reviews)

1. Confirm the target explicitly: installed engine uses API `7331`, Focus `7332` and `%APPDATA%/klm/engine`; dev uses API `17331`, Focus `17332` and `%APPDATA%/klm/engine-dev` with `-tags dev`. Never copy one data directory into the other. Record the approved build/revision containing projection v2 and corresponding client; this implementation is currently an uncommitted working-tree delivery, not a released version.
2. Record the current target's API population immediately before activation. Page its complete feed at one watermark, retaining original IDs/counts for comparison (not raw payloads). Population can legitimately change through arrival, TTL or removals; compare the common retained release snapshot.
3. Build/package the approved engine and client using the project's normal delivery procedure. Keep the prior engine/client artifacts available for rollback. For installed use the regular engine and desktop installers; for dev use the regular dev-tag engine/client commands. Only perform replacement/restart in this final authorized stage. A normal engine stop/checkpoint provides the supported consistent point for an optional backup; do not hand-edit/copy partially changing state files or replay webhooks.
4. Start the approved target version and reload its client. There is **no migration command**: the new process has an empty summary cache, extracts metadata from original retained payloads and reconstructs v2 cards on first read. Discard old cursors by starting fresh. Repeating restart/reload/read is idempotent with respect to webhook records and creates no releases or backfill writes.
5. Read all card pages from `GET /api/activity/github?limit=100`. For each group, page `view=events` with that page's high watermark; collect original member IDs. For individual cards collect their original IDs directly. Verify union size equals the eligible retained-event population at that watermark and each ID appears once. Report total cards, grouped member events, individual/fallback events and suites/relationships still unresolved. Page `view=children` to verify every retained actual job/deployment is accessible, not just preview rows.
6. Validate manually with the user: runs #261–263 show Verify success and Build/deploy skipped; the deployment linked to #264 keeps its own state; Activity/Details, large history, category/type exclusions, member search, updates below the top, bookmarks and older-page boundaries behave correctly on the target client. Use actual available population, not the old snapshot's count.
7. Record the real observed counts, build identity, target and human result in the plan. Mark activation complete only after those checks.

Rollback: stop only the selected target through its normal lifecycle, restore the prior engine/client artifacts, restart and refresh the client/cursors. There is no v2 persistent state to reverse or payload migration to undo. TTL continues normally; expired/deleted events cannot be recovered by rollback. Do not modify event IDs, release/delivery metadata or retained state to force the historical population back.

## Checks executed during implementation

- `go test -tags dev -run '^(TestGithubActivityGroupingIdentityAndState|TestGithubActivityGroupingPaginationAndMembers|TestGeneralFeedRead)$' .` in `engine`: passed. This compiles the engine/test package and runs only two focused grouping tests plus the existing general-feed compatibility test.
- `npx tsc -b --pretty false` in `clients/desktop`: passed, including after final UI adjustments.
- No full suite, integration smoke, review rounds, live migration, binary swap, engine restart or human UI acceptance was performed.
