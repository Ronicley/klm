import { useEffect, useState } from 'react';
import { ENGINE_URL, request, type ModelCatalog } from '../../engine';

type Entry = { value: ModelCatalog | null; at: number; error: string; loading: boolean; listeners: Set<() => void>; controller?: AbortController; flight?: Promise<void> };
const entries = new Map<string, Entry>();
function entry(key: string): Entry {
  let found = entries.get(key);
  if (!found) { found = { value: null, at: 0, error: '', loading: false, listeners: new Set() }; entries.set(key, found); }
  return found;
}
function notify(item: Entry) { item.listeners.forEach(listener => listener()); }

function load(key: string, sessionId: string, force = false) {
  const item = entry(key);
  if (item.flight) return item.flight;
  if (!force && Date.now() - item.at < 120000 && item.value) return Promise.resolve();
  const controller = new AbortController();
  item.controller = controller; item.loading = true; item.error = ''; notify(item);
  const flight = request<ModelCatalog>(`/api/sessions/${encodeURIComponent(sessionId)}/models${force ? '?refresh=true' : ''}`, 'GET', undefined, 55000, controller.signal)
    .then(value => { if (item.controller === controller && !controller.signal.aborted) { item.value = value; item.at = Date.now(); } })
    .catch(error => { if (!controller.signal.aborted) item.error = error instanceof Error ? error.message : 'Could not load models.'; })
    .finally(() => { if (item.controller === controller) { item.controller = undefined; item.flight = undefined; item.loading = false; notify(item); } });
  item.flight = flight;
  return flight;
}

export function useModelCatalog(projectId: string, harness: string, sessionId: string) {
  const key = `${ENGINE_URL}/${projectId}/${harness}`;
  const [, render] = useState(0);
  useEffect(() => {
    const item = entry(key);
    const listener = () => render(value => value + 1);
    item.listeners.add(listener);
    listener();
    void load(key, sessionId);
    return () => {
      item.listeners.delete(listener);
      // Let another picker acquire the flight during a session switch before
      // cancelling a discovery that no longer has any consumers.
      queueMicrotask(() => {
        if (!item.listeners.size) { item.controller?.abort(); item.controller = undefined; item.flight = undefined; item.loading = false; }
      });
    };
  }, [key, sessionId]);
  const item = entry(key);
  return { catalog: item.value, loading: item.loading, error: item.error, revalidate: () => load(key, sessionId), refresh: () => load(key, sessionId, true) };
}
