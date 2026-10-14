import { useEffect, useId, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { ArrowLeftRight, ArrowUp, ArrowUpRight, ChevronDown, MessageSquare, PanelsTopLeft, Radio, Search, Settings2, RefreshCw } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { TabNav } from '../../design-system/TabNav';
import type { Project, Session } from '../../engine';
import type { ActivityCategory } from './githubActivityApi';
import { GithubActivityCard } from './GithubActivityCard';
import { ActivityFeedSettings } from './ActivityFeedSettings';
import { useGithubActivityFeed } from './useGithubActivityFeed';
import { saveHappeningOrder, useHappeningOrder } from './happeningOrder';
import './klm-agent.css';

type FeedProject = Pick<Project, 'name' | 'icon'>;
type FeedFilter = 'All' | ActivityCategory;
type SessionActions = { overlaySessionId?: string; onOpenOverlay: (session: Session) => void; onOpenProject: (session: Session) => void };
const filters: FeedFilter[] = ['All', 'PRs', 'Pipelines', 'Issues', 'Security', 'Repository', 'Other'];
const defaultChatRatio = 100 / 3;

function WorkspaceDivider({ label, swapLabel, controls, value, minimum, maximum, valueText, dragScale, step, onChange, onReset, onSwap, className = '' }: {
  label: string; swapLabel: string; controls: string; value: number; minimum: number; maximum: number; valueText: string;
  dragScale: number; step: number; onChange: (value: number) => void; onReset: () => void; onSwap: () => void; className?: string;
}) {
  const resize = useRef<{ pointerId: number; x: number; value: number } | null>(null);
  const clamp = (next: number) => Math.max(minimum, Math.min(maximum, next));
  return <div className={`klm-agent-divider ${className}`}>
    <div className="klm-agent-resize-handle" role="separator" aria-label={label} aria-orientation="vertical" aria-controls={controls}
      aria-valuemin={Math.round(minimum)} aria-valuemax={Math.round(maximum)} aria-valuenow={Math.round(value)} aria-valuetext={valueText} tabIndex={0}
      onPointerDown={event => {
        if (event.button !== 0 || !event.isPrimary) return;
        resize.current = { pointerId: event.pointerId, x: event.clientX, value };
        event.currentTarget.setPointerCapture(event.pointerId);
        event.currentTarget.dataset.resizing = 'true';
        event.currentTarget.focus();
        event.preventDefault();
      }}
      onPointerMove={event => {
        const start = resize.current;
        if (start?.pointerId === event.pointerId && dragScale !== 0) onChange(clamp(start.value + (event.clientX - start.x) * dragScale));
      }}
      onPointerUp={event => { if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId); }}
      onLostPointerCapture={event => { resize.current = null; delete event.currentTarget.dataset.resizing; }}
      onPointerCancel={event => { resize.current = null; delete event.currentTarget.dataset.resizing; }}
      onDoubleClick={onReset}
      onKeyDown={event => {
        if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
        event.preventDefault();
        onChange(event.key === 'Home' ? minimum : event.key === 'End' ? maximum : clamp(value + (event.key === 'ArrowLeft' ? -step : step) * Math.sign(dragScale)));
      }} />
    <IconButton label={swapLabel} className="klm-agent-swap" onClick={onSwap}><ArrowLeftRight /></IconButton>
  </div>;
}

function ProjectAvatar({ project, small = false }: { project: FeedProject; small?: boolean }) {
  return <span className={`activity-avatar ${small ? 'activity-avatar--small' : ''}`} aria-hidden="true">
    {project.icon ? <img src={project.icon} alt="" draggable={false} /> : <span>{project.name.slice(0, 2).toUpperCase()}</span>}
  </span>;
}

function happeningStatus(session: Session) {
  // Keep the sessions rail's runtime precedence, including retained idle runtimes.
  if (session.status === 'error') return { state: 'error', label: 'Failed', tone: 'danger' };
  if (session.permissions?.length || session.questions?.length) return { state: 'waiting', label: 'Needs attention', tone: 'warning' };
  if (session.status === 'running') return { state: 'running', label: 'Working', tone: 'success' };
  return { state: session.runtimeActive ? 'active' : 'off', label: 'Idle', tone: 'muted' };
}

export function HappeningNow({ projects, sessions, overlaySessionId, onOpenOverlay, onOpenProject, floating = false, rowTarget = floating ? 'main window' : 'overlay' }: { projects: Project[]; sessions: Session[]; floating?: boolean; rowTarget?: 'main window' | 'floating window' | 'overlay' } & SessionActions) {
  const [showAll, setShowAll] = useState(false);
  const list = useRef<HTMLDivElement>(null);
  const listId = useId();
  const order = useHappeningOrder();
  const orderIndex = new Map(order.map((id, index) => [id, index]));
  const dragging = useRef('');
  const suppressClick = useRef(false);
  const [draggedId, setDraggedId] = useState('');
  const [drop, setDrop] = useState<{ id: string; after: boolean } | null>(null);
  const projectsById = new Map(projects.map(project => [project.id, project]));
  const activeSessions = sessions.flatMap(session => {
    const project = projectsById.get(session.projectId);
    if (!project || session.parentId || session.role || session.archived || project.archivedFolders.includes(session.workspace)) return [];
    if (!session.runtimeActive && session.status !== 'running' && session.status !== 'error' && !session.permissions?.length && !session.questions?.length) return [];
    return [{ session, project }];
  // New arrivals precede the saved order, newest first. Streaming never reorders cards.
  }).sort((left, right) => (orderIndex.get(left.session.id) ?? -1) - (orderIndex.get(right.session.id) ?? -1) || Date.parse(right.session.createdAt) - Date.parse(left.session.createdAt));
  function moveSession(id: string, targetId: string, after: boolean) {
    const ids = activeSessions.map(item => item.session.id);
    if (id === targetId || !ids.includes(id) || !ids.includes(targetId)) return;
    const next = ids.filter(item => item !== id);
    next.splice(next.indexOf(targetId) + (after ? 1 : 0), 0, id);
    // Retain inactive sessions so their manual ordering is not forgotten on each update.
    saveHappeningOrder([...next, ...order.filter(item => !ids.includes(item))]);
  }
  function finishDrag() {
    dragging.current = ''; setDraggedId(''); setDrop(null);
  }
  function dropAfter(element: HTMLElement, x: number, y: number) {
    const bounds = element.getBoundingClientRect();
    const horizontal = !!list.current && getComputedStyle(list.current).display === 'flex';
    return horizontal ? x > bounds.left + bounds.width / 2 : y > bounds.top + bounds.height / 2;
  }
  return <aside className={`activity-happening${floating ? ' activity-happening--floating' : ''}`} aria-labelledby={`${listId}-title`}>
    <div className="activity-happening-heading"><h2 id={`${listId}-title`}>Happening now</h2><Radio aria-hidden="true" /></div>
    <div ref={list} id={listId} className="activity-happening-items" onDragLeave={event => {
      if (!(event.relatedTarget instanceof Node) || !event.currentTarget.contains(event.relatedTarget)) setDrop(null);
    }}>
      {activeSessions.slice(0, floating || showAll ? undefined : 3).map(({ session, project }) => {
        const status = happeningStatus(session);
        return <div className={`activity-happening-item${overlaySessionId === session.id ? ' is-selected' : ''}${draggedId === session.id ? ' is-dragging' : ''}${drop?.id === session.id ? drop.after ? ' drop-after' : ' drop-before' : ''}`} key={session.id} draggable
          onPointerDownCapture={() => { suppressClick.current = false; }}
          onDragStart={event => {
            if ((event.target as HTMLElement).closest('.activity-session-project')) { event.preventDefault(); return; }
            dragging.current = session.id; suppressClick.current = true; setDraggedId(session.id);
            event.dataTransfer.effectAllowed = 'move';
            event.dataTransfer.setData('application/x-klm-happening-session', session.id);
            event.stopPropagation();
          }}
          onDragOver={event => {
            if (!dragging.current) return;
            event.preventDefault(); event.stopPropagation(); event.dataTransfer.dropEffect = 'move';
            if (dragging.current === session.id) { setDrop(null); return; }
            const after = dropAfter(event.currentTarget, event.clientX, event.clientY);
            setDrop(current => current?.id === session.id && current.after === after ? current : { id: session.id, after });
          }}
          onDrop={event => {
            if (!dragging.current) return;
            event.preventDefault(); event.stopPropagation();
            moveSession(dragging.current, session.id, dropAfter(event.currentTarget, event.clientX, event.clientY));
            finishDrag();
          }} onDragEnd={finishDrag}>
          <button type="button" className="activity-happening-open" aria-label={`Open ${session.title} in ${rowTarget}`} aria-description="Drag to reorder, or use Alt + Arrow keys." aria-pressed={overlaySessionId === session.id}
            onKeyDown={event => {
              if (!event.altKey || !['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight'].includes(event.key)) return;
              event.preventDefault();
              const forward = event.key === 'ArrowDown' || event.key === 'ArrowRight';
              const target = activeSessions[activeSessions.findIndex(item => item.session.id === session.id) + (forward ? 1 : -1)];
              if (target) moveSession(session.id, target.session.id, forward);
            }}
            onClick={event => { if (!suppressClick.current || event.detail === 0) onOpenOverlay(session); }}>
            <span className={`activity-happening-category activity-tone--${status.tone}`}><span className={`session-runtime-led is-${status.state}`} role="img" aria-label={status.label} />Agent · {status.label}</span>
            <span className="activity-happening-project" title={project.name}><ProjectAvatar project={project} small /><strong>{session.title}</strong></span>
            {session.latestReasoningTitle && <span className="activity-happening-subtitle">{session.latestReasoningTitle}</span>}
          </button>
          <IconButton className="activity-session-project" label={`Open ${session.title} in ${floating ? 'floating window' : 'project'}`} onClick={() => onOpenProject(session)}>{floating ? <PanelsTopLeft /> : <ArrowUpRight />}</IconButton>
        </div>;
      })}
      {!activeSessions.length && <p className="activity-happening-empty">No active sessions.</p>}
    </div>
    {!floating && (showAll || activeSessions.length > 3) && <Button variant="ghost" className="activity-show-more" aria-expanded={showAll} aria-controls={listId} onClick={() => {
      setShowAll(value => !value);
      list.current?.scrollTo({ top: 0, left: 0 });
    }}>{showAll ? 'Show less' : 'Show more'}<ChevronDown className={showAll ? 'is-expanded' : ''} /></Button>}
  </aside>;
}

export function ActivityFeed({ projects, sessions, visible, standalone = false, actionsTarget, ...actions }: { projects: Project[]; sessions: Session[]; visible: boolean; standalone?: boolean; actionsTarget?: HTMLElement | null } & SessionActions) {
  const [filter, setFilter] = useState<FeedFilter>('All');
  const [search, setSearch] = useState('');
  const grid = useRef<HTMLDivElement>(null);
  const timeline = useRef<HTMLDivElement>(null);
  const [settings, setSettings] = useState(false);
  const [bookmarks, setBookmarks] = useState<Set<string>>(() => new Set());
  const feed = useGithubActivityFeed(filter, search, projects, visible, timeline);
  const projectsById = new Map(projects.map(project => [project.id, project]));
  const newPostProjects = [...new Set(feed.newPosts.map(post => post.projectId))].flatMap(id => {
    const project = projectsById.get(id);
    return project ? [project] : [];
  });
  const discoveryId = useId();
  const [available, setAvailable] = useState(0);
  const [savedWidth, setSavedWidth] = useState(() => {
    try {
      const value = Number(localStorage.getItem('klm.agent.discovery-width.v1'));
      return Number.isFinite(value) && value >= 200 ? value : 230;
    } catch { return 230; }
  });
  const [swapped, setSwapped] = useState(() => {
    try { return localStorage.getItem('klm.agent.discovery-swapped.v1') === 'true'; }
    catch { return false; }
  });
  const maximum = Math.max(200, available - 320);
  const width = Math.min(savedWidth, maximum);
  useEffect(() => {
    const element = grid.current;
    if (!element) return;
    const measure = () => setAvailable(element.getBoundingClientRect().width);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    try { localStorage.setItem('klm.agent.discovery-width.v1', String(savedWidth)); }
    catch { /* Resizing still works without browser storage. */ }
  }, [savedWidth]);
  useEffect(() => {
    try { localStorage.setItem('klm.agent.discovery-swapped.v1', String(swapped)); }
    catch { /* Swapping still works without browser storage. */ }
  }, [swapped]);
  const feedActions = <><IconButton label="Refresh activity" disabled={feed.busy || !feed.catalog} onClick={feed.refresh}><RefreshCw /></IconButton><IconButton label="Feed settings" disabled={!feed.catalog} onClick={() => setSettings(true)}><Settings2 /></IconButton></>;
  return <section className={`activity-feed${standalone ? ' activity-feed--standalone' : ''}`} aria-label="Project activity">
    {standalone ? actionsTarget && createPortal(feedActions, actionsTarget) : <header className="activity-feed-heading"><div><h2>Activity</h2><span>Across your projects</span></div><div className="activity-feed-tools"><span className="activity-project-count">{projects.length} {projects.length === 1 ? 'project' : 'projects'}</span>{feedActions}</div></header>}
    {standalone && <label className="activity-search"><Search aria-hidden="true" /><input aria-label="Search activity" type="search" maxLength={200} placeholder="Search activity" value={search} onChange={event => setSearch(event.target.value)} /></label>}
    {settings && feed.catalog && <ActivityFeedSettings items={feed.catalog.items} overrides={feed.overrides} error={feed.preferenceError} onChange={feed.savePreferences} onClose={() => setSettings(false)} />}
    <div className="activity-feed-scroll">
      <div ref={grid} className={`activity-feed-grid${swapped ? ' is-swapped' : ''}`} style={{ '--activity-sidebar-width': `${width}px` } as CSSProperties}>
        <div className="activity-timeline" ref={timeline} tabIndex={-1}>
          <nav className="activity-filters" aria-label="Filter activity">{filters.map(value => <button type="button" key={value} aria-current={filter === value ? 'page' : undefined} className={filter === value ? 'is-active' : ''} onClick={() => setFilter(value)}>{value}</button>)}</nav>
          <div className="activity-new-posts">
            {newPostProjects.length > 0 && <Button variant="primary" className="activity-new-posts-button" aria-label={`Show ${feed.newPosts.length} new ${feed.newPosts.length === 1 ? 'post' : 'posts'}`} title={`New posts from ${newPostProjects.map(project => project.name).join(', ')}`} onClick={feed.showNewPosts}>
              <ArrowUp aria-hidden="true" />
              <span className="activity-new-posts-projects">{newPostProjects.slice(0, 3).map(project => <ProjectAvatar key={project.id} project={project} small />)}</span>
              {newPostProjects.length > 3 && <span aria-hidden="true">+{newPostProjects.length - 3}</span>}
            </Button>}
          </div>
          <div className="activity-posts" aria-label={`${filter} updates`}>
            {(feed.error || feed.setupError) && <div className="activity-feedback" role="alert">{feed.error || feed.setupError}<Button size="sm" variant="ghost" disabled={feed.busy} onClick={feed.retry}>Retry</Button></div>}
            {feed.preferenceError && !settings && <p className="activity-feedback" role="alert">{feed.preferenceError}</p>}
            {feed.notice && <p className="activity-feedback" role="status">{feed.notice}</p>}
            {feed.items.map(post => <GithubActivityCard key={post.id} post={post} project={projectsById.get(post.projectId)} now={feed.clock} onUnavailable={feed.unavailable} saved={bookmarks.has(post.id)} onBookmark={() => setBookmarks(current => { const next = new Set(current); if (next.has(post.id)) next.delete(post.id); else next.add(post.id); return next; })} />)}
            {!feed.loaded && !feed.error && !feed.setupError && <p className="activity-end" role="status">Loading activity…</p>}
            {feed.loaded && !feed.items.length && <div className="activity-empty"><Search aria-hidden="true" /><h3>{search || filter !== 'All' ? 'No matching updates' : 'No updates yet'}</h3>{(search || filter !== 'All') && <Button variant="ghost" onClick={() => { setSearch(''); setFilter('All'); }}>Clear filters</Button>}</div>}
            {feed.hasMore && <div className="activity-end"><Button variant="ghost" disabled={feed.busy} onClick={feed.loadMore}>{feed.busy ? 'Loading…' : 'Load more'}</Button></div>}
            {feed.loaded && !feed.hasMore && feed.items.length > 0 && <p className="activity-end">No more updates</p>}
          </div>
        </div>
        {!standalone && <><WorkspaceDivider className="activity-divider" label="Resize timeline and Happening now" swapLabel="Swap timeline and Happening now" controls={discoveryId}
          value={width} minimum={200} maximum={maximum} valueText={`${Math.round(width)}px sidebar`} dragScale={available > 660 ? (swapped ? 1 : -1) : 0} step={24}
          onChange={setSavedWidth} onReset={() => setSavedWidth(230)} onSwap={() => setSwapped(value => !value)} />
        <div id={discoveryId} className="activity-discovery">
          <label className="activity-search"><Search aria-hidden="true" /><input aria-label="Search activity" type="search" maxLength={200} placeholder="Search activity" value={search} onChange={event => setSearch(event.target.value)} /></label>
          <HappeningNow projects={projects} sessions={sessions} {...actions} />
        </div></>}
      </div>
    </div>
  </section>;
}

export function KlmAgentWorkspace({ enabled, visible = true, projects, sessions, children, onCloseOverlay, onOpenFloatingChat, openingFloatingChat = false, ...actions }: { enabled: boolean; visible?: boolean; projects: Project[]; sessions: Session[]; children: ReactNode; onCloseOverlay: () => void; onOpenFloatingChat?: () => void; openingFloatingChat?: boolean } & SessionActions) {
  const [mobilePane, setMobilePane] = useState<'chat' | 'activity'>('chat');
  const columns = useRef<HTMLDivElement>(null);
  const chatId = useId();
  const [available, setAvailable] = useState(0);
  const [savedRatio, setSavedRatio] = useState(() => {
    try {
      const saved = localStorage.getItem('klm.agent.chat-ratio.v2');
      const value = Number(saved ?? localStorage.getItem('klm.agent.chat-ratio.v1'));
      // Replace the previous auto-saved 50/50 default, retaining custom widths.
      return Number.isFinite(value) && value > 0 && value < 100 && (saved !== null || value !== 50) ? value : defaultChatRatio;
    } catch { return defaultChatRatio; }
  });
  const [swapped, setSwapped] = useState(() => {
    try { return localStorage.getItem('klm.agent.panels-swapped.v1') === 'true'; }
    catch { return false; }
  });
  const minimum = Math.min(50, 360 / Math.max(available, 1) * 100);
  const maximum = 100 - minimum;
  const ratio = Math.max(minimum, Math.min(maximum, savedRatio));
  useEffect(() => {
    const element = columns.current;
    if (!enabled || !element) return;
    const measure = () => setAvailable(element.getBoundingClientRect().width);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [enabled]);
  useEffect(() => {
    try { localStorage.setItem('klm.agent.chat-ratio.v2', String(savedRatio)); }
    catch { /* Resizing still works without browser storage. */ }
  }, [savedRatio]);
  useEffect(() => {
    try { localStorage.setItem('klm.agent.panels-swapped.v1', String(swapped)); }
    catch { /* Swapping still works without browser storage. */ }
  }, [swapped]);
  if (!enabled) return children;
  return <div className="klm-agent-workspace" data-mobile-pane={mobilePane}>
    <div className="klm-agent-mobile-tabs"><TabNav value={mobilePane} onChange={setMobilePane} items={[{ value: 'chat', label: 'Chat' }, { value: 'activity', label: 'Activity' }]} /></div>
    <div ref={columns} className={`klm-agent-columns${swapped ? ' is-swapped' : ''}`} style={{ '--klm-left-width': `${swapped ? 100 - ratio : ratio}%` } as CSSProperties}>
      <section id={chatId} className={`klm-agent-chat${actions.overlaySessionId ? ' has-session-overlay' : ''}`} aria-label="KLM Agent chat"><div className="klm-agent-chat-heading"><MessageSquare aria-hidden="true" /><h2>Chat</h2>{onOpenFloatingChat && <IconButton className="klm-agent-open-floating" label="Open KLM Agent in floating window" disabled={openingFloatingChat} onClick={onOpenFloatingChat}><PanelsTopLeft /></IconButton>}</div>{actions.overlaySessionId && <button type="button" className="klm-overlay-backdrop" aria-label="Dismiss session overlay" onClick={onCloseOverlay} />}{children}</section>
      <WorkspaceDivider label="Resize chat and activity" swapLabel="Swap chat and activity" controls={chatId}
        value={ratio} minimum={minimum} maximum={maximum} valueText={`${Math.round(ratio)}% chat`} dragScale={available > 800 ? 100 / available * (swapped ? -1 : 1) : 0} step={2}
        onChange={setSavedRatio} onReset={() => setSavedRatio(defaultChatRatio)} onSwap={() => setSwapped(value => !value)} />
      <ActivityFeed projects={projects} sessions={sessions} visible={visible && (available > 800 || mobilePane === 'activity')} {...actions} onOpenOverlay={session => { setMobilePane('chat'); actions.onOpenOverlay(session); }} />
    </div>
  </div>;
}
