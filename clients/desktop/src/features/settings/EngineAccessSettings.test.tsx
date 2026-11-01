import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { EngineAccessSettings } from './EngineAccessSettings';

const fixture = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock('../../engine', () => ({ request: fixture.request }));
const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
const current = { allowedOrigins: ['https://app.example.com'], trustedTlsProxies: ['192.168.1.10/32'], restartRequired: false, restarting: false };

beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true, value(this: HTMLDialogElement) { this.setAttribute('open', ''); },
  });
  fixture.request.mockResolvedValue(current);
});
afterEach(() => {
  vi.resetAllMocks();
  if (originalShowModal) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', originalShowModal);
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
});

it('saves origin and proxy lists and requires restart without restarting on save', async () => {
  fixture.request.mockImplementation(async (_path, method, body) => method === 'PATCH' ? { ...body, restartRequired: true, restarting: false } : current);
  render(<EngineAccessSettings />);
  await waitFor(() => expect(screen.getByLabelText('Allowed frontend origins')).toHaveValue('https://app.example.com'));
  fireEvent.change(screen.getByLabelText('Allowed frontend origins'), { target: { value: 'https://new.example.com\nhttps://another.example.com' } });
  fireEvent.change(screen.getByLabelText('Trusted TLS proxies (CIDR)'), { target: { value: '192.168.10.200/32' } });
  expect(screen.getByRole('button', { name: 'Restart engine' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(await screen.findByText('Restart required')).toBeInTheDocument();
  expect(fixture.request).toHaveBeenCalledWith('/api/settings/access', 'PATCH', {
    allowedOrigins: ['https://new.example.com', 'https://another.example.com'], trustedTlsProxies: ['192.168.10.200/32'],
  });
  expect(fixture.request.mock.calls.some(([path]) => path === '/api/engine/restart')).toBe(false);
  expect(screen.getByRole('button', { name: 'Restart engine' })).toBeEnabled();
});

it('requires interruption confirmation and leaves Cancel without a restart request', async () => {
  render(<EngineAccessSettings />);
  const restart = screen.getByRole('button', { name: 'Restart engine' });
  await waitFor(() => expect(restart).toBeEnabled());
  fireEvent.click(restart);
  let dialog = screen.getByRole('dialog', { name: 'Restart engine?' });
  expect(within(dialog).getByText(/interrupt all agents and graph runs in progress/)).toBeInTheDocument();
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(fixture.request.mock.calls.some(([path]) => path === '/api/engine/restart')).toBe(false);
  fireEvent.click(restart);
  dialog = screen.getByRole('dialog', { name: 'Restart engine?' });
  fireEvent.click(within(dialog).getByRole('button', { name: 'Restart engine' }));
  await waitFor(() => expect(fixture.request).toHaveBeenCalledWith('/api/engine/restart', 'POST', { confirmInterrupt: true }));
  expect(await screen.findByText('Restarting engine...')).toBeInTheDocument();
});
