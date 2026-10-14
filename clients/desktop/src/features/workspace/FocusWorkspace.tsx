import { useEffect, useRef, useState, type ReactNode } from 'react';
import { ArrowUpRight, Check, ChevronRight, MessageSquare, PanelsTopLeft, Settings } from 'lucide-react';
import { IconButton } from '../../design-system/Button';
import { Menu, MenuItem } from '../../design-system/Menu';
import { ENGINE_URL, type Project, type Session } from '../../engine';
import { ActivityFeed, HappeningNow } from '../klm-agent/KlmAgentWorkspace';
import { FocusFloatingWindow } from './FocusFloatingWindow';
import { ThemePicker } from '../settings/ThemePicker';
import klmIcon from '../projects/klm-icon.png';
import './focus-workspace.css';

type Widgets = { happening: boolean; feed: boolean };
const storageKey = `klm.focus.widgets.v1:${ENGINE_URL}`;
function readWidgets(): Widgets {
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey) ?? 'null');
    return { happening: saved?.happening === true, feed: saved?.feed === true };
  } catch { return { happening: false, feed: false }; }
}

export function FocusWorkspace({ visible, projects, sessions, floatingSessions, front, onActivate, onOpenMain, onOpenFloating, onCloseFloating, onRenameSession, renderSession, minimized, onRestore, onSettings, onOpenGeneral, openingGeneral, generalError }: {
  visible: boolean; projects: Project[]; sessions: Session[]; floatingSessions: Session[];
  front: string; onActivate: (id: string) => void; onOpenMain: (session: Session) => void; onOpenFloating: (session: Session) => void;
  onCloseFloating: (id: string) => void; onRenameSession: (session: Session) => void; renderSession: (session: Session) => ReactNode;
  minimized: boolean; onRestore: () => void; onSettings: () => void;
  onOpenGeneral: () => void; openingGeneral: boolean; generalError: string;
}) {
  const [widgets, setWidgets] = useState(readWidgets);
  const [feedActionsTarget, setFeedActionsTarget] = useState<HTMLDivElement | null>(null);
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null);
  const [submenu, setSubmenu] = useState(false);
  const trigger = useRef<HTMLDivElement>(null);
  const submenuElement = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!visible) { setPosition(null); return; }
    const context = (event: MouseEvent) => {
      if (!(event.target instanceof HTMLElement) || !event.target.matches('body, html, #root')) return;
      event.preventDefault();
      // Leave enough room for the cascading menu to open to the right.
      setPosition({ x: Math.max(8, Math.min(event.clientX, window.innerWidth - 388)), y: Math.max(8, Math.min(event.clientY, window.innerHeight - 104)) });
      setSubmenu(false);
    };
    document.addEventListener('contextmenu', context);
    return () => document.removeEventListener('contextmenu', context);
  }, [visible]);
  function toggle(widget: keyof Widgets) {
    const next = { ...widgets, [widget]: !widgets[widget] };
    setWidgets(next);
    if (next[widget]) onActivate(widget);
    try { localStorage.setItem(storageKey, JSON.stringify(next)); } catch { /* Widgets remain usable without storage. */ }
  }
  return <>
    <Menu label="Focus background" open={!!position && visible} onOpenChange={open => { if (!open) setPosition(null); }} position={position ?? undefined} className="focus-context-menu"
      trigger={() => null}>
      <div ref={trigger} className="focus-widget-menu" onMouseEnter={() => setSubmenu(true)} onMouseLeave={() => { if (!submenuElement.current?.contains(document.activeElement)) setSubmenu(false); }}>
        <MenuItem aria-haspopup="menu" aria-expanded={submenu} onClick={() => setSubmenu(value => !value)} onKeyDown={event => {
          if (event.key === 'ArrowRight' || event.key === 'ArrowDown') { event.preventDefault(); setSubmenu(true); requestAnimationFrame(() => submenuElement.current?.querySelector<HTMLButtonElement>('button')?.focus()); }
        }}>Widgets<ChevronRight /></MenuItem>
        {submenu && <div ref={submenuElement} className="focus-widget-submenu" role="menu" aria-label="Widgets" onKeyDown={event => {
          if (event.key === 'ArrowLeft') { event.preventDefault(); setSubmenu(false); trigger.current?.querySelector<HTMLButtonElement>('button')?.focus(); }
          if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
            event.preventDefault();
            const items = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('button')];
            const index = items.indexOf(document.activeElement as HTMLButtonElement);
            items[event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length]?.focus();
          }
        }}>
          {(['happening', 'feed'] as const).map(widget => <MenuItem key={widget} role="menuitemcheckbox" aria-checked={widgets[widget]} onClick={() => toggle(widget)}>{widget === 'happening' ? 'Happening now' : 'Feed'}{widgets[widget] && <Check />}</MenuItem>)}
        </div>}
      </div>
    </Menu>
    <div className="focus-windows" hidden={!visible}>
      {widgets.happening && <FocusFloatingWindow id="happening" title="Happening now" initial={{ left: 24, top: 88, width: 300, height: 260 }} front={front === 'happening'} onActivate={() => onActivate('happening')} onClose={() => toggle('happening')} className="focus-happening-window">
        <div className="activity-feed focus-happening-body"><HappeningNow projects={projects} sessions={sessions} floating rowTarget={minimized ? 'floating window' : 'main window'} onOpenOverlay={minimized ? onOpenFloating : onOpenMain} onOpenProject={onOpenFloating} /></div>
      </FocusFloatingWindow>}
      {widgets.feed && <FocusFloatingWindow id="feed" title="Feed" initial={{ left: window.innerWidth - 444, top: 88, width: 420, height: Math.max(360, window.innerHeight - 176) }} minimumWidth={340} minimumHeight={300} front={front === 'feed'} onActivate={() => onActivate('feed')} onClose={() => toggle('feed')} actions={<div ref={setFeedActionsTarget} className="focus-floating-actions" />}>
        <ActivityFeed projects={projects} sessions={sessions} visible={visible} standalone actionsTarget={feedActionsTarget} onOpenOverlay={onOpenMain} onOpenProject={onOpenMain} />
      </FocusFloatingWindow>}
      {floatingSessions.map((session, index) => {
        const project = projects.find(item => item.id === session.projectId);
        const general = session.role === 'general_agent';
        const title = general ? 'KLM Agent' : session.title;
        return <FocusFloatingWindow key={session.id} id={`session:${session.id}`} title={title} initial={{ left: 36 + index * 28, top: Math.max(120, window.innerHeight - 624) + index * 28, width: 420, height: 600 }} minimumWidth={340} minimumHeight={360} front={front === session.id} onActivate={() => onActivate(session.id)} onClose={() => onCloseFloating(session.id)} className="focus-session-window"
          leading={<div className="focus-session-project" title={general ? 'KLM' : project?.name}>
            {general || project?.icon ? <img src={general ? klmIcon : project!.icon} alt="" draggable={false} /> : <span className="focus-session-project-initials" aria-hidden="true">{project?.name.slice(0, 2).toUpperCase()}</span>}
            <span>{general ? 'KLM' : project?.name}</span>
          </div>}
          onRename={general ? undefined : () => onRenameSession(session)} actions={<IconButton label={`Open ${title} in main window`} onClick={() => onOpenMain(session)}><ArrowUpRight /></IconButton>}>
          {renderSession(session)}
        </FocusFloatingWindow>;
      })}
      {minimized && <div className="focus-minimized-bar">
        <ThemePicker />
        <IconButton label="Settings" onClick={onSettings}><Settings /></IconButton>
        {generalError && <span className="form-error" role="alert">{generalError}</span>}
        <IconButton label="Open KLM Agent in floating window" disabled={openingGeneral} onClick={onOpenGeneral}><MessageSquare /></IconButton>
        <IconButton label="Restore main window" onClick={onRestore}><PanelsTopLeft /></IconButton>
      </div>}
    </div>
  </>;
}
