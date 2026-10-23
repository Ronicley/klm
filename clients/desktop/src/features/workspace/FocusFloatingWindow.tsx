import { useEffect, useRef, useState, type ReactNode } from 'react';
import { X } from 'lucide-react';
import { IconButton } from '../../design-system/Button';
import { ENGINE_URL } from '../../engine';
import { FocusWindowResizeHandles } from './FocusWindowResizeHandles';

type Geometry = { left: number; top: number; width: number; height: number };

function reachable(value: Geometry): Geometry {
  if (window.innerWidth <= 760) return value;
  return { ...value, left: Math.max(80 - value.width, Math.min(window.innerWidth - 80, value.left)), top: Math.max(8, Math.min(window.innerHeight - 48, value.top)) };
}

export function FocusFloatingWindow({ id, title, initial, minimumWidth = 280, minimumHeight = 180, front, onActivate, onClose, onRename, leading, actions, children, className = '' }: {
  id: string; title: string; initial: Geometry; minimumWidth?: number; minimumHeight?: number;
  front: boolean; onActivate: () => void; onClose: () => void; onRename?: () => void; leading?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string;
}) {
  const key = `klm.focus.geometry.v1:${ENGINE_URL}:${id}`;
  const panel = useRef<HTMLElement>(null);
  const header = useRef<HTMLElement>(null);
  const drag = useRef<{ pointerId: number; x: number; y: number; left: number; top: number; capture: HTMLElement; moved: boolean } | null>(null);
  const suppressClick = useRef(false);
  const [geometry, setGeometry] = useState<Geometry>(() => {
    try {
      const saved = JSON.parse(localStorage.getItem(key) ?? 'null') as Geometry | null;
      if (saved && [saved.left, saved.top, saved.width, saved.height].every(Number.isFinite) && saved.width >= minimumWidth && saved.height >= minimumHeight) return reachable(saved);
    } catch { /* Fall back to the initial placement. */ }
    return reachable(initial);
  });
  function saveGeometry() {
    if (!panel.current) return;
    const { left, top, width, height } = panel.current.getBoundingClientRect();
    if (!width || !height) return; // Hidden widgets must not overwrite their layout.
    const next = reachable({ left, top, width, height });
    // Resize writes directly to the DOM; React may see the clamped coordinates
    // as unchanged, so explicitly restore them even when the state value matches.
    panel.current.style.left = `${next.left}px`;
    panel.current.style.top = `${next.top}px`;
    setGeometry(next);
    try { localStorage.setItem(key, JSON.stringify(next)); } catch { /* Keep the current layout in memory. */ }
  }
  function finishDrag() {
    if (drag.current?.moved) saveGeometry();
    drag.current = null;
    if (header.current) delete header.current.dataset.dragging;
  }
  useEffect(() => {
    try { localStorage.setItem(key, JSON.stringify(geometry)); } catch { /* Keep the in-memory layout. */ }
  }, [key, geometry]);
  useEffect(() => {
    const save = () => saveGeometry();
    const onVisibility = () => { if (document.visibilityState === 'hidden') save(); };
    window.addEventListener('pagehide', save);
    document.addEventListener('visibilitychange', onVisibility);
    return () => { window.removeEventListener('pagehide', save); document.removeEventListener('visibilitychange', onVisibility); };
  }, [key]);
  useEffect(() => {
    const resize = () => setGeometry(current => reachable(current));
    window.addEventListener('resize', resize);
    return () => window.removeEventListener('resize', resize);
  }, []);
  return <section ref={panel} className={`focus-floating-window ${className}`} aria-label={title} style={{ ...geometry, zIndex: front ? 3 : 2 }}
    onPointerDownCapture={onActivate} onFocusCapture={onActivate}>
    <header ref={header} className={`focus-floating-header${leading ? ' has-leading' : ''}`}
      onDragStart={event => event.preventDefault()}
      onClickCapture={event => {
        if (suppressClick.current && event.detail !== 0) { event.preventDefault(); event.stopPropagation(); }
        suppressClick.current = false;
      }}
      onPointerDown={event => {
        if (event.button !== 0 || !event.isPrimary) return;
        suppressClick.current = false;
        const target = event.target as HTMLElement;
        const titleButton = target.closest<HTMLElement>('.session-title-button');
        if (!titleButton && target.closest('button, a, input, select, textarea')) return;
        const bounds = panel.current!.getBoundingClientRect();
        // Capture on the title button itself so a stationary click still renames.
        const capture = titleButton ?? event.currentTarget;
        drag.current = { pointerId: event.pointerId, x: event.clientX, y: event.clientY, left: bounds.left, top: bounds.top, capture, moved: false };
        capture.setPointerCapture(event.pointerId);
        if (!titleButton) event.preventDefault();
      }}
      onPointerMove={event => {
        const start = drag.current;
        const element = panel.current;
        if (!start || start.pointerId !== event.pointerId || !element) return;
        if (!start.moved && Math.hypot(event.clientX - start.x, event.clientY - start.y) < 6) return;
        start.moved = true;
        suppressClick.current = true;
        event.currentTarget.dataset.dragging = 'true';
        event.preventDefault();
        const next = reachable({ left: start.left + event.clientX - start.x, top: start.top + event.clientY - start.y, width: element.offsetWidth, height: element.offsetHeight });
        element.style.left = `${next.left}px`; element.style.top = `${next.top}px`;
      }}
      onPointerUp={event => {
        const start = drag.current;
        if (!start || start.pointerId !== event.pointerId) return;
        finishDrag();
        if (start.capture.hasPointerCapture(event.pointerId)) start.capture.releasePointerCapture(event.pointerId);
      }}
      onLostPointerCapture={event => { if (drag.current?.pointerId === event.pointerId) finishDrag(); }}
      onPointerCancel={event => { if (drag.current?.pointerId === event.pointerId) finishDrag(); }}>
      {leading && <div className="focus-floating-leading">{leading}</div>}
      <h2 title={title}>{onRename ? <button type="button" className="session-title-button" title="Rename session" aria-label={`Rename ${title}`} onClick={onRename}>{title}</button> : title}</h2><div className="focus-floating-actions">{actions}<IconButton label={`Close ${title}`} onClick={onClose}><X /></IconButton></div>
    </header>
    <div className="focus-floating-content">{children}</div>
    <FocusWindowResizeHandles minimumWidth={minimumWidth} minimumHeight={minimumHeight} centered={false} onResizeEnd={saveGeometry} />
  </section>;
}
