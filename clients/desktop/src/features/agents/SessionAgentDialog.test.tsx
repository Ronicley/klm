import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { SessionAgentDialog } from './SessionAgentDialog';
import { useSessionAgent, patchSessionAgent } from './binding';
import { agentDetail, boundSession, modelCatalog } from './sessionAgentFixtures';

vi.mock('./binding', () => ({ useSessionAgent: vi.fn(), patchSessionAgent: vi.fn() }));
vi.mock('../chat/modelCatalog', () => ({ useModelCatalog: () => ({ catalog: modelCatalog, loading: false, error: '', refresh: vi.fn() }) }));
vi.mock('../library/SessionCapabilityPicker', () => ({ SessionCapabilityPicker: () => null }));
const showModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value: function(this: HTMLDialogElement) { this.setAttribute('open', ''); } });
  vi.mocked(useSessionAgent).mockReturnValue({ agent: agentDetail, loading: false, refresh: vi.fn() });
  vi.mocked(patchSessionAgent).mockResolvedValue(agentDetail);
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); if (showModal) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', showModal); else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal'); });

it('saves an effort-only edit without pinning the inherited model', async () => {
  render(<SessionAgentDialog session={boundSession} onClose={vi.fn()} />);
  await screen.findByLabelText('Effort: high');
  fireEvent.click(screen.getByLabelText('Effort: high'));
  fireEvent.click(screen.getByRole('button', { name: 'low' }));
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
  await waitFor(() => expect(patchSessionAgent).toHaveBeenCalledWith(boundSession, '1', { effort: 'low' }));
});
it('requires an explicit compatible effort after changing model instead of silently replacing high', async () => {
  render(<SessionAgentDialog session={boundSession} onClose={vi.fn()} />);
  fireEvent.click(await screen.findByLabelText('Model: Model One'));
  fireEvent.click(screen.getByRole('button', { name: /Model Two/ }));
  expect(screen.getByLabelText('Effort: high')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
  fireEvent.click(screen.getByLabelText('Effort: high'));
  fireEvent.click(screen.getByRole('button', { name: 'low' }));
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
  await waitFor(() => expect(patchSessionAgent).toHaveBeenCalledWith(boundSession, '1', { model: 'provider/two', effort: 'low' }));
});
it('clears only skills and renders the nested applied capture without truncating prompt', async () => {
  render(<SessionAgentDialog session={boundSession} onClose={vi.fn()} />);
  expect(await screen.findByText('Applied specialist prompt')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Clear skills' }));
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
  await waitFor(() => expect(patchSessionAgent).toHaveBeenCalledWith(boundSession, '1', { skills: [] }));
});
it('resets only Library families in Library mode, preserving model and prompt overrides', async () => {
  vi.mocked(useSessionAgent).mockReturnValue({ agent: { ...agentDetail, overriddenFields: ['model', 'prompt', 'skills', 'mcpConnections'] }, loading: false, refresh: vi.fn() });
  render(<SessionAgentDialog session={boundSession} libraryOnly onClose={vi.fn()} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Reset overrides' }));
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
  await waitFor(() => expect(patchSessionAgent).toHaveBeenCalledWith(boundSession, '1', { skills: null, mcpConnections: null }));
});
it('retains a dirty prompt through source invalidation until explicit discard reload', async () => {
  const view = render(<SessionAgentDialog session={boundSession} onClose={vi.fn()} />);
  fireEvent.change(await screen.findByLabelText(/^Prompt ·/), { target: { value: 'Unsaved complete prompt' } });
  const changed = { ...agentDetail, execution: { allowed: false, token: 'changed', message: 'Choose a compatible model.' }, desired: { ...agentDetail.desired, prompt: 'New source prompt' } };
  vi.mocked(useSessionAgent).mockReturnValue({ agent: changed, loading: false, refresh: vi.fn() });
  view.rerender(<SessionAgentDialog session={boundSession} onClose={vi.fn()} />);
  expect(screen.getByLabelText(/^Prompt ·/)).toHaveValue('Unsaved complete prompt');
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Reload agent settings' }));
  fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }));
  expect(screen.getByLabelText(/^Prompt ·/)).toHaveValue('New source prompt');
});
