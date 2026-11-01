import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { StrictMode } from 'react';
import { request } from '../../engine';
import { agentExecutionBlocked, patchSessionAgent, useSessionAgent } from './binding';
import { agentDetail, boundSession } from './sessionAgentFixtures';

const notices = vi.hoisted(() => [] as Array<() => void>);
vi.mock('../../engine', async original => ({ ...await original<typeof import('../../engine')>(), request: vi.fn() }));
vi.mock('../chat/scopedConnection', () => ({ subscribeScope: (_kind: string, _id: string, listener: () => void) => {
  notices.push(listener); return () => { const at = notices.indexOf(listener); if (at >= 0) notices.splice(at, 1); };
} }));
beforeEach(() => { vi.clearAllMocks(); notices.length = 0; });
afterEach(cleanup);

it('shares one read across mounted consumers and coalesces scoped refreshes without aborting', async () => {
  let finish: (value: unknown) => void = () => {};
  let signal: AbortSignal | undefined;
  vi.mocked(request).mockImplementation((_path, _method, _body, _timeout, suppliedSignal) => {
    signal = suppliedSignal;
    return new Promise(resolve => { finish = resolve; });
  });
  const view = renderHook(() => [useSessionAgent(boundSession), useSessionAgent(boundSession)]);
  await waitFor(() => expect(request).toHaveBeenCalledTimes(1));
  await act(async () => { notices[0](); notices[0](); });
  expect(signal!.aborted).toBe(false);
  await act(async () => { finish({ agent: agentDetail }); });
  expect(view.result.current[0].agent?.revision).toBe('1');
  await waitFor(() => expect(request).toHaveBeenCalledTimes(2));
  expect(view.result.current[1].agent?.revision).toBe('1');
  view.unmount();
  expect(signal!.aborted).toBe(true);
  await act(async () => { finish({ agent: agentDetail }); });
  expect(request).toHaveBeenCalledTimes(2);
});

it('does not overwrite a saved override with an older in-flight GET', async () => {
  const reads: Array<(value: unknown) => void> = [];
  const changed = { ...agentDetail, revision: '2', execution: { allowed: true, token: 'new-token' }, overrides: { effort: 'low' } };
  vi.mocked(request).mockImplementation((_path, method) => method === 'PATCH' ? Promise.resolve({ agent: changed }) : new Promise(resolve => reads.push(resolve)));
  const view = renderHook(() => useSessionAgent(boundSession));
  await waitFor(() => expect(reads).toHaveLength(1));
  await act(async () => { await patchSessionAgent(boundSession, '1', { effort: 'low' }); });
  expect(view.result.current.agent?.revision).toBe('2');
  await act(async () => { reads[0]({ agent: agentDetail }); });
  expect(view.result.current.agent?.revision).toBe('2');
  await waitFor(() => expect(reads).toHaveLength(2));
  await act(async () => { reads[1]({ agent: changed }); });
  expect(view.result.current.agent?.overrides).toEqual({ effort: 'low' });
  expect(vi.mocked(request).mock.calls.filter(call => call[1] === 'PATCH')[0][2]).toEqual({ revision: '1', overrides: { effort: 'low' } });
});

it('retains cache publication and eligibility after StrictMode cleanup/resubscription', async () => {
  const changed = { ...agentDetail, revision: '2', execution: { allowed: false, token: 'blocked-token', message: 'Source unavailable.' } };
  vi.mocked(request).mockImplementation((_path, method) => Promise.resolve({ agent: method === 'PATCH' ? changed : agentDetail }));
  const view = renderHook(() => useSessionAgent(boundSession), { wrapper: StrictMode });
  await waitFor(() => expect(view.result.current.agent?.revision).toBe('1'));
  await act(async () => { await patchSessionAgent(boundSession, '1', { prompt: 'Updated prompt' }); });
  expect(view.result.current.agent?.revision).toBe('2');
  expect(agentExecutionBlocked(boundSession)).toBe(true);
});
