import { useEffect, useRef } from 'react';
import { Button } from '../../design-system/Button';

export function StopAutomationDialog({ onDecision }: { onDecision: (confirmed: boolean) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => { dialog.current?.showModal(); }, []);
  return <dialog ref={dialog} className="settings-dialog add-project-dialog" aria-labelledby="stop-automation-title" onCancel={event => { event.preventDefault(); onDecision(false); }}>
    <h2 id="stop-automation-title">Stop automation run?</h2>
    <p>This will stop the model, mark this automation run as interrupted by you, and return this conversation to normal chat.</p>
    <div className="dialog-actions"><Button onClick={() => onDecision(false)}>Cancel</Button><Button variant="danger-soft" onClick={() => onDecision(true)}>Stop run</Button></div>
  </dialog>;
}
