import { useEffect, useRef, useState } from 'react';
import { ArrowLeft, ArrowRight, CalendarClock, Check, GitPullRequest, Info, Plus } from 'lucide-react';
import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Select } from '../../design-system/Select';
import { Toggle } from '../../design-system/Toggle';
import { getAuthoringCatalog, request, type Project, type Session, type Topic } from '../../engine';
import { CapabilitySelector } from '../library/CapabilitySelector';
import { useModelCatalog } from '../chat/modelCatalog';
import { topicColors } from '../workspace/topics';
import { automationPath, type Automation, type AutomationList } from './api';
import { useTopicPage } from '../workspace/useTopicPage';
import { topicArchived } from '../workspace/topics';
import { scheduledDate, sessionModes, triggerSummary, weekdays } from './presentation';
import { bindDraftDiscard, useEngineDraft } from '../drafts/draftStore';

const steps = ['Activation', 'Task', 'Configuration'];
const references = [
  { token: '{{pr.number}}', name: 'PR number', meaning: 'Pull request number', example: '42' },
  { token: '{{organization.name}}', name: 'Organization', meaning: 'Organization login; empty when absent', example: 'octo-org' },
  { token: '{{repository.name}}', name: 'Repository', meaning: 'Repository name', example: 'hello-world' },
];
const failure = (error: unknown) => error instanceof Error ? error.message : 'Could not load configuration.';

export function AutomationEditor({ initial: incoming, project, sessions, topics, zone, busy, onSave, onCancel }: {
  initial: Automation; project: Project; sessions: Session[]; topics: AutomationList['topics']; zone: string; busy: boolean;
  onSave: (v: Automation, topic?: { name: string; color: string }) => Promise<boolean>; onCancel: () => void;
}) {
  const key = `form.automation:${project.id}:${incoming.id || 'new'}`;
  const forgetDrafts = bindDraftDiscard();
  const [initial] = useEngineDraft(`${key}.base`, incoming, true);
  const [draft, setDraft] = useEngineDraft(`${key}.values`, initial, true);
  const [step, setStep] = useEngineDraft(`${key}.step`, 0);
  const [topicMode, setTopicMode] = useEngineDraft(`${key}.topicMode`, initial.topicId ? 'existing' : 'new');
  const [topicName, setTopicName] = useEngineDraft(`${key}.topicName`, '');
  const [topicColor, setTopicColor] = useEngineDraft(`${key}.topicColor`, 'neutral');
  const [topicSearch, setTopicSearch] = useState('');
  const [sessionSearch, setSessionSearch] = useState('');
  const [sessionQuery, setSessionQuery] = useState('');
  const [sessionCursor, setSessionCursor] = useState('');
  const [sessionPage, setSessionPage] = useState<{ items: Session[]; nextCursor?: string }>({ items: [] });
  const [sessionLoadError, setSessionLoadError] = useState('');
  const [exactDestination, setExactDestination] = useState<Session>();
  const [graphs, setGraphs] = useState<{ id: string; name: string }[]>([]);
  const [catalogError, setCatalogError] = useState('');
  const [catalogAttempt, setCatalogAttempt] = useState(0);
  const [catalogLoading, setCatalogLoading] = useState(true);
  const [preview, setPreview] = useState('');
  const [previewError, setPreviewError] = useState('');
  const [previewLoading, setPreviewLoading] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const [validation, setValidation] = useState('');
  const form = useRef<HTMLFormElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const prompt = useRef<HTMLTextAreaElement>(null);
  const followUp = useRef<HTMLTextAreaElement>(null);
  const [promptField, setPromptField] = useEngineDraft<'prompt' | 'followUpPrompt'>(`${key}.promptField`, 'prompt');
  const sessionMode = draft.sessionMode || 'new_per_run';
  const usesSession = sessionMode === 'existing' || sessionMode === 'reuse_created' && !!draft.sessionId;
  const creating = !usesSession;
  const linked = !!initial.sessionId && (initial.sessionMode || 'new_per_run') !== 'new_per_run';
  const destinations = [...new Map([...sessionPage.items, ...(exactDestination ? [exactDestination] : [])].filter(s => s.projectId === project.id && !s.role && !s.parentId && !s.archived && !topicArchived(project,s)).map(s => [s.id,s])).values()];
  const topicPage = useTopicPage(project.id, creating && step === 2, topicSearch);
  const [pickedTopic, setPickedTopic] = useState(topics.find(t => t.id === initial.topicId));
  const topicOptions = [...new Map([...topicPage.items, ...(pickedTopic ? [pickedTopic] : [])].filter(t => !t.archived).map(t => [t.id,t])).values()];
  const destination = exactDestination?.id === draft.sessionId ? exactDestination : sessions.find(s => s.id === draft.sessionId);
  useEffect(() => { const timer = setTimeout(() => { setSessionQuery(sessionSearch); setSessionCursor(''); }, 200); return () => clearTimeout(timer); }, [sessionSearch]);
  useEffect(() => {
    if (sessionMode !== 'existing') return;
    const controller = new AbortController(); setSessionLoadError('');
    const query = new URLSearchParams({ search: sessionQuery, limit: '100', ...(sessionCursor ? { cursor: sessionCursor } : {}) });
    void request<{ items: Session[]; nextCursor?: string }>(`/api/projects/${encodeURIComponent(project.id)}/sessions?${query}`, 'GET', undefined, 15000, controller.signal).then(value => { if (!controller.signal.aborted) setSessionPage(value); }).catch(error => { if (!controller.signal.aborted) setSessionLoadError(failure(error)); });
    return () => controller.abort();
  }, [project.id, sessionMode, sessionQuery, sessionCursor]);
  useEffect(() => {
    if (!draft.sessionId) { setExactDestination(undefined); return; }
    const controller = new AbortController();
    void request<Session>(`/api/sessions/${encodeURIComponent(draft.sessionId)}`, 'GET', undefined, 15000, controller.signal).then(value => { if (!controller.signal.aborted) setExactDestination(value); }).catch(error => { if (!controller.signal.aborted) setSessionLoadError(failure(error)); });
    return () => controller.abort();
  }, [draft.sessionId]);
  useEffect(() => {
    if (!initial.topicId || topics.some(t => t.id === initial.topicId)) return;
    const controller = new AbortController();
    void request<Topic>(`/api/projects/${encodeURIComponent(project.id)}/topics/${encodeURIComponent(initial.topicId)}`, 'GET', undefined, 15000, controller.signal).then(value => { if (!controller.signal.aborted) setPickedTopic(value); }).catch(error => { if (!controller.signal.aborted) setValidation(failure(error)); });
    return () => controller.abort();
  }, [project.id, initial.topicId]);
  const { catalog, loading, error: modelError, refresh } = useModelCatalog(project.id, draft.harness, undefined, creating);
  const patch = (value: Partial<Automation>) => setDraft(current => ({ ...current, ...value }));
  const schedule = (value: Partial<Automation['schedule']>) => patch({ schedule: { ...draft.schedule, ...value, anchor: undefined } });
  const dirty = JSON.stringify(draft) !== JSON.stringify(initial) || !!topicName || topicColor !== 'neutral' || topicMode !== (initial.topicId ? 'existing' : 'new');

  useEffect(() => {
    let live = true;
    setCatalogLoading(true); setCatalogError('');
    getAuthoringCatalog(project.id).then(value => {
      if (live) setGraphs(value.graphs.filter(g => g.definition.enabled).map(g => ({ id: g.id, name: g.definition.name })));
    }, error => { if (live) setCatalogError(failure(error)); }).finally(() => { if (live) setCatalogLoading(false); });
    return () => { live = false; };
  }, [project.id, catalogAttempt]);
  useEffect(() => {
    if (draft.mode !== 'schedule') return;
    const controller = new AbortController();
    setPreview(''); setPreviewError(''); setPreviewLoading(true);
    const timer = window.setTimeout(() => {
      request<{ nextScheduledAt: string }>(`${automationPath(project.id)}/preview`, 'POST', draft.schedule, 15000, controller.signal).then(value => {
        if (!controller.signal.aborted) { setPreview(value.nextScheduledAt); setPreviewLoading(false); }
      }, error => {
        if (!controller.signal.aborted) { setPreviewError(failure(error)); setPreviewLoading(false); }
      });
    }, 250);
    return () => { window.clearTimeout(timer); controller.abort(); };
  }, [draft.schedule, draft.mode, project.id, zone]);
  useEffect(() => { heading.current?.focus(); form.current?.querySelector('.automation-editor-body')?.scrollTo(0, 0); }, [step]);

  function insert(token: string) {
    const field = !creating ? 'followUpPrompt' : sessionMode === 'new_per_run' ? 'prompt' : promptField;
    const editor = field === 'prompt' ? prompt.current : followUp.current;
    const text = draft[field] || '';
    const start = editor?.selectionStart ?? text.length, end = editor?.selectionEnd ?? start;
    patch({ [field]: text.slice(0, start) + token + text.slice(end) });
    requestAnimationFrame(() => { editor?.focus(); editor?.setSelectionRange(start + token.length, start + token.length); });
  }
  const option = catalog?.models.find(model => model.id === draft.model || !draft.model && model.id === catalog.defaultModel);
  const initialReferences = draft.mode === 'schedule' && creating && references.some(ref => draft.prompt.includes(ref.token));
  const followUpReferences = draft.mode === 'schedule' && sessionMode !== 'new_per_run' && references.some(ref => (draft.followUpPrompt || '').includes(ref.token));

  function validate(all = false) {
    setValidation('');
    const panels = form.current?.querySelectorAll<HTMLElement>('[data-editor-step]');
    for (const panel of panels || []) {
      const index = Number(panel.dataset.editorStep);
      if (!all && index !== step) continue;
      const fields = panel.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>('input, textarea, select');
      for (const field of fields) {
        field.setCustomValidity('');
        if (field.required && !field.value.trim()) field.setCustomValidity('Complete this field.');
        if (field === prompt.current && initialReferences || field === followUp.current && followUpReferences) field.setCustomValidity('Remove webhook references from this scheduled prompt.');
        if (!field.checkValidity()) {
          setStep(index);
          setValidation('Check the highlighted field before continuing.');
          requestAnimationFrame(() => { field.focus(); field.reportValidity(); });
          return false;
        }
      }
      if (index === 0 && draft.mode === 'schedule' && draft.schedule.recurrence === 'weekly' && !draft.schedule.weekdays.length) {
        setStep(0); setValidation('Choose at least one weekday.');
        requestAnimationFrame(() => form.current?.querySelector<HTMLButtonElement>('.automation-weekdays button')?.focus());
        return false;
      }
    }
    return true;
  }
  function changeStep(next: number) { if (busy) return; setValidation(''); setStep(next); }
  const libraryCount = `${draft.selection.skills.length} skills · ${draft.selection.mcpConnections.length} MCP connections`;

  return <form ref={form} className="automation-editor" noValidate onSubmit={event => {
    event.preventDefault();
    if (busy) return;
    if (step < 2) { if (validate()) changeStep(step + 1); }
    else if (validate(true)) void onSave(draft, creating && topicMode === 'new' ? { name: topicName.trim(), color: topicColor } : undefined).then(saved => { if (saved) forgetDrafts(key); }).catch(error => setValidation(failure(error)));
  }} onInput={event => {
    if (event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement || event.target instanceof HTMLSelectElement) event.target.setCustomValidity('');
  }}>
    <nav className="automation-steps" aria-label="Automation setup">
      {steps.map((label, index) => <button key={label} type="button" disabled={busy} aria-current={step === index ? 'step' : undefined} onClick={() => changeStep(index)}>
        <span className="automation-step-number" aria-hidden="true">{index + 1}</span><span>{label}</span>
      </button>)}
    </nav>
    <div className="automation-editor-body">
      <h4 ref={heading} tabIndex={-1} className="automation-step-heading">{['When should it run?', 'What should it do?', 'How should it run?'][step]}</h4>
      {validation && <p className="form-error" role="alert">{validation}</p>}
      <fieldset disabled={busy}>
        <div data-editor-step="0" hidden={step !== 0} className="automation-step-fields">
          <label className="automation-field">Automation name<Input required maxLength={200} placeholder="e.g. Daily code review" value={draft.name} onChange={event => patch({ name: event.target.value })} /></label>
          <div className="automation-field"><span>Activation</span>
            <div className="automation-trigger-options" role="group" aria-label="Activation">
              <Button className="automation-trigger-option" aria-pressed={draft.mode === 'schedule'} onClick={() => patch({ mode: 'schedule', parallel: false })}><CalendarClock aria-hidden="true" /><span><strong>Schedule</strong><small>At a specific time</small></span>{draft.mode === 'schedule' && <Check aria-hidden="true" />}</Button>
              <Button className="automation-trigger-option" aria-pressed={draft.mode === 'webhook'} onClick={() => patch({ mode: 'webhook', parallel: false })}><GitPullRequest aria-hidden="true" /><span><strong>Webhook</strong><small>When an event arrives</small></span>{draft.mode === 'webhook' && <Check aria-hidden="true" />}</Button>
            </div>
          </div>
          {draft.mode === 'webhook' ? <div className="automation-trigger-description"><GitPullRequest aria-hidden="true" /><div><strong>Pull request opened</strong><p>From GitHub webhooks connected to this project.</p></div></div> : <>
            <div className="automation-field-grid">
              <label className="automation-field">Repeat<Select label="Recurrence" variant="field" value={draft.schedule.recurrence} onChange={event => schedule({ recurrence: event.target.value as Automation['schedule']['recurrence'] })}><option value="once">Once</option><option value="daily">Daily</option><option value="every_days">Every N days</option><option value="weekly">Weekly</option><option value="monthly">Monthly</option></Select></label>
              <label className="automation-field">Time<Input type="time" required value={draft.schedule.time} onChange={event => schedule({ time: event.target.value })} /></label>
            </div>
            {draft.schedule.recurrence === 'once' && <label className="automation-field">Date<Input type="date" required value={draft.schedule.date || ''} onChange={event => schedule({ date: event.target.value })} /></label>}
            {draft.schedule.recurrence === 'every_days' && <label className="automation-field">Interval in days<Input type="number" min={1} max={365} required value={draft.schedule.everyDays || ''} onChange={event => schedule({ everyDays: Number(event.target.value) })} /></label>}
            {draft.schedule.recurrence === 'weekly' && <div className="automation-field"><span>Run on</span><div className="automation-weekdays" role="group" aria-label="Weekdays">{weekdays.map((day, index) => <Button key={day} aria-label={day} aria-pressed={draft.schedule.weekdays.includes(index)} onClick={() => schedule({ weekdays: draft.schedule.weekdays.includes(index) ? draft.schedule.weekdays.filter(d => d !== index) : [...draft.schedule.weekdays, index] })}>{day.slice(0, 3)}</Button>)}</div></div>}
            {draft.schedule.recurrence === 'monthly' && <label className="automation-field">Day of month<Input type="number" min={1} max={31} required value={draft.schedule.monthDay || ''} onChange={event => schedule({ monthDay: Number(event.target.value) })} /><small>Months without this day are skipped.</small></label>}
            <div className="automation-schedule-preview"><CalendarClock aria-hidden="true" /><div><span>Time zone · {zone}</span><strong aria-live="polite">{previewLoading ? 'Calculating next run…' : preview ? `Next · ${scheduledDate(preview, zone)}` : previewError ? 'Schedule needs attention' : 'No upcoming occurrence'}</strong></div></div>
            {previewError && <p className="form-error" role="status">{previewError}</p>}
          </>}
        </div>

        <div data-editor-step="1" hidden={step !== 1} className="automation-step-fields">
          <label className="automation-field">Session<Select label="Session mode" variant="field" disabled={linked} value={sessionMode} onChange={event => patch({ sessionMode: event.target.value as Automation['sessionMode'], sessionId: '', parallel: false })}>{sessionModes.map(mode => <option key={mode.value} value={mode.value}>{mode.name}</option>)}</Select><small>{linked ? 'This automation is linked to a fixed session.' : sessionModes.find(mode => mode.value === sessionMode)?.description}</small></label>
          {sessionMode === 'existing' && <><label className="automation-field">Search sessions<Input aria-label="Search automation sessions" disabled={linked} value={sessionSearch} onChange={event => setSessionSearch(event.target.value)} /></label><label className="automation-field">Destination<Select label="Existing session" variant="field" required disabled={linked} value={draft.sessionId || ''} onChange={event => patch({ sessionId: event.target.value })}><option value="">Choose a session</option>{destinations.map(s => <option key={s.id} value={s.id}>{s.title}{s.status === 'running' ? ' · Busy' : ''}</option>)}{draft.sessionId && !destinations.some(s => s.id === draft.sessionId) && <option value={draft.sessionId}>{destination?.title || 'Linked session'} · Unavailable</option>}</Select></label><div>{sessionCursor && <Button size="sm" onClick={() => setSessionCursor('')}>First sessions</Button>}{sessionPage.nextCursor && <Button size="sm" onClick={() => setSessionCursor(sessionPage.nextCursor ?? '')}>Next sessions</Button>}{sessionLoadError && <p role="alert" className="form-error">{sessionLoadError}</p>}</div></>}
          {usesSession && <div className="automation-note"><Info aria-hidden="true" /><p>Runs use the linked session’s current settings and wait until it is free.</p></div>}
          {draft.mode === 'webhook' && <div className="automation-reference-bar"><span>Insert into {creating && sessionMode !== 'new_per_run' ? promptField === 'prompt' ? 'initial prompt' : 'follow-up prompt' : 'prompt'}</span><div>{references.map(ref => <Button key={ref.token} size="sm" title={`${ref.meaning} · Example: ${ref.example}`} onClick={() => insert(ref.token)}><Plus aria-hidden="true" />{ref.name}</Button>)}</div></div>}
          {creating && <label className="automation-field">Initial prompt<textarea className="input textarea" ref={prompt} required placeholder="Describe the task and what a successful result looks like." onFocus={() => setPromptField('prompt')} aria-invalid={initialReferences || undefined} aria-describedby={initialReferences ? 'automation-prompt-references-error' : undefined} value={draft.prompt} onChange={event => patch({ prompt: event.target.value })} rows={6} />{sessionMode !== 'new_per_run' && <small>Sent only when the session is created.</small>}</label>}
          {sessionMode !== 'new_per_run' && <label className="automation-field">Follow-up prompt<textarea className="input textarea" ref={followUp} required placeholder="Describe the task for each run in this conversation." onFocus={() => setPromptField('followUpPrompt')} aria-invalid={followUpReferences || undefined} aria-describedby={followUpReferences ? 'automation-prompt-references-error' : undefined} value={draft.followUpPrompt || ''} onChange={event => patch({ followUpPrompt: event.target.value })} rows={6} /><small>{sessionMode === 'existing' ? 'Sent on every run, including the first.' : 'Sent on every run after the session is created.'}</small></label>}
          {(initialReferences || followUpReferences) && <p id="automation-prompt-references-error" className="form-error" role="alert">Remove PR, organization and repository references from scheduled prompts, or switch activation to Webhook.</p>}
          {draft.mode === 'webhook' && sessionMode === 'new_per_run' && <div className="automation-setting"><Toggle label="Allow parallel runs" checked={draft.parallel} onCheckedChange={parallel => patch({ parallel })} /><p>Accept new runs while another is still working.</p></div>}
        </div>

        <div data-editor-step="2" hidden={step !== 2} className="automation-step-fields">
          {creating ? <>
            <div className="automation-field-grid">
              <label className="automation-field">Harness<Select label="Harness" variant="field" value={draft.harness} onChange={event => patch({ harness: event.target.value as Automation['harness'], model: '', effort: '' })}><option value="pi">Pi</option><option value="opencode">OpenCode</option><option value="codex">Codex</option></Select></label>
              <label className="automation-field">Effort<Select label="Effort" variant="field" value={draft.effort} disabled={loading} onChange={event => patch({ effort: event.target.value })}><option value="">Default</option>{draft.effort && !option?.efforts.includes(draft.effort) && <option value={draft.effort}>{draft.effort} · Unavailable</option>}{option?.efforts.map(effort => <option key={effort} value={effort}>{effort}</option>)}</Select></label>
            </div>
            <label className="automation-field">Model<Select label="Model" variant="field" value={draft.model} disabled={loading} onChange={event => patch({ model: event.target.value, effort: '' })}><option value="">{loading ? 'Loading models…' : `Harness default${catalog?.defaultModel ? ` · ${catalog.defaultModel}` : ''}`}</option>{draft.model && !catalog?.models.some(model => model.id === draft.model) && <option value={draft.model}>{draft.model} · Unavailable</option>}{catalog?.models.map(model => <option key={model.id} value={model.id}>{model.name || model.id}</option>)}</Select></label>
            {modelError && <div className="form-error" role="alert">{modelError}<Button size="sm" onClick={() => void refresh()}>Reload models</Button></div>}
            <label className="automation-field">Search topics<Input aria-label="Search automation topics" value={topicSearch} onChange={event => setTopicSearch(event.target.value)} /></label>
            <label className="automation-field">Topic<Select label="Topic" variant="field" value={topicMode === 'new' ? 'new' : draft.topicId} onChange={event => { if (event.target.value === 'new') setTopicMode('new'); else { setTopicMode('existing'); setPickedTopic(topicOptions.find(t => t.id === event.target.value)); patch({ topicId: event.target.value }); } }}><option value="new">Create a new topic</option>{topicOptions.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}{topicMode === 'existing' && !topicOptions.some(t => t.id === draft.topicId) && <option value={draft.topicId}>Saved topic · {draft.topicId}</option>}</Select></label>
            <div>{topicPage.older && <Button size="sm" onClick={topicPage.first}>First topics</Button>}{topicPage.nextCursor && <Button size="sm" disabled={topicPage.loading} onClick={topicPage.next}>Next topics</Button>}{topicPage.error && <p role="alert" className="form-error">{topicPage.error}<Button size="sm" onClick={topicPage.first}>Reload topics</Button></p>}</div>
            {topicMode === 'new' && <div className="automation-new-topic"><label className="automation-field">Topic name<Input required maxLength={60} placeholder="e.g. Code reviews" value={topicName} onChange={event => setTopicName(event.target.value)} /></label><div className="topic-color-picker" role="group" aria-label="Topic color">{topicColors.map(color => <Button key={color} size="sm" className={`topic-color-chip topic-color-${color}`} aria-pressed={topicColor === color} onClick={() => setTopicColor(color)}><span className="topic-color-swatch" aria-hidden="true" />{color[0].toUpperCase() + color.slice(1)}</Button>)}</div></div>}
            <div className="automation-setting"><Toggle label="YOLO mode" checked={draft.yolo} onCheckedChange={yolo => patch({ yolo })} /><p>Automatically approve tool permissions. Questions still require a reply.</p></div>
          </> : <div className="automation-note"><Info aria-hidden="true" /><div><strong>{destination?.title || 'Linked session'}</strong><p>{destination?.harness || draft.harness} · {destination?.model || 'Harness default'}{destination?.effort ? ` · ${destination.effort}` : ''}{destination?.yolo ? ' · YOLO' : ''}</p><p>Session settings and Library are managed in the conversation.</p></div></div>}
          <details className="automation-disclosure"><summary><span>Enabled graphs</span><small>{draft.graphs.length} selected</small></summary><div className="automation-disclosure-body">
            {catalogLoading && <p className="muted" role="status">Loading graphs…</p>}
            {catalogError && <div className="form-error" role="alert">{catalogError}<Button size="sm" onClick={() => setCatalogAttempt(n => n + 1)}>Reload graphs</Button></div>}
            {!catalogLoading && !catalogError && !graphs.length && <p className="muted">No enabled graphs in this project.</p>}
            {graphs.map(g => <Toggle key={g.id} label={g.name} checked={draft.graphs.includes(g.id)} onCheckedChange={checked => patch({ graphs: checked ? [...draft.graphs, g.id] : draft.graphs.filter(id => id !== g.id) })} />)}
            {!catalogLoading && !catalogError && draft.graphs.filter(id => !graphs.some(g => g.id === id)).map(id => <Toggle key={id} label={`${id} · Unavailable`} checked onCheckedChange={() => patch({ graphs: draft.graphs.filter(g => g !== id) })} />)}
          </div></details>
          {creating && <details className="automation-disclosure"><summary><span>Library</span><small>{libraryCount}</small></summary><div className="automation-disclosure-body"><CapabilitySelector value={draft.selection} onChange={selection => patch({ selection })} harness={draft.harness} disabled={busy} /></div></details>}
          <div className="automation-save-summary"><strong>{draft.name || 'Untitled automation'}</strong><span>{triggerSummary(draft)}</span><Toggle label={draft.enabled ? 'Active after saving' : 'Paused after saving'} checked={draft.enabled} onCheckedChange={enabled => patch({ enabled })} /></div>
        </div>
      </fieldset>
    </div>
    {confirmDiscard ? <div className="automation-discard" role="alert"><p>Discard your unsaved changes?</p><div><Button disabled={busy} onClick={() => setConfirmDiscard(false)}>Keep editing</Button><Button variant="danger-soft" disabled={busy} onClick={() => { forgetDrafts(key); onCancel(); }}>Discard changes</Button></div></div> : <footer className="automation-editor-footer">
      <Button variant="ghost" disabled={busy} onClick={() => { if (dirty) setConfirmDiscard(true); else { forgetDrafts(key); onCancel(); } }}>Cancel</Button>
      <div>{step > 0 && <Button disabled={busy} onClick={() => changeStep(step - 1)}><ArrowLeft aria-hidden="true" />Back</Button>}<Button variant="primary" type="submit" disabled={busy || step === 2 && creating && loading}>{busy ? 'Saving…' : step < 2 ? 'Continue' : initial.id ? 'Save changes' : 'Create automation'}{step < 2 && <ArrowRight aria-hidden="true" />}</Button></div>
    </footer>}
  </form>;
}
