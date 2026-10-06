# YOLO inheritance, spawn recovery and live permission mode

Validated on 2026-10-06 using Playwright, the web client on port 5173 and the
current isolated development engine on port 17331. All executing sessions used
**OpenCode / `openai/gpt-6-luna` / `low`**. The installed release engine was not
restarted or updated.

Data directory: `C:\Users\Samuel\AppData\Local\Temp\opencode\klm-control-playwright-20261006`.
The existing Control B disposable project and Sender/Receiver sessions were reused.

## Live YOLO

Receiver began with YOLO disabled and requested permission for
`Write-Output 'LIVE_ONE'`. The YOLO switch was enabled and editable while the
Stop button remained present. Switching YOLO on removed that pending permission
without clicking Allow or stopping the turn. `LIVE_ONE` and `LIVE_TWO` both ran;
their audit entries recorded automatic allow with reason `YOLO mode`.

Receiver then asked a native Continue question, which remained pending despite
YOLO. While it waited, YOLO was switched off. After answering Continue, the next
command, `Write-Output 'LIVE_THREE'`, requested permission again. Allow was clicked
once and Receiver replied `LIVE_YOLO_DONE`.

All three commands completed in the same work flow, with no stopped/interrupted
events. No file changes were requested by this live-toggle test.

## Spawn inheritance and recovery

Sender's YOLO was enabled. Its user prompt explicitly requested exactly two new
validation sessions and a non-YOLO exception for the second one.

1. Sender called `session_spawn_options` for required fields, exact model IDs,
   efforts, active folders and the actual ID of that user message. Both model
   catalog pages were read successfully.
2. The first spawn deliberately supplied `model: "gpt-6-luna"` and omitted YOLO.
   The tool rejected it with this structured error before creating a child:

   ```json
   {
     "accepted": false,
     "code": "invalid_model_format",
     "field": "model",
     "message": "OpenCode model must use provider/model format.",
     "hint": "Use a full ID such as openai/gpt-6-luna, not gpt-6-luna or Luna. Call session_spawn_options for exact model IDs, or omit model to inherit/default."
   }
   ```

3. Sender corrected only the model to `openai/gpt-6-luna` and reused operation ID
   `spawn-yolo-inherit-1`. The child was accepted with `yolo:true`; the argument
   was still omitted, so it inherited the sender.
4. Repeated accepted requests returned the same session/message receipt.
5. `spawn-yolo-disabled-1` explicitly passed `yolo:false` and was accepted with
   YOLO disabled before its first turn.

Exactly two children were created (project inventory increased from three to
five sessions). Both used OpenCode/Luna/low, had exactly one initial agent prompt,
and recorded zero Harness error events:

| Session | ID | YOLO | Reply |
| --- | --- | --- | --- |
| YOLO inherited child | `3a5ce94d5d72aa0709c749553c0fd5d2` | true | `CHILD_INHERITED_DONE` |
| YOLO disabled child | `f9944a5c9771c84a87abe224185ca302` | false | `CHILD_DISABLED_DONE` |

This validates that a malformed spawn goes back to the agent for correction
rather than leaving a broken child and a standalone Harness error.

## Lightweight checks

- Targeted Go tests passed for spawn inheritance, explicit overrides,
  pointer/value idempotency, journal/reload recovery, rejected model format,
  catalog model/effort pairs, live pending/future permission handling, question
  separation, Codex downgrade rejection and existing permission-policy cases.
- `npx tsc -b --pretty false` passed.
- `npm run test -- src/features/chat/SessionOptions.test.tsx`: two tests passed.
- The development binary was rebuilt and used for the real Playwright flows.
- No full test suite was run.

## Scope and remaining validation

Model/effort still change between turns; send YOLO alone for live updates. Native
startup configuration refreshes on the next turn. Codex cannot safely disable a
turn started with its full-access sandbox, so the engine rejects that downgrade;
this behavior has focused unit coverage, not Codex runtime validation here. Pi
runtime, native desktop/mobile clients and native hard-deny configurations were
not exercised in this run. Questions and graph authorization remain independent
of YOLO.

At final observation the engine was healthy, with zero working sessions and zero
active graph runs. The disposable runtime/data remain available for inspection.

## Screenshots

- [YOLO enabled with a native question still pending](.playwright-mcp/yolo-live-enabled-question.png)
- [YOLO disabled and the next permission requested](.playwright-mcp/yolo-live-disabled-permission.png)
- [Inherited YOLO child](.playwright-mcp/spawn-yolo-inherited.png)
- [Explicit non-YOLO child](.playwright-mcp/spawn-yolo-disabled.png)
