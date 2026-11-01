import { Button } from '../../design-system/Button';
import { ENGINE_URL, type SourceReference } from '../../engine';
import type { ComposerDraft, SendMode } from './MessageComposer';
import { engineStorageKey } from '../../engineIdentity';
import { IS_DESKTOP } from '../../platform';

export type LocalSubmission = { id: string; draft: ComposerDraft; sources: SourceReference[]; mode: SendMode; status: 'sending' | 'accepted' | 'uncertain' | 'failed'; composerKey?: string };

export const pendingSubmissionStorageKey = `klm.pending-submissions.v1:${ENGINE_URL}`;
export function readPendingSubmissions(): Record<string, LocalSubmission[]> {
  if (IS_DESKTOP) throw new Error('Desktop delivery records require protected draft storage.');
  try {
    const saved: unknown = JSON.parse(sessionStorage.getItem(engineStorageKey(pendingSubmissionStorageKey)) ?? '{}');
    if (!saved || typeof saved !== 'object' || Array.isArray(saved)) return {};
    const result: Record<string, LocalSubmission[]> = {};
    for (const [session, items] of Object.entries(saved)) {
      if (!Array.isArray(items)) continue;
      result[session] = items.filter((item): item is LocalSubmission => item && typeof item.id === 'string' &&
        typeof item.draft?.text === 'string' && Array.isArray(item.draft.mentions) && Array.isArray(item.sources) &&
        (item.mode === 'queue' || item.mode === 'steer' || item.mode === 'interrupt')).map(item => ({ ...item, status: item.status === 'failed' ? 'failed' : 'uncertain' }));
    }
    return result;
  } catch { return {}; }
}

export function savePendingSubmissions(items: Record<string, LocalSubmission[]>) {
  if (IS_DESKTOP) throw new Error('Desktop delivery records require protected draft storage.');
  try { sessionStorage.setItem(engineStorageKey(pendingSubmissionStorageKey), JSON.stringify(items)); } catch { /* Still recoverable in memory if browser storage is full. */ }
}

export function PendingSubmission({ items, onRestore, onRetry }: { items: LocalSubmission[]; onRestore: (item: LocalSubmission) => void; onRetry: (item: LocalSubmission) => void }) {
  return <>{items.map(item => <div key={item.id} className="storage-error" role="status">
    <span>{item.status === 'sending' ? (item.mode === 'interrupt' ? 'Interrupting and sending…' : 'Sending message…') : item.status === 'accepted' ? 'Message accepted.' : item.status === 'failed' ? 'Message rejected.' : 'Delivery unconfirmed.'} {item.draft.text.slice(0, 120)}</span>
    {item.status === 'uncertain' && <><Button size="sm" onClick={() => onRetry(item)}>Retry delivery</Button><Button size="sm" onClick={() => onRestore(item)}>Restore draft</Button></>}
    {item.status === 'failed' && <Button size="sm" onClick={() => onRestore(item)}>Restore draft</Button>}
  </div>)}</>;
}
