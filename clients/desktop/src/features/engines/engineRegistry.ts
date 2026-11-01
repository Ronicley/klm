import { normalizeEngineURL } from '../../engineURL';

export const engineIconIds = ['monitor', 'server', 'cloud', 'house', 'database'] as const;
export type EngineIconId = typeof engineIconIds[number];
export type SavedEngine = { id: string; name: string; iconId?: EngineIconId; url: string };
export type EngineRegistry = { version: 1; engines: SavedEngine[]; activeEngineId: string | null };

// Tauri already separates app identifiers/origins; keep the development registry
// distinct too. Legacy endpoint/DB preference and native credential keys stay put.
export const engineRegistryStorageKey = `klm.engines.v1:${import.meta.env.DEV ? 'dev' : 'release'}`;
const invalidRegistry = () => new Error('Saved engine configuration is invalid. Restore it before connecting.');

function record(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

export function validateEngineRegistry(value: unknown): EngineRegistry {
  if (!record(value) || value.version !== 1 || !Array.isArray(value.engines) ||
      !(value.activeEngineId === null || typeof value.activeEngineId === 'string' && value.activeEngineId)) throw invalidRegistry();
  const ids = new Set<string>();
  const urls = new Set<string>();
  const engines = value.engines.map((entry): SavedEngine => {
    if (!record(entry) || typeof entry.id !== 'string' || !/^[\w-]{1,128}$/.test(entry.id) ||
        typeof entry.name !== 'string' || !entry.name.trim() || entry.name !== entry.name.trim() || [...entry.name].length > 80 ||
        typeof entry.url !== 'string' || entry.url.length > 2048 ||
        entry.iconId !== undefined && !engineIconIds.includes(entry.iconId as EngineIconId)) throw invalidRegistry();
    let url: string;
    try { url = normalizeEngineURL(entry.url); } catch { throw invalidRegistry(); }
    if (url !== entry.url || ids.has(entry.id) || urls.has(url)) throw invalidRegistry();
    ids.add(entry.id); urls.add(url);
    // Project only public metadata. Never persist arbitrary fields from a form.
    return { id: entry.id, name: entry.name, url, ...(entry.iconId !== undefined ? { iconId: entry.iconId as EngineIconId } : {}) };
  });
  if (value.activeEngineId !== null && !ids.has(value.activeEngineId as string)) throw invalidRegistry();
  return { version: 1, engines, activeEngineId: value.activeEngineId as string | null };
}

export function readEngineRegistry(): EngineRegistry | null {
  let raw: string | null;
  try { raw = localStorage.getItem(engineRegistryStorageKey); }
  catch { throw new Error('Cannot read saved engines. Reload the client to try again.'); }
  if (raw === null) return null;
  try { return validateEngineRegistry(JSON.parse(raw)); }
  catch { throw invalidRegistry(); }
}

export function saveEngineRegistry(registry: EngineRegistry) {
  const next = validateEngineRegistry(registry);
  // Do not turn a corrupt/unreadable existing registry into an empty/new one.
  readEngineRegistry();
  const serialized = JSON.stringify(next);
  try {
    localStorage.setItem(engineRegistryStorageKey, serialized);
    if (localStorage.getItem(engineRegistryStorageKey) !== serialized) throw new Error();
  } catch { throw new Error('Could not confirm the saved engine configuration. The current page is still using its original connection.'); }
  return next;
}

export function createSavedEngine(name: string, url: string, iconId?: EngineIconId): SavedEngine {
  const engine = { id: crypto.randomUUID(), name: name.trim(), url: normalizeEngineURL(url), ...(iconId !== undefined ? { iconId } : {}) };
  // Keep the same validation for migrated and user-created registrations.
  return validateEngineRegistry({ version: 1, engines: [engine], activeEngineId: engine.id }).engines[0];
}

// Only the explicit recovery UI may bypass validation of the original record.
// Its caller first preserves that exact original in protected pre-auth storage.
export function recoverEngineRegistry(registry: EngineRegistry, expectedOriginal: string | null) {
  const next = validateEngineRegistry(registry);
  if (localStorage.getItem(engineRegistryStorageKey) !== expectedOriginal) throw new Error('Saved engines changed. Read the configuration again before recovering it.');
  const serialized = JSON.stringify(next);
  try {
    localStorage.setItem(engineRegistryStorageKey, serialized);
    if (localStorage.getItem(engineRegistryStorageKey) !== serialized) throw new Error();
  } catch { throw new Error('Could not confirm the recovered configuration. Reload the client to check the saved selection.'); }
  return next;
}

export function initializeEngineRegistry(initialURL: () => string): EngineRegistry {
  const existing = readEngineRegistry();
  // null selection, including an empty registry after removal, is intentional.
  // Only a genuinely absent registry is eligible for legacy migration.
  if (existing) return existing;
  const url = normalizeEngineURL(initialURL());
  const name = [...new URL(url).hostname].slice(0, 80).join('');
  const engine = createSavedEngine(name, url);
  return saveEngineRegistry({ version: 1, engines: [engine], activeEngineId: engine.id });
}
