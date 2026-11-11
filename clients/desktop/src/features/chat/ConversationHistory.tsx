import { useEffect, useLayoutEffect, useRef, type ReactNode } from 'react';
import { type EventPage, type Session, type SessionResponse } from '../../engine';
import { followHistory } from '../pagination/pagination';
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
  const viewer = useRef({});
  useLayoutEffect(() => { following.current = !session.history?.hasNewer; if (history.current) history.current.scrollTop = history.current.scrollHeight; }, [session.id]);
  useEffect(() => {
    const element = history.current;
    const paging = () => { following.current = false; followHistory(session.id, viewer.current, false); };
    element?.addEventListener('klm:history-paging', paging);
    return () => { element?.removeEventListener('klm:history-paging', paging); followHistory(session.id, viewer.current); };
  }, [session.id]);
  useLayoutEffect(() => {
    if (following.current && history.current) history.current.scrollTo({ top: history.current.scrollHeight });
  }, [session.updatedAt, session.history?.total, !!session.history, working]);
  return <div className="chat-history" ref={history} role="log" tabIndex={0} aria-label={label} aria-live="polite" style={{ overflowAnchor: 'none' }} onScroll={event => {
    const element = event.currentTarget;
    following.current = !session.history?.hasNewer && element.scrollHeight - element.scrollTop - element.clientHeight < 24;
    followHistory(session.id, viewer.current, following.current);
  }}>
    <HistoryLoader key={`history-loader/${session.id}`} session={session} onPage={onHistory} />
    {!session.history && !session.events.length
      ? <p className="muted" role="status">Loading history...</p>
      : session.events.length || working
        ? <ConversationEvents key={`conversation-events/${session.id}`} session={session} subagents={subagents} working={working} onSnapshot={onSnapshot} onEventChange={onEventChange} onAskSide={onAskSide} onOpenSubagent={onOpenSubagent} />
         : empty}
    <HistoryLoader key={`newer/${session.id}`} session={session} onPage={onHistory} direction="after" />
    {session.role !== 'subagent' && <PendingRequestPages key={`pending/${session.id}`} session={session} onSnapshot={onSnapshot} />}
  </div>;
}
