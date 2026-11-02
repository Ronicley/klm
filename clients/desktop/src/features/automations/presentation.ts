import type { Automation, AutomationWebhook } from './api';

export const weekdays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
export const sessionModes = [
  { value: 'new_per_run', name: 'New session per run', description: 'Start fresh for every run.' },
  { value: 'reuse_created', name: 'Create once and reuse', description: 'Keep subsequent runs in the same conversation.' },
  { value: 'existing', name: 'Existing session', description: 'Continue a conversation in this project.' },
] as const;

export const webhookTriggers: (AutomationWebhook & { label: string })[] = [
  { eventType: 'pull_request', action: 'opened', label: 'Pull request opened' },
  { eventType: 'pull_request', action: 'ready_for_review', label: 'Pull request ready for review' },
  { eventType: 'issues', action: 'opened', label: 'Issue opened' },
  { eventType: 'issues', action: 'closed', label: 'Issue closed' },
  { eventType: 'issues', action: 'deleted', label: 'Issue deleted' },
  { eventType: 'issues', action: 'edited', label: 'Issue edited' },
  { eventType: 'issues', action: 'assigned', label: 'Issue assigned' },
  { eventType: 'issues', action: 'milestoned', label: 'Issue added to milestone' },
];
export const webhookConfig = (automation: Automation): AutomationWebhook => automation.webhook ?? webhookTriggers[0];

export function triggerSummary(automation: Automation) {
  if (automation.mode === 'webhook') {
    const trigger = webhookConfig(automation);
    return webhookTriggers.find(item => item.eventType === trigger.eventType && item.action === trigger.action)?.label || 'Unknown webhook event';
  }
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
