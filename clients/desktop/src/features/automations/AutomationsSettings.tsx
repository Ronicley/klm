import { useEffect, useRef, useState } from 'react';
import { AlertCircle, ArrowLeft, ArrowUpRight, CalendarClock, Check, ChevronRight, GitPullRequest, Pause, Play, Plus, Search, Trash2, Zap } from 'lucide-react';
import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Select } from '../../design-system/Select';
import { EngineRequestError, request, type Project, type Session } from '../../engine';
import { AutomationEditor } from './AutomationEditor';
import { automationPath, relativeNextRun, type Automation, type AutomationList, type AutomationRunLink } from './api';
import { scheduledDate, sessionSummary, triggerSummary } from './presentation';
import './automations.css';
import { bindDraftDiscard, flushDrafts, useEngineDraft } from '../drafts/draftStore';
import { IS_DESKTOP } from '../../platform';
import { releaseRunNowAttempt } from './runNowAttempt';
import { beginLocalActivity } from '../engines/localActivities';

const failure = (error: unknown) => error instanceof Error ? error.message : 'Could not update automations.';
const empty = (projectId: string): Automation => ({ id: '', projectId, revision: 0, operationId: crypto.randomUUID(), name: '', prompt: '', mode: 'schedule', enabled: true, parallel: false, harness: 'opencode', model: '', effort: '', yolo: false, graphs: [], selection: { skills: [], mcpConnections: [] }, topicId: '', schedule: { time: '09:00', recurrence: 'daily', everyDays: 2, weekdays: [1], monthDay: 1 }, afterSequence: 0 });

export function AutomationsSettings({ project, sessions, onOpenSession, onBusyChange }: { project: Project; sessions: Session[]; onOpenSession: (id: string) => void; onBusyChange: (busy: boolean) => void }) {
  const forgetDrafts = bindDraftDiscard();
  const [list, setList] = useState<AutomationList>();
  const [selected, setSelected] = useEngineDraft<Automation | undefined>(`ui.automation:${project.id}.selected`, undefined);
  const [editing, setEditing] = useEngineDraft(`ui.automation:${project.id}.editing`, false);
  const [error, setError] = useEngineDraft(`form.automationError:${project.id}`, '');
  const runKey = `form.automationRun:${project.id}`;
  const [runOperation, setRunOperation] = useEngineDraft<{ id: string; revision: Automation['revision']; operationId: string } | undefined>(runKey, undefined);
  const [loadError, setLoadError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [clock, setClock] = useState(Date.now());
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState('all');
  const heading = useRef<HTMLHeadingElement>(null);
  const requestSequence = useRef(0);
  const [listOffset, setListOffset] = useState(0);
  const [loadingList, setLoadingList] = useState(false);
  async function load() {
    const sequence = ++requestSequence.current;
    setLoadingList(true);
    try { const [value, topics] = await Promise.all([request<AutomationList>(`${automationPath(project.id)}?offset=${listOffset}`), request<{ items: import('../../engine').Topic[] }>(`/api/projects/${encodeURIComponent(project.id)}/topics?limit=200`)]); if (sequence === requestSequence.current) { setList({ ...value, topics: topics.items }); setLoadError(''); } }
    catch (error) { if (sequence === requestSequence.current) setLoadError(failure(error)); }
    finally { if (sequence === requestSequence.current) setLoadingList(false); }
  }
  useEffect(() => {
    if (!busy && (!editing || !list)) void load();
    const timer = window.setInterval(() => { if (!document.hidden && !busy && !editing) { setClock(Date.now()); void load(); } }, 15000);
    return () => { ++requestSequence.current; window.clearInterval(timer); };
  }, [project.id, busy, editing, listOffset]);
  useEffect(() => { onBusyChange(busy); return () => onBusyChange(false); }, [busy, onBusyChange]);
  useEffect(() => { heading.current?.focus(); }, [selected?.id, editing]);
  async function action(task: () => Promise<unknown>, success: string) {
    if (busy) return;
    const finished = beginLocalActivity('Updating automation');
    setBusy(true); setError(''); setNotice('');
    try { await task(); setNotice(success); }
    catch (error) { setError(failure(error)); }
    finally { setBusy(false); finished(); }
  }
  async function save(v: Automation, newTopic?: { name: string; color: string }) {
    if (busy) return false;
    const finished = beginLocalActivity('Saving automation');
    setBusy(true); setError(''); setNotice('');
    try {
      await flushDrafts();
      const saved = await request<Automation>(automationPath(project.id, v.id), v.id ? 'PATCH' : 'POST', { automation: v, ...(newTopic ? { newTopic } : {}) }, 65000);
      setList(current => current ? { ...current, items: current.items.map(row => row.automation.id === saved.id ? { ...row, automation: saved } : row) } : current);
      setSelected(saved); setEditing(false); setNotice(v.id ? 'Changes saved.' : 'Automation created.'); return true;
    } catch (error) { setError(failure(error)); return false; }
    finally { setBusy(false); finished(); }
  }
  function add() { setSelected(empty(project.id)); setEditing(true); setError(''); setNotice(''); }
  function back() { setSelected(undefined); setEditing(false); setConfirmDelete(false); setError(''); setNotice(''); }
  const row = list?.items.find(row => row.automation.id === selected?.id);
  const current = row?.automation ?? selected;
  const visible = list?.items.filter(({ automation: v }) => (filter === 'all' || (filter === 'active') === v.enabled) && `${v.name} ${triggerSummary(v)} ${sessionSummary(v)}`.toLowerCase().includes(search.trim().toLowerCase())) || [];
  const currentSession = sessions.find(session => session.id === current?.sessionId);
  const issue = error || loadError;
  const toggle = (v: Automation) => void action(() => request(automationPath(project.id, v.id), 'PATCH', { enabled: !v.enabled, revision: v.revision }), `${v.name} ${v.enabled ? 'paused' : 'activated'}.`);
  const run = (v: Automation) => void action(async () => {
    const pending = IS_DESKTOP ? runOperation : undefined;
    if (pending && pending.id !== v.id) throw new Error('Resolve the previous run request before requesting another run.');
    const operation = pending ?? { id: v.id, revision: v.revision, operationId: crypto.randomUUID() };
    let dispatched = false;
    const release = () => { setRunOperation(undefined); forgetDrafts(runKey); };
    try {
      setRunOperation(operation); await flushDrafts();
      dispatched = true;
      await request(`${automationPath(project.id, operation.id)}/run-now`, 'POST', { revision: operation.revision, operationId: operation.operationId });
      release();
    } catch (error) {
      if (releaseRunNowAttempt(!!pending, dispatched, error instanceof EngineRequestError ? error.status : undefined, error instanceof EngineRequestError ? error.message : undefined)) release();
      throw error;
    }
  }, `Run requested for ${v.name}.`);

  return <section className={`automations-settings${editing ? ' is-editing' : ''}`}>
    <header className="automation-page-heading">
      {selected && !editing && <Button variant="ghost" size="sm" disabled={busy} onClick={back}><ArrowLeft aria-hidden="true" />Automations</Button>}
      <div className="automation-title-row"><div><h3 ref={heading} tabIndex={-1}>{editing ? selected?.id ? 'Edit automation' : 'New automation' : selected ? current?.name : 'Automations'}</h3>{!selected && <p>{list ? `${list.items.length} ${list.items.length === 1 ? 'automation' : 'automations'} · ${list.items.filter(row => row.automation.enabled).length} active` : 'Schedules and webhook tasks'}</p>}</div>
        {!selected && !!list?.items.length && <Button variant="primary" size="sm" disabled={busy} onClick={add}><Plus aria-hidden="true" />Add automation</Button>}
        {selected && !editing && current && <Button variant="primary" size="sm" disabled={busy} onClick={() => { setSelected(current); setEditing(true); setConfirmDelete(false); setError(''); setNotice(''); }}>Edit automation</Button>}
      </div>
    </header>
    {issue && <div className="automation-feedback form-error" role="alert"><AlertCircle aria-hidden="true" /><span>{issue}</span>{!editing && <Button size="sm" disabled={busy} onClick={() => { setError(''); void load(); }}>Refresh list</Button>}</div>}
    {notice && !editing && <div className="automation-feedback automation-notice" role="status"><Check aria-hidden="true" /><span>{notice}</span></div>}
    {!list && !issue && <p className="muted" role="status">Loading automations…</p>}
    {list && !selected && <>
      {!!list.items.length && <div className="automation-list-toolbar"><div className="automation-search"><Search aria-hidden="true" /><Input aria-label="Search automations" placeholder="Search automations" value={search} onChange={event => setSearch(event.target.value)} /></div><Select label="Filter by status" variant="field" value={filter} onChange={event => setFilter(event.target.value)}><option value="all">All statuses</option><option value="active">Active</option><option value="paused">Paused</option></Select></div>}
      <div className="automation-content">
        {!list.items.length ? <div className="automation-empty"><Zap aria-hidden="true" /><h4>Your repeatable work, automated.</h4><p>Start with a schedule or a pull request event.</p><Button variant="primary" disabled={busy} onClick={add}><Plus aria-hidden="true" />Add automation</Button></div> : !visible.length ? <div className="automation-empty"><Search aria-hidden="true" /><h4>No matching automations</h4><Button onClick={() => { setSearch(''); setFilter('all'); }}>Clear filters</Button></div> : <div className="automation-list">
          {visible.map(({ automation: v, active, queued, blockedReason }) => <article className="automation-row" key={v.id}>
            <button className="automation-name" disabled={busy} onClick={() => { setSelected(v); setError(''); setNotice(''); }}>
              <span className="automation-row-icon" aria-hidden="true">{v.mode === 'schedule' ? <CalendarClock /> : <GitPullRequest />}</span>
              <span className="automation-row-copy"><strong>{v.name}</strong><span>{triggerSummary(v)}</span><small>{sessionSummary(v)}</small></span><ChevronRight className="automation-row-chevron" aria-hidden="true" />
            </button>
            <div className="automation-row-footer"><div className="automation-row-state"><AutomationStatus enabled={v.enabled} />{!!active && <span>{active} running</span>}{!!queued && <span>{queued} queued</span>}{v.enabled && v.mode === 'schedule' && v.nextScheduledAt && <span title={`${scheduledDate(v.nextScheduledAt, list.timeZone)} · ${list.timeZone}`}>{relativeNextRun(v.nextScheduledAt, clock)}</span>}</div><div className="automation-actions"><Button variant="ghost" size="sm" aria-label={`${v.enabled ? 'Pause' : 'Activate'} ${v.name}`} disabled={busy} onClick={() => toggle(v)}>{v.enabled ? <Pause aria-hidden="true" /> : <Play aria-hidden="true" />}{v.enabled ? 'Pause' : 'Activate'}</Button>{v.mode === 'schedule' && <Button size="sm" aria-label={`Run ${v.name} now`} disabled={busy} onClick={() => run(v)}><Play aria-hidden="true" />Run now</Button>}</div></div>
            {blockedReason && <p className="automation-blocked"><AlertCircle aria-hidden="true" />{blockedReason}</p>}
          </article>)}
        </div>}
      </div>
      <div>{listOffset > 0 && <Button size="sm" disabled={loadingList} onClick={() => setListOffset(0)}>First automations</Button>}{list.hasMore && <Button size="sm" disabled={loadingList} onClick={() => setListOffset(list.nextOffset ?? listOffset + list.items.length)}>Next automations</Button>}</div>
      {!!list.items.length && <p className="automation-timezone"><CalendarClock aria-hidden="true" />Schedule time zone · {list.timeZone}</p>}
    </>}
    {selected && editing && list && <AutomationEditor key={`${selected.id}/${selected.revision}`} initial={selected} project={project} sessions={sessions} topics={list.topics} zone={list.timeZone} busy={busy} onSave={save} onCancel={() => { setEditing(false); setError(''); if (!selected.id) setSelected(undefined); }} />}
    {selected && !editing && current && <div className="automation-content automation-detail">
      <div className="automation-detail-controls"><AutomationStatus enabled={current.enabled} /><div className="automation-actions"><Button size="sm" disabled={busy} onClick={() => toggle(current)}>{current.enabled ? <Pause aria-hidden="true" /> : <Play aria-hidden="true" />}{current.enabled ? 'Pause' : 'Activate'}</Button>{current.mode === 'schedule' && <Button size="sm" disabled={busy} onClick={() => run(current)}><Play aria-hidden="true" />Run now</Button>}</div></div>
      {row?.blockedReason && <p className="automation-blocked"><AlertCircle aria-hidden="true" />{row.blockedReason}</p>}
      <dl className="automation-facts"><div><dt>Activation</dt><dd>{triggerSummary(current)}{current.mode === 'schedule' && list && <small>{list.timeZone}</small>}</dd></div><div><dt>Session</dt><dd>{sessionSummary(current)}{current.sessionId && <Button variant="ghost" size="sm" onClick={() => onOpenSession(current.sessionId!)}>{currentSession?.title || 'Open linked session'}<ArrowUpRight aria-hidden="true" /></Button>}</dd></div>{current.enabled && current.nextScheduledAt && list && <div><dt>Next run</dt><dd>{scheduledDate(current.nextScheduledAt, list.timeZone)}</dd></div>}</dl>
      <details className="automation-disclosure"><summary><span>Task prompts</span></summary><div className="automation-disclosure-body">{current.prompt && <div><h4>Initial prompt</h4><p className="automation-prompt-text">{current.prompt}</p></div>}{current.followUpPrompt && <div><h4>Follow-up prompt</h4><p className="automation-prompt-text">{current.followUpPrompt}</p></div>}</div></details>
      <section className="automation-history-section"><AutomationSessionHistory key={`${project.id}/${selected.id}`} projectId={project.id} automationId={selected.id} onOpenSession={onOpenSession} /></section>
      <div className="automation-delete-area">{confirmDelete ? <div className="automation-confirm" role="alert"><h4>Delete “{current.name}”?</h4><p>Pending triggers will be cancelled. Active runs and existing sessions are preserved.</p><div><Button disabled={busy} onClick={() => setConfirmDelete(false)}>Keep automation</Button><Button variant="danger-soft" disabled={busy} onClick={() => void action(async () => { await request(automationPath(project.id, current.id), 'DELETE', { revision: current.revision }); setSelected(undefined); setConfirmDelete(false); }, 'Automation deleted.')}>Delete automation</Button></div></div> : <Button variant="ghost" size="sm" className="automation-delete" disabled={busy} onClick={() => setConfirmDelete(true)}><Trash2 aria-hidden="true" />Delete automation</Button>}</div>
    </div>}
  </section>;
}

function AutomationStatus({ enabled }: { enabled: boolean }) {
  return <span className={`automation-status ${enabled ? 'is-active' : ''}`}><span aria-hidden="true" />{enabled ? 'Active' : 'Paused'}</span>;
}

function AutomationSessionHistory({ projectId, automationId, onOpenSession }: { projectId: string; automationId: string; onOpenSession: (id: string) => void }) {
  const [history, setHistory] = useState<AutomationRunLink[]>([]);
  const [pages, setPages] = useState(1);
  const [more, setMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController(); let inFlight = false;
    async function refresh() {
      if (inFlight) return; inFlight = true; setLoading(true);
      try {
        const items: AutomationRunLink[] = []; let hasMore = true;
        for (let pageIndex = 0; pageIndex < pages && hasMore; pageIndex++) {
          const page = await request<{ items: AutomationRunLink[]; hasMore: boolean }>(`${automationPath(projectId, automationId)}/runs?offset=${items.length}`, 'GET', undefined, 15000, controller.signal);
          items.push(...page.items); hasMore = page.hasMore;
        }
        if (!controller.signal.aborted) { setHistory(items); setMore(hasMore); setError(''); }
      } catch (error) { if (!controller.signal.aborted) setError(failure(error)); }
      finally { inFlight = false; if (!controller.signal.aborted) setLoading(false); }
    }
    void refresh(); const timer = window.setInterval(() => { if (!document.hidden) void refresh(); }, 15000);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [projectId, automationId, pages, attempt]);
  return <>
    <h4>Run history</h4>
    {error && <div className="form-error" role="alert">{error}<Button size="sm" onClick={() => setAttempt(n => n + 1)}>Retry</Button></div>}
    {!history.length && !error && <p className="muted" role="status">{loading ? 'Loading…' : 'No sessions yet.'}</p>}
    <div className="automation-history">{history.map(session => <Button key={session.runId} variant="ghost" onClick={() => onOpenSession(session.sessionId)}><span>{session.title}</span><ArrowUpRight aria-hidden="true" /></Button>)}</div>
    {more && <Button size="sm" disabled={loading} onClick={() => setPages(current => current + 1)}>{loading ? 'Loading…' : 'Load more'}</Button>}
  </>;
}
