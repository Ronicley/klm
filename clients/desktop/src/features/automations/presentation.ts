import type { Automation } from './api';

export const weekdays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
export const sessionModes = [
  { value: 'new_per_run', name: 'New session per run', description: 'Start fresh for every run.' },
  { value: 'reuse_created', name: 'Create once and reuse', description: 'Keep subsequent runs in the same conversation.' },
  { value: 'existing', name: 'Existing session', description: 'Continue a conversation in this project.' },
] as const;

export function triggerSummary(automation: Automation) {
  if (automation.mode === 'webhook') return 'Pull request opened';
  const s = automation.schedule;
  const recurrence = s.recurrence === 'once' ? `Once · ${s.date || 'Choose a date'}`
    : s.recurrence === 'every_days' ? `Every ${s.everyDays} ${s.everyDays === 1 ? 'day' : 'days'}`
    : s.recurrence === 'weekly' ? [...s.weekdays].sort((a, b) => a - b).map(day => weekdays[day]?.slice(0, 3)).join(', ') || 'Choose weekdays'
    : s.recurrence === 'monthly' ? `Monthly · day ${s.monthDay}` : 'Daily';
  return `${recurrence} at ${s.time}`;
}

export function sessionSummary(automation: Automation) {
  return sessionModes.find(mode => mode.value === (automation.sessionMode || 'new_per_run'))?.name || 'New session per run';
}

export function scheduledDate(at: string, zone: string) {
  return new Date(at).toLocaleString('en', { timeZone: zone, month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit' });
}
