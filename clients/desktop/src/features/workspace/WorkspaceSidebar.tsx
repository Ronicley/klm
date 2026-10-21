import { Archive, ArchiveRestore, ArrowDown, ArrowUp, Bot, ChevronDown, ChevronRight, ListTodo, MoreHorizontal, PanelLeftClose, Pencil, Search, Settings, SquarePen, Workflow } from 'lucide-react';
import { useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties, type Dispatch, type SetStateAction } from 'react';
import { Button, IconButton } from '../../design-system/Button';
import { Menu, MenuItem } from '../../design-system/Menu';
import { ENGINE_URL, type Session, type Project, type Topic } from '../../engine';
import { IS_MOBILE_HOST } from '../../platform';
import { MobileSessionSubtitle, SessionRecencySubtitle } from './MobileSessionSubtitle';
import { topicGroups } from './topics';

const minimumSidebarWidth = 200;
const defaultSidebarWidth = 256;
const maximumSidebarWidth = 520;
const minimumChatWidth = 400;
const sidebarWidthKey = `klm.sessions.width.v1:${ENGINE_URL}`;
type SidebarDrag = { kind: 'topic' | 'session'; id: string };
type TopicDrop = { topicId: string; side: 'before' | 'after' | 'inside' };

function readSidebarWidth() {
  try {
    const width = Number(localStorage.getItem(sidebarWidthKey));
    return Number.isFinite(width) && width >= minimumSidebarWidth ? Math.min(width, maximumSidebarWidth) : defaultSidebarWidth;
  } catch { return defaultSidebarWidth; }
}

function sessionRuntimeState(session: Session) {
  if (session.status === 'error') return { state: 'error', label: 'Runtime error' };
  if ((session.permissions?.length ?? 0) > 0) return { state: 'waiting', label: 'Waiting for approval' };
  if ((session.questions?.length ?? 0) > 0) return { state: 'waiting', label: 'Waiting for input' };
  if (session.status === 'running') return { state: 'running', label: 'Turn running' };
  if (session.runtimeActive) return { state: 'active', label: 'Runtime active' };
  return { state: 'off', label: 'Runtime stopped' };
}

export function WorkspaceSidebar({ project, sessions, activeId, activeSection, collapsed, archivedCollapsed, onCollapsedChange, onArchivedCollapsedChange, onSelect, onNew, onRename, onEditTopic, onArchiveSession, onArchiveTopic, onMoveSession, onReorderTopic, onClose, onAgents, onGraphs, onProjectSettings, onActivityBoard, hasTaskProvider }: {
  project: Project; sessions: Session[]; activeId: string; collapsed: string[]; archivedCollapsed: boolean; onCollapsedChange: Dispatch<SetStateAction<string[]>>; onArchivedCollapsedChange: (collapsed: boolean) => void;
  onSelect: (id: string) => void; onNew: () => void; onRename: (session: Session) => void; onEditTopic: (topic: Topic) => void;
  onArchiveSession: (session: Session, archived: boolean) => Promise<boolean>; onArchiveTopic: (topic: Topic, archived: boolean) => Promise<boolean>;
  onMoveSession: (session: Session, topic: Topic) => Promise<boolean>; onReorderTopic: (topic: Topic, beforeId: string) => Promise<boolean>;
  onClose: () => void; activeSection: 'chat' | 'design' | 'agents' | 'graphs' | 'activity-board'; onAgents: () => void; onGraphs: () => void;
  onActivityBoard: () => void; hasTaskProvider: boolean; onProjectSettings: (project: Project) => void;
}) {
  const panel = useRef<HTMLElement>(null);
  const panelId = useId();
  const resize = useRef<{ pointerId: number; x: number; width: number } | null>(null);
  const [savedWidth, setSavedWidth] = useState(readSidebarWidth);
  const [maximumWidth, setMaximumWidth] = useState(maximumSidebarWidth);
  const width = Math.min(savedWidth, maximumWidth);
  const clampWidth = (value: number) => Math.max(minimumSidebarWidth, Math.min(maximumWidth, value));
  useLayoutEffect(() => {
    if (IS_MOBILE_HOST) return;
    const shell = panel.current?.parentElement;
    if (!shell) return;
    const main = shell.querySelector<HTMLElement>('.main-panel');
    const measure = () => {
      const railWidth = shell.querySelector('.project-rail')?.getBoundingClientRect().width ?? 0;
      const sideWidth = shell.querySelector('.side-chat-panel:not(.side-chat-panel--full)')?.getBoundingClientRect().width ?? 0;
      const dividerWidth = sideWidth ? shell.querySelector('.side-chat-divider')?.getBoundingClientRect().width ?? 0 : 0;
      setMaximumWidth(Math.max(minimumSidebarWidth, Math.min(maximumSidebarWidth, shell.clientWidth - railWidth - sideWidth - dividerWidth - minimumChatWidth)));
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(shell);
    if (main) observer.observe(main);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (IS_MOBILE_HOST) return;
    try { localStorage.setItem(sidebarWidthKey, String(savedWidth)); } catch { /* Resizing still works without local storage. */ }
  }, [savedWidth]);
  const [sessionMenu, setSessionMenu] = useState('');
  const [topicMenu, setTopicMenu] = useState('');
  const [search, setSearch] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState('');
  const [actionError, setActionError] = useState('');
  const [dragging, setDragging] = useState<SidebarDrag | null>(null);
  const [drop, setDrop] = useState<TopicDrop | null>(null);
  const suppressClick = useRef(false);
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const refresh = () => { if (!document.hidden) setNow(Date.now()); };
    const timer = window.setInterval(refresh, 60000);
    window.addEventListener('focus', refresh); document.addEventListener('visibilitychange', refresh);
    return () => { window.clearInterval(timer); window.removeEventListener('focus', refresh); document.removeEventListener('visibilitychange', refresh); };
  }, []);
  const query = (search ?? '').trim().toLowerCase();
  const groups = topicGroups(project, sessions);
  const activeTopics = groups.flatMap(group => group.topic && !group.topic.archived ? [group.topic] : []);

  async function runAction(key: string, action: () => Promise<boolean>, failure: string) {
    if (busyAction) return false;
    setBusyAction(key); setActionError('');
    try { if (await action()) { setSessionMenu(''); setTopicMenu(''); return true; } setActionError(failure); return false; }
    catch { setActionError(failure); return false; }
    finally { setBusyAction(''); }
  }

  async function moveSession(session: Session, topic: Topic) {
    if (session.topicId === topic.id) return;
    if (await runAction(`move:${session.id}`, () => onMoveSession(session, topic), 'Could not move the session. Restore archived content first, then try again.')) onCollapsedChange(current => current.filter(id => id !== topic.id));
  }

  function reorderTopic(topic: Topic, beforeId: string) {
    if (topic.id === beforeId) return;
    void runAction(`reorder:${topic.id}`, () => onReorderTopic(topic, beforeId), 'Could not reorder the topic. Try again.');
  }

  function endDrag() {
    setDragging(null); setDrop(null);
    window.setTimeout(() => { suppressClick.current = false; }, 0);
  }

  function sessionRow(session: Session, archived = false, topicIsArchived = false) {
    const runtime = sessionRuntimeState(session);
    return <div key={session.id} className={`session-row ${session.id === activeId ? 'is-active' : ''} ${sessionMenu === session.id ? 'has-open-menu' : ''} ${dragging?.kind === 'session' && dragging.id === session.id ? 'is-dragging' : ''}`} draggable={!archived && !busyAction}
      onDragStart={event => {
        if (event.target instanceof HTMLElement && event.target.closest('.menu-anchor')) { event.preventDefault(); return; }
        event.stopPropagation(); suppressClick.current = true; setSessionMenu(''); setTopicMenu(''); setDrop(null);
        setDragging({ kind: 'session', id: session.id }); event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('application/x-klm-session', session.id);
      }} onDragEnd={endDrag}>
      <button className="session-item" aria-current={session.id === activeId ? 'page' : undefined} title={session.title} onClick={() => { if (!suppressClick.current) onSelect(session.id); }}>
        {IS_MOBILE_HOST ? <span className="mobile-session-copy"><span className="mobile-session-title">{session.title}</span><MobileSessionSubtitle session={session} now={now} archived={archived} /></span> : <span className="session-copy"><span className="session-title">{session.title}</span><span className="session-meta">{!archived && <i className={`session-runtime-led is-${runtime.state}`} role="img" aria-label={runtime.label} title={runtime.label} />}<SessionRecencySubtitle session={session} now={now} /></span></span>}
      </button>
      {archived
        ? !topicIsArchived && <IconButton label={`Restore ${session.title}`} disabled={!!busyAction} onClick={() => void runAction(`restore:${session.id}`, () => onArchiveSession(session, false), 'Could not restore the session. Try again.')}><ArchiveRestore /></IconButton>
        : <Menu label={`${session.title} actions`} role="menu" className="workspace-action-menu" open={sessionMenu === session.id} onOpenChange={open => { setActionError(''); setSessionMenu(open ? session.id : ''); }} side="bottom" trigger={props => <IconButton {...props} label={`${session.title} actions`}><MoreHorizontal /></IconButton>}>
          <MenuItem role="menuitem" disabled={!!busyAction} onClick={() => { setSessionMenu(''); onRename(session); }}><Pencil />Rename</MenuItem>
          {activeTopics.some(topic => topic.id !== session.topicId) && <div className="topic-move-options" role="group" aria-label="Move to topic"><span className="topic-move-heading" aria-hidden="true">Move to topic</span>{activeTopics.filter(topic => topic.id !== session.topicId).map(topic => <MenuItem key={topic.id} role="menuitem" disabled={!!busyAction} title={topic.name} onClick={() => void moveSession(session, topic)}>{topic.name}</MenuItem>)}</div>}
          <MenuItem role="menuitem" disabled={!!busyAction} onClick={() => void runAction(`archive:${session.id}`, () => onArchiveSession(session, true), 'Could not archive the session. Try again.')}><Archive />Archive</MenuItem>
        </Menu>}
    </div>;
  }

  function groupView(group: ReturnType<typeof topicGroups>[number], archived: boolean) {
    const matches = group.sessions.filter(session => (group.topic?.archived || !!session.archived === archived) && (group.name.toLowerCase().includes(query) || session.title.toLowerCase().includes(query)));
    if (query && !matches.length && !group.name.toLowerCase().includes(query)) return null;
    if (archived && !matches.length && !group.topic?.archived) return null;
    const collapseKey = `${archived ? 'archived:' : ''}${group.id}`;
    const expanded = search !== null || !collapsed.includes(collapseKey);
    const dropSide = !archived && drop?.topicId === group.id ? drop.side : undefined;
    const index = activeTopics.findIndex(topic => topic.id === group.id);
    return <section key={collapseKey} className={`workspace-group topic-group ${group.topic ? `topic-color-${group.topic.color}` : 'is-ungrouped'} ${dropSide ? `topic-drop-${dropSide}` : ''} ${!archived && dragging?.kind === 'topic' && dragging.id === group.id ? 'is-dragging' : ''}`} aria-label={group.name}
      onDragOver={event => {
        if (busyAction || archived || !group.topic || !dragging) return;
        if (dragging.kind === 'topic' && dragging.id === group.id || dragging.kind === 'session' && sessions.find(session => session.id === dragging.id)?.topicId === group.id) return;
        event.preventDefault(); event.stopPropagation(); event.dataTransfer.dropEffect = 'move';
        const bounds = event.currentTarget.getBoundingClientRect();
        const side = dragging.kind === 'session' ? 'inside' : event.clientY < bounds.top + bounds.height / 2 ? 'before' : 'after';
        setDrop(current => current?.topicId === group.id && current.side === side ? current : { topicId: group.id, side });
      }}
      onDragLeave={event => { if (!(event.relatedTarget instanceof Node) || !event.currentTarget.contains(event.relatedTarget)) setDrop(current => current?.topicId === group.id ? null : current); }}
      onDrop={event => {
        if (busyAction || archived || !group.topic || !dragging) return;
        event.preventDefault(); event.stopPropagation();
        const item = dragging; const target = group.topic;
        const bounds = event.currentTarget.getBoundingClientRect();
        const after = event.clientY >= bounds.top + bounds.height / 2;
        endDrag();
        if (item.kind === 'session') { const session = sessions.find(session => session.id === item.id); if (session) void moveSession(session, target); }
        else { const topic = activeTopics.find(topic => topic.id === item.id); if (topic && topic.id !== target.id) reorderTopic(topic, after ? activeTopics[index + 1]?.id ?? '' : target.id); }
      }}>
      <div className="workspace-folder-row topic-heading">
        <button className="workspace-folder" title={group.name} aria-expanded={expanded} draggable={!!group.topic && !archived && !busyAction && search === null}
          onDragStart={event => { event.stopPropagation(); suppressClick.current = true; setSessionMenu(''); setTopicMenu(''); setDragging({ kind: 'topic', id: group.id }); setDrop(null); event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('application/x-klm-topic', group.id); }} onDragEnd={endDrag}
          onClick={() => { if (search === null && !suppressClick.current) onCollapsedChange(collapsed.includes(collapseKey) ? collapsed.filter(item => item !== collapseKey) : [...collapsed, collapseKey]); }}>{expanded ? <ChevronDown /> : <ChevronRight />}<span>{group.name}</span></button>
        {group.topic && (group.topic.archived
          ? <IconButton label={`Restore topic ${group.name}`} disabled={!!busyAction} onClick={() => void runAction(`restore-topic:${group.id}`, () => onArchiveTopic(group.topic!, false), 'Could not restore the topic. Try again.')}><ArchiveRestore /></IconButton>
          : !archived && <Menu label={`${group.name} actions`} role="menu" className="workspace-action-menu" open={topicMenu === group.id} onOpenChange={open => { setActionError(''); setTopicMenu(open ? group.id : ''); }} side="bottom" trigger={props => <IconButton {...props} label={`${group.name} actions`}><MoreHorizontal /></IconButton>}>
            <MenuItem role="menuitem" onClick={() => { setTopicMenu(''); onEditTopic(group.topic!); }}><Pencil />Edit topic</MenuItem>
            <MenuItem role="menuitem" disabled={!!busyAction || index <= 0} onClick={() => reorderTopic(group.topic!, activeTopics[index - 1].id)}><ArrowUp />Move up</MenuItem>
            <MenuItem role="menuitem" disabled={!!busyAction || index === activeTopics.length - 1} onClick={() => reorderTopic(group.topic!, activeTopics[index + 2]?.id ?? '')}><ArrowDown />Move down</MenuItem>
            <MenuItem role="menuitem" disabled={!!busyAction} onClick={() => void runAction(`archive-topic:${group.id}`, () => onArchiveTopic(group.topic!, true), 'Could not archive the topic. Try again.')}><Archive />Archive</MenuItem>
          </Menu>)}
      </div>
      {expanded && <div className="session-list">{matches.map(session => sessionRow(session, archived, !!group.topic?.archived))}{!matches.length && <p className="empty-workspace">No sessions</p>}</div>}
    </section>;
  }

  return <aside ref={panel} id={panelId} className="workspace-sidebar" aria-label="Sessions" style={{ '--sidebar-width': `${width}px` } as CSSProperties}>
    <div className="brand-row"><div className="project-sidebar-heading"><strong title={project.name}>{project.name}</strong><IconButton label="Project settings" className="project-settings-button" onClick={() => onProjectSettings(project)}><Settings /></IconButton></div><IconButton label="Close sessions" onClick={onClose}><PanelLeftClose /></IconButton></div>
    <p className="project-folder-label" title={project.folder}><bdi dir="ltr">{project.folder}</bdi></p>
    <nav className="sidebar-navigation" aria-label="Project navigation">
      <Button variant="ghost" size="sm" onClick={onNew}><SquarePen />New session</Button>
      {hasTaskProvider && <Button variant="ghost" size="sm" aria-current={activeSection === 'activity-board' ? 'page' : undefined} onClick={onActivityBoard}><ListTodo />Tasks</Button>}
      <Button variant="ghost" size="sm" aria-current={activeSection === 'agents' ? 'page' : undefined} onClick={onAgents}><Bot />Agents</Button>
      <Button variant="ghost" size="sm" aria-current={activeSection === 'graphs' ? 'page' : undefined} onClick={onGraphs}><Workflow />Graphs</Button>
    </nav>
    <div className="workspace-list">
      <div className="workspace-heading"><h2>Sessions</h2><IconButton label="Search sessions" aria-expanded={search !== null} onClick={() => setSearch(search === null ? '' : null)}><Search /></IconButton></div>
      {search !== null && <input autoFocus className="search-input" aria-label="Search sessions" placeholder="Find a session..." value={search} onChange={event => setSearch(event.target.value)} />}
      {groups.filter(group => !group.topic?.archived).map(group => groupView(group, false))}
      <div className="workspace-group">
        <div className="workspace-folder-row"><button className="workspace-folder" aria-expanded={search !== null || !archivedCollapsed} onClick={() => { if (search === null) onArchivedCollapsedChange(!archivedCollapsed); }}><Archive />Archived</button></div>
        {(search !== null || !archivedCollapsed) && <div className="archived-topic-list">{groups.map(group => groupView(group, true))}{!sessions.some(session => session.archived) && !project.topics?.some(topic => topic.archived) && <p className="empty-workspace">No sessions</p>}</div>}
      </div>
      {actionError && <p role="alert" className="form-error sidebar-action-error">{actionError}</p>}
    </div>
    {!IS_MOBILE_HOST && <div className="session-rail-resize" role="separator" aria-label="Resize sessions" aria-orientation="vertical" aria-controls={panelId} aria-valuemin={minimumSidebarWidth} aria-valuemax={Math.round(maximumWidth)} aria-valuenow={Math.round(width)} aria-valuetext={`${Math.round(width)} pixels`} tabIndex={0}
      onPointerDown={event => {
        if (event.button !== 0 || !event.isPrimary) return;
        resize.current = { pointerId: event.pointerId, x: event.clientX, width };
        event.currentTarget.setPointerCapture(event.pointerId);
        event.currentTarget.dataset.resizing = 'true';
        event.currentTarget.focus();
        event.preventDefault();
      }}
      onPointerMove={event => { const start = resize.current; if (start?.pointerId === event.pointerId) setSavedWidth(clampWidth(start.width + event.clientX - start.x)); }}
      onPointerUp={event => {
        if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
        resize.current = null;
        delete event.currentTarget.dataset.resizing;
      }}
      onLostPointerCapture={event => { resize.current = null; delete event.currentTarget.dataset.resizing; }}
      onPointerCancel={event => { resize.current = null; delete event.currentTarget.dataset.resizing; }}
      onDoubleClick={() => setSavedWidth(defaultSidebarWidth)}
      onKeyDown={event => {
        if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
        event.preventDefault();
        setSavedWidth(event.key === 'Home' ? minimumSidebarWidth : event.key === 'End' ? maximumWidth : clampWidth(width + (event.key === 'ArrowLeft' ? -16 : 16)));
      }} />}
  </aside>;
}
