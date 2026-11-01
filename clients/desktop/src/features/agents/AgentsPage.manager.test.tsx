import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { AgentsPage } from './AgentsPage';
import { useAgentPage } from './useAgentPage';
import { getAgent, getAgentPage, saveAgent } from './api';
import type { AgentPage, CatalogAgent } from './types';
import * as engineIdentity from '../../engineIdentity';

const notices = vi.hoisted(() => [] as Array<() => void>);
vi.mock('./api', () => ({ getAgent: vi.fn(), getAgentPage: vi.fn(), saveAgent: vi.fn(), deleteAgent: vi.fn() }));
vi.mock('../chat/scopedConnection', () => ({ subscribeScope: (_kind: string, _id: string, listener: () => void) => {
  notices.push(listener); return () => { const index = notices.indexOf(listener); if (index >= 0) notices.splice(index, 1); };
} }));
vi.mock('../library/CapabilitySelector', () => ({ CapabilitySelector: () => null }));
vi.mock('./catalog', async () => {
  const { createContext } = await import('react');
  return { AuthoringProject: createContext<string | undefined>(undefined), useAgentModels: () => ({
    models: [{ id: 'fixture/model', name: 'Fixture', provider: 'Fixture', efforts: [] }], loading: false, error: '', retry: () => {},
  }) };
});

const agent: CatalogAgent = {
  id: 'saved-specialist', name: 'Saved specialist', description: '', enabled: true,
  defaultHarness: 'opencode', model: 'fixture/model', effort: '', prompt: 'Saved prompt',
  skills: [], mcpConnections: [], revision: 'same-revision',
  source: { sourceUid: 'fixture-source-identity', id: 'saved-specialist', scope: 'global' },
  status: 'linked', available: true, references: [], referencesHasMore: false,
};
const page: AgentPage = { agents: [{ ...agent, referenceCount: 0 }], diagnostics: [], complete: true, revision: 'catalog', hasMore: false, nextOffset: 1 };
const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');

beforeEach(() => {
  vi.clearAllMocks(); notices.length = 0;
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value: function (this: HTMLDialogElement) { this.setAttribute('open', ''); } });
  vi.mocked(getAgentPage).mockResolvedValue(page);
  vi.mocked(getAgent).mockImplementation(async () => ({ ...agent }));
});
afterEach(() => {
  cleanup(); vi.restoreAllMocks();
  if (originalShowModal) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', originalShowModal);
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
});

it('discards a failed edit when the server revision has not changed', async () => {
  vi.mocked(saveAgent).mockRejectedValue(new Error('Name is already used.'));
  render(<AgentsPage />);
  fireEvent.click(await screen.findByText('Saved specialist'));
  fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Unsaved changed name' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
  await screen.findByText('Name is already used.');
  fireEvent.click(screen.getByRole('button', { name: 'Discard changes and reload' }));
  await waitFor(() => expect(getAgent).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(screen.getByLabelText('Name')).toHaveValue('Saved specialist'));
  expect(screen.queryByText('Name is already used.')).not.toBeInTheDocument();
});

it('coalesces same-page invalidations without aborting every in-flight catalog read', async () => {
  const signals: AbortSignal[] = [];
  const completions: Array<(value: AgentPage) => void> = [];
  vi.mocked(getAgentPage).mockImplementation((_project, _search, _offset, signal) => {
    signals.push(signal!);
    return new Promise(resolve => completions.push(resolve));
  });
  const { result } = renderHook(() => useAgentPage('', '', 0));
  await waitFor(() => expect(signals).toHaveLength(1));
  await act(async () => { notices[0](); });
  await act(async () => { notices[0](); });
  expect(signals[0].aborted).toBe(false);
  await act(async () => { completions[0](page); });
  await waitFor(() => expect(result.current.page?.agents[0].name).toBe('Saved specialist'));
  await waitFor(() => expect(signals).toHaveLength(2));
  expect(signals[1].aborted).toBe(false);
  expect(result.current.loading).toBe(true);
  await act(async () => { completions[1]({ ...page, revision: 'refreshed' }); });
  await waitFor(() => expect(result.current.page?.revision).toBe('refreshed'));
  expect(result.current.loading).toBe(false);
  expect(signals).toHaveLength(2);
});

it('keeps an unsaved editor draft through same-source scoped updates', async () => {
  render(<AgentsPage />);
  fireEvent.click(await screen.findByText('Saved specialist'));
  fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Keep my draft' } });
  const reads = vi.mocked(getAgentPage).mock.calls.length;
  await act(async () => { notices[0](); });
  await waitFor(() => expect(getAgentPage).toHaveBeenCalledTimes(reads + 1));
  expect(screen.getByLabelText('Name')).toHaveValue('Keep my draft');
  expect(getAgent).toHaveBeenCalledTimes(1);
});

it('keeps loaded rows actionable during a pending background refresh', async () => {
  vi.mocked(getAgentPage).mockResolvedValueOnce(page).mockImplementationOnce(() => new Promise(() => {}));
  render(<AgentsPage />);
  await screen.findByText('Saved specialist');
  await act(async () => { notices[0](); notices[0](); });
  await screen.findByText('Refreshing agents...');
  fireEvent.click(screen.getByText('Saved specialist'));
  expect(await screen.findByLabelText('Name')).toHaveValue('Saved specialist');
});

it('ignores a delayed explicit editor reload after leaving its project scope', async () => {
  let finish: (value: CatalogAgent) => void = () => {};
  let signal: AbortSignal | undefined;
  vi.mocked(saveAgent).mockRejectedValue(new Error('Name is already used.'));
  vi.mocked(getAgent).mockResolvedValueOnce({ ...agent }).mockImplementationOnce((_source, _offset, suppliedSignal) => {
    signal = suppliedSignal;
    return new Promise(resolve => { finish = resolve; });
  });
  const view = render(<AgentsPage />);
  fireEvent.click(await screen.findByText('Saved specialist'));
  fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Unsaved changed name' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save changes' }));
  await screen.findByText('Name is already used.');
  fireEvent.click(screen.getByRole('button', { name: 'Discard changes and reload' }));
  await waitFor(() => expect(signal).toBeDefined());
  view.rerender(<AgentsPage projectId="other-project" />);
  expect(signal!.aborted).toBe(true);
  await act(async () => { finish({ ...agent }); });
  expect(screen.queryByLabelText('Name')).not.toBeInTheDocument();
});

it.each(['project', 'search', 'offset', 'engine'] as const)('aborts and ignores old catalog reads when %s changes', async change => {
  let engine = 'first-engine';
  vi.spyOn(engineIdentity, 'currentEngineIdentity').mockImplementation(() => engine);
  const signals: AbortSignal[] = [];
  const completions: Array<(value: AgentPage) => void> = [];
  vi.mocked(getAgentPage).mockImplementation((_project, _search, _offset, signal) => {
    signals.push(signal!);
    return new Promise(resolve => completions.push(resolve));
  });
  const initialProps = { project: '', search: '', offset: 0 };
  const view = renderHook(({ project, search, offset }) => useAgentPage(project, search, offset), { initialProps });
  await waitFor(() => expect(signals).toHaveLength(1));
  await act(async () => { notices[0](); });
  if (change === 'engine') engine = 'second-engine';
  view.rerender({ project: change === 'project' ? 'another-project' : '', search: change === 'search' ? 'specialist' : '', offset: change === 'offset' ? 50 : 0 });
  await waitFor(() => expect(signals).toHaveLength(2));
  expect(signals[0].aborted).toBe(true);
  await act(async () => { completions[0]({ ...page, revision: 'stale' }); });
  expect(view.result.current.page).toBeUndefined();
  expect(signals).toHaveLength(2);
  await act(async () => { completions[1]({ ...page, revision: 'current' }); });
  await waitFor(() => expect(view.result.current.page?.revision).toBe('current'));
  await act(async () => { notices[0](); });
  await waitFor(() => expect(signals).toHaveLength(3));
  view.unmount();
  expect(signals[2].aborted).toBe(true);
  await act(async () => { completions[2]({ ...page, revision: 'unmounted' }); });
  expect(signals).toHaveLength(3);
});
