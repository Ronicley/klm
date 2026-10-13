const permissionConfig = /*KLM_PERMISSION_CONFIG*/{};

export default {
  id: "klm.policy",
  async setup(ctx) {
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
      if (envelope.error || envelope.result?.allowed !== true) throw new Error("Permission denied by KLM.");
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
