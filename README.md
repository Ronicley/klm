# KLM

A Windows Tauri/React client for a separate Go engine that delegates work to
Pi, OpenCode, and Codex. Product scope is defined in [product.md](product.md).

## Install on Windows

Install **KLM Engine** and **KLM Desktop** independently. Engine installation adds
`klm` to user PATH and starts the engine, including on subsequent user logins.
Open a new terminal to use `klm start` / `klm stop`. Data is preserved in
`%APPDATA%\klm\engine` and worker logs in `engine.log` there.

The desktop's **Focus** button opens http://localhost:7332. On another device use
`http://IP-OF-THIS-COMPUTER:7332`; both clients use the engine on the same computer,
port 7331. Tauri must remain running: closing hides to the tray; **Open** restores
and **Exit** stops desktop/web only. The engine is independent.

The API and web listener bind all IPv4 interfaces, without network authentication
or TLS in this approved delivery. Use Windows Firewall's normal network access
authorization when required. Directory dialogs and commands run on the engine PC.

Build instructions and installer paths: [Windows packaging](distribution/windows/README.md).
Progress and human acceptance checklist: [delivery status](ENGINE_DESKTOP_IMPLEMENTATION_PROGRESS.md).

## Develop Locally

Requires Go 1.24.2+ and Node.js 22.12+. Build the engine from the project root:

```powershell
go -C engine build -tags dev -o klm-dev.exe .
.\engine\klm-dev.exe start
```

In another terminal:

```powershell
npm --prefix clients/desktop install
npm --prefix clients/desktop run dev
```

Open http://127.0.0.1:17333. The development API is available at http://localhost:17331.
`VITE_ENGINE_URL` overrides the client endpoint. For the native client, install
Rust/MSVC prerequisites and use `npm --prefix clients/desktop run desktop:dev`.
End the worker explicitly with `.\engine\klm-dev.exe stop`.

Development uses API port **17331** and native Focus/web port **17332**; Vite HMR
stays on **17333**. The installed release uses **7331/7332**. The `dev` build tag
also separates engine data/logs/control into `%APPDATA%\klm\engine-dev`.
`desktop:dev` uses a distinct application identifier, so both desktops can run
together. Dev starts with its own empty data store; release data is not migrated.

## Mobile

The Flutter **KLM Harness** app in [`clients/mobile`](clients/mobile/README.md)
provides native saved-host and Add host screens, then opens the selected frontend
in a WebView. Tap the KLM rail logo to return to hosts. IPs without a port use 7332;
domains receive no additional port. Names and addresses persist locally on the device.

From `clients/mobile`, run `flutter run -d emulator-5554`. See its README for
device addressing and platform setup.

## First Slice

- Add a project using the rail's plus button. The engine opens a native Windows
  directory picker; choose its name and icon, then save. Directory is read-only
  with a Change directory button. Icons are optional and stored locally.
- Use the compact **New session** shortcut at the top of the sidebar and choose
  Pi, OpenCode, or Codex. Select a
  model and supported effort/variant in the composer picker left of Send. Existing
  CLI credentials and model configuration are used; authenticate in the CLI first
  if necessary. Changes apply to the next turn.
- Send messages. The conversation displays actual text, exposed reasoning,
  commands, MCP calls, and generic tools emitted by the selected harness.
- Stop an active response, reload, or restart the engine. Projects, grouping
  folders, history, and native conversation identifiers are persisted. Unsent
  drafts last only until the browser reloads.
- Pending permissions appear above the composer for all three harnesses. Allow
  once, remember for the session, remember for this project/harness, or deny.
  Remembered grants match the exact displayed action/resources; they do not grant
  unrestricted tool access. Session grants survive restart with that logical session.
- Agent questions appear above the composer with option buttons and custom input
  when allowed. Select choices or type an answer, then **Send answer**, or **Dismiss**
  the request. The waiting tool receives your answer and the turn continues.
- Open **Side agent** in the header, or select a main-chat passage and choose
  **Ask side agent**. One persistent side conversation belongs to each main chat;
  it has independent harness/model controls, drafts, scrolling, permissions, questions,
  and Stop. Closing hides it. Drag the divider, or focus it and use arrow keys,
  Home, or End, to resize; narrow layouts show the side chat full-width.
- Both agents can retrieve or consult the other conversation across Pi, OpenCode,
  and Codex. Expand consultation activities for questions, answers, execution details,
  and cancellation. Busy recipients queue; replies arrive through the tool or a
  correlated continuation after the requesting turn finishes.

Old browser mock projects are left untouched in localStorage but are not imported:
their folder labels were not reliable absolute paths. Projects and chat sessions
use engine data. Agents and Graphs now read and write the selected project's files;
the chat graph selector and monitor use the same real catalog and engine state.
The design-system gallery retains isolated static examples.

The sidebar places **New session**, **Agents** and **Graphs** above the Sessions
list. Its heading shows the project name beside settings. Empty chats display a
compact **KLM / HARNESS** mark above “What would you like to work on?”, with both
logo rows aligned to the same width.

## Agents and Graphs

Agents are saved as `.klm/agents/<slug>.toml` inside the selected project. The form
uses the real harness model catalog; effort is required only for models that support
it. Prompt text lives in the TOML. Editing, renaming, enable/disable and deletion
persist to files. References from saved graphs prevent agent deletion/disabling;
agent renames update graph references.

New graph asks for a name and optional description, then opens the canvas. Configure
AI agent, Choice, Terminal command, Fork and Join elements. Apply commits a panel's
settings to the canvas; close node settings, then Save to write
`.klm/graphs/<slug>.yaml`. The first Save adds the graph to its catalog. Existing
graph tiles reopen the saved topology; Graph details edits metadata. Enable/Disable
and Delete are list actions.

Node positions autosave when dragging ends, independently of Save, to the companion
`.yaml.layout.json`. Draft layouts can exist before the first Save without creating
a catalog entry. Definition changes still require Save. Graph execution, commands,
worktrees and Join-agent integration are implemented in the Go engine. Terminal has
explicit output mapping; each Fork branch can use a separate worktree (default) or
share its inherited workspace. Save remains separate from execution preflight.
See the [desktop README](clients/desktop/README.md#agents-and-graphs) and
[refinement record](GRAPH_AUTHORING_REFINEMENT.md).

The [graph engine implementation plan](GRAPH_ENGINE_IMPLEMENTATION_PLAN.md) maps
the closed execution contracts and minimal active/completed-node monitor to the
Go adapters and desktop client. Current implementation/evidence is recorded in
[CORE](GRAPH_ENGINE_PROGRESS_CORE.md), [ADAPTER](GRAPH_ENGINE_PROGRESS_ADAPTER.md)
and [FRONTEND](GRAPH_ENGINE_PROGRESS_FRONTEND.md); use their latest status sections
rather than historical milestones in the plan or reports.

## Graph Execution and Monitoring

Choose a saved graph in the main-chat composer to expose **Graph** beside **Chat**.
Selection persists per conversation in the engine, independently of Send. Selecting
or opening Graph does not execute it. Ask the main-chat agent to run an explicitly
authorized activity; its private graph tools invoke work asynchronously. Under the
2026-09-13 decision, an omitted workspace defaults to the original project directory
without a question or mandatory workspace-choice message reference. Explicit
original/new-worktree choices remain available; retries involving earlier artifacts
retain the user's fresh/reuse decision. A conversation has one active graph run,
independent of chat turns.
Starting a run selects its graph. The orchestrator receives its result, assesses
success and manages authorized dependent activities or corrective attempts.

The runtime captures graph/agent definitions at start, uses private node sessions,
validates Choices before accepting them after execution shutdown, and records
Terminal, Fork/Join and workspace activity. Questions/permissions appear in the
existing main-chat cards with graph/node origin and answer the correct native session.
Restart records interrupted runs without resuming them; scheduled activities and
pending result notifications persist. Worktrees and modified files are retained.

Graph shows a read-only canvas with pan, zoom and Reset view. During the matching
active run it uses the captured graph and shows actual active/completed nodes,
including parallel activity, revisits and a separate Collecting state for Join.
The existing card shimmer is retained; the run LED appears only in the composer
selector for the matching graph. **None** hides Graph without stopping work or
removing pending requests. At completion, activity/LED clear and idle read-only
configuration returns. The initial node keeps its **Start** badge.

This minimal monitor replaces the earlier CSV timer and Run/Input/Output experiment.
Separate run tabs, execution-history browsing and detail panels remain deferred,
as established by section 12 of the refinement record.

### Graph adapter status

Windows mechanisms for **Pi, OpenCode and Codex** are implemented and admitted by
capability preflight. All still report **`RuntimeValidated=false`**: real model
tasks, Choice finality and native continuation require human validation.
OpenCode requires **1.18.30**, its owned plugin handshake and native tool inventory.
Pi uses its owned extension. Unix graph execution is not enabled.

The **2026-09-13 external-MCP decision** replaces the preventive configuration ban:
allow configured MCPs in OpenCode/Codex without a server-name allowlist or disabling
them. Seal the node against new calls and await calls actually started before Choice
acceptance. The guarantee covers node/tool-call lifecycle, not shutdown of shared
MCP servers or detached tasks beyond the response. "Task started" completes the call,
not the remote task. Missing completion evidence after error/cancellation or a lost
callback remains uncertain finality, never proof of remote cancellation or a normal
result. Grants/sandbox are unchanged. See refinement section 14 and
[the documentation update](GRAPH_ENGINE_PROGRESS_EXTERNAL_MCP_DOCS.md); this decision
does not itself establish that the updated adapter has been built or restarted.

## Boundaries

The **Session Status Bar** (`clients/desktop/src/features/chat/SessionStatusBar.tsx`)
is the compact row below the message input. It shows live input/output token totals
and latest context usage; skills and MCP counts remain static reference values.
Quota/reset appears for verified direct subscription connections. Other routes
(including OpenRouter and API-key billing) have no quota slot.
Unavailable token/context metrics stay hidden and appear when reported by the harness.
Reported zero values remain visible. See [CONTEXT.md](CONTEXT.md) for shared terminology.
The side status bar shows only its own token counts and context usage.

Rich graph inputs/files, nested Forks, worktree cleanup, graph stop/resume controls,
concurrent runs in one conversation, activation limits and task timeouts are deferred.
Model-account management and a general-purpose engine MCP interface are not included.
The private linked/graph bridge is configured only for KLM-owned harness executions.
The engine normalizes only the events the harness
actually emits, so some text/reasoning arrives in completed blocks, not tokens.
Pi runs with only an engine-owned extension, providing `ask_user`, linked-agent tools, and gating other
agent-dispatched tool calls; third-party extensions and their MCP tools remain disabled. It is not
an OS sandbox. OpenCode uses its server API and preserves existing permission rules.
Codex uses app-server with read-only sandboxing and user/on-request approvals.
Some Codex permission-profile grants last for the whole turn; the card labels that
scope. Ambiguous requests cannot be remembered, and unsupported forms are declined.
No blanket native policy amendments or approval/sandbox bypass flags are passed.

OpenCode questions honor native session permissions. Older sessions created by
`opencode run` may retain a question-tool denial; use a new session in that case.
Codex's Default-mode question tool uses its under-development feature flag, set
only on the engine-owned child process. Global config and selected models are
unchanged. Pi questions use the engine-owned `ask_user` tool and RPC bridge.

Pending user questions and permissions cannot survive a stopped process; they are
cleared on stop/restart. Linked-agent requests retain terminal interruption/cancellation
records and are not automatically replayed after restart.
The engine stores remembered approvals in its local state, not in global harness
configuration. A revoke/manage-grants UI is not included yet.

Engine data defaults to `%APPDATA%\klm\engine` on Windows. It is plaintext local
data; protect it like the project itself. The native directory picker currently
supports Windows only, including empty directories and full absolute paths.

See [engine/README.md](engine/README.md) for the API, adapter details, executable
overrides, persistence, and security boundaries. See
[clients/desktop/README.md](clients/desktop/README.md) for frontend structure.

## Validation

Use `go -C engine build` and `npx tsc --noEmit` from `clients/desktop` for lightweight
compilation checks when needed. Full test suites and adversarial reviews require
an explicit request; human validation is the feature acceptance gate.
