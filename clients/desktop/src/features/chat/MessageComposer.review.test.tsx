import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { MessageComposer, type ComposerDraft } from './MessageComposer';
import { GraphPicker } from './GraphPicker';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('shows agent/graph controls and sends an image-only draft with a removable preview', () => {
  const create = vi.fn(() => 'blob:preview');
  const revoke = vi.fn();
  vi.stubGlobal('URL', class extends URL { static createObjectURL = create; static revokeObjectURL = revoke; });
  const onSend = vi.fn((_draft: ComposerDraft) => true);
  function Fixture() {
    const [draft, setDraft] = useState<ComposerDraft>({ text: '', mentions: [] });
    return <MessageComposer draft={draft} onDraftChange={setDraft} onSend={onSend} graphControl={<><GraphPicker kind="agent" graphs={[]} value="" onChange={() => {}} /><GraphPicker graphs={[]} value="" onChange={() => {}} /></>} />;
  }
  const { container } = render(<Fixture />);
  expect(screen.getByRole('button', { name: 'Choose agent: None' })).toBeVisible();
  expect(screen.getByRole('button', { name: 'Choose graph: None' })).toBeVisible();
  expect(screen.getByRole('button', { name: 'Attach images' })).toBeEnabled();
  const image = new File(['image bytes'], 'shot.png', { type: 'image/png' });
  fireEvent.change(container.querySelector('input[accept]')!, { target: { files: [image] } });
  expect(screen.getByAltText('shot.png')).toHaveAttribute('src', 'blob:preview');
  expect(screen.getByRole('button', { name: 'Remove attachment shot.png' })).toBeEnabled();
  fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
  expect(onSend.mock.calls[0][0].files).toEqual([image]);
  expect(revoke).toHaveBeenCalledWith('blob:preview');
});

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

it('keeps previously uploaded images recoverable and blocks missing legacy bytes', async () => {
  vi.stubGlobal('URL', class extends URL { static createObjectURL = vi.fn(() => 'blob:legacy-preview'); static revokeObjectURL = vi.fn(); });
  const fetchImage = vi.fn(async (_url: string) => ({ ok: true, blob: async () => new Blob(['image']) }));
  vi.stubGlobal('fetch', fetchImage);
  const uploaded = { id: '0123456789abcdef0123456789abcdef', name: 'old.png', mime: 'image/png', size: 42, width: 2, height: 2 };
  const draft: ComposerDraft = { text: '', mentions: [], images: [{ id: 'draft-image', name: uploaded.name, size: uploaded.size, uploaded }] };
  const onSend = vi.fn(() => false);
  const { rerender } = render(<MessageComposer draft={draft} onDraftChange={() => {}} onSend={onSend} />);
  expect(await screen.findByAltText('old.png')).toHaveAttribute('src', 'blob:legacy-preview');
  expect(fetchImage.mock.calls[0][0]).toContain(`/api/images/${uploaded.id}`);
  fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
  expect(onSend).toHaveBeenCalledWith(draft, 'queue');
  rerender(<MessageComposer draft={{ ...draft, images: [{ id: 'draft-image', name: uploaded.name, size: uploaded.size }] }} onDraftChange={() => {}} onSend={onSend} />);
  expect(screen.getByRole('button', { name: 'Send message' })).toBeDisabled();
  expect(screen.getByText('Reselect file')).toBeVisible();
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
