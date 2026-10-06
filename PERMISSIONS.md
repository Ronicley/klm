# Permissions

## Controls

- Card: **Allow** (current request), **Allow session**, **Deny** (current request).
- Card menu: **Allow in this project**, **Deny in this project**, **Allow globally**,
  **Deny globally**. The scope shown above these choices is what is remembered.
- Composer menu: **YOLO mode**, per conversation. It can change during work;
  enabling resolves pending permission cards without stopping the turn. Model and
  effort still require an idle turn. Codex full-access turns cannot disable YOLO
  until they end, because their native sandbox cannot be safely downgraded live.
  Main and side conversations have independent settings. Private graph-node
  sessions retain their own normal policy; main-chat YOLO is not graph authorization.
- Recognized deletion commands and file-deletion patches display a warning icon.
  Recognition is deterministic; no permission-review agent is launched.

Rules live in the engine's existing `state.json`, alongside legacy exact grants.
Session rules are tied to the persistent KLM conversation, not just its process.
Project rules cover its conversations; global rules cover this engine, not other
engine installations. New rules also resolve matching cards already pending.

## Normal policy

Evaluation order: saved deny, saved allow, legacy exact allow, workspace defaults,
then a human decision. A more local allow does not defeat a global/project deny.
Removing the deny is necessary to revoke that restriction.

Initial defaults for understood operations:

| Operation | Inside execution workspace | Outside |
| --- | --- | --- |
| Structured file read/list/search | Allow | Ask |
| Structured write/edit | Allow | Deny |
| Identified deletion | Ask | Deny |
| Small set of exact read-oriented commands | Allow at workspace root | Ask |
| Unknown command/tool | Ask | Ask |
| Skill loading and reads of installed skill resources | Allow | Allow |
| Identified MCP tool invocation | Allow | Allow |

Skill/MCP allowances apply in normal mode, after explicit saved rules, without
enabling YOLO. OpenCode MCP identity comes from the owned server's registered MCP
namespaces, including names without an `mcp_` prefix. Skills are recognized through
the native `skill` tool or read operations under the standard project/user skill
directories (`.agents/skills`, `.claude/skills`, `.opencode/skills`, user
`.config/opencode/skills`, `.codex/skills`, and Pi skill directories). Loading a
skill does not approve subsequent commands or writes requested by that skill.
Codex's supported, empty MCP tool-approval forms are automatically accepted; forms
asking for user input are not answered automatically. Pi currently runs with external
extensions disabled; this policy does not add an external MCP transport to Pi.

The workspace is the actual execution directory, including a graph worktree.
Resolved paths, existing ancestors and symlinks/junctions are considered before
classification. Unresolved paths, expansions and compound shell expressions are
not assumed to stay within the workspace. This preflight is not an OS sandbox:
scripts, subprocesses, filesystem races and remote MCP effects are not contained.

The initial command set is `pwd`, `Get-Location`, `git status`, `git status --short`,
`git diff --stat`, and `git branch --show-current`. Recognition describes the direct
command, not a guarantee about executable substitutions, Git configuration or
filesystem effects. Additional Codex permission/network/environment requests do not
receive this automatic command allowance.

Simple literal `rm`, `rmdir`, `del`, `erase`, `rd`, `Remove-Item`, `ri`, and `unlink`
targets are resolved against the command's working directory. Flags outside the
supported subset, expansions, wildcards and compound commands require a decision.
The warning recognizer also flags `git clean`, `git reset --hard`, `Format-Volume`
and `Clear-Disk`; it does not claim to recognize every destructive action.

## Remembered scope

- File scopes identify operation plus canonical resources. Read does not grant write.
- Reads under a recognized NPM root share a **read-only directory-tree scope**:
  the execution directory's `node_modules`, `%APPDATA%/npm/node_modules`,
  `%LOCALAPPDATA%/npm-cache`, `$HOME/.npm`, or an absolute `npm_config_cache` /
  `NPM_CONFIG_CACHE`. The actual root is shown on the card. Approving a shared cache
  globally therefore works from another project and for another file in that cache.
  Distinct `node_modules` roots remain distinct resources.
- Command scopes retain the full command and native extra permissions; execution
  directories under the workspace use a workspace-relative identity. Command syntax
  remains harness-specific. Deletion scopes also retain their command/patch details.
- OpenCode `webfetch` uses its exact input/URL, independently of project directory.
  There is no blanket HTTP-domain allowance or network firewall in this first slice.
- Opaque tools retain their exact harness scope, including directory context where
  supplied. Missing native semantics never produce a guessed broad grant.
- Codex turn-scoped permission grants still last for the native turn. Unsupported
  forms and incomplete requests cannot become remembered or auto-approved grants.

## Harness coverage and YOLO

Pi's owned extension still gates tool dispatch, but the engine answers understood
workspace operations automatically. OpenCode's owned plugin gates tool calls before
execution, including calls that would not emit a native approval request. If that
call subsequently emits the corresponding native permission prompt, its already
resolved decision is reused once. Unrelated native guards are not implicitly waived.
Normal-mode native denies remain enforced by OpenCode itself.

Codex has no equivalent general pre-tool hook in this integration. Rules apply to
the approval requests it exposes; its normal `on-request` / `read-only` sandbox is
retained. Rules must not be presented as a universal interception guarantee for
unprompted reads, commands or remote tools in Codex.

YOLO precedes saved rules. Pi requests receive automatic replies. OpenCode uses an
in-memory plugin configuration with permissive tool and agent permissions, without
editing the user's harness configuration. Codex is resumed/started with `never`
approval policy and `danger-full-access`, and the returned settings are checked.
The engine consults the stored setting for every permission request, so live
changes apply to pending/future approvals without restarting the active runtime.
Pi/OpenCode pre-tool gating also observes disabling during the turn. Approved
operations are not revoked; callbacks already being delivered retain their actual
outcome. Native startup configuration refreshes before the next turn. Codex retains
its current native sandbox until then; enabling from normal mode auto-answers its
exposed approvals, while disabling a turn started with native full access returns
a conflict rather than claiming protection that has not been applied.
Native subagents run under their parent's harness configuration. Unsupported native
forms are declined rather than fabricated or presented as permission cards in YOLO.
User questions still wait for answers; graph authorization and lifecycle gates remain.

## API and recovery

- `PATCH /api/sessions/{id}/permissions` with `{ "yolo": true | false }`.
- `GET /api/permission-rules` lists remembered rules, including their IDs and scopes.
- `DELETE /api/permission-rules/{ruleID}` revokes a remembered rule. Send the usual
  JSON content type. Existing native turn grants cannot be retroactively withdrawn;
  finish/stop that turn before expecting native grants to expire.

There is no separate rule-management screen in this slice. Automatic decisions are
recorded in conversation history with their source and scope. Persisted rules survive
engine restart; pending tool calls do not resume automatically.

## Human validation

Targeted policy unit tests cover workspace boundaries, known deletion targets, NPM
read scope and deny precedence. Type/syntax checks do not validate harness behavior.
Validate the new OpenCode plugin with the installed harness, normal-to-YOLO-to-normal
transitions in each harness, shared NPM approvals across projects, global/project
denials, and the two composer menus in desktop/browser/mobile WebView.
