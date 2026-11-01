import { request, type Revision, type Harness, type Topic } from '../../engine';
import type { CapabilitySelection } from '../library/api';

export const DEFAULT_ENGINE_TIME_ZONE = 'America/Sao_Paulo';

export type AutomationSchedule = { time: string; date?: string; recurrence: 'once' | 'daily' | 'every_days' | 'weekly' | 'monthly'; everyDays: number; weekdays: number[]; monthDay: number; anchor?: string };
export type Automation = {
  sessionMode?: 'new_per_run' | 'reuse_created' | 'existing'; sessionId?: string; followUpPrompt?: string;
  id: string; projectId: string; revision: Revision; operationId: string; name: string; prompt: string;
  mode: 'webhook' | 'schedule'; enabled: boolean; parallel: boolean; harness: Harness['id']; model: string; effort: string; yolo: boolean;
  graphs: string[]; selection: CapabilitySelection; topicId: string; schedule: AutomationSchedule; nextScheduledAt?: string; afterSequence: number;
};
export type AutomationRow = { automation: Automation; queued: number; active: number; blockedReason?: string };
export type AutomationList = { items: AutomationRow[]; timeZone: string; topics: Topic[]; hasMore?: boolean; nextOffset?: number };
export type AutomationRunLink = { runId: string; sessionId: string; title: string };
export type EngineSettings = { timeZone: string; revision: Revision };
export const automationPath = (projectId: string, id?: string) => `/api/projects/${encodeURIComponent(projectId)}/automations${id ? `/${encodeURIComponent(id)}` : ''}`;
export const getEngineSettings = () => request<EngineSettings>('/api/settings/engine');

export function relativeNextRun(at?: string, current = Date.now()) {
  if (!at) return '';
  const hours = Math.max(0, Math.floor((Date.parse(at) - current) / 3600000));
  const days = Math.floor(hours / 24), remainder = hours % 24;
  const parts = [days ? `${days} ${days === 1 ? 'day' : 'days'}` : '', remainder ? `${remainder} ${remainder === 1 ? 'hour' : 'hours'}` : ''].filter(Boolean);
  return `Next run in ${parts.join(' and ') || 'less than an hour'}`;
}
