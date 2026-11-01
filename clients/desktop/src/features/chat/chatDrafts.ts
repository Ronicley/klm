import type { ComposerDraft } from './MessageComposer';
import type { SourceReference } from '../../engine';
import { engineStorageKey } from '../../engineIdentity';
import { pendingSubmissionStorageKey, type LocalSubmission } from './PendingSubmission';
import { draftRecord, flushDrafts, loadDraftContext, readDraftValue, setDraftValue } from '../drafts/draftStore';

export const chatDraftKeys = { composers: 'chat.composers', sources: 'chat.sources', submissions: 'chat.submissions', migration: 'chat.legacyMigration' };
const invalid = () => new Error('Saved chat drafts are invalid. Keep the saved file and restore a valid copy before editing.');

function composer(value: unknown): ComposerDraft {
  if (!draftRecord(value) || typeof value.text !== 'string' || !Array.isArray(value.mentions)) throw invalid();
  const text = value.text;
  const mentions = value.mentions.map(mention => {
    if (!draftRecord(mention) || typeof mention.id !== 'string' || typeof mention.path !== 'string' ||
        !['file', 'directory'].includes(mention.kind as string) || !Number.isInteger(mention.start) || !Number.isInteger(mention.end) ||
        (mention.start as number) < 0 || (mention.end as number) < (mention.start as number) || (mention.end as number) > text.length) throw invalid();
    return { id: mention.id, path: mention.path, kind: mention.kind as 'file' | 'directory', start: mention.start as number, end: mention.end as number };
  });
  return { text, mentions };
}

function sources(value: unknown): SourceReference[] {
  if (!Array.isArray(value)) throw invalid();
  return value.map(source => {
    if (!draftRecord(source) || typeof source.sessionId !== 'string' || typeof source.messageId !== 'string' || typeof source.passage !== 'string') throw invalid();
    return { sessionId: source.sessionId, messageId: source.messageId, passage: source.passage };
  });
}

function dictionary<T>(value: unknown, parse: (item: unknown) => T): Record<string, T> {
  if (!draftRecord(value)) throw invalid();
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, parse(item)]));
}

function submissions(value: unknown): Record<string, LocalSubmission[]> {
  return dictionary(value, list => {
    if (!Array.isArray(list)) throw invalid();
    const ids = new Set<string>();
    return list.map((item): LocalSubmission => {
      if (!draftRecord(item) || typeof item.id !== 'string' || !item.id || ids.has(item.id) ||
          !['queue', 'steer', 'interrupt'].includes(item.mode as string) || !['sending', 'accepted', 'uncertain', 'failed'].includes(item.status as string) ||
          item.composerKey !== undefined && typeof item.composerKey !== 'string') throw invalid();
      ids.add(item.id);
      return { id: item.id, draft: composer(item.draft), sources: sources(item.sources), mode: item.mode as LocalSubmission['mode'],
        status: item.status === 'failed' ? 'failed' : 'uncertain', ...(item.composerKey !== undefined ? { composerKey: item.composerKey as string } : {}) };
    });
  });
}

function prepare(entries: Record<string, unknown>) {
  const restored = { ...entries };
  if (Object.hasOwn(entries, chatDraftKeys.composers)) restored[chatDraftKeys.composers] = dictionary(entries[chatDraftKeys.composers], composer);
  if (Object.hasOwn(entries, chatDraftKeys.sources)) restored[chatDraftKeys.sources] = dictionary(entries[chatDraftKeys.sources], sources);
  if (Object.hasOwn(entries, chatDraftKeys.submissions)) restored[chatDraftKeys.submissions] = submissions(entries[chatDraftKeys.submissions]);
  if (Object.hasOwn(entries, chatDraftKeys.migration)) {
    const marker = entries[chatDraftKeys.migration];
    if (!draftRecord(marker) || typeof marker.unscoped !== 'boolean') throw invalid();
  }
  return restored;
}

// Only legacy desktop delivery receipts are imported; new native drafts never
// enter web storage. Confirm the native write before deleting either old key.
export async function restoreChatDraftContext(databaseId: string, allowUnscoped: boolean) {
  await loadDraftContext(databaseId, prepare);
  const scoped = engineStorageKey(pendingSubmissionStorageKey);
  let marker = readDraftValue<{ unscoped: boolean } | undefined>(chatDraftKeys.migration, undefined);
  try {
    if (!marker) {
      const keys = [...new Set([scoped, ...(allowUnscoped ? [pendingSubmissionStorageKey] : [])])];
      const existing = readDraftValue<Record<string, LocalSubmission[]>>(chatDraftKeys.submissions, {});
      const merged = { ...existing };
      for (const key of keys) {
        const raw = sessionStorage.getItem(key);
        if (raw === null) continue;
        let parsed: unknown;
        try { parsed = JSON.parse(raw); } catch { throw invalid(); }
        for (const [id, items] of Object.entries(submissions(parsed))) {
          const current = merged[id] ?? [];
          for (const item of items) {
            const duplicate = current.find(old => old.id === item.id);
            if (duplicate && JSON.stringify([duplicate.draft, duplicate.sources, duplicate.mode]) !== JSON.stringify([item.draft, item.sources, item.mode])) throw invalid();
          }
          merged[id] = [...current, ...items.filter(item => !current.some(old => old.id === item.id))];
        }
      }
      if (Object.keys(merged).length) setDraftValue(chatDraftKeys.submissions, merged, {});
      marker = { unscoped: allowUnscoped };
      setDraftValue(chatDraftKeys.migration, marker, undefined);
    }
    // Also retries an earlier failed migration write before touching legacy data.
    await flushDrafts();
    sessionStorage.removeItem(scoped);
    if (marker.unscoped) sessionStorage.removeItem(pendingSubmissionStorageKey);
  } catch (error) {
    if (error instanceof Error && error.message === invalid().message) throw error;
    throw new Error('Could not preserve existing delivery records. Keep this window open and retry.');
  }
}
