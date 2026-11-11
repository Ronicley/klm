export const PAGE_SIZE = 25;
export const EDGE_MARGIN = 160;

export function scrollRoot(element: HTMLElement): HTMLElement {
  for (let parent = element.parentElement; parent; parent = parent.parentElement) {
    if (/(auto|scroll)/.test(getComputedStyle(parent).overflowY)) return parent;
  }
  return document.documentElement;
}

export function mergeItems<T>(previous: T[], incoming: T[], key: (item: T) => string): T[] {
  return [...new Map([...previous, ...incoming].map(item => [key(item), item])).values()];
}

// Main chat and Focus may show the same history concurrently.
const operations = new Map<string, Promise<unknown>>();
export async function serializeHistory<T>(id: string, run: () => Promise<T>): Promise<T> {
  const previous = operations.get(id);
  const current = (previous ? previous.catch(() => {}) : Promise.resolve()).then(run);
  operations.set(id, current);
  try { return await current; }
  finally { if (operations.get(id) === current) operations.delete(id); }
}

const followers = new Map<string, Map<object, boolean>>();
export function followHistory(id: string, viewer: object, following?: boolean) {
  const views = followers.get(id) ?? new Map<object, boolean>();
  if (following === undefined) views.delete(viewer); else views.set(viewer, following);
  if (views.size) followers.set(id, views); else followers.delete(id);
}
export const historyFollowing = (id: string) => [...(followers.get(id)?.values() ?? [])].every(Boolean);
