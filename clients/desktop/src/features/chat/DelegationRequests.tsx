import { useEffect, useState } from 'react';
import { Button } from '../../design-system/Button';
import { request, type DelegationRequest, type PermissionRequest, type QuestionRequest, type Session, type SessionResponse } from '../../engine';
import { PermissionCard } from './PermissionCard';
import { QuestionCard } from './QuestionCard';
import { subscribeScope } from './scopedConnection';

type Page = { items: DelegationRequest[]; hasMore: boolean; nextAfterId: string };
export function DelegationRequests({ session, projectName, onSnapshot }: { session: Session; projectName: string; onSnapshot: (value: SessionResponse) => void }) {
  const [page, setPage] = useState<Page>(); const [error, setError] = useState(''); const [after, setAfter] = useState(''); const [reload, setReload] = useState(0);
  useEffect(() => {
    const controller = new AbortController(); let busy = false, again = false; let timer: ReturnType<typeof setTimeout> | undefined;
    async function refresh() {
      if (busy) { again = true; return; } if (controller.signal.aborted || document.hidden) return; busy = true;
      try { const result = await request<Page>(`/api/sessions/${encodeURIComponent(session.id)}/delegation/requests?afterId=${encodeURIComponent(after)}`, 'GET', undefined, 15000, controller.signal); if (!controller.signal.aborted) { setPage(result); setError(''); } }
      catch (cause) { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not read child requests.'); }
      finally { busy = false; if (again && !controller.signal.aborted) { again = false; timer = setTimeout(() => { timer = undefined; void refresh(); }, 150); } }
    }
    const schedule = () => { if (!timer) timer = setTimeout(() => { timer = undefined; void refresh(); }, 150); };
    const unsubscribe = subscribeScope('conversation', session.id, schedule);
    const visible = () => { if (!document.hidden) void refresh(); }; document.addEventListener('visibilitychange', visible);
    void refresh(); return () => { controller.abort(); clearTimeout(timer); unsubscribe(); document.removeEventListener('visibilitychange', visible); };
  }, [session.id, after, reload]);
  const resolved = (value: SessionResponse) => { onSnapshot(value); setReload(value => value + 1); };
  return <>{!!page?.items.length && <div className="permission-queue" aria-label="Child requests">{page.items.map(item => item.kind === 'permission'
    ? <PermissionCard key={item.request.id} permission={item.request as PermissionRequest} sessionId={session.id} projectName={projectName} origin={`Subagent · ${item.childTitle || item.childSessionId.slice(0, 8)}`} onResolved={resolved} />
    : <QuestionCard key={item.request.id} question={item.request as QuestionRequest} sessionId={session.id} origin={`Subagent · ${item.childTitle || item.childSessionId.slice(0, 8)}`} onResolved={resolved} />)}</div>}
    {page?.hasMore && <Button size="sm" onClick={() => setAfter(page.nextAfterId)}>More child requests</Button>}{after && <Button size="sm" onClick={() => setAfter('')}>First child requests</Button>}
    {error && <p className="form-error" role="alert">{error}<Button size="sm" onClick={() => setReload(value => value + 1)}>Retry</Button></p>}
  </>;
}
