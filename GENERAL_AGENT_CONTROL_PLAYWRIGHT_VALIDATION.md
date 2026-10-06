# General-agent control: Playwright validation

Date: 2026-10-06.

This records the initial control slice before live YOLO changes. Updated spawn
inheritance, recoverable spawn errors and execution-time YOLO validation are in
[YOLO_SPAWN_PLAYWRIGHT_VALIDATION.md](YOLO_SPAWN_PLAYWRIGHT_VALIDATION.md).

## Environment

- Web client: `http://127.0.0.1:5173`.
- Current development engine: `http://127.0.0.1:17331`, built with `-tags dev`.
- OpenCode **1.18.33**, model **`openai/gpt-6-luna`**, effort **`low`** for the general agent and all disposable sessions.
- Isolated data: `C:\Users\Samuel\AppData\Local\Temp\opencode\klm-control-playwright-20261006`.
- Disposable project directories: `C:\Users\Samuel\AppData\Local\Temp\opencode\klm-control-fixtures\a` and `b`.
- The previous development runtime was stopped with the user's approval; its data was preserved. The installed release engine remained running (PID 59800 at the final check).
- Fixture projects/folders and extra sessions were prepared through the public API from Playwright; harness/model selection, prompts, recipient timelines, native permission approval and reload were exercised in the UI. Tool execution used real OpenCode conversations, not mocked responses.

The first launch inherited `OPENCODE_CONFIG_CONTENT` containing the calling harness's owned KLM plugin. That unrelated plugin denied preflight in the test workspace. Restarting the disposable engine with that inherited variable cleared resolved the environment conflict. No production permission rules or plugin behavior were relaxed.

## Observed results

| Scenario | Result |
| --- | --- |
| Project inventory | Two nonremoved projects; `project_list` limit 1 and its cursor returned both. |
| Session inventory/details | Main identities, configured/resolved OpenCode/Luna/low, folder, cwd, working vs retained runtime, usage and pending requests were returned. Archived sessions were included explicitly. |
| Unknown context | A fresh archived probe returned null usage/context, not 0%. |
| Rename/move/settings | Receiver became **Receiver Verified** in **Validated**. Cwd remained project B's registered directory. Low and YOLO false were retained. |
| Invalid atomic settings | Invalid effort combined with YOLO true was rejected; low/YOLO false remained. A busy-target YOLO update also conflicted. |
| Graph catalog/selection | **Selection Check** was listed and selected. No graph was executed; final health reported zero active graph runs. |
| Visible general-agent orders | `playwright-order-1` entered normal history; `playwright-order-2` was accepted as queued. Both instructions, normal activity and replies appeared in the recipient timeline without consultation grouping. |
| Idempotency | Identical retries returned the original message ID `456f4ecd199a73ac5601c5d1a82b4499`, including after engine restart. The evidence file still contained exactly `FIRST` and `SECOND`, once each. |
| Native permission | The second order's append command required approval; Playwright clicked **Allow** once for that disposable command. |
| Ordinary collaboration | Sender sent to a same-project receiver; identical retry returned the same receipt. The receiver displayed exactly one **Instruction from Sender** and replied `NORMAL_SEND_DONE`. |
| Ordinary scope | A normal sender's cross-project send was rejected as outside its scope. |
| Archived destination | General-agent reads worked; sending was rejected with restoration required. The archived probe remained archived, with zero events. |
| Correlated consultation | `session_ask` yielded, the recipient answered through `linked_answer`, and the general conversation resumed with the answer. The recipient consultation remained grouped and finished as completed/delivered. |
| Native question | Receiver displayed Alpha/Beta. General read the exact request and answered the explicitly chosen Alpha through `session_question_answer`; Receiver replied `QUESTION_DONE Alpha`. |
| Question provenance/repetition | The response was stored as `agent_prompt`, origin kind `question_answer`, and displayed **Question response from General agent**. Repeating the same answer conflicted as no longer waiting. |
| Messages/search | Message pages and literal marker search returned attributed instructions and assistant replies. |
| Long UTF-8 message/event | A 40,974-byte message was read at offsets 0 and 4096, with next offsets 4096 and 8192. Event reads returned JSON fragments at those offsets. No replacement characters appeared. |
| Expired revision | `sinceRevision: 0` returned `resetRequired: true` and bounded resynchronization pages. |
| Strict live delta pagination | Five pages returned 3, 3, 3, 3 and 1 items. `consumedRevision` stayed 1056 through page 4, then advanced to 1116 only on the final page. `DELTA_SEND_DONE` appeared on page 4; repeated updates to an event ID were also observed. |
| Reload | The final report and history persisted after page reload; the accepted general harness picker remained absent. |

At the final observation, the general conversation had **47 successful KLM tool calls**. The largest recorded successful tool-response content was **8,857 UTF-8 bytes**, below the 64 KiB ceiling. Final API health was `ok`, with **zero running sessions**, **zero active graph runs**, and no pending receiver questions or permissions. The fresh page reload had no browser console errors.

## Corrections made during validation

1. Normal history cursors originally depended on the engine's global revision. Recording a tool result in the requester invalidated the unchanged recipient's next page. Cursors now bind to the recipient's history version/journal revision; unrelated turns do not invalidate them. Snapshot fragments also exclude the changing global observation revision from their content fingerprint.
2. Luna copied an incomplete long cursor during a multi-page read; the engine correctly rejected it. Cursor binding hashes were compacted, and unused snapshot versions were removed from delta cursors. In the successful five-page retest, all cursors were copied exactly and were 91–92 characters long.
3. Added `TestGeneralHistoryPaginationIgnoresOtherTurns` for unchanged-recipient pagination, real target updates and reset-page continuation. It and `TestGeneralHistoryDeltaFragments` passed after the corrections.

The current development binary was rebuilt and used for the successful retests. No full test suite was run.

## Evidence

Screenshots, kept in the ignored Playwright output directory:

- [General-agent controls](.playwright-mcp/klm-control-general-validation.png)
- [Normal recipient order timeline](.playwright-mcp/klm-control-recipient.png)
- [Pending native question](.playwright-mcp/klm-control-question-pending.png)
- [Answered native question](.playwright-mcp/klm-control-question-answered.png)
- [Ordinary-session order](.playwright-mcp/klm-control-normal-send.png)
- [Completed delta pagination](.playwright-mcp/klm-control-delta-pagination.png)

The disposable development runtime and its data remain available for inspection. Workspaces and the previous development data were not deleted.

## Not exercised in this run

Pi/Codex runtime coverage, native desktop/mobile clients, graph-node question routing, secret-answer handling in a supporting harness, and restart during uncertain native delivery remain separate validation items. Graph execution was deliberately not invoked. The long-message test read two fragments rather than reconstructing the entire message; the focused Go delta-fragment test verifies full reconstruction and revision consumption.
