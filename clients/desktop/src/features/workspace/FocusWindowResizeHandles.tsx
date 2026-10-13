import { useRef } from 'react';

const edges = [
  { direction: 'n', label: 'top' }, { direction: 'e', label: 'right' },
  { direction: 's', label: 'bottom' }, { direction: 'w', label: 'left' },
  { direction: 'nw', label: 'top-left' }, { direction: 'ne', label: 'top-right' },
  { direction: 'sw', label: 'bottom-left' }, { direction: 'se', label: 'bottom-right' },
] as const;
type Edge = typeof edges[number]['direction'];

function geometry(element: HTMLElement) {
  const bounds = element.getBoundingClientRect();
  return {
    width: bounds.width, height: bounds.height,
    offsetLeft: parseFloat(element.style.left) || 0, offsetTop: parseFloat(element.style.top) || 0,
  };
}

function resizeWindow(element: HTMLElement, start: ReturnType<typeof geometry>, edge: Edge, dx: number, dy: number) {
  const fromLeft = edge.includes('w');
  const fromTop = edge.includes('n');
  const width = start.width + (fromLeft ? -dx : edge.includes('e') ? dx : 0);
  const height = start.height + (fromTop ? -dy : edge.includes('s') ? dy : 0);
  const nextWidth = Math.max(Math.min(720, start.width), width);
  const nextHeight = Math.max(Math.min(400, start.height), height);
  element.dataset.resized = 'true';
  element.style.width = `${nextWidth}px`;
  element.style.height = `${nextHeight}px`;
  // Compensate for flex centering so the opposite edge stays anchored.
  element.style.left = `${start.offsetLeft + (nextWidth - start.width) / 2 * (fromLeft ? -1 : 1)}px`;
  element.style.top = `${start.offsetTop + (nextHeight - start.height) / 2 * (fromTop ? -1 : 1)}px`;
}

export function FocusWindowResizeHandles() {
  const resize = useRef<(ReturnType<typeof geometry> & { pointerId: number; x: number; y: number; edge: Edge }) | null>(null);
  return <>{edges.map(({ direction, label }) => <button key={direction} type="button" className="focus-window-resize" data-edge={direction} aria-label={`Resize window from ${label}`}
    onPointerDown={event => {
      if (event.button !== 0 || !event.isPrimary || !event.currentTarget.parentElement) return;
      event.stopPropagation();
      resize.current = { ...geometry(event.currentTarget.parentElement), pointerId: event.pointerId, x: event.clientX, y: event.clientY, edge: direction };
      event.currentTarget.setPointerCapture(event.pointerId);
      event.preventDefault();
    }}
    onPointerMove={event => {
      const start = resize.current;
      const element = event.currentTarget.parentElement;
      if (!start || start.pointerId !== event.pointerId || !element) return;
      resizeWindow(element, start, start.edge, event.clientX - start.x, event.clientY - start.y);
    }}
    onPointerUp={event => {
      if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
      resize.current = null;
    }}
    onLostPointerCapture={() => { resize.current = null; }}
    onPointerCancel={() => { resize.current = null; }}
    onKeyDown={event => {
      const element = event.currentTarget.parentElement;
      if (!element || !['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) return;
      event.preventDefault();
      const start = geometry(element);
      resizeWindow(element, start, direction, event.key === 'ArrowLeft' ? -24 : event.key === 'ArrowRight' ? 24 : 0, event.key === 'ArrowUp' ? -24 : event.key === 'ArrowDown' ? 24 : 0);
    }}
  />)}</>;
}
