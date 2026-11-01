import { useEffect, useRef, useState, type ReactNode } from 'react';
import { request, type EventPage, type Session, type SessionResponse } from '../../engine';
import { Button } from '../../design-system/Button';
import { ConversationEvents } from './ConversationEvents';
import { HistoryLoader } from './HistoryLoader';
import { PendingRequestPages } from './PendingRequestPages';

export function ConversationHistory({ session, subagents, working, label, empty, onSnapshot, onHistory, onEventChange, onAskSide, onOpenSubagent }: {
  session: Session;
  subagents?: Session[];
  working: boolean;
  label: string;
  empty?: ReactNode;
  onSnapshot: (session: SessionResponse) => void;
  onHistory: (page: EventPage) => void;
  onEventChange?: (sessionId: string, event: import('../../engine').EngineEvent) => void;
  onAskSide?: (messageId: string, passage: string) => void;
  onOpenSubagent?: (sessionId: string) => void;
}) {
  const history = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const latestRequest = useRef<AbortController | null>(null);
  const [historyError, setHistoryError] = useState('');
  useEffect(() => () => latestRequest.current?.abort(), [session.id]);
  useEffect(() => {
    if (following.current && history.current) history.current.scrollTo({ top: history.current.scrollHeight });
  }, [session.updatedAt, session.history?.total, !!session.history, working]);
  return <div className="chat-history" ref={history} role="log" aria-label={label} aria-live="polite" onScroll={event => {
    const element = event.currentTarget;
    following.current = element.scrollHeight - element.scrollTop - element.clientHeight < 24;
  }}>
    <HistoryLoader key={`history-loader/${session.id}`} session={session} onPage={onHistory} />
    {!session.history && !session.events.length
      ? <p className="muted" role="status">Loading history...</p>
      : session.events.length || working
        ? <ConversationEvents key={`conversation-events/${session.id}`} session={session} subagents={subagents} working={working} onSnapshot={onSnapshot} onEventChange={onEventChange} onAskSide={onAskSide} onOpenSubagent={onOpenSubagent} />
         : empty}
    {session.history?.hasNewer && <Button size="sm" onClick={() => {
      latestRequest.current?.abort(); const controller = new AbortController(); latestRequest.current = controller; setHistoryError('');
      void request<EventPage>(`/api/sessions/${encodeURIComponent(session.id)}/history`, 'GET', undefined, 15000, controller.signal).then(page => {
        if (controller.signal.aborted) return;
        following.current = true; onSnapshot({ kind: 'reset', summary: session, revision: page.revision, history: page, changes: [], total: page.total });
        requestAnimationFrame(() => history.current?.scrollTo({ top: history.current.scrollHeight }));
      }).catch(error => { if (!controller.signal.aborted) setHistoryError(error instanceof Error ? error.message : 'Could not load latest messages.'); });
    }}>Latest messages</Button>}
    {historyError && <p className="form-error" role="alert">{historyError}</p>}
    {session.role !== 'subagent' && <PendingRequestPages key={`pending/${session.id}`} session={session} onSnapshot={onSnapshot} />}
  </div>;
}
