import { invoke, isTauri } from '@tauri-apps/api/core';

export const IS_DESKTOP = isTauri();

type MobileBridge = { postMessage: (message: string) => void };
const mobileBridge = (window as Window & { KlmMobile?: MobileBridge }).KlmMobile;
export const IS_MOBILE_HOST = typeof mobileBridge?.postMessage === 'function';

const engineURLStorageKey = 'klm.engine-url.v1';

export function returnToHosts() {
  mobileBridge?.postMessage('home');
}

export function resolveEngineURL() {
  const override = import.meta.env.VITE_ENGINE_URL?.trim();
  if (override) return override.replace(/\/+$/, '');
  try {
    const configured = localStorage.getItem(engineURLStorageKey)?.trim();
    if (configured) return configured.replace(/\/+$/, '');
  } catch { /* Fall back to the URL-derived engine when storage is unavailable. */ }
  const hostname = IS_DESKTOP ? 'localhost' : window.location.hostname;
  const port = import.meta.env.DEV || window.location.port === '17332' ? '17331' : '7331';
  return `http://${hostname || 'localhost'}:${port}`;
}

export function configureEngineURL(value: string) {
  let address = value.trim();
  const hasScheme = /^[a-z][a-z\d+.-]*:\/\//i.test(address);
  if (!hasScheme) {
    // A bare IPv6 literal needs brackets before URL parsing.
    if (/^[\da-f:]+$/i.test(address) && address.includes("::")) address = "[" + address + "]";
    const candidate = new URL("http://" + address);
    const ip = candidate.hostname.startsWith("[") || /^\d+\.\d+\.\d+\.\d+$/.test(candidate.hostname);
    address = (ip ? "http://" : "https://") + address;
  }
  const url = new URL(address);
  if (url.username || url.password) throw new Error("The engine URL cannot include credentials.");
  const ip = url.hostname.startsWith("[") || /^\d+\.\d+\.\d+\.\d+$/.test(url.hostname);
  // URL.port omits explicit :80/:443, so inspect the original authority.
  const authority = address.slice(address.indexOf("://") + 3).split(/[/?#]/, 1)[0];
  if (ip && !/:\d+$/.test(authority)) url.port = "7331";
  if (url.protocol !== 'http:' && url.protocol !== 'https:') throw new Error('Use an HTTP or HTTPS URL.');
  if (url.search || url.hash) throw new Error('The engine URL cannot include a query or fragment.');
  const configured = url.toString().replace(/\/+$/, '');
  localStorage.setItem(engineURLStorageKey, configured);
  return configured;
}

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
  const notification = new Notification('Permission required', { body, tag, silent: true });
  notification.onclick = () => { window.focus(); notification.close(); };
}
