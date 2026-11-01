import { Component, createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { flushSync } from 'react-dom';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Textarea } from '../../design-system/Textarea';
import { engineConnectionError, IS_DESKTOP, resolveEngineURL } from '../../platform';
import { normalizeEngineURL } from '../../engineURL';
import { pairCandidateClient, pairClient, removeSavedClientCredential, verifyCandidateClient } from '../../clientAuth';
import { flushDraftsForEngineChange } from '../drafts/draftStore';
import { engineChangePending } from './localActivities';
import { createSavedEngine, engineRegistryStorageKey, readEngineRegistry, recoverEngineRegistry, saveEngineRegistry, validateEngineRegistry, type EngineRegistry, type SavedEngine } from './engineRegistry';
import { flushConnectionDrafts, loadConnectionDrafts, readConnectionDrafts, updateConnectionDrafts, useConnectionDrafts, type ConnectionForm } from './connectionDrafts';
import { switchEngine, type EngineChangePhase } from './switchEngine';
import { EngineConnectionDialog } from './EngineConnectionDialog';
import { EngineAvatar, EngineList, EngineSwitcher } from './EngineSwitcher';
import './engines.css';

type Editor = { kind: 'add' | 'edit' | 'repair'; engine?: SavedEngine; slot: string };
type Context = {
  registry: EngineRegistry | null; active?: SavedEngine; error: string; busy: boolean;
  rail: (present: boolean) => void; manage: () => void; add: () => void; edit: (engine: SavedEngine) => void;
  repair: (engine?: SavedEngine) => void; remove: (engine: SavedEngine) => void;
  select: (engine: SavedEngine) => void; retry: () => void;
};
const Engines = createContext<Context | undefined>(undefined);
export const useEngines = () => useContext(Engines);
const failure = (error: unknown) => typeof error === 'string' ? error : error instanceof Error ? error.message : 'The engine connection could not be changed.';
const emptyRegistry = (): EngineRegistry => ({ version: 1, engines: [], activeEngineId: null });

export function EngineDialog({ title, children, close, busy = false }: { title: string; children: ReactNode; close: () => void; busy?: boolean }) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialog.current?.showModal();
    return () => { if (opener?.isConnected) opener.focus(); };
  }, []);
  return <dialog ref={dialog} className="settings-dialog engine-dialog" data-engine-transition-ui aria-label={title} onCancel={event => { event.preventDefault(); if (!busy) close(); }}>
    <div className="dialog-heading"><h2>{title}</h2><IconButton label={`Close ${title.toLowerCase()}`} disabled={busy} onClick={close}><X /></IconButton></div>
    {children}
  </dialog>;
}

class WorkspaceBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  render() {
    return this.state.failed ? <main className="client-connection"><section className="client-connection-card"><h1>Could not open the workspace</h1><p>Saved drafts have been kept.</p><EngineRecoveryActions /></section></main> : this.props.children;
  }
}

export function EngineProvider({ children }: { children: ReactNode }) {
  if (!IS_DESKTOP) return children;
  return <NativeEngineProvider>{children}</NativeEngineProvider>;
}

function NativeEngineProvider({ children }: { children: ReactNode }) {
  const runtimeURL = resolveEngineURL();
  const [registry, setRegistry] = useState<EngineRegistry | null>(() => { try { return readEngineRegistry(); } catch { return null; } });
  const [error, setError] = useState(() => { try { readEngineRegistry(); return ''; } catch (error) { return failure(error); } });
  const initial = registry?.engines.find(engine => engine.url === runtimeURL);
  const lastActive = useRef<SavedEngine | undefined>(initial ?? (runtimeURL ? { id: 'runtime', name: new URL(runtimeURL).hostname, url: runtimeURL } : undefined));
  const active = registry?.engines.find(engine => engine.url === runtimeURL) ?? lastActive.current;
  if (active) lastActive.current = active;
  const [railPresent, setRailPresent] = useState(false);
  const [workspace, setWorkspace] = useState(true);
  const [manager, setManager] = useState(!runtimeURL);
  const [editor, setEditor] = useState<Editor>();
  const [removing, setRemoving] = useState<SavedEngine>();
  const [phase, setPhase] = useState<EngineChangePhase>();
  const [labels, setLabels] = useState<string[]>([]);
  const [working, setWorking] = useState(false);
  const workingRef = useRef(false);
  const cancellation = useRef<AbortController | undefined>(undefined);
  const drafts = useConnectionDrafts();
  const recoveryJSON = drafts.document.recoveryJSON ?? '';
  const [duplicate, setDuplicate] = useState<SavedEngine>();
  const busy = working || !!phase;

  useEffect(() => { void loadConnectionDrafts().catch(error => setError(failure(error))); }, []);
  useEffect(() => {
    if (!editor || !drafts.ready || phase || workingRef.current || drafts.document.forms[editor.slot]) return;
    updateConnectionDrafts(document => ({ ...document, forms: { ...document.forms, [editor.slot]: {
      id: editor.engine?.id ?? crypto.randomUUID(), name: editor.engine?.name ?? '', iconId: editor.engine?.iconId, url: editor.engine?.url ?? '', clientName: 'My desktop', code: '',
    } } }));
  }, [editor, drafts.ready, drafts.document.forms, phase, working]);
  useEffect(() => {
    const guard = (event: Event) => {
      if (engineChangePending() && event instanceof KeyboardEvent && (event.key === 'F5' || (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'r')) { event.preventDefault(); event.stopImmediatePropagation(); return; }
      if (!engineChangePending() || event.target instanceof Element && event.target.closest('[data-engine-transition-ui]')) return;
      event.preventDefault(); event.stopImmediatePropagation();
    };
    const events = ['click', 'submit', 'keydown', 'pointerdown', 'dragstart', 'beforeinput', 'change'];
    events.forEach(event => document.addEventListener(event, guard, true));
    return () => events.forEach(event => document.removeEventListener(event, guard, true));
  }, []);

  function refresh() {
    const next = readEngineRegistry(); setRegistry(next); return next;
  }
  function currentRegistry() { return readEngineRegistry() ?? emptyRegistry(); }
  function openEditor(kind: Editor['kind'], engine?: SavedEngine) {
    if (workingRef.current || phase) return;
    const slot = kind === 'add' ? `add:${runtimeURL || 'unselected'}` : `${kind}:${engine?.id}`;
    if (readConnectionDrafts().ready && !readConnectionDrafts().document.forms[slot]) updateConnectionDrafts(document => ({ ...document, forms: { ...document.forms, [slot]: {
      id: engine?.id ?? crypto.randomUUID(), name: engine?.name ?? '', iconId: engine?.iconId, url: engine?.url ?? '', clientName: 'My desktop', code: '',
    } } }));
    setError(''); setDuplicate(undefined); setManager(false); setEditor({ kind, engine, slot });
  }
  async function transition(commit: () => void | Promise<void>) {
    if (cancellation.current) return;
    const controller = new AbortController(); cancellation.current = controller;
    let completed = false;
    try {
      await switchEngine({ signal: controller.signal, status: (phase, labels) => { setPhase(phase); setLabels(labels); },
        detach: () => flushSync(() => setWorkspace(false)), restore: () => flushSync(() => setWorkspace(true)), commit });
      completed = true;
    } catch (error) {
      if (!(error instanceof DOMException && error.name === 'AbortError')) setError(failure(error));
      try { refresh(); } catch { /* Preserve last-known public metadata; writes still use a strict read. */ }
    } finally { cancellation.current = undefined; if (!completed) setPhase(undefined); }
  }
  function select(engine: SavedEngine) {
    if (busy || workingRef.current) return;
    if (engine.url === runtimeURL) { setManager(false); return; }
    setError(''); setManager(false);
    void transition(() => {
      const current = currentRegistry();
      if (!current.engines.some(entry => entry.id === engine.id && entry.url === engine.url)) throw new Error('This saved engine changed. Open the engine list again.');
      saveEngineRegistry({ ...current, activeEngineId: engine.id });
    });
  }
  async function submit(form: ConnectionForm, savedAccess: boolean) {
    if (!editor || workingRef.current) return;
    workingRef.current = true; setWorking(true); setError(''); setDuplicate(undefined);
    let attemptRecorded = false, dispatched = false;
    try {
      const finishForm = async () => {
        updateConnectionDrafts(document => { const forms = { ...document.forms }; delete forms[editor.slot]; return { ...document, forms }; });
        setEditor(undefined); await flushConnectionDrafts();
      };
      if (!form.name.trim() || Array.from(form.name.trim()).length > 80) throw new Error('Enter an engine name of up to 80 characters.');
      const validated = createSavedEngine(form.name, form.url, form.iconId);
      const entry: SavedEngine = { ...validated, id: form.id };
      validateEngineRegistry({ version: 1, engines: [entry], activeEngineId: entry.id });
      const current = currentRegistry();
      const existing = current.engines.find(engine => engine.url === entry.url && engine.id !== entry.id);
      if (existing) { setDuplicate(existing); throw new Error('This Engine URL is already saved. Use its existing connection.'); }
      if (editor.kind !== 'add' && !current.engines.some(engine => engine.id === entry.id && engine.url === entry.url)) throw new Error('This saved engine was removed. Open the engine list.');
      // Verify public storage and protected drafts before consuming a one-use code.
      saveEngineRegistry(current);
      await flushConnectionDrafts(); await flushDraftsForEngineChange();
      if (editor.kind === 'edit') {
        saveEngineRegistry({ ...current, engines: current.engines.map(engine => engine.id === entry.id ? entry : engine) });
        refresh(); await finishForm(); return;
      }
      if (!savedAccess) {
        if (!form.clientName.trim() || Array.from(form.clientName.trim()).length > 80) throw new Error('Enter a client name of up to 80 characters.');
        if (!form.code.trim()) throw new Error('Enter a pairing code.');
        if (!form.codeURL || normalizeEngineURL(form.codeURL) !== entry.url) throw new Error('Enter a pairing code for this Engine URL.');
        if (form.attemptedCode === form.code.trim()) throw new Error('Verify saved access or enter a new pairing code. The previous pairing attempt is not replayed.');
        updateConnectionDrafts(document => ({ ...document, forms: { ...document.forms, [editor.slot]: { ...form, attemptedCode: form.code.trim() } } }));
        attemptRecorded = true;
        await flushConnectionDrafts();
      }
      const saveSelection = async () => {
        const latest = currentRegistry();
        if (latest.engines.some(engine => engine.url === entry.url && engine.id !== entry.id)) throw new Error('This Engine URL was saved by another action. Use its existing entry.');
        const engines = latest.engines.some(engine => engine.id === entry.id) ? latest.engines.map(engine => engine.id === entry.id ? entry : engine) : [...latest.engines, entry];
        saveEngineRegistry({ version: 1, engines, activeEngineId: entry.id });
        await finishForm();
      };
      if (entry.url === runtimeURL && !savedAccess) {
        await transition(async () => { await pairClient(entry.url, form.clientName.trim(), form.code.trim(), () => { dispatched = true; }); await saveSelection(); });
      } else {
        if (savedAccess) await verifyCandidateClient(entry.url);
        else await pairCandidateClient(entry.url, form.clientName.trim(), form.code.trim(), () => { dispatched = true; });
        await transition(saveSelection);
      }
    } catch (error) { setError(failure(error)); }
    finally {
      if (attemptRecorded && !dispatched && readConnectionDrafts().document.forms[editor.slot]) updateConnectionDrafts(document => ({ ...document, forms: { ...document.forms, [editor.slot]: { ...document.forms[editor.slot], attemptedCode: form.attemptedCode } } }));
      workingRef.current = false; setWorking(false);
    }
  }
  async function removeConfirmed() {
    if (!removing || workingRef.current) return;
    const target = removing;
    const remove = async () => {
      const current = currentRegistry();
      if (current.engines.some(engine => engine.url === target.url && engine.id !== target.id)) throw new Error('This saved connection changed. Open the engine list before removing it.');
      saveEngineRegistry(current);
      await removeSavedClientCredential(target.url);
      saveEngineRegistry({ ...current, engines: current.engines.filter(engine => engine.id !== target.id), activeEngineId: target.url === runtimeURL || current.activeEngineId === target.id ? null : current.activeEngineId });
    };
    workingRef.current = true; setWorking(true); setError('');
    try {
      if (target.url === runtimeURL) await transition(remove);
      else { await remove(); refresh(); setRemoving(undefined); }
    } catch (error) { setError(failure(error)); }
    finally { workingRef.current = false; setWorking(false); }
  }
  async function recover(startEmpty: boolean) {
    if (workingRef.current) return;
    workingRef.current = true; setWorking(true); setError('');
    try {
      const original = localStorage.getItem(engineRegistryStorageKey);
      const next = startEmpty ? emptyRegistry() : validateEngineRegistry(JSON.parse(recoveryJSON));
      if (original !== null) updateConnectionDrafts(document => ({ ...document, registryOriginals: document.registryOriginals.includes(original) ? document.registryOriginals : [...document.registryOriginals, original] }));
      await flushConnectionDrafts();
      await transition(async () => { recoverEngineRegistry(next, original); updateConnectionDrafts(document => ({ ...document, recoveryJSON: '' })); await flushConnectionDrafts(); });
    } catch (error) { setError(failure(error)); }
    finally { workingRef.current = false; setWorking(false); }
  }

  const context: Context = { registry, active, error, busy, rail: setRailPresent, manage: () => { if (!busy) { setManager(true); setError(''); try { refresh(); } catch (error) { setError(failure(error)); } } },
    add: () => openEditor('add'), edit: engine => openEditor('edit', engine), repair: engine => openEditor('repair', engine ?? active),
    remove: engine => { if (!busy) { setRemoving(engine); setError(''); } }, select,
    retry: () => { if (!busy) { setError(''); void transition(() => {}); } }, };
  const registryBroken = (() => { try { return readEngineRegistry() === null; } catch { return true; } })();
  return <Engines.Provider value={context}>
    {workspace && <WorkspaceBoundary>{children}</WorkspaceBoundary>}
    {!railPresent && !phase && <div className="engine-recovery-switch" data-engine-transition-ui><EngineSwitcher /><Button size="sm" onClick={context.manage}>{active?.name ?? 'Saved engines'}</Button></div>}
    {error && !editor && !manager && !removing && !phase && <div role="alert" className="engine-global-error" data-engine-transition-ui>{error}<Button size="sm" onClick={context.manage}>Engines</Button><IconButton label="Dismiss connection error" onClick={() => setError('')}><X /></IconButton></div>}
    {manager && !phase && <EngineDialog title="Saved engines" busy={busy} close={() => setManager(false)}>
      {error && <p className="form-error" role="alert">{error}</p>}
      {registryBroken ? <div className="engine-registry-recovery"><p>{engineConnectionError() || 'Saved engine configuration is unavailable.'}</p>
        <Button disabled={busy} onClick={() => { try { refresh(); setError(''); } catch (error) { setError(failure(error)); } }}>Retry reading configuration</Button>
        <label>Configuration JSON<Textarea rows={5} disabled={busy || !drafts.ready} value={recoveryJSON} onChange={event => { const recoveryJSON = event.target.value; updateConnectionDrafts(document => ({ ...document, recoveryJSON })); }} /></label>
        <div className="dialog-actions"><Button disabled={busy || !drafts.ready || !recoveryJSON.trim()} onClick={() => void recover(false)}>Restore configuration</Button><Button disabled={busy || !drafts.ready} onClick={() => void recover(true)}>Preserve original and start new list</Button></div>
      </div> : <EngineList close={() => setManager(false)} />}
      {drafts.error && <p className="form-error" role="alert">{drafts.error}<Button onClick={() => void loadConnectionDrafts().then(flushConnectionDrafts).catch(error => setError(failure(error)))}>Retry connection drafts</Button></p>}
      {!!drafts.document.registryOriginals.length && <Button size="sm" onClick={() => void navigator.clipboard.writeText(drafts.document.registryOriginals.at(-1)!).catch(() => setError('Could not copy the preserved configuration.'))}>Copy preserved original</Button>}
    </EngineDialog>}
    {editor && !phase && <EngineConnectionDialog mode={editor.kind} slot={editor.slot} error={error} busy={busy} duplicate={duplicate} submit={submit} close={() => { if (!busy) setEditor(undefined); }} useDuplicate={engine => { setEditor(undefined); select(engine); }} />}
    {removing && !phase && <EngineDialog title="Remove saved engine" busy={busy} close={() => { if (!busy) setRemoving(undefined); }}>
      <p>Remove <strong>{removing.name}</strong> from this desktop and forget its local credential?</p><p className="muted">Server data, running work and protected drafts are kept.</p>
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="dialog-actions"><Button disabled={busy} onClick={() => setRemoving(undefined)}>Cancel</Button><Button variant="danger-soft" disabled={busy} onClick={() => void removeConfirmed()}>Remove engine</Button></div>
    </EngineDialog>}
    {phase && <EngineDialog title="Changing engine" busy={phase === 'committing'} close={() => cancellation.current?.abort()}>
      <p role="status">{phase === 'waiting' ? 'Waiting for local operations…' : phase === 'saving' ? 'Saving protected drafts…' : 'Opening the selected connection…'}</p>
      {!!labels.length && <ul>{labels.map(label => <li key={label}>{label}</li>)}</ul>}
      <div className="dialog-actions"><Button disabled={phase === 'committing'} onClick={() => cancellation.current?.abort()}>Cancel switch</Button></div>
    </EngineDialog>}
  </Engines.Provider>;
}

export function EngineRecoveryActions() {
  const engines = useEngines();
  if (!engines) return null;
  return <div className="engine-recovery-actions" data-engine-transition-ui>
    {engines.active && <><Button disabled={engines.busy} onClick={engines.retry}>Retry</Button><Button disabled={engines.busy} onClick={() => engines.repair()}>Pair again</Button></>}
    <Button disabled={engines.busy} onClick={engines.manage}>Switch engine</Button><Button variant="primary" disabled={engines.busy} onClick={engines.add}>Add engine</Button>
  </div>;
}

export function EngineRecoverySummary() {
  const engines = useEngines();
  if (!engines) return null;
  return <div className="engine-current-summary"><EngineAvatar engine={engines.active} /><span><strong>{engines.active?.name ?? 'No engine selected'}</strong>{engines.active && <small>{engines.active.url}</small>}</span></div>;
}
