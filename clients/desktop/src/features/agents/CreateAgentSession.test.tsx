import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { CreateSessionDialog } from '../workspace/CreateSessionDialog';
import { sourceAgent, modelCatalog } from './sessionAgentFixtures';

vi.mock('./AgentPicker', async () => {
  const { sourceAgent } = await import('./sessionAgentFixtures');
  return { AgentPicker: ({ onChange, onBusyChange }: { onChange: (value: unknown) => void; onBusyChange?: (busy: boolean) => void }) => <><button type="button" onClick={() => onChange(sourceAgent)}>Select specialist</button><button type="button" onClick={() => onChange(null)}>Default agent</button><button type="button" onClick={() => onBusyChange?.(true)}>Start source lookup</button><button type="button" onClick={() => { onChange(sourceAgent); onBusyChange?.(false); }}>Finish source lookup</button></> };
});
vi.mock('../chat/modelCatalog', () => ({ useModelCatalog: () => ({ catalog: modelCatalog, loading: false, error: '', refresh: vi.fn() }) }));
vi.mock('../library/SessionCapabilityPicker', () => ({ SessionCapabilityPicker: () => null }));

const showModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
beforeEach(() => { Object.defineProperty(HTMLDialogElement.prototype, 'showModal', { configurable: true, value: function(this: HTMLDialogElement) { this.setAttribute('open', ''); } }); });
afterEach(() => { cleanup(); if (showModal) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', showModal); else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal'); });
function setup() {
  const create = vi.fn().mockResolvedValue(null);
  render(<CreateSessionDialog project={{ id: 'project-one', name: 'Project', folder: 'C:/fixture', icon: '', folders: [], archivedFolders: [] }} topicId="topic-one"
    harnesses={[{ id: 'pi', name: 'Pi', available: true }, { id: 'opencode', name: 'OpenCode', available: true }, { id: 'codex', name: 'Codex', available: true }]} onCreate={create} onClose={vi.fn()} />);
  fireEvent.change(screen.getByLabelText('Session name'), { target: { value: 'Keep title' } });
  return create;
}
it('sends source identity without materializing displayed agent defaults', async () => {
  const create = setup();
  fireEvent.click(screen.getByText('Select specialist'));
  fireEvent.click(screen.getByRole('button', { name: 'Create session' }));
  await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
  expect(create.mock.calls[0][0]).toEqual({ projectId: 'project-one', title: 'Keep title', topicId: 'topic-one', agent: sourceAgent.id, agentSourceUid: sourceAgent.source.sourceUid, agentRevision: sourceAgent.revision });
});
it('keeps explicit creation settings nested once and harness creation-only', async () => {
  const create = setup();
  fireEvent.click(screen.getByText('Select specialist'));
  fireEvent.click(screen.getByLabelText('Codex'));
  fireEvent.change(screen.getByLabelText(/^Prompt ·/), { target: { value: 'Explicit full prompt\nwith second line' } });
  fireEvent.click(screen.getByRole('button', { name: 'Create session' }));
  await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
  expect(create.mock.calls[0][0]).toEqual({ projectId: 'project-one', title: 'Keep title', topicId: 'topic-one', agent: sourceAgent.id, agentSourceUid: sourceAgent.source.sourceUid, agentRevision: sourceAgent.revision, harness: 'codex', agentOverrides: { prompt: 'Explicit full prompt\nwith second line' } });
});
it('returns to Default without sending agent-only fields or losing title/topic', async () => {
  const create = setup();
  fireEvent.click(screen.getByText('Select specialist'));
  fireEvent.change(screen.getByLabelText(/^Prompt ·/), { target: { value: 'Agent-only edit' } });
  fireEvent.click(screen.getByText('Default agent'));
  fireEvent.click(screen.getByRole('button', { name: 'Create session' }));
  await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
  expect(create.mock.calls[0][0]).toEqual({ projectId: 'project-one', title: 'Keep title', topicId: 'topic-one', harness: 'pi' });
});
it('does not create using previous defaults while the exact source is still loading', async () => {
  const create = setup();
  fireEvent.click(screen.getByText('Start source lookup'));
  expect(screen.getByRole('button', { name: 'Create session' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Create session' }));
  expect(create).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText('Finish source lookup'));
  fireEvent.click(screen.getByRole('button', { name: 'Create session' }));
  await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
  expect(create.mock.calls[0][0].agent).toBe(sourceAgent.id);
});
