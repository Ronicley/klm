import { invoke } from '@tauri-apps/api/core';
import { useSyncExternalStore } from 'react';
import { engineIconIds, type EngineIconId } from './engineRegistry';

export type ConnectionForm = {
  id: string; name: string; iconId?: EngineIconId; url: string;
  clientName: string; code: string; codeURL?: string; attemptedCode?: string;
};
type Document = { version: 1; forms: Record<string, ConnectionForm>; registryOriginals: string[]; recoveryJSON?: string };
type State = { ready: boolean; error: string; document: Document };
// Profile-owned connection management, not an engine network destination. Add
// forms carry a runtime-endpoint key; edits/repair carry a saved connection ID.
// Recovery originals remain accessible after selecting a different connection.
const namespace = { endpoint: 'http://localhost', databaseId: null };
let state: State = { ready: false, error: '', document: { version: 1, forms: {}, registryOriginals: [] } };
const listeners = new Set<() => void>();
let loading: Promise<void> | undefined;
let writing: Promise<void> | undefined;
let revision = 0, committed = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
const publish = (next: State) => { state = next; listeners.forEach(listener => listener()); };
const errorText = (error: unknown) => typeof error === 'string' ? error : error instanceof Error ? error.message : 'Connection drafts are unavailable.';
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);

export function loadConnectionDrafts() {
  if (state.ready) return Promise.resolve();
  return loading ??= (async () => {
    const raw = await invoke<string | null>('draft_read', { namespace });
    let document: Document = { version: 1, forms: {}, registryOriginals: [] };
    if (raw !== null) {
      let parsed: unknown;
      try { parsed = JSON.parse(raw); } catch { throw new Error('Saved connection draft format is invalid.'); }
      if (!record(parsed) || parsed.version !== 1 || !record(parsed.forms) || !Array.isArray(parsed.registryOriginals) || !parsed.registryOriginals.every(value => typeof value === 'string')) throw new Error('Saved connection draft format is invalid.');
      if (parsed.recoveryJSON !== undefined && typeof parsed.recoveryJSON !== 'string') throw new Error('Saved connection draft format is invalid.');
      for (const form of Object.values(parsed.forms)) if (!record(form) || !['id', 'name', 'url', 'clientName', 'code'].every(key => typeof form[key] === 'string') || form.attemptedCode !== undefined && typeof form.attemptedCode !== 'string' || form.codeURL !== undefined && typeof form.codeURL !== 'string') throw new Error('Saved connection draft format is invalid.');
      for (const form of Object.values(parsed.forms)) if (record(form) && form.iconId !== undefined && !engineIconIds.includes(form.iconId as EngineIconId)) throw new Error('Saved connection draft icon is invalid.');
      document = parsed as Document;
    }
    publish({ ready: true, error: '', document });
  })().catch(error => { publish({ ...state, error: errorText(error) }); throw error; }).finally(() => { loading = undefined; });
}

export function updateConnectionDrafts(update: (document: Document) => Document) {
  if (!state.ready) throw new Error(state.error || 'Connection drafts have not loaded.');
  revision++; publish({ ...state, document: update(state.document) });
  clearTimeout(timer);
  timer = setTimeout(() => { void flushConnectionDrafts().catch(() => {}); }, 400);
}

export async function flushConnectionDrafts() {
  clearTimeout(timer);
  if (loading) { try { await loading; } catch { /* Unreadable existing files remain untouched if no edits exist. */ } }
  if (writing) return writing;
  if (revision === committed) return;
  writing = (async () => {
    while (committed !== revision) {
      const version = revision;
      await invoke('draft_write', { namespace, document: JSON.stringify(state.document) });
      committed = version;
    }
    publish({ ...state, error: '' });
  })().catch(error => { publish({ ...state, error: errorText(error) }); throw error; }).finally(() => { writing = undefined; });
  return writing;
}

export const readConnectionDrafts = () => state;
export function useConnectionDrafts() {
  return useSyncExternalStore(listener => { listeners.add(listener); return () => { listeners.delete(listener); }; }, readConnectionDrafts);
}
