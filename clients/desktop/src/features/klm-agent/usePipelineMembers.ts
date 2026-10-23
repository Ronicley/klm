import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { compareActivitySequence, getActivityPage, type GithubActivityItem, type GithubActivityPage } from './githubActivityApi';

type Window = { items: GithubActivityItem[]; page: GithubActivityPage; high: string; boundary?: string; composition: string; snapshotComposition: string };
const retained = (item: GithubActivityItem, now: number) => !item.expiresAt || Date.parse(item.expiresAt) > now;

// The mounted reader owns its watermark/window. Feed updates reconcile retention
// at that watermark; only an explicit update admits newer transitions.
export function usePipelineMembers(post: GithubActivityItem, query: string, view: 'events' | 'children', now: number) {
  const root = useRef<HTMLDivElement>(null);
  const snapshot = useRef<Window | undefined>(undefined);
  const handled = useRef(-1);
  const clock = useRef(now); clock.current = now;
  const [window, setWindow] = useState<Window>();
  const [request, setRequest] = useState({ id: 0, more: false, high: post.group!.highWatermark });
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState('');
  const anchor = useRef<{ element?: HTMLElement; top: number; scrollTop: number; scroller: HTMLElement } | undefined>(undefined);
  const composition = post.group!.composition;

  useEffect(() => {
    const controller = new AbortController(); setBusy(true); setError('');
    const previous = snapshot.current;
    const explicit = handled.current !== request.id;
    const more = explicit && request.more && !!previous?.page.nextCursor && previous.composition === composition;
    const high = explicit ? request.high : previous?.high ?? request.high;
    const params = new URLSearchParams(query);
    params.set('cardId', post.id); params.set('view', view); params.set('limit', '20'); params.set('high', high);
    void (async () => {
      try {
        let page = await getActivityPage(params.toString(), more ? previous?.page.nextCursor : undefined, controller.signal);
        const items = more ? [...(previous?.items ?? []), ...page.items] : [...page.items];
        // Revalidate the whole already-loaded window, rather than replacing it
        // with page one. Missing members disappear; surviving rows keep their keys/Details.
        while (!more && previous?.boundary && page.hasMore && page.nextCursor && (!page.items.length || compareActivitySequence(page.items.at(-1)!.releaseSequence, previous.boundary) > 0)) {
          page = await getActivityPage(params.toString(), page.nextCursor, controller.signal); items.push(...page.items);
        }
        if (controller.signal.aborted) return;
        const timeline = root.current?.closest<HTMLElement>('.activity-timeline');
        const scroller = timeline && (getComputedStyle(timeline).overflowY === 'visible' ? timeline.closest<HTMLElement>('.activity-feed-scroll') : timeline);
        if (scroller) {
          const top = scroller.getBoundingClientRect().top;
          const element = [...(root.current?.querySelectorAll<HTMLElement>('[data-activity-member-id]') ?? [])].find(row => row.getBoundingClientRect().bottom > top);
          anchor.current = { element, top: element?.getBoundingClientRect().top ?? 0, scrollTop: scroller.scrollTop, scroller };
        }
        const next = { items: [...new Map(items.filter(item => retained(item, Math.max(clock.current, Date.parse(page.asOf)))).map(item => [item.id, item])).values()], page, high, composition,
          snapshotComposition: previous && previous.high === high ? previous.snapshotComposition : composition,
          boundary: items.at(-1)?.releaseSequence ?? previous?.boundary };
        snapshot.current = next; handled.current = request.id; setWindow(next);
      } catch (cause) {
        // Do not leave unvalidated cached Details visible after a failed retention check.
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Activity could not be loaded.');
      } finally { if (!controller.signal.aborted) setBusy(false); }
    })();
    return () => controller.abort();
  }, [post.id, composition, query, view, request]);

  useEffect(() => {
    const previous = snapshot.current;
    if (!previous) return;
    const items = previous.items.filter(item => retained(item, now));
    if (items.length !== previous.items.length) { const next = { ...previous, items }; snapshot.current = next; setWindow(next); }
  }, [now]);
  useLayoutEffect(() => {
    const saved = anchor.current; anchor.current = undefined;
    if (!saved) return;
    if (saved.element?.isConnected) saved.scroller.scrollTop += saved.element.getBoundingClientRect().top - saved.top;
    else saved.scroller.scrollTop = saved.scrollTop;
  }, [window]);

  return { root, items: error ? [] : (window?.items ?? []).filter(item => retained(item, now)), busy, error,
    hasMore: window?.page.hasMore ?? false,
    hasUpdates: !!window && (compareActivitySequence(post.releaseSequence, window.high) > 0 || composition !== window.snapshotComposition && compareActivitySequence(post.group!.highWatermark, window.high) > 0),
    loadMore: () => setRequest(current => ({ id: current.id + 1, more: true, high: snapshot.current?.high ?? current.high })),
    update: () => setRequest(current => ({ id: current.id + 1, more: false, high: post.group!.highWatermark })),
    retry: () => setRequest(current => ({ ...current, id: current.id + 1 })),
  };
}
