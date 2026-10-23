import { useState } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { legacyImageFiles, request, type Session, type SessionResponse } from '../../engine';
import { FileAttachments } from './FileAttachments';

const labels = { queued: 'Queued', steering: 'Next turn', sending: 'Sending', accepted: 'Next turn', interrupting: 'Interrupting', paused: 'Paused', uncertain: 'Delivery unconfirmed' };

export function MessageQueue({ session, disabled, onSnapshot, onInterrupt }: { session: Session; disabled?: boolean; onSnapshot: (value: SessionResponse) => void; onInterrupt: (sessionId: string) => void }) {
  const [pending, setPending] = useState('');
  const [error, setError] = useState('');
  async function update(id: string, send: boolean) {
    if (pending || disabled) return;
    if (send) onInterrupt(session.id);
    setPending(id); setError('');
    try {
      onSnapshot(await request<SessionResponse>(`/api/sessions/${encodeURIComponent(session.id)}/queue/${encodeURIComponent(id)}${send ? '/send' : ''}`, send ? 'POST' : 'DELETE', send ? {} : undefined));
    } catch (error) { setError(error instanceof Error ? error.message : 'Could not update the queue.'); }
    finally { setPending(''); }
  }
  if (!session.queue?.length && !error) return null;
  return <section className="message-queue" aria-label="Message queue">
    {session.queue?.map(message => <div className="message-queue-item" key={message.id}>
      <div className="message-queue-content"><small>{message.origin ? `${message.origin.kind === 'instruction' ? 'Instruction' : 'Initial prompt'} from ${message.origin.title} (${message.origin.sessionId}) · ` : ''}{labels[message.status]}</small><p>{message.text}</p><FileAttachments files={[...(message.files ?? []), ...legacyImageFiles(message.images)]} />{message.error && <small className="form-error">{message.error}</small>}</div>
      <div className="message-queue-actions">
        {message.status !== 'sending' && message.status !== 'interrupting' && <Button size="sm" disabled={disabled || !!pending} onClick={() => void update(message.id, true)}>Send now</Button>}
        <IconButton label="Remove queued message" disabled={disabled || !!pending || message.status === 'sending' || message.status === 'accepted' || message.status === 'interrupting'} onClick={() => void update(message.id, false)}><X /></IconButton>
      </div>
    </div>)}
    {error && <p role="alert" className="form-error">{error}</p>}
  </section>;
}
