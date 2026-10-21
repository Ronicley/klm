# Project Instructions

## Product Context

Read [product.md](product.md) before planning or implementing product features.
It defines the product's purpose and boundaries. Keep implementation aligned
with it, and do not treat provisional terminology or unspecified capabilities
as approved requirements.

## Delivery Priority

This project is in a rapid implementation phase. Prioritize delivering the
smallest working feature so the user can validate it manually. Keep changes
focused, straightforward, and easy to extend. Do not overengineer or expand the
scope with speculative abstractions or unrelated cleanup.

## Validation Policy

- Do not run full test suites unless the user explicitly asks.
- Do not perform adversarial reviews or launch review agents unless the user
  explicitly asks. These activities come later, after human validation.
- Do not write integration tests or complex unit tests during normal feature
  implementation. Add them only when explicitly requested.
- Avoid repeated test runs and broad verification workflows. Human validation is
  the primary acceptance gate for feature behavior at this stage.
- Still meet baseline engineering quality: valid syntax, valid imports, sound
  types, intact files, and a minimally working implementation.
- Use the smallest relevant verification step, such as a targeted type check,
  syntax check, or build when needed to catch compilation or import problems.
  Do not run several overlapping checks by default.
- Fix obvious errors introduced by the change. Do not knowingly leave broken
  code, expose secrets, or compromise essential security or data integrity for
  speed.
- State briefly what was implemented, which lightweight check was performed,
  and what remains for the user to validate. Never claim tests passed or behavior
  was verified when those checks were not run.
- Human validation alone does not authorize full suites or adversarial reviews;
  wait for an explicit request before performing them.

## Project Layout

- `clients/desktop`: Shared React/TypeScript/Vite frontend and Tauri 2 Windows client.
- `clients/desktop/src-tauri`: Native window, tray, single instance and static web server.
- `clients/mobile`: Flutter KLM Harness app; native saved-host screens and frontend WebView.
- `engine`: Standalone Go engine for persistence and headless harness execution.
- `distribution/windows`: Independent engine NSIS installer and login/PATH helpers.
- `clients/desktop/src/design-system`: Shared UI primitives and visual tokens.
- `clients/desktop/src/features`: Feature-specific components and behavior.
- `code.html`: Original visual reference. Preserve it unless asked to change it.

Reuse existing design-system components and tokens. Keep application behavior
outside generic UI primitives. Preserve the established visual language unless
the user requests a redesign.

## Windows Runtime

- Engine and desktop have independent installers/lifecycles. Public engine CLI:
  `klm start` and `klm stop`; installed engine starts at user login via HKCU Run.
- Public API binds `0.0.0.0:7331`. The private authenticated harness bridge remains
  loopback-only; do not relax `loopbackHost` globally. Engine stop uses a per-user
  Windows named pipe, never a LAN endpoint.
- Tauri serves the same bundled frontend on `0.0.0.0:7332` while the process runs.
  Close hides the window; tray Open restores it; Exit stops only desktop/web.
- Native desktop fills its WebView. Browser Focus retains the gradient and draggable
  app shell. Determine presentation by Tauri context, not hostname.
- Fetch/SSE share `src/platform.ts` endpoint resolution: desktop localhost, browser
  page hostname, port 7331; `VITE_ENGINE_URL` remains a development override.
- User data stays in `%APPDATA%/klm/engine`, separate from installed executables.
  Development engine builds use `-tags dev`: API 17331, Focus origin 17332 and
  `%APPDATA%/klm/engine-dev` (including separate locks/control). `desktop:dev` uses
  its own app identifier and web port 17332; Vite stays on 5173 and targets 17331.
  Install/uninstall, login and LAN validation remain explicit human checks.

## Skills and MCP Library

- User skills belong to the engine user's `~/.klm/skills`. Use an absolute temporary
  `KLM_USER_SKILLS_DIR` for probes; dev/release otherwise share the approved root.
- Internal sources are editable under `engine/skills`, with authoritative
  `KLM_SKILLS_DIR` overrides. Only general-agent receives skill-library-manager.
  Its captured installation instructions and engine-resolved destination are
  delivered on Pi, OpenCode and Codex independently of external Library gates.
- Managed Windows profiles are gated to Pi 0.85.1, OpenCode 1.18.35 and Codex
  0.153.4. OpenCode projects provider/model/instruction/permission configuration
  into a controlled discovery profile; remote-config auth/org/admin variants fail
  explicitly. Codex suppresses native model-side skills/plugins/apps/hooks and
  native delegation, projects MCPs per thread and supplies selected skill metadata.
  Never present additive configuration or CODEX_HOME alone as exclusive isolation.
- Internal role tools and external Library tools use separate authenticated
  loopback capabilities (`klm_linked` / `klm_library`), even under YOLO. Native
  OpenCode children cannot inherit root conversation-role tools or internal skills.
- Keep selection/snapshots at complete engine-turn/run boundaries. Steering does
  not refresh capabilities. MCP allowlists and graph seals apply even under YOLO;
  elicitation always needs an explicit human response. Protect upstream secrets
  from harnesses, projections, snapshots, TOMLs and logs.
- MCP stdio servers have their own owned processes; harness containment is not
  their sandbox. Keep the private gateway authenticated and loopback-only. OAuth
  uses its dedicated public API callback, with a provider-accepted reachable URI.
- See SKILLS_MCP_LIBRARY_IMPLEMENTATION_PROGRESS.md for evidence and unvalidated
  runtime paths; do not infer acceptance from compilation or fixture probes.

## UI Copy

Mobile host URLs use port 7332 by default only for literal IP addresses. Never add
that port to a domain; preserve explicit ports and HTTP(S). The mobile WebView's
`KlmMobile.postMessage('home')` bridge is only for returning via the Back to hosts
footer button below Settings. Show it only with `IS_MOBILE_HOST`, never by viewport
size. The rail logo opens the engine's singleton general-agent chat on every client.
Flutter visual primitives live in `clients/mobile/lib/design_system` and follow
the existing web tokens; host behavior belongs in feature modules.

- Keep all application-controlled UI text in English, including picker buttons.
- Assume users understand the tool. Avoid obvious explanations, tutorial-like
  helper text, and redundant labels such as "Mock", "Demo data", or "UI preview".
- Show only information that helps users decide, act, or recover from an error.
  Keep implementation limitations in documentation rather than repeating them
  throughout the interface.
- Use custom, English-labeled buttons for native file inputs instead of exposing
  browser-generated text such as "No file chosen". Native system dialogs may
  still follow the operating system's language.

## Collaboration

The engine-owned general conversation has no user ProjectID and uses a dedicated
`workspaces/general-agent` directory below the engine data directory. Reuse normal
chat execution, history and request controls. Its dedicated private tool catalog
can discover/read project conversations, send visible instructions, ask correlated
questions and apply the documented session controls across nonremoved projects.
These include explicit-project top-level creation, chat Stop, exact queue removal
and Send now, archive/restore, and exact pending permission replies through the
owning chat. Creation reuses durable spawn receipts and actual general-chat human
message references; agent prompts remain attributed. Stop does not stop independent
graphs. Uncertain delivery retries require an explicit user recovery decision;
permission replies preserve the requested scope and available decisions.
Do not expose implicit-project linked/side-agent, spawn or graph execution tools
there, or give normal sessions its global scope. No automatic monitoring or
completion notifications. Agent-authored instructions never become human graph
or spawn authorization. Its harness locks durably at first message acceptance,
including queued input; model/effort remain normal turn settings.

Only the general agent's private catalog exposes `project_add`. On the user's
registration request it accepts a name and an absolute existing directory on the
engine computer, including with no registered projects. Never infer the path from
the client, selected project or general workspace. It returns created/existing/
restored with a usable project ID, keeps active matches unchanged and restores
removed identity/history/grouping; multiple matching records conflict with IDs.
Registration is independent of Data Sources and does not authorize sessions,
graphs or file work. Preserve manual HTTP registration, shared validation,
lock-protected matching/mutation, commitLocked persistence and client notifications.
Do not expose the tool to normal chats, side chats, graph nodes or subagents.

Implement directly when the request is clear. Ask only when a missing decision
materially blocks the work. Keep progress updates and final explanations concise.
Preserve unrelated user changes. Do not commit or push unless explicitly asked.
