import { Fragment, useEffect, useRef, useState, type ReactNode } from 'react';
import { Bot, FileCode2, FilePenLine, MessagesSquare, Plug, Search, SquareTerminal, Wrench, type LucideIcon } from 'lucide-react';
import { AgentWork, type AgentWorkActivity, type AgentWorkItemStatus, type AgentWorkStatus, type AgentWorkThought, type AgentWorkTone } from '../../design-system/AgentWork';
import { Button } from '../../design-system/Button';
import { request, type EngineEvent, type Session, type SessionResponse } from '../../engine';
import { ChatMessage, InlineMarkdownContent, MarkdownContent } from './ChatMessage';
import { SessionEvent } from './SessionEvent';

type WorkEvent = EngineEvent & { type: 'reasoning' | 'command' | 'mcp' | 'tool' };
type TimelineItem = { kind: 'event'; event: EngineEvent } | {
  kind: 'work'; id: string; events: WorkEvent[]; status: AgentWorkStatus; startedAt: string; completedAt?: string;
};

function isWorkEvent(event: EngineEvent): event is WorkEvent {
  if (event.type === 'reasoning') return !!event.text.trim();
  return event.type === 'command' || event.type === 'mcp' || event.type === 'tool';
}

function isHiddenStatus(event: EngineEvent) {
  return event.type === 'status' && event.status !== 'warning' && event.status !== 'cancelled';
}

function projectTimeline(events: EngineEvent[], sessionStatus: Session['status'], updatedAt: string): TimelineItem[] {
  const items: TimelineItem[] = [];
  let pending: WorkEvent[] = [];
  let pendingStartedAt = '';
  let lastBoundaryAt = '';
  const flush = (boundary?: EngineEvent) => {
    if (!pending.length) return;
    const status: AgentWorkStatus = boundary?.type === 'error' || (!boundary && sessionStatus === 'error')
      ? 'failed'
      : !boundary && sessionStatus === 'running' ? 'running' : 'completed';
    items.push({
      kind: 'work',
      id: pending[0].id,
      events: pending,
      status,
      startedAt: pendingStartedAt || pending[0].createdAt,
      completedAt: status === 'running' ? undefined : boundary?.createdAt ?? updatedAt,
    });
    pending = [];
    pendingStartedAt = '';
  };

  for (const event of events) {
    if (isHiddenStatus(event) || (event.type === 'reasoning' && !event.text.trim())) continue;
    if (isWorkEvent(event)) {
      if (!pending.length) pendingStartedAt = lastBoundaryAt || event.createdAt;
      pending.push(event);
      continue;
    }
    flush(event);
    items.push({ kind: 'event', event });
    lastBoundaryAt = event.createdAt;
  }
  flush();
  return items;
}

function formatDuration(milliseconds: number) {
  const seconds = Math.max(1, Math.round(milliseconds / 1000));
  if (seconds < 60) return `${seconds} ${seconds === 1 ? 'second' : 'seconds'}`;
  const minutes = Math.floor(seconds / 60);
  const remainder = seconds % 60;
  return remainder ? `${minutes} ${minutes === 1 ? 'minute' : 'minutes'} ${remainder} seconds` : `${minutes} ${minutes === 1 ? 'minute' : 'minutes'}`;
}

function useWorkDuration(startedAtValue: string | undefined, completedAtValue: string | undefined, running: boolean) {
  const [mountedAt] = useState(() => Date.now());
  const startedAt = startedAtValue ? Date.parse(startedAtValue) : mountedAt;
  const completedAt = completedAtValue ? Date.parse(completedAtValue) : undefined;
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!running) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 250);
    return () => window.clearInterval(timer);
  }, [running, startedAt]);
  const end = completedAt !== undefined && Number.isFinite(completedAt) ? completedAt : now;
  return Number.isFinite(startedAt) ? formatDuration(Math.max(0, end - startedAt)) : undefined;
}

function itemStatus(event: WorkEvent, workStatus: AgentWorkStatus): AgentWorkItemStatus {
  if (event.status === 'error' || event.status === 'failed') return 'failed';
  if (workStatus !== 'running') return 'completed';
  return event.status === 'running' || event.status === 'pending' || event.status === 'started' ? 'running' : 'completed';
}

function objectValue(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function cleanDisplayValue(value: string) {
  let text = value.trim();
  for (let attempt = 0; attempt < 2 && text.startsWith('"') && text.endsWith('"'); attempt += 1) {
    try {
      const decoded: unknown = JSON.parse(text);
      if (typeof decoded !== 'string') break;
      text = decoded;
    } catch { break; }
  }
  return /[A-Za-z]:\\{2,}/.test(text) ? text.replace(/\\{2,}/g, '\\') : text;
}

function primaryInput(value: unknown): string | undefined {
  if (typeof value === 'string' && value.trim()) return cleanDisplayValue(value);
  const input = objectValue(value);
  if (!input) return undefined;
  for (const key of ['command', 'filePath', 'path', 'directory', 'target', 'pattern', 'query', 'url']) {
    if (typeof input[key] === 'string' && input[key].trim()) return cleanDisplayValue(input[key]);
  }
  return undefined;
}

function activityDetail(event: WorkEvent) {
  const data = event.data;
  const state = objectValue(data?.state);
  const item = objectValue(data?.item);
  for (const candidate of [state?.input, data?.args, item, data]) {
    const detail = primaryInput(candidate);
    if (detail) return detail;
  }
  const firstLine = event.text.trim().split(/\r?\n/, 1)[0];
  if (!firstLine) return undefined;
  try {
    const parsed: unknown = JSON.parse(firstLine);
    return primaryInput(parsed) ?? cleanDisplayValue(firstLine);
  } catch {
    return cleanDisplayValue(firstLine);
  }
}

function EventDetails({ event }: { event: WorkEvent }) {
  return <>
    {event.text.trim() ? event.type === 'reasoning' ? <MarkdownContent text={event.text} className="agent-work__reasoning-markdown" /> : <pre>{cleanDisplayValue(event.text)}</pre> : null}
    {event.data && Object.keys(event.data).length > 0 ? <details className="agent-work__native-details"><summary>Event details</summary><pre>{JSON.stringify(event.data, null, 2)}</pre></details> : null}
  </>;
}

function activityPresentation(event: WorkEvent): { icon: LucideIcon; tone: AgentWorkTone } {
  if (event.status === 'error' || event.status === 'failed') return { icon: Wrench, tone: 'danger' };
  if (event.type === 'command') return { icon: SquareTerminal, tone: 'accent' };
  if (event.type === 'mcp') return { icon: Plug, tone: 'success' };
  const name = (event.title || '').toLowerCase();
  if (/read|file|glob/.test(name)) return { icon: FileCode2, tone: 'muted' };
  if (/search|grep|find/.test(name)) return { icon: Search, tone: 'muted' };
  if (/write|edit|patch|apply/.test(name)) return { icon: FilePenLine, tone: 'warning' };
  return { icon: Wrench, tone: 'muted' };
}

function ConversationWork({ item }: { item: Extract<TimelineItem, { kind: 'work' }> }) {
  const durationLabel = useWorkDuration(item.startedAt, item.completedAt, item.status === 'running');
  const thoughts: AgentWorkThought[] = item.events.filter(event => event.type === 'reasoning').map(event => ({
    id: event.id,
    status: itemStatus(event, item.status) === 'running' ? 'running' : 'completed',
    text: event.text,
    preview: <InlineMarkdownContent text={event.text} />,
    details: <EventDetails event={event} />,
  }));
  const activities: AgentWorkActivity[] = item.events.filter(event => event.type !== 'reasoning').map(event => {
    const presentation = activityPresentation(event);
    return {
      id: event.id,
      icon: presentation.icon,
      label: event.title || (event.type === 'command' ? 'Command' : event.type === 'mcp' ? 'MCP call' : 'Tool call'),
      detail: activityDetail(event),
      status: itemStatus(event, item.status),
      tone: presentation.tone,
      details: <EventDetails event={event} />,
    };
  });
  return <AgentWork status={item.status} durationLabel={durationLabel} thoughts={thoughts} activities={activities} />;
}

function ActiveWorkPlaceholder({ startedAt }: { startedAt?: string }) {
  const durationLabel = useWorkDuration(startedAt, undefined, true);
  return <AgentWork status="running" durationLabel={durationLabel} />;
}

export function EventSequence({ events, status, updatedAt, renderEvent, showActiveWork = false, activeWorkStartedAt }: {
  events: EngineEvent[];
  status: Session['status'];
  updatedAt: string;
  renderEvent?: (event: EngineEvent) => ReactNode;
  showActiveWork?: boolean;
  activeWorkStartedAt?: string;
}) {
  const items = projectTimeline(events, status, updatedAt);
  const hasActiveWork = items.some(item => item.kind === 'work' && item.status === 'running');
  const lastItem = items.at(-1);
  const responding = lastItem?.kind === 'event' && lastItem.event.type === 'assistant';
  return <>
    {items.map(item => item.kind === 'work'
      ? <ConversationWork key={`work/${item.id}`} item={item} />
      : <Fragment key={item.event.id}>{renderEvent ? renderEvent(item.event) : <SessionEvent event={item.event} />}</Fragment>)}
    {showActiveWork && status === 'running' && !hasActiveWork && !responding ? <ActiveWorkPlaceholder startedAt={activeWorkStartedAt} /> : null}
  </>;
}

function incomingConsultationIds(session: Session) {
  return new Set(session.events
    .filter(event => event.type === 'consultation' && event.data?.to === session.id)
    .map(event => String(event.data?.requestId)));
}

function visibleConversationEvents(session: Session) {
  const incoming = incomingConsultationIds(session);
  return session.events.filter(event => !event.consultationId || !incoming.has(event.consultationId));
}

function ConsultationActivity({ event, output, sessionId, onSnapshot }: { event: EngineEvent; output: EngineEvent[]; sessionId: string; onSnapshot: (session: SessionResponse) => void }) {
  const [cancelling, setCancelling] = useState(false);
  const [error, setError] = useState('');
  const requestId = typeof event.data?.requestId === 'string' ? event.data.requestId : '';
  const answer = typeof event.data?.answer === 'string' ? event.data.answer : '';
  const failure = typeof event.data?.error === 'string' ? event.data.error : '';
  const active = event.status === 'queued' || event.status === 'answering';
  const pendingDelivery = event.data?.from === sessionId && (event.data?.delivery === 'pending' || event.data?.delivery === 'delivering');
  async function cancel() {
    if (cancelling) return;
    setCancelling(true); setError('');
    try { onSnapshot(await request<SessionResponse>(`/api/sessions/${encodeURIComponent(sessionId)}/consultations/${encodeURIComponent(requestId)}/cancel`, 'POST', {})); }
    catch (error) { setError(error instanceof Error ? error.message : 'Could not cancel the consultation.'); }
    finally { setCancelling(false); }
  }
  return <details className="session-event consultation-activity">
    <summary><MessagesSquare /><span>{event.title}</span><small className={event.status === 'failed' || event.status === 'interrupted' ? 'form-error' : 'muted'}>{event.status}</small></summary>
    <div className="consultation-body">
      <strong>Question</strong><p className="consultation-question">{event.text}</p>
      {answer && <><strong>Answer</strong><ChatMessage message={{ id: `${event.id}/answer`, role: 'assistant', text: answer }} /></>}
      {failure && <p className="form-error">{failure}</p>}
      {event.data?.delivery === 'interrupted' && <p className="form-error">Answer delivery interrupted by engine restart.</p>}
      {event.data?.delivery === 'failed' && <p className="form-error">Answer continuation failed.</p>}
      {(active || pendingDelivery) && <Button size="sm" disabled={cancelling} onClick={() => void cancel()}>{cancelling ? 'Cancelling...' : 'Cancel request'}</Button>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {!!output.length && <details className="consultation-output"><summary>Agent activity</summary><div><EventSequence events={output} status={active ? 'running' : failure ? 'error' : 'idle'} updatedAt={output.at(-1)?.createdAt ?? event.createdAt} /></div></details>}
    </div>
  </details>;
}

function subagentEventLabel(event: EngineEvent | undefined) {
  if (!event) return '';
  const title = event.title?.trim() ?? '';
  const text = event.text.trim().split(/\r?\n/, 1)[0];
  return title && title !== 'Command' ? title : text || title || event.type;
}

function SubagentActivity({ event, session, onOpen }: { event: EngineEvent; session?: Session; onOpen?: (sessionId: string) => void }) {
  const childSessionId = typeof event.data?.childSessionId === 'string' ? event.data.childSessionId : '';
  const running = ['running', 'pending', 'started'].includes(event.status ?? 'running') && (!session || session.status === 'running');
  const detail = running ? subagentEventLabel(session?.events.at(-1)) || 'starting' : 'completed';
  return <button type="button" className={`subagent-activity ${running ? 'is-running' : ''}`} disabled={!childSessionId || !onOpen} onClick={() => onOpen?.(childSessionId)}>
    <span className="subagent-activity-icon"><Bot aria-hidden="true" /></span>
    <span className="subagent-activity-copy"><strong>{event.title || 'Subagent'}</strong><small title={detail}>{detail}</small></span>
  </button>;
}

export function ConversationEvents({ session, subagents, onSnapshot, onEventChange, onAskSide, onOpenSubagent, working }: {
  session: Session;
  subagents?: Session[];
  onSnapshot: (session: SessionResponse) => void;
  onEventChange?: (sessionId: string, event: EngineEvent) => void;
  onAskSide?: (messageId: string, passage: string) => void;
  onOpenSubagent?: (sessionId: string) => void;
  working?: boolean;
}) {
  const root = useRef<HTMLDivElement>(null);
  const [selection, setSelection] = useState<{ messageId: string; passage: string; left: number; top: number } | null>(null);
  async function favorite(event: EngineEvent, value: boolean) {
    const next = await request<EngineEvent>(`/api/sessions/${encodeURIComponent(session.id)}/events/${encodeURIComponent(event.id)}`, 'PATCH', { favorite: value });
    onEventChange?.(session.id, next);
  }
  useEffect(() => {
    if (!onAskSide) return;
    const readSelection = () => {
      const selected = window.getSelection();
      if (!selected?.rangeCount || selected.isCollapsed) return null;
      const range = selected.getRangeAt(0);
      const element = (node: Node) => node instanceof Element ? node : node.parentElement;
      const start = element(range.startContainer)?.closest('.message-text');
      const end = element(range.endContainer)?.closest('.message-text');
      const message = start?.closest<HTMLElement>('[data-message-id]');
      if (!start || start !== end || !message || message.closest('.consultation-activity') || !root.current?.contains(message)) return null;
      const passage = selected.toString();
      const bounds = range.getBoundingClientRect();
      return passage.trim() ? { messageId: message.dataset.messageId!, passage, left: Math.max(8, Math.min(bounds.left, window.innerWidth - 176)), top: Math.max(8, Math.min(bounds.top - 50, window.innerHeight - 52)) } : null;
    };
    const clear = () => setSelection(null);
    const onAction = (event: MouseEvent) => event.target instanceof Element && !!event.target.closest('.selection-action');
    const beginSelection = (event: MouseEvent) => { if (!onAction(event)) clear(); };
    const finishSelection = (event: MouseEvent) => {
      if (event.button === 0 && !onAction(event)) setSelection(readSelection());
    };
    const selectionChanged = () => {
      const current = readSelection();
      // Selection changes may dismiss an existing button, but only mouseup shows it.
      setSelection(previous => previous && current?.messageId === previous.messageId && current.passage === previous.passage ? previous : null);
    };
    document.addEventListener('mousedown', beginSelection);
    document.addEventListener('mouseup', finishSelection);
    document.addEventListener('selectionchange', selectionChanged);
    window.addEventListener('resize', clear);
    document.addEventListener('scroll', clear, true);
    return () => {
      document.removeEventListener('mousedown', beginSelection);
      document.removeEventListener('mouseup', finishSelection);
      document.removeEventListener('selectionchange', selectionChanged);
      window.removeEventListener('resize', clear);
      document.removeEventListener('scroll', clear, true);
    };
  }, [!!onAskSide, session.id]);
  // Only answering the other agent is background work. A continuation on the
  // requesting side is its response to the user, including in saved histories.
  const incoming = incomingConsultationIds(session);
  const visibleEvents = visibleConversationEvents(session);
  const active = working ?? session.status === 'running';
  const timelineStatus: Session['status'] = active ? 'running' : session.status;
  const lastUserEvent = [...visibleEvents].reverse().find(event => event.type === 'user');
  const activeWorkStartedAt = active && session.status !== 'running' ? undefined : lastUserEvent?.createdAt;
  const grouped = new Map<string, EngineEvent[]>();
  for (const event of session.events) {
    if (!event.consultationId || !incoming.has(event.consultationId)) continue;
    const group = grouped.get(event.consultationId) ?? [];
    group.push(event); grouped.set(event.consultationId, group);
  }
  return <div ref={root} className="conversation-events">
    <EventSequence events={visibleEvents} status={timelineStatus} updatedAt={session.updatedAt} showActiveWork={active} activeWorkStartedAt={activeWorkStartedAt} renderEvent={event => event.type === 'consultation'
      ? <ConsultationActivity event={event} output={grouped.get(String(event.data?.requestId)) ?? []} sessionId={session.id} onSnapshot={onSnapshot} />
      : event.type === 'subagent' ? <SubagentActivity event={event} session={subagents?.find(item => item.id === event.data?.childSessionId)} onOpen={onOpenSubagent} />
      : <SessionEvent event={event} onFavorite={onEventChange && event.type === 'assistant' ? value => favorite(event, value) : undefined} />} />
    {selection && onAskSide && <Button size="sm" className="selection-action" style={{ left: selection.left, top: selection.top }} onPointerDown={event => event.preventDefault()} onClick={() => {
      onAskSide(selection.messageId, selection.passage); setSelection(null); window.getSelection()?.removeAllRanges();
    }}>Ask side agent</Button>}
  </div>;
}
