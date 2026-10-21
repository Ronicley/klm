import { request } from '../../engine';

export type ActivityCategory = 'PRs' | 'Pipelines' | 'Issues' | 'Security' | 'Repository' | 'Other';
export type ActivityCatalogItem = { id: string; label: string; defaultEnabled: boolean; category: ActivityCategory };
export type ActivityCatalog = { version: 1; items: ActivityCatalogItem[] };
export type GithubActivityItem = {
  id: string; projectId: string; projectName: string; webhookId: string; provider: 'github';
  eventType: string; action?: string; catalogId?: string; releaseSequence: string;
  receivedAt: string; emittedAt: string; expiresAt?: string; category: ActivityCategory;
  tone: 'accent' | 'success' | 'danger' | 'warning' | 'muted'; icon: string;
  actionLabel: string; summary: string; actor?: string; repository?: string;
  title: string; reference: string; branch?: string; baseBranch?: string;
  targetUrl?: string; targetKind?: 'resource' | 'repository'; result?: string; summaryTruncated?: boolean;
  detailEventId?: string; groupRelation?: string;
  retentionPending?: boolean; // Client-only shell while a group's members are revalidated.
  pipeline?: { kind: string; repositoryId: string; id: string; runId?: string; attempt?: string; runNumber?: string; workflowId?: string; workflowName?: string; suiteId?: string; checkUrl?: string; deploymentId?: string; environment?: string; sha?: string; ref?: string; status?: string; conclusion?: string; createdAt?: string; updatedAt?: string; startedAt?: string; completedAt?: string };
  group?: { kind: 'workflow' | 'deployment'; runId?: string; attempt?: string; deploymentId?: string; eventCount: number; childCount: number; children: GithubActivityItem[]; childrenHasMore: boolean; filtered: boolean; highWatermark: string; composition: string };
};
export type GithubActivityPage = { items: GithubActivityItem[]; nextCursor?: string; hasMore: boolean; highWatermark: string; asOf: string };
export type GithubActivityDetails = { id: string; fields: { label: string; value: string }[]; body?: string; entries?: { text: string; url?: string }[]; truncated: boolean };
export const getActivityCatalog = (signal?: AbortSignal) => request<ActivityCatalog>('/api/activity/github/catalog', 'GET', undefined, 15000, signal);
export const getActivityPage = (query: string, cursor?: string, signal?: AbortSignal) => request<GithubActivityPage>(`/api/activity/github?${query}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`, 'GET', undefined, 30000, signal);
export const getActivityDetails = (id: string, signal?: AbortSignal) => request<GithubActivityDetails>(`/api/activity/github/${encodeURIComponent(id)}`, 'GET', undefined, 30000, signal);
export type ActivityBookmark = { eventId: string; cardId: string; expiresAt?: string };
export const getActivityBookmarks = (ids: string[], high: string | undefined, signal?: AbortSignal) => request<{ items: ActivityBookmark[] }>(`/api/activity/github/bookmarks?${new URLSearchParams([...ids.map(id => ['eventId', id]), ...(high ? [['high', high]] : [])])}`, 'GET', undefined, 30000, signal);

export function safeActivityUrl(value?: string) {
  if (!value || value.length > 2048 || /[\u0000-\u001f\u007f-\u009f\\]/.test(value) || !/^https?:\/\//.test(value)) return undefined;
  try { const url = new URL(value); return url.hostname && !url.username && !url.password ? value : undefined; } catch { return undefined; }
}
// Decimal strings preserve uint64 sequences beyond JavaScript's safe integer range.
export function compareActivitySequence(a: string, b: string) { return a.length - b.length || a.localeCompare(b); }
