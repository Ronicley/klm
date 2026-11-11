import { createClientEventSource } from '../../clientAuth';
import { ENGINE_URL, request, type EventPage, type Session, type SessionUpdate } from '../../engine';
import { historyFollowing, PAGE_SIZE, serializeHistory } from '../pagination/pagination';

export type ScopeNotice = { kind: 'ready' | 'invalidate' | 'reset'; revision: string; scopes?: string[] };
type Listener = (notice: ScopeNotice) => void;
const scopes = new Map<string, Set<Listener>>();
let source: EventSource | undefined;
let sourceScopes = '';
let timer: ReturnType<typeof setTimeout> | undefined;
let delay = 1000;
let stopped = false;

export function stopScopedConnection() { stopped = true; clearTimeout(timer); source?.close(); source = undefined; }
export function resumeScopedConnection() { stopped = false; if (scopes.size) changed(); }

// All mounted scopes share one HTTP/1.1 connection, leaving API controls free.
function open() {
  if (stopped) return;
  timer = undefined;
  const keys = [...scopes.keys()].sort();
  const signature = JSON.stringify(keys);
  // Effects can remove and re-add the same scope in one commit. Keep its stream:
  // another ready notice would refresh catalogs and repeat that same cycle.
  if (source && signature === sourceScopes) return;
  source?.close(); source = undefined;
  if (!scopes.size) return;
  const params = new URLSearchParams({ contract: '3' });
  keys.forEach(key => params.append('watch', key));
  const current = createClientEventSource(`${ENGINE_URL}/api/updates?${params}`);
  source = current;
  sourceScopes = signature;
  current.onopen = () => { delay = 1000; };
  current.onmessage = event => {
    if (source !== current) return;
    try {
      const notice = JSON.parse(event.data) as ScopeNotice;
      if (['ready', 'invalidate', 'reset'].includes(notice.kind)) {
        for (const key of notice.scopes ?? keys) scopes.get(key)?.forEach(fn => fn(notice));
      }
    } catch { current.onerror?.call(current, new Event('error')); }
  };
  current.onerror = () => {
    if (source !== current) return;
    current.close(); source = undefined;
    // A removed watch can make reconnect return 404 before any ready frame.
    // Reconcile mounted scopes through their authenticated HTTP reads.
    for (const key of keys) scopes.get(key)?.forEach(fn => fn({ kind: 'reset', revision: '0' }));
    timer = setTimeout(open, delay); delay = Math.min(30000, delay * 2);
  };
}

function changed() {
  clearTimeout(timer);
  if (stopped) return;
  // Batch subscriptions mounted/removed in the same React commit.
  timer = setTimeout(open, 0);
}

export function subscribeScope(kind: 'global' | 'project' | 'conversation', id: string, listener: Listener) {
  const key = `${kind}/${id}`;
  let listeners = scopes.get(key);
  if (!listeners) { listeners = new Set(); scopes.set(key, listeners); changed(); }
  listeners.add(listener);
  return () => { listeners.delete(listener); if (!listeners.size) { scopes.delete(key); changed(); } };
}

if (import.meta.hot) import.meta.hot.dispose(() => { clearTimeout(timer); source?.close(); scopes.clear(); });

export async function readConversationWindow(id: string, current: Session | undefined, signal: AbortSignal, reset = false): Promise<SessionUpdate> {
  return serializeHistory(id, async () => {
  const base = `/api/sessions/${encodeURIComponent(id)}`;
  const session = await request<Session>(`${base}?limit=25`, 'GET', undefined, 15000, signal);
  const old = current?.history;
  let page: EventPage;
  if (old && current.events.length && (!historyFollowing(id) || old.hasNewer)) {
    const pages: EventPage[] = [];
    const end = old.startIndex + current.events.length;
    let after = old.startIndex - 1;
    do {
      const limit = Math.min(PAGE_SIZE, end - after - 1);
      const next = await request<EventPage>(`${base}/history?limit=${limit}${after >= 0 ? `&after=${after}` : `&before=${limit}`}`, 'GET', undefined, 15000, signal);
      if (!next.events?.length || next.endIndex <= after + 1) break;
      pages.push(next); after = next.endIndex - 1;
    } while (after + 1 < end);
    const first = pages[0]; const last = pages.at(-1);
    page = first && last ? { ...first, revision: last.revision, total: last.total, events: pages.flatMap(value => value.events ?? []), endIndex: last.endIndex, afterCursor: last.afterCursor, hasNewer: last.hasNewer } : { sessionId: id, revision: old.revision, startIndex: old.startIndex, endIndex: old.startIndex, total: old.total, events: [], hasMore: old.hasMore, hasNewer: old.hasNewer };
  } else page = await request<EventPage>(`${base}/history?limit=${PAGE_SIZE}`, 'GET', undefined, 15000, signal);
  return { kind: 'reset', windowRefresh: !reset, revision: page.revision, summary: session, history: page, changes: [], total: page.total };
  });
}
