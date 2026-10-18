import type { Session } from '../../engine';

const absoluteDate = new Intl.DateTimeFormat('en-US', { month: 'long', day: 'numeric', year: 'numeric' });
const exactDate = new Intl.DateTimeFormat('en-US', { dateStyle: 'full', timeStyle: 'short' });

function calendarDay(date: Date) {
  return Date.UTC(date.getFullYear(), date.getMonth(), date.getDate());
}

export function lastMessageLabel(timestamp: string, now: number) {
  const date = new Date(timestamp);
  if (!Number.isFinite(date.getTime())) return 'No messages yet';
  const minutes = Math.floor(Math.max(0, now - date.getTime()) / 60000);
  const days = Math.round((calendarDay(new Date(now)) - calendarDay(date)) / 86400000);
  if (days <= 0) {
    if (minutes < 1) return 'Just now';
    if (minutes < 60) return `${minutes} min ago`;
    const hours = Math.floor(minutes / 60);
    return `${hours} ${hours === 1 ? 'hour' : 'hours'} ago`;
  }
  if (days === 1) return 'Yesterday';
  if (days < 30) return `${days} days ago`;
  return absoluteDate.format(date);
}

function lastMessageTimestamp(session: Session) {
  if (session.lastMessageAt) return session.lastMessageAt;
  // Full-session mutation responses can precede the next inventory summary.
  for (let i = session.events.length - 1; i >= 0; i--) {
    const event = session.events[i];
    if (!event.consultationId && event.createdAt && (event.type === 'user' || event.type === 'assistant' && event.text.trim())) return event.createdAt;
  }
  return '';
}

export function MobileSessionSubtitle({ session, now, archived }: { session: Session; now: number; archived: boolean }) {
  const runtime = session.status === 'error' ? { state: 'error', label: 'Failed' }
    : session.permissions?.length || session.questions?.length ? { state: 'waiting', label: 'Needs attention' }
    : session.status === 'running' ? { state: 'running', label: 'Working' }
    : session.runtimeActive ? { state: 'active', label: 'Idle' } : undefined;
  if (runtime && !archived) return <span className="mobile-session-subtitle">
    <i className={`session-runtime-led is-${runtime.state}`} aria-hidden="true" />
    <span>{runtime.label}</span>
  </span>;

  const timestamp = lastMessageTimestamp(session);
  const valid = Number.isFinite(Date.parse(timestamp));
  return <span className="mobile-session-subtitle">{valid
    ? <time dateTime={timestamp} title={exactDate.format(new Date(timestamp))}>{lastMessageLabel(timestamp, now)}</time>
    : <span>No messages yet</span>}
  </span>;
}
