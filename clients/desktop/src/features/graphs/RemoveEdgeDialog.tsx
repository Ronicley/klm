import { useEffect, useId, useRef } from 'react';
import { Button } from '../../design-system/Button';

export function RemoveEdgeDialog({ count, connectionName, onConfirm, onClose }: {
  count: number;
  connectionName: string;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const id = useId();
  useEffect(() => { dialog.current?.showModal(); }, []);
  return <dialog ref={dialog} className="settings-dialog graph-remove-node-dialog" aria-labelledby={`${id}-title`} aria-describedby={`${id}-description`} onCancel={onClose} onClose={onClose}>
    <h2 id={`${id}-title`}>{count === 1 ? 'Remove connection' : 'Remove connections'}</h2>
    <p id={`${id}-description`}>{count === 1 ? `Remove “${connectionName}” from this graph?` : `Remove ${count} connections from this graph?`}</p>
    <div className="dialog-actions"><Button autoFocus onClick={onClose}>Cancel</Button><Button variant="danger-soft" onClick={onConfirm}>Remove</Button></div>
  </dialog>;
}
