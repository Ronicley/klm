import type { Metric } from './analyticsApi';

export const weekdays = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
export const compact = (n: number) => new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 }).format(n);
export const hours = (ms: number) => `${(ms / 3600000).toLocaleString('en', { maximumFractionDigits: 1 })}h`;
export const dateLabel = (date: string) => new Date(`${date.slice(0, 10)}T12:00:00Z`).toLocaleDateString('en', { month: 'short', day: 'numeric', timeZone: 'UTC' });
export function metricText(metric: Metric | undefined, format: (n: number) => string = compact) { return metric?.value == null || metric.coverage === 'not_applicable' ? '—' : format(metric.value); }
const unavailableReasons: Record<string, string> = {
  before_collection: 'Earlier than the collection boundary.', usage_incomplete: 'A starting or final usage sample is missing.',
  overflow: 'The reported count exceeds the supported exact integer range.',
  native_children_unsupported: 'Native child accounting is unavailable on this adapter path.',
  managed_delegation_disabled: 'Native delegation is disabled on this managed path.',
  component_unavailable: 'A token component is unavailable.',
  no_timed_runs: 'No in-period average is available.', future: 'Upcoming.',
};
export function coverageTitle(metric: Metric) {
  // Coverage still gates comparisons internally. Available measurements display
  // without coverage explanations, including hover and screen-reader text.
  const unavailable = metric.value === null || metric.coverage === 'unavailable';
  const notApplicable = metric.coverage === 'not_applicable';
  return [notApplicable ? 'Not applicable.' : unavailable ? 'Unavailable.' : '', metric.provisional ? 'Active observation; provisional.' : '', ...(unavailable || notApplicable ? (metric.reasons ?? []).map(reason => unavailableReasons[reason] ?? '') : [])].filter(Boolean).join(' ');
}
