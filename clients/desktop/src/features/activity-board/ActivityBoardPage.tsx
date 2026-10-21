import { useCallback, useEffect, useRef, useState } from 'react';
import { ArrowLeft, ExternalLink, ListTodo, RefreshCw, Settings } from 'lucide-react';
import { Button } from '../../design-system/Button';
import { Badge } from '../../design-system/Badge';
import { SearchSelect } from '../../design-system/SearchSelect';
import { EngineRequestError, type Project } from '../../engine';
import { BoardSettingsDialog } from './BoardSettingsDialog';
import { SprintPicker } from './SprintPicker';
import type { ActivityBoardState } from './useActivityBoard';
import { boardError, boardItems, boardMoveReceipt, cancelUnacceptedMove, moveBoardItem, providerName, scopeName, type BoardConfig, type BoardItem, type BoardMove, type BoardPage, type BoardPhase, type BoardSprint } from './api';
import './activity-board.css';

function externalURL(item: BoardItem) {
  try { const url = new URL(item.url); return url.protocol === 'https:' && url.hostname === (item.provider === 'github' ? 'github.com' : 'dev.azure.com') ? url.href : undefined; } catch { return undefined; }
}
function operationID() { return [...crypto.getRandomValues(new Uint8Array(16))].map(b => b.toString(16).padStart(2, '0')).join(''); }

function BoardColumn({ phase, projectId, sprintId, config, projects, moving, active, onMove }: {
  phase: BoardPhase; projectId: string; sprintId: string; config: BoardConfig; projects: Project[]; moving: boolean; active: boolean; onMove: (item: BoardItem, phaseId: string) => void;
}) {
  const element = useRef<HTMLElement>(null);
  const [visible, setVisible] = useState(false);
  const [page, setPage] = useState<BoardPage>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const controller = useRef<AbortController | null>(null);
  const flight = useRef(false);
  useEffect(() => {
    const target = element.current;
    if (!target) return;
    const observer = new IntersectionObserver(entries => { if (entries.some(entry => entry.isIntersecting)) { setVisible(true); observer.disconnect(); } }, { rootMargin: '0px 160px' });
    observer.observe(target);
    return () => observer.disconnect();
  }, []);
  const load = useCallback(async (cursor = '') => {
    if (flight.current) return;
    flight.current = true; setBusy(true); setError('');
    const signal = controller.current?.signal;
    try {
      const next = await boardItems(projectId, phase.id, cursor, signal, sprintId);
      if (!signal?.aborted) setPage(previous => cursor && previous ? { ...next, items: [...previous.items, ...next.items] } : next);
    } catch (e) { if (!signal?.aborted) { setError(boardError(e)); if (e instanceof EngineRequestError && e.status === 409) setPage(undefined); } }
    finally { if (!signal?.aborted) { flight.current = false; setBusy(false); } }
  }, [projectId, phase.id, sprintId]);
  useEffect(() => {
    if (!visible || !active) return;
    controller.current = new AbortController(); flight.current = false;
    void load();
    return () => controller.current?.abort();
  }, [visible, active, load, config.projectionRevision]);
  return <section ref={element} className="board-column" aria-label={phase.name}>
    <header><h2>{phase.name}</h2>{page && <Badge>{page.total}</Badge>}</header>
    <div className="board-column-items">
      {page?.items.map(item => {
        const c = config.connections.find(connection => connection.id === item.connectionId);
        if (c?.sync.snapshotPending) return null;
        const url = externalURL(item);
        return <article key={item.key} className="board-card"><div className="board-card-meta"><span>{providerName(item.provider)} · #{item.number}</span><Badge>{item.state}</Badge></div><h3>{item.title}</h3><p>{c ? scopeName(c) : ''}</p>{!projectId && <p>{projects.find(p => p.id === item.projectId)?.name}</p>}
          {item.move && <p role="status">{item.move.status === 'pending' ? 'Updating…' : 'Update unconfirmed'}</p>}
          <div className="board-card-actions">{url && <a href={url} target="_blank" rel="noreferrer">Open in {providerName(item.provider)}<ExternalLink aria-hidden="true" /></a>}
            <SearchSelect label={`Move #${item.number} to`} value="" placeholder="Move to" disabled={moving || c?.syncing || c?.sync.stale || !c?.available || !!item.move} options={config.phases.filter(p => p.id !== item.phaseId).map(p => ({ value: p.id, label: p.name }))} onChange={id => onMove(item, id)} />
          </div>
        </article>;
      })}
      {error && <p className="form-error" role="alert">{error}<Button size="sm" disabled={busy} onClick={() => void load()}>Reload column</Button></p>}
      {busy && <p role="status" className="muted">Loading…</p>}
      {visible && page && !page.items.length && !busy && <p className="muted">No matching tasks</p>}
      {page?.hasMore && <Button disabled={busy} onClick={() => void load(page.nextCursor)}>Load more</Button>}
    </div>
  </section>;
}

export function ActivityBoardPage({ projects, projectId, visible, board, onProjectChange, onBack, onConnections }: { projects: Project[]; projectId: string; visible: boolean; board: ActivityBoardState; onProjectChange: (id: string) => void; onBack: () => void; onConnections: (projectId: string) => void }) {
  const [settings, setSettings] = useState(false);
  const [sprintFilter, setSprintFilter] = useState<{ projectId: string; sprint?: BoardSprint }>();
  const [moving, setMoving] = useState(false);
  const [receipt, setReceipt] = useState<BoardMove>();
  const [unknownOperation, setUnknownOperation] = useState('');
  const [missingReceipt, setMissingReceipt] = useState('');
  const [attempt, setAttempt] = useState<{ item: BoardItem; phaseId: string; revision: string; operationId: string }>();
  const [notice, setNotice] = useState('');
  const live = useRef(true);
  useEffect(() => { live.current = true; return () => { live.current = false; }; }, []);
  const { config } = board;
  const relevant = config?.connections.filter(c => !projectId || c.projectId === projectId) ?? [];
  const activeSprintFilter = sprintFilter?.projectId === projectId ? sprintFilter : undefined;
  const sprint = activeSprintFilter?.sprint;
  const sprintId = sprint?.id ?? '';
  const hasAzure = relevant.some(c => c.provider === 'azure');
  const waitingForSprint = hasAzure && !activeSprintFilter;
  const unresolved = config?.unresolvedMoves.filter(m => relevant.some(c => c.id === m.connectionId)) ?? [];

  async function checkReceipt(id: string) {
    try { const result = await boardMoveReceipt(id); if (live.current) { setReceipt(result); setUnknownOperation(''); setMissingReceipt(''); setNotice(result.message ?? 'Updating…'); void board.load(); } }
    catch (e) { if (live.current) { setUnknownOperation(id); setMissingReceipt(e instanceof EngineRequestError && e.status === 404 ? id : ''); setNotice(e instanceof EngineRequestError && e.status === 404 ? 'No update receipt found. You can cancel this unaccepted request before another move.' : 'Update result unavailable. Check the receipt and refresh before another update.'); } }
  }
  useEffect(() => {
    if (!visible || receipt?.status !== 'pending') return;
    const controller = new AbortController();
    let checking = false;
    const timer = window.setInterval(async () => {
      if (checking || document.hidden) return;
      checking = true;
      try {
        const next = await boardMoveReceipt(receipt.operationId, controller.signal);
        if (!controller.signal.aborted) { setReceipt(next); if (next.status !== 'pending') { setNotice(next.message ?? next.status); void board.load(); } }
      } catch (e) { if (!controller.signal.aborted) { setUnknownOperation(receipt.operationId); setMissingReceipt(e instanceof EngineRequestError && e.status === 404 ? receipt.operationId : ''); setNotice('Update result unavailable. Check the receipt before another update.'); setReceipt(undefined); } }
      finally { checking = false; }
    }, 1500);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [receipt?.operationId, receipt?.status, board.load, visible]);

  async function move(item: BoardItem, phaseId: string) {
    if (!config || moving || receipt?.status === 'pending' || unknownOperation) return;
    setMoving(true); setNotice('');
    const id = operationID();
    setAttempt({ item, phaseId, revision: config.revision, operationId: id }); setMissingReceipt('');
    try { const next = await moveBoardItem(item, phaseId, config.revision, id); if (live.current) { setReceipt(next); setNotice(next.message ?? 'Updating…'); void board.load(); } }
    catch (e) {
      if (e instanceof EngineRequestError && e.status >= 400 && e.status < 500) { if (live.current) setNotice(boardError(e)); }
      else await checkReceipt(id);
    } finally { if (live.current) setMoving(false); }
  }
  async function cancelMissingUpdate() {
    if (!attempt || attempt.operationId !== missingReceipt || moving) return;
    setMoving(true);
    try {
      const result = await cancelUnacceptedMove(attempt.item, attempt.phaseId, attempt.revision, attempt.operationId);
      if (live.current) { setReceipt(result); setUnknownOperation(''); setMissingReceipt(''); setNotice(result.message ?? 'The engine already accepted this update; check its receipt.'); await board.load(true); }
    } catch { await checkReceipt(attempt.operationId); }
    finally { if (live.current) setMoving(false); }
  }
  return <section className="activity-board-page" aria-label="Tasks">
    <div className="board-toolbar"><Button size="sm" variant="ghost" onClick={onBack}><ArrowLeft />Back</Button><SearchSelect label="Tasks project" value={projectId} options={[{ value: '', label: 'All projects' }, ...projects.map(p => ({ value: p.id, label: p.name }))]} onChange={id => { setSprintFilter(undefined); onProjectChange(id); }} />{config && (sprint || hasAzure) && <SprintPicker key={projectId} projectId={projectId} projectionRevision={config.projectionRevision} visible={visible} initialized={!!activeSprintFilter} value={sprint} onChange={next => setSprintFilter({ projectId, sprint: next })} />}<div className="board-toolbar-actions"><Button size="sm" disabled={!config || board.busy} onClick={() => void board.refresh()}><RefreshCw />{board.busy ? 'Refreshing…' : 'Refresh'}</Button><Button size="sm" disabled={!config} onClick={() => setSettings(true)}><Settings />Phases</Button>{projectId ? <Button size="sm" onClick={() => onConnections(projectId)}><ListTodo />Connections</Button> : <SearchSelect label="Task connections for project" value="" placeholder="Connections" disabled={!projects.length} options={projects.map(p => ({ value: p.id, label: p.name }))} onChange={onConnections} />}</div></div>
    {board.error && <p role="alert" className="board-feedback form-error">{board.error}<Button size="sm" onClick={() => void board.load()}>Retry</Button></p>}
    {notice && <p role="status" className="board-feedback">{notice}{unknownOperation && <Button size="sm" disabled={moving} onClick={() => void checkReceipt(unknownOperation)}>Check update receipt</Button>}{missingReceipt && attempt?.operationId === missingReceipt && <Button size="sm" disabled={moving} onClick={() => void cancelMissingUpdate()}>Cancel unaccepted update</Button>}</p>}
    {unresolved.filter(m => m.status === 'unconfirmed').map(m => <p key={m.operationId} className="board-feedback" role="status">Update unconfirmed · {config?.connections.find(c => c.id === m.connectionId)?.name}. Refresh to reconcile.<Button size="sm" onClick={() => void checkReceipt(m.operationId)}>Check receipt</Button></p>)}
    {relevant.filter(c => !c.available || c.sync.stale).map(c => <p key={c.id} role="status" className="board-feedback"><strong>{c.name}</strong> · {!c.available ? 'Project unavailable' : 'Out of date'}{c.sync.error && ` · ${c.sync.error}`}</p>)}
    {!config ? !board.error && <p className="board-feedback" role="status">Loading…</p> : <>
      {!relevant.length && <div className="board-empty"><h2>Connect a provider</h2>{projectId ? <Button onClick={() => onConnections(projectId)}>Project settings</Button> : <SearchSelect label="Project to connect" value="" placeholder="Select a project" disabled={!projects.length} options={projects.map(p => ({ value: p.id, label: p.name }))} onChange={onConnections} />}</div>}
      {!config.phases.length && <div className="board-empty"><h2>Add phase</h2><Button onClick={() => setSettings(true)}>Phases</Button></div>}
      {waitingForSprint ? <p className="board-feedback" role="status">Waiting for sprint selection…</p> : <div className="board-columns" tabIndex={0} aria-label="Task phases">
        {[...config.phases, ...(config.connections.length ? [{ id: 'conflicts', name: 'Mapping conflicts' }, { id: 'unmapped', name: 'Mapping required' }] : [])].map(phase => <BoardColumn key={`${projectId}:${sprintId}:${phase.id}`} phase={phase} projectId={projectId} sprintId={sprintId} config={config} projects={projects} active={visible} moving={moving || receipt?.status === 'pending' || !!unknownOperation} onMove={(item, id) => void move(item, id)} />)}
      </div>}
      {settings && <BoardSettingsDialog config={config} onSaved={board.setConfig} onClose={() => setSettings(false)} />}
    </>}
  </section>;
}
