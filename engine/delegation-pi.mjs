import { createDelegationTransport, delegationMetadata, delegationParentEligible, delegationLeafForbidden } from './delegation-protocol.mjs';
const config = /*KLM_DELEGATION_CONFIG*/{};

export default function(pi) {
  if (!config.enabled) return; // Additive asset; no behavior before explicit activation.
  if (!delegationParentEligible(config.parentRole)) {
    pi.on('session_start', () => pi.setActiveTools(pi.getActiveTools().filter(name => !delegationLeafForbidden(name))));
    pi.on('tool_call', event => delegationLeafForbidden(event.toolName) ? { block: true, reason: 'Leaf execution cannot delegate or use parent role tools.' } : undefined);
    return;
  }
  const transport = createDelegationTransport(config);
  pi.registerTool({
    name: delegationMetadata.name, label: 'Subagents', description: delegationMetadata.description,
    parameters: delegationMetadata.schema, executionMode: 'sequential',
    async execute(id, request, signal, _onUpdate, ctx) {
      try {
        const result = await transport.execute(request, id, signal);
        return { content: [{ type: 'text', text: JSON.stringify(result) }], details: { delegationBatchId: result.batchId, delegationIsError: result.isError } };
      } catch (error) {
        // The engine retains durable ownership and cancels/joins on every failure.
        // Abort rather than let a transport exception release another model step.
        if (!error.delegationInputRejected) ctx.abort();
        throw error;
      }
    },
  });
  pi.on('session_start', async (_event, ctx) => {
    await transport.ensureReady(ctx.signal);
    pi.setActiveTools(pi.getActiveTools().filter(name => name !== 'subagent' && name !== 'task'));
  });
  const beforeModel = async (_event, ctx) => {
    try { await transport.ensureReady(ctx.signal); transport.assertCanContinue(); await transport.gate('__model_boundary__', ctx.signal); }
    catch (error) { ctx.abort(); throw error; }
  };
  pi.on('before_agent_start', beforeModel);
  pi.on('before_provider_request', beforeModel);
  pi.on('tool_call', async (event, ctx) => {
    try { await transport.gate(event.toolName, ctx.signal); }
    catch (error) { return { block: true, reason: error.message }; }
  });
  pi.on('tool_result', event => event.toolName === delegationMetadata.name && event.details?.delegationIsError ? { content: event.content, details: event.details, isError: true } : undefined);
}
