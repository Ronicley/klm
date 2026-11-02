import { request } from '../../engine';

export type Coverage = { known: number; total: number };
export type ExecutionUsageCoverage = { complete: number; partial: number; unavailable: number; total: number };
export type Accounting = {
  version: number; protocol: string; harnessVersion?: string;
  coverage: 'complete' | 'partial' | 'unavailable'; reason?: string;
  usage: { inputTokens: number; outputTokens: number; cacheReadTokens: number; cacheWriteTokens: number; reasoningTokens: number | null } | null;
  cost: null; costCoverage: 'unavailable'; costReason: string; modelAttribution: string;
  source: string | null; baselineObservedAt: string | null; endpointObservedAt: string | null;
};
export type ObservedExecutionUsage = {
  inputTokens: number | null; outputTokens: number | null; tokens: number | null;
  coverage: ExecutionUsageCoverage; cost: null; averageCost: null; costCoverage: ExecutionUsageCoverage; reason?: string;
};
export type Execution = {
  id: string; startedAt: string; endedAt: string | null; observedAt: string;
  outcome: string | null; interruptionReason?: string; durationMs: number;
  partial: boolean; harness: string; models: string[]; requestedModel?: string; source: string;
  accounting: Accounting | null;
  role: string | null;
  timing: { source: 'engine_visible_emit'; firstResponseAt: string | null; firstResponseLatencyMs: number | null } | null;
};
export type MetricsRow = {
  id: string; title: string; projectId: string; project: string; harness: string; models: string[];
  archived: boolean; currentState: string; lastOutcome: string | null;
  inputTokens: number | null; outputTokens: number | null; tokens: number | null; cost: string | null;
  durationMs: number | null; durationPartial: boolean; executions: number | null;
  failed: number | null; cancelled: number | null; interrupted: number | null; completed: number | null;
  lastActivityAt: string | null; observedSince: string | null;
};
export type Metrics = {
  updatedAt: string; page: number; pageSize: number; total: number; rows: MetricsRow[];
  summary: {
    observedExecutionUsage: ObservedExecutionUsage;
    interruptions: { total: number; stop: number; replacement: number; restart: number; shutdown: number; other: number } | null;
    sessions: number; inputTokens: number | null; outputTokens: number | null; tokens: number | null;
    cost: string | null; averageCost: string | null; durationMs: number | null; durationPartial: boolean;
    executions: number | null; failed: number | null; cancelled: number | null;
    interrupted: number | null; completed: number | null; working: number; waiting: number;
    coverage: Record<string, Coverage>;
  };
};
export type AnalyticsMeasure = {
  executions: number; durationMs: number | null; durationCoverage: ExecutionUsageCoverage; usage: ObservedExecutionUsage;
};
export type AnalyticsGroup = AnalyticsMeasure & { id: string; label: string };
export type AnalyticsBucket = AnalyticsMeasure & { from: string; to: string; label: string };
export type Analytics = {
  updatedAt: string;
  analytics: {
    period: { from: string; to: string; timezone: string; bucket: string };
    summary: AnalyticsMeasure; projects: AnalyticsGroup[]; harnesses: AnalyticsGroup[]; buckets: AnalyticsBucket[];
    matchedSessions: number; sessionsWithHistory: number; undatedUsageExecutions: number;
    timeAttribution: 'execution_end_approximation'; modelAttribution: 'unallocated';
  };
};
export const getAnalytics = (query: string, signal: AbortSignal) => request<Analytics>(`/api/metrics/sessions?view=analytics&${query}`, 'GET', undefined, 15000, signal);
export type ExecutionPage = { rows: Execution[]; total: number; page: number; pageSize: number };
export const getMetrics = (query: string, signal: AbortSignal) => request<Metrics>(`/api/metrics/sessions?${query}`, 'GET', undefined, 15000, signal);
export const getExecutions = (id: string, page: number, signal: AbortSignal) => request<ExecutionPage>(`/api/metrics/sessions/${encodeURIComponent(id)}/executions?page=${page}`, 'GET', undefined, 15000, signal);
