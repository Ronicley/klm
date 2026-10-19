# Skills/MCP library — implementation progress

Source implementation is delivered for the initial managed Pi path. This document
records evidence, not acceptance. OAuth and Elicitation are included implementation,
not optional backlog. End-to-end engine/model/provider/mobile acceptance is pending.

## Initial inspection — 2026-10-19

- Read the complete plan, AGENTS.md and product.md. Existing concurrent changes
  include API/adapters, general tools/prompts, GitHub feed and product documentation;
  they are retained. No graphs, sessions or subagents are started for this work.
- Installed CLI versions: Pi **0.85.1**, OpenCode **1.18.35**, Codex **0.153.4**;
  Go **1.24.2 windows/amd64**. Version commands only, no model requests.
- Pi source `dist/core/resource-loader.js:316-334` explicitly replaces discovered
  extensions/skills with CLI paths under `noExtensions`/`noSkills`. Only the owned
  extension is supplied; it cannot load third-party resource hooks. Providers/auth
  and project instructions keep native paths. A disposable fixture probe is in
  `engine/probes/pi-library.mjs`; execution results are recorded below.
- OpenCode remains **not validated**: additive paths/config do not prove isolation.
- Codex remains **not supported for managed capabilities**: CODEX_HOME and path
  disabling do not establish a permanent allowlist. No global configuration changes.
- SDK chosen: official `modelcontextprotocol/go-sdk v1.4.0`, compatible with Go 1.24,
  stdio/Streamable HTTP, tools/resources, form/URL elicitation (2025-11-25). Its full
  client OAuth is experimental/build-tagged. The implementation uses its public
  `oauthex.ProtectedResourceMetadata` representation, engine-owned bounded metadata
  discovery/validation and `x/oauth2` PKCE/token handling. No experimental build tag
  is needed and no handwritten MCP transport/protocol stack was added.
- OAuth design: explicit per-connection redirect URI to the **public engine API**
  callback, with HTTPS for remote clients (loopback HTTP only for local use), accepted
  by the provider's client registration. Callback transaction is expiring, one-use,
  state/PKCE bound. The private authenticated gateway remains loopback-only. Mobile
  must use the reachable engine origin, never mobile localhost. No universal provider,
  tunnel or device-flow promise. A disposable provider/callback prototype passed;
  real provider and real mobile/WebView acceptance remain unvalidated.

## Compatibility evidence

| Path | Managed status | Evidence / boundary |
| --- | --- | --- |
| Windows, Pi 0.85.1 `dist/cli.js` through Node | Supported initial gate | Installed-source inspection; resource-loader and real RPC fixtures passed; exact effective tool and skill-path handshakes before prompt. Actual model task/resume/Stop not exercised. |
| Other Pi version/entry/platform | Not validated; gated | Version/installation/platform gate refuses managed startup. No automatic update. |
| OpenCode 1.18.35 | Not validated | No exclusivity inferred from additive paths/config directories. Legacy empty-selection use remains explicit; nonempty selection blocks execution/preflight. |
| Codex 0.153.4 | Unsupported for managed selection | No CODEX_HOME/deny-current-paths exclusivity claim. Legacy empty-selection use remains explicit; nonempty selection blocks execution/preflight. |

The first real Pi RPC probe exposed a built-in `llama` provider command, which the
resource-loader-only probe did not contain. The implementation explicitly retains
that exact inline command and rejects unknown commands/tools; skill identity also
checks the captured SKILL.md path. The corrected RPC probe passed with empty and
selected snapshots. It includes global/project skills, a configured local package
with a forbidden throwing extension, and a missing npm package fixture. `--offline`
prevents native startup package installation/update; it does not disable model
execution. Native model/provider/auth files and project instructions remain in place.

## Implemented in source

| Area | Files / behavior |
| --- | --- |
| Skill library | `engine/skill_library.go`: home/absolute probe override, bounded complete-package scan, UTF-8/frontmatter/portable-path validation, collision diagnostics, repeated stable read, content revisions and atomic private snapshots. Invalid metadata remains visible and safely scanned invalid packages can be removed. |
| Internal skill | `engine/internal_skills.go`, `engine/skills/skill-library-manager/*`: authoritative editable loader, explicit general-agent association, complete installation guidance; recursive Windows packaging/uninstall separation. |
| Selection | `engine/capabilities.go`, store/history/runtime integration: empty migration, desired/applied/preparing separation, optimistic edits, complete-turn snapshots/runtime keys, external-only side/spawn inheritance, offline diagnostics. Steering/Send now semantics retained. |
| Agents/runs | Agent TOML fields, effective-harness preflight, content/config/known-catalog snapshots and a locked library revalidation before durable reservation at actual start, library integrity hash, unchanged captured runs, read-only selection in node activity. Terminal gets no MCPs. |
| MCP registry/secrets | `engine/mcp_connections.go`, `mcp_secrets_*.go`: explicit CRUD/test, separate args/cwd/env, per-user Windows DPAPI protected records and presence-only projections. Non-Windows protected-secret storage fails explicitly. No implicit installation or native global import. |
| MCP client/gateway | `engine/mcp_client.go`, bridge/Pi/policy integration: official SDK stdio/Streamable HTTP; independent owned stdio process groups; bounded paginated schema-validated discovery; stable reverse mapping; positive tool/resource routes; dispatch-time validation; catalog-change refusal; structured content/`isError` translation; graph admission, worker drainage and finality distinction. |
| OAuth | `engine/mcp_oauth.go`: protected-resource/issuer discovery, challenge metadata, DCR or configured client, PKCE/state/issuer/resource, expiring one-use dedicated public callback, protected refresh serialization, disconnect of captured credential references, no credential-bearing redirects or tool retry on auth error. Explicit mobile same-tab navigation and same-origin return-to-Library link. |
| Elicitation | `engine/mcp_elicitation.go`, questions/cards: flat primitive/string-enum-array forms and HTTPS URL mode; one owning connection/call/turn; Accept/Decline/Cancel; normal pending-request graph projection; timeout/Stop; general-agent/native ordinary question replies rejected for these forms. No YOLO answer, fabricated user event or permission grant. |
| Deletion | `engine/library_api.go`: revisioned preview of sessions/known-project TOMLs/captured runs; durable state/authoring outbox; checked roll-forward/recovery; source tombstones and retained runtime copies. Inaccessible projects/external edits block completion instead of being overwritten. |
| UI | Shared `features/library`: Settings Library, explicit connection test/auth, tool-level positive selection, true selected counts/pending/unavailable, correct conversation targeting in main/general/Focus/overlays, side Session options, agent editor and graph read-only capture. No skill upload/editor/automatic send. |

## Checks actually performed — 2026-10-19

- **Go engine dev compilation: passed.** Final command:
  `go build -tags dev -o <approved-temp>/klm-library-check.exe .`
  from `engine`. The binary was not started or installed. An earlier compile found
  new-module wiring errors; they were fixed before the successful final build.
- **Shared frontend TypeScript: passed.** `npx tsc -b --pretty false` from
  `clients/desktop`, after the Library/mobile-return UI changes.
- **Owned Pi extension strict TypeScript: passed.** A temporary no-emit config
  resolved the installed Pi 0.85.1 and TypeBox declarations and checked only
  `engine/pi-permissions.ts`. It caught a text/image union access in Choice parsing;
  the access was narrowed and the targeted check passed. No installed files changed.
- **Pi resource-loader fixture: passed.**
  `node engine/probes/pi-library.mjs <installed-pi-package>`; empty/selected skills
  and excluded global extension. No model call or credential/library write.
- **Actual Pi RPC startup fixture: passed after correcting the built-in-provider
  assumption.** `node engine/probes/pi-runtime.mjs <installed-pi-package>`;
  actual owned extension readiness, empty/selected skill catalog, excluded project/
  global/package resources, missing package skipped offline. All fixtures and the
  native agent/session directories were temporary and removed.
- **Standalone SDK/OAuth protocol prototype: passed.**
  `go run ./probes/protocol` from `engine`; stdio subprocess and Streamable HTTP
  initialize/list/call/close, structured tool content and resource list/read. A
  disposable provider accepted a **non-loopback HTTPS callback URI**, routed by a
  fixture-only trusted TLS client to an owned listener; PKCE exchange and refresh
  passed. This checks SDK/callback design, **not the engine Library endpoints**,
  a real provider, a real remote/mobile device or native harness tool execution.

No full test suite, adversarial review, review agent, model execution, graph run,
session creation, installer build/install, real credential operation, global plugin
installation, real skill-library write or running-engine restart was performed.

## Concrete remaining validation / limitations

1. **Human acceptance is still required.** The engine Library endpoints, full Pi
   model turns, native history continuation, exact runtime replacement, and actual
   permission/Elicitation replies have not been exercised end to end here.
2. **OAuth provider/mobile compatibility is conditional on configuration.** The
   redirect must reach the engine API and be accepted by that provider. The fixture
   does not establish a real provider's DCR support, client requirements, TLS ingress,
   embedded-WebView policy, remote login, token revocation or refresh behavior.
3. **Only the initial Pi gate is delivered as managed.** Other harnesses stay
   explicit legacy/unknown or reject nonempty managed selection. This includes the
   general agent's mandatory internal skill being unavailable on legacy runtimes.
4. **Known graph catalog boundary:** a run captures the locally known catalog.
   Execution-directory-relative stdio is not started in a guessed directory just
   to discover a catalog before its workspace exists. An activation reconnects in
   its actual cwd, compares the captured contract, and refuses new/changed tools;
   a run with no captured catalog warns instead of injecting it later. Test/discover
   the connection before selecting graph tools/resources.
5. **Protocol subset/limits are explicit.** No Prompts/Sampling, old HTTP+SSE,
   resource subscriptions or general public MCP gateway. Flat forms/string enum
   arrays and HTTPS URL mode are implemented; unsupported forms get a protocol
   error. Audio/unknown result blocks and oversized content fail explicitly. Images
   have a Pi representation, not proven end-to-end multimodal delivery.
6. **Lifecycle/recovery evidence is compilation, not runtime acceptance.** Real
   stdio descendant shutdown, lost remote replies, graph Choice settlement, active
   snapshots after deletion, DPAPI restart behavior and interrupted outbox recovery
   still need deliberate human validation. Snapshot/trash copies are conservatively
   retained; no automatic snapshot-directory cleanup is enabled.

## Round 1 corrections and round 2 review approval

R1–R3 from `SKILLS_MCP_LIBRARY_REVIEW.md` have been corrected in source. The second
review approved these corrections and the reviewed Library implementation, and
independently reran the focused probes successfully (7.606s). The review record
releases commit/push under the user's original request; the delivery result is
reported separately. This is code/regression approval, not end-to-end acceptance.

Before publication, the feature-only Git index was checked for whitespace errors
and its engine files exported to a disposable directory. A dev Go build of that
staged-only engine passed, excluding concurrent GitHub/feed and adapter-diagnostic
changes. Mixed API/product/README hunks were staged selectively; unrelated work
remains in the shared working tree.

- **R1:** `engine/mcp_client.go` now owns a per-connection HTTP transport and
  cancellable request lifetime. DELETE cleanup and the close fallback have a
  two-second budget; normal POST/SSE have no short total client timeout. Cleanup
  uses last-dispatched auth instead of starting refresh/waiting on its mutex,
  closes the DELETE body the SDK otherwise leaves open, cancels owned requests
  and closes idle connections. Session-close errors propagate as lifecycle errors,
  including the graph missing-catalog branch. `engine/mcp_oauth.go` also makes an
  in-flight HTTP request's wait for serialized credentials context-cancellable.
  This is local cleanup, not proof that a remote call/job was cancelled.
- **R2:** `engine/graph_snapshot.go` semantically compares absent/empty external
  selections (including empty per-connection tool lists) while retaining raw
  source hashes and strict comparison of other fields, IDs, selected names and
  ordering. Existing nil selections and explicit `skills = []` /
  `mcp_connections = []` survive snapshot JSON persistence/revalidation.
- **R3:** `engine/mcp_client.go` iterates saved selected names against captured and
  discovered catalogs. Disappeared selected chat tools now generate model/user
  diagnostics, remain selected, and acquire no route. New unselected tools remain
  excluded.

Focused evidence: `go test -tags dev -run '^TestLibraryReviewFix' -count=1
-timeout 30s -v .` passed (five tests plus the empty-selection subcases; final
elapsed package time **7.632s**). The fixtures exercised actual `connectMCP` with a
localhost SDK server whose DELETE stayed blocked: close returned a lifecycle error
in **2s**, even with the credential mutex held, and cancelled an in-flight credential
wait. Normal RPC/SSE requests lasting **2.1s** still succeeded; normal DELETE
cleanup succeeded. The missing-graph-catalog cleanup error propagated, omitted and
explicitly empty agent fields passed full graph-snapshot JSON revalidation while
real field/selection changes were rejected, and the removed chat tool generated
both warnings without changing the selection/allowlist.

The first focused invocation exposed a missing node name in the new R2 fixture;
the fixture was corrected before the passing final invocation. Temporary probe
source was moved out of the repository to
`C:\Users\Samuel\AppData\Local\Temp\opencode\klm-library-r1-r3-probe_test.go` for
reproduction. No full suite, model/provider call, harness/engine/graph execution,
real credential/library operation, new reviewer/session, commit or push occurred.
Frontend code was unchanged in this correction round, so its checks were not rerun.

## Suggested manual acceptance route

Use a deliberately chosen development engine with an absolute temporary
`KLM_USER_SKILLS_DIR` before touching the shared real library. Start it only when the
operator chooses; this implementation session did not restart any engine.

1. Open Settings → Library; validate an installed complete skill, invalid metadata,
   path/link error and refresh. Ask a **Pi** KLM Agent to install a harmless skill;
   check complete files, no activation, normal write/shell permissions, and exclusive
   internal manager distribution. Change internal/reference content and check the
   next turn's revision without recompilation.
2. Select different skills/tools in two Pi chats; confirm empty selection excludes
   global/project/package resources and positive filters still hold in YOLO. Save
   during a tool call, steer, then Send now; verify old/new full-turn boundaries,
   same native history/cwd and runtime replacement. Check side/spawn inheritance,
   overlay/Focus/mobile composer targeting and incompatible harness diagnostics.
3. Add an already-installed harmless stdio server and an HTTP fixture. Registration
   must not start them. Explicitly test/discover, select a subset and resources,
   inspect result/error/structured content, pagination, changed schema, unavailable
   connection and oversized/unsupported response diagnostics.
4. Configure a provider-accepted reachable OAuth redirect. Authenticate in desktop,
   remote browser and the mobile WebView, return to Library, test, refresh/expire/
   disconnect and check one-use/expired/bad-state callbacks. Confirm no upstream
   credentials reach public projections, native snapshots/configuration or logs.
5. Exercise MCP forms and URL requests: Accept, Decline, Cancel, required/types/
   options, YOLO, Stop, timeout, stale answers, concurrent sessions and graph-owner
   projection. Answers must return only to the exact server request, not new chats.
6. Explicitly authorize a separate small graph only when ready. Validate Agent/Join/
   Fork snapshot/cwd/worktree behavior, later library edits, continue_target, seal
   and genuine call drainage. Simulate a lost remote response without auto retry.
7. Preview deletion with chat/side/general/agent/run references. Confirm future
   references removed, active run preserved, external-file conflict and recovery,
   restart, real stdio cleanup, dev/release data separation and installer distribution.
