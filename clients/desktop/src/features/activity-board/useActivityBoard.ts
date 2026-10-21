import { useCallback, useEffect, useRef, useState } from 'react';
import { boardConfig, boardError, refreshBoard, type BoardConfig } from './api';

export function useActivityBoard(visible: boolean) {
  const [config, updateConfig] = useState<BoardConfig>();
  const [error, setError] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const generation = useRef(0);
  const flight = useRef<AbortController | null>(null);
  const refreshFlight = useRef<number | null>(null);
  const active = useRef(false);
  const invalidate = useCallback(() => {
    generation.current += 1;
    flight.current?.abort(); flight.current = null;
    return generation.current;
  }, []);
  const applyConfig = useCallback((next: BoardConfig) => updateConfig(current => {
    if (current && (BigInt(next.revision) < BigInt(current.revision) || BigInt(next.projectionRevision) < BigInt(current.projectionRevision))) return current;
    return next;
  }), []);
  const setConfig = useCallback((next: BoardConfig) => { invalidate(); applyConfig(next); }, [invalidate, applyConfig]);
  const load = useCallback(async (fresh = false): Promise<BoardConfig | undefined> => {
    if (!visible || !active.current || document.hidden || refreshFlight.current !== null) return;
    if (flight.current && !fresh) return;
    if (fresh) invalidate();
    const version = generation.current;
    const controller = new AbortController(); flight.current = controller;
    try {
      const next = await boardConfig(controller.signal);
      if (!controller.signal.aborted && active.current && version === generation.current) { applyConfig(next); setError(''); return next; }
    } catch (e) { if (!controller.signal.aborted && active.current && version === generation.current) setError(boardError(e)); }
    finally { if (flight.current === controller) flight.current = null; }
  }, [visible, invalidate, applyConfig]);
  useEffect(() => {
    if (!visible) return;
    active.current = true; setRefreshing(refreshFlight.current !== null);
    void load();
    const onVisible = () => { if (!document.hidden) void load(); };
    window.addEventListener('focus', onVisible);
    document.addEventListener('visibilitychange', onVisible);
    // Invalidate reads on hide, but keep an in-transit Refresh owned until it
    // settles. Restoration must not race a new GET ahead of that POST.
    return () => { active.current = false; invalidate(); window.removeEventListener('focus', onVisible); document.removeEventListener('visibilitychange', onVisible); };
  }, [load, visible, invalidate]);
  const syncing = config?.connections.some(c => c.syncing) ?? false;
  useEffect(() => {
    if (!visible || !syncing) return;
    const timer = window.setInterval(() => void load(), 1500);
    return () => window.clearInterval(timer);
  }, [syncing, load, visible]);
  async function refresh() {
    if (refreshFlight.current !== null) return;
    const version = invalidate(); refreshFlight.current = version;
    setRefreshing(true);
    try { const next = await refreshBoard(); if (active.current && version === generation.current) { applyConfig(next); setError(''); } }
    catch (e) { if (active.current && version === generation.current) setError(boardError(e)); }
    finally {
      if (refreshFlight.current === version) {
        refreshFlight.current = null;
        if (active.current) { setRefreshing(false); void load(true); }
      }
    }
  }
  return { config, setConfig, error, load, refresh, busy: refreshing || syncing };
}

export type ActivityBoardState = ReturnType<typeof useActivityBoard>;
