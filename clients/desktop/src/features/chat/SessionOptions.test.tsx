import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { request, type Session } from '../../engine';
import { SessionOptions } from './SessionOptions';

vi.mock('../../engine', () => ({ request: vi.fn(), ENGINE_URL: 'http://test' }));
beforeEach(() => vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} }));
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.unstubAllGlobals(); });

const session: Session = { id: 's', projectId: 'p', title: 'Session', workspace: 'Ungrouped', harness: 'pi', status: 'idle', createdAt: '', updatedAt: '', events: [] };

it('shows YOLO immediately and rolls back only after the pending request fails', async () => {
  let reject!: (error: Error) => void;
  vi.mocked(request).mockReturnValue(new Promise((_, fail) => { reject = fail; }));
  const first = render(<SessionOptions session={session} disabled={false} onSnapshot={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Session options' }));
  fireEvent.click(screen.getByRole('switch', { name: 'YOLO mode' }));
  expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'true');
  expect(screen.getByRole('switch')).toBeDisabled();
  first.unmount();
  render(<SessionOptions session={session} disabled={false} onSnapshot={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Session options' }));
  expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'true');
  expect(screen.getByRole('switch')).toBeDisabled();
  await act(async () => { reject(new Error('Selection rejected')); });
  expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'false');
  expect(screen.getByRole('alert')).toHaveTextContent('Selection rejected');
});

it('keeps execution-time restrictions while still allowing the local menu to open', () => {
  render(<SessionOptions session={{ ...session, status: 'running' }} disabled={false} onSnapshot={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Session options' }));
  expect(screen.getByRole('switch', { name: 'YOLO mode' })).toBeDisabled();
  expect(request).not.toHaveBeenCalled();
});
