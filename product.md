# Product

## Definition

The product is an interface for users to interact with coding-agent harnesses.
It provides its own interface features and delegates work execution to connected
harnesses.

The user interacts with this software, while harnesses such as Pi, OpenCode,
CoPilot, Codex, and CloudCode carry out the delegated work. These names represent
integrations mentioned in the product vision, not a list of integrations already
implemented. Official names and integration methods will be confirmed as each
integration is defined.

## Product Role

- Be the interface the user interacts with.
- Provide features that support that interaction.
- Connect to external harnesses and delegate the requested work to them.
- Present the progress and results made available by the harnesses to the user.

The product is not intended to replace harnesses or reimplement their execution
mechanisms. The interface is the interaction layer; harnesses are responsible for
executing the delegated work.

## Provisional Terminology

"Orchestrator" is a provisional description, not a definitive classification.
"Interface for coding-agent harnesses" remains the product description. Within
graph execution, orchestrator specifically means the main-chat agent invoking
user-authorized activities; it does not imply unrestricted autonomous coordination.

In this document, "harness" means the external software that receives delegated
work and manages its execution with an agent. It is not just the language model.

## Current Definition Boundaries

The first real slice is a separate Go engine with persistent projects and sessions,
and a React client. A project has a name, directory, and icon. Ordinary sessions belong
to one project and use Pi, OpenCode, or Codex headlessly. The client presents the
messages, exposed reasoning, commands, and MCP/tool events those harnesses emit.

Top-level sessions are grouped by persistent, project-scoped **topics**, replacing
visual session folders. A new independent project session starts without a topic.
During its ordinary turn, as soon as the objective is understood, its main agent
sets a short initial topic name through the private `session_topic_set` tool.
Agent assignment is one-time: repeated calls return the current topic unchanged.
Only the user can edit its name/color or move a session to another existing active
topic through UI. Equal names do not merge identities.
The engine gives spawned sessions and further descendants the source main topic ID,
including creation from a side chat. Spawn waits for initial assignment; it does not
invent a topic or create an extra classification turn. The general agent has no
topic and its newly created project sessions assign their own topics.

The shared sidebar displays topic blocks with a colored left border and collapsible name headers,
preserving session selection and runtime LEDs. Sessions without a topic appear in
**Ungrouped**. Members are newest-created first with stable ID ties; groups initially
use the newest-created persisted member, including archived members, with topic-ID
ties. Users can drag a topic's header above/below another topic to save a manual
project-wide order. Once positioned, topics retain that order even when members
move; new unpositioned topics appear ahead of it. Messages and runtime disposal
do not reorder them. Collapse is local per
browser/WebView, engine, project and topic ID; old folder-name collapse preferences
are not applied to the new topic namespace. Search includes topic names and titles
and temporarily expands matches without overwriting collapse preferences.

Desktop and browser session rows also display a recency subtitle below the title,
using the mobile convention: the latest user/assistant message, relative minutes
or hours, Yesterday, days up to 29, then a calendar date. Sessions without messages
show No messages yet. Subtitles refresh every minute while visible and on return
to the client; runtime LEDs remain independent, and recency does not reorder rows.

On desktop and wide browser layouts, the sessions sidebar can be resized by dragging
its right edge. Its width is remembered locally per browser origin and engine,
shared across projects, with a 256px default and 200–520px bounds. The available
window width and any docked side chat further limit expansion to preserve the main
chat's usable area. Double-click resets the preferred width; a focused divider
supports Left/Right, Home and End. Narrow navigation and the Flutter-hosted mobile
layout retain their existing presentation without a resize handle. Temporary window
constraints do not replace the saved preferred width.

The chat navigation's topic tag opens a shared name/color editor. Colors use a small
theme-aware palette (Neutral, Blue, Purple, Green, Amber, Rose), initially Neutral.
Color is limited to group outlines/headers and tags; row selection and runtime LEDs
retain their existing styles. Users can drag an active session onto another active
topic, including a collapsed topic, which expands after a successful move. The
session keeps its identity, history, runtime, settings and Library selection; its
side chat follows the main's topic. Already-spawned sessions are not moved, while
future spawns inherit its current topic. Menus provide Move up/Move down for topics
and Move to topic for sessions as keyboard/touch alternatives. Ungrouped stays last;
it can supply sessions to a topic but is not a destination for clearing an assigned
topic. Archived content must be restored before moving/reordering. Legacy folder
creation/move controls and manual ordering of sessions within a topic remain absent;
global New session never inherits the selected topic.

Sessions and topics can be archived and restored. Archived history remains readable,
runtime state is retained, and archive is not Stop. Archived sessions/topics do not
contribute rail/Happening now activity and cannot receive new execution without
restoration. Pending linked answers and graph-result chat notifications remain
deferred while archived; they can deliver after restoration without stopping
independent work. Restoring a session in an archived topic requires restoring the topic
first, without changing membership. Topic identity survives Stop, dispose and engine
restart. A versioned durable migration converts each legacy project folder into a
distinct topic, retaining archive state; Ungrouped remains absence of a topic.
Historical spawn requests/receipts are retained unchanged. No history classification
or graph execution-workspace changes are included. Delivery and compile evidence,
with pending human acceptance, are recorded in `TOPICS_IMPLEMENTATION_PROGRESS.md`.

Assistant message actions are hidden while that message is streaming. Completed
assistant messages provide Copy and a durable Favorite toggle; like and dislike are
not used. Failed or cancelled partial responses remain copyable but cannot be marked
as favorites.

### Project Settings and GitHub Webhooks

The **Project settings** button in the sessions sidebar opens a modal with
**General**, **Webhooks** and **Tasks** sections. General edits the project's name and icon,
shows its fixed directory and provides confirmed project removal. Project creation
and the rail's Edit/Remove menu retain their existing entry points. Webhooks belong
to this project modal, not global Settings.

Each project can have multiple independent GitHub connections, with their own name,
secret, stable ID, endpoint, enabled state and explicit event retention. The UI
provides Add and paginated rows showing name, retention, time since the
last accepted receipt (or Never) and an enable/disable toggle. The visible list
updates these timestamps and reloads its loaded rows every 30 seconds, pausing
while hidden or during editing/mutations.
Selecting a row opens read-only labels/text with Copy URL and an Edit action;
editing exposes configuration and enabled state. Details also provide confirmed
removal. Removal deletes the connection and all its stored events,
pending entries and releases. Secrets are required, entered by the user and never
returned by the API. The creation form generates a random 256-bit secret using
Web Crypto; an inline reload button generates a replacement. The value remains
editable and copyable and is discarded after saving or closing. Editing an existing
connection requires opting into Change secret before replacing its credential.
Changing a secret clears its previous ping verification.

Copy URL uses the configured engine base plus `/api/webhooks/github/{webhookId}`.
The local engine uses port **7331**, or **17331** in development, rather than the
frontend's port. Existing remote bases, explicit ports and path prefixes are
preserved. The user manually configures a GitHub repository or organization webhook
with this receiving path, **application/json** and the same secret. GitHub must be
able to reach it: any HTTPS proxy/tunnel setup and public URL substitution in GitHub
are the user's responsibility. KLM does not manage a tunnel, public URL field,
GitHub registration, OAuth or PAT. An ingress-only proxy should forward just the
required receiving path, preserving body bytes and GitHub headers, without exposing
engine administration. After a durably accepted signed `ping`, the connection detail shows
**Verified** with `lastPingAt`; this is historical receipt verification, not an
online status. GitHub redelivery after an outage or uncertain response is manual;
automatic GitHub retries are not assumed.

Reception requires HMAC-SHA256 over the exact raw body, a valid UTF-8 JSON object,
and single signature, delivery and event headers. Original payload bytes, event
type and optional action are preserved. HMAC authenticates the body, not the
delivery/type headers. A connection plus `X-GitHub-Delivery` deduplicates while its
event remains retained and unexpired; duplicates keep the same event ID and do not
refresh receipt time, verification, TTL or sequences. After expiry or deletion,
that delivery ID may be accepted again. New acceptance persists the event and
pending entry together before returning 202; retained duplicates return 200.

An engine worker releases accepted entries asynchronously into durable internal
storage, independently of clients. **Emitted** means internally available, not
processed by a feed, agent or automation. Pending entries resume after restart;
existing releases retain their sequence and timestamp. Metadata/history and raw
payload detail are available through project-scoped APIs. Released events use an
independent persistent release sequence, with decimal-string `after`, `nextAfter`
and `highWatermark` cursors. Reading does not consume events. SSE and in-memory wake
signals are not durable webhook delivery. The KLM Agent timeline now reads retained
releases through a separate global, descending projection. Consumer progress storage
and automatic integration with sessions, agents or graphs remain outside this slice.

Retention starts at UTC engine receipt, not GitHub occurrence or internal release:
**1 day**, **3 days**, **1 week**, **1 month**, **3 months**, **6 months**, **1 year**
or **Indefinitely**. Days/weeks are 24/72/168 hours; months/years use calendar
arithmetic clamped to the last valid day of the destination month. Retention changes
affect only newly accepted events. Expiry also removes pending entries and releases;
expired events are unavailable even before physical cleanup. Initial engine limits
are 25 MiB per payload, two simultaneous ingress bodies, 100 connections per project,
1,000 connections per engine, and 10,000 events or 256 MiB of raw retained payloads
per engine. Ping, pending entries and removed projects count toward capacity;
Indefinitely does not remove quotas. Pages default to 50 items, with a maximum of 100.

Disabling a connection rejects new deliveries but continues releasing already
accepted entries. Removing a project preserves its identity, connections, secrets,
endpoints and retained events; new ingress and webhook management/reads become
unavailable, pending release pauses, and TTL continues. Restoring the same registered
directory restores its identity and resumes only still-valid pending entries.

Backend/frontend implementation is delivered; owners reported a successful Go dev
build and TypeScript check. A short isolated local API smoke verified signed ping
acceptance, invalid-signature rejection, deduplication, durable release, original
payload and retained release/deduplication after restart. UI, real GitHub reception,
pending recovery, retention and full lifecycle still await human validation. Setup,
API contracts and the manual acceptance route are documented in `engine/README.md`
and `GITHUB_WEBHOOKS_IMPLEMENTATION_PLAN.md`.

### KLM Agent

The general agent is presented as **KLM Agent** in the client. Its view splits the
available area between the existing chat and an X-inspired activity feed, initially
one-third chat and two-thirds feed. The divider supports dragging and keyboard
resizing, with a 360px minimum per pane and a locally remembered split. Double-click
restores the default proportions. Hovering or focusing the divider reveals a
centered swap button that exchanges the panels while retaining their widths and
content. Panel order is also remembered locally.
The timeline displays real released GitHub deliveries across active projects, with
project icons, GitHub provider, factual action/result, safe resource links, category
and summary search, on-demand compact Details, and in-memory bookmarks. Feed settings
starts with all 56 event families enabled; local preferences are scoped to browser
origin and engine URL and affect display only. The push-ruleset bypass preference
uses the documented request discriminator; its canonical header is still unconfirmed.
No local fixtures or internal agent posts appear in the live timeline.
The KLM Agent can explicitly consult all available feed posts using its private
`feed_read` tool, paginated newest first across nonremoved projects and independent
of client-side category/event preferences. A post ID reads the same compact Details
as the UI. Reads share the feed's release/retention rules, do not consume events,
and do not trigger monitoring or execution. External post content is reference data,
not agent instructions or user authorization.
Workflow runs are projected as one stable card per explicit run/attempt identity,
with independent actual job states and paginated Activity/Details. Deployments keep
their own state and join a run/job only through an explicit unambiguous provider
relationship. Unresolved checks/suites stay individual; other event families are not
grouped. The projection is derived from retained original events before pagination,
without persisted backfill or TTL changes. See `GITHUB_ACTIVITY_GROUPING_ROLLOUT.md`
for the v2 card/member API and the still-pending activation/review procedure.
When new posts arrive while reading below the top, the visible post keeps its
position. A floating accent pill below the category bar shows an upward arrow and
up to three distinct project icons (plus an overflow count). Clicking it scrolls
to the top; reaching the top manually also clears the notice. Initial loads,
older-page loads and filter changes do not announce new posts. Reduced-motion
preferences disable smooth scrolling.
**Happening now** uses the live session inventory across active projects.
It includes normal top-level sessions with a running turn, a retained active runtime,
pending permissions/questions, or a failed state, including independently spawned sessions,
and excludes archived sessions, sessions in archived topics, removed projects,
and general/side/subagent/graph-node conversations. Stopping a session or retaining
an idle runtime does not close it, but an idle session without an active runtime or
pending request is excluded. Cards show the real project icon, session title,
and latest exposed reasoning title when available. Failed takes precedence over
Needs attention (pending permissions/questions), then Working, then Idle; status
dots reuse the sessions rail's runtime styles and reduced-motion behavior. Reasoning
prefers an explicit title, otherwise the first meaningful text line, with simple
Markdown formatting removed, whitespace collapsed and a 160-character limit. The
subtitle is cleared when the turn ends. A new turn starts without a subtitle and
only reasoning emitted during that turn can populate it; prior-turn reasoning is
never replayed as the current subtitle.
Sessions initially appear newest-created first so newly spawned sessions are visible
immediately in the three-item collapsed list. Users can drag cards to reorder them,
with a visible insertion indicator, or use Alt + Arrow keys on a focused card.
Manual ordering is remembered locally per browser origin and engine and shared
between the KLM Agent list and the Focus widget. Newly appearing sessions without
a saved position precede the saved order, newest first; streaming does not reorder
cards. Dropping a card does not open its chat. Show more expands a scrollable list with a persistent
Show less control, including when the active list shrinks, and an empty state appears
when none are active. Its column has a separate draggable divider and
centered hover/focus swap button, retaining its width and side locally. It starts
at 230px, with a 200px minimum and at least 320px left for the timeline; double-click
resets the width. Timeline and sidebar scroll independently in the column layout.
It moves above the feed in narrower split views, where this divider is hidden;
compact windows switch between Chat and Activity. Happening now uses the existing
engine state/update stream. Activity polls while visible and refreshes on focus;
refresh reloads its contiguous loaded window before replacing it, preserving the
pagination boundary. Retention/deletion and removed projects limit availability;
readers do not consume deliveries or extend retention. No automatic agent monitoring
or event-triggered execution is included.

Clicking a Happening now session opens its live chat as a shorter overlay above
the KLM Agent composer, flush with the chat column's sides and bottom and rounded
at its top corners. A translucent backdrop, light in light theme and dark in dark
theme, covers the general conversation
and its Chat header; the foreground overlay and composer remain undimmed. The
overlay casts no shadow and its bottom blends into the composer area without a
border. The composer, model/settings,
queue, Stop and pending requests target the
overlay's real project session. Drafts remain separate per session; closing the
overlay returns the composer to KLM Agent without stopping either conversation.
Clicking the translucent backdrop also closes the overlay and restores the general draft.
Both chats remain subscribed while the overlay is open. Its header has Close and
Open in project actions; the same project-navigation action appears on row hover
or keyboard focus (always visible on touch). Navigation selects the owning project,
expands the session's topic and opens that session's Chat tab. On compact screens,
opening an overlay switches Activity to Chat. Archiving the overlaid session or its
topic, or removing its project, closes the overlay.

The KLM logo at the top of Project Rail opens one persistent general-agent
conversation per engine. Its identity and history are independent of the selected
project and available even with no projects. Project Rail remains visible; the
sessions sidebar and responsive Open sessions control are absent in this view.
Returning to a project retains its normal session selection.

Before the first accepted message, Pi, OpenCode and Codex choices appear immediately
above the composer, with unavailable harnesses disabled. A draft does not lock the
harness. Acceptance permanently locks it in both engine and UI, including messages
accepted into the queue, deleted queued inputs, and failed turns. Model and effort
remain editable between turns through the ordinary controls.

The general agent uses the same session history, native harness adapters, queue,
streaming, Stop, permission and question flows as project chats. It is engine-owned,
excluded from ordinary project session lists and project removal/archive behavior,
and cannot be created through ordinary session creation, renamed, moved or archived.
Its stable execution directory is `workspaces/general-agent` below the engine data
directory, separate from stored engine data and user projects. Development and
installed engines retain separate conversations and workspaces. Permission choices
use session/global scopes, with no project-level lifetime.

`engine/prompts/general-agent.md` is loaded through the internal prompt mechanism
for all three harnesses. The general agent has a dedicated private tool catalog for
projects and top-level conversations across nonremoved projects in this engine.
It can inspect side chats through their main relationship, read/search bounded
messages and activity updates, inspect context usage and pending questions, and
list destination models and project graphs. A compact observed project/activity
snapshot accompanies each general turn; changing it does not start another turn.
Retained runtimes are distinguished from working turns; unknown context usage
remains unknown.

On the user's request, the general agent can register a project with `project_add`
using a name (1 to 60 characters) and an absolute existing directory on the engine
computer. This works before any project exists and uses the normal persistent
registration and client inventory updates. It returns `projectId`, `name`, `path`
and `outcome` (`created`, `existing` or `restored`), available immediately to project
inventory and session creation options. An active matching project is unchanged;
a removed match retains its ID, sessions/history and visual grouping, using the
requested name and default icon. Multiple matching legacy records conflict with
their IDs instead of choosing one. Duplicate detection and mutation share one lock;
matching cleans paths and ignores case on Windows without resolving aliases,
junctions or symlinks. Invalid names/paths and persistence failures return errors.
The path is never inferred from a client, selected project or general workspace.
Registration does not authorize sessions, graphs or file operations. This capability
is exclusive to the general agent and independent of Data Sources; directory creation,
cloning, Git initialization, dependency installation and project editing/removal are
outside it. Manual project registration keeps its existing HTTP contract.

The general agent can send visible, self-contained instructions to project chats
and ask questions with correlated replies. It can rename session titles,
atomically update model/effort/YOLO between turns, select a graph without executing
it, and answer the exact pending native question through its owning conversation.
Topic association and name/color editing are not exposed as its tools. Archived history is readable; sending
and consulting require effective restoration. Engine-owned
identity is not a synthetic project or a grant of harness permissions.

Its dedicated tools can create normal top-level sessions in an explicitly chosen
registered project on the user's explicit creation request, with a self-contained
prompt and a durable sender-scoped receipt referencing an actual human message in
the general conversation. The child uses the project's registered directory;
it starts without a topic and classifies itself during its normal turn. Harness/settings and YOLO inherit under the
existing spawn rules, with explicit requested overrides applied before its first
turn. Creation acceptance is not work completion and creates no subscription.

The general agent can Stop project main/side chats, remove an exact unsent queue
item, apply its existing Send now control, archive/restore normal top-level chats,
and reply to an exact pending permission through its owning conversation, including
projected graph requests. Stop pauses unsent input and cancels owned chat processes,
not an independent graph. Uncertain native delivery requires the user's explicit
retry choice and may duplicate delivery. Archive/restore is metadata and preserves
runtime state; restoring under an archived topic requires the user to restore that
topic first, preserving membership. Send now requires effective
restoration; Stop, removal and replies to existing requests remain recovery controls.
Permission decisions use only offered choices, preserve the user's requested scope,
and retain native-session/project/global grant ownership, rollback and attributed
agent provenance. None of these replies become human graph/spawn authorization.

No Hermes, linked side agent for the general conversation, project file mentions,
general graph invocation, automatic monitoring or completion notifications is included.
The activity timeline and Happening now are engine-backed. The GitHub feed passed
Go/TypeScript compilation and a disposable local HMAC → release → feed/Details smoke,
including filtering, pagination and retained history after restart. The standalone
simulator covers 56 families in 124 partial synthetic scenarios (55 confirmed headers,
one explicitly synthetic bypass header). Targeted Playwright checks covered cards,
settings/reload, filters/search, bookmarks, responsive layouts, refresh recovery and
a 105-event arrival across multiple pages while preserving the reading position.
Human UI acceptance, real external GitHub delivery, elapsed TTL and remaining
lifecycle cases still await validation.

### Windows Delivery and Focus

The engine and Tauri 2 desktop client are installed separately. `klm start` starts
the engine in the background and returns after readiness; `klm stop` requests
graceful shutdown through a local, per-user Windows channel. The engine installer
adds user PATH and starts the engine at login via HKCU Run. Data stays under
`%APPDATA%/klm/engine`; uninstall preserves it. Stopping manually does not disable
the next login start. There is no pre-login Windows service.

The public HTTP API listens on `0.0.0.0:7331`. Network authentication and TLS are
deferred by the approved personal-use contract; grants, sandbox and the private
authenticated loopback bridge keep their existing boundaries. Commands, project
files and native directory dialogs always belong to the engine computer.

The public API accepts any HTTP Host and any CORS origin, including tunnel/proxy
domains, without an allowlist. This does not change the private authenticated
loopback bridge. Frontend Settings accept an engine IP or domain, with optional
HTTP(S) scheme and port. IPs without a port use 7331; explicit ports are preserved.
Domains receive no added port. Without a scheme, IPs use HTTP and domains use
HTTPS. A tunnel subdomain change only requires updating the frontend engine URL.

The desktop fills its native window. Closing it hides to the tray; **Open** restores
it and **Exit** ends only desktop/web. The client neither starts nor stops the
engine. Opening another desktop shortcut restores the same instance.

While Tauri is active, it serves the same built application on `0.0.0.0:7332`.
The **Focus** header button, between **Session log** and **Side agent**, opens
`http://localhost:7332` in the default browser. The browser opens with the gradient
and draggable central window. In place of Focus, a Maximize window icon toggles an
opaque, edge-to-edge layout that follows the browser viewport; Restore window returns
to the floating layout, including its resized dimensions. The floating window can
be resized by dragging any edge or corner, with the matching resize cursor and
no visible resize icon. The invisible resize targets support arrow keys when
focused. Minimum usable dimensions apply, but there is no maximum size or
viewport-based resizing limit; oversized windows remain draggable.
Resizing is unavailable in the maximized layout, narrow mobile layout and
Flutter-hosted WebView. The toggle is absent in the Flutter-hosted WebView. Another device
uses `http://IP-OF-ENGINE-COMPUTER:7332`; API and SSE use that same host on 7331.
Ports remain fixed; collisions are reported instead of selecting another port.

In the browser's floating Focus layout, right-clicking the gradient opens a
**Widgets** cascading menu with checked toggles for **Happening now** and **Feed**.
The background picker also accepts a local image in place of its gradient presets.
Images cover the viewport while retaining their aspect ratio, cropping overflow
around the center rather than stretching. Image mode exposes **Blur** (0–80px),
**Mask** opacity (0–100%) and a mask color picker; the mask is a separate layer
above the blurred photo. Images can be replaced, removed or reselected after
switching to a gradient. The photo is stored in browser IndexedDB and adjustments
in localStorage, so they survive refresh/tab closure on the same browser origin.
Images are decoded before saving, limited to 20 MiB and never uploaded to the engine.
Each widget has its own softly shadowed, draggable and edge-resizable container.
The floating Happening now widget renders all matching sessions without Show more
or a fixed item limit; its list uses the available height and scrolls on overflow.
Happening now initially sits at the upper left and Feed at the right, following
the supplied visual reference. Position, dimensions and enabled widgets are saved
locally per engine; toggling a widget off and on restores its geometry.
The Focus layout survives refresh and closing/reopening the browser tab in the
same browser origin and engine: open session windows (including KLM Agent), their
opening order and foreground window, widget toggles, positions and sizes, and the
main window's position, dimensions and minimized/maximized state. Saved session
IDs are reconciled only after engine inventory loads, so a disconnected initial
load does not discard the layout. Archived/unavailable sessions are omitted after
reconciliation. Smaller viewports keep window headers reachable; entering a
narrow layout does not overwrite the saved floating geometry.
Clicking a Happening now row selects its project chat in the main window when
that window is open. When the main window is minimized, the row opens or brings
forward the session's chat-only floating window, keeping the main window minimized.
The row's floating-window icon always opens or brings forward that same live
session in an independent chat-only window within Focus, with its
existing composer, draft, queue, requests and Stop. Closing this window does not
stop the session. Session windows close when their session/topic is archived or
their project is removed. Their geometry is retained locally for reopening.
In Focus, the session navigation also has a floating-window icon to the left of the topic
tag. It opens or brings forward that session's dedicated chat window and
automatically minimizes the main window.

The KLM Agent's Chat header and the minimized main-window bar also offer an
**Open KLM Agent in floating window** icon. Both open or bring forward the same
chat-only window for the engine's singleton general conversation. Its draft,
history, model, harness selection/lock, queue, requests and Stop are shared with
the main view. It remains available when switching projects or minimizing the
main window. The general chat has no rename action or project graph selector;
its Open in main window action returns to the KLM Agent view.

A **Minimize window** button immediately before Maximize hides the main window
while keeping its state, leaving a small bottom-centered bar with **Restore main
window**. The bar offers the shared theme picker and Settings, usable without
restoring the main window. Theme selection stays synchronized with Project Rail.
Widgets remain available
while the main window is minimized; maximizing hides the floating widgets until
the main window is restored or minimized. These controls are absent on native
desktop, the mobile host and narrow browser layouts. TypeScript validation covers
compilation; visual layout, dragging/resizing and live interactions await human
acceptance.

Local development uses API **17331** and Tauri Focus/web **17332**, with Vite HMR
on **5173**. Engine builds with the `dev` tag use a separate `engine-dev` data
directory and control channel; the dev desktop has a distinct app identifier so
development and the installed release can run concurrently.

Persisted data comes from the shared engine. Drafts and local browser/WebView state
remain independent. The desktop must stay active, including in the tray, for page
reloads/assets in Focus. Client autostart is outside this delivery. Packaging builds
and human runtime validation are tracked in `ENGINE_DESKTOP_IMPLEMENTATION_PROGRESS.md`.

The engine remains independent of the client. A private, KLM-managed MCP bridge
connects linked conversations for owned OpenCode and Codex processes; Pi uses an
automatically loaded extension for the same operations. A general-purpose public
engine MCP interface remains outside this slice.

The private bridge also exposes graph operations according to the conversation's
role. Graph-node sessions receive their own Choice capability, without access to
main/side conversation history or tools to invoke other graphs.

### Skills and MCP Library

Settings → **Library** manages engine-shared user skills and MCP connections. User
skills are complete first-level folders under `~/.klm/skills` on the engine computer;
the filesystem remains their source of truth. Invalid entries display diagnostics.
Installation is requested through KLM Agent using existing tools and its private,
editable `engine/skills/skill-library-manager` skill. Installing does not select a
skill. That internal skill is distributed only to the general-agent role, never
through external selection or inheritance. The engine loads its captured SKILL.md
and installation-format instructions into each KLM Agent turn on Pi, OpenCode and
Codex, together with the absolute engine-user library path. Native installers' or
discovery skills' `.agents/skills` defaults do not choose the KLM destination.

The status-row Library button replaces the reference skill/MCP counts and opens
the selection for its actual composer conversation, including overlays and Focus.
Side conversations expose Library in Session options without status-row counts.
Selections are positive lists of skill IDs, connection IDs and explicit known tool
names; selecting a connection also permits its resource discovery/read tools.
Selection edits take effect on the next complete engine turn. Steering retains the
current snapshot; Send now's replacement uses the latest selection after confirmed
shutdown. New side/spawned conversations copy the originator's applied external
selection (saved selection before any application), then evolve independently.

Agent TOMLs include `skills` and `mcp_connections`. New graph runs capture local
configuration, known MCP catalogs and content-addressed skill copies at actual
start, not scheduling. They do not follow later edits. Execution-directory-relative
stdio catalogs use the known catalog until an activation can connect in its real
directory; a changed/missing remote contract is diagnosed rather than injected.
Captured selections are available read-only in node activity. Legacy runs remain
readable without being relabeled as exclusively managed.

Managed Windows profiles target **Pi 0.85.1**, **OpenCode 1.18.35** and
**Codex 0.153.4**, with version/preparation gates and effective catalog checks before
prompting. Pi uses explicit snapshot paths and its owned extension. OpenCode uses
a private discovery/config root, projects provider/model/instruction/native
permission settings, disables unrelated project/external skill discovery, and
applies positive native skill permissions including under YOLO. Repository
instructions remain available. Native build/plan root policies and
`OPENCODE_PERMISSION` overrides are preserved; unsupported custom roots or
file-substituted instruction paths fail explicitly. Native auth/history
data and the real execution directory remain in place. Organization, well-known
remote-config authentication and administrator-managed OpenCode config require a
separate compatible path and are explicitly refused rather than silently imported.

Codex keeps its native home/auth/history and approval/sandbox flow, disables native
model-side skill discovery plus plugin/app/hook/delegation sources, and supplies
only selected captured skill metadata for on-demand file loading. Its app-server
disk inventory can still list host skills; that inventory is not used as the model
catalog. Before thread start/resume, known native MCP configurations are projected
to disabled entries alongside the two owned gateways, then the entire thread
registry is checked. Native subagent tools are unavailable in this managed Codex
profile; independent KLM sessions and graph agents retain their normal behavior.

OpenCode/Codex use a fresh managed process per complete engine turn and resume the
same native history; Codex waits for EOF shutdown before retiring it so the native
transcript can flush. Changing selection during execution still waits for the next
complete turn. Other versions/configuration variants fail preparation explicitly.
The mandatory general-agent installation guidance remains independent of external
selection and is delivered as engine-owned role context on all three harnesses.

The engine is the MCP client for stdio and Streamable HTTP. Harnesses receive only
authenticated loopback gateways and the turn's allowed catalogs, never upstream
credentials. Conversation-role tools (`klm_linked`) and external Library tools
(`klm_library`) have different tokens/catalogs/dispatch scopes; the Library token
cannot call privileged role/control methods. OpenCode native children receive only
the external selection, not root conversation-role tools or the internal manager.
Tools, resources and form/HTTPS-URL elicitation are implemented;
Prompts, Sampling, legacy HTTP+SSE and resource subscriptions are not enabled.
Elicitation uses correlated pending questions with Accept/Decline/Cancel and is
never answered by YOLO or general-agent question tools. Local MCP processes have
their own owned lifetime and Stop cleanup; this is not a filesystem/network sandbox.
Gateway workers participate in graph sealing/drainage. Lost transport remains
uncertain and calls are not automatically repeated.

OAuth supports discovery, public dynamic registration or a configured client ID,
Authorization Code/PKCE, user-bound Windows credential protection, serialized
refresh and local disconnect. Remote/mobile clients require a provider-accepted
HTTPS redirect reaching the dedicated public API callback; browser localhost is
not substituted for the engine. The mobile web flow uses explicit same-tab
navigation and a return-to-Library link. Providers' embedded-WebView policies and
real desktop/mobile login still require human validation. The public personal-use
API's existing network contract is unchanged.

Missing/offline MCP connections warn the model and user while retaining selection.
Invalid selected skills, corrupted snapshots and isolation failures stop preparation.
Confirmed deletion previews session/agent/run impact, removes future references
through a recoverable authoring/state outbox and preserves captured runs/active
turns. Unresolved filesystem conflicts remain recoverable errors. Existing sessions
migrate to empty external selection without importing native global configuration.

Implementation and validation evidence are tracked separately in
`SKILLS_MCP_LIBRARY_IMPLEMENTATION_PROGRESS.md`. Compilation and disposable probes
do not constitute human acceptance of model execution, OAuth providers or lifecycle.

### Audio Transcription

The main chat composer can record audio and transcribe it through the engine without
sending the resulting text automatically. Transcribed text is appended to the current
draft while preserving selected file and folder references. Side-agent and design-system
composers do not expose recording.

The engine stores the OpenAI API key, model, optional language, and vocabulary globally.
The API never returns the key, only whether one is configured. Vocabulary notes remain
available as engine-backed context for the interface; this first slice sends only terms
as transcription prompt context. Audio uploads are limited to 25 MiB.

Desktop and ordinary browser recording require `getUserMedia` and `MediaRecorder`.
Browser security rules may deny microphone capture on insecure remote origins, so remote
HTTP browser access is not promised to support recording. The Flutter-hosted mobile
WebView instead uses native microphone permission and AAC/M4A recording, including on
HTTP LAN hosts. Its separate audio bridge is restricted to the exact saved frontend
origin and forwards bounded audio chunks to the existing engine transcription upload.
Cancel discards audio or aborts transcription; backgrounding, navigation and disposal
release capture and temporary files. Recordings are limited to ten minutes and 25 MiB.
Transcripts append to the current draft and preserve references without sending. This
requires an updated mobile app and frontend and does not change the existing personal-use
network and authentication contract.

Permission prompts are supported for OpenCode, Pi, and Codex. Cards expose **Allow**,
**Allow session**, and **Deny**, with a menu for **Allow in this project**, **Deny in
this project**, **Allow globally**, and **Deny globally**. Rules persist in the engine;
global rules apply across its projects and sessions. The displayed operation/resource
scope determines matching, not the button's lifetime alone. Recognized filesystem
scopes can be shared across harnesses; opaque requests retain exact native scope.
Explicit denies win over remembered allows. Known destructive requests carry a
small warning icon. Pi and an owned OpenCode pre-tool plugin evaluate workspace
policy before execution; Codex uses the requests and sandbox controls it exposes.
Native grant lifetimes and unsupported forms retain their actual semantics.
Skill loading and identified MCP tool calls are automatically approved by the
engine's normal policy, without requiring YOLO. Standard installed skill-resource
reads are included; subsequent file changes and shell commands use their own policy.
Explicit saved denies still apply. Questions and MCP forms requiring user input
are not answered by this automatic tool approval.

The composer's vertical-ellipsis menu, immediately left of the harness/model
selector, initially contains only **YOLO mode**. YOLO is persisted per conversation
and can be enabled during work without stopping the turn. Enabling resolves pending
permissions and automatically handles future requests; disabling restores normal
engine policy for future requests without deleting rules or undoing approved work.
Pi and OpenCode gate new tool dispatch using the live engine setting. Native startup
configuration refreshes on the next turn. Codex uses `never` approvals and
`danger-full-access` when a turn starts in YOLO; that native full-access turn cannot
be safely downgraded, so disabling is rejected until it ends. A Codex turn started
normally can enable automatic engine approvals, with its native sandbox retained
until the next turn. Questions and graph authorization/lifecycle remain separate.
The normal workspace policy, precise matching rules, API recovery controls and
adapter coverage are documented in `PERMISSIONS.md`. Runtime behavior awaits human
validation; command classification is not filesystem or network isolation.

Agents can ask users questions through their harness tools. The interface shows
clickable choices and an optional custom answer, including multi-select when the
tool supports it. Answers are returned to the waiting tool, not sent as a separate
chat turn, and never become remembered permission grants. Users can dismiss a
question or stop the turn.
OpenCode questions and permission requests from native subagents are presented
in the owning conversation, including requests received before task metadata.
Question answers return to the originating native request; YOLO never chooses
an answer on the user's behalf.

## Mobile Client

**KLM Harness** in `clients/mobile` is a Flutter mobile client with a native list
of saved web clients and a form used to add or edit them. Each entry has a
user-provided name, a web client URL and an optional emoji; the server icon remains
the default. Entries can be created, edited and deleted, with changes persisted
locally across app restarts. The list does not imply an online-status check.

Selecting an entry opens its web frontend inside a WebView. An IP without an explicit
port uses the frontend default **7332**. A domain without a port gets no added port,
allowing tunnel/proxy URLs. Explicit ports and HTTP(S) schemes are preserved;
without a scheme, IPs use HTTP and domains use HTTPS.

Mobile registration optionally adds a local web-client URL after selecting one or
more Wi-Fi SSIDs. The original URL remains the public/default address; parsing and
port rules are unchanged for both URLs, and old URL-only records remain valid.
On opening an entry, a matching connected SSID selects the local URL. Other or
unknown networks select the public URL. Initial local-load failures fall back once
to a fresh public WebView; only successful public fallback displays a modal notice.
Both failures retain normal Retry recovery. Attempts have separate controllers and
origin-scoped audio capabilities, and existing connected sessions are not rerouted
when the network changes. Android discovers nearby networks; iOS can identify the
current network and accepts additional SSIDs by name subject to OS permissions.

The KLM logo at the top of the web project rail opens the general-agent chat.
A **Back to hosts** icon button below Settings returns to the native web client
list through `KlmMobile.postMessage('home')`. It appears only in the Flutter-hosted
WebView, not on desktop or in ordinary mobile browsers. While loaded, the WebView
has no additional native toolbar. Flutter knows
only the web client URL; engine selection and configuration belong to that web
client. Failed loads offer Retry and a native KLM home action. Mobile does not
start servers or relocate execution/files to the device. Native screens reuse
KLM's visual language through Flutter equivalents of its colors, typography,
inputs, buttons and cards.

In the Flutter-hosted WebView, opening a project session hides both rails and
lets its conversation fill the available screen. The header's **Back to sessions**
button replaces **Open sessions** and returns to a navigation screen with Project
Rail on the left and the project's sessions filling the remaining width. Selecting
a project shows its sessions; selecting or creating a session opens its full-screen
chat. Returning to navigation preserves the mounted conversation and its draft.
This navigation uses the APK's existing `KlmMobile` bridge, independently of viewport
size, and does not apply to native desktop or browser Focus.

Mobile session tiles have larger touch targets and a subtitle below the title.
Active sessions show the runtime LED followed by **Idle**, **Working**, **Failed**
or **Needs attention**, with failure and pending requests taking precedence.
Inactive and archived tiles show the time of the last user or assistant message:
relative minutes/hours, **Yesterday**, days up to 29, then a full calendar date.
Sessions without messages show **No messages yet**. Message recency comes from
conversation messages, excluding tools, consultations and metadata changes, and
relative labels refresh while the client is visible.

In mobile chat composers, Send is hidden and takes no horizontal space while the
draft contains no non-whitespace text. Typing reveals it with a short slide that
moves the other controls left; clearing the draft collapses it again. Stop remains
available during execution. Reduced-motion preferences disable the transition.

On mobile, the composer's three-dot **Session options** button opens a modal bottom
sheet containing the **YOLO mode** toggle and, for project chats that support it,
the existing graph selector. The composer no longer displays the YOLO label or
graph name; its three-dot button uses the warning-yellow color while YOLO is active.
This button stays anchored to the composer's bottom-left corner, with its own
reserved space as other controls move or wrap.
The sheet closes through Close, backdrop tap or Escape. Desktop and browser Focus
retain their existing options menu, YOLO label and composer graph selector.

The mobile KLM Agent view omits the outer title header and its session-log button.
The KLM Agent's Feed is the mobile home: each new WebView load opens it regardless
of the previously selected project or chat, retaining project/session selections
for subsequent navigation.
Project Rail starts hidden on the mobile KLM Agent page. A **Show projects** icon
inside the Feed search field toggles it without leaving the feed; returning from
a project or opening the app starts with the rail hidden again.
Its compact tabs are ordered **Feed**, then **KLM Agent**, replacing the previous
**Chat** and **Activity** labels. Desktop and browser Focus keep their existing header
and tab labels/order.

The mobile Feed omits the Activity header, project counter and refresh button.
Feed settings is an icon button inside the search field. Pulling down at the top
of the feed and releasing past the threshold refreshes the existing loaded feed
window, with a progress indicator. Short pulls and normal scrolling do not refresh.

Mobile Happening now displays every matching session in a vertical list, with no
Show more/Show less controls or internal scrolling. The full list scrolls with the
feed, preserving the existing session actions and ordering.

On mobile, tapping a Happening now session keeps the Feed tab selected and opens
that session in an edge-to-edge bottom sheet at 85% of the viewport height, with
rounded top corners and a modal mask above it. The sheet owns its composer, draft,
live history, queue, requests, Stop, model and graph controls; it does not retarget
the KLM Agent composer. Closing the sheet preserves both drafts and the feed's
scroll position without stopping either conversation. Archived or unavailable
sessions close the sheet. Desktop and browser Focus retain their existing overlay.

The same 85%-height mobile sheet opens sessions from **Created {session-name}**
cards in any conversation, retaining the underlying chat or feed. Subagent activity
entries also open in this sheet rather than switching the main chat's tabs; native
subagents retain their read-only history/status view and Back to parent-chat action.
Opening another session or subagent pushes a new sheet onto a mobile stack instead
of replacing the previous one. Covered sheets stay mounted and subscribed, retaining
their scroll positions and composer state. Close, backdrop tap, Escape or the
subagent's Back action dismisses only the top sheet and reveals the previous one.

## Tasks

One engine-global **Tasks** board projects tasks from GitHub.com and Azure DevOps
Services. Its global user-created phases must be mapped in every board connection;
connections explicitly bind one GitHub repository or one Azure project/work-item
type to a local project. The same board opens below Happening now on the KLM Agent
page, using a checklist icon. A **Tasks** button below New session appears only when
the project has at least one configured task provider, opening the same board with
that project's filter. The **Phases** action manages global phases. Provider setup,
editing, mappings, outbound priority and disconnect live in **Project settings →
Tasks**, directly below Webhooks. This section lists only the current project's
connections and fixes new connections to that project. Saving/disconnecting updates
the navigation immediately; drafts and failed saves do not expose the Tasks button.
Azure setup accepts organization and PAT, then lists accessible projects and the
selected project's work item types in searchable pickers. Choosing a type loads
its states. Project discovery requires Project and Team read access in addition
to the Work Items access used by the board; no incomplete connection is saved.
Azure inventory always uses `[System.AssignedTo] = @Me`, where `@Me` is the user
authenticated by that connection's PAT. There is no assignment toggle or option to
list other users' tasks. Existing connections adopt this rule automatically at
engine startup, withholding older unfiltered snapshots until a full filtered scan
succeeds. Replacing an Azure token likewise withholds the previous owner's snapshot
until refresh succeeds. This does not change assignees or remote tasks.
The Tasks toolbar has a searchable **Sprint** dropdown for Azure, with **All sprints**
to clear the filter. Choices combine deduplicated iteration paths in the confirmed
assigned-task snapshot with the provider-reported current sprint of each Azure
project's **default team**, as explicitly selected by the user. Current sprints
show **Current**, sort first and remain available even without assigned tasks.
Exactly one confirmed current sprint is preselected on each project visit; no
current sprint or multiple current sprints retains All sprints with a status note.
Metadata failures show Retry and require an explicit selection instead of silently
defaulting to All. Refresh never overrides a manual sprint/All sprints choice.
Choices are qualified by Azure organization/project. **No sprint** represents the
project root iteration. A specific choice filters exact paths and shows matching
Azure tasks only; All sprints retains the normal mixed-provider view. This is a
temporary view filter, not a connection setting or a remote sprint change. Other
empty iterations and non-default teams are not enumerated.
Task configuration drafts preserve their edit-start revision and require explicit
reload after conflicts or uncertain saves. Global phase mapping discovery can read
retained removed-project connections without permitting inventory/moves or label
creation for those projects. Missing move receipts have explicit cancellation of
the unaccepted operation ID; accepted provider work is returned as its existing
receipt, never undone or replayed. Refresh keeps its in-transit request through
Focus hide/restore and discards earlier GETs. Two requested static review/correction
rounds are recorded in `ACTIVITY_BOARD_REVIEW.md`; human runtime acceptance remains
pending.

GitHub uses labels on open and closed issues, excluding PRs; unmapped issues are
hidden and cross-phase label conflicts remain visible. Move to preserves unrelated
labels and open/closed. Explicit label creation is available during setup. Azure
uses real states for the selected work-item type and revision-guarded state updates,
without bypassing transition rules. Phase count has no product cap, but complete
coverage still requires enough distinct Azure states. Providers remain the source
of truth; local persistence is configuration, compact cache and update receipts.

PATs are write-only and Windows-user protected at rest. Inventory refreshes on
startup, connection save and explicit Refresh. Partial scans retain stale snapshots;
uncertain updates require read reconciliation and are never automatically replayed.
The existing personal-use public API network contract remains unchanged. No hook
consumer, Azure receiver or automatic agent/graph execution is included. Go dev and
TypeScript compilation succeeded; provider runtime and desktop/Focus/mobile behavior
still need human validation. Actual APIs, recovery limits and acceptance steps are
recorded in `ACTIVITY_BOARD_IMPLEMENTATION_PROGRESS.md`.

## Brand assets

The transparent sphere is used for KLM logos inside every client, the desktop
application/window/tray icon and the web favicon. The solid-background sphere is
used for the mobile launcher icon. Both source images are centered 1000×1000 crops,
without rescaling their original pixels. Platform-specific icon sizes are generated
from these crops. Flutter screen branding uses `assets/brand/logo.png`; its launcher
generator uses the separate solid `assets/brand/icon.png`.

## File and Folder References

In both main and side chats, typing `@` opens project file/folder suggestions.
Confirming a suggestion attaches that reference to the draft. Plain paths and
unconfirmed `@` text remain ordinary message text. Selected references remain
visible as inline badges in the draft and persisted chat history. Badges show only
the file/folder name; full project-relative paths remain in the message metadata.
They are atomic selections in the editor: deleting a badge removes its reference.
Failed submission preserves the draft.

UTF-8 text files contribute bounded content to the model's initial message.
Folders contribute their immediate children, including subfolder names, without
recursively attaching file contents. Empty files and folders are valid. References
must resolve inside the conversation's project; binary files, images, and PDFs are
not supported in this slice. Discovery respects nested `.gitignore` rules and
excludes `.git` metadata. Explicit folder attachments list real children, including
ignored entries.

KLM accepts up to eight unique paths per message and deduplicates their content.
OpenCode materializes native file parts with its own Read limits. For Pi and Codex,
the engine prepares user-message context: up to 2,000 lines and 50 KiB per file,
2,000 characters per line, or 2,000 entries and 50 KiB per directory. Truncation is
recorded and included in the attached context. The prepared JSON context is capped
at 200 KiB; oversized submissions require fewer/smaller attachments.

The engine persists references and preparation metadata, while the harness owns
the native message content, history, compaction, and subsequent tool execution.
Selecting context does not change tool permissions or sandbox settings.

## Messages During Execution

Main and side chats accept messages while a turn is active. Enter or the send
button submits **steering** for the next model interaction, after the current
native assistant/tool cycle, rather than waiting for the entire task to finish.
Pending messages are delivered together: the engine batches pending inputs;
Pi and OpenCode claim them through native boundary hooks. A message submitted
after that boundary belongs to the following interaction; text and tool calls
already emitted in one assistant response are not interrupted by ordinary Enter.

**Send now** interrupts the active chat and its owned native processes, then starts
a replacement turn with the selected message only after shutdown is confirmed.
When idle it starts that input immediately. Other pending inputs are preserved as
paused or delivery-unconfirmed, not silently replayed into the replacement turn.
Explicit Stop cancels a pending replacement. Interruption cannot undo completed
tool effects and does not stop an independently running graph.

Pi queues one combined native message without changing shared settings; Codex uses
`turn/steer` with expected-turn and client-message identities. OpenCode persists a
complete batched prompt and adds it to the current native model snapshot rather
than depending on delayed SSE acknowledgements. Native acceptance is distinct
from proof that the model acted on an input. Pi/Codex payloads remain durable until
a correlated native message event; OpenCode waits for all persisted parts. Pending permissions/questions
retain their own reply controls; ordinary steering does not answer them.

The engine stores up to 32 pending messages per conversation, including prepared
attachments. Batch size is bounded by the native transport limit. Stop, execution
failure, and engine restart pause unsent inputs; uncertain native delivery requires
manual recovery and is never automatically replayed. Unsent inputs can be removed;
Send now can select a pending input for interruption/replacement. Explicit API
`queue` mode and agent-authored `session_send` retain end-of-task FIFO behavior.
Stopping a chat cancels its turn and terminates its owned process container,
including native subagents and local tool subprocesses, without waiting for a
permission reply or a successful native abort. The stopped runtime is discarded;
the replacement resumes the stored native conversation in a new runtime.
Runtime behavior across the three harnesses awaits human validation.

## Linked Side Agent Conversation

Each main chat can have one persistent side conversation, attached to that chat
and excluded from the top-level session list. The side agent has its own native
harness session, model, draft, execution state, and stop control. Main and side
may independently use Pi, OpenCode, or Codex. A side harness can be changed before
its first turn; model settings apply to subsequent turns.

The Side agent header button opens the side chat (after Focus on desktop). Selecting a passage in
a main-chat message exposes **Ask side agent**, attaching that exact passage and
its source reference to the side composer without sending a prompt. Later
selections join the same conversation. Closing the panel hides it; reopening
restores its history. The panel is resizable with pointer or keyboard, remembers
its width, and occupies the full panel area when two usable chats do not fit.

Context starts with main-conversation identity, project, selected passages and
bounded nearby messages, plus the user's question. Selected passages remain intact;
either agent can retrieve more messages or consult the other. No generated summary
or native fork is required. Starting without a selection provides the same tools.

KLM's internal linked-conversation tools are automatically allowed on its owned
bridge. They do not require user permission cards or remembered grants. Identified
external MCP tool calls use the normal automatic-approval policy; filesystem actions
and command execution retain their permission flow.

Consultations run in the recipient's actual native conversation and enter its
native history. The engine distinguishes them from user turns and groups their
output under expandable activity entries with the question, answer, and status.
Idle recipients start answering; busy recipients queue until their turn ends.
Each conversation has only one active turn. Bounded waits and reciprocal-wait
deferral avoid deadlocks. Replies return through the waiting tool or a correlated
continuation after the requester yields. Requests persist cancellation, failure,
and restart-interruption states.

The side Session Status Bar shows its own context usage and input/output token
counts. Quota, skills, and MCP indicators belong only to the main status bar.

This feature does not imply simultaneous turns inside one native session or
automatic switching between harnesses during a task.

### Independent sessions and cross-session consultations

On an explicit user request to create sessions, a main or side agent can create
independent top-level sessions in the same project directory, each with its own
title, initial agent-authored prompt, native history, harness settings, permission
and question flow, and Stop control. The initial prompt is attributed to the
originating session and cannot serve as a human authorization message in the new
session. A durable operation ID prevents duplicate creation on retries. Creating
a session does not subscribe the creator to its progress or completion; normal
session controls and inventory show it to the user.

The creation event in chat is a clickable card with the session title, harness
icon/name, model and effort, without the internal session ID in its label. New
events capture the creation settings; legacy events fall back to available session
inventory. Default selections remain labeled Default instead of inventing resolved
settings. The main chat navigates to the created session's project; a floating chat
opens or brings forward a separate floating window for it. Archived/unavailable
destinations remain visible but cannot be opened through the card.

Opening a native subagent from a floating chat shows its existing read-only
activity and status inside that same window, with a Back to parent-chat button.
The main window remains in its current minimized/open state. The parent draft and
chat remain mounted, both histories remain subscribed, and the selected subagent
is saved with the Focus layout. Returning restores the parent chat's normal controls.

Spawned sessions inherit the sender's current YOLO mode by default. An explicit
`yolo:false` implements the user's request for a non-YOLO child before its first
turn; overrides are only made at the user's request. Later sender-mode changes do
not change an existing child. Spawn options expose exact model IDs/efforts, the inherited
topic and recent user message IDs. Validation rejects malformed or unavailable
settings before creating a child and returns an actionable error to the agent for
correction. OpenCode model IDs require `provider/model` format.

When the user mentions another session and checking it is relevant, agents can
discover top-level sessions in the same project, read bounded history by ID, or
consult their agents in their actual native conversations. Main/side consultation
without a target ID continues to work. Archived sessions can be read explicitly
but must be restored before being consulted. Correlated answers and bounded waits
use the existing consultation mechanism. Graph nodes and native subagents do not
receive these collaboration tools. The adjustable agent guidance lives in
`engine/prompts/session-collaboration.md` and applies from the next turn.

`session_send` is shared by normal conversations and the general agent, with
different authenticated scopes. Normal main/side agents can send instructions to
their existing linked agent or same-project top-level conversations within the
user's authorized work. An instruction uses the normal persistent FIFO queue and
appears as an attributed agent prompt, with normal visible tool activity and
output rather than consultation grouping. A durable sender-scoped operation ID
prevents duplicate acceptance even after queue consumption or reload. Acceptance
does not imply started or completed execution; sending creates no completion
subscription or automatic continuation. `ask` remains for information/clarification
and retains correlated replies. Agent-authored instructions and question responses
are not human authorization for graph execution or session creation.

## Agent and Graph Authoring

Project agents and graphs can be authored independently of graph execution. Each
agent is a TOML in `.klm/agents`; each graph is a YAML in the sibling `.klm/graphs`.
The project files are the source of truth. Agents use real harness model identifiers
and require effort only when supported by the model. Their prompts live in TOML.

Graph creation starts with a name and optional description. Save persists the graph
definition; node positions autosave on drag release to a separate layout file, even
before the draft's first Save. A layout alone is not a catalog graph. Saved graphs
can be reopened, edited, enabled/disabled and deleted. Agent references are preserved
when agents are renamed; referenced agents cannot be deleted or disabled.

Authoring includes terminal Choice output, explicit Terminal output mapping and
per-Fork-branch worktree isolation, enabled by default. Saving a draft is separate
from the engine's execution preflight. `.klm/harness.toml` remains deferred.
`GRAPH_AUTHORING_REFINEMENT.md` defines the approved contracts; its latest execution
and minimal-monitor decisions take precedence over earlier visual experiments.

## Graph Execution

The Go engine implements asynchronous graph invocation, persistent activities and
runs, and Agent, Choice, Terminal, Fork and Join execution. The main-chat agent
invokes work with references to the user's express authorization and a self-contained
task, available as `run.input.task`. The orchestrator catalog exposes each graph's
initial node ID, type and name, plus the associated agent's name and description
when available. The orchestrator prepares the task for that entry role (for example,
planning rather than implementation), while preserving the user's overall goal
in the objective. This uses existing agent descriptions, not a new input schema
or the full graph topology. Selection alone is not authorization. One run
may be active per conversation, independently of the invoking chat turn. The
orchestrator receives results and assesses whether the objective was satisfied
before success-dependent activities proceed. Corrective attempts retain the approved
authorization, concrete-correction and explicit fresh/reuse workspace rules.

Invocation default approved on 2026-09-13: omitting `workspace` uses `original`,
the conversation's registered project directory, without asking or requiring a
reference to a workspace-choice message. The product default is the basis for
that directory; omission is not express consent. Explicit `original` and
`new_worktree` remain available; `original` never accepts an arbitrary path.
`workspace.authorizationEventId` is not mandatory. Activity `authorization`
retains `{eventId,text}` references to existing `user` events in the invoking
conversation, providing traceability rather than semantic proof or a permission
grant. Tool schemas must describe the nested authorization/workspace contracts
completely. Orchestrator instructions apply defined defaults and ask only when
missing information or ambiguity blocks execution. This supersedes the earlier
mandatory workspace question, while retaining the user's fresh/reuse decision for
corrective attempts involving earlier artifacts; see refinement section 13.

Each run captures its graph and agent definitions at actual start. Nodes use private
native sessions and explicit input/output mappings. `continue_target` reuses the
node's latest session in that run only with the same working directory/harness and
adapter-confirmed continuity; otherwise it starts a new session. A Choice is reserved,
then accepted only after the adapter confirms node shutdown and completion of
actually started tool calls, with new calls prevented by the seal; reservation
does not dispatch the destination. Invalid submissions and normal endings without
a Choice share a three-violation limit per activation. The engine's built-in blocked
escape remains distinct from an author-defined Choice with the same name.

Terminal executes one noninteractive PowerShell script in its inherited directory,
with `$payload` supplied as data. Output mapping can use `command.result`; a nonzero
exit still follows that mapping, while infrastructure failure fails the run. Fork
starts parallel branches with explicit payloads and either separate worktrees or
the inherited workspace. Join waits for all incoming deliveries and delegates
integration to its agent. The engine records workspace provenance and explicit reuse;
it does not implicitly reset the original directory, commit work or clean worktrees.

Node questions and permissions use the existing cards in the owning main chat and
route answers to the originating native session. Session grants remain local to
that node session. Shutdown/restart records interrupted runs without automatic
resumption, while retaining scheduled activities and pending result notifications.

Windows mechanisms for Pi, OpenCode and Codex are implemented and admitted by the
capability preflight, but `RuntimeValidated=false`: real model tasks and native
session continuation still require human validation. OpenCode requires its owned
plugin handshake and a healthy server, but graph execution is not blocked by the
harness version. Protocol incompatibilities surface as runtime failures instead
of requiring prior version approval. Unix graph execution is not enabled.

The managed Library adapter's positive allowlist supersedes native MCP inheritance
only on its validated compatibility path, as described above. The following remains
the legacy OpenCode/Codex boundary, not evidence of managed exclusivity.

External-MCP boundary approved on 2026-09-13 (refinement section 14): permit
configured MCPs in OpenCode/Codex without a server-name allowlist or disabling
them. This replaces the preventive configuration ban. The engine guarantees the
node and tool-call lifecycle, not shutdown of shared MCP servers or detached
remote tasks continuing beyond a tool response. A "task started" response finishes
that call's lifecycle responsibility without proving task success. Error/cancellation
or a lost callback without evidence of call completion retains uncertain finality;
local shutdown is not proof of remote cancellation or a normal result. Once all
activation workers have returned, a run with uncertain finality terminates as
`failed`, retaining the diagnostic and notifying the orchestrator. Uncertainty
does not leave the run in `ending` indefinitely or accept a pending Choice. Grants,
approval flows and sandbox settings remain unchanged. Implementation and runtime
validation of this revised boundary are tracked separately.

Nested Forks, worktree cleanup, user interruption/resumption controls, concurrent
runs within one conversation, rich graph inputs/files, activation limits and task
timeouts remain outside this delivery.

## Graph Selection in Chat

The main chat composer has a graph selector at the bottom left, opposite model
and effort controls. It shows graphs belonging to the selected project and the
selected graph's name. Lists longer than five graphs enable search and scrolling.
Selecting a graph adds a **Graph** tab beside **Chat**, below the session title.
The tab displays the selected graph in a read-only canvas with pan, zoom and reset
view controls. Nodes, connections and settings cannot be edited there. The composer
and its status bar are visible only in Chat; Graph uses that space for its canvas.
Selecting **None** hides Graph and returns to Chat if
needed; selecting a graph makes the tab available without opening it automatically.

The selector uses the project's file-backed catalog and persists selection per
conversation in the engine, independently of Send. Starting an authorized run selects
its graph. Selecting or opening Graph never starts execution; selecting None or
switching views does not stop a run or remove its pending questions/permissions.
Catalog errors remain recoverable errors rather than an empty catalog or silent None.

When the selected graph matches the conversation's active run, Graph displays the
captured definition with real active/completed-node progress from the engine.
Parallel nodes can be active together; a new activation takes precedence over an
earlier completion. Join collecting inputs is distinct from its agent running.
The composer graph icon becomes a blinking LED for that matching active run,
including startup, shutdown and human waits. The name remains plain text. On run
completion, activity and LED clear and the view returns to idle configuration;
an already-open node activity panel remains available until closed so its final
output can be read. Unexecuted nodes are never marked completed.

The active cards retain the pink-to-blue body shimmer and Running indicator.
The Run/Input/Output panel is connected to real Agent and Join activations during
a run. Run reuses the chat timeline for the reasoning and tool events exposed by
the harness, including reads, writes, commands, results and errors. Input shows
the activation's received task and other incoming fields; Output shows its submitted
Choice and payload, distinguishing pending acceptance from accepted settlement.
Repeated activations can be selected independently without mixing their histories.
Activity is fetched only while its panel is visible, separately from the graph
progress projection. This supersedes the earlier deferral of this panel in section
12 of `GRAPH_AUTHORING_REFINEMENT.md`. Separate run tabs, execution history lists
and a new sidebar request component remain deferred. Final results still reach
the main-chat orchestrator. Integrated runtime/UI behavior awaits human validation.

The initial agent or terminal node carries a small **Start** badge inside its header.
All node cards have the same moderate resting shadow, independent of the active
card's shimmer. Reduced-motion preferences disable the shimmer.

When no graph run is active, clicking a card opens a right-side configuration panel
in the same visual style as the authoring panels. Values appear as text, not disabled
form controls. It includes agent execution settings and their inherited/override
source, Choice contracts and output templates, Fork branch/worktree settings, Join
settings and terminal commands as applicable. There are no editing actions.
Required/Optional is shown as text in this first version; its final presentation
still needs user validation. Values come from the real project catalog, including
terminal Choice output, Terminal output and each Fork branch's isolation setting.

Further decisions will be made as features are discussed and validated by the
user. They must not be treated as already approved requirements.
