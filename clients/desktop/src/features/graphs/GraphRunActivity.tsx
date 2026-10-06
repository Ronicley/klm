import { useEffect, useRef, useState } from 'react';
import { Panel } from '@xyflow/react';
import { X } from 'lucide-react';
import { IconButton } from '../../design-system/Button';
import { TabNav } from '../../design-system/TabNav';
import { Select } from '../../design-system/Select';
import { request, type GraphNodeActivity } from '../../engine';
import { EventSequence } from '../chat/ConversationEvents';

const statusLabels: Record<string, string> = { reserved: 'Pending', running: 'Running', waiting_user: 'Waiting', sealing: 'Finishing', completed: 'Completed', failed: 'Failed', interrupted: 'Interrupted' };

export function GraphRunActivity({ sessionId, runId, nodeId, nodeName, onClose }: {
  sessionId: string; runId: string; nodeId: string; nodeName: string; onClose: () => void;
}) {
  const [tab, setTab] = useState<'run' | 'input' | 'output'>('run');
  const [activationId, setActivationId] = useState('');
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
        const result = await request<GraphNodeActivity>(`/api/sessions/${encodeURIComponent(sessionId)}/graph/runs/${encodeURIComponent(runId)}/nodes/${encodeURIComponent(nodeId)}/activity${activationId ? `?activationId=${encodeURIComponent(activationId)}` : ''}`, 'GET', undefined, 15000, controller.signal);
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
  }, [sessionId, runId, nodeId, activationId]);
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
      {error && <p role="alert" className="form-error graph-run-notice">{error}</p>}
      {!data ? <p className="muted graph-run-notice">{error ? 'Retrying...' : 'Loading activity...'}</p> : !activity ? <p className="muted graph-run-notice">Not started.</p> : tab === 'run' ? <div ref={log} className="graph-run-timeline" role="log" aria-label="Node events" tabIndex={0}
        onScroll={event => { const element = event.currentTarget; following.current = element.scrollHeight - element.scrollTop - element.clientHeight < 24; }}>
        <div className="conversation-events"><EventSequence events={activity.events} status={status} updatedAt={activity.updatedAt} showActiveWork={active} activeWorkStartedAt={activity.createdAt} /></div>
        {activity.error && <p className="form-error">{activity.error}</p>}
      </div> : <div className="graph-run-payload" role="region" aria-label={tab === 'input' ? 'Input payload' : 'Output payload'} tabIndex={0}>
        {tab === 'input' ? <>
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
