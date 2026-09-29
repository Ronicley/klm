import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { MessageComposer, type ComposerDraft } from './MessageComposer';

afterEach(cleanup);

it('can send the exact draft object restored after a rejected submission', () => {
  const original: ComposerDraft = { text: 'Please investigate this failure', mentions: [] };
  const onSend = vi.fn(() => true);
  function Fixture() {
    const [draft, setDraft] = useState(original);
    return <>
      <MessageComposer draft={draft} onDraftChange={setDraft} onSend={onSend} />
      <button onClick={() => setDraft(original)}>Restore rejected draft</button>
    </>;
  }
  render(<Fixture />);
  fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
  expect(onSend).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole('button', { name: 'Restore rejected draft' }));
  expect(screen.getByRole('button', { name: 'Send message' })).not.toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
  expect(onSend).toHaveBeenCalledTimes(2);
});

it('retains mentions across an intentional restore without duplicate send gestures', () => {
  const original: ComposerDraft = { text: 'Look at @file', mentions: [{ id: 'mention', path: 'src/file.ts', kind: 'file', start: 8, end: 13 }] };
  const onSend = vi.fn(() => true);
  function Fixture() {
    const [draft, setDraft] = useState(original);
    return <><MessageComposer draft={draft} onDraftChange={setDraft} onSend={onSend} />
      <button onClick={() => setDraft(original)}>Restore with mention</button></>;
  }
  render(<Fixture />);
  const send = screen.getByRole('button', { name: 'Send message' });
  fireEvent.click(send);
  fireEvent.click(send);
  expect(onSend).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole('button', { name: 'Restore with mention' }));
  fireEvent.click(send);
  expect(onSend).toHaveBeenCalledTimes(2);
  expect(onSend).toHaveBeenLastCalledWith(original, 'queue');
});
