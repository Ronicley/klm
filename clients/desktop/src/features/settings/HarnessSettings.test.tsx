// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { clientFetch } from '../../clientAuth';
import { ENGINE_URL } from '../../engine';
import { engineStorageKey } from '../../engineIdentity';
import { HarnessSettings } from './HarnessSettings';

vi.mock('../../clientAuth', () => ({ clientFetch: vi.fn() }));
beforeEach(() => { localStorage.clear(); vi.clearAllMocks(); });
afterEach(() => cleanup());

it.each([false, true])('renders all pristine rows with zero receipts (pending=%s)', async pending => {
  const harnesses = [{ id: 'pi', name: 'Pi' }, { id: 'opencode', name: 'OpenCode' }, { id: 'codex', name: 'Codex' }];
  // Simulate the old empty-table payload too: the API client must normalize it.
  vi.mocked(clientFetch).mockResolvedValue(new Response(JSON.stringify({
    harnesses: harnesses.map(h => ({ ...h, available: false })), jobs: null, activeJobId: '', host: 'fixture', platform: 'windows/amd64',
    plans: harnesses.map(h => ({ harnessId: h.id, token: h.id, version: 'fixture', destination: 'fixture', noop: false, replace: false, prerequisites: { bootstrap: false, version: 'v22.22.0', npmVersion: '10.9.4' } })),
  }), { status: 200 }));
  if (pending) localStorage.setItem(engineStorageKey(`klm.harness-install.v1:${ENGINE_URL}`), JSON.stringify({ id: 'pi', request: { operationId: 'pristine-uncertain-operation' } }));
  render(<HarnessSettings />);
  for (const harness of harnesses) expect(await screen.findByRole('heading', { name: harness.name })).toBeTruthy();
  expect(screen.getAllByRole('button', { name: /^Install$/ })).toHaveLength(3);
  if (pending) expect(screen.getByRole('button', { name: 'Check receipt' })).toBeTruthy();
});
