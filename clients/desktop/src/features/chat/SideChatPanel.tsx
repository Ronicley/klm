import { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import type { EventPage, Harness, Project, Session, SessionResponse, SourceReference } from '../../engine';
import { HistoryLoader } from './HistoryLoader';
import { ConversationEvents } from './ConversationEvents';
import { HarnessIcon } from './HarnessIcon';
import { MessageQueue } from './MessageQueue';
import { MessageComposer, type ComposerDraft, type SendMode } from './MessageComposer';
import { ModelPicker } from './ModelPicker';
import { PermissionCard } from './PermissionCard';
import { QuestionCard } from './QuestionCard';
import { SessionStatusBar } from './SessionStatusBar';
import { PendingSubmission, type LocalSubmission } from './PendingSubmission';

const minimumSideWidth = 304;
const minimumMainWidth = 400;

export function SideChatPanel({ session, project, harnesses, draft, sources, submissions, onRestoreSubmission, onRetrySubmission, error, disconnected, pending, onClose, onRetry, onSnapshot, onHistory, onEventChange, onDraftChange, onRemoveSource, onSend, onStop, onHarness, onModel }: {
  session?: Session; project: Project; harnesses: Harness[]; draft: ComposerDraft; sources: SourceReference[];
  error: string; disconnected: boolean; pending: boolean; onClose: () => void; onRetry: () => void;
  submissions: LocalSubmission[]; onRestoreSubmission: (item: LocalSubmission) => void; onRetrySubmission: (item: LocalSubmission) => void;
  onSnapshot: (session: SessionResponse) => void; onHistory: (page: EventPage) => void; onDraftChange: (draft: ComposerDraft) => void;
  onEventChange: (sessionId: string, event: import('../../engine').EngineEvent) => void;
  onRemoveSource: (index: number) => void; onSend: (draft: ComposerDraft, mode?: SendMode) => boolean; onStop: () => void;
  onHarness: (harness: Harness['id']) => void; onModel: (model: string, effort: string) => Promise<boolean>;
}) {
  const panel = useRef<HTMLElement>(null);
  const history = useRef<HTMLDivElement>(null);
  const followingHistory = useRef(true);
  const [savedWidth, setSavedWidth] = useState(() => {
    try { const value = Number(localStorage.getItem('klm.side-agent.width.v1')); return Number.isFinite(value) && value >= minimumSideWidth ? Math.min(value, 900) : 352; }
    catch { return 352; }
  });
  const [available, setAvailable] = useState(1000);
  const resize = useRef<{ x: number; width: number } | null>(null);
  const maximum = Math.max(minimumSideWidth, available - minimumMainWidth - 6);
  const width = Math.min(savedWidth, maximum);
  const narrow = available < minimumMainWidth + minimumSideWidth + 6;
  useEffect(() => {
    const shell = panel.current?.parentElement;
    if (!shell) return;
    const measure = () => {
      const main = shell.querySelector<HTMLElement>('.main-panel');
      if (main) setAvailable(shell.getBoundingClientRect().right - main.getBoundingClientRect().left - 1);
    };
    measure();
    const observer = new ResizeObserver(measure); observer.observe(shell);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    try { localStorage.setItem('klm.side-agent.width.v1', String(savedWidth)); } catch { /* Width still works without browser storage. */ }
  }, [savedWidth]);
  useEffect(() => {
    if (!narrow || !panel.current) return;
    const target = panel.current;
    const previous = document.activeElement as HTMLElement | null;
    const siblings = Array.from(target.parentElement?.children ?? []).filter((item): item is HTMLElement => item instanceof HTMLElement && item !== target && !item.matches('dialog'));
    siblings.forEach(item => { item.inert = true; });
    const focusable = () => Array.from(target.querySelectorAll<HTMLElement>('button:not(:disabled), [contenteditable="true"], input, select:not(:disabled), summary, [tabindex="0"]')).filter(item => item.getClientRects().length > 0);
    focusable()[0]?.focus();
    const keydown = (event: KeyboardEvent) => {
      // The composer's suggestions own these keys before the panel focus trap.
      if (event.target instanceof HTMLElement && event.target.closest('.composer-editor')?.getAttribute('aria-expanded') === 'true' && (event.key === 'Escape' || (event.key === 'Tab' && !event.shiftKey))) return;
      if (event.key === 'Escape') { event.preventDefault(); onClose(); }
      if (event.key !== 'Tab') return;
      const items = focusable(); const first = items[0]; const last = items[items.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    };
    target.addEventListener('keydown', keydown);
    return () => { siblings.forEach(item => { item.inert = false; }); target.removeEventListener('keydown', keydown); previous?.focus(); };
  }, [narrow]);
  useEffect(() => {
    if (followingHistory.current && history.current) history.current.scrollTo({ top: history.current.scrollHeight });
  }, [session?.updatedAt, session?.history?.total, !!session?.history]);
  useEffect(() => { if (sources.length) panel.current?.querySelector<HTMLElement>('.composer-editor')?.focus(); }, [sources.length, session?.id]);
  const running = session?.status === 'running';
  const permissions = !!session?.permissions?.length;
  const questions = !!session?.questions?.length;
  const disabled = disconnected || pending || running;
  return <>
    {!narrow && <div className="side-chat-divider" role="separator" aria-label="Resize side agent" aria-orientation="vertical" aria-valuemin={minimumSideWidth} aria-valuemax={Math.round(maximum)} aria-valuenow={Math.round(width)} tabIndex={0}
      onPointerDown={event => { if (event.button !== 0) return; resize.current = { x: event.clientX, width }; event.currentTarget.setPointerCapture(event.pointerId); event.preventDefault(); }}
      onPointerMove={event => { if (resize.current) setSavedWidth(Math.max(minimumSideWidth, Math.min(maximum, resize.current.width + resize.current.x - event.clientX))); }}
      onPointerUp={event => { if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId); resize.current = null; }}
      onLostPointerCapture={() => { resize.current = null; }} onPointerCancel={() => { resize.current = null; }}
      onKeyDown={event => {
        if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
        event.preventDefault();
        setSavedWidth(event.key === 'Home' ? minimumSideWidth : event.key === 'End' ? maximum : Math.max(minimumSideWidth, Math.min(maximum, width + (event.key === 'ArrowLeft' ? 24 : -24))));
      }} />}
    <aside ref={panel} className={`side-chat-panel ${narrow ? 'side-chat-panel--full' : ''}`} style={{ width: narrow ? undefined : width }} role={narrow ? 'dialog' : undefined} aria-modal={narrow || undefined} aria-label="Side agent">
      <header className="side-chat-header"><h2>Side agent</h2><IconButton label="Close side agent" onClick={onClose}><X /></IconButton></header>
      <div className="chat-history" ref={history} role="log" aria-label="Side conversation" aria-live="polite" onScroll={event => {
        const element = event.currentTarget;
        followingHistory.current = element.scrollHeight - element.scrollTop - element.clientHeight < 24;
      }}>
        {session ? <><HistoryLoader key={session.id} session={session} onPage={onHistory} /><ConversationEvents session={session} working={running || pending} onSnapshot={onSnapshot} onEventChange={onEventChange} />{!session.history && !session.events.length ? <p className="muted" role="status">Loading history...</p> : !session.events.length && !running && !pending && <div className="empty-chat"><h2>Ask the side agent</h2></div>}</>
          : !error && <p className="muted" role="status">Opening side agent...</p>}
      </div>
      {error && <div role="alert" className="storage-error">{error}{!session && <Button size="sm" onClick={onRetry}>Retry</Button>}</div>}
      {session && <div className="composer-area">
        {(permissions || questions) && <div className="permission-queue">
          {session.permissions?.map(permission => <PermissionCard key={permission.id} permission={permission} sessionId={session.id} projectName={project.name} onResolved={onSnapshot} />)}
          {session.questions?.map(question => <QuestionCard key={question.id} question={question} sessionId={session.id} onResolved={onSnapshot} />)}
        </div>}
        {!!sources.length && <div className="selection-context" aria-label="Selected context">{sources.map((source, index) => <div key={`${source.messageId}/${index}`}><blockquote>{source.passage}</blockquote><IconButton label={`Remove passage ${index + 1}`} disabled={pending} onClick={() => onRemoveSource(index)}><X /></IconButton></div>)}</div>}
        {session.history?.total === 0 && <fieldset className="harness-picker side-harness-picker" aria-label="Side agent harness" disabled={disabled}>
          {harnesses.map(harness => <label key={harness.id} className={`harness-option ${harness.id === session.harness ? 'is-active' : ''}`}>
            <input type="radio" name={`side-harness-${session.id}`} value={harness.id} checked={harness.id === session.harness} disabled={!harness.available} onChange={() => onHarness(harness.id)} />
            <span className="side-harness-icon" aria-hidden="true"><HarnessIcon harness={harness.id} /></span><span>{harness.name}</span>
            {!harness.available && <small>Not installed</small>}
          </label>)}
        </fieldset>}
        <PendingSubmission items={submissions} onRestore={onRestoreSubmission} onRetry={onRetrySubmission} />
        <MessageQueue key={`queue/${session.id}`} session={session} disabled={disconnected || pending} onSnapshot={onSnapshot} />
        <MessageComposer key={session.id} projectId={session.projectId} placeholder="Message the side agent" draft={draft} onDraftChange={onDraftChange} onSend={onSend} onStop={onStop} disabled={disconnected || pending} running={running}
          modelControl={<ModelPicker key={`${session.id}/${session.harness}`} session={session} disabled={disabled} onSave={onModel} onSnapshot={onSnapshot} />} />
        <SessionStatusBar session={session} side />
      </div>}
    </aside>
  </>;
}
