# KLM Engine

Small standalone Go HTTP engine for real projects, sessions, and delegated harness
execution. Go 1.24.2, with `go-gitignore` for project path discovery. Includes a private linked-agent MCP
bridge; no frontend or general-purpose engine MCP interface is bundled.

## Run

From this directory, in PowerShell, build a persistent executable before starting:

```powershell
go build -o klm.exe .
.\klm.exe start
.\klm.exe stop
```

The public CLI has only `klm start` and `klm stop`. Start launches a detached worker,
waits for readiness and returns; repeated start keeps the same engine. Closing the
terminal does not stop it. Stop uses a per-user Windows named pipe, waits for the
live pipe server's Windows process handle and data lock to be released after shutdown,
and is idempotent when stopped.
It never kills a process from a stored PID or exposes engine shutdown on HTTP.
`__worker` is an internal launch mode, not an additional public command.

- API listen address is fixed at `0.0.0.0:7331`. Port conflicts are errors.
- Data stays at `%APPDATA%\klm\engine`; `engine.log` captures background output.
- The same exclusive data-directory lock protects existing data and live instances.
- Shutdown cancels active harness work and records interruption without run replay.
- Packaged binaries resolve adjacent `prompts/` before development source paths.
  Pi's extension and OpenCode's graph plugin are embedded in the Go binary.
- The separate NSIS installer configures user PATH and HKCU Run login startup,
  starts the engine and preserves user data on uninstall. It does not install
  harnesses. See [Windows packaging](../distribution/windows/README.md).
- The Tauri client has its own installer and lifecycle; the engine serves no UI.

## HTTP Contract
Development builds use `go build -tags dev -o klm-dev.exe .`, then
`.\klm-dev.exe start` / `.\klm-dev.exe stop`. This selects API **17331**, Focus
origin port **17332**, and `%APPDATA%\klm\engine-dev` for data, logs, locks and
the derived local control channel. Release builds omit the tag and retain
**7331/7332** and `%APPDATA%\klm\engine`. Both can run concurrently.

For a fresh isolated development runtime, set `KLM_DEV_DATA_DIR` to an absolute
directory before running the dev binary's `start` or `stop`. This changes only the
engine's data, workspace, locks and derived control channel; harness account/config
directories remain unchanged. The override is ignored by release builds. API and
web ports remain fixed, so only one development engine can listen at a time.


JSON mutation requests, including stop and directory picker, require
`Content-Type: application/json`. Stop and picker accept an empty body or `{}`.
Other JSON mutation bodies are a single object; unknown fields are rejected.
Transcription uploads use multipart; webhook deletion requires no body or content
type. Signed GitHub ingress has its own raw JSON validation described below.
Errors are non-2xx JSON objects: `{"error":"message"}`.

| Method | Path | Body / response |
| --- | --- | --- |
| GET | `/api/health` | `{status:"ok",version:2,runningSessions:number,activeGraphRuns:number}`; 503 on persistence failure |
| GET | `/api/state` | `{projects:Project[],sessions:Session[],harnesses:Harness[]}` |
| POST | `/api/projects` | `{name,folder,icon}` -> 201 Project |
| PATCH | `/api/projects/{id}` | `{name?,icon?}` -> 200 Project |
| DELETE | `/api/projects/{id}` | `{removed:true}`; hides the project, preserving its files and saved history; 409 while a session is running |
| POST | `/api/projects/{id}/folders` | `{name}` -> 201 Project |
| PATCH | `/api/projects/{id}/folders` | `{name,archived}` -> 200 Project |
| GET | `/api/projects/{id}/paths?q=...` | `{paths:[{path,kind:"file"\|"directory"}],partial:boolean}`; project-relative paths |
| POST | `/api/sessions` | `{projectId,title,workspace,harness,model?}` -> 201 Session |
| POST | `/api/sessions/{id}/side` | `{harness?}` -> existing or newly created side Session; one per main, serialized atomically |
| POST | `/api/general-session` | `{}` -> existing or newly created singleton general SessionUpdate; atomic and independent of user projects |
| PATCH | `/api/sessions/{id}/harness` | `{harness}` -> side/general SessionUpdate; general harness permanently locks at first accepted message |
| POST | `/api/sessions/{id}/consultations/{requestID}/cancel` | `{}` -> Session; cancels the correlated request/continuation |
| PATCH | `/api/sessions/{id}` | `{title?,workspace?,archived?}` -> 200 SessionUpdate |
| GET | `/api/sessions/{id}/models` | Harness model catalog; `?refresh=true` bypasses the two-minute cache |
| PATCH | `/api/sessions/{id}/settings` | `{model:string,effort:string}` -> Session; empty strings select harness defaults |
| GET | `/api/sessions/{id}/quota` | `{quota:QuotaSnapshot\|null}`; null for unsupported/unverified connections |
| POST | `/api/sessions/{id}/messages` | `{text,mentions?:[{id,path,kind,start,end}],sources?:[{sessionId,messageId,passage}]}` -> 202 Session, already containing the durable user event |
| GET | `/api/sessions/{id}/events` | SSE, initial and subsequent full Session JSON snapshots |
| PATCH | `/api/sessions/{id}/events/{eventID}` | `{favorite}` -> 200 Event; completed assistant messages only |
| GET | `/api/sessions/{id}/graph` | Conversation selection, active-run projection and node requests |
| PATCH | `/api/sessions/{id}/graph` | `{selectedGraphId:string}`; empty string selects None |
| GET | `/api/graph-runs/{runId}` | Real run summary and terminal result; 404 for unknown runs |
| POST | `/api/sessions/{id}/stop` | 200 Session after cancellation completes; idle sessions are unchanged |
| POST | `/api/sessions/{id}/permissions/{permissionID}` | `{decision:"once"\|"session"\|"always"\|"reject"}` -> Session |
| POST | `/api/sessions/{id}/questions/{questionID}/reply` | `{answers:string[][]}` or `{cancelled:true}` -> Session |
| POST | `/api/dialogs/directory` | `{path:string\|null}` |

Adding the same directory again restores a removed project's identity, folders,
and session history with the name and icon chosen in the add dialog.

### Project GitHub webhooks

In the client, open **Project settings** from the sessions sidebar. **General**
edits the name/icon, displays the fixed directory and confirms project removal;
**Webhooks** manages multiple independent GitHub connections for that project.
The rail's Edit/Remove menu and Add project remain separate entry points. Global
Settings has no Webhooks section. List rows show connection name and retention,
with an enable/disable toggle;
Load more fetches another page. Each row shows time since the last accepted event,
or Never; the visible list updates every 30 seconds without a Refresh button,
preserving the loaded page size and pausing during detail/edit views and mutations.
Clicking a row opens
read-only details with Copy URL, Last received, Verified/Not verified and confirmed
removal. Edit switches to the form, including enabled state and credential changes;
Cancel returns to read mode. New connections also open in read mode after creation.
The KLM Agent Activity timeline projects released, retained GitHub deliveries.
The connection editor has no raw-payload history screen or automation consumer.

#### Manual GitHub setup

1. Choose **Add → GitHub**, enter a name (1–60 characters) and an explicit
   retention. The form generates a random 256-bit secret using Web Crypto; the
   reload icon inside its input generates another. You can also edit it. The secret must
   contain 16–512 UTF-8 bytes without control characters; it is not trimmed or
   normalized. **Copy secret** copies only the value currently entered. Store it
   separately before saving: the form discards it on save/close, and the API only
   returns `secretConfigured`, never the stored secret.
2. **Copy URL** combines the configured `ENGINE_URL` with the connection's stable
   `endpointPath`. Locally this is
   `http://localhost:7331/api/webhooks/github/WEBHOOK_ID`, or
   `http://localhost:17331/api/webhooks/github/WEBHOOK_ID` for a dev engine. Replace
   the placeholder with the actual ID. Ports 7332/17332 serve the frontend, not
   webhook ingress. A configured remote base retains its scheme, port and prefix;
   the URL is not derived from the browser origin or inbound Host header.
3. In the GitHub repository or organization settings, manually add a webhook with
   the reachable **Payload URL**, **Content type: application/json**, the same
   **Secret**, the desired events and **Active** enabled. Localhost on your engine
   computer is not reachable by GitHub. Set up your own HTTPS ingress proxy/tunnel
   and use its public URL with the same receiving path in GitHub. KLM neither
   provisions that infrastructure nor registers GitHub webhooks; no public URL
   field, GitHub App/OAuth or PAT is part of this feature.
4. After GitHub sends a signed `ping`, return to the Webhooks list and reopen the
   connection. **Verified** shows the
   durable `lastPingAt`; **Last received** shows the latest durably accepted event.
   Configuration creation alone does not verify receipt. These timestamps are
   historical, not a connectivity monitor; list updates query accepted receipt
   metadata rather than testing network connectivity. After changing a secret in
   KLM and GitHub, send a new ping delivery to
   verify the replacement. Redelivering an already retained ping is a duplicate
   and does not update verification.
5. If a delivery fails or its response is uncertain, inspect GitHub's Recent
   deliveries and **Redeliver** manually after restoring availability. GitHub does
   not automatically redeliver failed webhooks. Within retention, retrying the same
   delivery ID recovers the existing receipt without inserting another event.

For example, an ingress-only HTTPS proxy should use this exact-path mapping for
each connection (placeholders only):

```text
POST https://hooks.example.com/api/webhooks/github/WEBHOOK_ID
  -> http://127.0.0.1:7331/api/webhooks/github/WEBHOOK_ID
All other public paths and methods: reject; do not forward.
```

Use 17331 as the upstream port for development. Preserve the original body and
`X-Hub-Signature-256`, `X-GitHub-Delivery`, `X-GitHub-Event` and Content-Type headers;
do not parse/reserialize or compress the JSON. This mapping exposes no project,
session or other administration route. HMAC protects ingress only, not the engine's
existing unauthenticated personal-use administrative API. The private harness
bridge stays authenticated and loopback-only.

#### Reception, storage and release

`POST /api/webhooks/github/{webhookId}` requires `application/json` (media-type
parameters allowed) and no compression other than identity. Required single headers:

- `X-Hub-Signature-256`: `sha256=` plus 64 hexadecimal digits, HMAC-SHA256 of the
  exact raw body using this connection's secret, compared with `hmac.Equal`.
- `X-GitHub-Delivery`: 1–128 ASCII characters without spaces/control characters.
- `X-GitHub-Event`: 1–100 ASCII token characters; event types are not allowlisted.

After signature verification, the body must be a valid UTF-8 JSON object. `action`
may be absent or a string of at most 100 characters. The engine retains the exact
original UTF-8 JSON as an immutable string, plus delivery ID, event type and action,
without normalizing whitespace or numbers. The signature authenticates the body,
not the event/delivery headers; no signature, cookies or arbitrary header dump is
stored. Payload is untrusted data, not an instruction to execute.

Event, pending entry, receipt sequence and connection timestamps commit together
through the existing journal's durable Sync before a new delivery returns
`202 {"accepted":true,"duplicate":false,"eventId":"..."}`. A retained, unexpired
duplicate of `(webhookId,deliveryId)` returns
`200 {"accepted":true,"duplicate":true,"eventId":"..."}` without changing TTL,
timestamps, verification, sequence or queue. The same delivery ID on another
connection is independent; after expiry/deletion it can be accepted as a new event.
An unavailable/disabled connection is rejected before duplicate recovery.

The worker starts with the engine, wakes on acceptance and checks pending storage
every second. It releases eligible entries in receipt order, up to 100 per
transaction, atomically removing pending references and adding release records.
Release assigns a separate global `releaseSequence` and `emittedAt`; the raw event
remains retained. `status: "emitted"` means durably available internally, not
delivered to a feed, session, agent or graph. No consumer ACK, action retry or
external automation runs. Wake signals and session/inventory SSE are not the
durable webhook log. After restart, pending entries resume without a client;
already committed releases keep their sequence/time. Storage failure does not
publish successful acceptance; recovery requires restarting the engine and manual
redelivery when appropriate.

Secrets and payloads live in the engine data directory, not the project repository
or client localStorage. Secrets are needed for HMAC and stored with the engine's
private records; encryption at rest is not promised. Existing data without webhook
fields loads as empty; validation runs at load, after journal replay and before
save/checkpoint. Downgrading to a binary that rejects these fields is not supported.

#### Retention and lifecycle

Retention is explicitly selected on creation; there is no assumed default. UTC
`receivedAt` is captured when body reading completes, before commit, and represents
engine receipt rather than GitHub occurrence or worker emission. `expiresAt` is
computed once from that receipt and stored with each event:

| API value | UI label | From receivedAt (UTC) |
| --- | --- | --- |
| `1d` | 1 day | 24 hours |
| `3d` | 3 days | 72 hours |
| `1w` | 1 week | 168 hours |
| `1mo` | 1 month | 1 calendar month |
| `3mo` | 3 months | 3 calendar months |
| `6mo` | 6 months | 6 calendar months |
| `1y` | 1 year | 1 calendar year |
| `forever` | Indefinitely | No expiresAt |

Calendar arithmetic preserves the time of day and clamps to the destination
month's last valid day: January 31 plus one month becomes February 28/29;
February 29, 2024 plus one year becomes February 28, 2025. Editing retention affects
only newly accepted events; duplicates and release do not recalculate expiry.
`expiresAt <= now` is unavailable to reads/release even before cleanup. Cleanup
removes expired events, pending/release records and deduplication indexes at startup
before HTTP, then every minute in batches of 100, including removed projects.

- **Disabled connection:** new deliveries return 409, but accepted pending entries
  continue releasing. Re-enable uses the same endpoint and secret.
- **Change secret:** omission keeps the current secret; empty/null is invalid.
  A replacement clears `lastPingAt`, has no dual-key grace period and requires the
  matching GitHub update. A request racing a secret change returns 409 rather than
  committing with the old key.
- **Remove webhook:** the UI requires **Remove webhook?** confirmation. A single
  transaction deletes the connection and all its events, pending entries and
  releases; the old URL returns 404. It does not remove the GitHub-side registration.
- **Removed project:** logical project removal retains identity/history and webhook
  configuration/events, rejects new ingress and webhook management/reads with 404,
  pauses pending releases and continues TTL. Other projects continue releasing.
  Existing active-session/graph removal conflicts remain. Adding the same directory
  again restores the ProjectID, connection IDs, URLs, secrets and enabled flags;
  the worker resumes only still-valid pending entries.

Deletion is logical and immediately removes API access. Physical space is reclaimed
by the existing healthy checkpoint/rotation cycle (five minutes); older journal or
checkpoint bytes may persist until then. Secure erasure of bytes/backups is not
promised.

#### Webhook API and cursors

Project IDs and connection/event IDs are opaque and should be URL-encoded. Every
management/read route requires an active project and checks actual record ownership.
Creation starts enabled; PATCH requires at least one supported field and cannot
change ID, project or provider. Manager JSON decoding is strict.

| Method | Path | Body / response |
| --- | --- | --- |
| GET | `/api/projects/{id}/webhooks` | `limit`, `cursor` -> `{items:WebhookConnection[],nextCursor?,hasMore}` |
| POST | `/api/projects/{id}/webhooks` | `{provider:"github",name,secret,retention}` -> 201 WebhookConnection |
| PATCH | `/api/projects/{id}/webhooks/{webhookId}` | `{name?,enabled?,retention?,secret?}` -> 200 WebhookConnection |
| DELETE | `/api/projects/{id}/webhooks/{webhookId}` | No body -> 200 `{removed:true}`; 404 if absent |
| GET | `/api/projects/{id}/webhooks/{webhookId}/events` | `limit`, `cursor` -> `{items:WebhookEventMetadata[],nextCursor?,hasMore}` |
| GET | `/api/projects/{id}/webhooks/{webhookId}/events/{eventId}` | `{event:WebhookEventMetadata,payload:string}`; 404 if expired/deleted or outside scope |
| GET | `/api/projects/{id}/webhook-events` | `after`, `limit` -> `{items:WebhookEventMetadata[],nextAfter:string,highWatermark:string,hasMore}`; released events only |
| POST | `/api/webhooks/github/{webhookId}` | Original signed body/headers -> 202 new receipt or 200 duplicate |

Connection DTOs include `id`, `projectId`, `provider`, `name`, `enabled`, `retention`,
`secretConfigured`, `endpointPath`, `createdAt`, `updatedAt` and optional
`lastReceivedAt`/`lastPingAt`. Metadata includes event/connection/project identity,
`deliveryId`, `eventType`, optional `action`, `isPing`, `receivedAt`, optional
`expiresAt`, `receivedSequence`, `status` (`pending` or `emitted`), optional
`emittedAt`/`releaseSequence` and `payloadBytes`. Times are UTC RFC3339Nano; optional
fields are omitted, and empty pages contain `[]`. List pages contain no payload.

Pages default to 50 items; `limit` must be 1–100. Connection pages sort by creation
time/ID ascending; event pages sort by received sequence descending. Their returned
`nextCursor` is an opaque, versioned, scoped string (maximum 1 KiB), not an offset.
Use it unchanged for the same project/connection.

For releases, start with `after=0`, then pass the returned `nextAfter`. Receipt and
release sequences, `after`, `nextAfter` and `highWatermark` are **decimal strings**;
do not convert them to JavaScript numbers. Release order is ascending and `after`
is exclusive. Each response captures the global release high watermark; if more
retained project results remain, nextAfter is the last returned sequence. At the
end, nextAfter equals highWatermark, even for an empty page. A future release of an
older pending event gets a higher release sequence and remains discoverable.
Invalid cursors or after beyond the current counter return 400.

Reads neither consume events nor update a consumer position. Separate consumers
can retain their own nextAfter independently; KLM has no consumer-progress table
yet. Counters survive restart and do not reset on expiry/deletion. Cursors remain
usable across deleted/expired rows, but gaps are normal: an overdue consumer may
lose events to TTL, with no complete-history or expired-payload recovery guarantee.
After an uncertain management mutation, Refresh before retrying; creation is not
automatically retried after a transport failure.

#### GitHub Activity projection

The KLM Agent timeline reads releases directly, without a second event store,
journal, ACK or durable consumer cursor. It never starts an agent or graph.

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/api/activity/github/catalog` | `{version:1,items:[{id,label,defaultEnabled,category}]}`; 56 initial options, all enabled |
| GET | `/api/activity/github` | `limit=1..100` (default 50), `cursor`, `category`, `q` (max 200 chars), repeated `exclude` catalog IDs -> `{items,nextCursor?,hasMore,highWatermark,asOf}` |
| GET | `/api/activity/github/{eventId}` | `{id,fields:[{label,value}],body?,entries?:[{text,url?}],truncated}`; 404 if unavailable |

Categories are `PRs`, `Pipelines`, `Issues`, `Security`, `Repository`, `Other`;
absent/`All` means all. Unknown categories/exclusions or invalid cursors return 400.
Search is case-insensitive substring matching on displayed summary fields, not raw
payload full-text. Filters apply **before** pagination. Unknown event types remain
visible in Other, even if all known types are excluded. Ping is never feed activity.

Items include project/connection/event identity, original eventType/action, catalogId
when known, GitHub provider, category/tone/icon, actionLabel/summary/title/reference,
optional branches/result/actor/repository and safe targetUrl/targetKind. Sequence,
receipt/release times and optional expiry come from retained engine records. Sender
does not imply agent authorship. The project owner/name comes from engine inventory.

Order is **descending releaseSequence**. The opaque, strict, versioned base64url
cursor binds the canonical filters, captured high watermark and exclusive lower
sequence. All sequences are decimal strings, never JavaScript numbers. New releases
appear on refresh; pagination stays under its original watermark. TTL/deletion may
create gaps. Each query rechecks active projects, extant connections and unexpired
releases; disabled connections retain history, removed projects hide it until restored.
Concurrent deletion can finish an already captured read, as with existing webhook APIs.

Parsing runs outside `app.mu`, using immutable payload references. A per-engine
volatile cache holds compact normalized summaries only (10,000 entries/64 MiB limit),
pruned against eligible records on reads; it never authorizes visibility or stores raw
payloads. Project names are applied outside this cache. Summary text/URLs are bounded,
and compact Details has a 64 KiB serialized ceiling with explicit truncation. Body
text is literal, allowlisted by event family; secret-scanning secret values, deploy
key material and hook configuration are excluded. URLs must be absolute HTTP(S)
without userinfo/controls; missing resource links fall back to repository pages,
otherwise references are nonclickable. No GitHub enrichment request is performed.

The 56 preferences correspond to the supplied GitHub screenshot. There are **55
confirmed canonical headers** plus stable catalog ID `push_ruleset_bypass`, matched
for unknown headers through `exemption_request.request_type=push_ruleset_bypass`.
This deliberately does not assert a canonical header that has not been verified.
See `../GITHUB_ACTIVITY_FEED_IMPLEMENTATION_PLAN.md` for sources and availability.

Feed settings uses localStorage
`klm.agent.github-feed.preferences.v1:${encodeURIComponent(ENGINE_URL)}` with
`{version:1,overrides:{catalogId:boolean}}`. This is display configuration per client
origin/engine URL, independent of webhook subscriptions, retention and other readers.
Explicit overrides survive future default changes. Storage failures are reported.
Bookmarks remain in memory and never retain expired content.

The visible client refreshes every 15 seconds and on focus/visibility return. It
reloads all pages through the previous loaded lower boundary, then swaps atomically;
it never appends an old cursor to a new top. Load more is serialized with refresh,
query changes cancel stale requests, and errors preserve the last complete snapshot.
Refresh preserves the visible reading anchor in desktop and compact scroll containers.
New releases arriving below the top show a floating arrow/project-icon pill;
clicking it returns to the top, and scrolling to the top manually also dismisses it.
Project icons are deduplicated, with at most three displayed plus an overflow count.
Initial loads, Load more and filter changes do not count as new arrivals; expired or
removed events are excluded from the notice. Browser scroll anchoring is disabled
in these containers so it does not compete with the explicit reading anchor.
The client hides expired items using the engine clock offset and requests Details
only when expanded. Internal agent posts/Needs attention remain future work.

Standalone simulator: `node scripts/github-webhooks/simulate.mjs --list` and
`--all --dry-run` from the repository root; see
[`scripts/github-webhooks/README.md`](../scripts/github-webhooks/README.md).
It signs exact bytes, supports external payloads, duplicate/invalid-signature modes,
and has **124 partial synthetic scenarios covering all 56 catalog IDs**. Push ruleset
bypass explicitly uses `klm_simulated_push_ruleset_bypass` until a real header is known.

Implementation checks (2026-10-13): `go build -tags dev ./...`, `npx tsc -b`,
simulator list/dry-run passed. A disposable overlay build on port 27331 (new data,
two synthetic projects/connections) exercised all 124 scenarios, 202/200/401 behavior,
ping exclusion, all catalog classifications, compact Details, PR/CI/comment semantics,
descending pagination/watermark, category/search/exclusion, safe URL fallback,
large JSON numbers, cross-connection delivery independence and restart retention
of 128 visible occurrences. Targeted Playwright checks then covered 50/100/128-card
pagination, provider/copy/Details/links, bookmarks across filters, all 56 settings,
preference persistence after reload, search and empty states, and 1440/900/390px
layouts. Adding 105 signed deliveries refreshed the mounted feed to 233 unique
cards while preserving its reading anchor; deleting that disposable connection
returned it to 128. A simulated refresh error kept the loaded cards and Retry
recovered. PR readiness copy and synthetic closed-PR states were corrected and
checked; the final Go overlay build and targeted browser assertions passed.
Browser validation used a separate Playwright context, the existing Vite assets,
and request redirection only inside that context to the disposable API. No real
engine was restarted. Human UI acceptance, external GitHub delivery, elapsed TTL,
and remaining lifecycle cases are still pending. No full suites or versioned
integration tests were run.

#### Ingress limits and errors

| Resource | Limit / response |
| --- | --- |
| Individual raw payload | 25 MiB; 413, including oversized chunked bodies |
| Concurrent body read/hash/parse | 2 per engine; extra ingress returns 503 |
| Connections | 100 per project, 1,000 per engine including removed projects; creation returns 409 at capacity |
| Stored events/raw payloads | 10,000 events or 256 MiB per engine; new ingress returns 503 at capacity |
| Pages | Default 50, maximum 100; invalid limit/cursor returns 400 |
| Worker/expiry cleanup | Up to 100 events per transaction |
| HTTP headers/timeouts | 16 KiB headers; 5s read-header, 15s read, 20s write, 60s idle |

Ping, pending and released events, including removed projects, count toward global
event/byte limits; expired rows occupy capacity until cleanup. Indefinitely is
still bounded by quotas. No unexpired event is silently evicted. A retained
duplicate passes the event/byte quota check without allocating space, but still
requires an available/enabled connection, a valid signature and an ingress slot.
Capacity/ingress-slot responses include `Retry-After: 5`; this does not schedule a
GitHub retry. The engine holds state in memory and checkpoints it completely.

Ingress errors use `{"error":"English message"}`: 400 invalid headers/JSON/UTF-8,
401 signature, 404 absent/removed connection or project, 409 disabled/racing secret
change, 413 oversized body, 415 content type/encoding, and 503 capacity/shutdown/
storage failure. A timeout after durable acceptance may leave the sender uncertain;
manual redelivery with the same ID handles this while the event is retained.

#### Human acceptance (pending)

Backend/frontend owners reported one successful `go build -tags dev ./...` in
`engine` and one successful `npx tsc -b` in `clients/desktop`. Documentation was
checked against current routes, DTOs, wrappers and component wiring. A short local
API smoke used a dev executable and isolated disposable `KLM_DEV_DATA_DIR`: creation
without secret exposure, rejection of a bad signature, signed ping acceptance,
deduplication, release cursor, exact payload and ping verification passed. After
stop/start, payload, release sequence/time and deduplication were preserved. The
temporary engine was stopped afterwards. No full suite or UI/real GitHub validation
was run; pending recovery, expiry, quotas and full lifecycle remain human checks.

Use the dev engine with disposable data and the detailed checklist in
[`GITHUB_WEBHOOKS_IMPLEMENTATION_PLAN.md`, section 10](../GITHUB_WEBHOOKS_IMPLEMENTATION_PLAN.md#10-aceitação-e-validação-humana-preparada).
The primary manual route is:

1. Open General/Webhooks from the sidebar; save name/icon and confirm Add project
   and rail editing still work. Create two connections in one project and one in
   another, with distinct secrets and URLs. Confirm global Settings has no Webhooks.
2. Send a signed ping, Refresh and observe Verified; send a signed push and inspect
   API metadata/raw payload. For local deliveries, HMAC exactly the UTF-8 bytes sent
   and include all three required headers. Repeat the delivery ID: expect the same
   eventId/TTL and one stored event; repeat it on a different connection: expect a
   separate event. Do not commit real secrets or payload credentials.
3. Read released events with after=0 then nextAfter; repeat independently to confirm
   reads do not consume them. Restart and confirm retained releases keep their
   sequence/time. Pending-at-restart recovery needs the controlled disposable-data
   scenario in the plan; the normal worker may release too quickly to observe it.
4. Change retention and compare old/new expiresAt; exercise calendar month/year
   boundaries and expiry as specified in the plan, including pending expiry. There
   is no public clock override; do not modify the live engine's state/journal.
5. Disable/re-enable, rotate the secret and send a new ping, then confirm removal
   deletes stored events and makes the old URL unavailable. Separately remove/
   restore a project: same identity/configuration, pending paused while removed,
   TTL still active and only valid pending entries resumed. Pending-state scenarios
   require controlled disposable data rather than assuming a visible queue delay.
6. On desktop, browser and compact WebView, check layout/scrolling, Tab/Shift+Tab,
   keyboard activation, Escape/close and focus return, confirmation Cancel/Remove,
   clipboard fallback, loading/errors, Refresh, pagination and late responses after
   closing/changing project. Check release/dev/configured URL bases and real GitHub
   delivery through the user's HTTPS mapping. No received event should populate the
   fixture timeline or start a session, agent or graph.

### General conversation

`POST /api/general-session` serializes get-or-create with the same durable state
transaction gate as ordinary sessions. It persists a session with role
`general_agent`, an empty `projectId`, and an absolute `executionCwd` pointing to
`workspaces/general-agent` under the engine data directory. No synthetic user
project is created. The general conversation and its native child sessions are
excluded from `/api/state` project inventory; observing their IDs through
`/api/updates` uses normal summaries and bounded history deltas.

`harnessLocked` is durable metadata, set atomically with message acceptance and
projected independently of paginated history. Removing queued input does not
clear it. Ordinary create, rename, move, reorder, archive, side-agent and graph
routes cannot repurpose this conversation. Model discovery/settings and queue
execution resolve its engine-managed directory without requiring a project.

The internal `general-agent.md` prompt is reread on each turn and prepended through
the shared Pi/OpenCode/Codex execution path. Packaging already copies
`engine/prompts/` and installs `prompts/*.md`, including this file. The private
loopback bridge exposes a dedicated general-agent catalog, authenticated by the
owned adapter/turn, without assigning a synthetic project. Normal tool
permissions/questions/Stop are retained; remembered permissions are session/global
only. This role does not receive implicit-project `linked_*`, `session_spawn` or
graph-execution tools. Normal main/side scope is not widened.

### General-agent discovery and control (private harness bridge)

| Tool | Scope and behavior |
| --- | --- |
| `project_list` | Paginated nonremoved projects, registered directories and visual/archived folders. |
| `project_add` | `{name,path}` on the user's registration request; absolute existing engine-computer directory. Returns `{projectId,name,path,outcome}` with `created`, `existing` or `restored`; conflicts identify matching legacy IDs. No session or file execution. |
| `session_list` | Top-level chats by default; project/folder/title/state filters; explicit `parentId` lists side chats. |
| `session_get` | Identity, visual folder vs effective cwd, configured/resolved harness settings, raw/working/runtime state, queue counts, usage, full pending questions/permissions and projected graph requests. |
| `session_messages_list` / `session_messages_search` | Chronological bounded messages or literal case-insensitive excerpts; exact `messageId`/rune `offset` reads long content. Agent origins are retained. |
| `session_events_read` | Chronological harness activity, including partial events, or changes after `sinceRevision`. No subscription. |
| `session_models_list` / `project_graphs_list` | Real destination catalog/defaults, or selectable graph IDs with enabled/available flags and catalog errors. |
| `session_send` | Visible normal execution through the shared persistent FIFO queue; explicit destination, self-contained instruction and sender-scoped idempotency key. |
| `session_ask` | Information/clarification in the destination's actual conversation, using existing correlated answer/continuation semantics. |
| `session_rename` / `session_move` | Existing title and active visual-folder validation; no filesystem move or cwd change. |
| `session_settings_update` | Optional fields, preserved when omitted; model/effort validate atomically between turns. YOLO alone can change during work and resolves pending permissions on enable. Native Codex full-access turns cannot downgrade until they end. |
| `session_graph_select` | Available main-chat graph or empty ID for None; does not invoke a graph. |
| `session_question_answer` | Exact active native request, including graph requests routed through their owning chat; existing choices, cancellation, custom/multiple answers, resolving conflicts and private-answer masking. |
| `session_create_options` | Explicit `projectId`, optional harness and pagination; installed harnesses, active folders, connected model/effort defaults and recent real general-chat human message IDs. |
| `session_create` | `{projectId,title,prompt,operationId,sourceUserEventId,harness?,model?,effort?,folder?,yolo?}`; normal independent top-level child in the registered project directory; durable spawn receipt, not completion. |
| `session_stop` | Chat cancellation, owned native process termination and unsent-queue pause; waits up to 20 seconds, leaves independent `activeGraph` observable. |
| `session_queue_remove` / `session_queue_send` | Exact `messageId`; existing removal or Send now, with effective item ID and bounded queue state. Sending conflicts. Uncertain Send now requires `retryUncertain:true` after an explicit user recovery decision and can duplicate delivery. |
| `session_archive` / `session_restore` | Normal top-level metadata only; preserves runtime/requests. Restore accepts optional active `folder` or Ungrouped and requires it when the current folder is archived. |
| `session_permission_reply` | `{sessionId,permissionId,decision,sourceUserEventId?}`; exact currently pending request through its owner, including projected graph requests; offered decision only, native rollback and related-request resolution. |

All destinations resolve by stable ID in nonremoved projects. Archived sessions
are readable; orders/consultations/Send now require effective restoration. Stop,
queue removal and pending-request replies remain available for recovery.
Graph nodes and native subagents are not independent control/command destinations.
Global reads/controls are exclusive to the general role; an argument cannot grant
it. Replies to general consultations use its own execution directory and prompt.
Only the general agent can initiate cross-project consultations, including after
checkpoint/journal replay; ordinary conversations still require the same project.

List pages contain up to 50 items; message/event pages up to 20. Serialized tool
content is capped at 64 KiB. Oversized records return UTF-8 JSON fragments with
`encoding`, `fragment`, rune `offset` and `nextCursor`; concatenate fragments for
that record before decoding. Use only returned cursors with unchanged filters.
Changed inventories/snapshots require restarting pagination. Activity in unrelated
chats does not invalidate normal history pages, which bind to the destination's
event version. Event delta cursors
retain the upper observed revision, delivering repeated updates to the same ID
without mixing later versions. Advance `consumedRevision` only after all delta
pages/fragments arrive. Expired revisions and reset records return `resetRequired`
with a bounded resynchronization page, not an empty success. Completed reset pages
can resume deltas from the returned `revision`. Large consultation results include
bounded answer/question excerpts plus source session/message IDs for full reading.
Usage input/output totals remain separate from context tokens/window; percentages
require both known values and a positive window. `observedAt` is the observation
time, not an invented harness measurement timestamp.

A compact timestamped/revisioned project snapshot accompanies each general turn,
including working/waiting and separate graph activity counts, with truncation after
30 projects. There are no background subscriptions, unsolicited completion/idle
notifications or polling loops. Correlated replies to an explicit ask remain.
Hermes remains outside this slice. Three-harness runtime and shared desktop/web/mobile behavior
remain human validation steps; automated fixtures never execute project agents.

Creation reuses `SessionSpawn` with an optional request `projectId` restricted to
the general sender, validated on checkpoint, commit and journal-replayed state.
Normal main/side spawn still cannot select another project. `sourceUserEventId`
must refer to a real `user` event in the general conversation, never an agent prompt;
it records traceability, not semantic proof of consent or harness permissions.
The child starts with attributed agent input. Same-key identical retries survive
queue consumption/restart; another project/settings on the key conflict. Catalog
validation occurs outside the engine lock followed by destination/settings/turn
revalidation before atomic child, queue payload and receipt acceptance. Rejected
requests reserve nothing. Folder omission uses Ungrouped. Same-harness settings
and current YOLO inherit under spawn rules; a changed harness uses its defaults.
Explicit YOLO overrides apply before the first turn and only at the user's request.

HTTP and these tools share the underlying Stop, queue, archive and permission
operations. General control events carry attributed origin. Permission results
identify exact decision and request/session/project/global lifetime; grants belong
to the native target, including graph nodes resolved only from the owner's current
projection. They never become human authorization. An uncertain native reply is
not proof of non-delivery. Creation/options, Stop and permission reply use the
existing long bridge budget (55s server, 60s clients; catalogs 45s), leaving ask's
special correlated-wait handling intact.

Manual validation is still needed across all three harnesses: singleton identity
through simultaneous clients/reloads/restarts, queue acceptance and irreversible
harness locking, model/effort changes, streaming/errors/Stop/permissions/questions,
empty-project operation, and unchanged project history/navigation.

#### Project registration by the general agent

`project_add` is exclusive to the general role's authenticated private bridge. It
uses internal helpers shared with `POST /api/projects`, not a loopback HTTP call.
Names use the existing trimmed 1-to-60-character validation; `path` must be an
absolute existing directory on the engine computer, distinct from a session's
visual `folder`. No source project or destination session is required, so the
first project can be registered directly. The prompt requires a user request and
clarification of unclear paths, never inference from the selected project,
web/mobile client or general workspace. Registration alone authorizes no sessions,
graphs or file operations; Data Sources are outside this feature.

Filesystem validation occurs outside the engine mutex, followed by active-turn,
context and general-identity revalidation under the lock. Matching and mutation
share that lock. Paths are compared after `filepath.Clean`, case-insensitively on
Windows; aliases, junctions and symlinks are not resolved for deduplication. One
active match returns its current name/path/ID unchanged, including its icon. One
removed match restores its ID and visual/archived folders with the requested name
and default icon; sessions and history are untouched. Multiple matches, including
active and removed records together, return a conflict listing IDs without mutation.
New projects use the existing default icon. `commitLocked` persists and notifies
client inventory streams; a failed commit returns an error rather than success.
The ID is immediately usable by `project_list` and `session_create_options`.
Retries for an active path use persisted project identity, not a new receipt table.
The manual HTTP contract and its existing restoration behavior are preserved.

Manual acceptance in a disposable environment (not performed during implementation):

- Register the first project for an existing directory; observe Project Rail,
  `project_list`, and `session_create_options` with the returned ID.
- Repeat registration, including after restart and with a different requested name:
  the active ID, name and icon stay unchanged, with no duplicate.
- Reject invalid names and relative, nonexistent or file paths without registration.
- Restore a removed project and confirm the same ID, sessions/history and
  active/archived visual folders.
- Submit two simultaneous registrations for the same directory: one creation and
  one existing result, both with the same ID. Confirm cleaned paths and Windows case
  differences match; multiple legacy registrations conflict and report their IDs.
- Only choose models/create a session when the user requests that separate action.
- Confirm normal main/side chats, graph nodes and subagents lack `project_add`,
  and manual registration still works.
- Confirm cancellation during directory validation prevents a stale-turn write,
  and a persistence failure returns an error without reporting registration success.

### Session collaboration (private harness bridge)

`session_spawn` accepts `{title,prompt,operationId,sourceUserEventId,harness?,model?,effort?,workspace?,yolo?}`.
It creates one top-level session in the calling session's project, persists a
pending first input and a receipt in the origin atomically, and schedules its
normal turn independently. The source reference must be an existing human `user`
event in that conversation; the engine records traceability, while the editable
prompt instructs the agent to create sessions only when explicitly requested.
Repeated identical calls with the same operation ID return the original receipt;
different arguments conflict. The receipt reports acceptance, not execution
success. Default harness/model/effort and visual folder come from the caller;
changing harness uses that harness's model defaults. Omitted `yolo` inherits the
sender's current mode at creation; `yolo:false` starts the requested non-YOLO child
with permissions enabled. Override only at the user's request. The receipt records
the resolved creation mode, which does not change on retry or later sender updates.
Local grants, selected graphs, native history and active turns are not copied. No automatic completion
notification is sent to the origin. Pending or uncertain deliveries remain
recoverable with the ordinary queue controls after interruption.

`session_spawn_options` is available to normal main/side agents in their own
project. It lists installed harnesses, paginated exact model IDs and efforts,
active visual folders, required spawn fields and five bounded recent real user
message IDs. OpenCode requires full `provider/model` IDs such as
`openai/gpt-6-luna`, not display labels or bare model names. `workspace` is a visual
folder name, not a path; `sourceUserEventId` is a message ID in the sender, not its
session ID. The agent guidance directs recoverable validation errors back into
corrected tool calls rather than ending the user's task.

Spawn syntax and real model/effort availability are validated before acceptance.
Catalog discovery occurs outside the state mutex, with sender/turn/project
revalidation before atomic creation. Rejection creates no child, queue item or
receipt and does not reserve the operation ID. Tool errors are JSON with
`accepted:false`, `code`, `field`, `message`, `hint` and optional `validValues`.
An invalid OpenCode model therefore returns to the requesting agent before a
child can fail with a Harness error. Correct rejected arguments and reuse the
operation ID; changing an already accepted request still conflicts.

`session_send` accepts `{sessionId,instruction,operationId,sourceUserEventId?}` in both catalogs.
Ordinary callers can address their linked main/side or same-project top-level chat;
the general agent can address admitted project main/side conversations globally.
`SessionCommand` receipts and the queue payload/origin are persisted atomically.
Identical retries return `{accepted,sessionId,messageId,operationId,delivery}`;
different content/destinations with the same sender operation ID conflict. Receipts
outlive queue consumption and no receipt is reused as session ownership or creation.
The optional source reference must identify a real human message in the sender;
it is traceability, not permission, and can be omitted for agent-assigned work.
Busy recipients queue FIFO, paused/uncertain queues are preserved, and interrupted
unconfirmed native delivery is not automatically replayed. Orders do not carry a
`consultationId`; UI/log/export distinguish `Instruction from …` from initial
prompts. `agent_prompt` is not a human `user` authorization event. Sending does not
wait, yield, subscribe or resume the sender. `linked_ask`/`session_ask` are for
information/clarification; use `session_send` for implementation/execution.

Storage adds optional `sessionCommands` and optional origin `kind` fields through
the ordinary transaction/journal/checkpoint path. Older data remains loadable;
downgrade to binaries rejecting new fields is not promised. Strict decoding and
receipt/consultation identity/scope validation remain enabled.

`session_discover` accepts `{query?,cursor?,limit?,includeArchived?}` and returns
bounded same-project top-level identities (`id`, title, project, workspace,
harness and archived flag). `linked_read` and `linked_ask` accept an optional
`sessionId` for an eligible top-level target; without it they retain their
main/side target. Reading an archived session requires an explicit ID and is
allowed; consulting one requires restoration. `linked_answer` uses the active
correlated request, regardless of whether the two sessions have a side link.
Consultations retain the four-pending-per-origin limit and ten-minute timeout;
the same question to different recipients is separate. No graph node or native
subagent receives these tools. Pi loads the same engine-supplied tool catalog as
OpenCode and Codex through its managed extension.

Agent behavior is editable in `engine/prompts/session-collaboration.md` during
development, or `prompts/session-collaboration.md` next to the installed engine.
`KLM_PROMPTS_DIR` overrides both. The Windows engine packaging copies all
`engine/prompts/*.md` into the installer's prompts directory. Changes take effect
on the next turn; missing or empty prompt files surface an error instead of
silently reverting to hardcoded guidance. Consultations from other agents are
attributed context, not new user messages or permission grants. Creation and
consultation still use the private authenticated loopback bridge.

```typescript
type Project = {
  id: string
  name: string
  folder: string
  icon: string
  folders: string[]
  archivedFolders: string[]
}

type Session = {
  id: string
  parentId?: string
  sources?: { sessionId: string; messageId: string; passage: string }[]
  projectId: string
  title: string
  workspace: string
  harness: 'pi' | 'opencode' | 'codex'
  model?: string
  effort?: string
  resolvedModel?: string
  resolvedEffort?: string
  status: 'idle' | 'running' | 'error'
  archived?: boolean
  events: Event[]
  permissions?: Permission[]
  questions?: QuestionRequest[]
  usage?: {
    inputTokens: number | null
    outputTokens: number | null
    context: { tokens: number | null; window: number | null } | null
  }
  createdAt: string
  updatedAt: string
}

type Event = {
  id: string
  consultationId?: string
  type: 'user' | 'agent_prompt' | 'session_spawn' | 'assistant' | 'reasoning' | 'command' | 'mcp' | 'tool' | 'error' | 'status' | 'consultation'
  text: string
  title?: string
  status?: string
  favorite?: boolean
  createdAt: string
  data?: Record<string, unknown>
}

type Harness = {
  id: 'pi' | 'opencode' | 'codex'
  name: string
  available: boolean
  error?: string
}
```

### File and folder mentions

Both composers use confirmed `@path` selections (`@path/` for directories).
`mentions` is optional for backward compatibility. Each occurrence has its own
opaque `id`, normalized slash-separated project-relative `path`, `kind` (`file`
or `directory`), and half-open `[start,end)` **UTF-16 code-unit offsets** into the
exact untrimmed `text`. The range must match the full token. Offsets are not UTF-8
bytes and are unrelated to Codex `text_elements`. Ordinary text is never scanned
to implicitly attach paths.

Discovery works before harness startup and during active turns. An empty query
lists root entries. Name/prefix, path, substring, then simple subsequence matches
are ranked, with at most 40 results. Traversal is bounded to 20,000 entries, 64
directory levels, and a cooperative 300 ms deadline per request (individual OS
I/O calls can take longer). Reaching a traversal or result limit sets `partial`.
Nested `.gitignore` rules are parsed through `go-gitignore`, without requiring Git
or a repository. Ignore files are limited to 128 KiB; unreadable/oversized rules
skip that subtree and mark discovery partial. `.git` metadata is excluded.
Contained links may be selected, but directory links are not traversed during
discovery. The client debounces queries and discards obsolete responses.

At submission the engine derives the root from the session's project, resolves
links/junctions, checks containment and types, and opens through `os.Root` for
bounded reads. Absolute paths, traversal, Windows devices/alternate streams,
external links, and nonregular files are rejected. Up to eight unique canonical
paths and 256 occurrences are accepted, with case-insensitive canonical path
deduplication on Windows. All occurrences remain in `Event.data.mentions`.

Only UTF-8 text files and directories are supported; binary data, images, and PDFs
produce explicit errors. Text validation inspects the bounded prefix that can be
attached, not the unread tail of an oversized file. Empty files/directories work.
Files are read when sending, never by the frontend or during autocomplete. A retry
reads again; multiple files are not an atomic filesystem snapshot.

| Harness | Delivery |
| --- | --- |
| OpenCode | One text part plus deduplicated native `file:` parts (`text/plain` or `application/x-directory`) in `prompt_async`. OpenCode does materialization; no duplicate Go-expanded content. |
| Pi | The prepared JSON attachment block is appended to the same RPC `prompt.message`. |
| Codex | The prepared JSON attachment block is a second `type: "text"` item in `turn/start.input`; both items use `text_elements: []`. |

Pi/Codex preparation limits are 2,000 lines and 50 KiB per file, 2,000 Unicode
characters per line, and 2,000 immediate entries/50 KiB per directory. Individual
directory names are JSON-quoted, subdirectories end in `/`, and links are marked
`[link]`. Directory attachments include real children even if ignored in discovery;
no recursive content is read. Oversized directories contain a sorted bounded
subset of the OS enumeration, with truncation indicated. Blocks use JSON-escaped
paths/content plus a `truncated` flag. The entire serialized context, including
escaping and framing, is limited to 200 KiB; exceeding it rejects the submission
instead of dropping attachments. OpenCode retains its native Read limits and
reports native preparation errors through the existing harness error flow.

`Event.text` remains the original message. `Event.data.mentionPreparation` holds
`{path,kind,mode,bytes?,truncated?}` per unique reference. `mode: "prepared"` records
Go preparation, not delivery success. `mode: "native"` makes no claim about native
materialization size or success. Sources coexist with both fields. Native histories
hold the expanded context; KLM state does not duplicate large attachment snapshots.
The chat renders reference badges inline with the text, showing only file/folder
names. The editor keeps badges atomic and maps their DOM positions to the full
canonical token's UTF-16 offsets. Deleting a badge removes the reference. Known
truncation is marked on its badge; expanded content stays in native history.

Preparation runs outside the application lock and precedes acceptance. The engine
rechecks session/project/harness and the absence of a running turn before committing.
The existing 128 KiB user-text, 512 KiB HTTP body, and 2 MiB transport/frame limits
remain; the final escaped prompt (including side-chat sources and linked-tool
prefix) is checked with 16 KiB reserved for protocol envelopes and metadata.
Preparation errors preserve the draft and do not start a turn. Native failures
after acceptance remain visible as harness errors. Subsequent linked consultations
and continuations do not reattach earlier files. Tool permissions are unchanged.

#### Manual acceptance checklist

Run in main and side chats with Pi, OpenCode, and Codex:

1. Attach a small file with a unique marker. Inspect the native prompt/history to
   confirm the content was present before any model-decided Read call.
2. Attach a directory containing files and a subdirectory; confirm immediate names,
   including ignored children, without recursive file contents.
3. Mix multiple files/folders, repeat a reference, and combine side-chat passages.
   Confirm one content inclusion per canonical path and all visible occurrences.
4. Exercise spaces, accents, emoji/Unicode before references, mid-text edits,
   replacing/removing tokens, keyboard/click selection, IME, and session switching.
   Enter with suggestions open selects and never sends; Shift+Enter/Ctrl+J insert
   newlines; Escape closes suggestions.
5. Try empty files/folders, truncation, an oversized aggregate, removed files, and
   binary files. Confirm explicit errors/truncation and preserved drafts on rejection.
6. Reopen history and check references and sources, then send plain text and linked
   consultations/continuations. Confirm ordinary permissions still work.

Implementation validation uses an engine build to temporary output and the desktop
production build. Native turn behavior and installed-harness compatibility still
require the manual checks above; builds alone do not establish them.

## Agent and Graph Files

The authoring CRUD is independent of graph execution and of the chat graph selector.
Files live in the selected project's `.klm/agents` and `.klm/graphs`, which are
created on demand. No `harness.toml` is created. See
[`GRAPH_AUTHORING_REFINEMENT.md`](../GRAPH_AUTHORING_REFINEMENT.md) for the decisions.

| Method | Path (prefix `/api/projects/{id}`) | Body / response |
| --- | --- | --- |
| GET | `/authoring` | `{agents:AgentRecord[],graphs:GraphRecord[],errors:string[]}` |
| GET | `/models/{harness}` | Real `ModelCatalog`; optional `?refresh=true`, no model prompt |
| POST | `/agents` | `{agent:AgentDefinition,revision:""}` → AgentRecord |
| PATCH | `/agents/{slug}` | `{agent:AgentDefinition,revision}` → AgentRecord; supports rename |
| DELETE | `/agents/{slug}` | `{revision}` → `{removed:true}` |
| POST | `/graph-drafts` | `{name,description}` → draft GraphRecord; layout only |
| POST | `/graphs/{id}` | `{definition:GraphDefinition,revision}` → saved GraphRecord |
| PATCH | `/graphs/{slug}` | `{enabled,revision}` → GraphRecord |
| DELETE | `/graphs/{slug}` | `{revision}` → `{removed:true}` |
| POST | `/graphs/{id}/layout` | `{positions:{[elementID]:{x,y}},viewport?:{x,y,zoom}}` → layout |

AgentDefinition's JSON fields are `name`, `description`, `enabled`, `defaultHarness`,
`model`, optional `effort`, and `prompt`. TOML uses `default_harness`. AgentRecord
adds `id`, `revision`, and derived `graphReferences` (graph names), which are not
stored inside the TOML. Model IDs come from real harness catalogs. Configurable
effort requires a supported value; models without configurable effort omit it.
Prompts are multiline TOML strings, not separate prompt files.

GraphRecord is `{id,revision,definition,layout}`. GraphDefinition uses the YAML keys
also in JSON: `name`, optional description text, `enabled`, `initial_node`, `nodes`,
and `choices`. Nodes and Choices are maps keyed by identity. The Go structs in
`authoring.go` are the serialization schema. Node variants:

```yaml
nodes:
  implement:
    type: agent
    name: Implement task
    agent: implementer
    overrides: # optional; only local overrides, never the inherited definition
      harness: codex
      model: selected-model-id
      effort: medium
    choices: [ready, blocked]
  prepare:
    type: terminal
    name: Update repository
    command: git pull
    to: implement
  checks:
    type: fork
    name: Parallel checks
    branches:
      security:
        name: Security review
        git_branch: checks/security
        output:
          task: "{{run.input.task}}"
        to: security-review
      tests:
        name: Test review
        git_branch: checks/tests
        output:
          task: "{{run.input.task}}"
        to: test-review
  integrate:
    type: join
    agent: integrator
    prompt: Follow project integration conventions.
    output_branch: feature/integrated
    choices: [integrated, blocked]
```

This fragment illustrates fields, not a complete executable graph. Agent IDs,
destinations, model IDs and Choices must refer to actual definitions/configuration.
Fork worktree isolation and incoming revision are fixed type rules; this CRUD does
not perform Git operations or define synchronization. Join `prompt` and
`output_branch` are optional. Terminal commands preserve their text and are never
executed by these endpoints.

Choice keys/references use the name-derived slug (`request-changes`), while `name`
retains underscores (`request_changes`). Input is a map of string fields with
`required` flags; output is a flat map of string/template values. Terminal Choices
retain output and cannot have `to` or `session`. Eligible destinations for a session
policy are agent and Join; omission means new. Template output is validated, not
executed here. Graph Save checks settings and references, but is not a declaration
of runtime executability: unfinished/disconnected destinations can be saved.

### Saving, reading and concurrent edits

- Agents are `<slug>.toml`. Graph definitions are `<slug>.yaml`. Companion layout
  files are `<slug>.yaml.layout.json`, with `positions` and optional `viewport`.
  Choice position keys are `choice:<choice-slug>`; other keys are node IDs.
- Create establishes a unique `draft-<id>` layout identity without registering a
  graph. The first Save checks the name-derived target for collisions, writes YAML,
  moves the draft layout to the companion name and removes the old draft layout.
  Abandoned layout-only drafts stay outside the catalog; draft recovery/cleanup UI
  is not included.
- Layout writes are atomic and independent of definition revision. The client
  serializes them, saves positions on drag release and viewport on user move end,
  and waits for them before Save/return. Layout never stores executable connections.
- Definition revisions are hashes of current file bytes. Updates/deletes reject
  stale revisions rather than overwriting newer edits. Renaming a graph moves both
  files; renaming an agent updates saved graph references.
- Referenced agents cannot be deleted or disabled. Graph references are derived
  from YAML, not fixture counters or cached UI state.
- Writes use same-directory temporary files and atomic replacement. Multi-file
  changes use a `.klm/.authoring-transaction.json` roll-forward journal. Reads and
  mutations recover pending transactions under the authoring mutex. A failed
  transaction reports an error; a subsequent access finishes recovery before
  exposing the catalog. This coordinates engine writes, not arbitrary external
  editors writing simultaneously during a transaction.
- Unknown TOML/YAML fields, unreadable files and malformed documents are reported.
  Invalid definitions are not imported as fixtures or silently overwritten.
  Catalog errors block mutations until repaired. Missing layout is supported;
  invalid layout is reported and defaults positions when reading.
- Client paths never determine the project root. IDs are validated, configuration
  symlinks/junctions are rejected, and reads are capped at 2 MiB per file. Existing
  JSON mutation limits apply. These endpoints use the engine's public API boundary.

Validation for this delivery: Go build, TypeScript check, and the requested browser
CRUD round trip in a temporary project. Created a graph with optional description
empty, saved/reopened its terminal node and layout; created/reloaded an agent using
the real Codex catalog, selected it in a graph node, connected it through a terminal
Choice with output, saved/reopened, and disabled/enabled the graph across reloads.
No graph execution, model prompt, full test suite or deep/adversarial review was run.

### Model selection and account quotas

Catalog discovery uses isolated, prompt-free native control processes scoped to the
project directory: Pi RPC plus its installed model-capability library, OpenCode's
authenticated loopback provider/config API, and Codex app-server model pagination.
Catalog responses expose `connectedProviders` (`id`, `name`, `authType`) as an
explicit boundary. Pi derives this from its credential-filtered available snapshot;
OpenCode intersects native connected IDs with authenticated/environment or explicit
provider configuration, excluding automatic free catalogs. Codex's default OpenAI
catalog requires a native account connection. Both engine and picker filter models
against this list. Model IDs include provider identity
for Pi/OpenCode. Session settings reject unavailable models/efforts and active turns.
No global model or thinking defaults are written. Existing CLI model overrides remain
readable; new UI selections come from the actual catalog.

Quota snapshots contain `source`, `observedAt`, `windows` (`name`, `usedPercent`,
`resetsAt` in epoch seconds), and optional `stale`. They are in-memory only and
credential-fingerprint scoped. Codex reads `account/read` and `account/rateLimits/read`
without starting a thread; ChatGPT subscription auth is required. Cross-harness
OpenAI auth must identify the same Codex account.
OpenAI subscription detection recognizes native Codex protocol metadata as well as
the OpenAI/ChatGPT provider IDs; API-key billing and intermediary routes remain
excluded. Ordinary protocol/client headers and non-auth provider settings do not
disqualify a matching OAuth connection.
Claude Code must be installed with
readable OAuth credentials; direct Anthropic OAuth is matched by token or account
and organization from `/api/oauth/profile`, then reads `/api/oauth/usage`.

Anthropic's OAuth usage endpoint is a best-effort provider integration rather than
a documented standalone Claude CLI command. Missing/expired credentials or unsupported
responses yield no new quota. Credential rotation remains owned by the CLI; KLM never
rewrites auth files, logs tokens, sends a model prompt to obtain quota, or queries a
user-supplied quota host. Credential files currently cover Windows/Linux file-backed
CLI installations; OS keychain-only storage is not read. Pi credential helpers for
native Codex-protocol models can be resolved by `pi auth check --credentials --json
--no-refresh`; the returned credential stays inside the engine and must identify the
same Codex account. API keys, intermediary providers, custom auth/route overrides, and unknown
account relationships are hidden instead of showing unrelated subscription limits.
Quota responses may include a non-secret `reason` code when unavailable, such as
`account_mismatch` or `harness_oauth_unavailable`, for diagnostics.

### Token usage details

`usage` is persisted and published through the existing session snapshots. Input
totals include cached tokens; output totals include reported reasoning. Null means
unavailable, and absent usage on older sessions remains unknown until the next run.
Context is a latest available occupancy estimate, not the sum of session totals.
Counts update on harness usage reports, not every streamed text delta.

- Pi: `get_session_stats` provides authoritative session totals, including retained
  pre-compaction history, and `contextUsage`. Requested at startup, after assistant
  messages/compaction, and at settlement (with a bounded final wait). Missing optional
  telemetry does not fail execution.
- OpenCode: native history plus live message/step updates, deduplicated by IDs.
  Step-finish counts take precedence over message counts to avoid counting the same
  call twice. Input adds cache read/write; output adds the separate reasoning bucket.
  Context uses the latest exchange's input + output and the actual model's
  `/provider` context limit. Model lookup failure leaves capacity unknown.
- Codex: `thread/tokenUsage/updated.total` replaces cumulative counts; `last.totalTokens`
  and `modelContextWindow` supply context. Cached input and reasoning output are
  already included and must not be added again.

Compaction invalidates stale occupancy until a fresh usable context report arrives.
Context snapshots may lag new prompts, tool results, or streamed output. No client-side
tokenizer, hardcoded model capacity, or usage inferred from visible text is used.

IDs are opaque strings, and timestamps
are UTC RFC3339 with fractional seconds. Native harness identifiers and Pi's
session path live in a separate private section of `state.json`.

Validation and semantics:

- Project names and grouping folder names are trimmed, 1-60 Unicode characters,
  with no control characters. Duplicate grouping names are rejected
  case-insensitively; `Ungrouped` is reserved. These folders are labels, not
  filesystem directories. `Project.folders` initially equals `[]`.
- Project `folder` must be an existing absolute directory, checked again before
  every execution. Processes always use that directory as their working directory.
- `icon` may be empty. Otherwise it must be a `data:image/png;base64,` URL, at most
  128 KiB for the entire URL, and a fully decodable PNG no larger than 1024x1024.
  The client is responsible for resizing. The pixel bound prevents compressed
  image allocation bombs.
- Session titles are trimmed, 1-200 characters. Optional model IDs are trimmed,
  at most 200 characters, with no control characters or leading `-`.
- `workspace` names an existing project grouping folder or `Ungrouped`. An empty
  or omitted workspace becomes `Ungrouped`. Archived grouping folders cannot receive
  new or moved sessions. It never changes the process cwd.
- Archiving a grouping folder hides the folder and its sessions without mutating each
  session. Session and folder archives are reversible and preserve all stored history.
- Only completed assistant events, including legacy events without a status, accept
  the durable `favorite` flag. Running, failed, and cancelled output does not.
- New sessions are idle with `events: []`. The harness must be discoverable when
  creating a session, but availability does not imply authenticated or usable.
- Messages preserve whitespace, cannot be blank or contain NUL, and are capped
  at 128 KiB UTF-8. JSON request bodies are capped at 512 KiB.
- Only one turn per session can run; another message gets 409 until it exits.
  Different sessions may run independently. Linked-agent consultations queue and
  route through the scheduler described below. User messages sent during a turn
  use the persistent queue described below.
- Event IDs remain stable when a harness item is updated. Array ordering is
  first-observed ordering. Item updates replace that item's data/text in place;
  Pi text deltas append. This is not a duplicate raw-event audit log.
- Event status is optional and describes the item, typically `running`,
  `completed`, `error`, `started`, or `cancelled`. Tool errors do not by themselves
  fail a turn, because a harness may recover. Explicit turn/assistant errors do.
- SSE uses default `data: <Session JSON>\n\n` events, not named events. It sends the
  current snapshot immediately, plus 15-second comment heartbeats. Slow clients
  may skip intermediate snapshots, but the latest snapshot includes every stored
  event. Reconnect simply receives the latest state; no Last-Event-ID is needed.

## Skills and MCP Library

The shared client exposes **Settings → Library**. Install complete user-requested
skills through KLM Agent's existing tools and its internal manager skill, then
select them per conversation or agent. Installation does not activate a skill.

- User root: the engine OS user's `~/.klm/skills`, shared by dev/release. For disposable
  probes use an **absolute** `KLM_USER_SKILLS_DIR`. Do not use real library data for tests.
- Internal root: `KLM_SKILLS_DIR` (authoritative), installed sibling `skills/`, then
  the development `engine/skills`. Only `skill-library-manager` is explicitly
  distributed to `general_agent`. An invalid override fails instead of falling back.
- Bundled internal files are editable and shipped recursively with the engine.
  Upgrades can replace bundled edits; use an external override for durable edits.
  Uninstall never removes the user library or an override outside the installation.
- Skill packages: one lowercase slug directory, UTF-8 SKILL.md, matching `name`,
  nonempty description up to 1024 bytes, at most 512 files / 16 MiB / 16 levels.
  Links/junctions, path collisions and nonregular files are rejected. All file bytes
  contribute to the revision. Captures use a bounded repeated read and atomic
  publication below `<data-dir>/capabilities/skills/<revision>/<slug>`.
- Captured copies are retained conservatively; there is no automatic snapshot
  directory pruning in this delivery. Confirmed skill removals move the source to
  `<library-parent>/skill-library-trash/<operation-id>` after reference validation.

Only the Pi **0.85.1 npm CLI on Windows** has the managed exclusivity gate. Other Pi
installations fail that gate. OpenCode is unvalidated and Codex managed selection
unsupported. Empty-selection OpenCode/Codex legacy runs keep their native behavior
and unknown native inventory. Nonempty selections remain saved but block execution
and graph preflight. There is no CODEX_HOME-based isolation claim or automatic import
of native global settings. Pi model discovery uses the same disabled resource and
startup-package-discovery flags, while retaining native provider/auth files.

### MCP connections and OAuth

Create/edit connections without starting them. **Test connection** is explicit and
can execute a local server. Stdio uses an executable plus separate args, a fixed cwd
or the actual execution cwd, and protected explicit environment entries. Shell shims
are rejected; use an executable such as `node.exe` and an already installed entry
point. No runtime/package is installed implicitly. A test of an execution-relative
stdio connection requires temporarily configuring a fixed directory; actual graph
activations always resolve their own directories/worktrees.

Streamable HTTP uses JSON/SSE protocol sessions, not legacy HTTP+SSE. Credentials
are secret environment/header/bearer values or OAuth credentials protected with
Windows per-user DPAPI in private engine state. APIs return presence only. Editing
uses omitted secrets = keep, `secrets` = replace the set, `removeSecrets` = remove.
Config changes invalidate the catalog and apply at the next complete turn; retained
active snapshots keep their configuration and protected credential references.

For OAuth choose a registered client ID (and secret when required), or leave the ID
empty for public dynamic registration. Configure a provider-accepted redirect URI:

```text
https://<reachable-engine-api>/api/library/mcp/oauth/callback
```

That URL reaches the **engine API**, not the frontend server or private gateway.
Local engine-browser use may use loopback HTTP. A mobile/remote browser must not
use its own localhost as the callback. HTTPS ingress/provider registration is an
explicit user setup, not an engine-managed tunnel. Authorization opens only through
a user action. The mobile web flow uses the same tab and an explicit return link
to Library; providers that prohibit embedded WebViews need external-browser human
validation. There is no device-flow or manually pasted authorization-code bypass.
Callbacks are expiring, one-use, state/PKCE/issuer/resource-bound. Refresh is
serialized and never initiates interactive authorization or retries a tool on 401.
Disconnect invalidates stored local credentials, including references of captured
configurations, without claiming upstream token revocation.

The administrative API retains the existing personal-use network contract. Secret
redaction is not network authentication. The harness gateway remains authenticated,
origin-rejecting and loopback-only; upstream credentials never go to a harness.

### Selection and API contract

```toml
skills = ["example-skill"]

[[mcp_connections]]
connection_id = "<stable-engine-connection-id>"
tools = ["search", "read_document"]
```

These fields belong in `.klm/agents/*.toml`; credentials do not. An empty tools list
permits resources only when supported by the selected connection. It is not `*`.
New tools require explicit selection. Resource URIs stay opaque to their server.

| Route | Purpose |
| --- | --- |
| `GET /api/library` | Bounded skill/connection inventory and adapter status |
| `POST /api/library/mcp` / `PATCH /api/library/mcp/{id}` | Save name/configuration and explicit secret mutation |
| `POST /api/library/mcp/{id}/test` | Temporary connection, bounded paginated discovery and cleanup |
| `POST /api/library/mcp/{id}/oauth` | Start human OAuth; optional same-origin web `returnUrl` |
| `GET /api/library/mcp/oauth/callback` | Dedicated one-use OAuth callback |
| `POST /api/library/mcp/{id}/disconnect` | Clear local credentials/pending authorization |
| `GET /api/library/{skills|mcp}/{id}/deletion` | Item/reference revision and usage preview |
| `DELETE /api/library/{skills|mcp}/{id}` | Confirm with `{revision}`; durable deletion outbox |
| `POST /api/library/recover` | Retry a pending deletion without overwriting external conflicts |
| `GET/PATCH /api/sessions/{id}/capabilities` | Desired/applied/preparing snapshot; PATCH `{revision,selection}` |
| `POST /api/sessions/{id}/questions/{questionId}/elicitation` | Human `{action,content?}` response to the exact pending MCP question |

Selections contain `{skills: string[], mcpConnections: [{connectionId,tools}]}`.
Revision conflicts return 409 instead of overwriting another client. Main, general,
side and Focus composers target their actual conversation. Steering retains the
current full-turn snapshot. New side/spawned sessions copy only applied external
selection (or saved selection before any application). Runs freeze local config,
known catalogs and skill copies at actual start; later activations do not inject
newly discovered tools. A server's remote behavior itself cannot be frozen.

MCPs start with bounded initialization and a total startup deadline. Offline entries
remain selected with model/user diagnostics. Invalid skills, corrupted snapshots
and failed isolation are hard errors. Tool dispatch revalidates the execution and
allowlist even in YOLO. Tool calls are not retried after uncertain delivery. The
engine-owned stdio process group is independent of harness Stop containment and
is not a filesystem/network sandbox. Graph finality waits for started gateway
workers; closing a local client is not proof of remote cancellation.

Current limits: 64 saved connections, 32 selected connections per execution, 512
tools / 32 discovery pages / 2 MiB per catalog, 128 KiB per schema, 4 MiB transport
lines, 1 MiB / 100 content blocks per delivered tool response, 256 KiB call args,
five-minute calls/elicitation, and 16 pending OAuth transactions with ten-minute
expiry. Resource blocks retain explicit JSON representation. Images support the
Pi block representation for PNG/JPEG/GIF/WebP; audio/unknown blocks return explicit
errors. End-to-end model multimodal delivery is not validated. Form elicitation
supports flat primitive fields and string-enum arrays; unsupported forms/modes
return errors. HTTPS-URL elicitation never opens navigation or accepts itself.
Prompts, Sampling and resource subscriptions are not advertised.

Deletion previews all known project agents (including archived/removed projects),
session desired selections and preserved captured runs. An inaccessible project or
external file conflict blocks completion. A durable outbox revalidates authoring
revisions and rolls forward before serving after restart; it is not an atomic
multi-file transaction. The Library recovery action retries an unresolved outbox.

Compilation/probe evidence and the human acceptance route are in
`../SKILLS_MCP_LIBRARY_IMPLEMENTATION_PROGRESS.md`.

## Harness Adapters

Discovery runs at engine startup without sending any prompts or reading auth
files. Executable overrides are optional: `KLM_PI_BIN`, `KLM_OPENCODE_BIN`, and
`KLM_CODEX_BIN`. Each is a single executable path/name, not a shell command or
argument string. A Pi override can instead point to `cli.js`, requiring Node.js
on PATH. Invalid overrides make that adapter unavailable; they do not fall back.

On Windows, discovery checks `%APPDATA%\npm\node_modules`, npm package locations
adjacent to discovered PATH shims, native `.exe` files on PATH, and Codex's
`%LOCALAPPDATA%\Programs\OpenAI\Codex\bin\codex.exe`. Pi's installed package is
`@earendil-works/pi-coding-agent/dist/cli.js`; OpenCode's is
`opencode-ai/bin/opencode.exe`. No user-specific absolute installation paths are
hardcoded. `.cmd`, `.bat`, and `.ps1` shims are never executed or parsed.

All prompts are supplied verbatim through stdin and EOF, never shell-interpolated
or placed in command arguments. Model and native session identifiers are separate
argv entries. Children inherit the environment and use their harness's existing
authentication/configuration. The engine neither obtains nor returns credentials.

### Pi 0.80.6

```text
node <pi-cli.js> --mode rpc --no-extensions --extension <owned-extension.ts> --session <absolute-session.jsonl> [--model <model>]
```

One RPC process per turn; subsequent turns use the same session file at
`<data-dir>/sessions/<KLM-session-id>.jsonl`. Before prompting, the engine requires
both the approval extension's readiness notification and a validated get_state
response. The embedded extension gates every agent-dispatched tool_call using a
cancellable RPC UI selection. Unknown interactive dialogs fail closed. Responses
are sent over stdin, not inferred from stdout tool notifications.

`message_update.assistantMessageEvent` text/thinking deltas append to the correct
content-index item. `message_end` replaces each item with final text/thinking and
checks `stopReason`/`errorMessage`, even when the CLI exits zero.
`tool_execution_start`, `tool_execution_update`, and `tool_execution_end` share an
event; accumulated partial results replace rather than append. Bash is a command
event. `agent_settled` marks completion. Duplicate user messages, full `agent_end`
message copies, and turn boundary copies are not added to the UI history.

Pi is **not sandboxed** by this engine. The approval extension gates agent tool
dispatch, not arbitrary extension code or OS calls. Third-party extension discovery
remains disabled so later handlers cannot mutate arguments after approval. This
also disables extension-provided MCP tools. No ungated print-mode fallback exists.

### OpenCode 1.18.30

```text
opencode.exe serve --hostname 127.0.0.1 --port 0 --mdns=false
```

An owned loopback server is started per turn with random HTTP Basic credentials.
The engine uses directory-scoped HTTP/SSE directly, not `run --attach`, which
would still auto-reject prompts. Native session IDs are reused after directory
validation. It subscribes before posting prompt_async, tracks message/part IDs,
streams text/reasoning/tools, and reconciles final history without duplicating old
messages. Idle completes the turn; individual step-finish events do not. Stream
loss fails and aborts the turn rather than silently replaying actions.

History is read with `limit`/`before` and the native `X-Next-Cursor` header.
Pages start at 64 messages and shrink if a response exceeds KLM's 32 MiB bound.
Initial hydration retains message IDs and usage metadata, discarding old message
bodies after each page. Final reconciliation reads backwards only until the
pre-turn baseline, then processes the current turn in chronological order.
History JSON is projected while reading: only tool `state.metadata.diff` and
`state.metadata.files[].patch` strings larger than 64 KiB are replaced by an
explicit omission marker in KLM's copy. Small previews, file names, change counts,
diagnostics, tool input/output/status and usage remain intact. The native OpenCode
history and model context are not modified. The original bytes still travel over
HTTP; discarded preview strings are scanned without accumulating them in memory.
Pages retain a 32 MiB JSON bound after projection and a separate 256 MiB input
bound; either bound reduces the page size before failing on a single message.
HTTP JSON errors identify the method and endpoint without query values or bodies.
This projection applies to history HTTP responses; live SSE frames retain their
separate 2 MiB bound. Native runtime validation remains a human check. Focused
unit tests cover pagination, metadata projection, usage totals and diagnostics.

Permission asked/replied events create/remove pending cards. Positive choices
always send native `once`; KLM owns remembered scopes. Native `always` is not used
because its in-memory cache can affect other sessions in the same instance.
Existing plugins/config and explicit denials are respected. Native question events
are routed to question cards. Tools already allowed by native rules do not require
a permission card.

### Codex 0.142.5

```text
codex.exe app-server --listen stdio://
```

The engine initializes JSONL RPC, starts/resumes the stored thread with read-only
sandboxing and on-request/user approvals, then starts the turn. Native IDs and
working directories are validated; no replacement thread is silently created.
Text/reasoning/output deltas update stable items and completed snapshots replace
them. Turn completion, not process exit, ends execution.

Command and file-change requests receive accept/decline only. Permission-profile
requests preserve all requested access and deny entries; native grants have a
minimum **turn** lifetime, labeled in the UI. Recognized empty MCP tool-approval
forms receive automatic one-shot approval under normal policy. Unsupported
forms, URLs, legacy/dynamic-tool requests are declined or failed, never
answered affirmatively. No native session or global policy amendments are used.

Native item/tool/requestUserInput requests are handled as questions, not approvals.
The engine enables `features.default_mode_request_user_input=true` and
`tools.experimental_request_user_input.enabled=true` through child-only CLI flags.
This Codex feature is under development in 0.142.5; no global config, collaboration
mode, or model is changed.

## Permission Decisions

Session snapshots include optional pending `permissions` entries with id, harness,
kind, title, description, patterns, details, normalized command/path when
available, decisions, allowLabel, createdAt and resolving. IDs bind to the active
engine turn and native request. Client payloads contain a choice only, never raw
native responses. Concurrent/stale decisions are rejected; stop and restart
invalidate pending requests.

- `once`: approve the native request without an engine rule. Codex permission
  profiles last for the current turn, not one operation.
- `session`: persist an exact grant for the logical KLM session.
- `always`: persist an exact grant for this project and harness, across sessions.
- `reject`: deny the native request. The harness may recover or stop.

Remembered grants use a hash of the full semantic action/resource scope plus
project, harness and permission kind. No wildcard expansion or parent-directory
inference is performed. All request patterns/arguments must match. Ambiguous
requests disable remembered choices. Grant persistence succeeds before a request
is released; delivery failures roll back the new grant. Pending requests are not
replayed after restart. A permission audit entry is included in session events.

Grants are stored in `state.json`'s permissionGrants field. They do not modify
global harness settings. There is no grants-management UI in this first slice;
session grants end when that logical session is no longer used, not at every turn.

## Question Tools

Session `questions` entries contain id, harness, items, createdAt, and resolving.
Each item contains id, header, text, options (label/description), multiple, custom,
and secret. Responses are positional `answers:string[][]`, one array per item;
the engine maps them back to each harness's native identifiers and labels.
Requests are tied to the active turn. Unknown choices, empty answers, duplicates,
unsupported multi-selection, stale IDs, and duplicate submissions are rejected.
Dismissal returns no default answer. Pending questions are cleared on stop/restart.
Answers do not affect permission grants. The answer audit is recorded in history;
private fields are masked there but sent to the receiving harness. Its own records
or model output may retain/echo values; this is not end-to-end secret redaction.

- OpenCode: handles question.asked/replied/rejected, GET /question reconciliation,
  and ordered native reply/reject endpoints. Verified native descendants route
  requests to the owning conversation even before task metadata arrives; replies
  retain the native question ID. YOLO does not auto-answer questions. A failed
  delivery leaves an unresolved question available for manual retry.
  Custom entry defaults to allowed;
  multiple comes from the request. Native question registration is enabled on the
  owned server. Old CLI-run sessions may still deny question; use a new session
  rather than overriding intentional native permission rules.
- Pi: the owned extension registers sequential `ask_user` with structured questions,
  options, custom text, and multiple selection. Only this tool bypasses the redundant
  tool-permission gate. A versioned `klm.question.v1:` RPC input envelope carries
  the form, and JSON answer arrays return to the actual tool result. Other tools
  remain gated. Cancelled questions are explicitly reported as dismissed.
- Codex: native questions preserve IDs, offered choices, isOther/isSecret, native
  RPC IDs and resolution/expiry notifications. Replies map each question ID to
  `{answers:[...]}`. No native multiple flag is inferred from array-shaped answers.
  Malformed requests return empty answers and a warning, not fabricated consent.

## Linked-Agent Conversations

`Session.parentId` attaches exactly one side conversation to a main session in the
same project. Creation is idempotent under the engine mutex, including concurrent
clients. `/api/state` includes both; the client filters children out of its top-level
list. Main and side have independent native IDs/files and harness/model settings.
Switching a side harness is allowed only before any events or active execution.

Side messages accept up to eight source references from the parent conversation,
with at most 128 KiB of selected text total. Passages are never truncated. The engine
checks source IDs and attaches deterministic JSON reference context: project/main
identity, exact passages, each source and up to two neighboring events on each side
(2 KiB per surrounding event). A selection-free first turn includes the latest six
events with the same bound. The user question stays separate from reference material.
Original selected text and source references persist with the user event and session.

Shared tools, available to all three harnesses:

- `linked_discover`: caller/linked identity, role, project, harness and execution status.
- `linked_read`: only the linked conversation, latest ten events by default, maximum
  twenty per call; `cursor`/`nextCursor` page history, `messageId` targets a source,
  `offset`/`nextOffset` page long text in 4,096-character chunks. No native paths or
  arbitrary session targets are exposed.
- `linked_ask`: explicit short `topic` (120 characters) and `question` (32 KiB).
- `linked_answer`: `requestId` and `answer` (64 KiB), accepted only in the recipient's
  actual active consultation turn, then the agent is instructed to finish that turn.

Each execution owns a random-capability, authenticated loopback Streamable HTTP MCP
server with four tools. Access is bound to that turn and its linked counterpart;
capabilities are not placed in engine state and expire when the process finishes.
OpenCode dynamically registers `klm_linked` on its owned server and confirms connection
and tool-list readiness. Codex receives child-only `mcp_servers.klm_linked` overrides
and confirms the tool inventory through `mcpServerStatus/list` before starting a turn.
Pi's bundled temporary extension exposes the same operations and verifies the inventory
before its existing readiness notification. Linked communication tools bypass Pi's
redundant per-tool permission dialog. Codex automatically accepts native tool-call
approval forms for the ready, turn-owned `klm_linked` bridge; OpenCode automatically
replies `once` to permission requests matching its four exact internal tool names.
These internal operations never create permission cards or remembered grants.
External MCP tool invocations and skill loading also default to automatic approval,
subject to saved rules (see `PERMISSIONS.md`). MCP forms requiring user input are
not auto-answered; filesystem/shell tools keep their operation-specific policy. No user/global
installation or native transcript-format translation is required.

The installed versions inspected for this feature were OpenCode 1.18.30 and Codex
0.153.4; Codex's generated local app-server schema confirms the status-list contract.
Runtime readiness checks fail explicitly if an installed harness cannot expose the tools.

Requests persist in `state.json.consultations`, with question, answer, topic, endpoint
IDs, timestamps, status (`queued`, `answering`, `completed`, `failed`, `cancelled`,
`interrupted`), and delivery state. Both sessions receive atomically updated activity
events. Background output and permission/question answer audits carry `consultationId`
so the client groups the recipient's background answering work under the relevant
activity. The requester's resumed response is shown as ordinary chat messages,
including for previously saved consultations; its correlation ID is retained.
Native consultation prompts and outputs still enter the recipient's native history.

Idle recipients start immediately; busy recipients wait for the current turn to finish.
Tool requests wait at most five seconds, safely below HTTP timeouts. Reciprocal requests
yield immediately. If no terminal result arrives within that wait, the tool instructs
the requester to finish its turn; the engine starts a correlated continuation when
it is idle. No same-session parallel turn or polling loop is needed. At most four
questions may be outstanding per requester; identical pending questions deduplicate.
Tool results explicitly carry `action: "continue"` with terminal answers/failures
or `action: "yield"` while still pending. A reply present when the five-second wait
expires takes precedence over yielding and includes the full answer. Resumed prompts
explicitly replace the earlier yield instruction and ask the requester to use the
answer without another user follow-up.
Unanswered requests expire after ten minutes, including queue time. Stop cancels linked
requests involving that conversation; per-activity cancellation targets one request.
Failed/stopped requester turns suppress unattended continuations. Shutdown and restart
retain terminal interruption state rather than replaying background work; an answer
already persisted before restart remains readable even if its continuation was interrupted.

Manual validation: open from header and selection; reopen history; add later passages;
resize with pointer/keyboard and narrow layouts; test different-harness pairs in both
directions; queue against busy recipients; cancel, stop, and restart during consultation.
Only lightweight Go/desktop builds are part of implementation validation, not live model
execution, a full suite, or an adversarial review.

## Persistence And Recovery

- `state.json` is the full checkpoint (v2, `journalFormat: 1`). Commands and stream
  updates share one ordered `stream.journal`: each frame is a little-endian uint32
  JSON length, uint32 CRC32, then JSON, capped at 256 MiB. Stream records retain
  `{revision,session,updated,updates,usage?}`; command records use
  `{format:1,revision,transaction:[{path,value?|length?|delete?}]}`. Paths are arrays
  of field names (JSON tag, or Go name when untagged, including embedded containers),
  slice indexes and map keys. Each command frame includes all
  affected queue payloads, receipts, consultations, graph records and event edits.
  A frame is synced before publishing state, SSE notifications or HTTP success.
  Identical commands/stream updates need no new revision or write.
- Command callbacks clone mutable metadata in memory, sharing immutable strings
  and append-only event histories. Existing session events must be replaced through
  `diskState.setEvent`, which stages indexed changes until persistence succeeds;
  never mutate existing event payload maps in place. Activation event logs are
  append-only. SSE changes are ordered by index. No per-command full JSON state
  clone, global history diff or full-session event copy is performed. Metadata
  traversal/validation still scales with the number of records; this is not an
  O(1) database. Captured graph validation/compilation and event identity indexes
  are reused rather than rereading source text or unrelated event histories.
- Every five minutes the checkpoint worker serializes with other checkpoints,
  rotates the active journal to `stream.sealed` under `app.mu`, then releases it.
  It reconstructs an independent state from the previous checkpoint and sealed
  segment, writes/syncs/replaces `state.json`, and removes only that sealed segment.
  Commands and streaming continue into a new active journal during the expensive
  replay/validation/serialization/write. Shutdown joins this worker before releasing
  the data-directory lock. Small journal fsyncs remain serialized under `app.mu`.
- Recovery reads checkpoint, sealed segment, then active segment, ignoring revisions
  already in the checkpoint and requiring contiguous newer revisions. It truncates
  and syncs an incomplete last frame; a complete corrupt frame, unsupported format
  or revision gap is an error. Session reordering preserves histories by identity.
  Versions 1/2 and the older stream-only journal remain readable. Before serving,
  startup persists the `journalFormat` marker so older binaries refuse the data
  instead of ignoring a journal. Downgrade is unsupported; a backup includes all
  three storage files, not just `state.json`.
- Windows replacement uses `MoveFileExW(REPLACE_EXISTING | WRITE_THROUGH)`.
  Unix uses rename plus parent-directory sync. Use a local filesystem with normal
  atomic rename/sync semantics, not a network share or cloud-synced data directory.
- In-memory state changes and SSE notifications happen only after persistence
  succeeds. A write failure preserves the last published snapshot, cancels active
  executions, blocks further mutations, and makes health return 503. Fix the
  underlying storage problem and restart; no automatic overwrite/retry is done.
- `engine.lock` is held exclusively by an OS file lock/handle for the engine's
  lifetime. It contains a diagnostic PID, but ownership is not inferred from the
  PID. Kernel lock release on process exit handles stale files safely. Do not
  delete the lock file while an engine is running.
- Startup refuses malformed/unsupported state instead of resetting it. Sessions
  left running become `error` with a durable interruption event. Native harness
  transcripts remain owned/formatted by the harness itself; they are not part of
  the engine JSON transaction, and a stopped turn may have partial native history.
- Stop cancels the turn before persistence and independently terminates the whole
  conversation runtime: Windows closes its owned Job Object, including native
  subagents and local tool descendants; Unix signals its owned process groups.
  Native abort/EOF and pending permissions do not gate process termination. The
  runtime is discarded and a subsequent turn resumes the stored conversation in
  a new runtime. The stop response waits for termination and the durable cancelled
  snapshot; unconfirmed termination is reported as an error. There is no replay of
  the cancelled prompt. Force-killing the engine itself cannot run cleanup and
  may leave external children, which are not blindly killed by PID on restart.
- Codex resumes threads with `excludeTurns: true` so the response omits the old
  timeline already stored by KLM. Native conversation history and model context
  remain intact; this does not compact the conversation or replay failed inputs.
- Output is consumed concurrently from stdout and stderr. Pi/Codex JSON lines
  are capped at 4 MiB; an oversized line cancels the process with an explicit
  error. Their RPC readers use a bounded 64-frame channel with backpressure,
  without a cumulative stdout quota per turn or process. Long executions and
  repeated tool snapshots therefore do not exhaust a lifetime byte budget.
  OpenCode retains its separate SSE-frame and HTTP-response bounds. Stderr is
  capped at 16 KiB and is never persisted, exposed, or logged. Exit failures
  report a bounded diagnostic instead. These are transport bounds, not history quotas.
- Session history is durable and unbounded across turns. Full checkpoint/recovery
  cost still grows with retained history, but is outside the live application mutex.
  There is no history retention/deletion API yet. Back up the data directory with
  the engine stopped. Measurements and recovery checks: [R4 result](../LATENCY_R4_RESULT.md).

## Network and Private Control Boundaries

The public API binds `0.0.0.0:7331`, including LAN clients. It does not restrict
HTTP Host: domains, IP addresses and reverse-proxy authorities are accepted.
CORS sends `Access-Control-Allow-Origin: *`, including for opaque/null origins,
and allows wildcard request headers without credentials. Requests without Origin
are also accepted. CORS does not grant credentials and is not network authentication.
JSON mutation limits remain intact. Tunnel subdomains can change without engine
configuration; enter the new public engine URL in the frontend Settings.

Network authentication and TLS are explicitly deferred in the approved personal-use
delivery. The existing `loopbackHost` helper, private authenticated MCP bridge and
owned harness endpoints retain their local boundary. Engine shutdown is available
only through a named pipe whose ACL allows this Windows user and denies network
logons. Grants, permissions and sandbox behavior remain unchanged.

The engine never reads auth stores, prints environment values, or exposes binary
paths/command lines through discovery. Harness-generated tool/message content
can still contain sensitive project information and is shown/stored as content;
this is not a general-purpose content redaction system. Protect the data directory
with user-only OS permissions. State is plaintext, not encrypted.

The native directory picker is Windows-only. It runs a fixed PowerShell
`-NoProfile -NonInteractive -STA` WinForms `FolderBrowserDialog` script with UTF-8
stdout and no user interpolation. Dialogs are serialized, have a five-minute
timeout including queue time, and return only an existing absolute path or null
on dismissal. Request cancellation/shutdown kills the owned picker process tree.
Other platforms return 501 for this endpoint; Unix support has not been validated.

## Validation Scope

Installed Pi source and harness CLI help were inspected without sending prompts.
The implementation is intended for a lightweight Go build followed by manual
HTTP, picker, SSE, stop/restart, and authenticated chat validation. No tests or
test suites are included or run as part of this slice.

## Graph execution core (P1–P5)

Graph authoring remains separate from executable compilation. Runs snapshot the
graph YAML and referenced agent TOMLs, resolve real model/effort settings, and
retain those definitions through catalog edits. The compiler checks complete
destinations, output references, normal Fork reconvergence and Join contracts.
Layout is optional. Ordinary cycles are allowed; nested Forks are rejected.

The main conversation's private harness bridge exposes `graph_catalog`,
`graph_activities`, `graph_get_run`, `graph_recent_events`, `graph_invoke`,
`graph_assess` and `graph_update_activity`. The side conversation gets the four
read-only tools. There is no public HTTP invocation or orchestrator Stop tool.
Selecting a graph, selecting None and opening Graph never execute or cancel work.

Invocation records an express user-message authorization reference, self-contained
task, objective and resolved workspace decision. `authorization` is a list of
`{eventId,text}` references grounding the activity. Each event must exist, have type
`user`, and belong to the invoking conversation. The reference is traceability,
not semantic proof of consent or a native permission grant. The orchestrator
instructions enforce the distinction.
Activities use versions and operation IDs; duplicate operations do not dispatch
another run. Ready correction takes priority, then explicit priority and request
order. Dependencies wait for an explicit satisfied assessment of their objective;
normal completion alone does not release them. Blocked/failure/interruption cannot
satisfy a success prerequisite. A retry requires concrete correction and the
user's fresh/reuse decision when earlier artifacts/workspaces are involved.

### Invocation contract update — approved 2026-09-13

The latest decision in refinement section 13 supersedes the earlier requirement
to ask for a workspace mode and supply `workspace.authorizationEventId` on every
invocation. Incomplete nested `graph_invoke` schemas had left the agent guessing
`path`, `scope` and event identifiers against strict engine validation.

- Omitting `workspace` resolves to `original` without asking. The product default
  is the basis for that resolution; omission must not be represented as express
  consent or as a message in which the user chose that directory.
- Explicit `workspace.mode: "original"` and `"new_worktree"` remain available.
  `original` uses the registered project directory and its actual file state,
  including when it is already a worktree. It does not take an arbitrary `path`.
- `workspace.authorizationEventId` is not mandatory. Activity `authorization`
  remains required with the `{eventId,text}` shape above; it is not replaced by
  `scope` or by a required workspace-choice event.
- Publish complete nested schemas through MCP and Pi, with field types,
  required/optional fields, mode/attempt enums, defaults, descriptions and reuse
  map values. The workspace contract includes `mode`, `attempt`, optional
  `authorizationEventId`, `baseRevision`, `sourceRunId` and `reuse` associations;
  retain the applicable fresh/reuse conditions rather than marking every field
  mandatory. Validation and the published schemas must agree.
- Orchestrator instructions apply defined defaults and ask only for blocking
  ambiguity or missing information. A new worktree still uses the requested
  revision or current local HEAD by default, without a mandatory revision question.
- Retrying with earlier artifacts still requires the user's fresh/reuse decision
  and concrete correction; reuse still requires explicit associations. If that
  decision is missing, ask. Fresh in the original directory retains W2's existing-
  files behavior without reset or automatic worktree creation.

This section records the approved contract for the parallel Go implementation,
not evidence that its new default/schemas are running. See
`GRAPH_ENGINE_PROGRESS_INVOCATION_DOCS.md` at the repository root. The user will
reload the tab and retest after the principal agent coordinates the restart.

### Run lifecycle and execution

Each run has its own context and occupies its conversation slot during `starting`,
`running` and `ending`. The initiating chat turn can finish or keep conversing.
Node sessions are private, with explicit run/node ownership and canonical cwd.
Continued sessions are reused only for the latest visit to that node in the same
run, same directory and harness, after the adapter reports them continuable.
Questions and permissions retain their originating private-session callbacks and
are projected into the owning conversation, including when Graph is hidden.

Only a valid Choice can finish an agent/Join node. The engine's
`{origin:"engine",id:"blocked"}` is distinct from every author Choice. Invalid
payloads and normally ended turns missing a Choice share a deduplicated counter
per activation: two corrections, failure on the third. The core persists
reservation/sealed/drained/accepted phases. No acceptance callback dispatches work;
the supervisor continues only after the adapter's final `Finished` callback.

Terminal runs one noninteractive PowerShell script in the inherited workspace.
`$payload` is loaded from a private UTF-8 JSON file; values are never pasted into
the script. The output mapper supports local `payload.<field>`, `command.result`
and `run.input.task` references. Stdout/stderr are combined in observed order and
bounded to 64 KiB UTF-8 with explicit truncation metadata. A nonzero shell exit
continues through the configured mapping; process-start/collection failures fail
the run. There is no task timeout or interactive Terminal input.

Git operations use argument arrays and owned processes. New worktrees live under
`<data-dir>/worktrees/<project-id>/<workspace-id>` and use unique run/activation
branch suffixes. An intent is persisted before creation. Fork fixes one incoming
commit for its isolated branches; branches without isolation share inherited cwd,
branch and dirty state. No implicit commit, push, pull, checkout of the original
directory, reset, dirty-file copy or cleanup is performed. Fresh in the original
directory intentionally uses existing files. Reuse explicitly maps `initial` and
`node:<node-id>:<occurrence>[:branch:<branch-id>]` to prior workspace IDs; repository,
source-run usage and managed-branch provenance are checked.

Join collects distinct payloads by incoming connection and causal round; `{}` is
an arrival. It starts its agent only when all inputs arrived, rejects duplicate
independent arrivals/overlapping rounds, and agrees incoming Choice session policy
(Terminal does not vote). Without output branch it integrates at the parallel
origin. With output branch it creates a new integration worktree from that origin's
current local HEAD. The agent integrates and resolves conflicts; the engine never
merges automatically. Continued work uses the integration directory. Missing
inputs with no remaining producer fail with a dependency diagnostic; user waits
are still active work, not deadlock.

Terminal results and slot release are committed with a persistent notification
outbox only after owned work settles. Notifications share the existing linked/user
turn arbiter and cannot overlap native turns. Failed/uncertain notification sends
retry with the same ID; effects are idempotent, not an exactly-once distributed
transaction. Preflight failures have persistent activity-error notices. Startup
preserves scheduled work/outbox and interrupts in-flight runs, activations and
workspace intents without replaying them. Workspace artifacts and native IDs stay.

Adapter preflight is authoritative per harness/platform. Implemented mechanisms
are exposed by `GraphAdapterCapabilities`; they are not a claim of native runtime
validation. Unconfirmed node/tool-call completion keeps a run in `ending` with a
concrete diagnostic instead of releasing its slot as if termination were confirmed.

### External MCP lifecycle boundary — approved 2026-09-13

Refinement section 14 supersedes the preventive OpenCode/Codex configuration ban
reported as `cannot confirm lifecycle ... agentdeck`. A configured external server
alone is not evidence of a pending call. Permit configured MCPs without a server-name
allowlist and without disabling them; this is not an `agentdeck`-only exception.

The engine guarantees the node and its tool calls: prevent new calls after sealing
and await completion of calls actually started before accepting Choice. Shared MCP
server shutdown is not required or guaranteed. A response such as "task started"
completes the call's lifecycle responsibility; the detached remote task continuing
past that response is outside this guarantee and is not thereby deemed successful.

Error/cancellation or loss of the callback without evidence of call completion must
record uncertain finality. Process exit, an empty owned Job, closing the MCP client
or requesting cancellation cannot substitute for a missing call result or prove
remote cancellation. Preserve uncertain-finality handling; do not accept Choice
or report a normal result merely because the local callback/process died.
Grants, native approval flows and sandbox settings remain unchanged.

The adapter implementer owns the parallel implementation. This contract update
does not claim a new build, test or restart. The principal coordinates the requested
restart after the build, then the user reloads the tab and retests. See
`GRAPH_ENGINE_PROGRESS_EXTERNAL_MCP_DOCS.md` at the repository root.

Internal instructions live in `engine/prompts/` and are reread for every send.
Development uses that source directory. Packaged engines can include a sibling
`prompts/` directory or set `KLM_PROMPTS_DIR`. Missing/empty prompts fail explicitly.
With `go -C engine build`, the resulting `engine/engine.exe` also has the existing
`engine/prompts/` as its sibling resource directory. Neither default lookup relies
on the server's working directory. For an explicit development override, from the
repository root use `$env:KLM_PROMPTS_DIR = (Resolve-Path .\engine\prompts).Path`
before launching the engine. No prompt is read as a package-initialization side
effect; the factory is registered in `init()` and prompts are loaded when needed.
See `GRAPH_ENGINE_PROGRESS_CORE.md` and `GRAPH_ENGINE_PROGRESS_ADAPTER.md` at the
repository root for integration contracts and actual verification evidence.

## Retired Tasks storage

The abandoned Tasks prototype is not part of the current engine. At startup,
while holding the data-directory lock, the engine removes its known checkpoint
fields (`taskSchema`, `tasks`, `taskRuns`, `githubBindings`, `githubReceipts`,
`engineTime`) and task ownership metadata on sessions and graph records. Ordinary
chat histories, native session identities, projects and authorized graph records
remain intact; graph input `task` is unrelated and is preserved.

Cleanup first replays both journal segments into an isolated copy, excluding only
retired Tasks fields, and validates the result with the current storage rules.
Unknown fields, corrupt journals and graph records without valid user authorization
still fail rather than being silently discarded or reauthorized. Before replacing
the checkpoint, original files are synced to `backup-before-tasks-removal-*` under
the data directory. Journal segments are retired only after the clean checkpoint
covers their revisions. Existing `state.pre-tasks-*` backups are never restored or
deleted. No harness work is started by the cleanup.

## Queued messages and steering (issue #4)

`POST /api/sessions/{id}/messages` accepts optional
`mode: "steer" | "interrupt" | "queue"`. The default and normal composer Enter
are `steer`: deliver at the next native model boundary, not after all work ends.
All pending steering inputs are claimed as one submission with separate timeline
entries. Pi and OpenCode claim at their native boundary hooks. If the
turn finishes before dispatch, pending steering takes priority over follow-ups
and starts together in the same native conversation. Explicit `queue` mode and
agent-authored `session_send` keep end-of-task FIFO semantics.

`interrupt` is the composer's **Send now**: durably accept the selected input,
cancel the active chat and its owned process container, then start only that input
after confirmed shutdown. Other pending items are paused/unconfirmed. Explicit
Stop cancels a pending replacement; restart never automatically replays it.
Already completed effects are not undone. Independent graphs are not stopped.

Session summaries and SSE include `queue`, with item ID, text, mode, status, and
recoverable error. `POST /api/sessions/{id}/queue/{messageID}/send` sends a pending
item with the same interrupt-and-replace semantics; `DELETE
/api/sessions/{id}/queue/{messageID}` removes an unsent item. Dispatching,
native-pending, and interrupting items cannot be removed. Queue actions retain the normal
chat-session restrictions; graph-node and observed subagent sessions are excluded.

Queue entries and prepared attachment payloads are saved before acknowledgement,
with a maximum of 32 pending entries per conversation. Payloads are separate from
HTTP/SSE projections. Selected passages and mention metadata enter user history
when the message is dispatched/accepted. Queue delivery retains the existing
one-turn reservation and uses the native session, model settings and grant flow.

Pi's owned extension pulls one combined input at `before_agent_start` / `turn_end`,
without changing shared Pi settings. Payloads remain durable until the correlated
native user/custom-message event; a runtime with unconfirmed steering is discarded.
Codex uses `turn/steer` with `expectedTurnId` and `clientUserMessageId`; its 15-second
ACK only marks input accepted. A matching native `userMessage.clientId` confirms
incorporation. OpenCode's message-transform hook pulls the batch, persists it with
`noReply`, appends the complete result to the current model snapshot, then confirms
consumption over the private bridge. Header-only SSE events never erase payloads.
OpenCode still owns loop completion; its parent-message bookkeeping can cause an
additional model iteration after a boundary-injected prompt. Validate response
ordering with the installed harness before relying on this behavior.
Interrupted native-pending items become `uncertain`. Explicitly sending an
uncertain or native-pending item creates a fresh correlation ID; retry can duplicate
an input already consumed. No uncertain delivery is automatically replayed.
Composer and queue-row Send now share browser submission invalidation across
main, side and floating chats, including unsent HTTP tails.

Manual validation: during a long review tool, press Enter three times; verify all
pending instructions reach the next model interaction. Click Send now and verify
the current tool/subagents stop and the selected input starts after shutdown.
Repeat in main/side/floating chats with all three harnesses. Check attachments,
pending questions/permissions, explicit Stop during restart, end-of-turn races,
reload, engine restart, and delivery errors. Live harness behavior still requires
human validation; no full suite was run.
