const permissionConfig = /*KLM_PERMISSION_CONFIG*/{};

export default {
  id: "klm.policy",
  async setup(ctx) {
    // KLM owns the execution directory and cannot follow native session moves.
    await ctx.tool.transform((editor) => { editor.remove("opencode_session_move"); });
    await ctx.session.hook("context", (event) => {
      event.system.push({ type: "text", text: "KLM assigns this session's execution directory. Do not move or reassociate the native session. Access worktrees with absolute paths and an explicit tool workdir when supported, subject to normal permissions." });
    });
    await ctx.tool.hook("execute.before", async (event) => {
      const response = await fetch(permissionConfig.url, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: "Bearer " + permissionConfig.token },
        body: JSON.stringify({ jsonrpc: "2.0", id: crypto.randomUUID(), method: "klm/permission/opencode", params: {
          sessionID: event.sessionID, toolCallId: event.id, toolName: event.tool,
          cwd: ctx.location.directory, input: event.input,
        } }),
      });
      if (!response.ok) throw new Error("KLM permission gate is unavailable.");
      const envelope = await response.json();
      if (envelope.error) throw new Error(envelope.error.message || "KLM tool preflight failed.");
      if (envelope.result?.allowed === false) throw new Error("Permission denied by KLM.");
      if (envelope.result?.allowed !== true) throw new Error("Invalid KLM tool preflight response.");
    });
    if (permissionConfig.yolo) {
      await ctx.agent.transform((editor) => {
        for (const agent of editor.list()) {
          editor.update(agent.id, (draft) => { draft.permissions = [{ action: "*", resource: "*", effect: "allow" }]; });
        }
      });
    }
    await graphPlugin(ctx);
    await ctx.rpc.register({
      id: "klm.policy",
      methods: {
        inventory: {
          input: { type: "object", additionalProperties: false },
          output: { type: "object", properties: { tools: { type: "array", items: { type: "string" } } }, required: ["tools"], additionalProperties: false },
        },
      },
      events: {},
    }, { inventory: async () => ({ tools: (await ctx.tool.list()).map((tool) => tool.id) }) });
  },
};