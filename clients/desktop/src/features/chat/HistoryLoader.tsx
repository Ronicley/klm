import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { request, type EventPage, type Session } from '../../engine';
import { AutoPage } from '../pagination/AutoPage';
import { PAGE_SIZE, serializeHistory } from '../pagination/pagination';
import { captureScrollAnchor } from '../pagination/scrollAnchor';

const requests = new Map<string, Promise<EventPage>>();
export function HistoryLoader({ session, onPage, direction = 'before' }: { session: Session; onPage: (page: EventPage) => void; direction?: 'before' | 'after' }) {
  const container = useRef<HTMLDivElement>(null);
  const restore = useRef<(() => void) | undefined>(undefined);
  const controller = useRef<AbortController | null>(null);
  const flight = useRef(false);
  const current = useRef(session); current.current = session;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [intent, setIntent] = useState('before');
  const [gesture, setGesture] = useState(0);
  const explicit = useRef(false);
  useEffect(() => {
    const root = container.current?.closest<HTMLElement>('.chat-history');
    let touch = 0; let lastGesture = 0;
    const intent = (event: Event) => {
      if (!root) return;
      if (event instanceof WheelEvent && event.deltaY) root.dataset.historyDirection = event.deltaY < 0 ? 'before' : 'after';
      if (event instanceof KeyboardEvent) root.dataset.historyDirection = ['ArrowUp', 'PageUp', 'Home'].includes(event.key) ? 'before' : 'after';
      if (typeof TouchEvent !== 'undefined' && event instanceof TouchEvent && event.touches[0]) { const y = event.touches[0].clientY; if (touch) root.dataset.historyDirection = y > touch ? 'before' : 'after'; touch = y; }
      setIntent(root.dataset.historyDirection ?? 'before');
      if (Date.now() - lastGesture > 350) { lastGesture = Date.now(); explicit.current = true; setGesture(value => value + 1); }
    };
    root?.addEventListener('wheel', intent, { passive: true }); root?.addEventListener('keydown', intent); root?.addEventListener('touchmove', intent, { passive: true });
    return () => { controller.current?.abort(); root?.removeEventListener('wheel', intent); root?.removeEventListener('keydown', intent); root?.removeEventListener('touchmove', intent); };
  }, [session.id]);
  useLayoutEffect(() => { restore.current?.(); restore.current = undefined; }, [session.events]);
  const history = session.history;
  const cursor = direction === 'before' ? history?.nextCursor : history?.afterCursor;
  const more = direction === 'before' ? history?.hasMore : history?.hasNewer;
  async function load() {
    const root = container.current?.closest<HTMLElement>('.chat-history');
    if (flight.current || !cursor || !root || !root.getClientRects().length) return;
    if (session.events.length >= 200 && (root.scrollHeight <= root.clientHeight + 1 && !explicit.current || root.dataset.historyDirection && root.dataset.historyDirection !== direction)) return;
    explicit.current = false;
    const abort = new AbortController(); controller.current = abort;
    flight.current = true; setBusy(true); setError('');
    try {
      const key = `${session.id}/${direction}/${cursor}`;
      let pending = requests.get(key);
      if (!pending) {
        pending = serializeHistory(session.id, () => request<EventPage>(`/api/sessions/${encodeURIComponent(session.id)}/history?${direction}=${encodeURIComponent(cursor!)}&limit=${PAGE_SIZE}`));
        requests.set(key, pending);
        void pending.finally(() => { if (requests.get(key) === pending) requests.delete(key); }).catch(() => {});
      }
      const page = await pending;
      if (abort.signal.aborted) return;
      const now = current.current.history;
      if (!now || (direction === 'before' ? now.nextCursor : now.afterCursor) !== cursor) return;
      if (!page.events?.length || (direction === 'before' ? page.startIndex >= now.startIndex : page.endIndex <= now.startIndex + current.current.events.length)) throw new Error('History did not advance. Retry.');
      restore.current = captureScrollAnchor(root);
      root.style.overflowAnchor = 'none'; root.dispatchEvent(new Event('klm:history-paging'));
      onPage({ ...page, direction });
    } catch (cause) { if (!abort.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load history.'); }
    finally { if (controller.current === abort && !abort.signal.aborted) { flight.current = false; setBusy(false); } }
  }
  return <div ref={container}><AutoPage hasMore={!!more && (session.events.length < 200 || intent === direction)} cursor={`${session.id}/${direction}/${cursor ?? ''}/${intent}/${gesture}`} busy={busy} error={error} onLoad={() => void load()} /></div>;
}
