import { useEffect, useRef, useState } from 'react';
import { Panel } from '@xyflow/react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { TabNav } from '../../design-system/TabNav';
import { Select } from '../../design-system/Select';
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
  useEffect(() => {
    const controller = new AbortController();
    let timer: number | undefined;
    setData(null); setError(''); following.current = true;
    async function refresh() {
      let complete = false;
      try {
        const query = new URLSearchParams({ offset: String(activationOffset), ...(activationId ? { activationId } : {}), ...(eventOffset === undefined ? {} : { eventOffset: String(eventOffset) }) });
        const result = await request<GraphNodeActivity>(`/api/sessions/${encodeURIComponent(sessionId)}/graph/runs/${encodeURIComponent(runId)}/nodes/${encodeURIComponent(nodeId)}/activity?${query}`, 'GET', undefined, 15000, controller.signal);
        complete = !result.runActive;
        if (!controller.signal.aborted) { setData(result); setError(''); }
      } catch (cause) {
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load node activity.');
      } finally {
        if (!controller.signal.aborted && !complete) timer = window.setTimeout(() => void refresh(), 1000);
      }
    }
    void refresh();
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [sessionId, runId, nodeId, activationId, activationOffset, eventOffset]);
  const activity = data?.activation;
  const active = !!activity && ['reserved', 'running', 'waiting_user', 'sealing'].includes(activity.status);
  const status = activity?.status === 'failed' || activity?.status === 'interrupted' ? 'error' : active ? 'running' : 'idle';
  useEffect(() => {
    if (tab === 'run' && following.current && log.current) log.current.scrollTop = log.current.scrollHeight;
  }, [tab, activity]);

  return <Panel position="bottom-center" className="graph-run-activity-position nodrag nopan nowheel">
    <section className="graph-run-activity" aria-label={`${nodeName} activity`}>
      <header className="graph-run-activity-heading">
        <strong className="graph-run-activity-title" title={nodeName}>{nodeName}</strong>
        <TabNav value={tab} onChange={setTab} items={[{ value: 'run', label: 'Run' }, { value: 'input', label: 'Input' }, { value: 'output', label: 'Output' }]} />
        <span className={`graph-run-status graph-run-status--${active ? 'running' : activity?.status ?? 'pending'}`}>{statusLabels[activity?.status ?? 'reserved'] ?? activity?.status}</span>
        <IconButton label="Close node activity" onClick={onClose}><X /></IconButton>
      </header>
      {!!data && data.activations.length > 0 && <label className="graph-run-activation-picker">Activation <Select label="Activation" value={activationId} onChange={event => { setActivationId(event.target.value); setEventOffset(undefined); }}>
        <option value="">Latest</option>
        {activationId && activity && !data.activations.some(item => item.id === activationId) && <option value={activationId}>#{activity.occurrence} · {statusLabels[activity.status] ?? activity.status}</option>}
        {data.activations.map(item => <option key={item.id} value={item.id}>#{item.occurrence} · {statusLabels[item.status] ?? item.status}</option>)}
      </Select></label>}
      {data && <div>{activationOffset > 0 && <Button size="sm" onClick={() => setActivationOffset(Math.max(0, activationOffset - 50))}>Previous activations</Button>}{data.hasMore && <Button size="sm" onClick={() => setActivationOffset(data.nextOffset ?? activationOffset + 50)}>Next activations</Button>}</div>}
      {error && <p role="alert" className="form-error graph-run-notice">{error}</p>}
      {!data ? <p className="muted graph-run-notice">{error ? 'Retrying...' : 'Loading activity...'}</p> : !activity ? <p className="muted graph-run-notice">Not started.</p> : tab === 'run' ? <div ref={log} className="graph-run-timeline" role="log" aria-label="Node events" tabIndex={0}
        onScroll={event => { const element = event.currentTarget; following.current = element.scrollHeight - element.scrollTop - element.clientHeight < 24; }}>
        {activity.hasEarlier && <Button size="sm" onClick={() => setEventOffset(activity.previousOffset)}>Earlier activity</Button>}
        <div className="conversation-events"><EventSequence events={activity.events} status={status} updatedAt={activity.updatedAt} showActiveWork={active} activeWorkStartedAt={activity.createdAt} renderEvent={event => event.type === 'subagent' && event.data?.managed ? event.data.kind === 'batch' ? <DelegationActivity ownerId={sessionId} batchId={String(event.data.batchId)} basePath={`/api/sessions/${encodeURIComponent(sessionId)}/graph/runs/${encodeURIComponent(runId)}/activations/${encodeURIComponent(activity.id)}/delegation`} revision={String(event.data.revision ?? '')} /> : null : <SessionEvent event={event} />} /></div>
        {activity.hasMore && <Button size="sm" onClick={() => setEventOffset(activity.nextOffset)}>Later activity</Button>}{eventOffset !== undefined && <Button size="sm" onClick={() => setEventOffset(undefined)}>Latest activity</Button>}
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
