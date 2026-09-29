import { Atom, CircleAlert, Plug, Terminal, Wrench } from 'lucide-react';
import type { EngineEvent } from '../../engine';
import { ChatMessage } from './ChatMessage';

export function SessionEvent({ event, onFavorite }: { event: EngineEvent; onFavorite?: (favorite: boolean) => Promise<void> }) {
  if (event.type === 'consultation' || event.type === 'subagent') return null;
  if (event.type === 'session_spawn') return <p className="event-status">{event.title} <span className="muted">({String(event.data?.sessionId ?? '')})</span></p>;
  if (event.type === 'user' || event.type === 'assistant' || event.type === 'agent_prompt') return <div>
    {event.type === 'agent_prompt' && <small className="muted">Initial prompt from {String((event.data?.origin as { title?: string } | undefined)?.title ?? 'another session')} ({String((event.data?.origin as { sessionId?: string } | undefined)?.sessionId ?? '')})</small>}
    {Array.isArray(event.data?.sources) && <details className="sent-selection-context"><summary>Selected context</summary>{event.data.sources.map((source: unknown, index: number) => source && typeof source === 'object' && 'passage' in source && typeof source.passage === 'string' ? <blockquote key={index}>{source.passage}</blockquote> : null)}</details>}
    <ChatMessage message={{ id: event.id, role: event.type === 'agent_prompt' ? 'user' : event.type, text: event.text, status: event.status, favorite: event.favorite, mentions: event.data?.mentions, mentionPreparation: event.data?.mentionPreparation }} senderLabel={event.type === 'agent_prompt' ? 'Initial prompt from another session' : undefined} onFavorite={onFavorite} />
  </div>;
  if (event.type === 'error') return <div className="event-error" role="alert"><CircleAlert /><div><strong>{event.title || 'Harness error'}</strong><p>{event.text}</p></div></div>;
  if (event.type === 'status') return event.status === 'warning' || event.status === 'cancelled' ? <p className="event-status">{event.text || event.title}</p> : null;
  if (event.type === 'reasoning' && !event.text.trim()) return null;
  const Icon = event.type === 'reasoning' ? Atom : event.type === 'command' ? Terminal : event.type === 'mcp' ? Plug : Wrench;
  const label = event.title || ({ reasoning: 'Reasoning', command: 'Command', mcp: 'MCP call', tool: 'Tool call' } as const)[event.type];
  return <details className={`session-event session-event--${event.type}`}>
    <summary><Icon /><span>{label}</span>{event.status && <small className={event.status === 'error' || event.status === 'failed' ? 'form-error' : 'muted'}>{event.status}</small>}</summary>
    {event.text && <pre>{event.text}</pre>}
    {event.data && Object.keys(event.data).length > 0 && <details className="event-details"><summary>Details</summary><pre>{JSON.stringify(event.data, null, 2)}</pre></details>}
  </details>;
}
