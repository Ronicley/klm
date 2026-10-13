// Owned, turn-scoped plugin. No workspace data or credentials are logged.
const config = /*KLM_GRAPH_CONFIG*/{};

async function graphPlugin(ctx) {
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
  const correlation = (event) => ({ sessionID: event.sessionID, callID: event.id, tool: event.tool });
  await ctx.tool.hook("execute.before", async (event) => {
    const request = correlation(event);
    try {
      await gate({ ...request, operation: "admit" });
    } catch (error) {
      try { await gate({ ...request, operation: "not_started" }); } catch {}
      throw error;
    }
  });
  await ctx.tool.hook("execute.after", async (event) => {
    // V2 reports both success and failure after the executor has settled.
    await gate({ ...correlation(event), operation: "response" });
  });
  // V2 shell hooks have no session/call IDs; tool admission owns correlation.
  await ctx.shell.hook("create.before", async () => { await gate({ operation: "shell" }); });
  await gate({ operation: "ready" });
}
