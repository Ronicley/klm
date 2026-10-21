# Tasks — implementation and validation

## Confirmed decisions

D1–D4 from `ACTIVITY_BOARD_IMPLEMENTATION_PLAN.md` were answered in the implementation conversation:

- Each board connection explicitly associates one GitHub repository, or one Azure organization/project/work-item type, with one local project. Multiple connections may belong to a local project; duplicate external scopes are rejected. Existing webhook connections are independent and do not count toward phase coverage.
- GitHub.com and Azure DevOps Services (`dev.azure.com`) use user-supplied PATs. No token was supplied to or used by this implementation session.
- A provider value can belong to one phase per connection. Multiple labels for different phases appear in **Mapping conflicts**, with explicit **Move to** recovery. Phase count has no product ceiling, but every Azure connection needs sufficient distinct native states to cover every phase.
- GitHub classification uses labels alone, includes open and closed issues, excludes PRs and hides issues without mapped labels. Moving preserves open/closed and unrelated labels.

## Delivered code

### Engine

- `engine/activity_board.go`: separate `activityBoard*` configuration, protected credential storage, compact provider projection, sync state and durable move receipts. Stable GitHub issue IDs and Azure organization identity + work item IDs form hashed external keys; labels are mapped by stable GitHub ID, Azure states by exact name within one work item type.
- `engine/activity_board_api.go`: board-specific public HTTP routes and explicit DTOs, configuration revisions, complete mapping validation, scope uniqueness, discovery, explicit GitHub label creation and column pagination.
- `engine/activity_board_github.go`: repository validation, paginated labels and issues (`state=all`, PRs excluded), current-item reads and specific label add/remove calls. No whole-label-list replacement.
- `engine/activity_board_azure.go`: organization/project/type discovery, native state catalog, ascending WIQL ID windows of 200 and complete detail batches, plus state-only JSON Patch with `/rev` test. No rule bypass, invented states or automatic fallback transitions.
- `engine/activity_board_worker.go`: startup/new-connection/manual refresh, two scan slots, one scan or move at a time per connection, five-minute scan deadline, all-or-nothing cache replacement and generation/config revision checks. Failed/partial scans retain the previous snapshot as **Out of date**. Azure revisions do not regress cached items.
- Intent is journaled before dispatch. A move returns **pending**, performs current-item/catalog validation, checks the observed version and rereads after mutation. Only confirmed provider state produces success. Interrupted/potentially partial updates remain **unconfirmed**; refresh reads current state without replaying a write. Observing the requested phase during reconciliation does not claim causation.
- Completed receipts are retained for 30 days and pruned on subsequent board activity/startup. Pending/unconfirmed receipts do not expire. Reusing an operation ID with changed input conflicts; local deduplication is not provider exactly-once delivery.
- Mapping edits and disconnect are blocked while receipts are unresolved; replacing a credential with the same mapping IDs/order remains available for recovery. Provider/scope/local-project changes require a new connection.
- All provider HTTP occurs outside `app.mu`. Redirects are refused; hosts are fixed, responses/timeouts bounded, upstream errors sanitized and writes never retried automatically.

Integration points: `store.go`, `stream_journal.go`, `main.go`, `api.go`. Load, replay and checkpoint validate the new records. Old storage initializes an empty board; abandoned Tasks are not imported. Removing a project hides its items, prevents new scans/moves and marks its connections stale; after restoration use **Refresh**. Removing a board connection affects only local configuration/cache/credential, never provider items or webhooks.

### Client

`clients/desktop/src/features/activity-board/` contains the shared Tasks page, checklist entry card, API types, shared configuration/status loading, phase dialog, project connection panel and ordered mapping editor. The internal `activityBoard*` storage/API namespace is retained; this UI rename does not revive abandoned Tasks records.

- **Tasks** / **Open tasks** entry below **Happening now** on the KLM Agent page; **Tasks** button with the same `ListTodo` checklist icon below **New session** only for projects with at least one saved task connection. Failed/offline connections still count as configured; unsaved drafts do not. Configuration loaded once at app level is shared by navigation, Tasks and project settings, with existing focus/visibility refresh and active-work polling. Successful saves and disconnects immediately update the navigation; removing the last connection hides the button.
- One page with an independent **All projects**/project filter, **Back**, **Refresh**, **Phases** and **Connections**. The latter opens **Project settings → Tasks**, selecting a project first in the global view. It opens without an active session and supports empty configuration without projects. The board uses the main area without a sessions sidebar; the rail reflects the board filter rather than the previous chat project.
- Global phases, empty columns, horizontal scrolling, keyboard/touch **Move to**, provider links, project/resource metadata and separate conflict/unmapped-Azure columns.
- First 40 items per visible column, explicit **Load more**; offscreen columns defer their initial local API read. Cursors bind to projection revision/project/phase; changed cursors request column reload.
- The **Sprint** dropdown filters exact Azure iteration paths, keeping the mandatory PAT-owner restriction. Besides distinct confirmed task paths, it queries `/{project}/_apis/work/teamsettings/iterations?$timeframe=current&api-version=7.1` with no team parameter: the user explicitly selected Azure's default team as the current-sprint source. Current paths are validated within the canonical project, marked `current:true`, sorted first and labeled **Current** in the picker. They remain selectable with no assigned work items. Organization/full-path descriptions disambiguate names; multiple type connections to one external project share one metadata lookup and one choice per path. Other empty iterations/non-default teams are not enumerated.
- Exactly one confirmed current sprint is preselected once per project visit. None or multiple confirmed currents initializes **All sprints** with a status note, never an arbitrary first current. Failed current discovery leaves the initial selection pending, offering Retry/manual choices rather than silently choosing All. Initial Azure columns wait for default resolution or an explicit choice, avoiding an initial flash of all historical tasks. Project changes reinitialize the default; subsequent refreshes do not override manual past sprint/All sprints selection. A chosen sprint filters Azure items only; All sprints retains the mixed-provider view. **No sprint** is the project-root iteration. No filter changes remote work or connection settings.
- `System.IterationPath` remains in the compact Azure projection. Legacy/missing data yields **Refresh tasks to load sprint data** and no partial cache-derived choices, but confirmed current metadata can still be shown. Default-team lookups run outside `app.mu`, once per unique external project per catalog request, with bounded requests and a 90-second total deadline. Config/projection/lifecycle changes during lookup invalidate the response; current metadata failures are sanitized in `currentError`. Request cancellation/outdated responses cannot replace newer selections. No external request is made per card. Sprint changes cancel/unmount column loads and start sprint-bound pagination.
- **Phases** creates/renames global phases and requires mappings in every connection. **Project settings → Tasks**, directly below **Webhooks**, adds/edits/disconnects task connections, discovers values, explicitly creates GitHub labels and edits mappings/outbound priority using **Move up/down**. This panel lists only connections for the owning project, fixes new connections to that project and retains Azure discovery. General and Webhooks keep their existing sections. The global phase dialog no longer contains a connection flow.
- Azure setup takes organization + PAT, loads accessible projects into a searchable selector, then loads the selected project's native work item types. Choosing a type loads states for mapping. Organization/token/project changes clear dependent draft selections and mappings; existing connections retain their fixed scope. Empty catalogs and failures are distinct; failed type/state reads have explicit reload actions. Project listing uses the documented `$top`/`$skip` pagination without silently returning only the first page. The token needs **Project and Team (Read)** for project listing, alongside the board's Work Items permissions. These requests are metadata reads through the engine, not saved configuration or provider mutations.
- Azure tasks always use `[System.AssignedTo] = @Me` in every WIQL inventory window; Azure resolves the identity from the PAT. Current-item reads for moves always recheck that assignment through WIQL before the revision-guarded update. This is mandatory, with no UI toggle or opt-out, and does not assign/reassign work or compare ambiguous names/emails. Each connection's PAT determines whose tasks are included; GitHub behavior is unchanged.
- At startup, Azure connections with the former `onlyAssignedToMe` flag false/absent are migrated automatically: the flag becomes an internal legacy snapshot marker, configuration/projection revisions advance and the old unfiltered snapshot is withheld from item DTOs/cards (`sync.snapshotPending`) until a successful full filtered scan. No form save is required. The inventory query and current-item checks are unconditional and never consult this marker to decide whether to filter. Replacing any Azure PAT also withholds the previous owner's snapshot. Failed scans preserve the hold instead of showing unfiltered/previous-owner tasks. The legacy `onlyAssignedToMe` request field is accepted but ignored, including explicit false; it cannot disable @Me. Credential-only recovery with unchanged mappings retains its existing path.
- Configuration is engine-owned. Credentials stay in transient password inputs and are not written to browser storage. HTTP uses the existing `request`/`ENGINE_URL` resolution; the Flutter WebView uses the same page without native bridge changes.
- Receipt lookup and status polling never resubmit the mutation. Failed/uncertain delivery keeps an explicit receipt/refresh recovery action. Config polling is paused in hidden browser documents; provider inventory itself refreshes only at startup, connection save or explicit Refresh.
- Connection/phase drafts retain their initial config revision and require explicit reload after concurrent edits or uncertain saves. Mutating/discovery requests have timeout signals. Config GETs use a local generation and cancellation; Refresh retains ownership of its in-transit POST through Focus hide/restore and reads state after settlement.
- Missing receipts offer **Cancel unaccepted update** for the exact known local attempt. The engine returns an existing receipt or reserves the ID with a terminal cancellation receipt under the dispatch lock. Delayed original requests cannot dispatch during the normal 30-day receipt window. Accepted provider work is never cancelled, undone or replayed by this control.

## Actual HTTP contracts

All mutations use JSON. IDs/revisions are strings. These are the implemented contracts, not every proposed endpoint in the original plan.

| Route | Request / result |
| --- | --- |
| `GET /api/activity-board/config` | `{revision, projectionRevision, phases, connections, unresolvedMoves}`; connections contain `credentialConfigured`, availability and sync metadata, never the token/blob. |
| `PATCH /api/activity-board/config` | `{expectedRevision, phaseId, name, mappings}`. Empty `phaseId` creates a server ID; `mappings` is connection ID → ordered values for the new phase. Nonempty `phaseId` renames it. |
| `POST /api/activity-board/discover` | New draft `{provider,scope,token,...}` or existing `{connectionId,token?,...}` → `{scope,values}`. Azure `discovery:"projects"` lists projects; `discovery:"work-item-types"` lists project types. Read-only `discovery:"phase-mappings"` requires an existing connection and permits retained removed-project connections for the global matrix; `createLabel` is forbidden in this stage. Omit discovery for ordinary labels/states. GitHub label creation otherwise remains an explicit setup action. No incomplete connection is persisted. |
| `POST /api/activity-board/connections` | `{expectedRevision, name, provider, projectId, scope, token, mappings}`; mappings is phase ID → ordered `{id,name}` values, revalidated and resolved from the provider catalog. Azure is always assigned-to-PAT-owner only. The legacy `onlyAssignedToMe` input is ignored. |
| `PATCH /api/activity-board/connections/{id}` | Same input; token omission/empty string retains the stored token. Scope/provider/project remain fixed; mappings and credential can change with revision checks. No field can disable mandatory Azure @Me filtering. |
| `DELETE /api/activity-board/connections/{id}` | JSON `{expectedRevision}`; removes only local board ownership. |
| `GET /api/activity-board/items?projectId=&phaseId=&sprintId=&cursor=&limit=` | `{items,total,nextCursor,hasMore,projectionRevision}`. Default 40, max 100. `phaseId=conflicts` / `unmapped` select diagnostics. Optional `sprintId` is the opaque choice ID from `/sprints`, hashing Azure organization/project identity and case-normalized iteration path; empty means All sprints. It filters only the confirmed local projection and never expands PAT-owner scope. Cursors bind to project, phase, sprint and projection revision. Unknown/unmapped GitHub issues remain hidden. |
| `GET /api/activity-board/sprints?projectId=` | `{items:[{id,name,path,organization,project,current?}],needsRefresh,currentError}` combines active Azure connections' confirmed task paths with native current-sprint metadata for each project's default team. Same external project/path across type connections deduplicates; local project filtering applies before lookups. Missing legacy metadata/withheld snapshots yield `needsRefresh:true` without partial cache choices, but native current sprints remain available. `currentError` prevents automatic default selection when a scope's current lookup fails. No credentials, remote authoring or arbitrary team inference. |
| `POST /api/activity-board/refresh` | `{connectionId?}` → config with current `syncing` flags; reads run asynchronously/coalesce with existing connection work. |
| `POST /api/activity-board/items/{key}/phase` | `{operationId,phaseId,expectedConfigRevision,observedVersion}` → durable receipt, normally 202 pending. Identical replay returns the existing receipt without another write. |
| `POST /api/activity-board/items/{key}/phase/cancel` | Same identity/payload as the original move, explicit user recovery only. An existing matching receipt is returned unchanged; otherwise a terminal receipt reserves the ID without provider dispatch, even if config/item changed. Changed content under the same operation ID conflicts. |
| `GET /api/activity-board/moves/{operationId}` | Receipt recovery after uncertain client delivery/restart. |

Discovery/configuration requests can take up to 90 seconds; their response deadline is extended locally without changing engine-wide HTTP policy. Errors include an English `error` and structured board HTTP `code`. No external response body is returned.

## Boundaries and current limitations

- There are no board webhook consumers, Azure hook receiver, auto-created hooks, agent board tools or automatic execution. Existing webhook/feed infrastructure remains independent. Hooks require separate authorization.
- GitHub App/OAuth/Enterprise and Azure Server/Entra ID are not implemented. Runtime permission/access diagnosis occurs through provider failures; discovery alone is not proof the token can write or create labels.
- Labels are individual nontransactional GitHub API calls. Concurrent provider-side edits can still race; no atomicity/lost-update guarantee is made. A failed label step can leave a partial mapping conflict for explicit refresh/recovery.
- A receipt whose item is absent/inaccessible in the complete source scope stays unresolved rather than treating absence as proof of success/failure. Restore scope/access and reconcile before changing its mapping or disconnecting. Scope transfers/renames requiring authenticated redirects are diagnosed rather than followed; moved identities do not create duplicate cards.
- A refresh is a scan of a changing external system, not a transactional provider snapshot. Repeat Refresh after concurrent external changes. Scans exceeding the deadline or response bound remain incomplete/stale, not silently truncated. No automatic retry is performed, including after rate limits.
- During refresh/move, connection writes are serialized and configuration saves are blocked until existing work finishes. Reorder/remove phases and manual card ranking are not implemented; phase order is creation order. Provider title/body editing remains in the provider.
- The engine's existing JSON mutation limit (512 KiB), response bounds (16 MiB per external response) and journal limits remain technical bounds, not a business phase-count cap.
- PATs are protected with user-bound Windows DPAPI and board-specific entropy, in a separate namespace. Non-Windows credential protection fails explicitly. DTOs, harness inputs and logs never receive the credential.
- Public API network/CORS policy is unchanged: DPAPI protects storage, not transit or access to administration. Configure credentials over a trusted/local connection or appropriate HTTPS. Do not expose `/api/activity-board/*` through an ingress-only webhook proxy.
- Back up the complete engine data directory before changing installed binaries. Older strict loaders do not understand `activityBoard*`; downgrade requires a compatible backup, not deleting these fields or reviving abandoned Tasks.

## Verification and human acceptance

Lightweight compilation was run successfully:

- `go build -tags dev -o C:\Users\Samuel\AppData\Local\Temp\opencode\klm-board-check.exe .` in `engine`.
- `npx tsc -b` in `clients/desktop`.

No full suite, integration/unit tests, adversarial review, installer build, real provider request or provider mutation was performed. Compilation does not establish runtime acceptance.

The follow-up Azure project/type picker change used the same Go dev build and
TypeScript checks and was subsequently packaged in the 0.5.2 installers. Real-account
project permissions, more than 100 accessible projects, custom types, empty catalogs
and the desktop/mobile picker interactions still need human validation.

The Tasks naming/icon/project-settings follow-up changes the frontend only; its
lightweight check is `npx tsc -b`. Human validation remains pending for both entry
points, the Tasks section below Webhooks, project-scoped connection management,
Azure selection and immediate button visibility after connecting/disconnecting the
last provider, including projects with no sessions. Existing 0.5.2 installers predate
this UI follow-up; no new installer generation was requested for it.

The Azure PAT-owner assignment follow-up was changed from an optional toggle to
a mandatory rule at the user's request. Its lightweight checks are a Go dev build
and TypeScript check. Manual validation should cover tasks assigned to the PAT
owner, another user and nobody; multiple WIQL pages; automatic startup migration
of existing unfiltered connections without saving; ignored legacy false inputs;
replacing the PAT with another user's token; failed refresh without unfiltered
fallback; and concurrent reassignment before a move. No real provider request/
mutation, broad test suite or new installer generation is part of this follow-up.

The sprint picker and default-current follow-ups use Go dev builds and TypeScript
checks. Manual validation remains pending for default-team current discovery,
Current marker/preselection including zero assigned tasks, no/multiple current
sprints, lookup failures and manual recovery, keeping manual All/past choices on
refresh, project changes, All/specific/No sprint, matching names in different
organizations/projects, duplicate type connections, filtered pagination, legacy
cache refresh, rapid filter changes and desktop/mobile layout. No real provider
call, test suite or new installers were run for these follow-ups.

The requested two static native-subagent review/correction rounds are recorded in
`ACTIVITY_BOARD_REVIEW.md`. Their five initial findings and the remaining second-
round Refresh hide/restore case were corrected. They do not imply runtime/provider
acceptance or a third review of the last correction.

Human validation still needs a disposable GitHub repository (preferably two connections) and Azure organization/project/type, with scoped credentials entered in the client. Use the manual route in the plan: complete coverage and stale-revision rejection; both board entries and project filter; labels/open+closed/PR exclusion; label creation and unrelated-label preservation; Azure multiple states, outbound priority and revision/rule rejection; pagination beyond 100 items; partial scans, credentials/offline recovery, uncertain writes/restart and removed/restored projects; desktop/Focus/Flutter, light/dark, keyboard/touch and draft/session continuity.
