# Graph interchange, version 1

The graph editor provides **Export SVG**, **Export JSON** and **Copy JSON**.
Exports use the current canvas, including applied edits that have not been saved.
Close/apply the open node configuration panel first. SVG is a standalone vector
diagram of all nodes and connections, including nodes outside the current viewport.
It uses embedded colors and vector text/shapes, with system font fallbacks.

**Import JSON** accepts a file or pasted text. Continue validates the document and
prepares a new local draft. Review the graph name and agent mappings, then open the
editor. **Save** registers the graph, positions and new agents together. Leaving
before Save does not register agents or a graph, and importing never starts a run.

## Document

Files use UTF-8 JSON, normally named `<name>.klm-graph.json`, with a 512 KiB limit.
The version and format fields are mandatory. The top-level fields are:

| Field | Value |
| --- | --- |
| `format` | `"klm.graph"` |
| `version` | `1` |
| `definition` | Graph name, description, enabled state, initial node, nodes and choices |
| `layout` | `positions` map and optional `{x, y, zoom}` viewport |
| `agents` | Object keyed by agent identifier, containing every referenced agent definition |

Node and Choice identifiers remain stable during transfer. Choice layout keys use
`choice:<choice-id>`. Missing node positions receive the editor's normal fallback
layout. Coordinates must be finite and have absolute value below `1e9`; viewport
zoom must be greater than zero and at most 10. Unknown fields, unsupported versions,
invalid node types, broken references and oversized requests are rejected.

Agent definitions contain `name`, `description`, `enabled`, `defaultHarness`,
`model`, optional `effort`, and `prompt`. Identifier is the name-derived slug, as in
the existing agent catalog. Include only agents used by agent/Join nodes. Supported
harness identifiers are `pi`, `opencode`, and `codex`. Prompts are included verbatim.
Execution history, project identity, local graph ID and revision are not included.

Graph definitions use the existing authoring fields:

- Common: `name`, `description`, `enabled`, `initial_node`, `nodes`, `choices`.
- Agent: `type: "agent"`, `name`, `agent`, `choices`, optional `overrides`
  (`harness`, `model`, `effort`). An explicit empty override effort remains empty.
- Terminal: `type: "terminal"`, `name`, `command`, `output`, optional `to`.
- Fork: `type: "fork"`, `name`, `branches`. Branches contain `name`, `git_branch`,
  optional `separate_worktree`, `output`, and `to`.
- Join: `type: "join"`, `agent`, `prompt`, `output_branch`, `choices`.
- Choice: `name`, optional `description`, `input`, `output`, optional `to`,
  `session` (`new` or `continue_target`), and `terminal`.

The engine's existing authoring validation applies. A JSON draft must satisfy it
to import; for example, it needs an initial agent/Terminal node and all referenced
agents must be enabled. Branch isolation defaults to true when omitted. See
`GRAPH_AUTHORING_REFINEMENT.md` for the execution meaning of these fields.

## Example

Replace `model` with a real model ID available in the destination harness, or map
Worker to an existing agent during import.

```json
{
  "format": "klm.graph",
  "version": 1,
  "definition": {
    "name": "Implementation",
    "description": "Implement a task and finish",
    "enabled": true,
    "initial_node": "work",
    "nodes": {
      "work": {
        "type": "agent",
        "name": "Implement",
        "agent": "worker",
        "choices": ["done"]
      }
    },
    "choices": {
      "done": { "name": "done", "input": {}, "output": {}, "terminal": true }
    }
  },
  "layout": {
    "positions": { "work": { "x": 0, "y": 0 }, "choice:done": { "x": 400, "y": 0 } }
  },
  "agents": {
    "worker": {
      "name": "Worker",
      "description": "Implements tasks",
      "enabled": true,
      "defaultHarness": "codex",
      "model": "replace-with-available-model-id",
      "prompt": "Implement the requested task, verify the result, then finish."
    }
  }
}
```

## Destination behavior

- Existing graph names get an editable numbered suggestion.
- An existing agent is reused only when both identifier and full definition match.
  A differing agent gets a new numbered name and updated graph references, shown
  in the import preview. Selecting **Use existing** intentionally uses that local
  agent's settings and prompt; node overrides remain intact.
- Save validates harness model/effort availability, including node overrides.
  Errors name the agent or node. **Import agents** can map unavailable agents to
  local ones; node panels can adjust overrides before retrying Save.
- Save rechecks current catalog conflicts. Concurrent changes never authorize an
  overwrite. The existing authoring journal recovers interrupted multi-file writes.
  After an unconfirmed save, reload the catalog before creating another copy.
- Internal storage remains agent TOML, graph YAML and layout JSON in `.klm`.

The project-scoped API accepts this document directly:
`POST /api/projects/{id}/graph-imports/preview` returns a conflict-resolved document
without registering it; `POST /api/projects/{id}/graph-imports` validates and saves
the reviewed document, returning a normal graph record.

Browser exports use downloads. Desktop exports use a native Save dialog. The
mobile host uses a separate `KlmFiles` picker bridge for JSON import and JSON/SVG
export; both the frontend and mobile host must include this feature. The original
`KlmMobile` channel remains dedicated to returning to the hosts page. JSON copy
also supports HTTP LAN pages via the browser's clipboard fallback.

Manual acceptance: export a graph with all node types and a shared-workspace
branch; open its SVG; import its JSON into another project; check agents, overrides
and positions; cancel one import and save another; repeat with conflicting names
and an unavailable model. Validate file dialogs/downloads on each target client.
