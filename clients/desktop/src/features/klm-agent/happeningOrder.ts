import { useSyncExternalStore } from 'react';
import { ENGINE_URL } from '../../engine';

const storageKey = `klm.happening-order.v1:${ENGINE_URL}`;
const listeners = new Set<() => void>();
let cached: string[] | undefined;

function readOrder(): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(storageKey) ?? 'null');
    return Array.isArray(value) ? [...new Set(value.filter((id): id is string => typeof id === 'string' && id.length > 0))] : [];
  } catch { return []; }
}

function snapshot() {
  return cached ??= readOrder();
}

function onStorage(event: StorageEvent) {
  if (event.key !== storageKey && event.key !== null) return;
  cached = readOrder();
  listeners.forEach(listener => listener());
}

function subscribe(listener: () => void) {
  if (!listeners.size) {
    // Reconcile edits made by another tab while no list was mounted.
    const stored = readOrder();
    if (!cached || stored.join('\n') !== cached.join('\n')) cached = stored;
    window.addEventListener('storage', onStorage);
  }
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
    if (!listeners.size) window.removeEventListener('storage', onStorage);
  };
}

export function saveHappeningOrder(ids: string[]) {
  cached = [...new Set(ids)];
  try { localStorage.setItem(storageKey, JSON.stringify(cached)); }
  catch { /* Ordering remains shared between mounted lists without storage. */ }
  listeners.forEach(listener => listener());
}

export function useHappeningOrder() {
  return useSyncExternalStore(subscribe, snapshot);
}
