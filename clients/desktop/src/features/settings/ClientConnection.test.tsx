import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { clientFetch, retryClientAuth } from '../../clientAuth';
import { ClientConnection } from './ClientConnection';

const fixture = vi.hoisted(() => ({ base: 'https://offline.example.test', reload: vi.fn() }));
vi.mock('../../platform', async importOriginal => ({
  ...await importOriginal<typeof import('../../platform')>(),
  IS_DESKTOP: false,
  resolveEngineURL: () => fixture.base,
  reloadClient: fixture.reload,
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

beforeEach(() => {
  localStorage.clear();
  vi.stubEnv('VITE_ENGINE_URL', '');
  retryClientAuth();
});
afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); vi.clearAllMocks(); });

it('does not remount protected content when a late successful renewal follows a newer denial', async () => {
  const renewal = deferred<Response>();
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response('{}'))
    .mockReturnValueOnce(renewal.promise)
    .mockResolvedValue(new Response('{}', { status: 401 }));
  vi.stubGlobal('fetch', fetch);
  render(<ClientConnection><div>Protected workspace</div></ClientConnection>);
  expect(await screen.findByText('Protected workspace')).toBeInTheDocument();
  fireEvent.focus(window);
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  await act(async () => { await clientFetch('/api/state'); });
  expect(screen.queryByText('Protected workspace')).not.toBeInTheDocument();
  await act(async () => { renewal.resolve(new Response('{}')); });
  expect(screen.getByRole('heading', { name: 'Connect to engine' })).toBeInTheDocument();
  expect(screen.queryByText('Protected workspace')).not.toBeInTheDocument();
  expect(fetch).toHaveBeenCalledTimes(3);
});

it('selects already-paired engine B from unavailable A without requiring or redeeming a code', async () => {
  const engineB = 'https://paired.example.test';
  const fetch = vi.fn().mockRejectedValue(new Error('Engine A unavailable'));
  vi.stubGlobal('fetch', fetch);
  render(<ClientConnection><div>Protected workspace</div></ClientConnection>);
  const input = await screen.findByLabelText('Engine URL');
  fireEvent.change(input, { target: { value: `${engineB}/` } });
  fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
  expect(localStorage.getItem('klm.engine-url.v1')).toBe(engineB);
  expect(fixture.reload).toHaveBeenCalledTimes(1);
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(fetch.mock.calls[0][0]).toBe(`${fixture.base}/api/auth/session`);
  expect(fetch.mock.calls.some(([url]) => url.includes('/api/auth/pair'))).toBe(false);
});

it('rejects a different fixed development engine before either pairing or persisting a switch', async () => {
  vi.stubEnv('VITE_ENGINE_URL', fixture.base);
  const fetch = vi.fn().mockRejectedValue(new Error('Engine A unavailable'));
  vi.stubGlobal('fetch', fetch);
  render(<ClientConnection><div>Protected workspace</div></ClientConnection>);
  fireEvent.change(await screen.findByLabelText('Engine URL'), { target: { value: 'https://paired.example.test' } });
  fireEvent.change(screen.getByLabelText('Pairing code'), { target: { value: 'a'.repeat(43) } });
  fireEvent.click(screen.getByRole('button', { name: 'Pair client' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('fixed by VITE_ENGINE_URL');
  fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
  expect(screen.getByRole('alert')).toHaveTextContent('fixed by VITE_ENGINE_URL');
  expect(localStorage.getItem('klm.engine-url.v1')).toBeNull();
  expect(fixture.reload).not.toHaveBeenCalled();
  expect(fetch).toHaveBeenCalledTimes(1);
});
