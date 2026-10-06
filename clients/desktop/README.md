# KLM Desktop

React 19 + TypeScript + Vite, packaged as a Tauri 2 Windows client for the standalone
[Go engine](../../engine/README.md). The same build runs in a browser. The original
`../../code.html` is preserved; native folder selection belongs to the engine PC.

## Run

Build the development engine with `go -C engine build -tags dev -o klm-dev.exe .`
from the repository root and start it with `.\engine\klm-dev.exe start`.
From `clients/desktop`:

```sh
npm install
npm run dev
```

For the native client, use `npm run desktop:dev`; Rust/MSVC/WebView2 prerequisites
are required. `npm run desktop:build` builds frontend, Rust and a per-user NSIS
installer in `src-tauri/target/release/bundle/nsis`. No engine is bundled or started.

`src/platform.ts` resolves fetch and SSE to `http://localhost:7331` inside Tauri and
to the browser page's hostname on port 7331. Settings can persist a different HTTP(S)
engine URL for that frontend; connecting reloads the page so requests and streams
switch together. IP literals without a port use 7331; explicit ports are preserved,
including 80/443. Domains receive no added port. Without a scheme, IPs use HTTP
and domains use HTTPS. IPv6 supports `[IP]:port` and bare compressed literals.
For Cloudflare Tunnels, enter e.g. `klmengine.codemob.com.br`; changing the
subdomain needs no engine allowlist update. `VITE_ENGINE_URL` remains a development override. The production
desktop CSP permits HTTP(S) connections so the configured endpoint also works in the
packaged client.
`npm run build` / `npm run preview` remain available for browser development.

Vite dev uses API port **17331**. `desktop:dev` applies `tauri.dev.conf.json` for
an independent app instance and serves Focus on **17332**, also targeting **17331**.
The embedded Focus snapshot selects the dev API by its web port. Vite HMR remains
on **5173**; ordinary production builds and previews default to API **7331**.
The dev engine keeps data and local start/stop control separate in
`%APPDATA%\klm\engine-dev`.

## Desktop and Focus Lifecycle

The desktop fills the WebView without the web page's outer gradient or margins.
Header drag moves the native window; browser header drag still moves the app shell.
Detection uses Tauri context rather than hostname. Closing the native window hides
it to the tray. **Open** restores it; **Exit** stops Tauri and its web listener.
Launching a second shortcut restores the existing instance.

Rust embeds the same Vite `dist` assets and serves them on `0.0.0.0:7332`, with MIME
types, GET/HEAD, SPA fallback and no filesystem access outside embedded assets.
**Focus**, between **Session log** and **Side agent**, opens `http://localhost:7332`
in the default browser. The button is absent in web mode. Port collisions show an
error; Focus never opens an unknown listener. On another device, use
`http://IP-OF-ENGINE-COMPUTER:7332`. The engine remains on that host, port 7331.

The server lives with Tauri, including while hidden, and shuts down on Exit. It
requires no Node/Vite at runtime. In development, the embedded Focus build is the
snapshot produced before `desktop:dev`; Vite HMR applies to the native dev window.
Rebuild/restart to update the embedded browser assets.

Persisted data is shared through the engine. Unsent drafts, localStorage and WebView
state are per-client and are not transferred by Focus. HTTP LAN pages use a
`getRandomValues`-based UUID fallback; unavailable browser clipboard APIs retain
the existing manual-copy recovery. Native directory dialogs still open on the engine PC.

## Structure

```text
src/
  platform.ts           Native context, endpoint resolution and Focus/window actions
  engine.ts             HTTP client, shared API types, native picker request
  design-system/        Domain-independent primitives, tokens, and shared styles
  features/projects/    Project rail and creation form
  features/workspace/   Sessions sidebar and creation dialog
  features/chat/        Main/side chats, composers, consultation activity, harness events
  features/agents/      File-backed agent catalog/editor and real harness catalogs
  features/graphs/      File-backed authoring, YAML/canvas mapping, catalog and run monitor
  App.tsx               Engine state, SSE subscriptions, and application composition
  DesignSystem.tsx      Interactive gallery with isolated static examples
```

`tokens.css` owns semantic colors, typography, spacing, radii, shadows, and panel
dimensions. Primitive styles are in `design-system/styles.css`; app layout is in
`styles.css`. Import components directly and keep service calls outside generic
UI primitives. Inter fonts and Lucide icons are bundled without CDN dependencies.

## Agents and Graphs

`ProjectAuthoring.tsx` shares the engine-backed project catalog with the chat picker
through `features/graphs/catalog.ts`. Agents and Graphs are file-backed CRUD screens.
They reload when revisited and invalidate the catalog after mutations; failed saves keep
the form open and display the error. Revisions prevent stale edits overwriting
newer saved definitions. Invalid project files are reported rather than replaced
with fixtures.

Agents use the selected harness's real model catalog. The form saves the model ID,
requires effort when configurable, and stores the prompt directly in the agent's
TOML. The identifier is derived from the name. Enable/Disable persists; referenced
agents cannot be disabled/deleted, and renaming updates saved graph references.

New graph opens a modal with required Name and optional Description. Create opens
an identified draft canvas. Graph tiles reopen saved topology. Graph details edits
name/description; Save persists the definition and registers a new graph. AI agent
settings update the canvas immediately; other panels use Apply/Cancel. Close node
settings before Save. Cancelling a new node's configuration leaves its draft in the
canvas; incomplete required settings are rejected on Save.

Layout autosaves on drag release and user viewport changes, separately from YAML.
Before first Save it uses a unique draft layout filename; the first Save moves it
to the graph's companion filename. Layout-only drafts do not appear in the catalog.
Old positions are retained for saved nodes removed/renamed in an unsaved edit, so
layout autosave does not erase the last saved definition's presentation. Layout
failures expose Retry. Leaving the editor discards unsaved definition changes.

`files.ts` maps the executable YAML definition to canvas nodes/connections and back.
Layout stores positions/viewport only, never executable topology. Without layout,
nodes get deterministic fallback positions. This CRUD does not establish that a
saved graph is executable: disconnected authoring nodes can still be saved; runtime
preflight separately checks completeness and the approved execution contracts.

The initial node can be AI agent or Terminal command. The canvas catalog also
includes Choice, Fork and Join. AI agent and Join nodes connect to Choices.
Choice session policy is shown only for an AI agent or Join destination and is
cleared when its outgoing connection or destination is removed. Choice input/output
and Fork output field names use the same normalization as Choice names: lowercase,
spaces converted to underscores, accents folded, other symbols removed, and
edge underscores stripped on blur/apply. Payload values retain their text.
Choice output remains editable for End graph, with no destination/session policy.
The shared output editor validates the restricted template grammar and offers
immediately connected payload fields rather than global node-result lookup:

- Choice output: `choice.<declared field>` and `run.input.task`.
- Fork output: `payload.<field>` and `run.input.task`.
- Terminal output: the Fork references plus `command.result`.

Terminal has a flat output editor and JSON preview. Missing output means `{}`, not
implicit input forwarding; Command remains literal PowerShell with `$payload`
provided as data by the engine. Each Fork branch has **Separate worktree**, default
true even when absent in older files. Turning it off preserves the configured Git
branch name without requiring or using it to create a branch. `files.ts` roundtrips
Terminal `output` and branch `separate_worktree` through the engine's YAML contract.
Previews perform local validation/interpolation, not harness submissions.

The Go engine implements invocation, private node sessions, Choice acceptance,
PowerShell commands, workspace tracking, parallel branches and Join-agent integration.
Authoring only saves definitions; execution begins through the main-chat
orchestrator's tools under the user's authorization and workspace decision.

## Chat Graph Selection and Monitor

The main chat composer shows a graph picker at bottom left, opposite the model
and effort controls. It uses the selected project's file-backed catalog.
All graphs are listed; disabled ones cannot be selected. Up to five graph rows
are visible, with search only when the catalog has more than five graphs. None
clears the selection through the engine. Selection persists per conversation via
GET/PATCH `/api/sessions/{id}/graph`, independently of Send; PATCH uses
`{"selectedGraphId":"id"}` or `{"selectedGraphId":""}` for None. Starting an authorized
run selects its graph atomically. Rename/delete reconcile through engine state; unavailable
selections and catalog errors remain recoverable rather than silently becoming None.
Failed loads preserve the last known catalog and show Retry; fixtures are not a fallback.
The gallery's Composer example has six graphs to demonstrate search and scrolling.
Model and graph search inputs omit the thick external focus outline.
The graph menu aligns its left edge with the picker and extends rightward to
avoid covering the sessions sidebar. Other menus keep their existing alignment.

Selecting a graph adds a **Graph** tab beside **Chat** below the session title,
without switching tabs automatically. Graph shows a read-only React Flow canvas
using the authoring cards, with pan, zoom and Reset view. Nodes cannot be moved,
connected, removed or edited, and the authoring catalog is not exposed.
The composer and Session Status Bar appear only in Chat, preserving the draft while
Graph is open. None hides Graph and returns to Chat without cancelling the run or
removing its pending requests. Selecting/opening Graph does not execute it, and
switching views does not control its lifecycle. Tab selection is local to
each chat. Changing graphs while viewing Graph loads the new drawing and fits it.

`GraphView.tsx` renders a real `GraphRecord`. When the selection matches the active
run, it uses `run.snapshot`, including captured layout or deterministic fallback
positions. `run.graphId` tracks catalog identity while `run.snapshot.id` retains the
captured identity. Agent TOMLs are not part of this frontend DTO: the active monitor
does not present live-catalog model settings as if they were captured run settings.

`ConversationGraphState` carries the selection, monotonic revision, active-run
projection and node requests. HTTP/SSE merges ignore older graph revisions; a legacy
Session response without `graph` preserves the last known projection. Initial
hydration and small graph polling complement global state refresh and SSE, including
conversations with active graph runs while the orchestrator is idle. An authoritative
`run:null` clears activity/LED and returns to idle configuration.

The monitor shows all active and actually completed nodes, including parallel work
and revisits. Active takes precedence over earlier completion; Join **Collecting**
is distinct from its agent **Running**. Raw completed Choice IDs gain `choice:` only
in the canvas. Completion never marks unexecuted nodes or retains a run-history view.

The graph picker replaces its icon with the running LED only when its selection
matches the conversation's active run, including starting/ending and human waits.
The name remains plain text. The LED is in the composer selector, not the Graph tab.
Cards retain the pink-to-blue body shimmer, Running indicator and `--shadow-card`;
reduced-motion preferences disable the shimmer and keep LEDs steady. Styles remain
in `graph-shimmer.css` and `graph-running-led.css`. The initial agent or Terminal
keeps its explicit **Start** badge.

Earlier CSV timers, fictional drawings and Run/Input/Output panels are historical
visual experiments; their files remain outside the production monitor's imports.
Section 12 of [the refinement record](../../GRAPH_AUTHORING_REFINEMENT.md) takes
precedence: the delivered monitor has no execution-detail panels, separate run tabs,
history browser or new sidebar request component. Results still reach the orchestrator.

Without an active graph run, clicking a card opens `NodeConfigurationPanel.tsx` in
the same right-side floating position and shell as the authoring panels. It renders
labels/values rather than disabled controls: agent name, selected agent and effective
harness/model/effort with inherited/override provenance; Choice description, outcome,
input fields, output templates, destination and eligible session policy; Fork branch
names, destinations, worktrees, Git branches, base revision and output fields; Join
agent, additional prompt and output Git branch; terminal name and command.
There are no Apply/Cancel, edit, add/remove or preview-editing actions. Close and
Escape return focus to the card. Required/Optional is provisional plain text rather
than a toggle. Values come from the real catalog; terminal Choice output, Terminal
output mapping and per-branch isolation are included. Active runs expose progress
without opening these configuration panels.

Graph-node questions/permissions reuse `PermissionCard` and `QuestionCard` above
the main composer with graph/node origin. Replies use the projected native
`sessionId` and `requestId`, then refresh the owning conversation; private node
sessions stay outside the session browser. None keeps these requests available,
and a waiting node does not disable an idle orchestrator's composer. Answers are
tool replies, not ordinary chat messages; grants keep their native scope.

### Runtime validation status

Windows mechanisms for Pi, OpenCode and Codex are implemented, with
`RuntimeValidated=false`. OpenCode requires version **1.18.30** and the owned plugin;
the 2026-09-13 decision in refinement section 14 replaces the preventive external-MCP
configuration ban. Permit configured MCPs in OpenCode/Codex without name allowlists
or disabling them. Node sealing blocks new calls and Choice waits for calls actually
started to complete; shared MCP servers and detached tasks beyond the tool response
need not terminate. A "task started" response completes the call, not the task.
Missing completion evidence after error/cancellation or a lost callback remains
uncertain finality, not a normal result or proof of remote cancellation. Existing
grants/sandbox and permission cards keep their behavior. This is the approved
contract, not evidence of updated runtime validation or restart.
Unix graph execution is not enabled. The frontend's reported
`npx tsc --noEmit` check covers TypeScript compilation, not real model execution,
Choice finality or native continuation. Human validation of integrated selection,
reload/reconnection, parallel/Join progress, requests with None and authoring roundtrip
remains pending. See the current [frontend](../../GRAPH_ENGINE_PROGRESS_FRONTEND.md)
and [adapter](../../GRAPH_ENGINE_PROGRESS_ADAPTER.md) reports for evidence and limits.

## Behavior

Project creation persists name, absolute directory, and a resized PNG icon through
the engine. The sidebar contains only the selected project's sessions, optionally
grouped into topic folders. Each real session chooses its harness at creation.
Session menus provide Rename and Archive, and the header title is also a rename
trigger. Sessions can be dragged between active folders; the folder select remains
available for keyboard and touch use. Folder menus archive whole visual groups.
The Archived section restores sessions and folders without deleting history.
Below the project heading/path, compact icon-and-text rows show **New session**,
**Agents** and **Graphs**, before the Sessions list. New session uses a compose icon
and opens the existing creation dialog; it replaces the former large button.
The sidebar heading shows only the project name beside its settings button. The
Harness badge is reused in the empty main chat, below a KLM wordmark and above
“What would you like to work on?”. Both logo rows share the same compact width,
with tightly spaced KLM letters and 2px horizontal padding on the badge.
The composer has independent model and effort menus immediately left of Send.
The harness icon appears beside the model name; there is no lower-left harness label.
Clicking the model opens an anchored search menu with five model rows visible at a
time and scrolling for more. Each row identifies its connected provider. Selecting
a model saves immediately. Clicking effort opens a stepped slider; releasing it
or finishing a keyboard adjustment saves effort without changing the model.
The catalog carries an explicit connected-provider list, enforced by both engine
and picker. Automatically available free catalogs without a configured connection
are excluded. A model offered by OpenRouter remains under OpenRouter even when
its name contains OpenAI, Anthropic, or another model vendor.
Pi/Codex expose thinking
efforts; OpenCode exposes native model variants. Selections apply to the
next turn. Selection is disabled while running, and unsupported effort overrides
reset to Default when switching models. Harness default remains available; the
resolved model is displayed when the harness reports it.

Shared `design-system/Menu.tsx` and `Slider.tsx` primitives are demonstrated in the
Design system gallery. Menus are non-modal, viewport-aware anchored panels, dismissed
by outside clicks or Escape. Slider values are controlled and expose native keyboard
interaction and accessible value text. The model menu also supports arrow-key navigation.

SSE sends full session snapshots and reconnects automatically. A small state refresh
also discovers changes from other clients. The UI renders real user/assistant
messages, collapsible reasoning and command/MCP/tool events, and errors. Reasoning
availability and streaming granularity depend on the chosen harness and model.
Stop cancels the owned harness process. Drafts clear only after an accepted send.

The icon immediately after Session log opens the linked side conversation, replacing
the former details inspector. Side sessions are filtered from the workspace list.
Selecting text in a main-chat message exposes **Ask side agent**; exact passages
remain removable composer context until a question is sent. Subsequent selections
use the same side conversation. Sent context and source references persist in the engine.
Drafts and unsent selections are independent per main chat and last until reload.
The selected project, last selected session per project, and side-panel open state
per main session are saved in browser localStorage under the versioned, engine-scoped
`klm.workspace-navigation.v1:<engine-url>` key. Reloading restores that navigation.
The same entry preserves each project's collapsed folders. Search expands groups
temporarily without replacing the saved collapsed state.
Switching sessions restores only that session's side-panel state; opening a side chat
does not open or create side chats for other sessions. Missing projects/sessions fall
back to the first available entry. Storage failures leave in-memory navigation usable.

The side chat reuses message rendering, composer, model picker, permission/question
cards, and token/context status. Its status bar omits quota, skills, and MCP indicators.
Choose its harness before the first turn. SSE subscriptions include the visible side
and every running session, including hidden side conversations. Consultation output
from the answering agent is grouped under expandable question/answer activities with
lifecycle status and Cancel. The requesting agent's resumed reply appears in the
normal conversation automatically, including when reopening saved history.

Side width defaults to 352px and is remembered in `klm.side-agent.width.v1`. The
divider supports pointer dragging, ArrowLeft/ArrowRight (24px), Home, and End.
It retains at least 304px for the side and 400px for the main chat; when those
columns do not fit, the side occupies the full app panel with a close button and
keyboard focus containment. Closing the panel does not delete its native history.

Permission cards appear above the composer. Session snapshots include optional
`permissions`; decisions POST to `/api/sessions/{id}/permissions/{permissionID}`.
While a request is pending, Working changes to Waiting for approval. Buttons use
the backend's allowed choices and display the actual native grant lifetime.
Errors leave the request visible for recovery, and Stop remains available.

Question cards share that area but are separate from permission prompts. Options
are clickable buttons, multiple selections are available when requested, and
custom text is shown when allowed. Send answer submits all questions in the batch;
Dismiss cancels the request without choosing an answer. Working becomes Waiting
for your answer. Session snapshots include optional `questions`; answers POST to
`/api/sessions/{id}/questions/{questionID}/reply`. Secret inputs are masked and the
engine's answer audit is redacted; the receiving harness still receives their values.

Assistant replies render Markdown using react-markdown and remark-gfm: headings,
emphasis, lists, task lists, blockquotes, links, tables, and fenced code blocks.
User messages and tool output remain plain text. Code blocks and tables scroll
within the message. Copy preserves the original Markdown. Raw HTML is disabled,
unsafe URL schemes are filtered, and Markdown images appear as links rather than
automatically fetching remote resources. The Design system gallery includes a
Markdown example for visual validation. Syntax highlighting is not included yet.
Assistant message actions stay hidden while the message streams. A completed response
provides Copy and a persistent Favorite toggle; failed or cancelled partial output
provides Copy only. Like and dislike actions are not shown.

Mermaid `flowchart` and `graph` blocks render inline between paragraphs in both
chats, with a preview capped at 320px high. **Expand** opens a modal with zoom,
**Fit**, scrolling, and Escape to close. The renderer loads on demand and uses
the current theme. It accepts `mermaid` fences, unlabelled flowchart code blocks,
and standalone flowchart headers followed by contiguous recognizable statements
without fences. Explicit `mermaid` fences are recommended for complex syntax.
Incomplete, invalid, or unsupported diagrams retain their source. Rendering is
debounced during streaming; copying a message keeps its original text. Generated
SVG is displayed as an isolated image, with HTML labels, agent configuration
directives, and remote image resources disabled. Other Mermaid diagram types are
not included in this slice. The Design system gallery has a **Flowcharts** example
for checking inline layout and expansion without running a harness.

The composer grows up to ten visible lines before scrolling. Enter sends;
Shift+Enter and Ctrl+J insert a newline. Project/session data and native harness
continuity survive restart; drafts are browser-memory-only. Message favorites are
stored by the engine and are not sent to a provider.

Archived sessions and sessions inside archived folders retain their runtime state but
do not affect the error, waiting, or activity treatment on the project rail. Restoring
them makes their current state visible again.

The window has a blue/lavender gradient backdrop and can be dragged by its header.
Header buttons do not drag it. Mobile retains the full-screen layout, and side
panels become toggleable overlays. Chat/Add project borders do not change on focus.

The moon/sun button at the bottom of the project rail switches between Light and
Graphite Dark. Light is the default. The browser stores the choice under `klm.theme`
and applies it before the app renders. Themes share the same layout and semantic
CSS tokens, including dialogs, permission cards, Markdown, and error states.

The footer below the composer shows live session input/output totals and context
tokens / model window and percentage as plain text, without a Context label.
Hover the context figures for unrounded counts;
the same details are available to screen readers. Input includes cached
tokens; output includes reported reasoning. Unknown values display `—`, not zero.
Values persist with the session and refresh when the harness reports usage, rather
than on each text delta. Older sessions populate on their next harness run.

The order is turns/steps, LLM time, cache hit, context, quota (when eligible), input/output.
Context replaces average TTFT. Turns, steps, LLM time, and cache hit
still use static reference values pending later metric decisions. Context is the
latest available snapshot, not cumulative session consumption or an estimate of
an unsent draft. Hover the usage figures for unrounded token counts.

Quota replaces tok/s with `83% · 2 days` (used percentage and time to reset).
The most-used unexpired window is shown; hover lists all returned windows, source,
reset timestamps, and observation time. Quota is hidden for intermediary providers,
API-key billing, unsupported routes, or unverified account matches. It refreshes
every two minutes while visible. Expired windows and snapshots older than five
minutes are hidden; a reset countdown never invents a fresh percentage.

Old mock localStorage data is not imported or deleted. Live sessions have no
fabricated responses, MCP connectivity indicators, or permissions/model
selectors that do not affect execution. The design-system gallery uses static
examples only. Native OS picker language may follow Windows settings; app copy
remains English.

See the root README for current execution, permission, and persistence boundaries.

### Messages during a response

Enter queues a message while the agent is running. The composer keeps Stop
available and offers **Send now** for steering. The queue above the composer
supports removing and sending individual items; it is shared through engine SSE
and survives page reload. Main and side chats use the same controls. Paused and
unconfirmed items require explicit action after Stop, failure or engine restart.

## General-agent navigation

The Project Rail logo opens a persistent global conversation with the engine's
general agent, including when no projects exist. The view reuses the main chat
components without the sessions sidebar or responsive Open sessions control.
Returning to a project retains its selected session. General-view navigation is
stored locally; conversation identity/history/harness settings belong to the engine.

Pi, OpenCode and Codex choices appear immediately above the composer until the
first accepted message. Engine-projected `harnessLocked` controls this visibility;
loaded event pages do not determine whether the harness may change. Pending sends
disable switching, and the backend enforces the permanent lock, including queued
messages. Model and effort use normal chat controls. Project graph selection,
side-agent actions and file-mention suggestions are absent in the general view.

In the Flutter-hosted WebView only, a **Back to hosts** footer icon below Settings
performs `returnToHosts()`; the logo opens chat on every client. The Flutter
compatibility handler recognizes the new `data-klm-general-navigation` logo marker.
The logo also retains `data-klm-host-navigation` so older installed mobile apps
skip their capture handler; the actual home action belongs only to the footer.
Singleton/restart behavior and real turns, requests, Stop and mobile navigation
remain manual acceptance checks; no installer or deployed runtime was updated here.
