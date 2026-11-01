import { request } from '../../engine';
import type { AgentDefinition, AgentPage, AgentSource, CatalogAgent } from './types';

export const agentMutationPath = (source: AgentSource) => source.scope === 'global'
  ? `/api/agents/${encodeURIComponent(source.id)}`
  : `/api/projects/${encodeURIComponent(source.projectId!)}/agents/${encodeURIComponent(source.id)}`;

export function getAgentPage(projectId: string, search: string, offset: number, signal?: AbortSignal) {
  const query = new URLSearchParams({ search, offset: String(offset), limit: '50' });
  if (projectId) query.set('projectId', projectId);
  return request<AgentPage>(`/api/agents?${query}`, 'GET', undefined, 15000, signal);
}

export function getAgent(source: AgentSource, offset = 0, signal?: AbortSignal) {
  const query = new URLSearchParams({ sourceUid: source.sourceUid, offset: String(offset), limit: '50' });
  if (source.scope === 'project') query.set('projectId', source.projectId!);
  return request<CatalogAgent>(`/api/agents/${encodeURIComponent(source.id)}?${query}`, 'GET', undefined, 15000, signal);
}

export function saveAgent(agent: AgentDefinition, scope: 'global' | 'project', projectId?: string, old?: CatalogAgent) {
  const path = old ? agentMutationPath(old.source) : scope === 'global' ? '/api/agents' : `/api/projects/${encodeURIComponent(projectId!)}/agents`;
  return request<CatalogAgent>(path, old ? 'PATCH' : 'POST', {
    agent, ...(old ? { revision: old.revision, sourceUid: old.source.sourceUid } : {}),
  }, 60000, AbortSignal.timeout(60000));
}

export const deleteAgent = (agent: CatalogAgent) => request(agentMutationPath(agent.source), 'DELETE', {
  revision: agent.revision, sourceUid: agent.source.sourceUid,
}, 15000, AbortSignal.timeout(15000));
