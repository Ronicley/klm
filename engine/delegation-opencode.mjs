import { createDelegationTransport, delegationMetadata, delegationParentEligible, delegationLeafForbidden } from './delegation-protocol.mjs';
const config = /*KLM_DELEGATION_CONFIG*/{};

// OpenCode 1.18.35 registry::fromPlugin explicitly supports non-Zod raw JSON
// property definitions. Its Schema.Unknown compatibility boundary preserves args;
// execute validates them and tool.definition publishes the complete v1 schema.
export default async function(context) {
    if (!config.enabled) return {};
    const eligible = delegationParentEligible(config.parentRole);
    const transport = eligible ? createDelegationTransport(config) : undefined;
    if (transport) await transport.ensureReady(); // Replacement attested before native suppression.
    const calls = new Map();
    const key = (session, operation) => session + '\0' + operation;
    const suppress = message => {
      message.tools = { ...message.tools, task: false, ...(eligible ? {} : { subagent_run: false }) };
      if (!eligible) for (const name of Object.keys(message.tools)) if (delegationLeafForbidden(name)) message.tools[name] = false;
    };
    return {
      ...(eligible ? { tool: { [delegationMetadata.name]: {
        description: delegationMetadata.description,
        args: delegationMetadata.schema.properties,
        async execute(request, ctx) {
          const callKey = key(ctx.sessionID, request.operationId);
          const pending = calls.get(callKey) ?? [];
          const nativeCallId = pending.shift();
          if (!pending.length) calls.delete(callKey);
          try {
            const result = await transport.execute(request, nativeCallId, ctx.abort);
            if (result.isError) {
              const failure = new Error(JSON.stringify(result)); failure.delegationSettled = true; throw failure;
            }
            return JSON.stringify(result);
          }
          catch (error) {
            // Native abort cancels this tool's signal. Do not deadlock by awaiting
            // the abort endpoint while it waits for this tool to unwind.
            if (!error.delegationSettled && !error.delegationInputRejected) void context.client.session.abort({ path: { id: ctx.sessionID }, throwOnError: true }).catch(() => {});
            throw error;
          }
        },
      } } } : {}),
      config: async cfg => {
        // Apply after any YOLO '*' rewriting. The unconditional before hook and
        // per-input map remain necessary; config-only suppression failed CP1a.
        cfg.permission = { ...cfg.permission, task: 'deny' };
        cfg.tools = { ...cfg.tools, task: false };
        for (const agent of Object.values(cfg.agent ?? {})) {
          agent.permission = { ...agent.permission, task: 'deny' };
          agent.tools = { ...agent.tools, task: false };
        }
      },
      'chat.message': async (_input, output) => suppress(output.message),
      'tool.definition': async (input, output) => { if (eligible && input.toolID === delegationMetadata.name) output.jsonSchema = delegationMetadata.schema; },
      'tool.execute.before': async (input, output) => {
        if (input.tool === 'task' || input.tool === 'subagent' || !eligible && delegationLeafForbidden(input.tool)) throw new Error('Native/nested delegation is unavailable.');
        await transport?.gate(input.tool);
        if (eligible && input.tool === delegationMetadata.name) {
          const callKey = key(input.sessionID, output.args?.operationId);
          calls.set(callKey, [...(calls.get(callKey) ?? []), input.callID]);
        }
      },
      'experimental.chat.messages.transform': async (_input, output) => {
        transport?.assertCanContinue(); // Includes a fatal latch after active Promise cleanup.
        await transport?.gate('__model_boundary__');
        for (const message of output.messages) if (message.info.role === 'user') suppress(message.info);
      },
    };
}
