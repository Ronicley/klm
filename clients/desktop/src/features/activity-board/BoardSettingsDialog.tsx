import { useEffect, useRef, useState } from 'react';
import { Plus, X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { BoardMappingsEditor } from './BoardMappingsEditor';
import { EngineRequestError } from '../../engine';
import { BoardMutationUnconfirmed, boardConfig, boardError, discoverPhaseMappings, saveBoardPhase, type BoardConfig, type BoardMappings, type BoardValue } from './api';

export function BoardSettingsDialog({ config, onSaved, onClose }: { config: BoardConfig; onSaved: (config: BoardConfig) => void; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [phase, setPhase] = useState<{ id: string; name: string } | null>(null);
  const [draftRevision, setDraftRevision] = useState('');
  const [needsReload, setNeedsReload] = useState(false);
  const [phaseMaps, setPhaseMaps] = useState<BoardMappings>({});
  const [catalogs, setCatalogs] = useState<Record<string, BoardValue[]>>({});
  const syncing = config.connections.some(c => c.syncing);
  useEffect(() => {
    const element = dialog.current;
    returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (element && !element.open) element.showModal();
    return () => { if (!element?.isConnected && returnFocus.current?.isConnected) returnFocus.current.focus(); };
  }, []);

  async function run(action: () => Promise<void>) {
    if (busy) return;
    setBusy(true); setError('');
    try { await action(); } catch (e) { setError(boardError(e)); if (e instanceof BoardMutationUnconfirmed || e instanceof EngineRequestError && e.status === 409) setNeedsReload(true); } finally { setBusy(false); }
  }
  async function loadValues() {
    await run(async () => {
      const entries = await Promise.all(config.connections.map(async c => [c.id, (await discoverPhaseMappings(c)).values] as const));
      setCatalogs(Object.fromEntries(entries));
    });
  }
  const complete = phase && phase.name.trim() && (phase.id || config.connections.every(c => (phaseMaps[c.id]?.length ?? 0) > 0));
  return <dialog ref={dialog} className="settings-dialog board-settings-dialog" aria-labelledby="task-phases-title" onCancel={e => { e.preventDefault(); if (!busy) onClose(); }} onClose={onClose}>
    <div className="dialog-heading"><h2 id="task-phases-title">Task phases</h2><IconButton label="Close task phases" disabled={busy} onClick={onClose}><X /></IconButton></div>
    <div className="board-settings-content">
      {error && <p className="form-error" role="alert">{error}</p>}
      {phase && (needsReload || draftRevision !== config.revision) && <div className="board-form-actions"><span role="status">Reload phases before saving this draft.</span><Button disabled={busy} onClick={() => void run(async () => { const next = await boardConfig(); onSaved(next); setDraftRevision(next.revision); setNeedsReload(false); setPhaseMaps({}); setCatalogs({}); if (phase.id) setPhase(next.phases.find(p => p.id === phase.id) ?? null); else { setPhase({ id: '', name: phase.name }); const entries = await Promise.all(next.connections.map(async c => [c.id, (await discoverPhaseMappings(c)).values] as const)); setCatalogs(Object.fromEntries(entries)); } })}>Reload phases</Button></div>}
      {syncing && <p role="status" className="muted">Refreshing connections… Configuration can be saved when refresh finishes.</p>}
      {!phase ? <><div className="board-settings-list">{config.phases.map(p => <div key={p.id}><strong>{p.name}</strong><Button size="sm" variant="ghost" onClick={() => { setPhase(p); setDraftRevision(config.revision); setNeedsReload(false); setError(''); }}>Rename</Button></div>)}</div><Button disabled={busy || syncing} onClick={() => { setPhase({ id: '', name: '' }); setDraftRevision(config.revision); setNeedsReload(false); setPhaseMaps({}); setCatalogs({}); void loadValues(); }}><Plus />Add phase</Button></> : <form onSubmit={event => { event.preventDefault(); if (!needsReload) void run(async () => { onSaved(await saveBoardPhase(phase.name, phase.id, phaseMaps, draftRevision)); setPhase(null); }); }}>
        <label>Phase name<Input autoFocus required maxLength={100} value={phase.name} disabled={busy} onChange={e => setPhase({ ...phase, name: e.target.value })} /></label>
        {!phase.id && config.connections.map(c => <BoardMappingsEditor key={c.id} label={c.name} values={phaseMaps[c.id] ?? []} catalog={catalogs[c.id] ?? []} excluded={Object.values(c.mappings).flat().map(v => v.id)} disabled={busy} onChange={values => setPhaseMaps(current => ({ ...current, [c.id]: values }))} />)}
        <div className="board-form-actions"><Button type="submit" variant="primary" disabled={busy || syncing || needsReload || draftRevision !== config.revision || !complete}>Save phase</Button><Button disabled={busy} onClick={() => setPhase(null)}>Cancel</Button>{!phase.id && config.connections.length > 0 && <Button disabled={busy} onClick={() => void loadValues()}>Reload values</Button>}</div>
      </form>}
    </div>
  </dialog>;
}
