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

let permissionAudio: AudioContext | undefined;

export async function preparePermissionAudio() {
  if (IS_DESKTOP || IS_MOBILE_HOST || typeof window.AudioContext !== 'function') return;
  permissionAudio ??= new AudioContext();
  if (permissionAudio.state === 'suspended') await permissionAudio.resume();
}

export function notificationPermission(): NotificationPermission | 'unsupported' {
  return !IS_MOBILE_HOST && window.isSecureContext && typeof Notification === 'function'
    ? Notification.permission : 'unsupported';
}

export async function enableNotifications() {
  // Both calls start within the button gesture, before awaiting either one.
  const audio = preparePermissionAudio();
  const permission = notificationPermission() === 'default' ? Notification.requestPermission() : Promise.resolve(notificationPermission());
  const [state] = await Promise.all([permission, audio]);
  return state;
}

export async function showPermissionAlert(body: string, tag: string) {
  if (IS_MOBILE_HOST) return;
  if (IS_DESKTOP) {
    await invoke('notify_permission', { body });
    return;
  }
  if (document.hasFocus() && document.visibilityState === 'visible') return;
  // Never resume suspended audio here: a blocked alert must not play later on focus.
  if (permissionAudio?.state === 'running') {
    try {
      const oscillator = permissionAudio.createOscillator();
      const gain = permissionAudio.createGain();
      const now = permissionAudio.currentTime;
      oscillator.frequency.setValueAtTime(880, now);
      gain.gain.setValueAtTime(0, now);
      gain.gain.linearRampToValueAtTime(0.15, now + 0.02);
      gain.gain.exponentialRampToValueAtTime(0.001, now + 0.3);
      oscillator.connect(gain).connect(permissionAudio.destination);
      oscillator.onended = () => { oscillator.disconnect(); gain.disconnect(); };
      oscillator.start(now);
      oscillator.stop(now + 0.3);
    } catch (error) { console.warn('Permission alert audio unavailable:', error); }
  }
  if (notificationPermission() !== 'granted') return;
  const notification = new Notification('Response required', { body, tag, silent: true });
  notification.onclick = () => { window.focus(); notification.close(); };
}
