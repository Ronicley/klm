import { Bot } from 'lucide-react';

export function SubagentActivity({ title, agentName, detail, running, onOpen }: {
  title: string;
  agentName?: string;
  detail: string;
  running: boolean;
  onOpen?: () => void;
}) {
  const activity = agentName ? `${agentName} — ${detail}` : detail;
  return <button type="button" className={`subagent-activity ${running ? 'is-running' : ''}`} disabled={!onOpen} onClick={onOpen}>
    <span className="subagent-activity-icon"><Bot aria-hidden="true" /></span>
    <span className="subagent-activity-copy"><strong title={title}>{title}</strong><small title={activity}>{activity}</small></span>
  </button>;
}
