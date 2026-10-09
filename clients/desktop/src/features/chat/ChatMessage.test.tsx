import { fireEvent, render, screen, cleanup } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { MarkdownContent } from './ChatMessage';

const { invoke, platform } = vi.hoisted(() => ({ invoke: vi.fn(), platform: { IS_DESKTOP: true, resolveEngineURL: () => 'http://localhost:7331' } }));
vi.mock('@tauri-apps/api/core', () => ({ invoke }));
vi.mock('../../platform', () => platform);
afterEach(() => { cleanup(); invoke.mockReset(); platform.IS_DESKTOP = true; });

it('opens PR links through the desktop browser command', () => {
  invoke.mockResolvedValue(undefined);
  render(<MarkdownContent text="[PR #260](https://github.com/example/repo/pull/260)" />);
  fireEvent.click(screen.getByRole('link', { name: 'PR #260' }));
  expect(invoke).toHaveBeenCalledWith('open_external_url', { url: 'https://github.com/example/repo/pull/260' });
});

it('reports native opener failures', async () => {
  invoke.mockRejectedValue(new Error('Unavailable'));
  render(<MarkdownContent text="[PR #260](https://github.com/example/repo/pull/260)" />);
  fireEvent.click(screen.getByRole('link'));
  expect((await screen.findByRole('alert')).textContent).toContain('Could not open link');
});

it('retains browser navigation and local anchors without native IPC', () => {
  platform.IS_DESKTOP = false;
  render(<MarkdownContent text="[PR #260](https://github.com/example/repo/pull/260) [Section](#section)" />);
  const link = screen.getByRole('link', { name: 'PR #260' });
  expect(link.getAttribute('target')).toBe('_blank');
  link.addEventListener('click', event => event.preventDefault());
  fireEvent.click(link);
  platform.IS_DESKTOP = true;
  fireEvent.click(screen.getByRole('link', { name: 'Section' }));
  expect(invoke).not.toHaveBeenCalled();
});
