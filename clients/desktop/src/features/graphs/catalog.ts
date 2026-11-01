import { useCallback, useEffect, useSyncExternalStore } from 'react';
import { ENGINE_URL, getAuthoringCatalog } from '../../engine';
import { currentEngineIdentity, engineIdentityGeneration } from '../../engineIdentity';
import { subscribeScope } from '../chat/scopedConnection';
import type { AuthoringCatalog } from './files';

type CatalogState = { catalog: AuthoringCatalog | null; error: string; loading: boolean };
type Entry = { state: CatalogState; listeners: Set<() => void>; pending?: Promise<void> };
const entries = new Map<string, Entry>();
let cacheIdentity = '';
function catalogIdentity() { return `${ENGINE_URL}/${currentEngineIdentity()}/${engineIdentityGeneration()}`; }
function entryFor(projectId: string): Entry {
  const identity = catalogIdentity();
  if (cacheIdentity !== identity) { entries.clear(); cacheIdentity = identity; }
  const key = JSON.stringify([identity, projectId]);
  let entry = entries.get(key);
  if (!entry) {
    entry = { state: { catalog: null, error: '', loading: false }, listeners: new Set() };
    entries.set(key, entry);
  }
  return entry;
}
function publish(entry: Entry, state: CatalogState) {
  entry.state = state;
  entry.listeners.forEach(listener => listener());
}
// One catalog/request per project, shared by authoring and the conversation picker.
export function reloadAuthoringCatalog(projectId: string): Promise<void> {
  if (!projectId) return Promise.resolve();
  const entry = entryFor(projectId);
  if (entry.pending) return entry.pending;
  publish(entry, { ...entry.state, loading: true });
  entry.pending = getAuthoringCatalog(projectId).then(catalog => {
    publish(entry, { catalog, error: '', loading: false });
  }, error => {
    publish(entry, { ...entry.state, error: error instanceof Error ? error.message : 'Could not load project files.', loading: false });
    throw error;
  }).finally(() => { entry.pending = undefined; });
  return entry.pending;
}
export function useAuthoringCatalog(projectId: string) {
  const identity = catalogIdentity();
  const entry = entryFor(projectId);
  const subscribe = useCallback((listener: () => void) => {
    entry.listeners.add(listener);
    return () => { entry.listeners.delete(listener); };
  }, [entry]);
  const getSnapshot = useCallback(() => entry.state, [entry]);
  const state = useSyncExternalStore(subscribe, getSnapshot);
  const reload = useCallback(() => reloadAuthoringCatalog(projectId), [projectId, identity]);
  const invalidate = useCallback(async () => {
    // A pre-mutation request must not satisfy post-CRUD invalidation.
    await entry.pending?.catch(() => {});
    await reloadAuthoringCatalog(projectId);
  }, [entry, projectId]);
  useEffect(() => { void reload().catch(() => {}); }, [reload]);
  useEffect(() => {
    if (!projectId) return;
    const refresh = () => { void invalidate().catch(() => {}); };
    const global = subscribeScope('global', '', refresh);
    const project = subscribeScope('project', projectId, refresh);
    return () => { global(); project(); };
  }, [projectId, invalidate]);
  return { ...state, reload, invalidate };
}
