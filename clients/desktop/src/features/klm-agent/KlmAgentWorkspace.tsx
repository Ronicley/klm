import { useEffect, useId, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { ArrowLeftRight, ArrowUpRight, Bookmark, Check, ChevronDown, CircleCheck, CircleDot, CircleX, GitBranch, GitCommitHorizontal, GitMerge, GitPullRequest, MessageSquare, Radio, Search, TriangleAlert } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { TabNav } from '../../design-system/TabNav';
import type { Project, Session } from '../../engine';
import paperBoatIcon from '../projects/paper-boat-rail.png';
import { activityKinds, mockActivity, type ActivityCategory, type ActivityKind, type ActivityPost } from './mockActivity';
import './klm-agent.css';

type FeedProject = Pick<Project, 'name' | 'icon'>;
type FeedFilter = 'All' | ActivityCategory;
type SessionActions = { overlaySessionId?: string; onOpenOverlay: (session: Session) => void; onOpenProject: (session: Session) => void };
const filters: FeedFilter[] = ['All', 'PRs', 'Pipelines', 'Agents', 'Issues'];
const defaultChatRatio = 100 / 3;
const fallbackProjects: FeedProject[] = [{ name: 'KLM', icon: paperBoatIcon }, { name: 'Cubiq', icon: '' }, { name: 'Workspace', icon: '' }];
const eventIcons = {
  'pr-opened': GitPullRequest, 'pr-merged': GitMerge, 'issue-assigned': CircleDot,
  'pipeline-succeeded': CircleCheck, 'pipeline-failed': CircleX, attention: TriangleAlert,
  'pipeline-running': Radio, 'agent-pr': GitPullRequest, 'agent-review': MessageSquare, 'agent-push': GitCommitHorizontal,
} satisfies Record<ActivityKind, typeof GitPullRequest>;

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
    {project.icon ? <img src={project.icon} alt="" /> : <span>{project.name.slice(0, 2).toUpperCase()}</span>}
  </span>;
}

function ActivityCard({ post, project }: { post: ActivityPost; project: FeedProject }) {
  const [saved, setSaved] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const kind = activityKinds[post.kind];
  const EventIcon = eventIcons[post.kind];
  return <article className="activity-post" aria-label={`${project.name}: ${kind.label}`}>
    <div className={`activity-event activity-tone--${kind.tone}`}><EventIcon aria-hidden="true" /><span>{kind.label}</span></div>
    <div className="activity-post-layout">
      <ProjectAvatar project={project} />
      <div className="activity-post-content">
        <div className="activity-byline"><strong>{project.name}</strong><span className="activity-actor">{post.actor}</span><span aria-hidden="true">·</span><span className="activity-time">{post.time}</span></div>
        <p className="activity-post-title">{post.title}</p>
        <p className="activity-post-body">{post.body}</p>
        <div className={`activity-reference activity-tone--${kind.tone}`}>
          <div className="activity-reference-heading"><EventIcon aria-hidden="true" /><strong>{post.reference}</strong>{post.kind === 'pipeline-succeeded' && <span className="activity-reference-result"><Check aria-hidden="true" />Passed</span>}{post.kind === 'attention' && <span className="activity-reference-result">Input needed</span>}</div>
          {post.branch && <span className="activity-branch"><GitBranch aria-hidden="true" />{post.branch}</span>}
          {post.progress !== undefined && <div className="activity-progress-row"><progress value={post.progress} max={100} aria-label={`${post.title} progress`} /><span>{post.progress}%</span></div>}
        </div>
        <div className="activity-post-actions">
          <Button variant="ghost" size="sm" aria-expanded={expanded} aria-controls={`activity-detail-${post.id}`} onClick={() => setExpanded(value => !value)}><MessageSquare />{expanded ? 'Hide details' : 'Details'}</Button>
          <IconButton label={saved ? 'Remove bookmark' : 'Bookmark update'} aria-pressed={saved} className={saved ? 'is-saved' : ''} onClick={() => setSaved(value => !value)}><Bookmark fill={saved ? 'currentColor' : 'none'} /></IconButton>
        </div>
        {expanded && <p id={`activity-detail-${post.id}`} className="activity-detail">{post.detail}</p>}
      </div>
    </div>
  </article>;
}

function happeningStatus(session: Session) {
  // Keep the sessions rail's runtime precedence, including retained idle runtimes.
  if (session.status === 'error') return { state: 'error', label: 'Failed', tone: 'danger' };
  if (session.permissions?.length || session.questions?.length) return { state: 'waiting', label: 'Needs attention', tone: 'warning' };
  if (session.status === 'running') return { state: 'running', label: 'Working', tone: 'success' };
  return { state: session.runtimeActive ? 'active' : 'off', label: 'Idle', tone: 'muted' };
}

function HappeningNow({ projects, sessions, overlaySessionId, onOpenOverlay, onOpenProject }: { projects: Project[]; sessions: Session[] } & SessionActions) {
  const [showAll, setShowAll] = useState(false);
  const list = useRef<HTMLDivElement>(null);
  const listId = useId();
  const projectsById = new Map(projects.map(project => [project.id, project]));
  const activeSessions = sessions.flatMap(session => {
    const project = projectsById.get(session.projectId);
    if (!project || session.parentId || session.role || session.archived || project.archivedFolders.includes(session.workspace)) return [];
    if (!session.runtimeActive && session.status !== 'running' && session.status !== 'error' && !session.permissions?.length && !session.questions?.length) return [];
    return [{ session, project }];
  // New sessions arrive at the end of engine inventory. Keep them visible in the
  // collapsed list without reordering cards on every streamed reasoning update.
  }).sort((left, right) => Date.parse(right.session.createdAt) - Date.parse(left.session.createdAt));
  return <aside className="activity-happening" aria-labelledby="happening-title">
    <div className="activity-happening-heading"><h2 id="happening-title">Happening now</h2><Radio aria-hidden="true" /></div>
    <div ref={list} id={listId} className="activity-happening-items">
      {activeSessions.slice(0, showAll ? undefined : 3).map(({ session, project }) => {
        const status = happeningStatus(session);
        return <div className={`activity-happening-item${overlaySessionId === session.id ? ' is-selected' : ''}`} key={session.id}>
          <button type="button" className="activity-happening-open" aria-label={`Open ${session.title} in overlay`} aria-pressed={overlaySessionId === session.id} onClick={() => onOpenOverlay(session)}>
            <span className={`activity-happening-category activity-tone--${status.tone}`}><span className={`session-runtime-led is-${status.state}`} role="img" aria-label={status.label} />Agent · {status.label}</span>
            <span className="activity-happening-project" title={project.name}><ProjectAvatar project={project} small /><strong>{session.title}</strong></span>
            {session.latestReasoningTitle && <span className="activity-happening-subtitle">{session.latestReasoningTitle}</span>}
          </button>
          <IconButton className="activity-session-project" label={`Open ${session.title} in project`} onClick={() => onOpenProject(session)}><ArrowUpRight /></IconButton>
        </div>;
      })}
      {!activeSessions.length && <p className="activity-happening-empty">No active sessions.</p>}
    </div>
    {(showAll || activeSessions.length > 3) && <Button variant="ghost" className="activity-show-more" aria-expanded={showAll} aria-controls={listId} onClick={() => {
      setShowAll(value => !value);
      list.current?.scrollTo({ top: 0, left: 0 });
    }}>{showAll ? 'Show less' : 'Show more'}<ChevronDown className={showAll ? 'is-expanded' : ''} /></Button>}
  </aside>;
}

function ActivityFeed({ projects, sessions, ...actions }: { projects: Project[]; sessions: Session[] } & SessionActions) {
  const [filter, setFilter] = useState<FeedFilter>('All');
  const [search, setSearch] = useState('');
  const grid = useRef<HTMLDivElement>(null);
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
  const feedProjects = projects.length ? projects : fallbackProjects;
  const query = search.trim().toLowerCase();
  const posts = mockActivity.filter(post => (filter === 'All' || activityKinds[post.kind].categories.includes(filter)) &&
    (!query || [post.title, post.body, post.actor, post.reference, post.branch, activityKinds[post.kind].label, feedProjects[post.project % feedProjects.length].name].join(' ').toLowerCase().includes(query)));

  return <section className="activity-feed" aria-label="Project activity">
    <header className="activity-feed-heading"><div><h2>Activity</h2><span>Across your projects</span></div><span className="activity-project-count">{feedProjects.length} {feedProjects.length === 1 ? 'project' : 'projects'}</span></header>
    <div className="activity-feed-scroll">
      <div ref={grid} className={`activity-feed-grid${swapped ? ' is-swapped' : ''}`} style={{ '--activity-sidebar-width': `${width}px` } as CSSProperties}>
        <div className="activity-timeline">
          <nav className="activity-filters" aria-label="Filter activity">{filters.map(value => <button type="button" key={value} aria-current={filter === value ? 'page' : undefined} className={filter === value ? 'is-active' : ''} onClick={() => setFilter(value)}>{value}</button>)}</nav>
          <div className="activity-posts" aria-label={`${filter} updates`}>
            {posts.map(post => <ActivityCard key={post.id} post={post} project={feedProjects[post.project % feedProjects.length]} />)}
            {!posts.length && <div className="activity-empty"><Search aria-hidden="true" /><h3>No matching updates</h3><Button variant="ghost" onClick={() => { setSearch(''); setFilter('All'); }}>Clear filters</Button></div>}
            {posts.length > 0 && <p className="activity-end">You’re all caught up</p>}
          </div>
        </div>
        <WorkspaceDivider className="activity-divider" label="Resize timeline and Happening now" swapLabel="Swap timeline and Happening now" controls={discoveryId}
          value={width} minimum={200} maximum={maximum} valueText={`${Math.round(width)}px sidebar`} dragScale={available > 660 ? (swapped ? 1 : -1) : 0} step={24}
          onChange={setSavedWidth} onReset={() => setSavedWidth(230)} onSwap={() => setSwapped(value => !value)} />
        <div id={discoveryId} className="activity-discovery">
          <label className="activity-search"><Search aria-hidden="true" /><input aria-label="Search activity" type="search" placeholder="Search activity" value={search} onChange={event => setSearch(event.target.value)} /></label>
          <HappeningNow projects={projects} sessions={sessions} {...actions} />
        </div>
      </div>
    </div>
  </section>;
}

export function KlmAgentWorkspace({ enabled, projects, sessions, children, onCloseOverlay, ...actions }: { enabled: boolean; projects: Project[]; sessions: Session[]; children: ReactNode; onCloseOverlay: () => void } & SessionActions) {
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
      <section id={chatId} className={`klm-agent-chat${actions.overlaySessionId ? ' has-session-overlay' : ''}`} aria-label="KLM Agent chat"><div className="klm-agent-chat-heading"><MessageSquare aria-hidden="true" /><h2>Chat</h2></div>{actions.overlaySessionId && <button type="button" className="klm-overlay-backdrop" aria-label="Dismiss session overlay" onClick={onCloseOverlay} />}{children}</section>
      <WorkspaceDivider label="Resize chat and activity" swapLabel="Swap chat and activity" controls={chatId}
        value={ratio} minimum={minimum} maximum={maximum} valueText={`${Math.round(ratio)}% chat`} dragScale={available > 800 ? 100 / available * (swapped ? -1 : 1) : 0} step={2}
        onChange={setSavedRatio} onReset={() => setSavedRatio(defaultChatRatio)} onSwap={() => setSwapped(value => !value)} />
      <ActivityFeed projects={projects} sessions={sessions} {...actions} onOpenOverlay={session => { setMobilePane('chat'); actions.onOpenOverlay(session); }} />
    </div>
  </div>;
}
