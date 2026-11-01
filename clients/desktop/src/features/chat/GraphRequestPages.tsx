import { useEffect, useRef, useState } from 'react';
import { Button } from '../../design-system/Button';
import { request, type ConversationGraphState, type Session } from '../../engine';
import { PermissionCard } from './PermissionCard';
import { QuestionCard } from './QuestionCard';

export function GraphRequestPages({ session, projectName, onResolved }: { session: Session; projectName: string; onResolved: () => void }) {
  const [page, setPage] = useState<ConversationGraphState>();
  const [busy, setBusy] = useState(false); const [error, setError] = useState('');
  const controller = useRef<AbortController | null>(null);
  useEffect(() => { setPage(undefined); setError(''); setBusy(false); return () => controller.current?.abort(); }, [session.id, session.graph?.run?.id]);
  async function load(first = false) {
    if (busy) return;
    const current = new AbortController(); controller.current = current; setBusy(true); setError('');
    const offset = first ? session.graph?.requestsNextOffset : page?.requestsNextOffset ?? session.graph?.requestsNextOffset;
    try {
      const next = await request<ConversationGraphState>(`/api/sessions/${encodeURIComponent(session.id)}/graph?offset=${offset ?? 0}`, 'GET', undefined, 15000, current.signal);
      if (!current.signal.aborted) { if (next.run?.id !== session.graph?.run?.id) throw new Error('Graph requests changed. Reload the conversation.'); setPage(next); }
    } catch (error) { if (!current.signal.aborted) setError(error instanceof Error ? error.message : 'Could not read graph requests.'); }
    finally { if (!current.signal.aborted) setBusy(false); }
  }
  const resolved = () => { setPage(undefined); onResolved(); };
  if (!session.graph?.requestsHasMore && !error) return null;
  return <div>{page?.requests.filter(item => !session.graph?.requests.some(base => base.requestId === item.requestId && base.sessionId === item.sessionId)).map(item => item.permission
    ? <PermissionCard key={`${item.sessionId}/${item.requestId}`} permission={{ ...item.permission, id: item.requestId }} sessionId={item.sessionId} projectName={projectName} origin={`Graph ${item.graphId} · ${item.nodeName}`} onResolved={resolved} />
    : item.question ? <QuestionCard key={`${item.sessionId}/${item.requestId}`} question={{ ...item.question, id: item.requestId }} sessionId={item.sessionId} origin={`Graph ${item.graphId} · ${item.nodeName}`} onResolved={resolved} /> : null)}
    {(page?.requestsHasMore ?? true) && <Button size="sm" disabled={busy} onClick={() => void load()}>More graph requests</Button>}
    {!!page && <Button size="sm" disabled={busy} onClick={() => void load(true)}>First additional graph requests</Button>}
    {error && <p role="alert" className="form-error">{error}</p>}</div>;
}
