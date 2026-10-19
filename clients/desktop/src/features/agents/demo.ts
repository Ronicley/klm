import type { Harness } from '../../engine';
import type { MCPSelection } from '../library/api';

// UI-only records. Model/effort/harness values below are visual fixtures, not execution defaults.
export type Agent = {
  skills?: string[];
  mcpConnections?: MCPSelection[];
  revision?: string;
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  model?: string;
  effort?: string;
  defaultHarness?: Harness['id'];
  prompt?: string;
  // Snapshot of simulated references, retained when the prototype's slug changes.
  graphReferences?: string[];
};

export const mockAgentSettings = {
  model: 'GPT-5.6 Terra',
  effort: 'high',
  defaultHarness: 'opencode',
} satisfies Pick<Agent, 'model' | 'effort' | 'defaultHarness'>;

export const initialAgents: Agent[] = [
  { id: 'planner', name: 'Planner', description: 'Turns a request into a focused implementation plan.', enabled: true, model: 'GPT-5.6 Terra', effort: 'high', defaultHarness: 'opencode', prompt: 'Turn the request and supplied context into a focused implementation plan. Identify the affected areas, constraints, and acceptance criteria. Keep the work scoped to the request.' },
  { id: 'implementer', name: 'Implementer', description: 'Implements the plan and addresses requested changes.', enabled: true, model: 'GPT-5.6 Terra', effort: 'medium', defaultHarness: 'codex', prompt: 'Implement the supplied plan or requested corrections. Follow the project conventions, preserve unrelated changes, and report the changes made and any remaining issues.' },
  { id: 'security-reviewer', name: 'Security reviewer', description: 'Reviews access controls and identifies security issues.', enabled: true, model: 'GPT-5.6 Terra', effort: 'high', defaultHarness: 'pi', prompt: 'Review the supplied implementation for security issues, including access controls and data exposure. Report actionable findings with the relevant file locations.' },
  { id: 'delivery-reviewer', name: 'Delivery reviewer', description: 'Reviews the implementation and verification reports.', enabled: true, model: 'GPT-5.6 Terra', effort: 'medium', defaultHarness: 'opencode', prompt: 'Review the implementation and supplied verification reports against the request. Identify any remaining gaps and describe the corrections needed.' },
];

// Graph references are simulated separately from agent configuration until the graph UI exists.
export const mockAgentGraphReferences: Record<string, string[]> = {
  planner: ['CSV export'],
  implementer: ['CSV export'],
};

// Catalog and compatibility are visual fixtures, not discovered harness capabilities.
export const mockAgentModels: { name: string; provider: string; harnesses: Harness['id'][]; efforts: string[] }[] = [
  { name: 'GPT-5.6 Terra', provider: 'OpenAI', harnesses: ['opencode', 'codex', 'pi'], efforts: ['low', 'medium', 'high', 'xhigh'] },
  { name: 'GPT-5.4', provider: 'OpenAI', harnesses: ['opencode', 'codex', 'pi'], efforts: ['low', 'medium', 'high', 'xhigh'] },
  { name: 'Claude Sonnet 4.6', provider: 'Anthropic', harnesses: ['opencode', 'pi'], efforts: ['low', 'medium', 'high'] },
];

const mockConnectedProviders: Record<Harness['id'], string[]> = {
  opencode: ['OpenAI', 'Anthropic'],
  codex: ['OpenAI'],
  pi: ['OpenAI'],
};

export function modelsForHarness(harness: Harness['id']) {
  return mockAgentModels.filter(model => model.harnesses.includes(harness) && mockConnectedProviders[harness].includes(model.provider));
}

export function agentSlug(name: string) {
  return name.normalize('NFKD').replace(/\p{M}/gu, '').toLowerCase().trim()
    .replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-+|-+$/g, '');
}
