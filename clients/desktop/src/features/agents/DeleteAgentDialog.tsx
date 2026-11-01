import { useEffect, useId, useRef } from 'react';
import { Button } from '../../design-system/Button';

export function DeleteAgentDialog({ agentName, busy = false, onConfirm, onClose }: {
  agentName: string;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const id = useId();
  useEffect(() => { dialog.current?.showModal(); }, []);

  return <dialog ref={dialog} className="settings-dialog agent-delete-dialog" aria-labelledby={`${id}-title`} aria-describedby={`${id}-description`} onCancel={event => { event.stopPropagation(); if (busy) event.preventDefault(); else onClose(); }} onClose={event => { event.stopPropagation(); onClose(); }}>
    <h2 id={`${id}-title`}>Delete agent</h2>
    <p id={`${id}-description`}>Are you sure you want to delete “{agentName}”?</p>
    <div className="dialog-actions"><Button autoFocus disabled={busy} onClick={onClose}>Cancel</Button><Button variant="danger-soft" disabled={busy} onClick={onConfirm}>{busy ? 'Deleting...' : 'Delete agent'}</Button></div>
  </dialog>;
}
