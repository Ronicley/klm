import type { Session, ModelCatalog } from '../../engine';
import type { CatalogAgent, SessionAgent } from './types';

// Shared, explicit test fixtures; production never imports this module.
export const sourceAgent: CatalogAgent = {
  id: 'specialist', name: 'Specialist', description: '', enabled: true,
  defaultHarness: 'opencode', model: 'provider/one', effort: 'high', prompt: 'Specialist prompt',
  skills: ['skill-one'], mcpConnections: [{ connectionId: 'connection-one', tools: ['tool_one'] }],
  revision: 'source-1', source: { sourceUid: 'uid-one', id: 'specialist', scope: 'global' },
  status: 'linked', available: true, references: [], referencesHasMore: false,
};
export const agentDetail: SessionAgent = {
  source: sourceAgent.source, name: sourceAgent.name, sourceStatus: 'linked', capturedRevision: 'source-1',
  overriddenFields: [], pending: false, execution: { allowed: true, token: 'token-one' }, revision: '1', overrides: {},
  inheritedCapabilities: { skills: [], mcpConnections: [] },
  desired: { harness: 'opencode', model: 'provider/one', effort: 'high', prompt: 'Specialist prompt', selection: { skills: ['skill-one'], mcpConnections: [{ connectionId: 'connection-one', tools: ['tool_one'] }] } },
  applied: { config: { harness: 'opencode', model: 'provider/one', effort: 'high', prompt: 'Applied specialist prompt', selection: { skills: [], mcpConnections: [] } }, revision: 'applied-1' },
};
export const boundSession: Session = { id: 'session-one', projectId: 'project-one', title: 'Session', workspace: 'Ungrouped', harness: 'opencode', model: 'provider/one', effort: 'high', status: 'idle', events: [], createdAt: '', updatedAt: '', agentConfig: agentDetail };
export const modelCatalog: ModelCatalog = { models: [
  { id: 'provider/one', name: 'Model One', provider: 'provider', providerName: 'Provider', efforts: ['low', 'high'] },
  { id: 'provider/two', name: 'Model Two', provider: 'provider', providerName: 'Provider', efforts: ['low'] },
], connectedProviders: [{ id: 'provider', name: 'Provider', authType: 'fixture' }], defaultModel: 'provider/one', effortLabel: 'Effort' };
