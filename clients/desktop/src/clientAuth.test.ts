import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const base = 'https://engine.example.test';
const invoke = vi.fn();

async function transport(native = false, engine = base) {
  vi.resetModules();
  vi.doMock('./platform', () => ({ IS_DESKTOP: native, resolveEngineURL: () => engine, engineConnectionError: () => '' }));
  vi.doMock('@tauri-apps/api/core', () => ({ invoke }));
  return import('./clientAuth');
}

beforeEach(() => { localStorage.clear(); sessionStorage.clear(); });
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

it('sends cookie credentials and multipart CSRF headers, then blocks requests after a 401', async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response('{}', { status: 200 }))
    .mockResolvedValue(new Response('{}', { status: 401 }));
  vi.stubGlobal('fetch', fetch);
  const auth = await transport();
  const form = new FormData(); form.append('file', new Blob(['audio']), 'audio.m4a');
  await auth.clientFetch('/api/transcriptions', { method: 'POST', body: form });
  const [, init] = fetch.mock.calls[0];
  expect(init.credentials).toBe('include');
  expect(init.headers.get('X-KLM-CSRF')).toBe('1');
  expect(init.headers.has('Content-Type')).toBe(false);
  const denied = vi.fn(); window.addEventListener(auth.AUTH_REQUIRED_EVENT, denied);
  await auth.clientFetch('/api/auth/session');
  await auth.clientFetch('/api/updates');
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(denied).toHaveBeenCalledTimes(1);
  window.removeEventListener(auth.AUTH_REQUIRED_EVENT, denied);
});

it('loads native credentials from OS storage and sends them only as bearer headers', async () => {
  const credential = 'a'.repeat(43);
  invoke.mockResolvedValue(credential);
  const fetch = vi.fn().mockResolvedValue(new Response('{}'));
  vi.stubGlobal('fetch', fetch);
  const auth = await transport(true);
  await auth.clientFetch('/api/auth/session');
  expect(invoke).toHaveBeenCalledWith('client_credential_get', { engine: base });
  const [url, init] = fetch.mock.calls[0];
  expect(url).toBe(`${base}/api/auth/session`);
  expect(init.credentials).toBe('omit');
  expect(init.headers.get('Authorization')).toBe(`Bearer ${credential}`);
  expect(localStorage.getItem('credential')).toBeNull();
});

it('parses fragmented SSE and aborts the authenticated transport when closed', async () => {
  let streamController: ReadableStreamDefaultController<Uint8Array>;
  const body = new ReadableStream<Uint8Array>({ start(controller) { streamController = controller; } });
  const fetch = vi.fn().mockResolvedValue(new Response(body, { headers: { 'Content-Type': 'text/event-stream' } }));
  vi.stubGlobal('fetch', fetch);
  const auth = await transport();
  const source = auth.createClientEventSource(`${base}/api/updates?ids=x`);
  const message = vi.fn(); source.onmessage = message;
  await vi.waitFor(() => expect(source.readyState).toBe(1));
  const encoder = new TextEncoder();
  streamController!.enqueue(encoder.encode(': keepalive\n\ndata: {"kind":'));
  streamController!.enqueue(encoder.encode('"inventory"}\r\n\r\n'));
  await vi.waitFor(() => expect(message).toHaveBeenCalledTimes(1));
  expect(message.mock.calls[0][0].data).toBe('{"kind":"inventory"}');
  source.close();
  expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
  streamController!.close();
});

it('ignores an old 401 arriving after pairing without deleting the replacement native credential', async () => {
  const first = deferred<Response>();
  const late = deferred<Response>();
  const oldCredential = 'a'.repeat(43);
  const newCredential = 'b'.repeat(43);
  invoke.mockImplementation(async command => command === 'client_credential_get' ? oldCredential : undefined);
  const fetch = vi.fn()
    .mockReturnValueOnce(first.promise)
    .mockReturnValueOnce(late.promise)
    .mockResolvedValueOnce(new Response(JSON.stringify({ credential: newCredential })))
    .mockResolvedValue(new Response('{}'));
  vi.stubGlobal('fetch', fetch);
  const auth = await transport(true);
  const firstRequest = auth.clientFetch('/api/auth/session');
  const lateResult = auth.clientFetch('/api/state').catch(error => error);
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  first.resolve(new Response('{}', { status: 401 }));
  expect((await firstRequest).status).toBe(401);
  await auth.pairClient(base, 'Replacement', 'test-code');
  late.resolve(new Response('{}', { status: 401 }));
  expect(await lateResult).toMatchObject({ name: 'AbortError' });
  expect(invoke.mock.calls.filter(([command]) => command === 'client_credential_delete')).toHaveLength(1);
  expect(invoke).toHaveBeenCalledWith('client_credential_set', { engine: base, credential: newCredential });
  await auth.clientFetch('/api/auth/session');
  expect(fetch.mock.calls[3][1].headers.get('Authorization')).toBe(`Bearer ${newCredential}`);
});

it('uses engine B existing native storage after reload without pairing or forwarding engine A credential', async () => {
  const engineB = 'https://paired.example.test';
  const credentialA = 'a'.repeat(43);
  const credentialB = 'b'.repeat(43);
  invoke.mockImplementation(async (command, args) => command === 'client_credential_get' ? (args.engine === base ? credentialA : credentialB) : undefined);
  const fetch = vi.fn().mockRejectedValueOnce(new Error('Engine A unavailable')).mockResolvedValue(new Response('{}'));
  vi.stubGlobal('fetch', fetch);
  const authA = await transport(true);
  await expect(authA.clientFetch('/api/auth/session')).rejects.toThrow('Engine A unavailable');
  const authB = await transport(true, engineB);
  await authB.clientFetch('/api/auth/session');
  expect(invoke).toHaveBeenCalledWith('client_credential_get', { engine: engineB });
  expect(fetch.mock.calls[1][0]).toBe(`${engineB}/api/auth/session`);
  expect(fetch.mock.calls[1][1].headers.get('Authorization')).toBe(`Bearer ${credentialB}`);
  expect(fetch.mock.calls.some(([url]) => url.includes('/api/auth/pair'))).toBe(false);
});

it('pairs/verifies a candidate using only its own native key without changing the active generation or DB identity', async () => {
  const other = 'https://other.example.test/prefix';
  const credentials = new Map([[base, 'a'.repeat(43)]]);
  invoke.mockImplementation(async (command, args) => {
    if (command === 'client_credential_get') return credentials.get(args.engine) ?? null;
    if (command === 'client_credential_set') credentials.set(args.engine, args.credential);
  });
  const fetch = vi.fn(async (url: string, _init?: RequestInit) => new Response(JSON.stringify(url.endsWith('/pair') ? { credential: 'b'.repeat(43) } : { clientId: 'paired-client' })));
  vi.stubGlobal('fetch', fetch);
  const auth = await transport(true);
  const identity = await import('./engineIdentity');
  identity.bindEngineIdentity('active-database');
  await auth.initializeClientAuth();
  const version = auth.clientAuthGeneration();
  await auth.pairCandidateClient('https://OTHER.example.test:443/prefix/', 'Other client', 'one-use-code');
  expect(auth.clientAuthGeneration()).toBe(version);
  expect(identity.currentEngineIdentity()).toBe('active-database');
  const [pairURL, pairInit] = fetch.mock.calls[0];
  expect(pairURL).toBe(`${other}/api/auth/pair`);
  expect(new Headers(pairInit?.headers).has('Authorization')).toBe(false);
  expect(new Headers(pairInit?.headers).has('X-KLM-Engine-ID')).toBe(false);
  const [verifyURL, verifyInit] = fetch.mock.calls[1];
  expect(verifyURL).toBe(`${other}/api/auth/session`);
  expect(new Headers(verifyInit?.headers).get('Authorization')).toBe(`Bearer ${'b'.repeat(43)}`);
  expect(new Headers(verifyInit?.headers).has('X-KLM-Engine-ID')).toBe(false);
  expect(verifyInit?.credentials).toBe('omit');
  await auth.clientFetch('/api/auth/session');
  expect(new Headers(fetch.mock.calls[2][1]?.headers).get('Authorization')).toBe(`Bearer ${'a'.repeat(43)}`);
  expect(invoke.mock.calls.some(([command]) => command === 'client_credential_delete')).toBe(false);
});

it('keeps an active response valid after candidate pairing fails', async () => {
  const late = deferred<Response>();
  const other = 'https://other.example.test';
  invoke.mockImplementation(async command => command === 'client_credential_get' ? 'a'.repeat(43) : undefined);
  const fetch = vi.fn()
    .mockReturnValueOnce(late.promise)
    .mockResolvedValueOnce(new Response(JSON.stringify({ error: 'Invalid pairing code.' }), { status: 400 }))
    .mockResolvedValue(new Response(JSON.stringify({ clientId: 'paired-client' })));
  vi.stubGlobal('fetch', fetch);
  const auth = await transport(true);
  const active = auth.clientFetch('/api/state');
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
  const version = auth.clientAuthGeneration();
  await expect(auth.pairCandidateClient(other, 'Other client', 'bad-code')).rejects.toThrow('Invalid pairing code');
  expect(auth.clientAuthGeneration()).toBe(version);
  late.resolve(new Response('{}'));
  expect((await active).status).toBe(200);
  await auth.verifyCandidateClient(other);
  expect(fetch.mock.calls.filter(([url]) => url.endsWith('/api/auth/pair'))).toHaveLength(1);
});

it('recovers an accepted candidate pairing from native storage after verification fails, without consuming another code', async () => {
  const other = 'https://other.example.test';
  const credentials = new Map([[base, 'a'.repeat(43)]]);
  invoke.mockImplementation(async (command, args) => {
    if (command === 'client_credential_get') return credentials.get(args.engine) ?? null;
    if (command === 'client_credential_set') credentials.set(args.engine, args.credential);
  });
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ credential: 'b'.repeat(43) })))
    .mockRejectedValueOnce(new Error('Verification unavailable'))
    .mockResolvedValue(new Response(JSON.stringify({ clientId: 'paired-client' })));
  vi.stubGlobal('fetch', fetch);
  const auth = await transport(true);
  await expect(auth.pairCandidateClient(other, 'Other client', 'one-use-code')).rejects.toThrow('Verification unavailable');
  expect(credentials.get(other)).toBe('b'.repeat(43));
  await auth.verifyCandidateClient(other);
  expect(fetch.mock.calls.filter(([url]) => url.endsWith('/api/auth/pair'))).toHaveLength(1);
  expect(new Headers(fetch.mock.calls[2][1].headers).get('Authorization')).toBe(`Bearer ${'b'.repeat(43)}`);
  expect(auth.clientAuthGeneration()).toBe(0);
});

it('retains the active credential after failed active re-pairing and rejects its pre-pair responses', async () => {
  const late = deferred<Response>();
  invoke.mockImplementation(async command => command === 'client_credential_get' ? 'a'.repeat(43) : undefined);
  const fetch = vi.fn()
    .mockReturnValueOnce(late.promise)
    .mockResolvedValueOnce(new Response(JSON.stringify({ error: 'Invalid pairing code.' }), { status: 400 }))
    .mockResolvedValue(new Response('{}'));
  vi.stubGlobal('fetch', fetch);
  const auth = await transport(true);
  const active = auth.clientFetch('/api/state').catch(error => error);
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
  await expect(auth.pairClient(base, 'Replacement', 'bad-code')).rejects.toThrow('Invalid pairing code');
  late.resolve(new Response('{}', { status: 401 }));
  expect(await active).toMatchObject({ name: 'AbortError' });
  await auth.clientFetch('/api/auth/session');
  expect(fetch.mock.calls[2][1].headers.get('Authorization')).toBe(`Bearer ${'a'.repeat(43)}`);
  expect(invoke.mock.calls.some(([command]) => command === 'client_credential_delete')).toBe(false);
});

it('waits for an old native credential deletion before storing an active replacement', async () => {
  const deletion = deferred<void>();
  invoke.mockImplementation(async command => {
    if (command === 'client_credential_get') return 'a'.repeat(43);
    if (command === 'client_credential_delete') return deletion.promise;
  });
  vi.stubGlobal('fetch', vi.fn()
    .mockResolvedValueOnce(new Response('{}', { status: 401 }))
    .mockResolvedValue(new Response(JSON.stringify({ credential: 'b'.repeat(43) }))));
  const auth = await transport(true);
  await auth.clientFetch('/api/state');
  const paired = auth.pairClient(base, 'Replacement', 'test-code');
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  expect(invoke.mock.calls.some(([command]) => command === 'client_credential_set')).toBe(false);
  deletion.resolve();
  await paired;
  expect(invoke).toHaveBeenCalledWith('client_credential_set', { engine: base, credential: 'b'.repeat(43) });
});
