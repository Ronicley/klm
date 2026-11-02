import { useEffect, useRef, useState, type RefObject } from 'react';
import { EDGE_MARGIN, scrollRoot } from './pagination';

export function useNearEdge(target: RefObject<HTMLElement | null>, { enabled, token, busy, error, load, root }: {
  enabled: boolean; token: string; busy?: boolean; error?: string; load: () => void; root?: RefObject<HTMLElement | null>;
}) {
  const latest = useRef(load); latest.current = load;
  const attempted = useRef('');
  const [layout, setLayout] = useState(0);
  useEffect(() => {
    const element = target.current;
    if (!element || !enabled || busy || error) return;
    const container = root?.current ?? scrollRoot(element);
    let frame = 0;
    const check = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        if (!root && scrollRoot(element) !== container) { setLayout(value => value + 1); return; }
        if (document.hidden || !element.getClientRects().length || attempted.current === token) return;
        const bounds = element.getBoundingClientRect();
        const viewport = container === document.documentElement ? { top: 0, bottom: innerHeight } : container.getBoundingClientRect();
        if (bounds.top <= viewport.bottom + EDGE_MARGIN && bounds.bottom >= viewport.top - EDGE_MARGIN) {
          attempted.current = token;
          latest.current();
        }
      });
    };
    const observer = typeof IntersectionObserver === 'undefined' ? undefined : new IntersectionObserver(check, { root: container === document.documentElement ? null : container, rootMargin: `${EDGE_MARGIN}px` });
    observer?.observe(element);
    const resize = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(check); resize?.observe(container); resize?.observe(element);
    container.addEventListener('scroll', check, { passive: true });
    document.addEventListener('visibilitychange', check); window.addEventListener('resize', check);
    check();
    return () => { cancelAnimationFrame(frame); observer?.disconnect(); resize?.disconnect(); container.removeEventListener('scroll', check); document.removeEventListener('visibilitychange', check); window.removeEventListener('resize', check); };
  }, [target, root, enabled, token, busy, error, layout]);
}
