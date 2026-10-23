// Owned, turn-scoped plugin. It runs in the same contained server as native tools.
// No tool arguments, results, credentials or workspace data are logged here.
const config = /** @type {{url: string, token: string}} */ (/*KLM_GRAPH_CONFIG*/{});

export default async function () {
  /** @param {{operation: string, sessionID?: string, tool?: string, callID?: string}} request */
  async function gate(request) {
    const response = await fetch(config.url, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: "Bearer " + config.token },
      body: JSON.stringify({ jsonrpc: "2.0", id: crypto.randomUUID(), method: "klm/graph/opencode", params: request }),
      signal: AbortSignal.timeout(8000),
    });
    if (!response.ok) throw new Error("KLM graph gate is unavailable.");
    const envelope = await response.json();
    if (envelope.error || envelope.result?.allowed !== true) {
      throw new Error("KLM graph activation does not permit further tool execution.");
    }
  }
  // Throwing from the before hook aborts execution before item.execute / MCP call.
  /** @param {{tool: string, sessionID: string, callID: string}} input */
  const before = async (input) => {
    try {
      await gate({ ...input, operation: "admit" });
    } catch (error) {
      // If admission was recorded but its HTTP reply was lost, this hook still
      // throws before native execution. Best-effort retract that admission.
      try { await gate({ ...input, operation: "not_started" }); } catch {}
      throw error;
    }
  };
  await gate({ operation: "ready" });
  return {
    "tool.execute.before": before,
    // OpenCode 1.18.30 only calls after on a returned value. It does NOT call it
    // when a tool throws (including MCP isError). Those cases are reconciled by
    // the engine's native error events and retain uncertainty without evidence.
    "tool.execute.after": async (/** @type {{tool: string, sessionID: string, callID: string}} */ input) => {
      await gate({ ...input, operation: "response" });
    },
    // The native shell endpoint also has a gate, independent of its permission path.
    "shell.env": async (/** @type {{sessionID?: string, callID?: string}} */ input) => {
      await gate({ operation: "admit", sessionID: input.sessionID, callID: input.callID, tool: "shell" });
    },
  };
}
