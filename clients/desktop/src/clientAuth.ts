import { invoke } from '@tauri-apps/api/core';
import { engineConnectionError, IS_DESKTOP, resolveEngineURL } from './platform';
import { currentEngineIdentity } from './engineIdentity';
import { normalizeEngineURL } from './engineURL';

const engine = resolveEngineURL();
let nativeCredential: string | null = null;
let blocked = false;
let generation = 0;
let initialized: Promise<void> | undefined;
let nativeDeletion: Promise<unknown> = Promise.resolve();
let pairingActive = false;
let pairingCandidate = false;
let readsPaused = false;
let transportStopped = false;
let readLifetime = new AbortController();
export const AUTH_REQUIRED_EVENT = 'klm-auth-required';

export function initializeClientAuth() {
  return initialized ??= (async () => {
    if (!engine) throw new Error(engineConnectionError() || 'Select an engine to connect.');
    if (IS_DESKTOP) {
      const version = generation;
      const credential = await invoke<string | null>('client_credential_get', { engine });
      if (version === generation) nativeCredential = credential;
    }
  })().catch(error => { initialized = undefined; throw error; });
}

export function clientAuthGeneration() { return generation; }
export function isClientAuthCurrent(version: number) { return version === generation && !blocked; }

export function retryClientAuth() { if (!pairingActive) { generation++; blocked = false; } }

export function pauseClientReads() { readsPaused = true; readLifetime.abort(); }
export function resumeClientReads() {
  if (!transportStopped && !readsPaused) return;
  readLifetime.abort(); transportStopped = false; readsPaused = false; readLifetime = new AbortController();
}
export async function stopClientTransport() { transportStopped = true; generation++; pauseClientReads(); await nativeDeletion; }

export async function removeSavedClientCredential(base: string) {
  if (!IS_DESKTOP) throw new Error('Saved engines require the desktop client.');
  await nativeDeletion;
  await invoke('client_credential_delete', { engine: normalizeEngineURL(base) });
}

export function invalidateClientAuth() {
  if (blocked) return;
  generation++;
  blocked = true;
  nativeCredential = null;
  if (IS_DESKTOP) nativeDeletion = invoke('client_credential_delete', { engine }).catch(() => {});
  window.dispatchEvent(new Event(AUTH_REQUIRED_EVENT));
}

export async function clientFetch(path: string, init: RequestInit = {}): Promise<Response> {
  if (transportStopped) throw new DOMException('Client connection closed.', 'AbortError');
  const read = !init.method || init.method.toUpperCase() === 'GET';
  if (IS_DESKTOP && read && readsPaused) throw new DOMException('Client reads paused.', 'AbortError');
  await initializeClientAuth();
  if (transportStopped || IS_DESKTOP && read && readsPaused) throw new DOMException('Client connection closed.', 'AbortError');
  if (blocked) return new Response(JSON.stringify({ error: 'Pair this client to access the engine.' }), { status: 401 });
  const version = generation;
  const headers = new Headers(init.headers);
  headers.set('X-KLM-CSRF', '1');
  headers.set('X-KLM-Read-Contract', '3');
  if (currentEngineIdentity()) headers.set('X-KLM-Engine-ID', currentEngineIdentity());
  if (IS_DESKTOP && nativeCredential) headers.set('Authorization', `Bearer ${nativeCredential}`);
  const signal = IS_DESKTOP && read ? init.signal ? AbortSignal.any([init.signal, readLifetime.signal]) : readLifetime.signal : init.signal;
  const response = await fetch(`${engine}${path}`, { ...init, signal, headers, credentials: IS_DESKTOP ? 'omit' : 'include' });
  // Responses from a previous credential must neither invalidate its replacement
  // nor supply protected data after a newer denial/retry/pairing transition.
  if (!isClientAuthCurrent(version)) throw new DOMException('Client authentication changed.', 'AbortError');
  if (response.status === 401) invalidateClientAuth();
  return response;
}

async function redeemPairingCode(base: string, name: string, code: string, onDispatch?: () => void) {
  onDispatch?.();
  const response = await fetch(`${base}/api/auth/pair`, {
    method: 'POST', credentials: IS_DESKTOP ? 'omit' : 'include',
    ...(IS_DESKTOP ? { redirect: 'error' as const } : {}),
    headers: { 'Content-Type': 'application/json', 'X-KLM-CSRF': '1', ...(IS_DESKTOP ? { 'X-KLM-Native': '1' } : {}) },
    body: JSON.stringify({ name, code }), signal: AbortSignal.timeout(15000),
  });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || 'Could not pair this client.');
  if (IS_DESKTOP && (typeof result.credential !== 'string' || !/^[\w-]{43}$/.test(result.credential))) {
    throw new Error('The engine did not return a valid native credential.');
  }
  return result.credential as string | undefined;
}

// Re-pair the active endpoint. Candidate pairing must never invalidate its
// generation, race its keyring deletion, or forward its DB identity/credential.
export async function pairClient(base: string, name: string, code: string, onDispatch?: () => void) {
  const target = normalizeEngineURL(base);
  if (IS_DESKTOP && target !== engine) throw new Error('This connection form only supports the active engine.');
  if (pairingActive) throw new Error('Client pairing is already in progress.');
  pairingActive = true;
  try {
    // Finish any original keyring read before installing a replacement.
    await initializeClientAuth();
    const previousCredential = nativeCredential;
    const previousBlocked = blocked;
    generation++;
    blocked = true;
    try {
      const credential = await redeemPairingCode(target, name, code, onDispatch);
      if (IS_DESKTOP) {
        await nativeDeletion;
        await invoke('client_credential_set', { engine: target, credential });
        nativeCredential = credential!;
      }
      generation++;
      blocked = false;
    } catch (error) {
      // Old responses remain obsolete even when pairing failed. A still-valid
      // active credential remains usable; an existing auth denial stays blocked.
      generation++;
      nativeCredential = previousCredential;
      blocked = previousBlocked;
      throw error;
    }
  } finally { pairingActive = false; }
}

function nativeEngineURL(base: string) {
  if (!IS_DESKTOP) throw new Error('Saved engines are available in the desktop client.');
  const target = normalizeEngineURL(base);
  if (target.length > 2048) throw new Error('The engine URL is too long.');
  return target;
}

function candidateURL(base: string) {
  const target = nativeEngineURL(base);
  if (target === engine) throw new Error('This engine is already the active connection.');
  return target;
}

// These temporary handshakes open no inventory/update stream and change no
// selection. The caller registers/activates only after draft persistence succeeds.
// Verification can be retried with the saved credential, without consuming a
// second pairing code when a later local registration/verification step fails.
export async function verifyCandidateClient(base: string) {
  const target = nativeEngineURL(base);
  const credential = await invoke<string | null>('client_credential_get', { engine: target });
  if (!credential) throw new Error('Pair this engine before connecting.');
  const response = await fetch(`${target}/api/auth/session`, {
    credentials: 'omit', redirect: 'error', signal: AbortSignal.timeout(15000),
    headers: { 'X-KLM-CSRF': '1', 'X-KLM-Read-Contract': '3', Authorization: `Bearer ${credential}` },
  });
  if (!response.ok) throw new Error(response.status === 401 ? 'Pair this engine again.' : 'Could not confirm access to the paired engine.');
  const session: unknown = await response.json();
  if (!session || typeof session !== 'object' || !('clientId' in session) || typeof session.clientId !== 'string') {
    throw new Error('The engine did not return a valid client session.');
  }
}

export async function pairCandidateClient(base: string, name: string, code: string, onDispatch?: () => void) {
  const target = candidateURL(base);
  if (pairingCandidate) throw new Error('Engine pairing is already in progress.');
  pairingCandidate = true;
  try {
    const credential = await redeemPairingCode(target, name, code, onDispatch);
    await invoke('client_credential_set', { engine: target, credential });
    await verifyCandidateClient(target);
  } finally { pairingCandidate = false; }
}

// Fetch streaming supports native Authorization headers, unlike EventSource.
// Keep the small EventSource interface used by the existing reconnect helper.
export function createClientEventSource(url: string): EventSource {
  const controller = new AbortController();
  const source = {
    readyState: 0, onopen: null, onmessage: null, onerror: null,
    close() { setState(2); controller.abort(); },
  } as unknown as EventSource;
  const setState = (state: number) => Object.defineProperty(source, 'readyState', { value: state, configurable: true });
  void (async () => {
    try {
      if (!url.startsWith(`${engine}/`)) throw new Error('Engine stream destination changed.');
      const response = await clientFetch(url.slice(engine.length), { signal: controller.signal });
      if (!response.ok || !response.body || !response.headers.get('content-type')?.startsWith('text/event-stream')) throw new Error('Engine stream unavailable.');
      setState(1);
      source.onopen?.call(source, new Event('open'));
      const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
      let buffer = '';
      let data: string[] = [];
      while (!controller.signal.aborted) {
        const chunk = await reader.read();
        if (chunk.done) break;
        buffer += chunk.value;
        let newline: number;
        while ((newline = buffer.indexOf('\n')) >= 0) {
          const line = buffer.slice(0, newline).replace(/\r$/, '');
          buffer = buffer.slice(newline + 1);
          if (line === '') {
            if (data.length) source.onmessage?.call(source, new MessageEvent('message', { data: data.join('\n') }));
            data = [];
          } else if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
        }
      }
      if (!controller.signal.aborted) throw new Error('Engine stream closed.');
    } catch {
      if (!controller.signal.aborted) {
        setState(2);
        source.onerror?.call(source, new Event('error'));
      }
    }
  })();
  return source;
}
