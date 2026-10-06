import { LogOut, Moon, Plus, Settings, Sun } from 'lucide-react';
import { Fragment, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { IconButton } from '../../design-system/Button';
import { ProjectActionsMenu } from './ProjectActionsMenu';
import type { Project, Session } from '../../engine';
import { IS_MOBILE_HOST, returnToHosts } from '../../platform';
import { animateSortPositions, captureSortPositions, type SortPositions } from '../sortAnimation';
import paperBoatIcon from './paper-boat-rail.png';

function projectRuntimeState(project: Project, sessions: Session[]) {
  let state = 'off';
  const projectSessions = sessions.filter(session => session.projectId === project.id);
  const byId = new Map(projectSessions.map(session => [session.id, session]));
  for (const session of sessions) {
    const parent = session.parentId ? byId.get(session.parentId) : undefined;
    if (session.projectId !== project.id || session.role === 'graph_node' || session.role === 'subagent' || session.archived || parent?.archived) continue;
    if (project.archivedFolders.includes(parent?.workspace ?? session.workspace)) continue;
    if (session.status === 'error') return { state: 'error', label: 'Session error' };
    if ((session.permissions?.length ?? 0) > 0 || (session.questions?.length ?? 0) > 0) state = 'waiting';
    else if (state === 'off' && (session.status === 'running' || session.runtimeActive)) state = 'active';
  }
  if (state === 'waiting') return { state, label: 'Waiting for input' };
  if (state === 'active') return { state, label: 'Runtime active' };
  return { state, label: '' };
}

function projectBeforeAt(container: HTMLElement, draggingId: string, clientY: number) {
  const buttons = container.querySelectorAll<HTMLButtonElement>('.project-icon');
  for (const button of buttons) {
    if (button.dataset.projectId === draggingId) continue;
    const bounds = button.getBoundingClientRect();
    if (clientY < bounds.top + bounds.height / 2) return button.dataset.projectId ?? '';
  }
  return '';
}

export function ProjectRail({ projects, sessions, activeId, onSelect, onEdit, onRemove, onReorder, onAdd, onSettings, onGeneralAgent, generalActive = false, adding = false }: {
  projects: Project[]; sessions: Session[]; activeId: string; onSelect: (project: Project) => void; onEdit: (project: Project) => void; onAdd: () => void; adding?: boolean;
  onRemove: (project: Project) => Promise<string | null>;
  onReorder: (project: Project, beforeId: string) => Promise<boolean>;
  onSettings: () => void;
  onGeneralAgent?: () => void;
  generalActive?: boolean;
}) {
  const [contextMenu, setContextMenu] = useState<{ projectId: string; x: number; y: number } | null>(null);
  const [dragging, setDragging] = useState('');
  const [dropBefore, setDropBefore] = useState<string | null>(null);
  const projectList = useRef<HTMLDivElement>(null);
  const projectPositions = useRef<SortPositions>(new Map());
  const [theme, setTheme] = useState<'light' | 'dark'>(() => document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light');
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'dark' ? '#111318' : '#f8f9fa');
    try { localStorage.setItem('klm.theme', theme); }
    catch { /* Theme switching still works when browser storage is unavailable. */ }
  }, [theme]);
  useLayoutEffect(() => {
    if (projectList.current) animateSortPositions(projectList.current, '[data-project-sort-key]', 'projectSortKey', projectPositions.current);
  }, [dropBefore]);
  return <nav className="project-rail" aria-label="Projects">
    {/* The legacy marker makes installed mobile capture listeners skip this logo; only the footer assigns the home action. */}
    <IconButton label="General agent" className={`rail-brand ${generalActive ? 'is-active' : ''}`} aria-current={generalActive ? 'page' : undefined} data-klm-general-navigation data-klm-host-navigation onClick={onGeneralAgent}>{IS_MOBILE_HOST ? <span className="brand-mark" aria-hidden="true" /> : <img className="rail-brand-icon" src={paperBoatIcon} alt="" />}</IconButton>
    <div ref={projectList} className="project-icons"
      onDragOver={event => {
        if (!dragging) return;
        event.preventDefault(); event.dataTransfer.dropEffect = 'move';
        const before = projectBeforeAt(event.currentTarget, dragging, event.clientY);
        if (before === dropBefore) return;
        projectPositions.current = captureSortPositions(event.currentTarget, '[data-project-sort-key]', 'projectSortKey');
        setDropBefore(before);
      }}
      onDrop={event => {
        event.preventDefault();
        const moved = projects.find(project => project.id === dragging);
        if (!moved) return;
        const beforeId = projectBeforeAt(event.currentTarget, dragging, event.clientY);
        projectPositions.current = captureSortPositions(event.currentTarget, '[data-project-sort-key]', 'projectSortKey');
        setDropBefore(null); void onReorder(moved, beforeId);
      }}>
    {projects.map(project => {
      const runtime = projectRuntimeState(project, sessions);
      return <Fragment key={project.id}>
      {dragging && dropBefore === project.id ? <span className="project-drop-placeholder" aria-hidden="true" /> : null}
      <ProjectActionsMenu project={project} onEdit={onEdit} onRemove={onRemove}
      open={contextMenu?.projectId === project.id} position={contextMenu?.projectId === project.id ? contextMenu : undefined}
      onOpenChange={open => { if (!open) setContextMenu(current => current?.projectId === project.id ? null : current); }}
      trigger={props => <button {...props} draggable={!adding} data-project-id={project.id} data-project-sort-key={project.id} className={`project-icon status-${runtime.state} ${project.id === activeId ? 'is-active' : ''} ${dragging === project.id ? 'is-dragging' : ''}`}
      aria-label={`Switch to ${project.name}${runtime.label ? `, ${runtime.label}` : ''}`} aria-current={project.id === activeId ? 'page' : undefined}
      title={runtime.label ? `${project.name}: ${runtime.label}` : project.name} onClick={() => { setContextMenu(null); onSelect(project); }}
      onDragStart={event => {
        const index = projects.findIndex(item => item.id === project.id);
        setContextMenu(null); setDragging(project.id); setDropBefore(projects[index + 1]?.id ?? '');
        event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('application/x-klm-project', project.id);
      }}
      onDragEnd={() => {
        if (projectList.current) projectPositions.current = captureSortPositions(projectList.current, '[data-project-sort-key]', 'projectSortKey');
        setDragging(''); setDropBefore(null);
      }}
      onContextMenu={event => {
        event.preventDefault(); event.currentTarget.focus();
        const rect = event.currentTarget.getBoundingClientRect();
        setContextMenu({ projectId: project.id, x: event.clientX || rect.right, y: event.clientY || rect.bottom });
      }}
      onKeyDown={event => {
        if (event.key !== 'ContextMenu' && !(event.shiftKey && event.key === 'F10')) return;
        event.preventDefault();
        const rect = event.currentTarget.getBoundingClientRect();
        setContextMenu({ projectId: project.id, x: rect.right, y: rect.bottom });
      }}
    >{project.icon ? <img src={project.icon} alt="" /> : <span>{project.name.slice(0, 2).toUpperCase()}</span>}</button>} />
      </Fragment>;
    })}
    {dragging && dropBefore === '' ? <span className="project-drop-placeholder" aria-hidden="true" /> : null}
    </div>
    <IconButton label={adding ? 'Choosing project directory' : 'Add project'} className="add-project-button" disabled={adding} onClick={onAdd}><Plus /></IconButton>
    <div className="project-rail-footer"><IconButton label={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'} onClick={() => setTheme(current => current === 'dark' ? 'light' : 'dark')}>{theme === 'dark' ? <Sun /> : <Moon />}</IconButton><IconButton label="Settings" onClick={onSettings}><Settings /></IconButton>{IS_MOBILE_HOST && <IconButton label="Back to hosts" data-klm-host-navigation onClick={returnToHosts}><LogOut /></IconButton>}</div>
  </nav>;
}
