import { invoke, isTauri } from '@tauri-apps/api/core';
import { normalizeEngineURL } from './engineURL';
import { initializeEngineRegistry } from './features/engines/engineRegistry';

export { normalizeEngineURL } from './engineURL';

export const IS_DESKTOP = isTauri();

type MobileBridge = { postMessage: (message: string) => void };
const mobileBridge = (window as Window & { KlmMobile?: MobileBridge }).KlmMobile;
export const IS_MOBILE_HOST = typeof mobileBridge?.postMessage === 'function';

const engineURLStorageKey = 'klm.engine-url.v1';

function defaultEngineURL() {
  const hostname = IS_DESKTOP ? 'localhost' : window.location.hostname;
  if (!IS_DESKTOP && hostname === 'klm.codemob.com.br') return 'https://klmengine.codemob.com.br';
  const port = import.meta.env.DEV || window.location.port === '17332' ? '17331' : '7331';
  return `http://${hostname || 'localhost'}:${port}`;
}

function desktopConnection(): { url: string; error: string } {
  try {
    const registry = initializeEngineRegistry(() => {
      const override = import.meta.env.VITE_ENGINE_URL?.trim();
      if (override) return override;
      // Desktop migration must not replace unreadable/invalid legacy data with
      // another endpoint. Web/mobile retain their legacy fallback below.
      return localStorage.getItem(engineURLStorageKey)?.trim() || defaultEngineURL();
    });
    const active = registry.engines.find(engine => engine.id === registry.activeEngineId);
    return { url: active?.url ?? '', error: active ? '' : 'Select an engine to connect.' };
  } catch (error) {
    return { url: '', error: error instanceof Error ? error.message : 'Cannot load saved engines.' };
  }
}

// Resolve once, before auth/engine/identity modules capture their endpoint.
// An unusable desktop selection has no URL: authenticated requests fail closed
// instead of sending relative requests or silently falling back to localhost.
const desktop = IS_DESKTOP ? desktopConnection() : undefined;
export const engineConnectionError = () => desktop?.error ?? '';

export function returnToHosts() {
  mobileBridge?.postMessage('home');
}

export async function openContentDownload(url: string) {
  if (IS_DESKTOP) {
    await invoke('open_content_download', { url });
    return;
  }
  const downloadBridge = (window as Window & { KlmDownload?: MobileBridge }).KlmDownload;
  if (IS_MOBILE_HOST) {
    if (!downloadBridge) throw new Error('Update the mobile client to download content.');
    downloadBridge.postMessage(url);
    return;
  }
  const link = document.createElement('a');
  link.href = url;
  link.download = 'event.json';
  link.rel = 'noreferrer';
  link.click();
}

export function resolveEngineURL() {
  if (desktop) return desktop.url;
  const override = import.meta.env.VITE_ENGINE_URL?.trim();
  if (override) return normalizeEngineURL(override);
  try {
    const configured = localStorage.getItem(engineURLStorageKey)?.trim();
    if (configured) return normalizeEngineURL(configured);
  } catch { /* Invalid legacy selections or unavailable storage fall back to the URL-derived engine. */ }
  return defaultEngineURL();
}

export function configureEngineURL(value: string) {
  const configured = selectableEngineURL(value);
  if (desktop) {
    if (desktop.error) throw new Error(desktop.error);
    // The legacy forms cannot bypass draft preservation. The engine selector
    // will commit a registry selection only after its confirmed draft flush.
    if (configured !== desktop.url) throw new Error('This connection form only supports the active engine.');
    return configured;
  }
  localStorage.setItem(engineURLStorageKey, configured);
  return configured;
}

export function selectableEngineURL(value: string) {
  const configured = normalizeEngineURL(value);
  const override = import.meta.env.VITE_ENGINE_URL?.trim();
  if (!IS_DESKTOP && override && configured !== normalizeEngineURL(override)) {
    throw new Error('Engine URL is fixed by VITE_ENGINE_URL. Update the development override to switch engines.');
  }
  return configured;
}

export function reloadClient() { window.location.reload(); }

export async function openFocus() {
  await invoke('open_focus');
}

export async function startWindowDrag() {
  const { getCurrentWindow } = await import('@tauri-apps/api/window');
  await getCurrentWindow().startDragging();
}
