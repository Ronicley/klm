import { describe, expect, it } from 'vitest';
import { sameComposerDraft } from './chatAcceptance';

describe('attachment draft acceptance', () => {
  it('does not clear a draft when its attachments changed', () => {
    const file = new File(['one'], 'notes.txt');
    const draft = { text: 'Read this', mentions: [], files: [file] };
    expect(sameComposerDraft(draft, { ...draft })).toBe(true);
    expect(sameComposerDraft({ ...draft, files: [new File(['two'], 'notes.txt')] }, draft)).toBe(false);
    expect(sameComposerDraft({ ...draft, files: [] }, draft)).toBe(false);
  });
});
