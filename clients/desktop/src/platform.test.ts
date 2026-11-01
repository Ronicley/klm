import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const native = vi.hoisted(() => ({ desktop: true, invoke: vi.fn(), credentials: new Map<string, string>() }));
vi.mock('@tauri-apps/api/core', () => ({ isTauri: () => native.desktop, invoke: native.invoke }));

beforeEach(() => {
  vi.resetModules();
  localStorage.clear();
  native.credentials.clear();
  native.desktop = true;
  vi.stubEnv('VITE_ENGINE_URL', '');
  native.invoke.mockImplementation(async (command, args) => {
    if (command === 'client_credential_get') return native.credentials.get(args.engine) ?? null;
    if (command === 'client_credential_set') native.credentials.set(args.engine, args.credential);
    if (command === 'client_credential_delete') native.credentials.delete(args.engine);
  });
});
afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); vi.clearAllMocks(); });

it.each([
  ['https://KLMENGINE.codemob.com.br:443/Engine/', 'https://klmengine.codemob.com.br/Engine'],
  ['http://LOCALHOST:17331/prefix/', 'http://localhost:17331/prefix'],
  ['https://127.0.0.1:443/prefix/', 'https://127.0.0.1/prefix'],
  ['http://127.0.0.1:80/prefix/', 'http://127.0.0.1/prefix'],
])('uses the same canonical identity for override, saved selection and repeated normalization: %s', async (input, canonical) => {
  vi.stubEnv('VITE_ENGINE_URL', input);
  localStorage.setItem('klm.engine-url.v1', input);
  const platform = await import('./platform');
  expect(platform.resolveEngineURL()).toBe(canonical);
  expect(platform.selectableEngineURL(canonical)).toBe(canonical);
  expect(platform.normalizeEngineURL(canonical)).toBe(canonical);
  vi.stubEnv('VITE_ENGINE_URL', '');
  expect(platform.resolveEngineURL()).toBe(canonical);
  expect(platform.configureEngineURL(input)).toBe(canonical);
  const { readEngineRegistry } = await import('./features/engines/engineRegistry');
  expect(readEngineRegistry()?.engines[0].url).toBe(canonical);
  expect(localStorage.getItem('klm.engine-url.v1')).toBe(input); // Legacy preferences/credential keys are not rewritten.
  expect(platform.normalizeEngineURL('127.0.0.1')).toBe('http://127.0.0.1:7331');
});

it('pairs once under a default-port override and uses the identical native key and fetch base after reload', async () => {
  const override = 'https://KLMENGINE.codemob.com.br:443/engine/';
  const canonical = 'https://klmengine.codemob.com.br/engine';
  const credential = 'c'.repeat(43);
  vi.stubEnv('VITE_ENGINE_URL', override);
  const fetch = vi.fn(async (url: string, init?: RequestInit) => {
    if (url.endsWith('/api/auth/pair')) return new Response(JSON.stringify({ credential }));
    const authorized = new Headers(init?.headers).get('Authorization') === `Bearer ${credential}`;
    return new Response(JSON.stringify({ clientId: 'test-client' }), { status: authorized ? 200 : 401 });
  });
  vi.stubGlobal('fetch', fetch);

  const platform = await import('./platform');
  const engine = await import('./engine');
  const auth = await import('./clientAuth');
  await auth.initializeClientAuth();
  const selection = platform.selectableEngineURL('https://klmengine.codemob.com.br/engine');
  expect(selection).toBe(engine.ENGINE_URL); // The connection gate must not reload on equivalent spelling.
  await auth.pairClient(selection, 'Native client', 'test-code');
  expect(platform.configureEngineURL(override)).toBe(canonical);
  await expect(engine.request('/api/auth/session')).resolves.toEqual({ clientId: 'test-client' });

  vi.resetModules(); // Simulate a page reload; only the mocked OS credential store survives.
  const reloadedEngine = await import('./engine');
  expect(reloadedEngine.ENGINE_URL).toBe(canonical);
  await expect(reloadedEngine.request('/api/auth/session')).resolves.toEqual({ clientId: 'test-client' });
  expect(native.invoke.mock.calls.filter(([command]) => command === 'client_credential_get').map(([, args]) => args.engine)).toEqual([canonical, canonical]);
  expect(native.invoke).toHaveBeenCalledWith('client_credential_set', { engine: canonical, credential });
  expect(native.credentials.size).toBe(1);
  expect(fetch.mock.calls.filter(([url]) => url.endsWith('/api/auth/pair'))).toHaveLength(1);
  expect(fetch.mock.calls.every(([url]) => url.startsWith(`${canonical}/api/`))).toBe(true);
  expect(platform.selectableEngineURL('https://different.example.test/engine')).toBe('https://different.example.test/engine');
  expect(() => platform.configureEngineURL('https://different.example.test/engine')).toThrow('only supports the active engine');
  expect(fetch.mock.calls.filter(([url]) => url.endsWith('/api/auth/pair'))).toHaveLength(1);
});

it.each(['not a valid address', 'http://user:secret@localhost:7331', 'javascript:bad', 'http://192.168.1.2:7331'])(
  'fails closed on invalid desktop legacy data without overwriting it: %s', async saved => {
    localStorage.setItem('klm.engine-url.v1', saved);
    const platform = await import('./platform');
    expect(platform.resolveEngineURL()).toBe('');
    const engine = await import('./engine');
    expect(engine.ENGINE_URL).toBe('');
    const auth = await import('./clientAuth');
    await expect(auth.initializeClientAuth()).rejects.toThrow();
    expect(native.invoke).not.toHaveBeenCalled();
    expect(localStorage.getItem('klm.engine-url.v1')).toBe(saved);
  },
);

it('uses the override only as desktop seed and keeps the endpoint immutable until reload', async () => {
  vi.stubEnv('VITE_ENGINE_URL', 'https://seed.example.test');
  const platform = await import('./platform');
  const { readEngineRegistry, saveEngineRegistry, createSavedEngine } = await import('./features/engines/engineRegistry');
  const saved = readEngineRegistry()!;
  const destination = createSavedEngine('Other', 'https://other.example.test');
  saveEngineRegistry({ ...saved, engines: [...saved.engines, destination], activeEngineId: destination.id });
  expect(platform.resolveEngineURL()).toBe('https://seed.example.test');
  vi.resetModules();
  expect((await import('./platform')).resolveEngineURL()).toBe(destination.url);
});

it.each([null, 'corrupt'])('does not seed or fetch a fallback for a removed selection or corrupt registry: %s', async selection => {
  const { engineRegistryStorageKey } = await import('./features/engines/engineRegistry');
  const value = selection === null ? JSON.stringify({ version: 1, engines: [], activeEngineId: null }) : '{broken';
  localStorage.setItem(engineRegistryStorageKey, value);
  const fetch = vi.fn(); vi.stubGlobal('fetch', fetch);
  const platform = await import('./platform');
  const auth = await import('./clientAuth');
  expect(platform.resolveEngineURL()).toBe('');
  await expect(auth.clientFetch('/api/projects', { method: 'POST' })).rejects.toThrow();
  expect(fetch).not.toHaveBeenCalled();
  expect(native.invoke).not.toHaveBeenCalled();
  expect(localStorage.getItem(engineRegistryStorageKey)).toBe(value);
});

it('keeps browser/mobile legacy resolution and fixed override policy without creating a registry', async () => {
  native.desktop = false;
  localStorage.setItem('klm.engine-url.v1', 'invalid address');
  const platform = await import('./platform');
  const { engineRegistryStorageKey } = await import('./features/engines/engineRegistry');
  expect(platform.resolveEngineURL()).toBe('http://localhost:17331');
  expect(platform.configureEngineURL('https://browser.example.test/')).toBe('https://browser.example.test');
  vi.stubEnv('VITE_ENGINE_URL', 'https://override.example.test');
  expect(platform.resolveEngineURL()).toBe('https://override.example.test');
  expect(() => platform.selectableEngineURL('https://other.example.test')).toThrow('fixed by VITE_ENGINE_URL');
  expect(localStorage.getItem(engineRegistryStorageKey)).toBeNull();
});
