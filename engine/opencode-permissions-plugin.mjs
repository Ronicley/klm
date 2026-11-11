const permissionConfig = /*KLM_PERMISSION_CONFIG*/{};

export default {
  id: "klm.policy",
  async setup(ctx) {
    // KLM owns the execution directory and cannot follow native session moves.
    await ctx.tool.transform((editor) => { editor.remove("opencode_session_move"); });
    await ctx.session.hook("context", (event) => {
      event.system.push({ type: "text", text: "KLM assigns this session's execution directory. Do not move or reassociate the native session. Access worktrees with absolute paths and an explicit tool workdir when supported, subject to normal permissions." });
    });
    const library = permissionConfig.library;
    const matches = (pattern, value) => new RegExp("^" + pattern.replace(/[.+^${}()|[\]\\]/g, "\\$&").replaceAll("*", ".*").replaceAll("?", ".") + "$").test(value);
    const effect = (rules, action, resource) => rules.filter(rule => matches(rule.action, action) && matches(rule.resource, resource)).at(-1)?.effect;
    let libraryReady = false;
    async function prepareLibrary() {
      if (!library || libraryReady) return;
      // Config plugins register their transforms after user plugin setup. Seal last.
      await Promise.all([ctx.agent.list(), ctx.skill.list(), ctx.tool.list()]);
      const plugins = (await ctx.plugin.list()).data;
      if (plugins.some(plugin => plugin.state.status !== "active")) throw new Error("OpenCode Library sources are not ready.");
      await ctx.mcp.transform(editor => {
        for (const [name] of editor.list()) if (name !== "klm_linked" && name !== "klm_library") editor.remove(name);
      });
      await ctx.skill.transform(editor => {
        for (const skill of editor.list()) {
          if (!library.skills.some(selected => selected.id === skill.id && selected.path === skill.path)) editor.remove(skill.id);
        }
      });
      await ctx.agent.transform(editor => {
        for (const agent of editor.list()) {
          if (!permissionConfig.yolo && agent.id === library.root && library.skills.some(skill => effect(agent.permissions, "skill", skill.id) === "deny")) {
            throw new Error("Native OpenCode permission denies a selected Library skill.");
          }
          editor.update(agent.id, draft => {
            if (agent.id === library.root) draft.mode = "primary";
            if (permissionConfig.yolo) draft.permissions = [{ action: "*", resource: "*", effect: "allow" }];
            draft.permissions.push({ action: "skill", resource: "*", effect: "deny" });
            for (const skill of library.skills) if (!skill.internal || agent.id === library.root) draft.permissions.push({ action: "skill", resource: skill.id, effect: "allow" });
            draft.permissions.push({ action: "klm_linked_*", resource: "*", effect: agent.id === library.root ? "allow" : "deny" });
          });
        }
      });
      await ctx.session.hook("context", async event => {
        const session = (await ctx.session.get({ sessionID: event.sessionID })).data;
        if (session.parentID) {
          if (event.agent === library.root) throw new Error("Native subagents cannot use the managed root role.");
          for (const name of Object.keys(event.tools)) if (name.startsWith("klm_linked_")) delete event.tools[name];
        }
      });
      await ctx.tool.transform(editor => {
        for (const tool of editor.list()) if (tool.id.startsWith("opencode_")) editor.remove(tool.id);
      });
      libraryReady = true;
    }
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
    if (permissionConfig.yolo && !library) {
      await ctx.agent.transform((editor) => {
        for (const agent of editor.list()) {
          editor.update(agent.id, (draft) => { draft.permissions = [{ action: "*", resource: "*", effect: "allow" }]; });
        }
      });
    }
    await graphPlugin(ctx);
    await delegationPlugin(ctx);
    await ctx.rpc.register({
      id: "klm.policy",
      methods: {
        inventory: {
          input: { type: "object", additionalProperties: false },
          output: { type: "object", properties: { tools: { type: "array", items: { type: "string" } } }, required: ["tools"], additionalProperties: false },
        },
      },
      events: {},
    }, { inventory: async () => {
      await prepareLibrary();
      return { tools: (await ctx.tool.list()).map((tool) => tool.id) };
    } });
  },
};
