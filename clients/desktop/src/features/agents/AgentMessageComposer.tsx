import { useState, type ComponentProps } from 'react';
import { Button } from '../../design-system/Button';
import type { Session, SessionResponse } from '../../engine';
import { MessageComposer } from '../chat/MessageComposer';
import { editableAgentSession, useSessionAgent, agentExecutionBlocked } from './binding';
import { SessionAgentDialog } from './SessionAgentDialog';
import { agentOrigin } from './types';

type Props = ComponentProps<typeof MessageComposer> & { session: Session; onSnapshot: (value: SessionResponse) => void };
export function AgentMessageComposer(props: Props) {
  if (!editableAgentSession(props.session)) return <MessageComposer {...props} actions={props.session.delegation?.actions} />;
  return <BoundComposer {...props} />;
}
function BoundComposer({ session, onSnapshot, ...props }: Props) {
  const state = useSessionAgent(session);
  const [open, setOpen] = useState(false);
  const blocked = !!state.error || !state.agent || !state.agent.execution.allowed;
  const agent = state.agent ?? session.agentConfig;
  return <>
    <div className="agent-session-notice">
      <Button variant="ghost" size="sm" disabled={session.delegation?.actions.settings === false} onClick={() => setOpen(true)}>{agent?.name || 'Agent settings'} · {agent ? agentOrigin(agent.source) : ''}</Button>
      {agent?.pending && <span>Changes next turn</span>}
      {blocked && <p className="form-error" role={state.loading && !state.agent ? 'status' : 'alert'}>{state.error ? 'Agent settings unavailable.' : state.agent?.execution.message || 'Loading agent settings...'} <Button size="sm" onClick={() => setOpen(true)}>{state.agent?.execution.fields?.some(field => field === 'model' || field === 'effort') ? 'Choose model' : 'Agent settings'}</Button><Button size="sm" onClick={state.refresh}>Reload</Button></p>}
    </div>
    <MessageComposer {...props} actions={session.delegation?.actions} disabled={props.disabled || blocked} onSend={(draft, mode) => {
      if (agentExecutionBlocked(session)) return false;
      return props.onSend(draft, mode);
    }} />
    {open && <SessionAgentDialog session={session} onSnapshot={onSnapshot} onClose={() => setOpen(false)} />}
  </>;
}
