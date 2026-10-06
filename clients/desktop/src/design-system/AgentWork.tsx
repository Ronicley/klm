import { ChevronDown, Clock3, type LucideIcon } from 'lucide-react';
import { useEffect, useId, useRef, useState, type ComponentPropsWithoutRef, type ReactNode } from 'react';
import { Icon } from './Icon';

export type AgentWorkStatus = 'running' | 'completed' | 'failed';
export type AgentWorkItemStatus = 'running' | 'completed' | 'failed';
export type AgentWorkTone = 'accent' | 'danger' | 'muted' | 'success' | 'warning';

export type AgentWorkThought = {
  id: string;
  status: 'running' | 'completed';
  text: string;
  durationLabel?: string;
  preview?: ReactNode;
  details?: ReactNode;
};

export type AgentWorkActivity = {
  id: string;
  icon: LucideIcon;
  label: string;
  detail?: string;
  status: AgentWorkItemStatus;
  tone?: AgentWorkTone;
  diff?: { added: number; removed: number };
  details?: ReactNode;
};

export type AgentWorkProps = Omit<ComponentPropsWithoutRef<'section'>, 'children'> & {
  label?: string;
  status: AgentWorkStatus;
  durationLabel?: string;
  thoughts?: readonly AgentWorkThought[];
  activities?: readonly AgentWorkActivity[];
  streamedResponse?: string;
};

function MatrixLoader() {
  return (
    <span aria-hidden="true" className="agent-work__matrix">
      {Array.from({ length: 9 }, (_, index) => <span key={index} />)}
    </span>
  );
}

function terminalLabel(status: Exclude<AgentWorkStatus, 'running'>, durationLabel?: string): string {
  if (status === 'failed') return durationLabel ? `Work failed after ${durationLabel}` : 'Work failed';
  return durationLabel ? `Worked for ${durationLabel}` : 'Work completed';
}

function LazyDisclosure({ className, details, children }: { className: string; details: ReactNode; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  return <details className={className} onToggle={event => setOpen(event.currentTarget.open)}>{children}{open && <div className="agent-work__item-details">{details}</div>}</details>;
}

function ActivityRow({ activity }: { activity: AgentWorkActivity }) {
  const tone = activity.status === 'running' ? 'pending' : activity.tone ?? (activity.status === 'failed' ? 'danger' : 'muted');
  const row = <>
      <span className={`agent-work__activity-icon agent-work__activity-icon--${tone}`}>
        {activity.status === 'running' ? null : <Icon glyph={activity.icon} size={12} />}
      </span>
      <strong>{activity.label}</strong>
      <span className="agent-work__activity-status">Status: {activity.status}.</span>
      {activity.detail ? <code>{activity.detail}</code> : null}
      {activity.diff ? <span className="agent-work__diff"><ins>+{activity.diff.added}</ins><del>-{activity.diff.removed}</del></span> : activity.details ? <Icon glyph={ChevronDown} size={10} /> : null}
    </>;
  return activity.details ? (
    <li className="agent-work__activity-item">
      <LazyDisclosure className="agent-work__activity-disclosure" details={activity.details}>
        <summary className="agent-work__activity">{row}</summary>
      </LazyDisclosure>
    </li>
  ) : <li className="agent-work__activity">{row}</li>;
}

export function AgentWork({ status, durationLabel, label, thoughts = [], activities = [], streamedResponse, className, ...props }: AgentWorkProps) {
  const [expanded, setExpanded] = useState(status === 'running');
  const detailsId = useId();
  const itemsRef = useRef<HTMLDivElement>(null);
  const hasDetails = thoughts.length > 0 || activities.length > 0;
  const itemCount = thoughts.length + activities.length;
  const response = streamedResponse?.trim() ? streamedResponse : undefined;

  useEffect(() => {
    setExpanded(status === 'running');
  }, [status]);

  useEffect(() => {
    if (status !== 'running' || !expanded || itemCount <= 5) return;
    const frame = window.requestAnimationFrame(() => {
      const items = itemsRef.current;
      if (items) items.scrollTop = items.scrollHeight;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [expanded, itemCount, status]);

  if (status !== 'running' && !hasDetails && !response) return null;

  const headerLabel = label ?? (status === 'running' ? 'Working...' : terminalLabel(status, durationLabel));
  return (
    <section {...props} aria-label={props['aria-label'] ?? (label ? `${label} work` : 'Agent work')} className={['agent-work', className].filter(Boolean).join(' ')}>
      <span aria-live="polite" className="agent-work__status" role="status">{label ?? 'Agent'} work {status === 'running' ? 'in progress' : status}.</span>
      <button
        aria-controls={hasDetails ? detailsId : undefined}
        aria-expanded={hasDetails && expanded}
        className={`agent-work__header${status === 'running' ? ' agent-work__header--active' : ''}`}
        disabled={!hasDetails}
        onClick={() => setExpanded(value => !value)}
        type="button"
      >
        {status === 'running' ? <MatrixLoader /> : null}
        <span>{headerLabel}{(label || status === 'running') && durationLabel ? ` · ${durationLabel}` : ''}</span>
        <Icon glyph={ChevronDown} size={10} />
      </button>
      {hasDetails && expanded ? (
        <div className="agent-work__details" id={detailsId}>
          <div className={`agent-work__items${itemCount > 5 ? ' agent-work__items--scrollable' : ''}`} ref={itemsRef}>
            {thoughts.map(thought => (
              <div className="agent-work__thought" key={thought.id}>
                {thought.details ? <LazyDisclosure className="agent-work__thought-disclosure" details={thought.details}>
                  <summary className="agent-work__thought-row"><Icon glyph={Clock3} size={12} /><span className="agent-work__thought-copy"><strong>Thought:</strong> {(thought.preview ?? thought.text) || 'Thinking...'}{thought.durationLabel ? ` · ${thought.durationLabel}` : ''}</span><Icon glyph={ChevronDown} size={10} /></summary>
                </LazyDisclosure> : <div className="agent-work__thought-row"><Icon glyph={Clock3} size={12} /><span><strong>Thought:</strong> {thought.text || 'Thinking...'}{thought.durationLabel ? ` · ${thought.durationLabel}` : ''}</span></div>}
              </div>
            ))}
            {activities.length > 0 ? <ul className="agent-work__activities" role="list">{activities.map(activity => <ActivityRow activity={activity} key={activity.id} />)}</ul> : null}
          </div>
        </div>
      ) : null}
      {response ? <p className="agent-work__response">{response}</p> : null}
    </section>
  );
}
