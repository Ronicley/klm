import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { AutoPage } from '../pagination/AutoPage';
import { captureScrollAnchor } from '../pagination/scrollAnchor';
import { Panel } from '@xyflow/react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { TabNav } from '../../design-system/TabNav';
import { SearchSelect } from '../../design-system/SearchSelect';
import { request, type GraphNodeActivity } from '../../engine';
import { EventSequence } from '../chat/ConversationEvents';
import { DelegationActivity } from '../chat/DelegationActivity';
import { SessionEvent } from '../chat/SessionEvent';

const statusLabels: Record<string, string> = { reserved: 'Pending', running: 'Running', waiting_user: 'Waiting', sealing: 'Finishing', completed: 'Completed', failed: 'Failed', interrupted: 'Interrupted' };

export function GraphRunActivity({ sessionId, runId, nodeId, nodeName, onClose }: {
  sessionId: string; runId: string; nodeId: string; nodeName: string; onClose: () => void;
}) {
  const [tab, setTab] = useState<'run' | 'input' | 'output'>('run');
  const [activationId, setActivationId] = useState('');
  const [activationOffset, setActivationOffset] = useState(0);
  const [eventOffset, setEventOffset] = useState<number>();
  const [data, setData] = useState<GraphNodeActivity | null>(null);
  const [error, setError] = useState('');
  const log = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const snapshot = useRef<GraphNodeActivity | null>(null);
  const restore = useRef<(() => void) | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const identity = `${sessionId}/${runId}/${nodeId}/${activationId}`;
  const previousIdentity = useRef(identity);
  const direction = useRef<'before' | 'after'>('before');
  const [navigation, setNavigation] = useState<'before' | 'after'>('before');
  const [gesture, setGesture] = useState(0);
  const explicit = useRef(false);
  const lastGesture = useRef(0);
  const touchY = useRef(0);
  function navigate(next: 'before' | 'after') {
    direction.current = next; setNavigation(next);
    if (Date.now() - lastGesture.current > 350) { lastGesture.current = Date.now(); explicit.current = true; setGesture(value => value + 1); }
  }
  function pageEvents(offset: number | undefined) {
    if ((snapshot.current?.activation?.events.length ?? 0) >= 200 && log.current && log.current.scrollHeight <= log.current.clientHeight + 1 && !explicit.current) return;
    explicit.current = false; following.current = false; setEventOffset(offset);
  }
  const consumedOffset = useRef<number | undefined>(undefined);
  useEffect(() => {
    const controller = new AbortController();
    let timer: number | undefined;
    if (previousIdentity.current !== identity) { snapshot.current = null; setData(null); following.current = true; previousIdentity.current = identity; }
    setError('');
    async function refresh() {
      let complete = false;
      setBusy(true);
      try {
        const previous = snapshot.current;
        const old = previous?.activation;
        const start = consumedOffset.current !== eventOffset ? eventOffset : old && (!following.current || old.hasMore) ? old.offset : undefined;
        const query = new URLSearchParams({ limit: '25', eventLimit: '25', offset: String(activationOffset), ...(activationId ? { activationId } : {}), ...(start === undefined ? {} : { eventOffset: String(start) }) });
        const result = await request<GraphNodeActivity>(`/api/sessions/${encodeURIComponent(sessionId)}/graph/runs/${encodeURIComponent(runId)}/nodes/${encodeURIComponent(nodeId)}/activity?${query}`, 'GET', undefined, 15000, controller.signal);
        complete = !result.runActive;
        result.activations = [...new Map([...(previous?.activations ?? []), ...result.activations].map(item => [item.id, item])).values()];
        if (old && result.activation?.id === old.id) {
          const next = result.activation;
          const from = next.offset ?? 0; let end = next.nextOffset ?? from + next.events.length;
          const oldStart = old.offset ?? 0; const oldEnd = old.nextOffset ?? oldStart + old.events.length;
          if (from <= oldEnd && end >= oldStart) {
            // Revalidate the retained range in pages, never turn two disjoint
            // responses into an apparently contiguous timeline.
            const events = new Map(old.events.map((item, index) => [oldStart + index, item]));
            next.events.forEach((item, index) => events.set(from + index, item));
            while (end < oldEnd && next.hasMore) {
              query.set('eventOffset', String(end));
              const page = await request<GraphNodeActivity>(`/api/sessions/${encodeURIComponent(sessionId)}/graph/runs/${encodeURIComponent(runId)}/nodes/${encodeURIComponent(nodeId)}/activity?${query}`, 'GET', undefined, 15000, controller.signal);
              const part = page.activation;
              if (!part || part.id !== next.id || (part.nextOffset ?? 0) <= end) throw new Error('Graph activity changed. Retry.');
              part.events.forEach((item, index) => events.set(end + index, item)); end = part.nextOffset!;
            }
            let first = Math.min(from, oldStart); let last = Math.max(end, oldEnd);
            if (from < oldStart) last = Math.min(last, first + 200); else first = Math.max(first, last - 200);
            next.events = Array.from({ length: last - first }, (_, index) => events.get(first + index)).filter((item): item is NonNullable<typeof item> => !!item);
            next.offset = first; next.nextOffset = last; next.previousOffset = Math.max(0, first - 25); next.hasEarlier = first > 0; next.hasMore = last < (next.total ?? last);
          }
        }
        if (!controller.signal.aborted) {
          if (log.current && !following.current) restore.current = captureScrollAnchor(log.current);
          snapshot.current = result; setData(result); setError('');
          consumedOffset.current = eventOffset;
        }
      } catch (cause) {
        complete = true;
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load node activity.');
      } finally {
        if (!controller.signal.aborted) setBusy(false);
        if (!controller.signal.aborted && !complete) timer = window.setTimeout(() => void refresh(), 1000);
      }
    }
    void refresh();
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [sessionId, runId, nodeId, activationId, activationOffset, eventOffset, attempt]);
  const activity = data?.activation;
  const active = !!activity && ['reserved', 'running', 'waiting_user', 'sealing'].includes(activity.status);
  const status = activity?.status === 'failed' || activity?.status === 'interrupted' ? 'error' : active ? 'running' : 'idle';
  useLayoutEffect(() => {
    restore.current?.(); restore.current = undefined;
    if (tab === 'run' && following.current && !activity?.hasMore && log.current) log.current.scrollTop = log.current.scrollHeight;
  }, [tab, activity]);

  return <Panel position="bottom-center" className="graph-run-activity-position nodrag nopan nowheel">
    <section className="graph-run-activity" aria-label={`${nodeName} activity`}>
      <header className="graph-run-activity-heading">
        <strong className="graph-run-activity-title" title={nodeName}>{nodeName}</strong>
        <TabNav value={tab} onChange={setTab} items={[{ value: 'run', label: 'Run' }, { value: 'input', label: 'Input' }, { value: 'output', label: 'Output' }]} />
        <span className={`graph-run-status graph-run-status--${active ? 'running' : activity?.status ?? 'pending'}`}>{statusLabels[activity?.status ?? 'reserved'] ?? activity?.status}</span>
        <IconButton label="Close node activity" onClick={onClose}><X /></IconButton>
      </header>
      {!!data && data.activations.length > 0 && <div className="graph-run-activation-picker">Activation <SearchSelect label="Activation" value={activationId} onChange={value => { setActivationId(value); setEventOffset(undefined); }} options={[{ value: '', label: 'Latest' }, ...data.activations.map(item => ({ value: item.id, label: `#${item.occurrence} · ${statusLabels[item.status] ?? item.status}` }))]} footer={<AutoPage hasMore={!!data.hasMore} cursor={`${identity}/${data.nextOffset}`} busy={busy} error={error} onLoad={() => setActivationOffset(data.nextOffset ?? activationOffset + 25)} onRetry={() => setAttempt(value => value + 1)} />} /></div>}
      {error && <p role="alert" className="form-error graph-run-notice">{error}<Button size="sm" onClick={() => setAttempt(value => value + 1)}>Retry</Button></p>}
      {!data ? <p className="muted graph-run-notice">{error ? 'Retrying...' : 'Loading activity...'}</p> : !activity ? <p className="muted graph-run-notice">Not started.</p> : tab === 'run' ? <div ref={log} className="graph-run-timeline" role="log" aria-label="Node events" tabIndex={0}
        onWheel={event => navigate(event.deltaY < 0 ? 'before' : 'after')}
        onKeyDown={event => { if (['ArrowUp', 'PageUp', 'Home'].includes(event.key)) navigate('before'); else if (['ArrowDown', 'PageDown', 'End'].includes(event.key)) navigate('after'); }}
        onTouchStart={event => { touchY.current = event.touches[0]?.clientY ?? 0; }} onTouchMove={event => { const y = event.touches[0]?.clientY ?? touchY.current; navigate(y > touchY.current ? 'before' : 'after'); touchY.current = y; }}
        onScroll={event => { const element = event.currentTarget; following.current = !activity.hasMore && element.scrollHeight - element.scrollTop - element.clientHeight < 24; }}>
        <AutoPage hasMore={!!activity.hasEarlier && (activity.events.length < 200 || navigation === 'before')} cursor={`${identity}/before/${activity.offset}/${gesture}`} busy={busy} error={error} onLoad={() => pageEvents(activity.previousOffset)} onRetry={() => setAttempt(value => value + 1)} />
        <div className="conversation-events"><EventSequence events={activity.events} status={status} updatedAt={activity.updatedAt} showActiveWork={active} activeWorkStartedAt={activity.createdAt} renderEvent={event => event.type === 'subagent' && event.data?.managed ? event.data.kind === 'batch' ? <DelegationActivity ownerId={sessionId} batchId={String(event.data.batchId)} basePath={`/api/sessions/${encodeURIComponent(sessionId)}/graph/runs/${encodeURIComponent(runId)}/activations/${encodeURIComponent(activity.id)}/delegation`} revision={String(event.data.revision ?? '')} /> : null : <SessionEvent event={event} />} /></div>
        <AutoPage hasMore={!!activity.hasMore && (activity.events.length < 200 || navigation === 'after')} cursor={`${identity}/after/${activity.nextOffset}/${gesture}`} busy={busy} error={error} onLoad={() => pageEvents(activity.nextOffset)} onRetry={() => setAttempt(value => value + 1)} />
        {activity.error && <p className="form-error">{activity.error}</p>}
      </div> : <div className="graph-run-payload" role="region" aria-label={tab === 'input' ? 'Input payload' : 'Output payload'} tabIndex={0}>
        {tab === 'input' ? <>
          <details><summary>Captured Library</summary>{data.capabilities ? <><p>{data.capabilities.selection.skills.length} skills · {data.capabilities.selection.mcpConnections.length} MCP connections</p>{data.capabilities.skills.map(skill => <p key={skill.id}>{skill.name} · {skill.revision.slice(0, 12)}</p>)}{data.capabilities.connections?.map(c => <p key={c.id}>{c.name} · {c.revision.slice(0, 12)} · {c.selection.tools.join(', ') || 'No tools selected'}</p>)}</> : <p className="muted">Managed inventory was not captured for this run.</p>}</details>
          {activity.input?.task !== undefined && <pre>{activity.input.task}</pre>}
          {Object.entries(activity.input ?? {}).filter(([key]) => key !== 'task').map(([key, value]) => <div key={key}><strong>{key}</strong><pre>{value}</pre></div>)}
        </> : activity.submission ? <>
          <div className="graph-run-output-choice"><span>Choice</span><code>{activity.submission.choice.id}</code><span>{activity.submission.acceptedAt ? 'Accepted' : 'Pending acceptance'}</span></div>
          <pre>{JSON.stringify(activity.submission.payload, null, 2)}</pre>
        </> : <p className="muted">No Choice submitted.</p>}
      </div>}
    </section>
  </Panel>;
}
