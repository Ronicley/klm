import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createSavedEngine, engineRegistryStorageKey, initializeEngineRegistry, readEngineRegistry, saveEngineRegistry, validateEngineRegistry } from './engineRegistry';

beforeEach(() => localStorage.clear());
afterEach(() => vi.restoreAllMocks());

it('migrates once, and does not seed again after explicit removal of the active selection', () => {
  const seed = vi.fn(() => 'https://ENGINE.example.test:443/prefix/');
  const registry = initializeEngineRegistry(seed);
  expect(registry.engines[0]).toMatchObject({ name: 'engine.example.test', url: 'https://engine.example.test/prefix' });
  expect(registry.activeEngineId).toBe(registry.engines[0].id);
  expect(initializeEngineRegistry(seed)).toEqual(registry);
  saveEngineRegistry({ ...registry, activeEngineId: null });
  expect(initializeEngineRegistry(seed).activeEngineId).toBeNull();
  expect(seed).toHaveBeenCalledTimes(1);
});

it('deduplicates canonical URLs, not names, and preserves distinct endpoint identities', () => {
  const first = createSavedEngine(' Same name ', 'https://ENGINE.example.test:443/path/');
  const second = createSavedEngine('Same name', 'https://alias.example.test/path', 'server');
  const registry = { version: 1, engines: [first, second], activeEngineId: first.id };
  expect(validateEngineRegistry(registry).engines).toHaveLength(2);
  expect(() => validateEngineRegistry({ ...registry, engines: [first, createSavedEngine('Duplicate', first.url)] })).toThrow('invalid');
  expect(() => validateEngineRegistry({ ...registry, activeEngineId: 'missing' })).toThrow('invalid');
});

it.each(['', '   ', 'n'.repeat(81)])('requires a bounded local name: %s', name => {
  expect(() => createSavedEngine(name, 'https://engine.example.test')).toThrow();
});

it('rejects unsupported icons and stores only public registration fields', () => {
  const engine = createSavedEngine('Engine', 'https://engine.example.test');
  expect(() => validateEngineRegistry({ version: 1, engines: [{ ...engine, iconId: 'unknown' }], activeEngineId: engine.id })).toThrow();
  const withExtraFields = { version: 1 as const, engines: [{ ...engine, pairingCode: 'not-metadata' }], activeEngineId: engine.id };
  const saved = saveEngineRegistry(withExtraFields);
  expect(saved.engines[0]).toEqual(engine);
  expect(localStorage.getItem(engineRegistryStorageKey)).not.toContain('pairingCode');
});

it('never replaces corrupt records or treats an unsuccessful migration write as complete', () => {
  localStorage.setItem(engineRegistryStorageKey, '{broken');
  const seed = vi.fn(() => 'https://engine.example.test');
  expect(() => initializeEngineRegistry(seed)).toThrow('invalid');
  expect(seed).not.toHaveBeenCalled();
  expect(localStorage.getItem(engineRegistryStorageKey)).toBe('{broken');
  localStorage.clear();
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('Quota'); });
  expect(() => initializeEngineRegistry(seed)).toThrow('Could not confirm');
  expect(localStorage.getItem(engineRegistryStorageKey)).toBeNull();
});

it('propagates read failures instead of initializing an empty or fallback registry', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('Storage unavailable'); });
  const seed = vi.fn(() => 'https://engine.example.test');
  expect(() => initializeEngineRegistry(seed)).toThrow('Cannot read saved engines');
  expect(() => readEngineRegistry()).toThrow('Cannot read saved engines');
  expect(seed).not.toHaveBeenCalled();
});
