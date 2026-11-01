import { useEffect, useId, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import type { Graph } from './demo';
import { bindDraftDiscard, flushDrafts, useEngineDraft } from '../drafts/draftStore';
import { beginLocalActivity } from '../engines/localActivities';
import { agentSlug } from '../agents/types';

export function GraphDetailsDialog({ graph, graphs, onSave, onClose, draftKey = `form.graphDetails:${graph?.id ?? 'new'}` }: {
  draftKey?: string;
  graph?: Graph;
  graphs: Graph[];
  onSave: (name: string, description: string) => void | Promise<void>;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const id = useId();
  const forgetDrafts = bindDraftDiscard();
  const [name, setName] = useEngineDraft(`${draftKey}.name`, graph?.name ?? '');
  const [description, setDescription] = useEngineDraft(`${draftKey}.description`, graph?.description ?? '');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const duplicate = graphs.some(item => item.id !== graph?.id && agentSlug(item.name) === agentSlug(name));
  useEffect(() => { dialog.current?.showModal(); }, []);

  return <dialog ref={dialog} className="settings-dialog graph-details-dialog" aria-labelledby={`${id}-title`} onCancel={onClose} onClose={onClose}>
    <form className="graph-details-form" onSubmit={async event => {
      event.preventDefault();
      if (!agentSlug(name) || duplicate || saving) return;
      const finished = beginLocalActivity('Saving graph details');
      setSaving(true); setError('');
      try { await flushDrafts(); await onSave(name.trim(), description.trim()); forgetDrafts(draftKey); } catch (e) { setError(e instanceof Error ? e.message : 'Could not create graph.'); }
      finally { setSaving(false); finished(); }
    }}>
      <div className="dialog-heading"><h2 id={`${id}-title`}>{graph ? 'Edit graph' : 'New graph'}</h2><IconButton label="Close graph details" onClick={onClose}><X /></IconButton></div>
      <div className="graph-details-field"><label htmlFor={`${id}-name`}>Name</label><Input id={`${id}-name`} autoFocus required maxLength={80} value={name} aria-invalid={duplicate} aria-describedby={duplicate ? `${id}-error` : undefined} onChange={event => setName(event.target.value)} />{duplicate && <p id={`${id}-error`} className="form-error" role="alert">A graph with this name already exists.</p>}</div>
      <div className="graph-details-field"><label htmlFor={`${id}-description`}>Description</label><Input id={`${id}-description`} maxLength={240} value={description} onChange={event => setDescription(event.target.value)} /></div>
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="graph-details-actions"><Button disabled={saving} onClick={() => { forgetDrafts(draftKey); onClose(); }}>Cancel</Button><Button type="submit" variant="primary" disabled={!agentSlug(name) || duplicate || saving}>{saving ? 'Saving...' : graph ? 'Apply' : 'Create graph'}</Button></div>
    </form>
  </dialog>;
}
