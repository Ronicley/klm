import { request } from '../../engine';

export type Metric = { value: number | null; coverage: 'complete' | 'partial' | 'unavailable' | 'not_applicable'; provisional?: boolean; reasons?: string[] };
export type Totals = {
  sessionsUsed: Metric; sessionsCreated: Metric; agentsExecuted: Metric; nativeSubagents: Metric;
  workingMs: Metric; blockedMs: Metric; inputTokens: Metric; outputTokens: Metric; totalTokens: Metric;
  graphsUsed: Metric; graphRuns: Metric; graphWallMs: Metric; graphTimedRuns: number; graphAverageMs: Metric;
};
export type SeriesBucket = { key: string; start: string; endExclusive: string; label: string; future: boolean; workingMs: Metric; tokens: Metric; previousWorkingMs?: Metric; previousTokens?: Metric };
export type AnalyticsResponse = {
  version: 1; revision: string; asOf: string; timeZone: string; collectionStartedAt: string;
  scope: { projectId: string | null; includesArchived: true; includesGeneral: false };
  projects: { id: string; name: string }[];
  period: { kind: 'week' | 'all'; start: string; endExclusive: string; weekStart?: string; current: boolean };
  availableWeeks: { startDate: string; endDate: string; current: boolean }[];
  summary: Totals;
  comparison: null | { start: string; endExclusive: string; summary: Totals };
  series: SeriesBucket[];
  weekdays: { weekday: number; future: boolean; workingMs: Metric }[];
  topics: { key: string; projectId: string; topicId: string | null; name: string; projectName: string; color: string; archived: boolean; totals: Totals }[];
  harnesses: { id: string; name: string; workingMs: Metric }[];
  graphs: { key: string; projectId: string; graphId: string; name: string; projectName: string; runs: Metric; wallMs: Metric; timedRuns: number; averageMs: Metric }[];
  calendar: { startDate: string; endDate: string; activeDays: Metric; days: { date: string; weekStart: string; future: boolean; workingMs: Metric }[] };
  coverage: { reasons: string[]; childSupport: { harness: string; role: string; status: string }[] };
};
export type AnalyticsQuery = { period: string; project: string; compare: boolean; timeZone: string };
export function getAnalytics(query: AnalyticsQuery, signal: AbortSignal) {
  const params = new URLSearchParams({ period: query.period === 'all' ? 'all' : 'week', projectId: query.project, compare: query.compare && query.period !== 'all' ? 'previous' : 'none', timeZone: query.timeZone });
  if (query.period !== 'all' && query.period !== 'current') params.set('weekStart', query.period);
  return request<AnalyticsResponse>(`/api/analytics?${params}`, 'GET', undefined, 15000, signal);
}
