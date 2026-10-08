import { Button } from '../../design-system/Button';
import { ENGINE_URL, type SourceReference } from '../../engine';
import type { ComposerDraft, SendMode } from './MessageComposer';
import { FileAttachments } from './FileAttachments';

export type LocalSubmission = { id: string; draft: ComposerDraft; sources: SourceReference[]; mode: SendMode; status: 'sending' | 'accepted' | 'uncertain' | 'failed' };

const storageKey = `klm.pending-submissions.v1:${ENGINE_URL}`;
export function readPendingSubmissions(): Record<string, LocalSubmission[]> {
  try {
    const saved: unknown = JSON.parse(sessionStorage.getItem(storageKey) ?? '{}');
    if (!saved || typeof saved !== 'object' || Array.isArray(saved)) return {};
    const result: Record<string, LocalSubmission[]> = {};
    for (const [session, items] of Object.entries(saved)) {
      if (!Array.isArray(items)) continue;
      result[session] = items.filter((item): item is LocalSubmission => item && typeof item.id === 'string' &&
        typeof item.draft?.text === 'string' && Array.isArray(item.draft.mentions) && Array.isArray(item.sources) &&
        (item.mode === 'queue' || item.mode === 'steer')).map(item => ({ ...item, draft: { ...item.draft, files: undefined }, status: item.status === 'failed' ? 'failed' : 'uncertain' }));
    }
    return result;
  } catch { return {}; }
}

export function savePendingSubmissions(items: Record<string, LocalSubmission[]>) {
  const saved = Object.fromEntries(Object.entries(items).map(([session, messages]) => [session, messages.map(item => ({ ...item, draft: {
    ...item.draft, files: undefined, missingFiles: [...(item.draft.missingFiles ?? []), ...(item.draft.files ?? []).map(file => ({ name: file.name, size: file.size }))],
  } }))]));
  try { sessionStorage.setItem(storageKey, JSON.stringify(saved)); } catch { /* Still recoverable in memory if browser storage is full. */ }
}

export function PendingSubmission({ items, onRestore, onRetry }: { items: LocalSubmission[]; onRestore: (item: LocalSubmission) => void; onRetry: (item: LocalSubmission) => void }) {
  return <>{items.map(item => <div key={item.id} className="storage-error" role="status">
    <span>{item.status === 'sending' ? (item.mode === 'steer' ? 'Sending now…' : 'Sending message…') : item.status === 'accepted' ? 'Message accepted.' : item.status === 'failed' ? 'Message rejected.' : 'Delivery unconfirmed.'} {item.draft.text.slice(0, 120)}</span>
    <FileAttachments files={item.draft.files} />
    <FileAttachments files={item.draft.missingFiles} missing />
    {item.status === 'uncertain' && <><Button size="sm" disabled={!!item.draft.missingFiles?.length} onClick={() => onRetry(item)}>Retry delivery</Button><Button size="sm" onClick={() => onRestore(item)}>Restore draft</Button></>}
    {item.status === 'failed' && <Button size="sm" onClick={() => onRestore(item)}>Restore draft</Button>}
  </div>)}</>;
}
