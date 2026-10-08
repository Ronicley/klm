import { StrictMode } from 'react';
import { act, cleanup, fireEvent, render, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { GraphRequestProjection, SessionResponse } from '../../engine';
import { request } from '../../engine';
import { GraphRequestQueue } from './GraphRequestQueue';

vi.mock('../../engine', () => ({ request: vi.fn() }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });

it('moves one pending question between surfaces, preserving draft and routing to its native owner', async () => {
  const chat = document.createElement('div');
  const panel = document.createElement('div');
  document.body.append(chat, panel);
  const pending: GraphRequestProjection = {
    runId: 'run', graphId: 'graph', nodeId: 'B', nodeName: 'Builder B', activationId: 'activation-B',
    sessionId: 'native-B', requestId: 'original-question', kind: 'question',
    question: { id: 'projected-id', harness: 'pi', createdAt: '', items: [
      { id: 'item', header: 'Target', text: 'Where?', options: [], multiple: false, custom: true, secret: false },
    ] },
  };
  let finish!: (result: SessionResponse) => void;
  vi.mocked(request).mockReturnValue(new Promise(resolve => { finish = resolve; }));
  const resolved = vi.fn();
  const view = (target: HTMLDivElement | null, requests = [pending]) => <StrictMode><GraphRequestQueue requests={requests} graphs={[]} projectName="Project" target={target} onResolved={resolved} /></StrictMode>;
  const mounted = render(view(chat));
  const answer = within(chat).getByRole('textbox');
  fireEvent.change(answer, { target: { value: 'Keep this answer' } });
  mounted.rerender(view(null));
  expect(chat.childElementCount).toBe(0);
  mounted.rerender(view(panel));
  expect(within(panel).getByRole('textbox')).toBe(answer);
  expect(answer).toHaveValue('Keep this answer');
  expect(within(panel).getByText('graph · Builder B')).toHaveTextContent('graph · Builder B');
  fireEvent.click(within(panel).getByRole('button', { name: 'Send answer' }));
  expect(request).toHaveBeenCalledWith('/api/sessions/native-B/questions/original-question/reply', 'POST', { answers: [['Keep this answer']] });
  mounted.rerender(view(chat));
  expect(within(chat).getByRole('button', { name: 'Sending...' })).toBeDisabled();
  await act(async () => { finish({} as SessionResponse); });
  expect(resolved).toHaveBeenCalledTimes(1);
  mounted.rerender(view(chat, []));
  expect(within(chat).queryByRole('textbox')).toBeNull();
  mounted.unmount();
  expect(chat.childElementCount).toBe(0);
  chat.remove(); panel.remove();
});
