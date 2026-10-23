import { useEffect, useState } from 'react';
import { Plus } from 'lucide-react';
import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Select } from '../../design-system/Select';
import { EngineRequestError, type Project } from '../../engine';
import { AzureScopePicker } from './AzureScopePicker';
import { BoardMappingsEditor } from './BoardMappingsEditor';
import { BoardMutationUnconfirmed, boardConfig, boardError, connectionDraft, discoverBoard, providerName, removeBoardConnection, saveBoardConnection, scopeName, type BoardConfig, type BoardConnection, type BoardValue, type ConnectionDraft } from './api';
import './activity-board.css';

export function ProjectTaskConnections({ project, config, onSaved, onBusyChange }: {
  project: Project; config: BoardConfig; onSaved: (config: BoardConfig) => void; onBusyChange: (busy: boolean) => void;
}) {
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState<ConnectionDraft>();
  const [draftRevision, setDraftRevision] = useState('');
  const [needsReload, setNeedsReload] = useState(false);
  const [catalog, setCatalog] = useState<BoardValue[]>();
  const [labelName, setLabelName] = useState('');
  const [deleting, setDeleting] = useState('');
  const syncing = config.connections.some(c => c.syncing);
  const connections = config.connections.filter(c => c.projectId === project.id);
  useEffect(() => { onBusyChange(busy); }, [busy, onBusyChange]);
  useEffect(() => () => onBusyChange(false), [onBusyChange]);

  async function run(action: () => Promise<void>) {
    if (busy) return;
    setBusy(true); setError('');
    try { await action(); } catch (e) { setError(boardError(e)); if (e instanceof BoardMutationUnconfirmed || e instanceof EngineRequestError && e.status === 409) setNeedsReload(true); } finally { setBusy(false); }
  }
  function editConnection(c?: BoardConnection, snapshot = config) {
    setDraftRevision(snapshot.revision); setNeedsReload(false);
    setDraft(c ? connectionDraft(c) : { name: '', provider: 'github', projectId: project.id, scope: {}, token: '', mappings: Object.fromEntries(snapshot.phases.map(p => [p.id, []])) });
    setCatalog(undefined); setError(''); setLabelName(''); setDeleting('');
  }
  const complete = draft && draft.name.trim() && catalog && config.phases.every(p => (draft.mappings[p.id]?.length ?? 0) > 0);
  return <section className="project-task-connections" aria-label="Task connections">
    <h3>Tasks</h3>
    {error && <p className="form-error" role="alert">{error}</p>}
    {draft && (needsReload || draftRevision !== config.revision) && <div className="board-form-actions"><span role="status">Reload the connection before saving this draft.</span><Button disabled={busy} onClick={() => void run(async () => { const next = await boardConfig(); onSaved(next); const current = next.connections.find(c => c.id === draft.connectionId); if (draft.connectionId && !current) { setDraft(undefined); setNeedsReload(false); setError('Connection was removed.'); } else editConnection(current,next); })}>Reload connection</Button></div>}
    {syncing && <p role="status" className="muted">Refreshing connections… Configuration can be saved when refresh finishes.</p>}
    {!draft ? <>
      <div className="board-settings-list">{connections.map(c => <div key={c.id}><div><strong>{c.name}</strong><small>{providerName(c.provider)} · {scopeName(c)}</small></div><Button size="sm" onClick={() => editConnection(c)}>Edit</Button></div>)}</div>
      <Button disabled={busy || syncing} onClick={() => editConnection()}><Plus />Connect provider</Button>
    </> : <form onSubmit={event => { event.preventDefault(); if (!needsReload) void run(async () => { onSaved(await saveBoardConnection(draft, draftRevision)); setDraft(undefined); setCatalog(undefined); }); }}>
      <label>Connection name<Input required value={draft.name} maxLength={100} disabled={busy} onChange={e => setDraft({ ...draft, name: e.target.value })} /></label>
      <label>Provider<Select label="Provider" variant="field" value={draft.provider} disabled={busy || !!draft.connectionId} onChange={e => { setDraft({ ...draft, provider: e.target.value as ConnectionDraft['provider'], scope: {}, mappings: Object.fromEntries(config.phases.map(p => [p.id, []])) }); setCatalog(undefined); }}><option value="github">GitHub</option><option value="azure">Azure DevOps</option></Select></label>
      {draft.provider === 'github' ? <label>Repository<Input required placeholder="owner/repository" value={draft.scope.repository ?? ''} disabled={busy || !!draft.connectionId} onChange={e => { setDraft({ ...draft, scope: { repository: e.target.value } }); setCatalog(undefined); }} /></label> : <label>Organization<Input required value={draft.scope.organization ?? ''} disabled={busy || !!draft.connectionId} onChange={e => { setDraft({ ...draft, scope: { organization: e.target.value }, mappings: Object.fromEntries(config.phases.map(p => [p.id, []])) }); setCatalog(undefined); }} /></label>}
      <label>{draft.connectionId ? 'Replace token' : 'Personal access token'}<Input type="password" autoComplete="off" spellCheck={false} required={!draft.connectionId} value={draft.token} disabled={busy} onChange={e => { setDraft({ ...draft, token: e.target.value, ...(draft.provider === 'azure' && !draft.connectionId ? { scope: { organization: draft.scope.organization }, mappings: Object.fromEntries(config.phases.map(p => [p.id, []])) } : {}) }); setCatalog(undefined); }} /></label>
      {draft.provider === 'azure' && <AzureScopePicker draft={draft} busy={busy} run={run} onChange={next => { setDraft(next); setCatalog(undefined); }} onSelectType={next => {
        setDraft(next); setCatalog(undefined);
        void run(async () => { const result = await discoverBoard(next); setCatalog(result.values); setDraft({ ...next, scope: result.scope }); });
      }} />}
      <Button disabled={busy || draft.provider === 'azure' && !draft.scope.workItemType} onClick={() => void run(async () => { const result = await discoverBoard(draft); setCatalog(result.values); setDraft({ ...draft, scope: result.scope }); })}>{busy ? 'Loading…' : draft.provider === 'azure' ? 'Load states' : 'Discover labels'}</Button>
      {catalog && <>
        <h3>Mappings</h3>
        {config.phases.length === 0 && <p className="muted">No phases configured.</p>}
        {config.phases.map(p => <BoardMappingsEditor key={p.id} label={p.name} values={draft.mappings[p.id] ?? []} catalog={catalog} excluded={Object.entries(draft.mappings).filter(([id]) => id !== p.id).flatMap(([, values]) => values.map(v => v.id))} disabled={busy} onChange={values => setDraft({ ...draft, mappings: { ...draft.mappings, [p.id]: values } })} />)}
        {draft.provider === 'github' && <div className="board-create-label"><label>New GitHub label<Input maxLength={50} disabled={busy} value={labelName} onChange={e => setLabelName(e.target.value)} /></label><Button disabled={busy || !labelName.trim()} onClick={() => void run(async () => { const result = await discoverBoard(draft, labelName.trim()); setCatalog(result.values); setLabelName(''); })}>Create label</Button></div>}
      </>}
      <div className="board-form-actions"><Button type="submit" variant="primary" disabled={busy || syncing || needsReload || draftRevision !== config.revision || !complete}>Save connection</Button><Button disabled={busy} onClick={() => setDraft(undefined)}>Cancel</Button></div>
      {draft.connectionId && <div className="board-form-actions">{deleting === draft.connectionId ? <><span>Disconnect this task provider?</span><Button variant="danger-soft" disabled={busy || syncing || needsReload || draftRevision !== config.revision} onClick={() => void run(async () => { onSaved(await removeBoardConnection(draft.connectionId!, draftRevision)); setDraft(undefined); setDeleting(''); })}>Confirm disconnect</Button><Button disabled={busy} onClick={() => setDeleting('')}>Keep connection</Button></> : <Button variant="danger-soft" disabled={busy || syncing || needsReload || draftRevision !== config.revision} onClick={() => setDeleting(draft.connectionId!)}>Disconnect</Button>}</div>}
    </form>}
  </section>;
}
