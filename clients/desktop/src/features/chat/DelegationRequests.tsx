import { useEffect, useState } from 'react';
import { AutoPage } from '../pagination/AutoPage';
import { Button } from '../../design-system/Button';
import { request, type DelegationRequest, type PermissionRequest, type QuestionRequest, type Session, type SessionResponse } from '../../engine';
import { PermissionCard } from './PermissionCard';
import { QuestionCard } from './QuestionCard';
import { subscribeScope } from './scopedConnection';

type Page = { items: DelegationRequest[]; hasMore: boolean; nextAfterId: string };
export function DelegationRequests({ session, projectName, onSnapshot }: { session: Session; projectName: string; onSnapshot: (value: SessionResponse) => void }) {
  const [page, setPage] = useState<Page>(); const [error, setError] = useState(''); const [pages, setPages] = useState(1); const [reload, setReload] = useState(0); const [loading, setLoading] = useState(false);
  useEffect(() => {
    const controller = new AbortController(); let busy = false, again = false; let timer: ReturnType<typeof setTimeout> | undefined;
    async function refresh() {
      if (busy) { again = true; return; } if (controller.signal.aborted || document.hidden) return; busy = true;
      setLoading(true);
      try {
        let after = ''; let result: Page = { items: [], hasMore: false, nextAfterId: '' }; const items: DelegationRequest[] = [];
        for (let index = 0; index < pages; index++) {
          result = await request<Page>(`/api/sessions/${encodeURIComponent(session.id)}/delegation/requests?limit=25&afterId=${encodeURIComponent(after)}`, 'GET', undefined, 15000, controller.signal);
          if (result.hasMore && result.nextAfterId === after) throw new Error('Child request pagination did not advance. Retry.');
          items.push(...result.items); after = result.nextAfterId; if (!result.hasMore) break;
        }
        if (!controller.signal.aborted) { setPage({ ...result, items: [...new Map(items.map(item => [`${item.childSessionId}/${item.kind}/${item.request.id}`, item])).values()] }); setError(''); }
      }
      catch (cause) { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not read child requests.'); }
      finally { busy = false; if (!controller.signal.aborted) setLoading(false); if (again && !controller.signal.aborted) { again = false; timer = setTimeout(() => { timer = undefined; void refresh(); }, 150); } }
    }
    const schedule = () => { if (!timer) timer = setTimeout(() => { timer = undefined; void refresh(); }, 150); };
    const unsubscribe = subscribeScope('conversation', session.id, schedule);
    const visible = () => { if (!document.hidden) void refresh(); }; document.addEventListener('visibilitychange', visible);
    void refresh(); return () => { controller.abort(); clearTimeout(timer); unsubscribe(); document.removeEventListener('visibilitychange', visible); };
  }, [session.id, pages, reload]);
  const resolved = (value: SessionResponse) => { onSnapshot(value); setReload(value => value + 1); };
  return <>{!!page?.items.length && <div className="permission-queue" aria-label="Child requests">{page.items.map(item => item.kind === 'permission'
    ? <PermissionCard key={`${item.childSessionId}/${item.request.id}`} permission={item.request as PermissionRequest} sessionId={session.id} projectName={projectName} origin={`Subagent · ${item.childTitle || item.childSessionId.slice(0, 8)}`} onResolved={resolved} />
    : <QuestionCard key={`${item.childSessionId}/${item.request.id}`} question={item.request as QuestionRequest} sessionId={session.id} origin={`Subagent · ${item.childTitle || item.childSessionId.slice(0, 8)}`} onResolved={resolved} />)}</div>}
    <AutoPage hasMore={!!page?.hasMore} cursor={`${session.id}/${page?.nextAfterId}`} busy={loading} error={error} onLoad={() => setPages(value => value + 1)} onRetry={() => setReload(value => value + 1)} />
    {error && <p className="form-error" role="alert">{error}<Button size="sm" onClick={() => setReload(value => value + 1)}>Retry</Button></p>}
  </>;
}
