import { useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject } from 'react';
import { EngineRequestError, type Project } from '../../engine';
import { activityPreferencesKey, readActivityPreferences, type ActivityOverrides } from './ActivityFeedSettings';
import { compareActivitySequence, getActivityCatalog, getActivityPage, type ActivityCatalog, type GithubActivityPage } from './githubActivityApi';

function feedScroller(timeline: HTMLDivElement | null) {
  if (!timeline) return null;
  return getComputedStyle(timeline).overflowY === 'visible' ? timeline.closest<HTMLElement>('.activity-feed-scroll') : timeline;
}

export function useGithubActivityFeed(category: string, search: string, projects: Project[], visible: boolean, timeline: RefObject<HTMLDivElement | null>) {
  const [catalog, setCatalog] = useState<ActivityCatalog>();
  const [overrides, setOverrides] = useState<ActivityOverrides>({});
  const [ready, setReady] = useState(false);
  const [setupError, setSetupError] = useState('');
  const [preferenceError, setPreferenceError] = useState('');
  const [setupAttempt, setSetupAttempt] = useState(0);
  const [queryText, setQueryText] = useState(search);
  const [page, setPage] = useState<GithubActivityPage>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [unseenIds, setUnseenIds] = useState<Set<string>>(() => new Set());
  const [clock, setClock] = useState(Date.now());
  const offset = useRef(0);
  const snapshot = useRef<GithubActivityPage | undefined>(undefined);
  const failedMore = useRef(false);
  const active = useRef(visible); active.current = visible;
  const operate = useRef<(more?: boolean) => void>(() => {});
  const anchor = useRef<{ id?: string; top: number; scrollTop: number; scroller: HTMLElement } | undefined>(undefined);
  const fingerprint = JSON.stringify(projects.map(p => [p.id, p.name, p.icon]));
  const projectIds = new Set(projects.map(p => p.id));
  useEffect(() => {
    const root = timeline.current;
    const outer = root?.closest<HTMLElement>('.activity-feed-scroll');
    const readTop = () => {
      const scroller = feedScroller(root);
      if (scroller && scroller.scrollTop <= 16) setUnseenIds(current => current.size ? new Set() : current);
    };
    root?.addEventListener('scroll', readTop, { passive: true });
    outer?.addEventListener('scroll', readTop, { passive: true });
    return () => { root?.removeEventListener('scroll', readTop); outer?.removeEventListener('scroll', readTop); };
  }, [timeline]);
  const showNewPosts = () => {
    const scroller = feedScroller(timeline.current);
    setUnseenIds(new Set());
    timeline.current?.focus({ preventScroll: true });
    scroller?.scrollTo({ top: 0, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' });
  };
  useEffect(() => { const timer = setTimeout(() => setQueryText(search.trim()), 250); return () => clearTimeout(timer); }, [search]);
  useEffect(() => {
    const controller = new AbortController(); setSetupError('');
    void (async () => {
      try {
        const result = await getActivityCatalog(controller.signal);
        if (!controller.signal.aborted) setCatalog(result);
        const preferences = readActivityPreferences();
        if (!controller.signal.aborted) { setCatalog(result); setOverrides(preferences); setReady(true); }
      } catch (cause) { if (!controller.signal.aborted) setSetupError(cause instanceof Error ? cause.message : 'Feed settings could not be loaded.'); }
    })();
    return () => controller.abort();
  }, [setupAttempt]);
  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key !== activityPreferencesKey && event.key !== null) return;
      try { setOverrides(readActivityPreferences()); setPreferenceError(''); } catch { setPreferenceError('Feed preferences could not be loaded.'); }
    };
    window.addEventListener('storage', sync); return () => window.removeEventListener('storage', sync);
  }, []);
  const savePreferences = (next: ActivityOverrides) => {
    setOverrides(next);
    setReady(true); setSetupError('');
    try { localStorage.setItem(activityPreferencesKey, JSON.stringify({ version: 1, overrides: next })); setPreferenceError(''); }
    catch { setPreferenceError('Preferences could not be saved.'); }
  };
  const params = new URLSearchParams({ category, q: queryText, limit: '50' });
  for (const item of catalog?.items ?? []) if (!(overrides[item.id] ?? item.defaultEnabled)) params.append('exclude', item.id);
  const query = params.toString();
  useEffect(() => {
    snapshot.current = undefined; anchor.current = undefined; setPage(undefined); setError(''); setUnseenIds(new Set());
    if (!ready) return;
    const controller = new AbortController(); let running = false; let queued = false;
    const captureAnchor = () => {
      anchor.current = undefined;
      const root = timeline.current;
      const scroller = feedScroller(root);
      if (!root || !scroller) return false;
      const scrollTop = scroller.scrollTop;
      anchor.current = { top: 0, scrollTop, scroller };
      if (scrollTop <= 16) return false;
      const viewport = scroller.getBoundingClientRect();
      const top = Math.max(viewport.top, root.querySelector('.activity-filters')?.getBoundingClientRect().bottom ?? viewport.top);
      const element = [...root.querySelectorAll<HTMLElement>('[data-activity-id]')].find(e => {
        const bounds = e.getBoundingClientRect();
        return bounds.bottom > top && bounds.top < viewport.bottom;
      });
      if (element) anchor.current = { id: element.dataset.activityId!, top: element.getBoundingClientRect().top, scrollTop, scroller };
      return true;
    };
    const run = async (more = false) => {
      if (controller.signal.aborted || !active.current || document.visibilityState === 'hidden') return;
      if (running) { if (!more) queued = true; return; }
      running = true; setBusy(true); setError('');
      try {
        const previous = snapshot.current;
        const boundary = previous?.items.at(-1)?.releaseSequence;
        let result = await getActivityPage(query, more ? previous?.nextCursor : undefined, controller.signal);
        const items = more ? [...(previous?.items ?? []), ...result.items] : [...result.items];
        // Fetch a contiguous new snapshot through the old lower bound. Never
        // splice a new top onto an old cursor (that loses intervening releases).
        while (!more && boundary && result.hasMore && result.nextCursor && (!result.items.length || compareActivitySequence(result.items.at(-1)!.releaseSequence, boundary) > 0)) {
          result = await getActivityPage(query, result.nextCursor, controller.signal); items.push(...result.items);
        }
        if (controller.signal.aborted) return;
        offset.current = Date.parse(result.asOf) - Date.now();
        const next = { ...result, items: [...new Map(items.map(item => [item.id, item])).values()] };
        const awayFromTop = captureAnchor();
        const retained = new Set(next.items.map(item => item.id));
        const arrivals = previous && !more && awayFromTop ? next.items.filter(item => compareActivitySequence(item.releaseSequence, previous.highWatermark) > 0) : [];
        setUnseenIds(current => {
          const unseen = new Set([...current].filter(id => retained.has(id)));
          for (const item of arrivals) unseen.add(item.id);
          return unseen;
        });
        snapshot.current = next; setPage(next); setClock(Date.now() + offset.current);
      } catch (cause) {
        if (!controller.signal.aborted) {
          failedMore.current = more && !(cause instanceof EngineRequestError && cause.status === 400);
          setError(cause instanceof Error ? cause.message : 'Activity could not be loaded.');
        }
      }
      finally {
        running = false;
        if (!controller.signal.aborted) { setBusy(false); if (queued) { queued = false; void run(); } }
      }
    };
    operate.current = more => { void run(more); };
    void run();
    const wake = () => { if (document.visibilityState !== 'hidden') void run(); };
    const timer = setInterval(wake, 15000);
    window.addEventListener('focus', wake); document.addEventListener('visibilitychange', wake);
    return () => { controller.abort(); clearInterval(timer); window.removeEventListener('focus', wake); document.removeEventListener('visibilitychange', wake); operate.current = () => {}; };
  }, [query, ready, timeline]);
  useEffect(() => { if (visible) operate.current(); }, [visible, fingerprint]);
  useEffect(() => { const timer = setInterval(() => setClock(Date.now() + offset.current), 1000); return () => clearInterval(timer); }, []);
  useLayoutEffect(() => {
    const saved = anchor.current; anchor.current = undefined;
    if (!saved) return;
    const element = saved.id ? timeline.current?.querySelector<HTMLElement>(`[data-activity-id="${saved.id}"]`) : undefined;
    if (element) saved.scroller.scrollTop += element.getBoundingClientRect().top - saved.top;
    else saved.scroller.scrollTop = saved.scrollTop;
  }, [page, timeline]);
  const unavailable = useCallback((id: string) => {
    if (snapshot.current) { snapshot.current = { ...snapshot.current, items: snapshot.current.items.filter(item => item.id !== id) }; setPage(snapshot.current); }
    setNotice('An update is no longer available.'); operate.current();
  }, []);
  useEffect(() => { if (!notice) return; const timer = setTimeout(() => setNotice(''), 6000); return () => clearTimeout(timer); }, [notice]);
  // Never keep expired payload summaries in the mounted snapshot.
  useEffect(() => {
    const old = snapshot.current;
    if (!old) return;
    const items = old.items.filter(item => !item.expiresAt || Date.parse(item.expiresAt) > clock);
    if (items.length !== old.items.length) { snapshot.current = { ...old, items }; setPage(snapshot.current); }
  }, [clock]);
  const items = (page?.items ?? []).filter(item => projectIds.has(item.projectId) && (!item.expiresAt || Date.parse(item.expiresAt) > clock));
  return { catalog, overrides, savePreferences, preferenceError, setupError, busy, error, notice, clock,
    items, newPosts: items.filter(item => unseenIds.has(item.id)), showNewPosts,
    loaded: !!page, hasMore: page?.hasMore ?? false, unavailable,
    refresh: () => operate.current(), loadMore: () => operate.current(true),
    retry: () => { if (setupError) setSetupAttempt(value => value + 1); else operate.current(failedMore.current); },
  };
}
