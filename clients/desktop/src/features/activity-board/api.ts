import { EngineRequestError, request } from '../../engine';

export type BoardPhase = { id: string; name: string };
export type BoardValue = { id: string; name: string };
export type BoardScope = { repository?: string; repositoryId?: string; organization?: string; organizationId?: string; project?: string; projectId?: string; workItemType?: string };
export type BoardMappings = Record<string, BoardValue[]>;
export type BoardConnection = {
  id: string; name: string; provider: 'github' | 'azure'; projectId: string; scope: BoardScope; mappings: BoardMappings;
  credentialConfigured: boolean; available: boolean; syncing: boolean; sync: { lastSyncedAt?: string; stale: boolean; error?: string; snapshotPending?: boolean };
};
export type BoardConfig = { revision: string; projectionRevision: string; phases: BoardPhase[]; connections: BoardConnection[]; unresolvedMoves: BoardMove[] };
export type BoardMove = { operationId: string; key: string; connectionId: string; phaseId: string; status: 'pending' | 'confirmed' | 'failed' | 'unconfirmed'; message?: string };
export type BoardItem = { key: string; connectionId: string; externalId: string; number: string; title: string; url: string; state: string; version: string; values: BoardValue[]; phaseId: string; projectId: string; provider: 'github' | 'azure'; move?: BoardMove; iterationPath?: string };
export type BoardSprint = { id: string; name: string; path: string; organization: string; project: string; current?: boolean };
export type BoardSprints = { items: BoardSprint[]; needsRefresh: boolean; currentError?: string };
export type BoardPage = { items: BoardItem[]; hasMore: boolean; nextCursor: string; total: number; projectionRevision: string };
export type ConnectionDraft = { connectionId?: string; name: string; provider: 'github' | 'azure'; projectId: string; scope: BoardScope; token: string; mappings: BoardMappings };
const base = '/api/activity-board';
export class BoardMutationUnconfirmed extends Error {}
async function boardMutation<T>(path: string, method: string, body: unknown, timeout = 15000): Promise<T> {
  try { return await request<T>(path, method, body, timeout, AbortSignal.timeout(timeout)); }
  catch (error) {
    if (error instanceof EngineRequestError) throw error;
    throw new BoardMutationUnconfirmed('Request outcome unconfirmed. Reload current state before trying again.');
  }
}
export const boardConfig = (signal?: AbortSignal) => request<BoardConfig>(`${base}/config`, 'GET', undefined, 15000, signal);
export const refreshBoard = () => boardMutation<BoardConfig>(`${base}/refresh`, 'POST', {});
export const discoverBoard = (draft: ConnectionDraft, createLabel?: string) => request<{ scope: BoardScope; values: BoardValue[] }>(`${base}/discover`, 'POST', { ...draft, createLabel }, 95000, AbortSignal.timeout(95000));
export const discoverPhaseMappings = (connection: BoardConnection) => request<{ scope: BoardScope; values: BoardValue[] }>(`${base}/discover`, 'POST', { connectionId: connection.id, discovery: 'phase-mappings' }, 95000, AbortSignal.timeout(95000));
export const discoverAzureOptions = (draft: ConnectionDraft, discovery: 'projects' | 'work-item-types') => request<{ scope: BoardScope; values: BoardValue[] }>(`${base}/discover`, 'POST', { ...draft, discovery }, 95000, AbortSignal.timeout(95000));
export const saveBoardConnection = (draft: ConnectionDraft, expectedRevision: string) => boardMutation<BoardConfig>(`${base}/connections${draft.connectionId ? `/${encodeURIComponent(draft.connectionId)}` : ''}`, draft.connectionId ? 'PATCH' : 'POST', { ...draft, expectedRevision }, 95000);
export const removeBoardConnection = (id: string, expectedRevision: string) => boardMutation<BoardConfig>(`${base}/connections/${encodeURIComponent(id)}`, 'DELETE', { expectedRevision });
export const saveBoardPhase = (name: string, phaseId: string, mappings: BoardMappings, expectedRevision: string) => boardMutation<BoardConfig>(`${base}/config`, 'PATCH', { name, phaseId, mappings, expectedRevision }, 95000);
export const boardItems = (projectId: string, phaseId: string, cursor: string, signal?: AbortSignal, sprintId = '') => request<BoardPage>(`${base}/items?${new URLSearchParams({ projectId, phaseId, cursor, sprintId, limit: '40' })}`, 'GET', undefined, 15000, signal);
export const boardSprints = (projectId: string, signal?: AbortSignal) => request<BoardSprints>(`${base}/sprints?${new URLSearchParams({ projectId })}`, 'GET', undefined, 95000, signal);
export const moveBoardItem = (item: BoardItem, phaseId: string, revision: string, operationId: string) => boardMutation<BoardMove>(`${base}/items/${encodeURIComponent(item.key)}/phase`, 'POST', { phaseId, expectedConfigRevision: revision, observedVersion: item.version, operationId });
export const cancelUnacceptedMove = (item: BoardItem, phaseId: string, revision: string, operationId: string) => boardMutation<BoardMove>(`${base}/items/${encodeURIComponent(item.key)}/phase/cancel`, 'POST', { phaseId, expectedConfigRevision: revision, observedVersion: item.version, operationId });
export const boardMoveReceipt = (id: string, signal?: AbortSignal) => request<BoardMove>(`${base}/moves/${encodeURIComponent(id)}`, 'GET', undefined, 15000, signal);
export const boardError = (error: unknown) => error instanceof Error ? error.message : 'Tasks request failed.';
export const connectionDraft = (c: BoardConnection): ConnectionDraft => ({ connectionId: c.id, name: c.name, provider: c.provider, projectId: c.projectId, scope: c.scope, token: '', mappings: c.mappings });
export const providerName = (provider: string) => provider === 'github' ? 'GitHub' : 'Azure DevOps';
export const scopeName = (c: Pick<BoardConnection, 'provider' | 'scope'>) => c.provider === 'github' ? c.scope.repository : `${c.scope.organization} / ${c.scope.project} / ${c.scope.workItemType}`;
