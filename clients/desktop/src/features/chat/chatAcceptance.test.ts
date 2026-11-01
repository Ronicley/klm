import { expect, it } from 'vitest';
import { acceptChatDeliveries, type ChatDraftState } from './chatAcceptance';
import type { LocalSubmission } from './PendingSubmission';

const sent = { text: 'Review @src/a.ts', mentions: [{ id: 'mention', path: 'src/a.ts', kind: 'file' as const, start: 7, end: 16 }] };
const source = { sessionId: 'main', messageId: 'event-1', passage: 'Sent passage' };
const newSource = { sessionId: 'main', messageId: 'event-2', passage: 'New passage' };
const receipt: LocalSubmission = { id: 'delivery-1', draft: sent, sources: [], mode: 'steer', status: 'uncertain' };
const main = (): ChatDraftState => ({ drafts: { main: { text: sent.text, mentions: sent.mentions.map(({ id, path, kind, start, end }) => ({ path, kind, start, end, id })) } }, sources: {}, submissions: { main: [structuredClone(receipt)] } });

it('cleans a reloaded uncertain composer and receipt when history confirms its ID, without an HTTP ACK', () => {
  const restored = main();
  const next = acceptChatDeliveries(restored, [{ sessionId: 'main', ids: ['delivery-1'] }]);
  expect(next.drafts).toEqual({});
  expect(next.submissions).toEqual({});
  expect(restored.drafts.main).toEqual(sent);
  expect(restored.submissions.main).toHaveLength(1); // Input is not mutated.
});

it('preserves newer composer edits when a history ACK removes the original receipt', () => {
  const current = main();
  const edited = { text: 'A new message', mentions: [] };
  current.drafts.main = edited;
  const next = acceptChatDeliveries(current, [{ sessionId: 'main', ids: ['delivery-1'] }]);
  expect(next.drafts.main).toBe(edited);
  expect(next.submissions).toEqual({});
});

it('handles queue ACK for a legacy side receipt and removes only the sources actually sent', () => {
  const current: ChatDraftState = { drafts: { 'side:main': structuredClone(sent) }, sources: { main: [structuredClone(source), newSource, structuredClone(source)] },
    submissions: { side: [{ ...receipt, sources: [source] }] } };
  const next = acceptChatDeliveries(current, [{ sessionId: 'side', parentSessionId: 'main', ids: ['delivery-1'] }]);
  expect(next.drafts).toEqual({});
  expect(next.sources.main).toEqual([newSource, source]); // A newly-added copy of the same passage survives too.
  expect(next.submissions).toEqual({});
  expect(acceptChatDeliveries(next, [{ sessionId: 'side', parentSessionId: 'main', ids: ['delivery-1'] }])).toBe(next);
});

it('uses the original composer key for HTTP acceptance, preserving new side text and passages', () => {
  const edited = { text: 'New side question', mentions: [] };
  const current: ChatDraftState = { drafts: { 'side:main': edited }, sources: { main: [source, newSource] },
    submissions: { side: [{ ...receipt, composerKey: 'side:main', sources: [source], status: 'sending' }] } };
  const next = acceptChatDeliveries(current, [{ sessionId: 'side', ids: ['delivery-1'] }]);
  expect(next.drafts['side:main']).toBe(edited);
  expect(next.sources.main).toEqual([newSource]);
  expect(next.submissions).toEqual({});
});

it('makes repeated HTTP/queue/history confirmations no-ops after receipt removal, even for a later identical draft', () => {
  const confirmed = acceptChatDeliveries(main(), [{ sessionId: 'main', ids: ['delivery-1', 'delivery-1'] }, { sessionId: 'main', ids: ['delivery-1'] }]);
  const later = { ...confirmed, drafts: { main: structuredClone(sent) } };
  expect(acceptChatDeliveries(later, [{ sessionId: 'main', ids: ['delivery-1'] }])).toBe(later);
  expect(later.drafts.main).toEqual(sent);
});

it('does not touch unrelated delivery receipts or unmatched drafts', () => {
  const current = main();
  current.submissions.main.push({ ...receipt, id: 'delivery-2' });
  expect(acceptChatDeliveries(current, [{ sessionId: 'main', ids: ['other-delivery'] }])).toBe(current);
  const next = acceptChatDeliveries(current, [{ sessionId: 'main', ids: ['delivery-1'] }]);
  expect(next.submissions.main.map(item => item.id)).toEqual(['delivery-2']);
});
