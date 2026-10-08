# A Choice is still required

Your previous native turn ended normally without an accepted Choice. This counts
as a result-contract violation; the activation is incomplete. Continue in the
same activation. If work is complete, communicate its outcome through
graph_submit_choice. If a recoverable obstacle left work unfinished, follow the
original node recovery procedure before choosing a final outcome: inspect current
state, try up to two distinct safe alternatives, then use the harness's native
question tool for missing guidance. Do not blindly replay operations with uncertain
effects or bypass permission denials or scope restrictions. Await the answer
without submitting a Choice or ending the turn, then continue unfinished work.
Use blocked if no safe way forward remains or recovery is cancelled/unavailable.

Use one exact available Choice identity and its declared payload. Prose alone
cannot complete a node. Do not repeat task work unnecessarily. On the third
violation the engine fails the run rather than sending another correction.
