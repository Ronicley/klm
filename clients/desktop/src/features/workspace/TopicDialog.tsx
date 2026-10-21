import { useEffect, useRef, useState } from 'react';
import { Check, X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import type { Topic } from '../../engine';
import { topicColors } from './topics';

export function TopicDialog({ topic, onSave, onClose }: { topic: Topic; onSave: (input: Pick<Topic, 'name' | 'color'>) => Promise<string | null>; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [name, setName] = useState(topic.name);
  const [color, setColor] = useState(topic.color);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => { dialog.current?.showModal(); }, []);
  return <dialog ref={dialog} className="settings-dialog add-project-dialog" aria-labelledby="topic-dialog-title" onCancel={event => { if (busy) event.preventDefault(); else onClose(); }} onClose={onClose}>
    <form onSubmit={async event => {
      event.preventDefault();
      if (busy || !name.trim()) return;
      setBusy(true); setError('');
      try { const failure = await onSave({ name: name.trim(), color }); if (failure) setError(failure); else onClose(); }
      catch (error) { setError(error instanceof Error ? error.message : 'Could not update topic.'); }
      finally { setBusy(false); }
    }}>
      <div className="dialog-heading"><h2 id="topic-dialog-title">Topic</h2><IconButton label="Close topic editor" disabled={busy} onClick={onClose}><X /></IconButton></div>
      <label className="project-field" htmlFor="topic-name">Name</label>
      <input id="topic-name" autoFocus required maxLength={60} value={name} disabled={busy} onChange={event => setName(event.target.value)} />
      <fieldset className="topic-color-picker" disabled={busy}><legend className="project-field">Color</legend>{topicColors.map(value => <Button key={value} size="sm" className={`topic-color-chip topic-color-${value}`} aria-pressed={color === value} onClick={() => setColor(value)}><span className="topic-color-swatch" aria-hidden="true" /><span>{value[0].toUpperCase() + value.slice(1)}</span><Check className="topic-color-check" aria-hidden="true" /></Button>)}</fieldset>
      {error && <p role="alert" className="form-error">{error}</p>}
      <div className="dialog-actions"><Button disabled={busy} onClick={onClose}>Cancel</Button><Button type="submit" variant="primary" disabled={busy || !name.trim()}>{busy ? 'Saving...' : 'Save'}</Button></div>
    </form>
  </dialog>;
}
