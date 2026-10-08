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

Top-level sessions and their visual grouping folders can be archived and restored.
Archived content retains its history and runtime state but does not contribute error,
waiting, or activity indicators to the project rail. Folder collapsed state remains
local to each browser or WebView. Sessions can also be renamed and moved between
active folders without changing their execution directory.

Assistant message actions are hidden while that message is streaming. Completed
assistant messages provide Copy and a durable Favorite toggle; like and dislike are
not used. Failed or cancelled partial responses remain copyable but cannot be marked
as favorites.

### General Agent

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

The general agent can send visible, self-contained instructions to project chats
and ask questions with correlated replies. It can rename/move visual grouping,
atomically update model/effort/YOLO between turns, select a graph without executing
it, and answer the exact pending native question through its owning conversation.
Moving a chat does not change its directory. Archived history is readable; sending
and consulting require effective restoration. Engine-owned
identity is not a synthetic project or a grant of harness permissions.

Its dedicated tools can create normal top-level sessions in an explicitly chosen
registered project on the user's explicit creation request, with a self-contained
prompt and a durable sender-scoped receipt referencing an actual human message in
the general conversation. The child uses the project's registered directory;
visual grouping defaults to Ungrouped. Harness/settings and YOLO inherit under the
existing spawn rules, with explicit requested overrides applied before its first
turn. Creation acceptance is not work completion and creates no subscription.

The general agent can Stop project main/side chats, remove an exact unsent queue
item, apply its existing Send now control, archive/restore normal top-level chats,
and reply to an exact pending permission through its owning conversation, including
projected graph requests. Stop pauses unsent input and cancels owned chat processes,
not an independent graph. Uncertain native delivery requires the user's explicit
retry choice and may duplicate delivery. Archive/restore is metadata and preserves
runtime state; restoring under an archived folder requires an active destination
folder or Ungrouped without restoring the whole folder. Send now requires effective
restoration; Stop, removal and replies to existing requests remain recovery controls.
Permission decisions use only offered choices, preserve the user's requested scope,
and retain native-session/project/global grant ownership, rollback and attributed
agent provenance. None of these replies become human graph/spawn authorization.

No Hermes, linked side agent for the general conversation, project file mentions,
general graph invocation, automatic monitoring, completion notifications or new global
dashboard is included. Runtime behavior awaits human validation.

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
`http://localhost:7332` in the default browser. The browser retains the gradient
and draggable central window and hides the redundant Focus button. Another device
uses `http://IP-OF-ENGINE-COMPUTER:7332`; API and SSE use that same host on 7331.
Ports remain fixed; collisions are reported instead of selecting another port.

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

### Audio Transcription

The main chat composer can record audio and transcribe it through the engine without
sending the resulting text automatically. Transcribed text is appended to the current
draft while preserving selected file and folder references. Side-agent and design-system
composers do not expose recording.

The engine stores the OpenAI API key, model, optional language, and vocabulary globally.
The API never returns the key, only whether one is configured. Vocabulary notes remain
available as engine-backed context for the interface; this first slice sends only terms
as transcription prompt context. Audio uploads are limited to 25 MiB.

Recording requires the client environment to support `getUserMedia` and `MediaRecorder`.
Browser security rules may deny microphone capture on insecure remote origins, so remote
HTTP access is not promised to support recording. This does not change the existing
personal-use network and authentication contract.

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

New permission requests in active main chats, including native-subagent and graph-node
requests projected into their parent, produce sound and a system notification only
when the client window is unfocused. This also applies to the general-agent chat.
Side chats, questions, archived chats/folders and existing requests on initial load
do not alert. Requests alert once per client lifetime, including across reconnects.
Windows desktop uses native notifications and the default system notification sound,
including when minimized or hidden in the tray. Browser/Focus uses Web Audio and the
Notifications API; Settings > Chat exposes **Enable notifications**. Browser alerts
require notification permission and a secure context, while audio requires prior
user interaction and browser autoplay support. Flutter WebView alerts and delivery
to closed clients are not included. Multiple open clients alert independently;
system notification/sound settings still apply. Runtime behavior awaits human validation.

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

## Uploaded Chat Files

Main, side and general-agent chats accept non-image files from the client device
through **Attach files**, drag-and-drop, or clipboard file data when exposed by the
client platform. Text paste remains ordinary message text. Users can remove selected
files and send a message containing only files. Folders are not uploaded.

Up to eight uploaded files, totaling at most 25 MiB, are allowed per message;
empty files are valid. These limits are separate from project `@` references.
Uploads use multipart message submission and are stored under the engine data
directory in private conversation/message directories, not in project files.
Accepted files remain available across queueing, steering and engine restarts.
Filenames and sizes appear in the queue and history; file bytes are not sent through SSE.

The engine gives Pi, OpenCode and Codex a JSON manifest with local paths and bounded
UTF-8 content using the existing text-reference limits. PDF, DOCX, ZIP, other binary
formats and other encodings are made available by path; their interpretation belongs
to harness tools. KLM does not convert documents, unpack archives, or execute uploaded
files. Existing tool permissions and sandbox settings still apply. Images are outside
this upload flow. Combined prepared context retains the 200 KiB limit.

Message identity includes uploaded content hashes, so retrying the same submission
does not duplicate accepted messages or files. Rejected/incomplete uploads are removed;
uncertain persistence failures retain files for restart recovery.
Unaccepted files remain recoverable in the client's memory. After a page reload, only
their names and sizes are retained; recovery requires restoring the draft and selecting
the files again. Missing file bytes are never silently omitted from a retry.
Native mobile WebView file selection and real harness behavior require human validation.

## Messages During Execution

Main and side chats accept messages while a turn is active. Enter or the send
button adds a message to a persistent FIFO queue for the next turn. **Send now**
submits steering to the active harness at its supported interaction boundary;
when idle it starts a turn. Queued items can be removed or sent ahead of the queue.
Steering does not stop the turn or change its model, permissions, or sandbox.

Pi uses native RPC steering; Codex uses `turn/steer` with the expected turn ID;
OpenCode submits another prompt to its existing session loop. Harness acceptance
is distinct from proof that the model has acted on the instruction. Pending
permissions and questions retain their own reply controls.

The engine stores up to 32 pending messages per conversation, including prepared
attachments. Accepted inputs enter chat history. Stop, execution failure, and
engine restart pause unsent inputs. Unconfirmed native delivery is marked for
manual recovery and is never automatically replayed. These controls affect the
chat agent; they do not stop or steer an independently running graph node.
Stopping a chat cancels its turn and terminates its owned process container,
including native subagents and local tool subprocesses, without waiting for a
permission reply or a successful native abort. The stopped runtime is discarded;
the next turn starts a new runtime and resumes the stored native conversation.
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

Spawned sessions inherit the sender's current YOLO mode by default. An explicit
`yolo:false` implements the user's request for a non-YOLO child before its first
turn; overrides are only made at the user's request. Later sender-mode changes do
not change an existing child. Spawn options expose exact model IDs/efforts, visual
folders and recent user message IDs. Validation rejects malformed or unavailable
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

Agent and Join prompts guide recovery before a final Choice: a tool error or timeout
does not automatically mean blocked. The agent inspects the current state and tries
up to two concrete, distinct safe alternatives within its scope. For failed Git
branch/base queries through a wrapper or shell, this can include calling the native
Git executable directly while retaining the directory and necessary checks. If
guidance is still needed, the node asks through its harness's existing question
tool and waits in the same activation. The answer returns to that native session;
unfinished work continues in its workspace without replaying completed graph steps.
Recovery never bypasses permission denials or blindly repeats operations with
uncertain effects. A definitive obstacle or cancelled/unavailable recovery can
still produce blocked. This is agent guidance, not automatic engine classification
of tool errors; real harness behavior requires human validation. Ended runs and
harness failures retain the existing corrective-attempt and interruption policies.

Terminal executes one noninteractive PowerShell script in its inherited directory,
with `$payload` supplied as data. Output mapping can use `command.result`; a nonzero
exit still follows that mapping, while infrastructure failure fails the run. Fork
starts parallel branches with explicit payloads and either separate worktrees or
the inherited workspace. Join waits for all incoming deliveries and delegates
integration to its agent. The engine records workspace provenance and explicit reuse;
it does not implicitly reset the original directory, commit work or clean worktrees.

Node questions and permissions use the existing cards in the owning main chat or
the currently open node activity panel. The open panel receives requests from all
parallel nodes, with graph and node origin identified, without switching the
inspected node, activity tab or historical activation. Returning to the parent Chat
shows the same pending requests; switching these surfaces preserves question drafts
and in-flight submissions. Answers route to the originating native session and
request, independently of the inspected activation. Session grants remain local to
that node session. Agent and Join harness sessions inherit the invoking conversation's
current YOLO setting at each turn start, including reused sessions and correction
turns. Changes apply to subsequent activations; running turns retain their initial
setting. User questions and graph authorization/lifecycle controls remain independent
of YOLO. Shutdown/restart records interrupted runs without automatic
resumption, while retaining scheduled activities and pending result notifications.

Windows mechanisms for Pi, OpenCode and Codex are implemented and admitted by the
capability preflight, but `RuntimeValidated=false`: real model tasks and native
session continuation still require human validation. OpenCode requires its owned
plugin handshake and a healthy server, but graph execution is not blocked by the
harness version. Protocol incompatibilities surface as runtime failures instead
of requiring prior version approval. Unix graph execution is not enabled.

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

The active graph offers **Pause** and **Resume** in its canvas and the Chat progress
indicator. Pause prevents new node activations and correction turns from dispatching;
already dispatched nodes finish normally, including parallel branches. **Pausing**
becomes **Paused** once those workers settle. Pending permissions and questions remain
answerable. Resume continues the same run with retained results, queued deliveries,
native conversations and workspaces. Completion or failure of in-flight work still
ends the run normally. Pause state is engine-owned and shared across clients; engine
shutdown/restart retains the existing interruption policy, without automatic resumption.
These controls do not suspend running commands or change the chat's Stop behavior.

Nested Forks, worktree cleanup, immediate interruption/resumption controls, concurrent
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

The owning main chat shows a compact live indicator immediately above the message
composer, identifying the running graph and all active nodes from its captured
definition. Parallel nodes are listed together; nodes with pending questions or
permissions show **Waiting for input**, and Join input collection shows **Collecting**.
Startup and shutdown without active nodes show **Starting** and **Finishing**.
The indicator remains tied to the actual run when the selected graph changes or
becomes None, and disappears when the run ends.

The active cards retain the pink-to-blue body shimmer and Running indicator.
Nodes with pending permissions or questions show **Waiting for input** instead.
When neither the parent Chat nor a node activity panel is visible in the focused
client window, those nodes pulse yellow. Opening either surface presents all pending
requests without resolving them; the waiting label remains until requests settle.
Reduced-motion preferences use a static yellow highlight. Focus means the visible
surface in the active window, not the location of the text cursor. Navigation and
closing the panel never discard pending requests or answer them automatically.
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
