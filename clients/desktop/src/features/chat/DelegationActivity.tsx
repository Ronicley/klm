import { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { request, type DelegationMember, type DelegationMemberPage, type Session } from '../../engine';
import { SubagentView } from './SubagentView';
import { mergeSessions, prependHistory } from './sessionState';
import { readConversationWindow, subscribeScope } from './scopedConnection';
import { SubagentActivity } from './SubagentActivity';
import './delegation.css';

export const delegationLabel = (value: string) => ({ admitted: 'Pending', preparing: 'Preparing', running: 'Running', joining: 'Joining', draining: 'Finishing', completed: 'Completed', failed: 'Failed', cancelled: 'Cancelled', interrupted: 'Interrupted', finality_unconfirmed: 'Completion unconfirmed', pending: 'Pending', sending: 'Delivering', delivered: 'Delivered', uncertain: 'Delivery unconfirmed' }[value] ?? value);

export function DelegationActivity({ ownerId, batchId, basePath, revision, onOpenSubagent }: { ownerId: string; batchId: string; basePath?: string; revision?: string; onOpenSubagent?: (sessionId: string) => void }) {
  const base = basePath ?? `/api/sessions/${encodeURIComponent(ownerId)}/delegation`;
  const [page, setPage] = useState<DelegationMemberPage>();
  const [error, setError] = useState('');
  const [child, setChild] = useState<DelegationMember>();
  const [reload, setReload] = useState(0);
  useEffect(() => {
    const controller = new AbortController(); let timer: ReturnType<typeof setTimeout> | undefined;
    const children = new Map<string, () => void>();
    let busy = false, again = false;
    async function refresh() {
      if (busy) { again = true; return; }
      if (controller.signal.aborted || document.hidden) return;
      busy = true;
      try {
        const result = await request<DelegationMemberPage>(`${base}/batches/${encodeURIComponent(batchId)}/members?limit=8`, 'GET', undefined, 15000, controller.signal);
        if (!controller.signal.aborted) {
          setPage(result); setError('');
          const activeIds = new Set(result.items.filter(member => !member.terminal).map(member => member.childSessionId));
          for (const [id, close] of children) if (!activeIds.has(id)) { close(); children.delete(id); }
          for (const id of activeIds) if (!children.has(id)) children.set(id, subscribeScope('conversation', id, schedule));
        }
      } catch (cause) { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not read subagents.'); }
      finally { busy = false; if (again && !controller.signal.aborted) { again = false; clearTimeout(timer); timer = setTimeout(() => { timer = undefined; void refresh(); }, 150); } }
    }
    const schedule = () => { if (!timer) timer = setTimeout(() => { timer = undefined; void refresh(); }, 150); };
    const unsubscribe = subscribeScope('conversation', ownerId, schedule);
    const visible = () => { if (!document.hidden) void refresh(); };
    document.addEventListener('visibilitychange', visible); void refresh();
    return () => { controller.abort(); clearTimeout(timer); unsubscribe(); children.forEach(close => close()); document.removeEventListener('visibilitychange', visible); };
  }, [base, batchId, ownerId, revision, reload]);
  return <section className="delegation-activity" aria-label="Subagents">
    {page?.items.map(member => <div key={member.invocationId}>
      <SubagentActivity title={member.title || 'Subagent'} agentName={member.agentName} running={!member.terminal}
        detail={member.terminal ? delegationLabel(member.outcome || member.executionState) : member.latestActivity || delegationLabel(member.executionState)}
        onOpen={() => onOpenSubagent ? onOpenSubagent(member.childSessionId) : setChild(member)} />
      {member.error && <p className="form-error">{member.error}</p>}
    </div>)}
    {error && <p className="form-error" role="alert">{error}<Button size="sm" onClick={() => setReload(value => value + 1)}>Retry</Button></p>}
    {!page && !error && <p role="status">Loading subagents...</p>}
    {child && <DelegationHistory childId={child.childSessionId} title={child.title} onClose={() => setChild(undefined)} />}
  </section>;
}

function DelegationHistory({ childId, title, onClose }: { childId: string; title: string; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [session, setSession] = useState<Session>();
  const current = useRef<Session | undefined>(undefined);
  const [error, setError] = useState('');
  useEffect(() => { dialog.current?.showModal(); }, []);
  useEffect(() => {
    const controller = new AbortController(); let busy = false, again = false; let timer: ReturnType<typeof setTimeout> | undefined;
    async function refresh() {
      if (busy) { again = true; return; } if (document.hidden || controller.signal.aborted) return; busy = true;
      try { const result = await readConversationWindow(childId, current.current, controller.signal); if (!controller.signal.aborted) { const next = mergeSessions(current.current ? [current.current] : [], [result], true)[0]; current.current = next; setSession(next); setError(''); } }
      catch (cause) { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not read child history.'); }
      finally { busy = false; if (again && !controller.signal.aborted) { again = false; timer = setTimeout(() => { timer = undefined; void refresh(); }, 150); } }
    }
    const schedule = () => { if (!timer) timer = setTimeout(() => { timer = undefined; void refresh(); }, 150); };
    const unsubscribe = subscribeScope('conversation', childId, schedule);
    const visible = () => { if (!document.hidden) schedule(); }; document.addEventListener('visibilitychange', visible);
    void refresh(); return () => { controller.abort(); unsubscribe(); clearTimeout(timer); document.removeEventListener('visibilitychange', visible); };
  }, [childId]);
  return <dialog ref={dialog} className="settings-dialog delegation-history-dialog" aria-label={`${title} history`} onKeyDown={event => { if (event.key === 'Escape') event.stopPropagation(); }} onCancel={event => { event.preventDefault(); event.stopPropagation(); onClose(); }}>
    <header><h2>{session?.title || title || 'Subagent'}</h2><IconButton label="Close child history" onClick={onClose}><X /></IconButton></header>
    {session ? <SubagentView session={session} onSnapshot={value => { const next = mergeSessions(current.current ? [current.current] : [], [value], true)[0]; current.current = next; setSession(next); }} onHistory={page => { if (current.current) { const next = prependHistory([current.current], page)[0]; current.current = next; setSession(next); } }} /> : <p role="status">{error || 'Loading history...'}</p>}
    {error && session && <p role="alert" className="form-error">{error}</p>}
  </dialog>;
}
