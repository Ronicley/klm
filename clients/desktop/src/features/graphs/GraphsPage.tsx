import { lazy, Suspense, useState } from 'react';
import { MoreHorizontal, Pause, Play, Plus, Trash2 } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { ListTile } from '../../design-system/ListTile';
import { Menu, MenuItem } from '../../design-system/Menu';
import { GraphDetailsDialog } from './GraphDetailsDialog';
import { DeleteGraphDialog } from './DeleteGraphDialog';
import { authoringPath, graphListEntry, type GraphFile } from './files';
import type { Agent } from '../agents/types';
import { request } from '../../engine';
import './graphs.css';
import { flushDrafts, useEngineDraft } from '../drafts/draftStore';

const GraphCanvas = lazy(() => import('./GraphCanvas').then(module => ({ default: module.GraphCanvas })));

export function GraphsPage({ projectId, graphs, agents, catalogError = false, onChanged }: { projectId: string; graphs: GraphFile[]; agents: Agent[]; catalogError?: boolean; onChanged: () => Promise<void> }) {
  const [search, setSearch] = useState('');
  const [openMenu, setOpenMenu] = useState<string | null>(null);
  const [creating, setCreating] = useEngineDraft(`ui.graphs:${projectId}.creating`, false);
  const [editing, setEditing] = useEngineDraft<GraphFile | null>(`ui.graphs:${projectId}.editing`, null);
  const [deleting, setDeleting] = useState<GraphFile | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const query = search.trim().toLowerCase();
  const matches = graphs.filter(g => `${g.definition.name} ${g.definition.description}`.toLowerCase().includes(query));
  const base = authoringPath(projectId);
  async function toggle(graph: GraphFile) {
    setOpenMenu(null); setPending(true); setError('');
    try { await request(`${base}/graphs/${encodeURIComponent(graph.id)}`, 'PATCH', { enabled: !graph.definition.enabled, revision: graph.revision }); await onChanged(); }
    catch (e) { setError(e instanceof Error ? e.message : 'Could not update graph.'); }
    finally { setPending(false); }
  }
  if (editing) return <Suspense fallback={<section className="graphs-page" aria-label="Loading graph canvas" aria-busy="true" />}><GraphCanvas graph={editing} graphs={graphs} projectId={projectId} agents={agents} onSaved={onChanged} onClose={() => { setEditing(null); void onChanged().catch(e => setError(e.message)); }} /></Suspense>;
  return <section className="graphs-page" aria-label="Graphs">
    <div className="graphs-toolbar"><Input type="search" aria-label="Search graphs" placeholder="Search graphs" value={search} onChange={e => setSearch(e.target.value)} /><Button variant="primary" disabled={pending} onClick={() => setCreating(true)}><Plus />New graph</Button></div>
    {error && <p className="form-error" role="alert">{error}</p>}
    {matches.length ? <ul className="graphs-list">{matches.map(graph => <li key={graph.id}>
      <ListTile title={graph.definition.name} description={graph.definition.description} onClick={() => { if (!pending) setEditing(graph); }} status={!graph.definition.enabled ? <span className="graph-disabled-status">Disabled</span> : undefined}
        actions={<Menu label={`${graph.definition.name} actions`} role="menu" side="bottom" className="graph-actions-menu" open={openMenu === graph.id} onOpenChange={open => setOpenMenu(open ? graph.id : null)} trigger={props => <IconButton {...props} disabled={pending} label={`${graph.definition.name} actions`}><MoreHorizontal /></IconButton>}>
          <MenuItem role="menuitem" onClick={() => void toggle(graph)}>{graph.definition.enabled ? <Pause /> : <Play />}{graph.definition.enabled ? 'Disable' : 'Enable'}</MenuItem>
          <MenuItem role="menuitem" className="graph-delete-action" onClick={() => { setOpenMenu(null); setDeleting(graph); }}><Trash2 />Delete</MenuItem>
        </Menu>} />
    </li>)}</ul> : <p className="graphs-empty" role="status">{catalogError ? 'Graph catalog is unavailable.' : query ? 'No graphs found' : 'No graphs yet'}</p>}
    {creating && <GraphDetailsDialog draftKey={`form.graphDetails:${projectId}:new`} graphs={graphs.map(graphListEntry)} onClose={() => setCreating(false)} onSave={async (name, description) => {
      await flushDrafts();
      const draft = await request<GraphFile>(`${base}/graph-drafts`, 'POST', { name, description });
      setCreating(false); setEditing(draft);
    }} />}
    {deleting && <DeleteGraphDialog graphName={deleting.definition.name} onClose={() => { if (!pending) setDeleting(null); }} onConfirm={() => {
      if (pending) return;
      setPending(true); setError('');
      void request(`${base}/graphs/${encodeURIComponent(deleting.id)}`, 'DELETE', { revision: deleting.revision })
        .then(async () => { setDeleting(null); await onChanged(); })
        .catch(e => { setDeleting(null); setError(e instanceof Error ? e.message : 'Could not delete graph.'); })
        .finally(() => setPending(false));
    }} />}
  </section>;
}
