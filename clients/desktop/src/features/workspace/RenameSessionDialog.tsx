import { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { request, type Session } from '../../engine';
import { bindDraftDiscard, flushDrafts, useEngineDraft } from '../drafts/draftStore';
import { IS_DESKTOP } from '../../platform';
import { beginLocalActivity } from '../engines/localActivities';

export function RenameSessionDialog({ session, onRename, onClose }: {
  session: Pick<Session, 'id' | 'title'>;
  onRename: (title: string) => Promise<string | null>;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const key = `form.session.rename:${session.id}`;
  const forgetDrafts = bindDraftDiscard();
  const [baseTitle] = useEngineDraft(`${key}.baseTitle`, session.title, true);
  const [title, setTitle] = useEngineDraft(`${key}.title`, session.title);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => { dialog.current?.showModal(); }, []);
  return <dialog ref={dialog} className="settings-dialog add-project-dialog" aria-labelledby="rename-session-title" onCancel={event => { if (busy) event.preventDefault(); else onClose(); }} onClose={onClose}>
    <form onSubmit={async event => {
      event.preventDefault();
      const next = title.trim();
      if (!next || busy) return;
      const finished = beginLocalActivity('Renaming session');
      setBusy(true); setError('');
      try {
        await flushDrafts();
        if (IS_DESKTOP && (await request<Session>(`/api/sessions/${encodeURIComponent(session.id)}`)).title !== baseTitle) throw new Error('Session name changed. Cancel this edit and reopen it to review the saved name.');
        const failure = await onRename(next);
        if (failure) setError(failure); else { forgetDrafts(key); onClose(); }
      } catch (error) { setError(error instanceof Error ? error.message : 'Could not rename the session.'); }
      finally { setBusy(false); finished(); }
    }}>
      <div className="dialog-heading"><h2 id="rename-session-title">Rename session</h2><IconButton label="Cancel renaming session" disabled={busy} onClick={onClose}><X /></IconButton></div>
      <label className="project-field" htmlFor="rename-session-name">Session name</label>
      <input id="rename-session-name" autoFocus required maxLength={200} value={title} onChange={event => setTitle(event.target.value)} disabled={busy} />
      {error && <p role="alert" className="form-error">{error}</p>}
      <div className="dialog-actions"><Button disabled={busy} onClick={() => { forgetDrafts(key); onClose(); }}>Cancel</Button><Button type="submit" variant="primary" disabled={busy || !title.trim()}>{busy ? 'Renaming...' : 'Rename'}</Button></div>
    </form>
  </dialog>;
}
