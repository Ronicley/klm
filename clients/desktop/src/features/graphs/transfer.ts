import type { Agent } from '../agents/demo';
import type { GraphDefinition, GraphLayout } from './files';

export const GRAPH_TRANSFER_LIMIT = 512 * 1024;
export type TransferAgent = Required<Pick<Agent, 'name' | 'description' | 'enabled' | 'defaultHarness' | 'model' | 'prompt'>> & Pick<Agent, 'effort' | 'skills' | 'mcpConnections'>;
export type GraphTransfer = {
  format: 'klm.graph'; version: 1;
  definition: GraphDefinition; layout: GraphLayout;
  agents: Record<string, TransferAgent>;
};

export function transferAgent(agent: Agent): TransferAgent {
  if (!agent.defaultHarness || !agent.model || !agent.prompt) throw new Error(`Agent ${agent.name} has an incomplete definition.`);
  return { name: agent.name, description: agent.description, enabled: agent.enabled, defaultHarness: agent.defaultHarness, model: agent.model, prompt: agent.prompt, ...(agent.effort ? { effort: agent.effort } : {}), ...(agent.skills?.length ? { skills: [...agent.skills] } : {}), ...(agent.mcpConnections?.length ? { mcpConnections: agent.mcpConnections.map(connection => ({ ...connection, tools: [...connection.tools] })) } : {}) };
}

export function graphTransfer(definition: GraphDefinition, layout: GraphLayout, agents: Agent[]): GraphTransfer {
  const ids = new Set(Object.values(definition.nodes).flatMap(node => node.agent ? [node.agent] : []));
  return { format: 'klm.graph', version: 1, definition, layout, agents: Object.fromEntries([...ids].map(id => {
    const agent = agents.find(item => item.id === id);
    if (!agent) throw new Error(`Agent ${id} is unavailable. Select an agent before exporting.`);
    return [id, transferAgent(agent)];
  })) };
}

export function transferJSON(bundle: GraphTransfer) {
  const text = JSON.stringify(bundle, null, 2);
  if (new TextEncoder().encode(text).length > GRAPH_TRANSFER_LIMIT) throw new Error('Graph JSON exceeds 512 KiB.');
  return text;
}

export function parseTransfer(text: string): unknown {
  if (new TextEncoder().encode(text).length > GRAPH_TRANSFER_LIMIT) throw new Error('Graph JSON exceeds 512 KiB.');
  try { return JSON.parse(text.replace(/^\uFEFF/, '')); }
  catch { throw new Error('Invalid JSON. Check the document syntax.'); }
}

export function remapTransfer(bundle: GraphTransfer, selections: Record<string, string>, catalog: Agent[]): GraphTransfer {
  const agents: Record<string, TransferAgent> = {};
  const include = (id: string, agent: TransferAgent) => {
    const normalized = transferAgent({ ...agent, id });
    if (Object.hasOwn(agents, id) && JSON.stringify(agents[id]) !== JSON.stringify(normalized)) throw new Error(`Agent ${agent.name} has conflicting mappings. Select the same definition for both references.`);
    agents[id] = normalized;
  };
  const nodes = Object.fromEntries(Object.entries(bundle.definition.nodes).map(([id, node]) => {
    if (!node.agent) return [id, node];
    const target = Object.hasOwn(selections, node.agent) ? selections[node.agent] : undefined;
    if (!target) {
      include(node.agent, bundle.agents[node.agent]);
      return [id, node];
    }
    const agent = catalog.find(item => item.id === target && item.enabled);
    if (!agent) throw new Error(`Agent ${target} is no longer available.`);
    include(target, transferAgent(agent));
    return [id, { ...node, agent: target }];
  }));
  return { ...bundle, definition: { ...bundle.definition, nodes }, agents };
}
