import { useEffect, useRef, useState } from 'react';
import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { ListTile } from '../../design-system/ListTile';
import { getAgent } from './api';
import { AgentProblemMessage } from './AgentProblemMessage';
import { useAgentPage } from './useAgentPage';
import { agentOrigin, type CatalogAgent } from './types';

export function AgentPicker({ projectId, value, onChange, onBusyChange, disabled = false }: { projectId: string; value: CatalogAgent | null; onChange: (value: CatalogAgent | null) => void; onBusyChange?: (busy: boolean) => void; disabled?: boolean }) {
  const [search, setSearch] = useState(''), [query, setQuery] = useState(''), [offset, setOffset] = useState(0);
  const [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  useEffect(() => { const timer = setTimeout(() => { setOffset(0); setQuery(search.trim()); }, 200); return () => clearTimeout(timer); }, [search]);
  const page = useAgentPage(projectId, query, offset);
  return <details className="agent-picker"><summary>Agent: {value ? `${value.name} · ${agentOrigin(value.source)}` : 'Default'}</summary>
    <Button disabled={disabled || busy} variant={!value ? 'soft' : 'ghost'} onClick={() => onChange(null)}>Default</Button>
    <Input type="search" aria-label="Search agents" value={search} onChange={event => setSearch(event.target.value)} disabled={disabled} placeholder="Search agents" />
    <AgentProblemMessage error={error || page.error} />
    {!!page.error && <Button size="sm" onClick={page.reload}>Retry agents</Button>}
    {page.loading && <p role="status">Loading agents...</p>}
    {busy && <p role="status">Opening agent...</p>}
    {page.page?.agents.map((agent, index) => <ListTile key={`${agent.source.sourceUid}/${agent.source.projectId}/${index}`} title={agent.name}
      description={`${agentOrigin(agent.source)} · ${agent.defaultHarness} · ${agent.model} · ${agent.effort || 'Default'}`}
      status={(!agent.available || !agent.enabled) ? <span>Unavailable</span> : value?.source.sourceUid === agent.source.sourceUid ? <span>Selected</span> : undefined}
      onClick={() => {
        if (disabled || busy || !agent.available || !agent.enabled) return;
        const next = new AbortController(); controller.current?.abort(); controller.current = next;
        setBusy(true); onBusyChange?.(true); setError(undefined);
        void getAgent(agent.source, 0, next.signal).then(full => {
          if (next.signal.aborted) return;
          if (!full.enabled || !full.available) throw new Error(full.unavailableReason || 'This agent is unavailable for new sessions.');
          onChange(full);
        }).catch(error => { if (!next.signal.aborted) setError(error); }).finally(() => { if (!next.signal.aborted) { setBusy(false); onBusyChange?.(false); } });
      }} />)}
    <div className="agents-pagination"><Button size="sm" disabled={disabled || busy || offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))}>Previous agents</Button><Button size="sm" disabled={disabled || busy || !page.page?.hasMore} onClick={() => setOffset(page.page!.nextOffset)}>Next agents</Button></div>
  </details>;
}
