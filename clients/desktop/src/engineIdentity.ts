import { IS_DESKTOP, resolveEngineURL } from './platform';

const endpoint = resolveEngineURL();
const identityKey = `klm.engine-identity.v1:${endpoint}`;
let identity = (() => { try { return localStorage.getItem(identityKey) ?? ''; } catch { return ''; } })();
let generation = 0;
export const currentEngineIdentity = () => identity;
export const engineIdentityGeneration = () => generation;
export const engineStorageKey = (base: string) => identity ? `${base}:db:${identity}` : base;

export function persistEngineIdentity() {
  try { localStorage.setItem(identityKey, identity); } catch { /* Identity is still confirmed in memory. */ }
}

export function bindEngineIdentity(next: string, persist = true) {
  if (!next || next === identity) return false;
  const previous = identity;
  identity = next;
  if (previous) generation++;
  try {
    // Adopt pre-contract preferences only once. Later databases at the same URL
    // have separate entity/navigation/delivery state, including restored clones.
    if (!previous) for (const storage of [localStorage, sessionStorage]) {
      const keys = Array.from({ length: storage.length }, (_, i) => storage.key(i)).filter((key): key is string => !!key && key.includes(`:${endpoint}`) && !key.includes(':db:') && /^(klm\.workspace-navigation|klm\.pending-submissions|klm\.focus\.(layout|geometry)|klm\.happening-order)/.test(key) && !(IS_DESKTOP && key.startsWith('klm.pending-submissions')));
      for (const key of keys) { const value = storage.getItem(key); if (value !== null && storage.getItem(engineStorageKey(key)) === null) storage.setItem(engineStorageKey(key), value); }
    }
    if (persist) persistEngineIdentity();
  } catch { /* In-memory isolation still works when browser storage is full. */ }
  return !!previous;
}
