import { useEffect, useId, useRef, useState } from 'react';
import { Button } from '../../design-system/Button';
import type { Session } from '../../engine';

export function DeleteSessionDialog({ session, onDelete, onClose }: {
  session: Session; onDelete: () => Promise<string | null>; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const pending = useRef(false);
  const id = useId();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => { dialog.current?.showModal(); }, []);
  return <dialog ref={dialog} className="settings-dialog add-project-dialog" aria-labelledby={`${id}-title`} aria-describedby={`${id}-description`} onCancel={event => { if (busy) event.preventDefault(); else onClose(); }} onClose={onClose}>
    <h2 id={`${id}-title`}>Delete session</h2>
    <p id={`${id}-description`}>Permanently delete “{session.title}”, its history, attachments and linked conversations? This cannot be undone.</p>
    {error && <p role="alert" className="form-error">{error}</p>}
    <div className="dialog-actions"><Button autoFocus disabled={busy} onClick={onClose}>Cancel</Button><Button variant="danger-soft" disabled={busy} onClick={async () => {
      if (pending.current) return;
      pending.current = true; setBusy(true); setError('');
      try { const failure = await onDelete(); if (failure) setError(failure); else onClose(); }
      catch (error) { setError(error instanceof Error ? error.message : 'Could not delete the session.'); }
      finally { pending.current = false; setBusy(false); }
    }}>{busy ? 'Deleting...' : 'Delete'}</Button></div>
  </dialog>;
}
