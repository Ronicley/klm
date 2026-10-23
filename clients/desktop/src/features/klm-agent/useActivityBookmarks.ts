import { useEffect, useState } from 'react';
import { getActivityBookmarks, type ActivityBookmark, type GithubActivityItem } from './githubActivityApi';

export function useActivityBookmarks(revision: string | undefined, now: number, high?: string) {
  const [anchors, setAnchors] = useState<ActivityBookmark[]>([]);
  const ids = JSON.stringify(anchors.map(anchor => anchor.eventId).sort());
  useEffect(() => {
    const eventIds = JSON.parse(ids) as string[];
    if (!eventIds.length) return;
    const controller = new AbortController();
    const batches: string[][] = [];
    for (let i = 0; i < eventIds.length; i += 100) batches.push(eventIds.slice(i, i + 100));
    void Promise.all(batches.map(batch => getActivityBookmarks(batch, high, controller.signal))).then(pages => {
      if (controller.signal.aborted) return;
      const resolved = new Map(pages.flatMap(page => page.items).map(item => [item.eventId, item]));
      const requested = new Set(eventIds);
      setAnchors(current => current.flatMap(anchor => !requested.has(anchor.eventId) ? [anchor] : resolved.has(anchor.eventId) ? [resolved.get(anchor.eventId)!] : []));
    }).catch(() => { /* Preserve intent on transport failure; the next feed refresh retries. */ });
    return () => controller.abort();
  }, [ids, revision, high]);
  useEffect(() => {
    setAnchors(current => current.some(anchor => anchor.expiresAt && Date.parse(anchor.expiresAt) <= now)
      ? current.filter(anchor => !anchor.expiresAt || Date.parse(anchor.expiresAt) > now) : current);
  }, [now]);
  return {
    saved: (post: GithubActivityItem) => anchors.some(anchor => anchor.cardId === post.id && (!anchor.expiresAt || Date.parse(anchor.expiresAt) > now)),
    toggle: (post: GithubActivityItem) => setAnchors(current => current.some(anchor => anchor.cardId === post.id)
      ? current.filter(anchor => anchor.cardId !== post.id)
      : [...current.filter(anchor => anchor.eventId !== (post.detailEventId ?? post.id)), { eventId: post.detailEventId ?? post.id, cardId: post.id, expiresAt: post.group ? undefined : post.expiresAt }]),
  };
}
