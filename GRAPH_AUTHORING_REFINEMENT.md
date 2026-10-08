# Agents and Graphs — Consolidated Refinement

Updated: 2026-09-13.

Latest invocation decision: section 13 (2026-09-13) supersedes the earlier
mandatory workspace question and workspace-authorization event requirement.
Latest lifecycle decision: section 14 (2026-09-13) supersedes blanket external-MCP
rejection and any requirement to terminate shared MCP servers or detached remote
tasks after their calls return. Node and tool-call finality remain required.
The pre-implementation milestones below remain historical; implementation progress
is recorded separately in `GRAPH_ENGINE_IMPLEMENTATION_PROGRESS.md`.

## Purpose and status

This document records decisions approved with the user before implementation.
It is the shared record for incremental refinement: discuss one area, resolve its
questions, then consolidate its rules here. Once all areas have been refined,
produce the actual implementation plan from these decisions.

This started as a refinement record, not the final graph-engine implementation
plan. The user subsequently authorized implementing agent and graph CRUD before
runtime refinement. Current implementation status and API are in `engine/README.md`
and `clients/desktop/README.md`; prototype behavior is not an execution contract.

The agent registration and graph authoring/file-persistence decisions below are
approved and the CRUD has been implemented and human-validated. Graph execution
itself is not implemented. The conceptual refinement for the current runtime
scope is now closed, including the cross-cutting decisions in section 11.
Explicitly deferred features remain outside this phase. The user subsequently
closed the initial monitoring scope with a minimal active/completed-node display
and the existing composer LED (section 12). The technical implementation plan is
now recorded in `GRAPH_ENGINE_IMPLEMENTATION_PLAN.md`, following static analysis
of the current code with user-authorized subagents. Runtime implementation has
not started; this refinement record does not itself authorize it.

Related context: `product.md`, `CONTEXT.md`, and the existing runtime refinement at
`C:/Users/Samuel/Documents/Projects/Personal/klm-test/GRAPH_ENGINEERING_REFINEMENT.md`.
For every topic explicitly resolved here, this document supersedes conflicting
prototype behavior, older memory and the external historical refinement.

### Current status

- Agent/graph CRUD is implemented; its code and this document are still uncommitted.
- Current work is conceptual runtime refinement, not an authorization to implement
  graph execution yet. Use the latest confirmed rules in each section.
- Join refinement is closed for the current scope. J1, J2 and J4–J8 were approved;
  J3 (nested Forks) is intentionally not treated in this phase. Do not reopen it
  or mistake the earlier nesting proposal for a decision.
- Prompt wording is delegated to implementation. Terminal behavior is closed.
  Normal parallel work reconverges through Join; worktree isolation is optional
  per Fork branch and defaults on.
- S1 is approved: ordinary agent nodes also reuse their latest session only when
  the working directory is unchanged. Per-node activation limits and execution
  timeouts (L1/L2) are explicitly deferred; their proposals were not approved.
- Retry/escalation and scheduled-work ordering remain settled (R1/R2); do not
  reconfirm them without a concrete new inconsistency. C1 is approved: each run
  uses its own graph/agent definition snapshot taken when it starts.
- E1 and W1 are approved: preserve pending notifications/scheduled work across
  engine restart, end interrupted runs, and explicitly map reused workspaces.
- W2 is resolved with the user's exception: a fresh run in the original directory
  uses its existing files as-is; no automatic reset or new worktree is required.
- The execution-engine conceptual agenda and initial monitoring scope are closed.
  Section 12 supersedes the larger monitoring proposal: use the existing Graph
  tab for active/completed-node indicators and keep the composer running LED.
  New run tabs, history, detail panels and the other monitoring additions are
  deferred. Proceed to the technical plan without reopening settled rules absent
  a concrete new inconsistency.
- The implementation plan is `GRAPH_ENGINE_IMPLEMENTATION_PLAN.md`, also saved
  under `plans/graph-engine-implementation.md` in AI Memory. P0/P1 are its starting
  increments; code changes and native execution were not performed to write it.
  Present genuine open questions together; do not ask again about ordinary agent
  tasks such as resolving conflicts or using blocked when unable to proceed.
- Builder validation UI and engine-owned worktree cleanup are future work.
  Interruption/resumption remains a later priority; do not silently implement it.
- Preserve the established UI structure. Structural layout changes require the
  user's approval; editor controls belong in the existing header.

## 1. Project-owned files

`agents` and `graphs` are sibling directories inside the project's `.klm` folder:

```text
project/
  .klm/
    agents/
      planner.toml
      implementer.toml
      delivery-reviewer.toml
    graphs/
      implementation-review.yaml
      implementation-review.yaml.layout.json
```

- One TOML file per reusable agent.
- One YAML file per reusable graph.
- A companion `<graph>.yaml.layout.json` stores presentation separately.
- YAML is authoritative for graph behavior. Execution must work without layout.
- Positions, viewport and visual connection details belong to layout. Executable
  connections and destinations belong to YAML; layout must not change execution.
- These files belong to the selected project's directory, not a global catalog.

## 2. Reusable agent registration — approved

### TOML contract

```toml
name = "Planner"
description = "Turns a request into an implementation plan."
enabled = true

default_harness = "opencode"
model = "provider/model-id"
effort = "high"

prompt = """
Turn the request and supplied context into a focused implementation plan.
Follow the project conventions and identify the relevant acceptance criteria.
"""
```

The model and effort above are illustrative, not real selections or defaults.

| Field | Rule |
| --- | --- |
| `name` | Required, human-readable catalog name. |
| `description` | Optional short description. |
| `enabled` | Boolean; new agents start enabled. |
| `default_harness` | Required: `opencode`, `codex`, or `pi`. |
| `model` | Required real model identifier from the selected harness's catalog. |
| `effort` | Required when the selected model supports configurable effort; omitted when it does not. |
| `prompt` | Required text, stored directly in the TOML as part of the agent definition. |

### Identity and file lifecycle

- The identifier is derived from the name using the agent slug convention:
  `Delivery Reviewer` becomes `delivery-reviewer`.
- The filename is `<slug>.toml`. Do not add an independently editable `id` field
  to TOML or the registration form.
- The form's Identifier remains derived and read-only.
- Reject names that collide after slug derivation; never overwrite another agent.
- Creation creates `.klm/agents` when necessary and writes the agent file.
- TOMLs are the source of truth for the project agent catalog. Editing updates the
  corresponding file.
- Renaming changes the identifier and filename when the derived slug changes.
  Once graph references exist, renaming must update those references as well.
- Enable/Disable remains a list action backed by `enabled`; it does not require a
  new registration-form field.

### Required UI alignment

- Existing Name, Identifier, Description, Default harness, Model, Effort level and
  Prompt controls cover the file's registration fields.
- Replace fictional model/compatibility lists with the real harness catalog.
- Store the real model identifier, not its display label.
- Validate effort against the selected model. Models without configurable effort
  must remain saveable without this field.
- Remove fictional fallback model/effort values from real persisted-agent handling.
- No additional registration field was found necessary for the approved TOML.

## 3. Graph registration and authoring persistence — approved

### Creation flow

1. New graph opens a modal containing Name and Description.
2. Name is required; Description is optional.
3. A valid name enables Create; naming validation must prevent derived filename
   collisions, not merely duplicate visible names.
4. Create establishes the draft's identity and opens its canvas.
5. Save persists the graph definition as YAML. The first Save also places the graph
   in the project catalog.

Enable/Disable remains a catalog action corresponding to graph `enabled`.
Graph metadata must be connected to the canvas's actual definition rather than
maintained as an unrelated list entry.

### Separate definition and layout saving

- Save is the explicit action for the graph definition itself.
- Layout is persisted automatically and independently of Save.
- When the user drags a node/card, persist its final position when the mouse is
  released, rather than waiting for Save or writing every pointer movement.
- Layout before the first Save is associated with the draft identity established
  by Create.
- A layout file alone does not constitute a registered graph. Leaving before the
  first Save must not make that layout appear as a graph in the catalog.
- Do not infer an approved draft recovery/cleanup feature or a particular internal
  staging format from this rule; technical representation belongs in the later plan.
- Layout autosave must not implicitly save executable topology or node settings.

### YAML shape

The following shape was approved for graph metadata, agent nodes and Choices.
Choice keys/references below include the subsequently approved slug correction.

```yaml
name: Implementation review
description: Implement a task and review the result.
enabled: true
initial_node: implement

nodes:
  implement:
    type: agent
    name: Implement task
    agent: implementer
    choices:
      - submit-for-review
      - blocked

  review:
    type: agent
    name: Review implementation
    agent: delivery-reviewer
    choices:
      - approve
      - request-changes
      - blocked

choices:
  submit-for-review:
    name: submit_for_review
    input:
      summary:
        type: string
        required: true
    output:
      task: "{{run.input.task}}"
      summary: "{{choice.summary}}"
    to: review
    session: new

  request-changes:
    name: request_changes
    input:
      findings:
        type: string
        required: true
    output:
      findings: "{{choice.findings}}"
      instructions: "Address the review findings."
    to: implement
    session: continue_target

  approve:
    name: approve
    input: {}
    output: {}
    terminal: true

  blocked:
    name: blocked
    input:
      reason:
        type: string
        required: true
    output:
      reason: "{{choice.reason}}"
    terminal: true
```

This is a configuration example, not an approved automatic graph template or an
execution request. The general availability/exposure and runtime semantics of
`blocked` still belong to runtime refinement; its presence in this example does
not close those questions.

### Canvas coverage required in the final schema

All existing authoring settings must be represented, including those absent from
the compact example above:

| Element | Required coverage |
| --- | --- |
| Agent node | Own identity and name, reusable agent reference, available Choices, optional `overrides` for harness/model/effort. |
| Choice | Derived identity, normalized name, optional `description`, input contract, output templates, terminal outcome or single destination, eligible session policy. |
| Terminal command | Own identity, name, command text and outgoing destination. |
| Fork | Own identity, name, branches with their own identities, free-text names, Git branch names, output payloads and destinations. |
| Join | Own identity, reusable agent, optional additional prompt, optional output Git branch and available Choices. |

The user approved adding this coverage. Exact YAML key names for Terminal, Fork
and Join beyond the agreed concepts have not been presented as a complete schema;
complete that representation in subsequent refinement/planning without inventing
runtime behavior.

Incoming revision is the common base rule for worktrees created by a Fork. The
original fixed Separate worktree rule is superseded by the optional-isolation
refinement in section 10; its UI change is deferred to implementation.

### Choice identity and terminal output

- Preserve the configured normalized name, e.g. `request_changes`.
- Derive its ID with the agent slug convention, e.g. `request-changes`.
- YAML keys and references use that ID; `name` retains underscores.
- A nonterminal Choice has one destination; a terminal Choice has no destination.
- Output payload remains configurable for terminal Choices, including End graph.
- Update the form to stop hiding/discarding output when a Choice becomes terminal.
- Session policy remains absent for terminal Choices and applies only to agent or
  Join destinations. Omission means `new`; `continue_target` retains the previously
  agreed destination-node session behavior.

### Values supplied by the canvas rather than manual form fields

| File value | Source |
| --- | --- |
| `initial_node` | Explicit initial node marked Start. |
| Node `type` | Selected node type. |
| Node IDs | Canvas-generated identities, independent of reusable-agent IDs. |
| Node `choices` | Node-to-Choice connections. |
| Choice `to` | Choice-to-destination connection. |
| `terminal` | Continue / End graph control. |
| `session` | New session / Continue target control for eligible destinations. |
| Input `type: string` | Current input-field contract; no type picker needed. |
| Input `required` | Required toggle. |
| Output fields | Existing name/value editor and supported templates. |

Current output authoring supports a flat object of text/template values. The
examples above fit it. Arbitrary nested objects, arrays and typed literal values
were not added to the scope by calling the resulting payload JSON.

## 4. Subsequent refinement and implementation-plan gate

### Subsequent authorization and scope

The user authorized implementing the CRUD covered here now, independently of graph
execution. Restrict the current delivery to agent registration and graph authoring.
Do not work on the chat graph selector in this delivery. Do not create
`.klm/harness.toml`: that configuration file was discussed and explicitly deferred
pending future refinement.

Validation requested: use Playwright to create a graph, leave and reopen it, create
an agent and confirm that it is available to graph nodes, and disable/enable the
graph. Inspect saved files for the approved folder structure. Lightweight compilation
checks are appropriate; no full suites, adversarial/deep reviews or extra review
stages were requested.

Next areas are chosen by the user. Append each area's resolved decisions here,
clearly distinguishing approved rules, examples and outstanding questions.

The original runtime inventory included invocation/input, harness result transport,
blocked, counters, limits, Terminal, Fork/Join and Git lifecycle. Many conceptual
questions are now resolved in sections 5–11; section 12 closes the minimal monitoring
scope. The implementation plan is recorded separately. Graph selection still does
not itself authorize or execute a run on Send.

Interruption/resumption remains the first priority after the previously agreed
current runtime scope, not an implicit addition to agent/graph registration.

After the user finishes refining the remaining runtime areas, produce the graph
execution implementation plan. CRUD implementation is now authorized as stated
above. Commits, pushes and real graph/harness execution are not authorized by this
refinement record.

## 5. Graph invocation and the orchestrator — consolidated conceptual rules

The user has completed human validation of the CRUD and started refining runtime
rules. This section records conceptual behavior only, not an implementation plan
or permission to build runtime mechanisms. It supersedes the earlier deferral of
orchestrator integration for the topics explicitly addressed below. Historical
memory about an older engine's invocation rules is not the current specification.

### Confirmed rules

1. Initially, a run is started only by an invocation from the orchestrator: the
   agent in the chat. Other invocation methods are future work. Sending a message
   or selecting a graph does not itself start a run.
2. Every invocation must be covered by express user authorization. An explicit
   request to run a graph already grants permission for that activity; an extra
   confirmation is not inherently required. This is authorization to accomplish
   the requested activity, not a mandatory separate approval for each corrective
   run attempt after failure or blocked; see the retry rules below.
3. The orchestrator may recognize a suitable graph and propose it, but cannot
   execute solely on its own judgment. It must obtain the user's affirmative
   authorization before invoking a graph it proposed.
4. The selected graph belongs to the conversation. Selection is not authorization.
   The orchestrator may also propose other available graphs, and the user may
   authorize named graphs that are not selected. When an authorized run actually
   starts, update the composer selection to its graph so the selection and running
   indicator refer to the executing graph. Proposing or scheduling a graph does
   not itself switch the selection in anticipation of execution.
5. Invocation is asynchronous relative to the conversation. The run starts and
   reports that it is running; the orchestrator remains available to converse and
   perform other work rather than waiting exclusively for completion.
6. The limit is one active run per conversation, not per project. Different
   conversations in the same project may have active runs concurrently. This is
   the current version's restriction; multiple simultaneous runs per conversation
   are intended future work, not supported by the current contract.
7. The user can authorize a sequence in one request, e.g. run X, then run Y using
   X's result, then deliver a PDF. The orchestrator may invoke those authorized
   graphs sequentially even when they are not selected. This illustrates advance
   authorization and follow-up work, not a new graph node type or PDF subsystem.
8. Terminal outcomes and run failures notify the orchestrator. In particular,
   reaching a terminal blocked Choice delivers that outcome and its configured
   output so the orchestrator can explain the obstacle and discuss next steps.
   A terminal outcome supplies the run's result; the normal flow reconverges from
   parallel work before terminal completion, as refined in section 9. A later attempt is a
   new run, not an implicit resumption of a paused flow.
9. A completion/failure/terminal-blocked notification activates an idle
   orchestrator so it can report to the user without requiring another user
   message. If the orchestrator is busy, the notification waits until its current
   work/turn finishes; it must not interrupt that turn or start a simultaneous
   turn in the same conversation.
10. The orchestrator can consult the run's actual current state and progress:
    visited nodes, selected Choices, parallel branches, completed work and what
    remains active. It can inspect recent events from a particular node to answer
    a user's more detailed question. The example of 10–15 events expresses bounded
    recent-log access, not a finalized fixed limit or transport contract.
11. The orchestrator is the bridge between the run and the user. The run lifecycle
    must not depend on the chat agent remaining idle and waiting for it.
12. The orchestrator cannot stop the graph. Stopping is a deliberate user action.
    Its mechanism, and the delivery scope of interruption/resumption, are not
    specified by this invocation discussion.

### Scheduling and dependent activities

- When a run is already active and the user requests another, the orchestrator may
  explain the one-at-a-time restriction and commit to scheduling the authorized
  activity for later. The user does not need to repeat the request merely because
  it could not start immediately. Scheduling is not concurrent execution.
- For an authorized sequence X then Y using X's result, successful completion of
  X is a prerequisite for Y. Failure or terminal blocked in X does not satisfy
  that prerequisite.
- The orchestrator first attempts to resolve X's issue within its existing
  authorization. If unable, it asks the user for help or a decision.
- The user may explicitly change the requested sequence, including abandoning X
  and proceeding to Y without X's successful result. The orchestrator must not
  make that dependency change on its own. Abandoning the outstanding X activity
  does not give the orchestrator authority to stop an active run.
- Corrective attempts for X take precedence over independent scheduled work when
  a concrete correction exists and the necessary decisions are resolved. If X
  awaits user input, an already-authorized independent activity may start.
- Ready independent activities follow request order unless the user specifies
  another priority. Activities dependent on X's success keep waiting for it.

### Authorization of corrective attempts

- An authorized request to execute X is intended to produce a successful run of
  X for the requested task. A failed or terminal-blocked attempt does not consume
  the authorization to accomplish that task.
- If the orchestrator has enough information to address the reported obstacle,
  it may resolve the issue, adapt the input prompt with the relevant information
  and corrective instructions, and invoke a new run of X under the original
  authorization. It need not ask for approval merely because the run ID is new.
- If missing information or repeated failures prevent progress, it can explain
  the problem and ask the user for the information needed to proceed.
- For a new attempt involving an earlier attempt's workspaces/artifacts, the
  explicit reuse-versus-clean-start decision in section 10 also applies. Existing
  graph-execution authorization does not implicitly authorize deleting prior work
  or choosing the workspace-reuse policy for the user.
- Each corrective attempt is a fresh run, not a resumption of the failed run.
  Resumption is deferred. A new run does not imply that earlier filesystem changes
  have been rolled back; no automatic workspace reset was approved here.
- Authorization remains tied to the requested graph/activity; this rule is not
  permission to invent unrelated work or run other graphs without authorization.
- Further attempts require a concrete corrective action that can produce progress.
  If the same obstacle repeats without such an action, explain it to the user and
  await guidance. No numeric retry count or obligation to retry indefinitely was
  adopted. Escalation and scheduled-work ordering are settled; see section 11's
  R1/R2 clarification rather than reopening them as lifecycle-policy questions.

### Input to a run

- The initial version accepts a text prompt as the run task, stored conceptually
  as `run.input.task`. The orchestrator prepares this prompt from the user's
  request and the relevant context of their conversation.
- The graph does not receive access to the conversation history. The orchestrator
  is responsible for making the supplied task sufficiently self-contained.
- The graph's name and description are the initial information used by the
  orchestrator to understand its purpose and prepare suitable input. Description
  remains optional in the existing CRUD; no new mandatory registration field was
  added by this rule.
- Formal graph input contracts and direct file inputs are future refinements.
  Current invocation is prompt-only. This does not prevent a node's harness from
  working with project files under the applicable execution rules.
- If an unsuitable task causes the run to fail, the failure is reported back and
  the orchestrator can adapt the input and start a corrective run. Name/description
  do not themselves provide a deterministic semantic validator of task adequacy.
- How the initial node receives the task together with its agent instructions and
  available Choices belongs to the next node-execution topic; the run task remains
  text regardless of the eventual transport envelope.

### Notifications and concurrent work

- Only completion, failure and terminal blocked proactively activate the
  orchestrator in this version. Intermediate progress and node logs remain
  queryable; individual node starts/completions and transitions do not trigger
  a new orchestrator turn by themselves.
- Terminal outcome notifications carry the outcome and its configured output;
  technical failures supply the reported failure information. A busy orchestrator
  processes these after its current turn finishes, as stated above.
- While a run is active, the orchestrator may do other work, including modifying
  the same files that the graph is working on. The user explicitly accepts the
  possibility of conflicts in this initial version.
- Whole-run worktree isolation is not mandatory. The user may instruct separate
  worktree use. Fork isolation is also now configurable as refined in section 10,
  superseding the earlier always-separate-worktree rule.
- Requiring every run to execute in its own worktree is a possible future
  refinement, not a current requirement.

The six questions raised in the first pass are answered above. These rules close
the initial conceptual invocation discussion; they do not select tool names,
APIs, approval UI, scheduling storage, notification transports or runtime code.
Agent-node execution and Choices are recorded below. Retry escalation and ordering
are settled as clarified in section 11; their former pending status is superseded.

## 6. Agent-node execution and Choices — conceptual refinement closed

### Confirmed: Choice submission concludes the node's work

- The agent works toward accomplishing the task received by its node. A Choice
  is its final communication of the outcome, not a progress update or a way to
  dispatch the next activity while it continues working.
- Finishing the work does not necessarily mean succeeding. The agent may have
  completed the task, failed to accomplish it, or concluded that it cannot proceed
  or even start because information or access is missing.
- At that point it selects the available Choice that accurately represents the
  result and supplies the required input payload. Successful implementation may
  select `submit_work`; inability to proceed may select `blocked`. These are
  examples, not a universal required success-Choice name.
- Example confirmed by the user: if the work requires access to a directory and
  the user refuses that permission, leaving the agent unable to proceed, the
  agent selects `blocked` to communicate the obstacle. This is a declared outcome,
  distinct from the engine classifying the entire run as a technical failure merely
  because a permission was denied.
- A valid accepted Choice submission is the final commitment of that activation.
  The agent may not continue modifying files or performing other task actions in
  that activation afterward; the flow may proceed according to the Choice.
  Section 14 defines this boundary as the node and its tool calls: seal against
  new calls and await calls already started before acceptance, without requiring
  shared MCP servers or detached tasks beyond a returned call to terminate.
- The end of an activation is not necessarily the end of the run. A nonterminal
  Choice continues to its configured destination; an author-defined terminal
  Choice supplies the run's terminal result. Parallel paths must first reconverge
  as refined in section 9, rather than completing normally as independent branches.
  A later visit to the same node is a new activation, subject to the agreed session
  policy, not continued work after the previous result was accepted.
- Acceptance requires valid available-Choice identity and payload. The previously
  agreed rejection/correction rules still apply to invalid submissions: a rejected
  attempt does not conclude the activation or dispatch its destination. Detailed
  counters and enforcement mechanisms are not defined by this finality rule.

### Confirmed: engine-owned communication contract

- Choice submission is the only valid way for an agent node to communicate its
  work outcome to the graph. Free-form prose is not a substitute for a Choice,
  is not delivered as an alternative result, and cannot complete the activity.
- The engine's internal instructions must explicitly teach this contract: the
  agent must finish by selecting an available Choice and supplying its payload,
  rather than addressing an assumed reader through free-form final text.
- The graph author must not be required to duplicate this protocol in the reusable
  agent prompt. Providing the internal protocol instructions is the engine's
  responsibility, distinct from the user's agent-specific work instructions.
- The previously agreed availability of run/node logs for the orchestrator is
  unchanged. Observability of harness activity does not turn free text into a
  valid result or introduce a second completion mechanism.

### Confirmed: recovery within an unfinished node (2026-10-08)

- Agent and Join nodes should attempt recovery before declaring a recoverable tool
  problem terminal. Inspect the error and current state; try up to two concrete,
  distinct safe alternatives within the authorized scope, without repeating the
  same failing command indefinitely. This prompt-level bound is not a graph-run
  retry limit or a new activation/time-limit setting.
- For Git branch/base query timeouts through a wrapper or shell, consider the
  native executable directly (git.exe on Windows), preserving the working directory
  and required verification. Do not bypass checks, permission denials or explicit
  agent restrictions. Uncertain side effects require state inspection and any
  necessary user recovery decision before replaying an operation.
- If alternatives cannot resolve the obstacle, use the harness's existing native
  question tool with a concise failure, attempted alternatives and the missing
  information/decision. Allow free text when supported. Keep the activation open
  while awaiting the reply; do not submit a Choice or voluntarily end the turn.
- A reply returns to the originating native session. Apply relevant guidance within
  scope and continue unfinished work in that session/workspace, retaining completed
  graph steps. The reply is not a permission grant or expanded task authorization.
- Use blocked for a definitive impediment, a denial that prevents work, cancelled
  or unavailable recovery, or guidance that still leaves no safe way forward. Do
  not loop on the same question when no concrete progress is available.
- This reuses the existing question cards and routing; it adds no runtime state,
  endpoint or automatic interpretation of tool errors. Effectiveness is subject to
  human validation with real harnesses. Choice finality and its three-violation
  counter remain unchanged. Runs already ended, harness failures and engine restarts
  retain the existing new-attempt/interruption policy; this is not their resumption.

### Confirmed: invalid Choice submissions

- The engine validates the available Choice and its input contract. Validation
  includes required parameters, permitted field types and rejection of extra
  parameters not declared in that Choice's input contract. Optional declared
  fields may be omitted; undeclared fields are not implicitly accepted.
- Reject invalid submissions without dispatching a destination or completing the
  activation. Send corrective instructions back to the model identifying the
  available Choices, the specific reasons its last submission was invalid, and
  the requirement to submit a valid Choice.
- The engine records invalid submissions in a counter. The first and second
  consecutive invalid submissions permit correction; the third consecutive
  invalid submission makes the run fail. Do not wait for a fourth attempt.
- This counter concerns Choice-contract violations, not new graph-run attempts
  authorized by the user or the optional node activation limit.
- These are engine-owned responsibilities. How validation and feedback cooperate
  with each harness is a later implementation decision, not satisfied solely by
  telling the model to behave correctly.

### Confirmed: the agent stops without submitting a Choice

- The engine detects when the agent has ended its turn without a valid Choice
  submission. It must not infer an outcome from prose or silently regard the node
  as completed.
- Instead of immediately failing solely for that first omission, the engine wakes
  the same node's agent with corrective instructions: it ended its work without
  selecting a Choice; the activity is incomplete until it communicates its outcome
  through a valid Choice.
- This is correction of the current activation's missing result, not a new graph
  run, an orchestrator invocation or an automatic retry of all the task's work.
- Waking for correction applies to an ended turn without a result, not to an
  agent that is still executing or waiting for a tool/user interaction.

### Confirmed: omissions share the error counter

- Ending a turn without a valid Choice counts as one violation toward the same
  three-consecutive-error counter used for invalid Choice submissions.
- Invalid submissions and missing submissions accumulate together. For example:
  invalid payload, then a turn ending without a Choice, then another invalid
  payload reaches three violations and fails the run.
- The first and second violations receive corrective feedback. At the third,
  fail the run rather than waking the agent for another correction attempt.
- Repeated omissions alone therefore cannot cause unlimited corrective wakeups.
- The counter belongs to a single node activation, not to a Choice definition,
  reusable agent, native session or the entire run.
- Switching between available Choices does not reset the activation's counter.
- Each new node activation starts at zero. Revisiting the same node in a cycle
  also starts at zero, including when `continue_target` reuses its session.
- Nodes sharing a Choice do not share a counter; distinct activations have
  independent counters.

### Confirmed: built-in blocked and author-defined Choices coexist

- The engine always supplies its own `blocked` Choice to agent-bearing nodes,
  including the agent in a Join. Its availability does not depend on the graph
  author declaring or connecting an escape Choice.
- The engine supplies this Choice even when the author has defined a Choice named
  `blocked`. Do not conditionally omit the built-in Choice, replace it with the
  author's Choice, or reject the graph merely because both have that name.
- Both may be available to the model. The model chooses which one to submit;
  process the specific Choice selected, without automatically preferring or
  substituting the other one.
- The author's Choice remains a graph-defined Choice, available through the
  normal node connections. Automatic availability applies to the engine's Choice.
- These are two distinct choices by origin/identity despite the shared name. The
  later technical representation must allow an unambiguous selection; no concrete
  namespace, identifier syntax or new authoring UI is chosen in this discussion.
- This concerns agents selecting outcomes. Terminal and Fork remain types that
  do not select Choices themselves.

### Confirmed: blocked contracts and lifecycle

- The engine's built-in `blocked` takes one required string field, `reason`.
  Ordinary Choice-contract validation applies; no additional input fields are
  implicitly permitted.
- It is always terminal: end the run as blocked and deliver the supplied reason
  to the orchestrator through the agreed terminal notification behavior.
- An author-defined Choice named `blocked` follows its configured input, output,
  terminal flag and destination. Its name alone does not give it special lifecycle
  meaning, turn it into a terminal outcome or override its configured routing.
- In particular, an author-defined nonterminal `blocked` can route to another
  node that handles the issue. Such a transition does not trigger a terminal
  blocked notification merely because of its name.
- The built-in Choice remains the guaranteed terminal escape regardless of how
  the author configures any same-named graph Choice.

### Confirmed: completion and task success are distinct

- A valid author-defined terminal Choice supplies the result for normal run
  completion, after any parallel work has reconverged as refined in section 9.
  On normal completion, report the selected Choice and its configured output to
  the orchestrator, including when the Choice is named `rejected` or `blocked`.
- The engine distinguishes normal completion, blocking through its built-in
  `blocked`, and technical failure. It does not infer special lifecycle meaning
  from the name of an author-defined Choice.
- Normal completion does not necessarily mean that the user's requested objective
  was achieved. The orchestrator interprets the selected Choice's contract and
  output to understand the task outcome and explain it to the user.
- This distinction must be retained when reasoning about the previously agreed
  prerequisite of success in an X-then-Y activity. A normal-completion notification
  alone is not a semantic guarantee that X delivered the result the user wanted.
- The orchestrator also decides whether a normally completed run satisfied the
  user's requested objective. An outcome such as `rejected` may itself be a
  successful result for that objective; its name does not mandate another run.
- If the orchestrator judges another attempt necessary to achieve the requested
  result, it may invoke the same authorized graph/activity again under the
  original authorization, even after normal completion. A separate approval is
  not required merely because the preceding attempt completed normally.
- This extends the corrective-attempt authorization to interpreted task outcomes,
  not just technical failure or built-in blocked. It does not expand the user's
  requested objective, authorize unrelated graphs, require retries after every
  rejection, or establish a numeric retry limit.

## 7. Internal prompts and node instructions — refinement closed

The user explicitly requested a dedicated conceptual refinement stage for the
instructions/prompts supplied to nodes. Work through it before turning these
contracts into an implementation plan.

### Confirmed: task conflicts with the agent's scope

- If the received task is outside the agent's configured scope, the agent refuses
  that work and communicates the incompatibility through `blocked`.
- A task does not authorize ignoring explicit restrictions in the reusable
  agent's prompt. For example, an agent instructed to review without modifying
  files must not start implementing merely because the incoming task asks it to.
- Graph-level suitability/compatibility checks are future work. In this version,
  the scope conflict is identified by the agent in the node, not by a newly
  introduced graph-level semantic validator.

### Confirmed: centralized, editable internal prompt files

- Internal engine prompts must live in an accessible, centralized directory in
  the development project/engine, so the user can inspect and edit their actual
  contents while developing and experimenting with behavior.
- Store each distinct internal prompt in its own text file. If a role's
  instructions are composed from multiple prompts, keep the constituent prompts
  individually accessible rather than hiding their prose inside application code.
- The requirement covers more than node work instructions: it also includes
  orchestrator rules such as use of an existing authorization for corrective runs,
  and other engine-authored behavioral/correction instructions.
- The user must be able to add, remove and revise instructions in these files and
  then try their effect. These are development-time engine instructions, distinct
  from the reusable agents' project-owned TOML prompt fields.
- Exact prompt wording is delegated to implementation, followed by the user's
  runtime testing and edits. Development-time loading is confirmed below; no
  per-project override/settings interface is introduced.

### Confirmed organization and development-time loading

Use `engine/prompts/` in the KLM repository, with this approved initial Markdown
file organization:

```text
engine/prompts/
  orchestrator.md
  agent-node.md
  join-node.md
  invalid-choice.md
  missing-choice.md
  run-result.md
```

These represent orchestrator policy, ordinary agent-node protocol, Join integration
instructions, invalid-submission feedback, missing-Choice feedback, and instructions
when delivering a terminal run result to the orchestrator. This is an approved
initial organization, not final prompt wording or an exhaustive final file list.
Additional distinct internal prompts also belong in this directory.
Terminal and Fork do not require model prompts merely to create a file for every
node type; their existing non-agent semantics are preserved.

- During development, reread the relevant file whenever its prompt needs to be
  sent. Editing the file does not require an engine restart to affect subsequent
  sends of that prompt.
- Changes apply to subsequent sends, not retroactively to instructions already
  delivered to an agent. Editing a file does not itself interrupt or wake an agent.
- Keep engine-authored instructional prose in these files. Supply variable data
  such as the task, available Choice contracts, validation errors and run results
  alongside the instructions rather than scattering prompt prose through code.
- Release packaging and runtime loading outside development are later technical
  planning details; this agreement does not specify them.

Prompt-writing responsibilities for implementation:

- The engine-owned instructions for agent-bearing nodes, including exclusive
  communication through Choices and their available contracts.
- Composition with the reusable agent's prompt, the task/input received by the
  node, and Join's additional integration instructions.
- Instructions at first activation and subsequent activations with continued
  sessions, so the currently available Choices remain authoritative.
- Corrective instructions for invalid submissions and ended turns missing a
  Choice, including useful error feedback.
- Differences in instructions/input by node type. Terminal and Fork remain
  non-agent types; this stage does not introduce model calls for those nodes.

The user explicitly closed this refinement stage without reviewing the draft
wording. Write appropriate prompts during implementation to express the approved
contracts; do not require a separate line-by-line prompt approval before proceeding.
The user will test and tune these files once the behavior is implemented. This
delegation concerns wording, not permission to change the agreed rules or begin
runtime implementation before the remaining conceptual areas are refined.

## 8. Terminal command — conceptual refinement closed

The user explicitly closed the Terminal node's conceptual refinement. The rules
below are the agreed basis for later implementation. Output collection details
belong to technical planning; timeout/hung-process policy belongs to lifecycle
refinement. Neither is an invitation to reopen the approved Terminal behavior.

Confirmed foundation: Terminal command runs the configured command without an
agent and continues through a structural outgoing connection, not through a
model-selected Choice. It can be the graph's initial node or occur later, including
inside a Fork branch. Commands in the reference graph have not been executed.

The earlier proposal to fail a run automatically on a nonzero command exit code
was rejected. Command-result handling and the separately approved infrastructure
failure rule are defined below.

Working directory and PowerShell script execution are confirmed below. Remaining
input-binding, log/result collection and interaction rules must not be implemented
by inference.

### Data-flow gap and approved solution

- In Fork -> Terminal -> agent/Join, the Fork branch's configured output reaches
  Terminal, but Terminal currently has no configured outgoing data contract.
  Merely executing a command does not establish what data the next node receives.
- The user rejects implicit forwarding of incoming data and wants explicit control
  over what leaves Terminal, including relevant command output and instructions
  for subsequent work. Not every command produces useful downstream data.
- Terminal should not need to inspect its destination's type to expose a special
  prompt field. The previous generic JSON output convention already allows
  author-chosen instruction fields without reserving them for a destination type.
- The user considered publishing command output as a variable, then approved
  explicit local output mapping and rejected a global variable store or arbitrary
  references to earlier nodes.

Approved: give Terminal an explicit output mapping whose sources are its current
input and current command result. The author can explicitly copy selected input
fields, include selected command output and write static instruction text. No
incoming fields are forwarded implicitly; commands with no useful downstream
output need not publish their logs. The command output reference is now confirmed
as `command.result`, replacing the earlier illustrative `command.stdout` reference.
Incoming Terminal fields use the local payload notation described below; the older
illustrative `input.task` spelling is superseded by `payload.task`. The complete
output-editing contract still needs to respect the existing explicit-mapping rules.

Distinguish command exit status from command output: the exit code concerns node
completion/failure policy; stdout/stderr are command data/logs. `git diff --check`
reports whitespace/conflict-marker issues rather than producing the ordinary
patch text of `git diff`. No command was executed to explore this discussion.

### Confirmed: explicit node-to-node data flow

- Information travels along the graph's configured connections, from node to
  node. A node must not retrieve an earlier node's payload merely because that
  earlier node exists in the same run.
- Each passage can transform or preserve data, but forwarding is always explicit
  and configured by the author. Keeping values unchanged does not mean implicitly
  forwarding all incoming fields.
- Apply the same explicit local output-mapping principle to other applicable
  node types if equivalent data-flow gaps arise. This does not approve additional
  node types, a global variable store or arbitrary earlier-node references.
- Agent-bearing nodes (ordinary agent nodes and Join) communicate their outcome
  by selecting a Choice and supplying its input; the engine constructs that
  Choice's configured output for its destination, or for terminal delivery.
- Terminal has an explicit output for its structural continuation. Fork has an
  explicit output for each structural branch; it opens those branches rather
  than selecting a Choice. In all cases the work data passed onward is JSON.
- Choices therefore govern agent outcomes/routing; output payloads govern the
  data passed onward. They are complementary concepts, not competing wire formats.
- Join's incoming multi-branch data and synchronization contract is now refined
  in section 9; its outgoing result remains a Choice with an explicit output.

### Confirmed: system-variable exception for the original run task

- Keep `run.input.task` available throughout the graph as a system variable
  containing the original task supplied for that run. It need not be forwarded
  through each intermediate payload to remain available as a reference.
- This is an explicit exception for engine-provided run context, not permission
  for nodes to access arbitrary prior-node outputs or publish global work values.
- At present, `run.input.task` is the only approved graph-wide system variable.
  Additional system variables may be refined later; none are implied now.
- Referencing the task in an outgoing payload remains an explicit author choice.
  Availability as a system variable does not automatically add it to every payload.

### Confirmed: unified command result and author-owned interpretation

- Expose command-produced output through the local `command.result` reference.
  It includes output from stdout and stderr, rather than presenting only stdout
  as the result or requiring separate error-output references in the payload.
- The result may contain normal output, diagnostics, warnings or descriptions of
  command errors. Its content is not classified by the engine as semantic success
  or failure of the requested activity.
- A command finishing with a nonzero exit code does not, by itself, fail the run
  or prevent following the Terminal node's structural continuation. Likewise,
  output on stderr does not itself fail the run.
- The author explicitly chooses whether to include `command.result` in Terminal's
  outgoing JSON and what task/instructions accompany it. The next agent can then
  interpret the result according to those instructions, including handling a
  preceding command that did not accomplish its intended work.
- This is an explicit author-controlled data flow, not automatic result forwarding
  or model execution inside Terminal. A following Terminal still receives the
  same generic JSON output contract.
- Exit status is observable process metadata, but it is not a universal semantic
  classifier. No output-contract feature for automatically identifying errors
  from exit codes or separate stdout/stderr variables is adopted in this version.

Confirmed: if the engine cannot start the shell/process at all, fail the run as
a technical failure and notify the orchestrator. Do not synthesize a normal
`command.result` and continue to the next node. This differs from a shell that
did start and reported, for example, an unknown command through its own output:
that output belongs to `command.result`, and a nonzero exit code alone does not
fail the run.
The unified result's detailed text-collection rules remain implementation/refinement
work; do not promise complete or universally ordered output without defining them.

### Confirmed: inherited working directory and PowerShell script

- The Terminal starts in the working directory for its current path in the run:
  the project directory on the main path, or that branch's worktree inside a Fork.
  It must not automatically return to the main project directory from a branch.
- In this version, pass the configured Command text to PowerShell as a single
  script. The user may separate commands with semicolons, including a sequence
  such as `cd ./subdirectory; command1; command2`.
- Execute that text together in the same shell context, so a directory change
  affects the later commands within that Terminal activity. Do not split the
  text at semicolons and launch each fragment in a fresh working directory.
- The engine does not parse or rewrite the script to discover directory changes,
  split commands into separate graph activities, or translate between shell
  languages. Such command/directory handling is future work.
- A script-local `cd` does not itself change the engine process's directory or
  redefine the graph's inherited working-directory context for subsequent nodes.
- Collect the text produced by the script through the agreed `command.result`
  contract; per-command nonzero exit codes do not acquire new automatic run-failure
  behavior. Execution within the script follows PowerShell semantics and any
  control flow explicitly written by its author.

Factual clarification: semicolons also separate commands in common Linux/macOS
shells such as Bash and Zsh. Shell languages, command availability and path syntax
can differ; semicolons themselves are not the incompatibility. PowerShell remains
the explicitly chosen shell for this version, without an implicit cross-platform
translation feature.

### Confirmed: noninteractive Terminal execution

- Terminal commands execute noninteractively in this version. The graph author
  must configure commands so they do not require interactive answers.
- Do not open an interactive terminal or forward command confirmations, password
  prompts or other interactive questions to the chat.
- This does not alter question/permission mechanisms of agent-bearing harness
  nodes; it is the contract for the standalone Terminal command node.
- Noninteractive mode does not guarantee that every arbitrary command will exit
  promptly. Handling of commands that hang and any execution timeout remain
  lifecycle-policy topics; no timeout value or automatic answer is implied here.

### Confirmed: retain native variables and KLM templates as distinct syntaxes

- The user agrees that Terminal can access its received input as a local payload
  value. This is the current activation's input, not global data or earlier-node
  access.
- After considering unification in either direction, the user chose to keep both
  native PowerShell variables (`$...`, evaluated by the shell) and KLM template
  references (`{{...}}`, resolved by the engine). Do not migrate all templates to
  dollar notation or replace native script-variable syntax with double braces.
- In the script, the local input object can be accessed as `$payload`, e.g.
  `Set-Location -LiteralPath $payload.directory; npm install`. Native PowerShell
  variable and string-interpolation rules apply to the script.
- In the Terminal output mapping, `{{payload.<field>}}` references its received
  input, while `{{command.result}}` references the collected command result.
  `{{run.input.task}}` remains the approved run-wide system reference.
- Existing `{{choice.<field>}}` continues to mean the model-submitted input to a
  Choice, not Terminal's received input. Dollar-prefixed text in an output template
  does not become a KLM reference merely because `$` is used in PowerShell.
- The payload is data supplied to the script, not arbitrary text pasted into
  executable PowerShell. Values containing spaces, quotes or semicolons must
  remain values rather than changing the script's structure.
- Additional double-brace preprocessing inside Command was discussed as an
  alternative; it is not required by retaining these two mechanisms. This decision
  does not approve a universal dollar-template parser or naive script substitution.

## 9. Fork and Join — Join refinement closed for the current scope

This discussion resumed after the graph-wide workspace/Git-branch refinement in
section 10. Join input organization is now approved below; integration destinations
are defined in section 10. The user answered the consolidated questions below:
J1/J2/J4–J8 are approved and J3 is intentionally not treated in this phase.
Ordinary agent responsibilities such as resolving Git conflicts or selecting
blocked when unable to complete the task are already settled.

This section covers parallel branching, worktree context, synchronization and
integration. Each Fork branch has an explicit output; creation of separate
worktrees is now optional as refined in section 10. Join delegates integration
to an agent and reports its result through a Choice.

### Confirmed: parallel work must reconverge, without a Fork configuration field

- The user reconsidered Forks without Join and explicitly requires all normal
  parallel work opened by a Fork to return to a single sequential flow through
  Join. A normal branch must not simply end independently before reconvergence.
- Do not require the author to select or declare a corresponding Join in Fork's
  configuration. Connections express the paths and where they reconverge.
- Even branches used only for independent commands must lead into a Join. If a
  command's output is not useful, the author can omit it from the configured
  outgoing payload. Arrival/completion still matters to synchronization even
  when no useful command data is passed onward.
- Reject the former model of detached branches with a pending terminal result
  waiting for unrelated work. This is a change to the allowed graph structure,
  not an additional first/last terminal-result selection policy.
- Workspace isolation follows the configurable rule in section 10. Returning to
  one sequential graph path does not itself decide Git merge strategy or the
  worktree used after integration.

### Confirmed direction: Join waits on its own incoming connections

- Join waits until all of its incoming connections have delivered their inputs,
  then starts its integration agent. That waiting is defined by Join's inputs,
  not a required association to one originating Fork.
- Incoming work is not required to declare a common originating Fork. The author
  draws the reconverging connections, and Join waits on those incoming deliveries.
- Receiving all inputs completes the waiting phase, not the integration agent's
  task by itself. The previously agreed Join-agent work and Choice-result contract
  remains in force.
- This supersedes the earlier formulation requiring an explicit association to
  one Fork. Sequential rounds and distinct incoming connections follow J1/J2
  below; arrivals must not be mixed between rounds.
- Mutually exclusive Choice paths that cannot all deliver to Join are invalid,
  as confirmed below. Keep the all-incoming-connections rule; do not introduce
  alternative-input grouping to accommodate that graph shape. Shared-Choice
  convergence and overlapping rounds are also invalid as confirmed in J1/J2.

### Confirmed: separate incoming payloads and engine-owned Git context

- Deliver a collection of inputs to the Join agent. Each entry identifies its
  incoming connection and preserves that connection's payload separately.
- Do not automatically merge payload fields across connections. Same-named fields
  such as `result` remain distinct values belonging to their respective entries.
- An empty payload still counts as that connection's arrival for synchronization.
- The engine separately supplies execution context identifying the participating
  worktrees, Git branches and commits. That context is not a payload field the
  graph author must fabricate or forward manually.
- Exact serialization/key names remain technical planning details. This agreement
  defines data separation and provenance, not a new graph-global variable store.

### Confirmed: authoring validation is a later stage

- The user wants future graph validation in the builder to identify branches
  that do not reconverge, present invalid states and explain the required Join
  connections. Design those validations/hints later rather than introducing
  a mandatory Join picker on Fork now.
- The graph-shape rule is agreed now; the validation UX and its implementation
  are deferred. Current CRUD acceptance is not proof that a graph obeys this
  execution contract. YAML remains the executable definition independent of layout.
- The user explicitly classifies mutually exclusive Choice inputs to the same
  Join as invalid. Example: one agent chooses either `review_done` or
  `review_skipped`, while both Choices have separate connections into that Join.
  That activation cannot supply every required incoming connection.
- Add this check in the future graph validator/builder validation stage. Do not
  add OR-input groups, infer that an unselected Choice has arrived, or relax Join
  readiness to execute such a graph. No validator implementation is requested now.

### Superseded parallel-completion alternative

Fork without Join, branch-local normal terminal Choices, and retaining one terminal
output while detached command branches finish were considered and then rejected
in favor of mandatory normal reconvergence. Do not implement output aggregation,
first-wins/last-wins behavior or detached-branch completion rules from that history.
The final output still comes from the terminal Choice on the reconverged flow,
not automatically from command output.

### Confirmed synchronization and exceptional termination

- Sequential Join rounds require fresh inputs; they do not mix deliveries from
  different visits. See J1/J2 for invalid convergence patterns.
- Technical failure or the engine's built-in blocked stops further dispatch and
  causes the engine to end the remaining node work and settle its tool calls.
  Publish the final outcome and release the conversation's run slot only after
  that lifecycle is confirmed, within section 14's boundary. Unconfirmed call
  completion remains uncertainty, not a normal outcome or proof of remote cancellation.
- Preserve worktrees and file modifications. Process termination is not the
  deferred worktree-deletion feature and does not give the orchestrator a stop tool.
- Preserve the distinction between technical failure and built-in blocked. A
  Terminal command's nonzero exit code or stderr alone still does not fail a run.
- Mandatory normal reconvergence does not require failed/blocked work to arrive
  at Join. Exceptional termination follows J7 instead.

### Confirmed: conflict resolution is ordinary Join-agent work

The user confirmed that resolving integration conflicts is the purpose of the
Join agent and already follows the agent-task contract. A Git conflict does not
by itself make the engine fail the run; an agent unable to complete its work has
the engine-provided blocked Choice. Do not reopen this as a separate decision.

### Consolidated Join decisions — user answers recorded

The user requested one complete batch rather than repeated questions about implied
behavior, then answered: J1 invalid; J2 agree; J3 intentionally not treated;
J4/J5/J6/J7/J8 agree. The following rules consolidate those answers. The user also
requested a handoff in AI Memory after this consolidation. No runtime code is
authorized by closing this conceptual block.

**J1 — Shared Choice collapses distinct branch arrivals onto one connection.**
Two parallel agent paths can use the same graph-wide Choice, whose single outgoing
connection targets Join. That is different from the already-invalid mutually
exclusive-input example: there can be two legitimate deliveries through one
connection. Confirmed: classify this convergence as invalid in the future graph
validator; each independently awaited path must have its own incoming connection.
This does not prohibit shared Choices elsewhere. Do not add multiple-arrival
counting through one connection to support this invalid convergence pattern.

**J2 — Overlapping rounds of the same Join.**
Revisiting a Join after a completed iteration is supported. Confirmed for this
version: a new round must not overlap one still collecting inputs or executing
its agent. Rounds of a given Join within a run are sequential; each round requires
fresh deliveries from every input, and
graphs generating overlapping rounds at that Join are invalid rather than queued
or executed concurrently. Ordinary cycles after completion remain allowed. This
concerns real activations, not duplicate transport notifications.

**J3 — Nested Forks: intentionally not treated in this phase.**
The user did not approve the proposed nested-convergence policy. Intentionally
defer this case rather than refining or implementing recursive Fork/Join support
now. Do not infer support, a mandatory inner-Join scheme or detailed crossed-path
validation from the previous proposal or from what the canvas can draw.

**J4 — Conflicting incoming session policies.**
Current Choice authoring allows `new`/`continue_target` for a Join destination.
Several arriving Choices can request incompatible policies for its single agent.
Confirmed: all applicable incoming Choice policies must agree (omitted means new);
otherwise the graph is invalid. Terminal connections carry no session-policy
vote; when all inputs are Terminals, default to new. Do not let arrival order
silently determine the selected session.

**J5 — continue_target with a different integration workspace.**
An explicit output branch creates a new worktree on each Join activation, while
continue_target asks to preserve the previous node session. Confirmed: for Join,
continue_target reuses a session only when the working directory is also the same;
otherwise start new. This extends the existing fallback rule for Join. It does
not claim that native harness sessions can universally change their directory.

**J6 — Base commit when Join creates a new output branch.**
If the original workspace A advances after Fork opens its branches, confirmed:
Join's new destination D starts from A's current local HEAD when D is
created, fixed once for that creation. This chooses D's starting state, not the
agent's conflict-resolution strategy or an automatic commit of uncommitted files.

**J7 — Termination of remaining work after failure or built-in blocked.**
Confirmed: stop further dispatch, have the engine terminate remaining work from
that run's nodes and settle their tool calls, and publish the final outcome/
release the conversation's active-run slot only after that work has ended. Preserve
worktrees and file changes. Apply this to both technical failure and built-in
blocked while preserving their different outcome types. This is process lifecycle
handling, not the deferred worktree-deletion feature or an orchestrator stop power.
Section 14 limits this obligation to nodes and tool calls, excluding shared MCP
server shutdown and detached tasks beyond the tool response. Lost callbacks or
unconfirmed calls do not count as settled merely because a local process exited.

**J8 — Join is waiting but no work can deliver the missing input.**
When only blocked-on-input activities remain and nothing is running or runnable
that could deliver the missing inputs, confirmed: fail the run with an unsatisfied-
dependency diagnostic. Do not wake Join's agent with partial data or wait forever.
This differs from an upstream activity that is still running/waiting for a user
interaction; general timeouts/limits remain a later lifecycle topic. Future builder
validation remains responsible for author-facing invalid graph guidance.

These answers close Join's current conceptual scope. Serialization, prompt wording
and concrete execution mechanisms belong to the later plan/implementation, not
another round of user confirmation about the agent's ordinary integration work.
The ordinary-agent same-directory rule is now approved as S1 in section 11.
That section also records settled scheduling/restart policies and explicitly
defers activation limits/timeouts; none reopen Join's approved J5 rule.

## 10. Run workspaces, worktrees and Git branches — conceptual discussion

The user moved this topic ahead of Join details because it affects the entire
graph. Discuss the execution workspace as a whole before returning to integration.

### Requirements and use cases raised by the user

- A graph must be able to work directly in the current project directory and its
  actual file state, including uncommitted changes. The user's example formats,
  lints, organizes commits, pushes and opens a PR for work already present there.
  These are example graph activities, not authorization to execute them now.
- That current directory may itself already be a Git worktree. Running without
  creating another worktree must not be confused with refusing existing worktrees.
- A graph must also be able to use a newly created isolated worktree so the chat
  orchestrator/user can continue other work in the original directory.
- The user chose invocation-time workspace selection through the orchestrator,
  rather than graph-level configuration. Generated-name and traceability rules
  are confirmed later in this section.
- A run may already be in worktree A when it reaches Fork. Its new branch
  worktrees need to start from the implementation produced along that path, rather
  than an unrelated or stale checkout. Uncommitted preceding work is therefore
  an explicit issue to resolve.

### Git facts informing the decision

- Linked worktrees are separate working directories of the same repository, with
  their own checked-out branch/HEAD and index. They share the repository's Git
  objects; they are not hierarchically nested copies of working directories.
- A new worktree can start a new branch from a commit reached in another worktree.
  This provides the intended logical derivation of B/C from A without requiring
  a separate repository or a literal child worktree relationship.
- The commit must be available locally. Push and pull are not prerequisites for
  creating those local branches/worktrees.
- Normal worktree creation checks out the selected commit. Uncommitted working
  tree/index changes and untracked files from A are not automatically copied.
  Copying such state would be a separate policy, not an inherent worktree feature.
- Worktrees isolate working directories, not external databases/services or
  shared repository references. Their use does not by itself settle integration.

### Confirmed: workspace choice belongs to invocation

- Treat workspace selection as a property of the concrete invocation: use the
  existing directory, or create a new worktree from a specified local revision.
  The reusable graph need not store that selection in this initial approach.
- Let the orchestrator resolve this choice in the conversation. An explicit user
  request can supply it; otherwise use the original project directory without
  asking, under the product default approved in section 13 (2026-09-13). No
  reference to a separate workspace-choice message is required. The earlier rule
  to ask before starting when the mode was omitted is superseded.

### Confirmed: preparing committed work is the author's responsibility

- The graph author decides how the work that must reach newly created worktrees
  becomes committed. The preceding agent can be tasked with committing, or the
  graph can include an explicit command/script that does so.
- The engine does not automatically commit all pending changes or copy dirty
  working-directory contents as an implicit part of creating a worktree.
- A local commit is sufficient; no automatic push/pull is required for the Fork's
  new worktrees to share that base. Remote operations remain explicit graph work.
- Within a run, the Fork still fixes one incoming base commit for all of that
  activation's branches. It must not silently substitute the run's original
  starting commit for the subsequent implementation the author has prepared.

### Confirmed: explicit starting commit or latest by default

- The user may name a starting commit in the request to execute a graph in a new
  worktree. The orchestrator uses that expressly requested commit.
- If the user omits a commit, assume the most recent commit rather than asking
  for one on every invocation. Use a different base when explicitly requested
  by the user, not through an unsolicited orchestrator choice.
- Confirmed: "most recent" means the current local HEAD of the source project
  directory/worktree at creation, not the newest commit across all branches by
  timestamp or an automatic remote update. For example, when the source is on
  `feature/login`, use its local HEAD even if `main` has a newer-dated commit.
  This does not copy pending uncommitted modifications.

### Naming follow-up

Readable bases, run/activation suffixes and explicit execution associations are
confirmed later in this section. Exact generated-name formatting, default readable
base when omitted and physical directory placement belong to the later plan;
do not reopen the already-approved uniqueness and traceability rules.

This subtopic is closed: invocation-time workspace selection, author-owned commit
preparation, and explicit-commit-or-local-HEAD starting revision are confirmed.
Further workspace and Join refinements are consolidated below and in section 9.

Further answers on isolation, reuse/fresh starts and the post-Join context are
recorded below. Engine cleanup is explicitly deferred. Section 11 resolves the
cross-cutting questions; its latest decisions supersede the older open checklist.

### Confirmed: Fork parallelism does not require separate worktrees

- The user explicitly replaces the earlier mandatory separate-worktree rule.
  Fork branches may run in the same inherited directory/worktree and Git branch
  instead of always creating new worktrees and branches.
- The example is multiple independent commands intentionally running in parallel
  on the same working copy. Separate worktrees remain useful for work that needs
  isolation, but they are not inherent to parallel execution.
- The author must have an explicit control, such as a toggle, for separate
  worktree versus no new worktree. UI and file-schema changes are for the later
  implementation; do not edit the current prototype/schema in this refinement.
- This is an author-chosen isolation policy, not automatic classification by node
  type. Commands may modify files too; selecting an agent or Terminal does not
  alone determine whether concurrent work should share its directory.
- No new worktree means using the execution path's existing directory and branch,
  which can already be a run worktree. It does not mean returning automatically
  to the main project checkout or creating different checked-out Git branches in
  one shared directory.
- Mandatory normal reconvergence through Join still applies regardless of whether
  the branches share a workspace. The effect on Join's Git integration when inputs
  come from shared and/or isolated workspaces is part of the paused Join refinement.

Confirmed: configure isolation per Fork branch rather than only once for the
entire Fork. A mixed Fork can have several branches sharing the incoming workspace
and another branch using its own worktree. The choice is independent of whether
the branch starts with an agent or a Terminal command.

Confirmed default: newly added Fork branches start with separate-worktree creation
enabled. The author explicitly disables isolation when the branch should share
the inherited directory and Git branch. UI changes remain deferred to implementation.

Existing common-base-commit rules apply to newly created isolated worktrees;
sharing the existing directory is not a checkout from that commit.

### Confirmed: generated names and run traceability

- The user approved treating a supplied branch/worktree name as a readable base,
  completed automatically with a unique execution/activation identifier when a
  new workspace is created. Do not silently overwrite or reuse an existing one.
- The user additionally wants the creating run's ID in the generated branch-name
  suffix so the branch/worktree can be traced to that run.
- The user explicitly highlighted that the same node can activate multiple times
  within the same run. A run ID alone does not distinguish those creations.

Confirmed: include both run ID and node-activation ID in new Fork branch/worktree
names. An activation ID identifies one occurrence of
executing a node and must distinguish occurrences across the run; it is not the
stable node ID from the graph or merely a per-node visit counter without node
identity. The branch's configured name/identity distinguishes the separate outputs
created by that activation.

Examples with illustrative identifiers: `checks/review-r42-a1` and
`checks/review-r42-a2` for two activations in run r42. Exact formatting and any
shortening/collision handling remain implementation-planning details.

Confirmed: retain the run/node-activation/Fork-branch association explicitly
in execution records rather than depending solely on parsing a Git branch name.
Inherited/shared existing workspaces need an execution association without adding
suffixes to or renaming the user's existing Git branch.

### Confirmed: explicit user choice between reuse and a fresh attempt

- The user rejected automatic reuse as the default for corrective attempts. The
  orchestrator must clearly resolve whether to build on the existing work or start
  from scratch before invoking the next attempt involving earlier artifacts. Ask
  when that user decision is missing; do not reconfirm a decision already supplied.
  Section 13's original-directory default does not decide fresh/reuse.
- The question explicitly covers the existing artifacts, Git branches and
  worktrees, not just whether to request another run. The user chooses between
  reusing that work and starting fresh without reusing it.
- Reuse preserves the chosen existing work. The new attempt is still a new run,
  not a resumption from the previous node. Existing branch names identify their
  creating run, and execution records also associate later runs that reuse them.
- In this version, starting fresh leaves the previous worktrees, branches and
  modifications intact at invocation. Do not delete or reset prior work as an
  automatic part of choosing a new attempt. "Start from scratch" no longer implies
  cleanup. When running in the original directory, W2 below explicitly accepts
  its existing file state; agents may change that state as part of the new task.
- At the end of its activity, an agent may request that the orchestrator arrange
  cleanup as follow-up work. Communicate such a request through the existing
  Choice/result contract, not a new free-form node communication mechanism.
- Cleanup performed as a separate orchestrator activity is not automatic engine
  cleanup or an implied destructive side effect of invoking the next run.

### Future feature: engine-owned cleanup operation — explicitly deferred

- The user wants a dedicated engine cleanup operation that an agent can invoke,
  but explicitly deferred its refinement and implementation. Do not build a
  minimal version or make it a dependency of the initial graph runtime.
- The earlier proposal to delete prior worktrees automatically for a clean start
  is superseded by preservation of those worktrees and their work.
- Revisit cleanup ownership, deletion boundaries, resources shared between runs,
  resources still in use and reporting/recovery behavior in that future feature.
  These questions do not block the current reuse-versus-fresh-attempt contract.
- Original graph-execution authorization or a fresh-attempt choice must not be
  treated as authorization to delete arbitrary paths or reset the user's original
  checkout/preexisting branches.

Confirmed workspace-reuse mechanics, separate from the deferred cleanup feature:

- Confirmed fresh-attempt base when creating a new worktree: retain a commit explicitly requested by the user
  for the activity. Otherwise use the current local HEAD of the original source
  directory, not the HEAD of the previous attempt's worktree. If the source
  advances from A to B while the earlier run executes, a fresh attempt defaults
  to B. Starting fresh means not reusing the previous attempt's work, not freezing
  the earlier base; preserve the old worktree and its modifications.
- Reuse covers prior Fork/Join workspaces as well as the run's top-level workspace.
  W1 in section 11 establishes explicit orchestrator-supplied associations rather
  than engine selection by name similarity or recency.
- A fresh attempt in the original directory follows W2 in section 11: run against
  the existing files, without resetting the checkout or requiring a new worktree.
  The new-worktree base rule above does not promise a clean filesystem there.

### Confirmed: Join's default integration destination

- When Join has no explicit output Git branch configured, integrate into the
  working directory and branch that originated that parallel work.
- If the run was working in worktree A before Fork opened B/C, the default Join
  result returns to A. Subsequent nodes continue in A, not an arbitrarily selected
  branch worktree or automatically the project's original checkout.
- Work performed by branches sharing A is already present there. The Join agent
  integrates work from separately created branch worktrees into that origin as
  needed. This destination rule does not itself choose Git conflict-resolution
  commands or change the requirement that the agent reports through a Choice.
- The destination is the origin of the relevant parallel work. Earlier discussion
  mentioned nested contexts, but J3 now explicitly defers that case. Do not use
  this destination rule as implied approval for nested-Fork support.
- When an explicit output branch is configured and a new branch is being created,
  Join uses a separate worktree on that branch for integration. The integrated
  result remains there, and subsequent nodes continue in that worktree/branch.
- Do not automatically switch the originating directory's checked-out branch.
  Example: origin A -> Fork branches -> Join integrates in D -> continuation in D.
- The configured output-branch name is a readable base for a new branch, not a
  reference to a preexisting fixed Git branch. Append the run ID and Join
  activation ID, following the same generated-name rule as Fork worktrees.
  Example: `feature/integrated-r42-a8`.
- A later Join activation creates a distinct destination. Do not silently reuse
  or replace a branch because its name resembles the configured base. Reuse of
  prior work remains subject to the explicit user decision for a new attempt.

## 11. Cross-cutting decisions and conceptual closure

### Confirmed S1: session continuity for ordinary agent nodes

J5's same-directory condition was approved for Join. An ordinary agent node can
also change workspace on a later activation: in the reference graph, implementation
starts in A, Join creates an output worktree D, and `request_changes` points back
to implementation with `continue_target`, now in D.

The user approved applying the same-directory condition to ordinary agent nodes:

- With `continue_target`, reuse the most recent session for that node within the
  run only when the working directory is unchanged; otherwise start a new session
  in the inherited directory. If no prior session exists, start new.
- The new session receives the reusable agent's instructions, the current
  activation's payload and the available Choice contracts. Do not automatically
  transfer the previous session's conversation history.
- In the A-to-D example above, the implementer starts a new session in D. The
  graph flow continues, but native conversation continuity is not presumed across
  working directories. Join's approved J5 rule remains in effect.

### Explicitly deferred L1/L2: activation limits and execution timeouts

The user chose to leave both topics open for future work. Neither the proposed
per-node activation-limit contract (L1) nor the proposed optional execution-timeout
contract (L2) was approved. Do not infer their configuration, counting rules,
defaults, human-wait clock suspension or exhaustion behavior from those proposals.
These topics no longer block the current conceptual refinement or its subsequent
implementation plan. The approved Choice-protocol violation counter, J7 exceptional
termination and J8 unsatisfied-dependency handling remain in effect.

### Settled R1/R2: corrective attempts and scheduled-work ordering

The user explicitly corrected the follow-up: R1/R2 repeated existing decisions,
and no new decision had made them inconsistent or unviable. Keep the behavior
described in section 5: concrete corrective action before another attempt;
escalate when no such progress is available; ready corrective work takes priority;
independent authorized work may proceed while X awaits the user; ready independent
activities follow request order unless the user changes priority. Preserve explicit
success dependencies and the reuse-versus-fresh user decision.

These are settled rules, not a new approval gate or an invitation to choose a
numeric retry limit. Do not ask for reconfirmation unless a concrete new conflict
with another approved decision is identified and explained.

### Confirmed C1: per-run graph and agent definition snapshots

- At the start of each run, capture the graph definition and the reusable agent
  definitions used by it. Execute that run against its captured definitions.
- The user can edit graphs and agents without changing an ongoing run's behavior,
  including definitions for nodes that have not activated yet.
- Subsequent runs, including corrective attempts, capture the definitions current
  at their own start. A scheduled activity is not yet a run; it uses the definitions
  current when it actually starts, not when the request was queued.
- This snapshot concerns graph/agent definitions, not a filesystem snapshot of
  the work being performed. Workspace policies remain separate.
- Development-time internal engine prompts retain section 7's approved reread-on-
  send behavior; that separate rule is not replaced by graph/agent snapshots.

### Confirmed E1: engine shutdown/restart

- Runs that were in progress are ended as interrupted by the engine shutdown,
  with that cause recorded. Restart does not automatically resume those runs.
- After restart, deliver the interruption information and any still-pending run
  result notifications to the orchestrator. Existing single-turn notification
  delivery rules continue to apply.
- Preserve scheduled activities. Supply the updated execution state to the
  orchestrator so it can follow existing authorizations and dependencies.
- An interrupted run does not satisfy a success prerequisite. Continuing the
  authorized activity follows the existing new-attempt rules, not resumption of
  the interrupted run. Detailed persistence and process handling belong to the
  technical plan.

### Confirmed W1: explicit workspace-reuse associations

- When the user chooses reuse, the orchestrator explicitly identifies the prior
  workspaces to use and their associations with the new execution, using the
  previous attempt's execution records.
- This includes Fork and Join workspaces, even when earlier runs contain several
  activations of the same node (for example, B1/C1 and B2/C2, or D1 and D2).
- If the user's intent does not distinguish the candidates, resolve that actual
  ambiguity in the conversation. Do not require another confirmation when the
  applicable association is already clear.
- The engine uses the supplied associations. It does not silently choose by
  similar branch names or simply take the most recently created directory.
- Reuse remains a new run with new activations, not resumption. Retain the
  existing creation provenance and later reuse associations as already agreed.
  Concrete mapping representation belongs to the technical plan.

### Confirmed W2: fresh attempt in the original directory accepts existing changes

The user rejected automatically creating a new worktree to provide a clean start
after a run modified the original project directory. This edge case is explicitly
accepted for the current phase:

- Start a new run in that original directory using the files as they actually
  exist, including modifications left by the earlier attempt. Treat the request
  as starting fresh without promising a pristine filesystem.
- Do not reset, roll back, reconstruct a clean state or create a worktree merely
  to make that fresh attempt clean. A specialized true-clean-start mechanism is
  outside the current scope.
- The new agents encounter the existing changes and handle them in the course of
  their assigned work. They may correct problems, adapt changes or remove changes
  that do not fit their implementation, under their ordinary task/scope rules.
- The user explicitly accepts the risk of prior changes affecting the new attempt
  to avoid adding a deeper cleanup/isolation mechanism now. Preservation at
  invocation does not make those files immutable during subsequent agent work.
- This exception does not change fresh-worktree creation rules when that workspace
  mode is actually selected, nor authorize engine-owned cleanup of old worktrees.

### Current conceptual scope closed

E1/W1 approval and the W2 exception resolve the remaining listed conceptual
questions. S1 and C1 are approved; R1/R2 retain the established behavior. L1/L2
and the other explicitly deferred features below remain outside this phase.
These execution contracts are the basis for the technical implementation plan.
The user subsequently refined and closed a minimal monitoring scope in section 12.
Do not reconfirm settled execution behavior unless a concrete new inconsistency is
found and explained. Closing refinement does not authorize runtime implementation.

### Technical plan after conceptual closure

The rules are resolved and the current integrations have been inspected for
`GRAPH_ENGINE_IMPLEMENTATION_PLAN.md`. The planned technical work includes:
run/activation persistence and scheduler, harness invocation/Choice feedback,
notification delivery, workspace tracking/reuse, prompt-file loading, and minimal
runtime enforcement of the agreed graph contract. Concrete IDs, serialization,
template binding, output bounds and directory placement belong here.

The authoring implementation also needs subsequent alignment with runtime rules:
Terminal output mapping, optional per-branch isolation, real run controls/indicators,
and the built-in-versus-author Choice identity. Do not treat these as implemented
merely because the CRUD can save its earlier schema.

### Known reference-graph mismatch — not a new conceptual question

The visual reference at
`C:/Users/Samuel/AppData/Local/Temp/opencode/klm-authoring-validation/.klm/graphs/graph-execution-reference.yaml`
predates the latest rules. Its `review` branch can select an author-defined
terminal `blocked` before reconvergence. Under current rules that is an ordinary
terminal graph Choice, not the engine's guaranteed blocked escape, so this path
does not satisfy normal reconvergence. The file also predates Terminal output
mapping and optional isolation fields.

Preserve this illustrative/user-accessible file during handoff. Align the example
when appropriate before using it as a runtime acceptance graph; do not infer an
exception to the rules from its current drawing or execute its commands now.

### Explicitly deferred

- Per-node activation limits and execution timeouts (L1/L2); both remain open for
  future refinement, with no policy approved from the proposed batch.
- Nested Fork handling (J3): intentionally not treated in this phase.
- Engine-owned worktree cleanup/deletion tooling.
- Full builder graph validation UX, guidance and invalid-state presentation.
- Interruption/resumption as a user feature, still the first subsequent priority
  under the earlier scope agreement.
- Additional invocation methods, concurrent runs within one conversation, richer
  graph inputs/files, extra system variables, and `.klm/harness.toml`.

No additional unresolved Join-specific rule was identified after J1–J8 were
consolidated. If a genuine consequence is discovered later, state it explicitly
and distinguish it from already delegated implementation details.

## 12. Run monitoring — minimal initial scope closed

### Latest user decision supersedes the larger monitoring proposal

After describing a richer monitor, the user explicitly reduced the initial
implementation requirement to knowing which nodes are active and which completed.
The earlier named-run-tab/history/detail-panel proposal and its A1–A5 follow-up
questions are not requirements or blockers for this delivery. Do not implement
those additions merely because they were discussed earlier.

### Confirmed initial behavior

- Keep the existing Graph tab associated with the graph selected in the main
  chat composer. Selecting None hides that tab as before. Do not introduce a
  separate named run tab or suppress Graph in favor of one.
- When the selected graph has the conversation's active run, display that run's
  progress in the existing Graph view and blink the running LED in the composer
  graph selector. Bind the display to the actual run, not the fictional timer.
- Opening Graph shows which nodes are currently active and which actually ran
  and completed. Parallel execution can have multiple active nodes; do not reduce
  this to one global active-node identifier.
- Reuse the established node-running visual treatment and completed indicators.
  A new activation must be represented as active even if that node completed an
  earlier activation. Never mark unexecuted nodes completed because the run ended.
- The executing graph uses its run definition snapshot. Previewing a selected graph
  without an active run remains the existing read-only configuration view.
- Selection or opening Graph does not start a run. Existing invocation rules,
  including selecting the graph when its authorized run starts, remain in force.
  Changing or hiding the view does not stop execution.
- This initial monitor exposes no execution logs, Input or Output panels and no
  per-activation drill-down. Its purpose is the small working active/completed
  overview the user can validate before richer monitoring is built.
- Final outcomes continue to reach the main-chat orchestrator under the existing
  notification contract. Reducing monitoring UI does not remove the engine's
  agreed execution records, result payloads or question/permission semantics.

### Deferred monitoring features

The following earlier proposals remain future work, not dependencies of the
initial engine delivery:

- Separate graph-named active-run tabs and their completion/navigation behavior.
- Historical run widget, hamburger menu and historical execution browsing.
- Run/Input/Output inspection, model/process logs and live Terminal output UI.
- Numbered left/right navigation between node activations and live-follow rules.
- Detailed Join arrival/missing-input inspection and per-element detail layouts.
- Expanded waiting/failure/blocked/interruption presentation beyond the minimal
  active/completed view.
- The new questions/permissions component at the bottom of the sessions sidebar.
  Existing interaction semantics remain required; no new centralized request UI
  is needed merely to deliver this monitoring slice.

Earlier unanswered UI questions, including the missing pasted Choice description,
belong to that future refinement. Do not ask the user to resolve them now.

### Next step

The current execution-engine conceptual rules and initial monitoring requirements
are closed. `GRAPH_ENGINE_IMPLEMENTATION_PLAN.md` records the technical plan against
the current Go harness integrations and desktop client. Persistence/API/event representations,
harness protocol enforcement, template resolution and workspace mapping mechanics
are technical planning work, not another blanket reconfirmation of user decisions.
If inspection reveals a genuine contract conflict or unavailable harness capability,
raise that specific issue. This record does not itself authorize runtime code.

## 13. Invocation defaults and tool schemas — approved 2026-09-13

### Diagnosis and precedence

During manual invocation, `graph_invoke` schemas did not describe nested
`authorization` and `workspace` fields completely. Strict engine validation then
left the agent guessing fields such as `path`, `scope` and `eventId`. The user
approved the following correction after the closed refinement and implementation
milestones above. It supersedes the mandatory workspace question in section 10
and older integration requirements for `workspace.authorizationEventId`.

### Current contract

- When `workspace` is omitted, use `original` without asking. The product's default
  is the basis for using that directory; omission is not express user consent to
  a workspace choice and must not be recorded or described as such.
- Explicit `original` and `new_worktree` modes remain supported. `original` means
  the conversation's registered project directory, including its existing changes
  and any existing worktree identity. It does not accept an arbitrary path.
- `workspace.authorizationEventId` is not mandatory. Applying the default does not
  require identifying a message in which the user chose that workspace.
- Activity authorization remains required. `authorization` retains its list of
  `{eventId, text}` references grounding the requested activity. The engine validates
  that each referenced event exists, has type `user`, and belongs to the invoking
  conversation. This is traceability, not semantic proof that the message authorizes
  the activity and not a native permission grant. The orchestrator must still act
  within the user's express activity authorization; graph selection alone is not it.
- Publish complete nested tool schemas, including field types, required fields,
  allowed modes, defaults, descriptions and reuse associations, consistently through
  the private MCP bridge and Pi extension. Strict validation must be supported by
  discoverable contracts rather than requiring guessed fields or event identifiers.
- Orchestrator instructions must apply defined defaults and ask only about missing
  information or ambiguity that actually blocks execution. Do not ask for a workspace
  choice merely because the request omitted it, or for a starting revision when the
  approved local-HEAD default applies.
- Earlier-artifact retries still require the user's fresh/reuse decision, concrete
  correction and, for reuse, explicit workspace associations (W1). If that decision
  is missing, ask. W2 still accepts existing files for fresh in the original directory;
  no reset, cleanup or automatic new worktree follows from this default.

The Go implementer is responsible for the default and complete nested schemas;
prompt alignment and restart are coordinated by the principal agent. This decision
record establishes the contract, not evidence of a build, restart or successful run.
The user will reload the browser tab and retest after that coordinated restart.

## 14. External MCP and node/tool-call lifecycle — approved 2026-09-13

### Diagnosis and precedence

The engine rejected configured external MCP servers in OpenCode/Codex with a
diagnostic such as `cannot confirm lifecycle ... agentdeck`. This was a generic
preventive rule based on configuration, not demonstrated pending tool calls.
The user approved replacing that boundary. This section supersedes blanket
external-MCP bans and earlier interpretations of Choice finality/J7 that required
terminating shared MCP servers or all remote work delegated through them.

### Current contract

- Permit configured external MCPs in OpenCode/Codex without a server-name allowlist
  and without disabling them. `agentdeck` is the reported example, not a special
  exception. Configuration alone does not establish a pending call or uncertain
  node finality.
- The engine guarantees lifecycle of the node and its tool calls. Seal the node
  against new calls, including already-permitted tools, and await completion of
  calls actually started before accepting its Choice. Reservation still is not
  acceptance and cannot dispatch a destination.
- The boundary is the tool-call response, not the lifetime of the MCP server.
  A response such as "task started" completes that call's lifecycle responsibility;
  the detached external task continuing beyond the response is outside this
  guarantee. It is not proof that the detached task finished or the user's
  objective succeeded; the orchestrator's result assessment remains separate.
- Do not require or promise shutdown of shared MCP servers. Node shutdown, process
  containment or closing a client connection does not prove remote cancellation.
- Error/cancellation without evidence of call completion records uncertainty.
  Losing a callback, killing the local process or merely requesting cancellation
  must not fabricate a completed call, accepted Choice or normal run result.
  Existing uncertain-finality handling remains required for actual unresolved
  calls/node termination; permission to use MCPs does not erase that state.
- Preserve grants, approval flows and sandbox settings. Allowing configured MCPs
  is not a blanket grant to invoke their tools or an exemption from the seal.

Section 13's original-directory default, activity authorization, complete nested
schemas and fresh/reuse decisions remain in force. No prompt or code change is
performed by this documentation update. The adapter implementer owns the parallel
implementation. The user requested restart after integration; the principal
coordinates it after the build, then the user reloads the tab and retests. This
record is not evidence of implementation, tests, build or restart for this change.
