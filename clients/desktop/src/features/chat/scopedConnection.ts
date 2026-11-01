import { createClientEventSource } from '../../clientAuth';
import { ENGINE_URL, request, type EventPage, type Session, type SessionUpdate } from '../../engine';

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
  const base = `/api/sessions/${encodeURIComponent(id)}`;
  const position = current?.history?.hasNewer ? `?before=${current.history.startIndex + 50}` : '';
  const [session, page] = await Promise.all([request<Session>(base, 'GET', undefined, 15000, signal), request<EventPage>(`${base}/history${position}`, 'GET', undefined, 15000, signal)]);
  return { kind: 'reset', windowRefresh: !reset, revision: page.revision, summary: session, history: page, changes: [], total: page.total };
}
