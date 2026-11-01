import { invoke } from '@tauri-apps/api/core';
import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type SetStateAction } from 'react';
import { currentEngineIdentity } from '../../engineIdentity';
import { IS_DESKTOP, resolveEngineURL } from '../../platform';
import { matchesDraftContainer } from './draftShape';

type Namespace = { endpoint: string; databaseId: string };
type Entries = Record<string, unknown>;
type Snapshot = { ready: boolean; error: string; namespace?: Namespace; entries: Entries };
const endpoint = resolveEngineURL();
let snapshot: Snapshot = { ready: !IS_DESKTOP, error: '', entries: {} };
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
const getSnapshot = () => snapshot;
let revision = 0;
let committedRevision = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
let writing: Promise<void> | undefined;
let loading: { databaseId: string; promise: Promise<void> } | undefined;
let sealed = false;

export function draftRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function publish(next: Snapshot) {
  snapshot = next;
  listeners.forEach(listener => listener());
}

function storageError(error: unknown) {
  // Native errors are fixed messages, never include document/secret content.
  return typeof error === 'string' ? error : error instanceof Error ? error.message : 'Draft storage is unavailable.';
}

function scheduleWrite() {
  clearTimeout(timer);
  timer = setTimeout(() => { void writePending().catch(() => { /* Error is retained in the subscribed snapshot. */ }); }, 400);
}

function writePending(): Promise<void> {
  clearTimeout(timer);
  if (writing) return writing;
  if (revision === committedRevision) return Promise.resolve();
  const namespace = snapshot.namespace;
  if (!namespace) return Promise.reject(new Error('Draft storage has no confirmed engine context.'));
  writing = (async () => {
    // One write in flight; edits during IPC become the next latest snapshot,
    // rather than a queued historical document for every keystroke.
    while (revision !== committedRevision) {
      const version = revision;
      const entries = snapshot.entries;
      if (Object.keys(entries).length) {
        await invoke('draft_write', { namespace, document: JSON.stringify({ version: 1, entries }) });
      } else await invoke('draft_delete', { namespace });
      committedRevision = version;
    }
    if (snapshot.error) publish({ ...snapshot, error: '' });
  })().catch(error => {
    publish({ ...snapshot, error: storageError(error) });
    throw error;
  }).finally(() => { writing = undefined; });
  return writing;
}

function ready() {
  return snapshot.ready && snapshot.namespace?.endpoint === endpoint && snapshot.namespace.databaseId === currentEngineIdentity();
}
export const draftStorageReady = ready;

// Explicitly called only after an authenticated bootstrap has bound engineId.
// prepare validates feature data and converts interrupted deliveries to uncertain.
export function loadDraftContext(databaseId: string, prepare: (entries: Entries) => Entries): Promise<void> {
  if (!IS_DESKTOP) return Promise.resolve();
  if (!databaseId || databaseId !== currentEngineIdentity()) return Promise.reject(new Error('Confirm the engine identity before loading drafts.'));
  if (ready() && snapshot.namespace?.databaseId === databaseId) return Promise.resolve();
  if (loading?.databaseId === databaseId) return loading.promise;
  const previous = loading?.promise;
  publish({ ...snapshot, ready: false, error: '' });
  clearTimeout(timer);
  const promise = (async () => {
    await previous?.catch(() => {});
    // A replacement database must not receive unsaved data from its predecessor.
    // Retain that old namespace/data on failure so Retry can flush it first.
    await writePending();
    const namespace = { endpoint, databaseId };
    const raw = await invoke<string | null>('draft_read', { namespace });
    let entries: Entries = {};
    if (raw !== null) {
      let document: unknown;
      try { document = JSON.parse(raw); } catch { throw new Error('Saved draft format is invalid or unsupported.'); }
      if (!draftRecord(document) || document.version !== 1 || !draftRecord(document.entries)) throw new Error('Saved draft format is invalid or unsupported.');
      entries = document.entries;
    }
    const restored = prepare(entries);
    revision = 0; committedRevision = 0;
    publish({ ready: true, error: '', namespace, entries: restored });
    if (JSON.stringify(restored) !== JSON.stringify(entries)) { revision++; scheduleWrite(); }
  })().catch(error => {
    publish({ ...snapshot, ready: false, error: storageError(error) });
    throw error;
  }).finally(() => { if (loading?.promise === promise) loading = undefined; });
  loading = { databaseId, promise };
  return promise;
}

export function readDraftValue<T>(key: string, fallback: T): T {
  if (!ready()) throw new Error(snapshot.error || 'Drafts have not loaded for this engine.');
  return Object.hasOwn(snapshot.entries, key) ? snapshot.entries[key] as T : fallback;
}

export function setDraftValue<T>(key: string, value: SetStateAction<T>, fallback: T) {
  const previous = readDraftValue(key, fallback);
  const next = typeof value === 'function' ? (value as (previous: T) => T)(previous) : value;
  if (Object.is(previous, next)) return;
  setDraftValues({ [key]: next });
}

// One publication/revision keeps related draft fields and their receipt coherent.
export function setDraftValues(values: Entries) {
  if (sealed) throw new DOMException('Engine workspace is detached.', 'AbortError');
  if (!ready()) throw new Error(snapshot.error || 'Drafts have not loaded for this engine.');
  if (Object.entries(values).every(([key, value]) => Object.hasOwn(snapshot.entries, key) && Object.is(snapshot.entries[key], value))) return;
  revision++;
  publish({ ...snapshot, entries: { ...snapshot.entries, ...values } });
  scheduleWrite();
}

export function deleteDraftValue(key: string) {
  if (sealed) throw new DOMException('Engine workspace is detached.', 'AbortError');
  readDraftValue(key, undefined);
  if (!Object.hasOwn(snapshot.entries, key)) return;
  const entries = { ...snapshot.entries }; delete entries[key];
  publish({ ...snapshot, entries }); revision++; scheduleWrite();
}

// Future engine selection must await this promise and stop on any failure.
export async function flushDrafts() {
  if (!IS_DESKTOP) return;
  if (!ready()) throw new Error(snapshot.error || 'Drafts have not loaded for this engine.');
  const namespace = snapshot.namespace;
  await writePending();
  if (!ready() || snapshot.namespace !== namespace) throw new DOMException('Draft context changed.', 'AbortError');
}

// Switching may leave a cold/offline/read-failed page without an entity context.
// It must still preserve any dirty memory under its original namespace.
export async function flushDraftsForEngineChange(seal = false) {
  if (!IS_DESKTOP) return;
  if (loading) { try { await loading.promise; } catch { /* Existing unreadable files are not replaced or discarded. */ } }
  if (seal) sealed = true;
  await writePending();
}

export function sealDraftEdits(value: boolean) { sealed = value; }

export function useDraftStorageStatus() {
  const state = useSyncExternalStore(subscribe, getSnapshot);
  return { ready: !IS_DESKTOP || ready(), error: state.error };
}

export function hasEngineDraft(key: string) { return IS_DESKTOP && ready() && Object.hasOwn(snapshot.entries, key); }

export function forgetDrafts(prefix: string) {
  if (!IS_DESKTOP) return;
  if (sealed) throw new DOMException('Engine workspace is detached.', 'AbortError');
  if (!ready()) throw new Error(snapshot.error || 'Drafts have not loaded for this engine.');
  const entries = Object.fromEntries(Object.entries(snapshot.entries).filter(([key]) => key !== prefix && !key.startsWith(`${prefix}.`)));
  if (Object.keys(entries).length === Object.keys(snapshot.entries).length) return;
  revision++; publish({ ...snapshot, entries }); scheduleWrite();
}

export function bindDraftDiscard() {
  const namespace = snapshot.namespace;
  return (prefix: string) => {
    if (IS_DESKTOP && namespace !== snapshot.namespace) throw new DOMException('Draft context changed.', 'AbortError');
    forgetDrafts(prefix);
  };
}

export function useEngineDraft<T>(key: string, initial: T | (() => T), retainInitial = false, validate?: (value: unknown) => boolean): [T, (value: SetStateAction<T>) => void, (next?: T) => void] {
  const [local, setLocal] = useState(initial);
  const state = useSyncExternalStore(subscribe, getSnapshot);
  const namespace = state.namespace;
  const fallback = useRef({ key, namespace, value: local });
  if (IS_DESKTOP && (fallback.current.key !== key || fallback.current.namespace !== namespace)) fallback.current = { key, namespace, value: typeof initial === 'function' ? (initial as () => T)() : initial };
  const value = IS_DESKTOP && ready() && Object.hasOwn(state.entries, key) ? state.entries[key] as T : IS_DESKTOP ? fallback.current.value : local;
  if (IS_DESKTOP && ready() && !(validate ? validate(value) : matchesDraftContainer(value, fallback.current.value))) throw new Error('Saved draft has an incompatible shape. The saved data has not been replaced.');
  const setValue = useCallback((next: SetStateAction<T>) => {
    if (IS_DESKTOP) {
      if (namespace !== snapshot.namespace || fallback.current.key !== key) throw new DOMException('Draft context changed.', 'AbortError');
      setDraftValue(key, next, fallback.current.value);
    }
    else setLocal(next);
  }, [key, namespace]);
  const clear = useCallback((next?: T) => {
    if (!IS_DESKTOP) return;
    if (namespace !== snapshot.namespace || fallback.current.key !== key) throw new DOMException('Draft context changed.', 'AbortError');
    fallback.current.value = next === undefined ? readDraftValue(key, fallback.current.value) : next;
    deleteDraftValue(key);
  }, [key, namespace]);
  useEffect(() => {
    if (IS_DESKTOP && retainInitial && namespace === snapshot.namespace && ready() && !hasEngineDraft(key) && fallback.current.value !== undefined) setDraftValues({ [key]: fallback.current.value });
    // Retaining a base is explicit; do not write editable defaults during hydration.
  }, [key, retainInitial, state.ready, state.namespace?.databaseId]);
  return [value, setValue, clear];
}
