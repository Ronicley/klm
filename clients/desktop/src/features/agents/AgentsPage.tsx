import { useEffect, useRef, useState } from 'react';
import { MoreHorizontal, Pause, Play, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { ListTile } from '../../design-system/ListTile';
import { Menu, MenuItem } from '../../design-system/Menu';
import { AgentEditorDialog } from './AgentEditorDialog';
import { DeleteAgentDialog } from './DeleteAgentDialog';
import { HarnessIcon } from '../chat/HarnessIcon';
import { effortLabel } from '../graphs/agent-node';
import { currentEngineIdentity, engineIdentityGeneration } from '../../engineIdentity';
import { getAgent, deleteAgent, saveAgent } from './api';
import { AgentProblemMessage } from './AgentProblemMessage';
import { useAgentPage } from './useAgentPage';
import { agentOrigin, type AgentDefinition, type AgentSource, type CatalogAgent } from './types';
import './agents.css';
import { useEngineDraft } from '../drafts/draftStore';

export function AgentsPage({ projectId = '' }: { projectId?: string }) {
  const identity = `${currentEngineIdentity()}/${engineIdentityGeneration()}/${projectId}`;
  return <AgentManagement key={identity} projectId={projectId} />;
}

function AgentManagement({ projectId }: { projectId: string }) {
  const [search, setSearch] = useState('');
  const [query, setQuery] = useState('');
  const [offset, setOffset] = useState(0);
  const [openMenu, setOpenMenu] = useState<string | null>(null);
  const [editing, setEditing] = useEngineDraft<CatalogAgent | 'new' | null>(`ui.agents:${projectId}.editing`, null);
  const [editorReset, setEditorReset] = useState(0);
  const [deleting, setDeleting] = useState<CatalogAgent | null>(null);
  const [error, setError] = useState<unknown>();
  const [pending, setPending] = useState(false);
  const detailController = useRef<AbortController | null>(null);
  const editorReloadController = useRef<AbortController | null>(null);
  useEffect(() => () => { detailController.current?.abort(); editorReloadController.current?.abort(); }, []);
  useEffect(() => { const timer = setTimeout(() => { setOffset(0); setQuery(search.trim()); }, 200); return () => clearTimeout(timer); }, [search]);
  const { page, loading, error: loadError, reload } = useAgentPage(projectId, query, offset);
  const rows = page?.agents ?? [];

  function closeEditor() {
    editorReloadController.current?.abort();
    setEditing(null);
  }

  async function discardAndReload(agent: CatalogAgent) {
    editorReloadController.current?.abort();
    const controller = new AbortController(); editorReloadController.current = controller;
    const engineId = currentEngineIdentity(), generation = engineIdentityGeneration();
    const full = await getAgent(agent.source, 0, controller.signal);
    if (controller.signal.aborted || engineId !== currentEngineIdentity() || generation !== engineIdentityGeneration()) return;
    setEditing(full);
    // Explicit human discard resets all editor-local state even at the same revision.
    setEditorReset(value => value + 1);
  }

  async function open(source: AgentSource, action: 'edit' | 'delete' | 'toggle', expectedRevision?: string) {
    if (pending) return;
    const controller = new AbortController(); detailController.current = controller;
    setOpenMenu(null); setPending(true); setError(undefined);
    try {
      const full = await getAgent(source, 0, controller.signal);
      if (controller.signal.aborted) return;
      if (action === 'edit') { editorReloadController.current?.abort(); setEditing(full); }
      else if (action === 'delete') setDeleting(full);
      else {
        if (full.revision !== expectedRevision) throw new Error('Agent changed. Refresh before changing its enabled state.');
        const definition: AgentDefinition = { name: full.name, description: full.description, enabled: !full.enabled,
          defaultHarness: full.defaultHarness, model: full.model, effort: full.effort, prompt: full.prompt,
          skills: full.skills, mcpConnections: full.mcpConnections };
        await saveAgent(definition, full.source.scope, full.source.projectId, full);
        if (!controller.signal.aborted) reload();
      }
    } catch (failure) { if (!controller.signal.aborted) setError(failure); }
    finally { if (!controller.signal.aborted) setPending(false); }
  }

  return <section className="agents-page" aria-label={projectId ? 'Project agents' : 'Global agents'}>
    {!projectId && <h3 className="agents-heading">Agents</h3>}
    <div className="agents-toolbar"><Input type="search" aria-label="Search agents" placeholder="Search agents" value={search} onChange={event => setSearch(event.target.value)} />
      <IconButton label="Refresh agents" disabled={loading || pending} onClick={reload}><RefreshCw /></IconButton>
      <Button variant="primary" disabled={pending} onClick={() => { editorReloadController.current?.abort(); setError(undefined); setEditing('new'); }}><Plus />New agent</Button></div>
    <AgentProblemMessage error={error || loadError} />
    {!!loadError && <Button size="sm" disabled={loading} onClick={reload}>Retry</Button>}
    {page?.diagnostics?.map((diagnostic, index) => <div className="form-error agent-problem" role="status" key={`${diagnostic.code}/${index}`}>
      <p>{diagnostic.message}{diagnostic.source && <> · {agentOrigin(diagnostic.source)} · {diagnostic.source.id}</>}</p>
      {!!diagnostic.conflicts?.length && <ul>{diagnostic.conflicts.map((source, i) => <li key={`${source.sourceUid}/${i}`}>{source.id} · {agentOrigin(source)}</li>)}</ul>}
    </div>)}
    {page && !page.complete && <p className="form-error" role="status">Agent catalog is incomplete. Restore access to the reported sources, then refresh.</p>}
    {loading && <p className="muted" role="status">{page ? 'Refreshing agents...' : 'Loading agents...'}</p>}
    {rows.length ? <ul className="agents-list">{rows.map((agent, index) => {
      const rowKey = `${agent.source.scope}/${agent.source.projectId}/${agent.source.sourceUid}/${agent.id}/${index}`;
      return <li key={rowKey}><ListTile title={agent.name} description={agent.description} onClick={() => { if (!pending) void open(agent.source, 'edit'); }}
        metadata={<><span>{agentOrigin(agent.source)}</span><span className="agent-model">{agent.model}</span>{agent.effort && <span className="agent-effort">{effortLabel(agent.effort)}</span>}{agent.defaultHarness && <span className="agent-harness"><HarnessIcon harness={agent.defaultHarness} />{agent.defaultHarness}</span>}</>}
        status={!agent.enabled ? <span className="agent-disabled-status">Disabled</span> : !agent.available ? <span className="agent-disabled-status" title={agent.unavailableReason}>{agent.status.replaceAll('_', ' ')}</span> : undefined}
        actions={<Menu label={`${agent.name} actions`} role="menu" side="bottom" className="agent-actions-menu" open={openMenu === rowKey} onOpenChange={show => setOpenMenu(show ? rowKey : null)} trigger={props => <IconButton {...props} disabled={pending} label={`${agent.name} actions`}><MoreHorizontal /></IconButton>}>
          <MenuItem role="menuitem" onClick={() => void open(agent.source, 'toggle', agent.revision)}>{agent.enabled ? <Pause /> : <Play />}{agent.enabled ? 'Disable' : 'Enable'}</MenuItem>
          <MenuItem role="menuitem" className="agent-delete-action" onClick={() => void open(agent.source, 'delete')}><Trash2 />Delete</MenuItem>
        </Menu>} /></li>;
    })}</ul> : !loading && page && <p className="agents-empty" role="status">{query ? 'No agents found' : offset ? 'No agents on this page' : 'No agents yet'}</p>}
    <div className="agents-pagination"><Button size="sm" disabled={pending || !page || offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))}>Previous</Button>
      <span>Page {Math.floor(offset / 50) + 1}</span><Button size="sm" disabled={pending || !page?.hasMore} onClick={() => setOffset(page!.nextOffset)}>Next</Button></div>
    {editing && <AgentEditorDialog key={`${editing === 'new' ? 'new' : `${editing.source.sourceUid}/${editing.revision}`}/${editorReset}`} agent={editing === 'new' ? undefined : editing} agents={[]}
      managed projectId={projectId || undefined} onClose={closeEditor} onReload={editing === 'new' ? undefined : () => discardAndReload(editing)}
      onSave={async (draft, scope, revision) => {
        const old = editing === 'new' ? undefined : editing;
        await saveAgent({ ...draft, enabled: old?.enabled ?? true }, scope, projectId, old ? { ...old, revision: revision ?? old.revision } : undefined);
        closeEditor(); reload();
      }} />}
    {deleting && <DeleteAgentDialog busy={pending} agentName={`${deleting.name} · ${agentOrigin(deleting.source)}`} onClose={() => { if (!pending) setDeleting(null); }} onConfirm={() => {
      if (pending) return;
      setPending(true); setError(undefined);
      void deleteAgent(deleting).then(() => { setDeleting(null); reload(); })
        .catch(failure => { setDeleting(null); setError(failure); }).finally(() => setPending(false));
    }} />}
  </section>;
}
