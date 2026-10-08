import { Pause, Play } from 'lucide-react';
import { Button } from '../../design-system/Button';
import type { GraphRunProjection } from '../../engine';

export function GraphRunControl({ run, pending, error, onControl }: {
  run: GraphRunProjection; pending: boolean; error: string;
  onControl: (control: 'pause' | 'resume') => void;
}) {
  const paused = run.status === 'pausing' || run.status === 'paused';
  return <div className="graph-run-control">
    {paused && <span role="status">{run.status === 'paused' ? 'Paused' : 'Pausing'}</span>}
    <Button size="sm" disabled={pending || run.status === 'ending'} onClick={() => onControl(paused ? 'resume' : 'pause')}>
      {paused ? <Play size={14} /> : <Pause size={14} />}{paused ? 'Resume' : 'Pause'}
    </Button>
    {error && <span role="alert">{error}</span>}
  </div>;
}
