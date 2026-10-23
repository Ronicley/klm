// Owned tool preflight. Native restrictions still apply outside YOLO mode.
const permissionConfig = /*KLM_PERMISSION_CONFIG*/{};
export default async function (context) {
  const hooks = await graphPlugin(context);
  async function steeringRPC(method, params) {
    const response = await fetch(permissionConfig.url, {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: "Bearer " + permissionConfig.token },
      body: JSON.stringify({ jsonrpc: "2.0", id: crypto.randomUUID(), method, params }),
      signal: AbortSignal.timeout(10000),
    });
    if (!response.ok) throw new Error("KLM steering boundary is unavailable.");
    const envelope = await response.json();
    if (envelope.error || !envelope.result) throw new Error("KLM steering boundary failed.");
    return envelope.result;
  }
  const ready = await steeringRPC("klm/opencode/ready", {});
  if (ready.ready !== true) throw new Error("KLM permission plugin handshake failed.");
  return {
    ...hooks,
    "experimental.chat.messages.transform": async (input, output) => {
      await hooks["experimental.chat.messages.transform"]?.(input, output);
      if (permissionConfig.graphNode) return;
      const user = output.messages.findLast(message => message.info.role === "user");
      if (!user) return;
      const sessionID = user.info.sessionID;
      const batch = await steeringRPC("klm/steering/take", { sessionID });
      if (!batch.batchID) return;
      // noReply waits for every native part to persist, unlike prompt_async's
      // early HTTP response and message.updated's header-only SSE event.
      const saved = await context.client.session.prompt({
        path: { id: sessionID },
        body: { noReply: true, parts: batch.parts, agent: user.info.agent,
          model: user.info.model, variant: user.info.variant ?? user.info.model?.variant },
        throwOnError: true, signal: AbortSignal.timeout(15000),
      });
      if (saved.data?.info?.role !== "user" || saved.data.info.sessionID !== sessionID ||
          !Array.isArray(saved.data.parts) || saved.data.parts.length < batch.parts.length ||
          saved.data.parts.some(part => part.sessionID !== sessionID || part.messageID !== saved.data.info.id)) {
        throw new Error("OpenCode did not persist the complete steering input.");
      }
      // The loop already captured its snapshot. Include the complete persisted
      // message in this very model call rather than waiting for SSE processing.
      output.messages.push(saved.data);
      await steeringRPC("klm/steering/consumed", { sessionID, batchID: batch.batchID });
    },
    "tool.execute.before": async (input, output) => {
      const response = await fetch(permissionConfig.url, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: "Bearer " + permissionConfig.token },
        body: JSON.stringify({ jsonrpc: "2.0", id: crypto.randomUUID(), method: "klm/permission/opencode", params: {
          sessionID: input.sessionID, toolCallId: input.callID, toolName: input.tool,
          cwd: context.directory, input: output.args,
        } }),
      });
      if (!response.ok) throw new Error("KLM permission gate is unavailable.");
      const envelope = await response.json();
      if (envelope.error || envelope.result?.allowed !== true) throw new Error("Permission denied by KLM.");
      // Admission happens after permission resolution; a sealed graph still denies.
      await hooks["tool.execute.before"]?.(input, output);
    },
    config: async (config) => {
      if (permissionConfig.yolo) {
        const skill = config.permission?.skill;
        config.permission = { "*": "allow", ...(permissionConfig.library ? { skill, "klm_linked_*": "deny" } : {}) };
        for (const agent of Object.values(config.agent ?? {})) {
          const skill = agent.permission?.skill;
          const linked = agent.permission?.["klm_linked_*"];
          agent.permission = { "*": "allow", ...(permissionConfig.library && skill ? { skill } : {}), ...(permissionConfig.library && linked ? { "klm_linked_*": linked } : {}) };
          if (agent.tools) {
            for (const tool of Object.keys(agent.tools)) agent.tools[tool] = true;
          }
        }
        if (config.tools) for (const tool of Object.keys(config.tools)) config.tools[tool] = true;
      }
      if (permissionConfig.library) {
        await steeringRPC("klm/library/opencode/ready", {});
      }
    },
  };
}
