import { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { request, type DelegationMember, type DelegationMemberPage, type DelegationOutputPage, type Session } from '../../engine';
import { ConversationHistory } from './ConversationHistory';
import { mergeSessions, prependHistory } from './sessionState';
import { readConversationWindow, subscribeScope } from './scopedConnection';
import './delegation.css';

export const delegationLabel = (value: string) => ({ admitted: 'Pending', preparing: 'Preparing', running: 'Running', joining: 'Joining', draining: 'Finishing', completed: 'Completed', failed: 'Failed', cancelled: 'Cancelled', interrupted: 'Interrupted', finality_unconfirmed: 'Completion unconfirmed', pending: 'Pending', sending: 'Delivering', delivered: 'Delivered', uncertain: 'Delivery unconfirmed' }[value] ?? value);

export function DelegationActivity({ ownerId, batchId, basePath, revision }: { ownerId: string; batchId: string; basePath?: string; revision?: string }) {
  const base = basePath ?? `/api/sessions/${encodeURIComponent(ownerId)}/delegation`;
  const [page, setPage] = useState<DelegationMemberPage>();
  const [error, setError] = useState('');
  const [child, setChild] = useState<DelegationMember>();
  const [output, setOutput] = useState<DelegationMember>();
  const [reload, setReload] = useState(0);
  useEffect(() => {
    const controller = new AbortController(); let timer: ReturnType<typeof setTimeout> | undefined;
    let busy = false, again = false;
    async function refresh() {
      if (busy) { again = true; return; }
      if (controller.signal.aborted || document.hidden) return;
      busy = true;
      try {
        const result = await request<DelegationMemberPage>(`${base}/batches/${encodeURIComponent(batchId)}/members?limit=8`, 'GET', undefined, 15000, controller.signal);
        if (!controller.signal.aborted) { setPage(result); setError(''); }
      } catch (cause) { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not read subagents.'); }
      finally { busy = false; if (again && !controller.signal.aborted) { again = false; clearTimeout(timer); timer = setTimeout(() => { timer = undefined; void refresh(); }, 150); } }
    }
    const schedule = () => { if (!timer) timer = setTimeout(() => { timer = undefined; void refresh(); }, 150); };
    const unsubscribe = subscribeScope('conversation', ownerId, schedule);
    const visible = () => { if (!document.hidden) void refresh(); };
    document.addEventListener('visibilitychange', visible); void refresh();
    return () => { controller.abort(); clearTimeout(timer); unsubscribe(); document.removeEventListener('visibilitychange', visible); };
  }, [base, batchId, ownerId, revision, reload]);
  return <section className="delegation-activity" aria-label="Subagent batch">
    <header><strong>Subagents</strong>{page && <span>{page.batch.terminal}/{page.batch.total} finished · {page.batch.waiting ? 'Waiting · ' : ''}{delegationLabel(page.batch.outcome ?? page.batch.state)}</span>}</header>
    {page?.items.map(member => <div className="delegation-member" key={member.invocationId}>
      <div><strong>{member.title || member.agentName || 'Subagent'} · {member.key}</strong><small>{member.harness === 'opencode' ? 'OpenCode' : member.harness === 'pi' ? 'Pi' : 'Codex'} · {member.model || 'Default model'} · {member.effort || 'Default effort'}{member.reused ? ` · Reused #${member.invocationNumber}` : ''}</small><span>{delegationLabel(member.outcome || member.executionState)}</span>{member.error && <p className="form-error">{member.error}</p>}</div>
      <div className="delegation-member-actions"><Button size="sm" onClick={() => setChild(member)}>History</Button>{member.resultReference && <Button size="sm" onClick={() => setOutput(member)}>Output</Button>}</div>
    </div>)}
    {error && <p className="form-error" role="alert">{error}<Button size="sm" onClick={() => setReload(value => value + 1)}>Retry</Button></p>}
    {!page && !error && <p role="status">Loading subagents...</p>}
    {page && <small>{page.batch.waiting ? 'Parent waiting' : delegationLabel(page.batch.delivery)}</small>}
    {child && <DelegationHistory childId={child.childSessionId} title={child.title} onClose={() => setChild(undefined)} />}
    {output && <DelegationOutput base={base} member={output} onClose={() => setOutput(undefined)} />}
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
    {session ? <ConversationHistory session={session} working={session.status === 'running'} label="Child activity" onSnapshot={value => { const next = mergeSessions(current.current ? [current.current] : [], [value], true)[0]; current.current = next; setSession(next); }} onHistory={page => { if (current.current) { const next = prependHistory([current.current], page)[0]; current.current = next; setSession(next); } }} /> : <p role="status">{error || 'Loading history...'}</p>}
    {error && session && <p role="alert" className="form-error">{error}</p>}
  </dialog>;
}

function DelegationOutput({ base, member, onClose }: { base: string; member: DelegationMember; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [offset, setOffset] = useState('0'); const [page, setPage] = useState<DelegationOutputPage>(); const [error, setError] = useState('');
  useEffect(() => { dialog.current?.showModal(); }, []);
  useEffect(() => { const controller = new AbortController(); setPage(undefined); setError(''); void request<DelegationOutputPage>(`${base}/batches/${encodeURIComponent(member.batchId)}/invocations/${encodeURIComponent(member.invocationId)}/output?digest=${encodeURIComponent(member.resultReference!.digest)}&offset=${offset}`, 'GET', undefined, 15000, controller.signal).then(value => { if (!controller.signal.aborted) setPage(value); }).catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not read output.'); }); return () => controller.abort(); }, [base, member, offset]);
  return <dialog ref={dialog} className="settings-dialog delegation-history-dialog" aria-label="Child output" onKeyDown={event => { if (event.key === 'Escape') event.stopPropagation(); }} onCancel={event => { event.preventDefault(); event.stopPropagation(); onClose(); }}><header><h2>{member.title} · Output</h2><IconButton label="Close child output" onClick={onClose}><X /></IconButton></header>
    <pre className="delegation-output">{page?.content || error || (page ? 'No output.' : 'Loading output...')}</pre>
    <footer>{offset !== '0' && <Button size="sm" onClick={() => setOffset('0')}>First page</Button>}{page?.hasMore && <Button size="sm" onClick={() => setOffset(page.nextOffset)}>Next output page</Button>}{page && <span>{page.offset}–{page.nextOffset} / {page.totalBytes} bytes</span>}</footer>
  </dialog>;
}
