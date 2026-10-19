import { useEffect, useRef, useState, type RefObject } from 'react';

const threshold = 56;

function hasScrolledParent(target: Element, root: HTMLElement) {
  for (let element: Element | null = target; element; element = element.parentElement) {
    if (element.scrollTop > 0) return true;
    if (element === root) break;
  }
  return false;
}

export function useFeedPullToRefresh(container: RefObject<HTMLDivElement | null>, enabled: boolean, busy: boolean, onRefresh: () => void) {
  const [distance, setDistance] = useState(0);
  const [refreshing, setRefreshing] = useState(false);
  const latest = useRef({ busy, refreshing, onRefresh });
  latest.current = { busy, refreshing, onRefresh };

  useEffect(() => {
    if (refreshing && (!enabled || !busy)) setRefreshing(false);
    if (!enabled) setDistance(0);
  }, [enabled, busy, refreshing]);

  useEffect(() => {
    const root = container.current;
    if (!enabled || !root) return;
    let gesture: { id: number; x: number; y: number; target: Element; pulling: boolean; distance: number } | undefined;
    const cancel = () => { gesture = undefined; setDistance(0); };
    const start = (event: TouchEvent) => {
      cancel();
      if (event.touches.length !== 1 || latest.current.busy || latest.current.refreshing) return;
      const target = event.target instanceof Element ? event.target : undefined;
      if (!target || target.closest('button, a, input, textarea, select, [contenteditable="true"]') || hasScrolledParent(target, root)) return;
      const touch = event.touches[0];
      gesture = { id: touch.identifier, x: touch.clientX, y: touch.clientY, target, pulling: false, distance: 0 };
    };
    const move = (event: TouchEvent) => {
      if (!gesture) return;
      if (event.touches.length !== 1 || latest.current.busy) { cancel(); return; }
      const touch = event.touches[0];
      if (touch.identifier !== gesture.id) { cancel(); return; }
      const dx = Math.abs(touch.clientX - gesture.x);
      const dy = touch.clientY - gesture.y;
      if (!gesture.pulling) {
        if (dx > 8 && dx >= dy || dy < -8 || hasScrolledParent(gesture.target, root)) { cancel(); return; }
        if (dy < 8) return;
        gesture.pulling = true;
      }
      if (event.cancelable) event.preventDefault();
      gesture.distance = Math.max(0, Math.min(80, dy * .5));
      setDistance(gesture.distance);
    };
    const end = () => {
      const refresh = !!gesture && gesture.distance >= threshold && !latest.current.busy && !latest.current.refreshing;
      cancel();
      if (refresh) {
        latest.current.refreshing = true;
        setRefreshing(true);
        latest.current.onRefresh();
      }
    };
    root.addEventListener('touchstart', start, { passive: true });
    root.addEventListener('touchmove', move, { passive: false });
    root.addEventListener('touchend', end, { passive: true });
    root.addEventListener('touchcancel', cancel, { passive: true });
    return () => {
      root.removeEventListener('touchstart', start);
      root.removeEventListener('touchmove', move);
      root.removeEventListener('touchend', end);
      root.removeEventListener('touchcancel', cancel);
    };
  }, [container, enabled]);

  return { distance, refreshing, ready: distance >= threshold };
}
