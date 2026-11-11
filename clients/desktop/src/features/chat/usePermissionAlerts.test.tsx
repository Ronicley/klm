import { StrictMode } from 'react';
import { act, fireEvent, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { request } from '../../engine';
import { usePermissionAlerts } from './usePermissionAlerts';

const live = vi.hoisted(() => ({ listener: undefined as (() => void) | undefined, unsubscribe: vi.fn() }));
vi.mock('@tauri-apps/api/core', () => ({ isTauri: () => false, invoke: vi.fn() }));
vi.mock('../../engine', () => ({ request: vi.fn() }));
vi.mock('./scopedConnection', () => ({ subscribeScope: (_kind: string, _id: string, listener: () => void) => { live.listener = listener; return live.unsubscribe; } }));

const alert = (id: string, kind = 'permission', origin = '') => ({ id, kind, projectName: 'Project', sessionTitle: 'Chat', origin });
const page = (items: ReturnType<typeof alert>[], nextAfterId = '', engineId = 'engine') => ({ engineId, items, hasMore: !!nextAfterId, nextAfterId });
async function invalidate() {
  await act(async () => { live.listener?.(); await vi.advanceTimersByTimeAsync(750); });
}
beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.clearAllMocks(); });

it('alerts new questions and permissions in hidden clients without loading chats, once across reconciliation', async () => {
  const sound = vi.fn();
  vi.stubGlobal('AudioContext', class {
    state = 'running'; currentTime = 0; destination = {};
    createOscillator() { return { frequency: { setValueAtTime: vi.fn() }, connect: (node: unknown) => node, disconnect: vi.fn(), start: sound, stop: vi.fn() }; }
    createGain() { return { gain: { setValueAtTime: vi.fn(), linearRampToValueAtTime: vi.fn(), exponentialRampToValueAtTime: vi.fn() }, connect: vi.fn(), disconnect: vi.fn() }; }
  });
  const notify = vi.fn(function () { return { close: vi.fn() }; });
  vi.stubGlobal('Notification', Object.assign(notify, { permission: 'granted' }));
  vi.stubGlobal('isSecureContext', true);
  const focused = vi.spyOn(document, 'hasFocus').mockReturnValue(false);
  const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  let items = [alert('old')];
  vi.mocked(request).mockImplementation(async () => page(items));
  const hook = renderHook(({ loaded }) => usePermissionAlerts(loaded, 'engine'), { initialProps: { loaded: false }, wrapper: StrictMode });
  expect(request).not.toHaveBeenCalled();
  await act(async () => { fireEvent.pointerDown(window); hook.rerender({ loaded: true }); });
  expect(notify).not.toHaveBeenCalled();

  focused.mockReturnValue(true);
  visibility.mockReturnValue('visible');
  items = [...items, alert('focused')];
  await invalidate();
  expect(notify).not.toHaveBeenCalled();

  visibility.mockReturnValue('hidden');
  items = [...items, alert('question', 'question', 'Planner'), alert('permission', 'permission', 'Builder')];
  await invalidate();
  expect(notify).toHaveBeenCalledTimes(2);
  expect(sound).toHaveBeenCalledTimes(2);
  expect(notify).toHaveBeenCalledWith('Response required', expect.objectContaining({ body: 'Project · Chat · Planner · Question received', silent: true }));
  expect(notify).toHaveBeenLastCalledWith('Response required', expect.objectContaining({ body: 'Project · Chat · Builder · Permission required' }));
  const previous = items;
  items = [];
  await invalidate();
  items = previous;
  await invalidate();
  expect(notify).toHaveBeenCalledTimes(2);
  expect(vi.mocked(request).mock.calls.every(([path]) => path.startsWith('/api/activity/requests'))).toBe(true);
  hook.unmount();
  expect(live.unsubscribe).toHaveBeenCalled();
});

it('baselines every pending page and finds new requests on later pages', async () => {
  const notify = vi.fn(function () { return { close: vi.fn() }; });
  vi.stubGlobal('Notification', Object.assign(notify, { permission: 'granted' }));
  vi.stubGlobal('isSecureContext', true);
  vi.spyOn(document, 'hasFocus').mockReturnValue(false);
  let later = [alert('old-second-page')];
  vi.mocked(request).mockImplementation(async path => path.includes('afterId=') ? page(later) : page([alert('old-first-page')], 'old-first-page'));
  const hook = renderHook(() => usePermissionAlerts(true, 'engine'));
  await act(async () => {});
  expect(request).toHaveBeenCalledWith('/api/activity/requests?afterId=old-first-page', 'GET', undefined, 15000, expect.any(AbortSignal));
  expect(notify).not.toHaveBeenCalled();
  later = [...later, alert('new-question', 'question')];
  await invalidate();
  expect(notify).toHaveBeenCalledTimes(1);
  await invalidate();
  expect(notify).toHaveBeenCalledTimes(1);
  hook.unmount();
});

it('keeps fallback reads active while hidden and discards responses from another engine', async () => {
  const notify = vi.fn(function () { return { close: vi.fn() }; });
  vi.stubGlobal('Notification', Object.assign(notify, { permission: 'granted' }));
  vi.stubGlobal('isSecureContext', true);
  vi.spyOn(document, 'hasFocus').mockReturnValue(false);
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  let response = page([]);
  vi.mocked(request).mockImplementation(async () => response);
  const hook = renderHook(({ identity }) => usePermissionAlerts(true, identity), { initialProps: { identity: 'engine' } });
  await act(async () => {});
  response = page([alert('other-engine')], '', 'other');
  await act(async () => { await vi.advanceTimersByTimeAsync(30750); });
  expect(request).toHaveBeenCalledTimes(2);
  expect(notify).not.toHaveBeenCalled();
  await act(async () => { hook.rerender({ identity: 'other' }); });
  expect(notify).not.toHaveBeenCalled();
  response = page([alert('other-engine'), alert('new')], '', 'other');
  await invalidate();
  expect(notify).toHaveBeenCalledTimes(1);
  hook.unmount();
});
