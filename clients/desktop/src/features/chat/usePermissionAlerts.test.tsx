import { StrictMode } from 'react';
import { act, fireEvent, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { PermissionRequest, Project, Session } from '../../engine';
import { usePermissionAlerts } from './usePermissionAlerts';

vi.mock('@tauri-apps/api/core', () => ({ isTauri: () => false, invoke: vi.fn() }));
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('alerts new parent and graph permissions only without focus, once across reconciliation', async () => {
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
  const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  const permission = (id: string): PermissionRequest => ({ id, harness: 'pi', kind: 'command', title: 'Run command', description: '', patterns: [], decisions: ['once', 'reject'], createdAt: '' });
  const project: Project = { id: 'p', name: 'Project', folder: '', icon: '', folders: [], archivedFolders: ['Archive'] };
  const session: Session = { id: 'parent', projectId: 'p', title: 'Chat', workspace: 'Ungrouped', harness: 'pi', status: 'running', events: [], createdAt: '', updatedAt: '' };
  const hook = renderHook(({ sessions, loaded }) => usePermissionAlerts(sessions, [project], loaded), {
    initialProps: { sessions: [session], loaded: false },
    wrapper: StrictMode,
  });
  await act(async () => { fireEvent.pointerDown(window); });
  const initial = { ...session, permissions: [permission('old')] };
  hook.rerender({ sessions: [initial], loaded: true });
  expect(notify).not.toHaveBeenCalled();

  focused.mockReturnValue(true);
  const foreground = { ...initial, permissions: [...initial.permissions, permission('focused')] };
  hook.rerender({ sessions: [foreground], loaded: true });
  expect(notify).not.toHaveBeenCalled();
  expect(sound).not.toHaveBeenCalled();

  focused.mockReturnValue(false);
  const background = { ...foreground, permissions: [...foreground.permissions, permission('new')] };
  hook.rerender({ sessions: [background], loaded: true });
  expect(notify).toHaveBeenCalledTimes(1);
  expect(sound).toHaveBeenCalledTimes(1);
  expect(notify).toHaveBeenLastCalledWith('Permission required', expect.objectContaining({ body: 'Project · Chat · Run command', silent: true }));
  hook.rerender({ sessions: [session], loaded: true });
  hook.rerender({ sessions: [background], loaded: true });
  expect(notify).toHaveBeenCalledTimes(1);

  const graph: Session = { ...background, graph: { sessionId: session.id, revision: 1, selectedGraphId: 'g', run: null, requests: [
    { kind: 'permission', sessionId: 'node-session', requestId: 'new', nodeName: 'Builder', runId: 'run', graphId: 'g', nodeId: 'node', activationId: 'activation', permission: permission('new') },
    { kind: 'question', sessionId: 'node-session', requestId: 'question', nodeName: 'Builder', runId: 'run', graphId: 'g', nodeId: 'node', activationId: 'activation' },
  ] } };
  hook.rerender({ sessions: [graph], loaded: true });
  expect(notify).toHaveBeenCalledTimes(2);
  expect(sound).toHaveBeenCalledTimes(2);
  expect(notify).toHaveBeenLastCalledWith('Permission required', expect.objectContaining({ body: 'Project · Chat · Builder · Run command' }));

  hook.rerender({ sessions: [graph,
    { ...session, id: 'archived', archived: true, permissions: [permission('archive')] },
    { ...session, id: 'folder', workspace: 'Archive', permissions: [permission('folder')] },
    { ...session, id: 'side', role: 'side_agent', parentId: session.id, permissions: [permission('side')] },
    { ...session, id: 'resolving', permissions: [{ ...permission('resolving'), resolving: true }] },
  ], loaded: true });
  expect(notify).toHaveBeenCalledTimes(2);

  // A hidden tab is background even if the browser still reports document focus.
  focused.mockReturnValue(true);
  visibility.mockReturnValue('hidden');
  hook.rerender({ sessions: [graph, { ...session, id: 'other-parent', permissions: [permission('other')] }], loaded: true });
  expect(notify).toHaveBeenCalledTimes(3);
  expect(sound).toHaveBeenCalledTimes(3);
});
