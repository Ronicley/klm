import type { Harness } from '../../engine';
import type { Agent } from '../agents/types';
import type { AgentModel } from '../agents/catalog';

export type AgentNodeOverrides = { harness?: Harness['id']; model?: string; effort?: string };

export const harnessNames = { opencode: 'OpenCode', codex: 'Codex', pi: 'Pi' };

export function effortLabel(value: string) {
  return value === 'xhigh' ? 'Extra high' : value ? value[0].toUpperCase() + value.slice(1) : '';
}

export function resolveAgentNode(agent: Agent, overrides: AgentNodeOverrides, catalog?: AgentModel[]) {
  const harness = overrides.harness ?? agent.defaultHarness;
  if (!harness) return undefined;
  const modelName = overrides.model ?? agent.model;
  const models: AgentModel[] = catalog ?? (modelName ? [{ id: modelName, name: modelName, provider: '', efforts: [] }] : []);
  const model = models.find(item => item.id === modelName);
  const effort = overrides.effort ?? agent.effort ?? '';
  return { harness, models, model, effort };
}
