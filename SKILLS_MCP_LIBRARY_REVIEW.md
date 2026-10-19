# Skills/MCP library review — round 1

Status: R1–R3 corrected and approved in round 2 for the reviewed Library changes.
Commit/push requested from the implementer; delivery is not yet confirmed.

## Blocking findings

### R1 — P1: HTTP MCP shutdown is unbounded

`engine/mcp_client.go`: `connectMCP`, `mcpLiveConnection.close`, `closeMCP`.

The HTTP client has no timeout and uses `http.DefaultTransport`. SDK v1.4.0
`streamableClientConn.Close` sends DELETE using its detached connection context
and cancels that context only after DELETE returns. Cancelling the turn/startup
context therefore does not bound shutdown. A server that accepts DELETE but
never responds leaves `live.close()` blocked; `harness.go` waits there before
turn finalization and graph settlement. Stop/Send now can remain blocked too.
The engine also discards `ClientSession.Close()` errors.

Confirmed with an owned localhost SDK fixture using the actual engine
`connectMCP`: cancel context, start close, observe DELETE received, and observe
close still blocked until the fixture is explicitly released. Fix with bounded,
owned HTTP cleanup/cancellation while preserving legitimate long-running SSE/tool
calls and reporting relevant lifecycle uncertainty.

### R2 — P1: valid empty agent selections break persisted graph validation

`engine/authoring.go`: `AgentDefinition` JSON `omitempty` on the new slices;
`engine/graph_snapshot.go`: `validateGraphSnapshot` uses `reflect.DeepEqual`
between the raw TOML agent and the persisted agent definition.

An editable valid agent containing `skills = []` or `mcp_connections = []`
produces nonnil empty slices from TOML. JSON omits them and restores nil slices.
After persistence/reload, the raw source and stored definition fail DeepEqual
(`Captured agent differs from its source.`). Graph record validation can then
reject engine state. Omitted fields and explicitly empty selections must have
consistent semantic comparison/normalization, without weakening source checks.

Confirmed with the actual `AgentDefinition` and TOML/JSON codecs: both source
slices nonnil, both restored slices nil, DeepEqual false. Probe did not execute a
graph or load production state.

### R3 — P2: disappeared selected chat tools are silently omitted

`engine/mcp_client.go`: `prepareMCP` replaces `c.Catalog` with discovery for
non-graph turns, then iterates `c.Catalog.Tools` to create routes and diagnostics.
If a saved selected tool is absent from discovery, it is never visited, so no
warning is recorded. The actual-vs-captured missing-tool branch only catches
this for graph snapshots. The saved selection remains but chat/model receives
no unavailable diagnostic. Compare selected tool names against the discovered
inventory and warn for each unavailable selected tool, preserving selection and
not expanding the allowlist. This finding is from code inspection, not an E2E run.

## Verification performed

- Two temporary targeted Go probes, using no real credentials, model calls,
  production skill directory, or graph runs:
  `go test -tags dev -run '^TestLibraryReview' -count=1 -timeout 25s .`
- Both expected regression assertions failed, reproducing R1 and R2.
- Temporary test source removed after the command. No full suite run.
- Source review included backend capture/selection, SDK lifecycle, OAuth,
  elicitation, deletion, bridge and UI integration. No claim of E2E acceptance.

## Round 2 — corrections verified

Reviewed `engine/mcp_client.go`, `engine/mcp_oauth.go` and
`engine/graph_snapshot.go` against the findings and the SDK's close semantics.

- R1: per-connection owned HTTP transport/lifetime, bounded DELETE and close
  cancellation, propagated cleanup errors, cancellable credential-lock waiting,
  and error propagation on the graph missing-catalog branch.
- R2: semantic comparison canonicalizes only absent/empty selection lists,
  retaining source hashes and checks for actual definition changes.
- R3: routing iterates the saved positive selection; absent tools produce
  diagnostics without changing selection or adding unselected discovered tools.

Independently reran the implementer's disposable fixtures after reading their
source: `go test -tags dev -run '^TestLibraryReviewFix' -count=1 -timeout 30s -v .`.
Result: PASS, 7.606s. Five tests covered blocked HTTP DELETE and credential waiting,
missing-catalog cleanup, a valid 2.1s HTTP tool call, full graph snapshot roundtrips
(four omitted/empty-selection cases plus rejection of real definition changes),
and missing-selected-tool warnings/allowlists. The temporary test copy was
removed; `KLM_USER_SKILLS_DIR` pointed to an absolute disposable directory.

No remaining blocker found in these corrections. No full suite or production
provider/harness/graph execution was performed. End-to-end OAuth/mobile,
elicitation and operational acceptance remain human-validation work documented
in the implementation progress file; this approval does not claim that acceptance.

Commit/push is released for the Library implementation and its review/docs.
Preserve concurrent unrelated GitHub activity and other changes; this review does
not approve or include them. The implementer must report the resulting commit and
push outcome separately.
