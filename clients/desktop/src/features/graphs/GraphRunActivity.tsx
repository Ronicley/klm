import { useEffect, useRef, useState } from 'react';
import { Panel } from '@xyflow/react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { PlanReader } from './PlanReader';
import { TabNav } from '../../design-system/TabNav';
import { Select } from '../../design-system/Select';
import { request, type GraphNodeActivity } from '../../engine';
import { EventSequence } from '../chat/ConversationEvents';

const statusLabels: Record<string, string> = { reserved: 'Pending', running: 'Running', waiting_user: 'Waiting', sealing: 'Finishing', completed: 'Completed', failed: 'Failed', interrupted: 'Interrupted' };

export function GraphRunActivity({ sessionId, runId, nodeId, nodeName, onClose, hasRequests, onRequestTarget, retryPending, onRetry }: {
  sessionId: string; runId: string; nodeId: string; nodeName: string; onClose: () => void;
  hasRequests: boolean; onRequestTarget: (target: HTMLDivElement | null) => void;
  retryPending?: boolean; onRetry?: (activationId: string) => void;
}) {
  const [tab, setTab] = useState<'run' | 'input' | 'output'>('run');
  const [activationId, setActivationId] = useState('');
  const [viewRunId, setViewRunId] = useState(runId);
  const [reading, setReading] = useState<{ title: string; text: string } | null>(null);
  function readField(field: string, text: string) { setReading({ title: `${nodeName} · ${field}`, text }); }
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
        const result = await request<GraphNodeActivity>(`/api/sessions/${encodeURIComponent(sessionId)}/graph/runs/${encodeURIComponent(viewRunId)}/nodes/${encodeURIComponent(nodeId)}/activity${activationId ? `?activationId=${encodeURIComponent(activationId)}` : ''}`, 'GET', undefined, 15000, controller.signal);
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
  }, [sessionId, viewRunId, nodeId, activationId]);
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
      {!!data && data.activations.length > 1 && <label className="graph-run-activation-picker">Activation <Select label="Activation" value={activationId} onChange={event => setActivationId(event.target.value)}>
        <option value="">Latest</option>
        {data.activations.map(item => <option key={item.id} value={item.id}>#{item.occurrence} · {statusLabels[item.status] ?? item.status}</option>)}
      </Select></label>}
      {(data?.previousAttempt || viewRunId !== runId) && <div className="graph-run-retry">
        {viewRunId !== runId && <><span className="small muted">Earlier attempt</span><Button size="sm" variant="ghost" onClick={() => { setViewRunId(runId); setActivationId(''); }}>Current attempt</Button></>}
        {data?.previousAttempt && <Button size="sm" variant="ghost" onClick={() => { setViewRunId(data.previousAttempt!.runId); setActivationId(data.previousAttempt!.activationId); }}>Previous attempt</Button>}
      </div>}
      {error && <p role="alert" className="form-error graph-run-notice">{error}</p>}
      {activity && viewRunId === runId && !data?.runActive && onRetry && <div className="graph-run-retry"><Button size="sm" disabled={retryPending || !!activity.retryReason} onClick={() => onRetry(activity.id)}>Retry from here</Button><span className="small muted">{activity.retryReason || `Repeat activation #${activity.occurrence} with its recorded input and current files.`}</span></div>}
      {!data ? <p className="muted graph-run-notice">{error ? 'Retrying...' : 'Loading activity...'}</p> : !activity ? <p className="muted graph-run-notice">Not started.</p> : tab === 'run' ? <div ref={log} className="graph-run-timeline" role="log" aria-label="Node events" tabIndex={0}
        onScroll={event => { const element = event.currentTarget; following.current = element.scrollHeight - element.scrollTop - element.clientHeight < 24; }}>
        <div className="conversation-events"><EventSequence events={activity.events} status={status} updatedAt={activity.updatedAt} showActiveWork={active} activeWorkStartedAt={activity.createdAt} /></div>
        {activity.error && <p className="form-error">{activity.error}</p>}
      </div> : <div className="graph-run-payload" role="region" aria-label={tab === 'input' ? 'Input payload' : 'Output payload'} tabIndex={0}>
        {tab === 'input' ? <>
          <details><summary>Captured Library</summary>{data.capabilities ? <><p>{data.capabilities.selection.skills.length} skills · {data.capabilities.selection.mcpConnections.length} MCP connections</p>{data.capabilities.skills.map(skill => <p key={skill.id}>{skill.name} · {skill.revision.slice(0, 12)}</p>)}{data.capabilities.connections?.map(c => <p key={c.id}>{c.name} · {c.revision.slice(0, 12)} · {c.selection.tools.join(', ') || 'No tools selected'}</p>)}</> : <p className="muted">Managed inventory was not captured for this run.</p>}</details>
          {Object.entries(activity.input ?? {}).map(([key, value]) => <div key={key}><div className="graph-run-field-heading"><strong>{key}</strong><Button size="sm" variant="ghost" disabled={!value.trim()} onClick={() => readField(key, value)}>Open reader</Button></div><pre>{value}</pre></div>)}
        </> : activity.submission || activity.output ? <>
          {activity.submission && <div className="graph-run-output-choice"><span>Choice</span><code>{activity.submission.choice.id}</code><span>{activity.submission.acceptedAt ? 'Accepted' : 'Pending acceptance'}</span></div>}
          {Object.entries(activity.submission?.payload ?? activity.output ?? {}).map(([key, value]) => <div key={key}><div className="graph-run-field-heading"><strong>{key}</strong><Button size="sm" variant="ghost" disabled={!value.trim()} onClick={() => readField(key, value)}>Open reader</Button></div><pre>{value}</pre></div>)}
        </> : <p className="muted">No output yet.</p>}
      </div>}
      <div ref={onRequestTarget} className="graph-run-requests" hidden={!hasRequests} aria-label="Graph requests" />
      {reading && <PlanReader title={reading.title} text={reading.text} onClose={() => setReading(null)} />}
    </section>
  </Panel>;
}
