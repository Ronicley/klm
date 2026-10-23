import { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { request, type Session } from '../../engine';
import { capabilityPath, emptySelection, selectionKey, selectionOf, type ConversationCapabilities } from './api';
import { CapabilitySelector } from './CapabilitySelector';

export function SessionLibraryDialog({ session, onClose }: { session: Session; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [state, setState] = useState<ConversationCapabilities>();
  const [draft, setDraft] = useState(emptySelection);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => { dialog.current?.showModal(); }, []);
  useEffect(() => {
    const controller = new AbortController();
    request<ConversationCapabilities>(capabilityPath(session.id), 'GET', undefined, 15000, controller.signal).then(value => { setState(value); setDraft(selectionOf(value.desired)); }, reason => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Could not load selection.'); });
    return () => controller.abort();
  }, [session.id, attempt]);
  const pending = !state?.applied || selectionKey(draft) !== selectionKey(state.applied.selection);
  return <dialog ref={dialog} className="settings-dialog library-dialog" aria-labelledby="session-library-title" onCancel={onClose} onClose={onClose}>
    <div className="dialog-heading"><h2 id="session-library-title">Library · {session.title}</h2><IconButton label="Close Library" onClick={onClose}><X /></IconButton></div>
    <div className="library-dialog-content">
      {pending && <p className="muted">Applies next turn</p>}
      {state && <CapabilitySelector value={draft} onChange={setDraft} harness={session.harness} general={session.role === 'general_agent'} disabled={saving} />}
      {state?.applied?.diagnostics?.map(message => <p className="form-error" key={message}>{message}</p>)}
      {error && <p className="form-error" role="alert">{error} <Button size="sm" onClick={() => { setError(''); setAttempt(n => n + 1); }}>Reload selection</Button></p>}
    </div>
    <div className="dialog-actions"><Button disabled={saving} onClick={onClose}>Cancel</Button><Button variant="primary" disabled={!state || saving} onClick={async () => {
      if (!state) return;
      setSaving(true); setError('');
      try { await request<ConversationCapabilities>(capabilityPath(session.id), 'PATCH', { revision: state.revision, selection: draft }); onClose(); }
      catch (reason) { setError(reason instanceof Error ? reason.message : 'Could not save selection.'); }
      finally { setSaving(false); }
    }}>{saving ? 'Saving...' : 'Save selection'}</Button></div>
  </dialog>;
}
