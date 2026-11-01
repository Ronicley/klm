import { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { request, type QueuedMessage, type Session, type SessionResponse } from '../../engine';
import { agentExecutionBlocked, useSessionAgent } from '../agents/binding';

const labels = { queued: 'Queued', steering: 'Next turn', sending: 'Sending', accepted: 'Next turn', interrupting: 'Interrupting', paused: 'Paused', uncertain: 'Delivery unconfirmed' };

export function MessageQueue({ session, disabled, onSnapshot, onInterrupt }: { session: Session; disabled?: boolean; onSnapshot: (value: SessionResponse) => void; onInterrupt: (sessionId: string) => void }) {
  useSessionAgent(session);
  const executionBlocked = agentExecutionBlocked(session);
  const [pending, setPending] = useState('');
  const [error, setError] = useState('');
  const [page, setPage] = useState<{ items: QueuedMessage[]; nextAfterId: string; hasMore: boolean }>();
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), [session.id]);
  const messages = [...(session.queue ?? []), ...(session.pendingMore?.queue ? page?.items.filter(item => !session.queue?.some(base => base.id === item.id)) ?? [] : [])];
  async function more(first = false) {
    if (pending) return; setPending('page'); setError('');
    const current = new AbortController(); controller.current = current;
    const after = first ? session.queue?.at(-1)?.id : page?.nextAfterId ?? session.queue?.at(-1)?.id;
    try { const result = await request<{ items: QueuedMessage[]; nextAfterId: string; hasMore: boolean }>(`/api/sessions/${encodeURIComponent(session.id)}/pending?kind=queue${after ? `&afterId=${encodeURIComponent(after)}` : ''}`, 'GET', undefined, 15000, current.signal); if (!current.signal.aborted) setPage(result); }
    catch (error) { if (!current.signal.aborted) setError(error instanceof Error ? error.message : 'Queue changed. Reload the conversation.'); }
    finally { if (!current.signal.aborted) setPending(''); }
  }
  async function update(id: string, send: boolean) {
     if (pending || disabled || send && (agentExecutionBlocked(session) || session.delegation?.actions.interrupt === false)) return;
    const retryUncertain = send && messages.find(item => item.id === id)?.status === 'uncertain';
    if (retryUncertain && !window.confirm('Delivery was not confirmed. Retrying may deliver this message twice. Retry delivery?')) return;
    if (send) onInterrupt(session.id);
    setPending(id); setError('');
    try {
      onSnapshot(await request<SessionResponse>(`/api/sessions/${encodeURIComponent(session.id)}/queue/${encodeURIComponent(id)}${send ? '/send' : ''}`, send ? 'POST' : 'DELETE', send ? { retryUncertain } : undefined)); setPage(undefined);
    } catch (error) { setError(error instanceof Error ? error.message : 'Could not update the queue.'); }
    finally { setPending(''); }
  }
  if (!session.queue?.length && !error) return null;
  return <section className="message-queue" aria-label="Message queue">
    {messages.map(message => <div className="message-queue-item" key={message.id}>
      <div className="message-queue-content"><small>{message.origin ? `${message.origin.kind === 'instruction' ? 'Instruction' : 'Initial prompt'} from ${message.origin.title} (${message.origin.sessionId}) · ` : ''}{labels[message.status]}</small><p>{message.text}</p>{message.error && <small className="form-error">{message.error}</small>}</div>
      <div className="message-queue-actions">
        {session.automation?.status !== 'running' && message.status !== 'sending' && message.status !== 'interrupting' && <Button size="sm" disabled={disabled || !!pending || executionBlocked || session.delegation?.actions.interrupt === false} onClick={() => void update(message.id, true)}>Send now</Button>}
        <IconButton label="Remove queued message" disabled={disabled || !!pending || message.status === 'sending' || message.status === 'accepted' || message.status === 'interrupting'} onClick={() => void update(message.id, false)}><X /></IconButton>
      </div>
    </div>)}
    {session.pendingMore?.queue && (page?.hasMore ?? true) && <Button size="sm" disabled={!!pending} onClick={() => void more()}>More queued messages</Button>}
    {page && session.pendingMore?.queue && <Button size="sm" disabled={!!pending} onClick={() => void more(true)}>First additional queued messages</Button>}
    {error && <p role="alert" className="form-error">{error}</p>}
  </section>;
}
