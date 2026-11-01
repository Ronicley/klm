import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Button } from '../../design-system/Button';
import { request, type EventPage, type Session } from '../../engine';

export function HistoryLoader({ session, onPage }: { session: Session; onPage: (page: EventPage) => void }) {
  const container = useRef<HTMLDivElement>(null);
  const anchor = useRef<{ element: HTMLElement; height: number; top: number } | null>(null);
  const loadingRef = useRef(false);
  const requestController = useRef<AbortController | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    loadingRef.current = false;
    setLoading(false); setError('');
    return () => {
      requestController.current?.abort();
      requestController.current = null;
      anchor.current = null;
    };
  }, [session.id]);
  useLayoutEffect(() => {
    if (!anchor.current) return;
    const { element, height, top } = anchor.current;
    element.scrollTop = top + element.scrollHeight - height;
    anchor.current = null;
  }, [session.events]);
  async function load() {
    if (loadingRef.current || !session.history?.nextCursor || !container.current?.getClientRects().length || document.visibilityState === 'hidden') return;
    const controller = new AbortController();
    requestController.current = controller;
    loadingRef.current = true;
    setLoading(true); setError('');
    try {
      const page = await request<EventPage>(`/api/sessions/${encodeURIComponent(session.id)}/history?before=${encodeURIComponent(session.history.nextCursor)}`, 'GET', undefined, 15000, controller.signal);
      if (controller.signal.aborted || requestController.current !== controller) return;
      const element = container.current?.closest<HTMLElement>('.chat-history');
      if (element) anchor.current = { element, height: element.scrollHeight, top: element.scrollTop };
      onPage(page);
    } catch (error) { if (!controller.signal.aborted) setError(error instanceof Error ? error.message : 'Could not load history.'); }
    finally {
      if (requestController.current === controller) { loadingRef.current = false; requestController.current = null; setLoading(false); }
    }
  }
  if (!session.history?.hasMore) return null;
  return <div ref={container}>
    <Button size="sm" disabled={loading} onClick={() => void load()}>{loading ? 'Loading...' : 'Load earlier messages'}</Button>
    {error && <p className="form-error" role="alert">{error}</p>}
  </div>;
}
