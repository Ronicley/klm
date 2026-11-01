import type { ComposerDraft } from './MessageComposer';
import type { LocalSubmission } from './PendingSubmission';
import type { SourceReference } from '../../engine';

export type ChatDraftState = {
  drafts: Record<string, ComposerDraft>;
  sources: Record<string, SourceReference[]>;
  submissions: Record<string, LocalSubmission[]>;
};
export type DeliveryConfirmation = { sessionId: string; parentSessionId?: string; ids: string[] };

export function sameComposerDraft(left: ComposerDraft | undefined, right: ComposerDraft) {
  return !!left && left.text === right.text && left.mentions.length === right.mentions.length && left.mentions.every((mention, index) => {
    const other = right.mentions[index];
    return mention.id === other.id && mention.path === other.path && mention.kind === other.kind && mention.start === other.start && mention.end === other.end;
  });
}

// Pure shared acceptance for HTTP, queue and history. A live local receipt is
// required: repeating an ACK after it was removed must not clear a later draft.
export function acceptChatDeliveries(current: ChatDraftState, confirmations: DeliveryConfirmation[]): ChatDraftState {
  let next = current;
  for (const confirmation of confirmations) {
    const pending = next.submissions[confirmation.sessionId];
    if (!pending?.length) continue;
    const ids = new Set(confirmation.ids);
    const accepted = pending.filter(message => ids.has(message.id));
    if (!accepted.length) continue;
    let drafts = next.drafts;
    let sources = next.sources;
    for (const message of accepted) {
      const key = message.composerKey ?? (confirmation.parentSessionId ? `side:${confirmation.parentSessionId}` : confirmation.sessionId);
      if (sameComposerDraft(drafts[key], message.draft)) {
        drafts = { ...drafts }; delete drafts[key];
      }
      if (key.startsWith('side:')) {
        const mainId = key.slice(5);
        const previous = sources[mainId];
        if (previous) {
          const sentSources = [...message.sources];
          const remaining = previous.filter(source => {
            const index = sentSources.findIndex(sent => sent.sessionId === source.sessionId && sent.messageId === source.messageId && sent.passage === source.passage);
            if (index < 0) return true;
            sentSources.splice(index, 1);
            return false;
          });
          if (remaining.length !== previous.length) {
            sources = { ...sources };
            if (remaining.length) sources[mainId] = remaining; else delete sources[mainId];
          }
        }
      }
    }
    // Compute composer/source cleanup first, then remove the corresponding IDs.
    // The caller publishes all three maps as one coherent store update.
    const submissions = { ...next.submissions };
    const remaining = pending.filter(message => !ids.has(message.id));
    if (remaining.length) submissions[confirmation.sessionId] = remaining; else delete submissions[confirmation.sessionId];
    next = { drafts, sources, submissions };
  }
  return next;
}
