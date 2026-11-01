import { ArrowUpRight } from 'lucide-react';
import { useState } from 'react';
import type { EngineEvent, Harness } from '../../engine';
import { HarnessIcon } from './HarnessIcon';
import { useSessionNavigation } from './SessionNavigation';
import { topicArchived } from '../workspace/topics';

const harnessNames: Record<Harness['id'], string> = { pi: 'Pi', opencode: 'OpenCode', codex: 'Codex' };
function text(value: unknown) { return typeof value === 'string' ? value : ''; }

export function SessionSpawnEvent({ event }: { event: EngineEvent }) {
  const navigation = useSessionNavigation();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const sessionId = text(event.data?.sessionId);
  const target = navigation.sessions.get(sessionId);
  const project = target && navigation.projects.get(target.projectId);
  const name = target?.title || text(event.data?.title) || event.title?.replace(/^Created\s+/, '') || 'Session';
  const harness = text(event.data?.harness) || target?.harness;
  const knownHarness = harness === 'pi' || harness === 'opencode' || harness === 'codex' ? harness : undefined;
  // New receipts describe the creation settings; legacy events use available inventory.
  const model = typeof event.data?.model === 'string' ? event.data.model || 'Default model' : target?.model || target?.resolvedModel || 'Unknown model';
  const effort = typeof event.data?.effort === 'string' ? event.data.effort || 'Default' : target?.effort || target?.resolvedEffort || 'Unknown effort';
  const effortLabel = effort === 'xhigh' ? 'Extra high' : effort.charAt(0).toUpperCase() + effort.slice(1);
  const available = !!target && !target.archived && !target.parentId && !target.role && !!project && !topicArchived(project, target);
  return <button type="button" className="session-spawn-event" disabled={loading || !sessionId || !!target && !available || !navigation.onOpenSession}
    aria-label={`Open created session ${name}`} title={error || (target && !available ? target.archived || topicArchived(project, target) ? 'Session archived' : 'Session unavailable' : `Open ${name}`)}
    onClick={() => {
      if (target && available) { navigation.onOpenSession?.(target); return; }
      setLoading(true); setError('');
      void navigation.resolveSession(sessionId).then(next => {
        if (next.archived || next.parentId || next.role) throw new Error('Session is archived or unavailable.');
        navigation.onOpenSession?.(next);
      }).catch(error => setError(error instanceof Error ? error.message : 'Could not open session.')).finally(() => setLoading(false));
    }}>
    <span className="session-spawn-title"><span>Created</span> <strong>{name}</strong></span>
    <span className="session-spawn-metadata">
      <span className="session-spawn-harness">{knownHarness && <span aria-hidden="true"><HarnessIcon harness={knownHarness} /></span>}{knownHarness ? harnessNames[knownHarness] : 'Unknown harness'}</span>
      <span title={model}>{model}</span><span>{effortLabel}</span>
    </span>
    <ArrowUpRight className="session-spawn-open" aria-hidden="true" />
  </button>;
}
