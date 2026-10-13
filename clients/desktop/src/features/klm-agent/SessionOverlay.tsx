import { useEffect, useId, useRef, type ReactNode } from 'react';
import { ArrowUpRight, X } from 'lucide-react';
import { IconButton } from '../../design-system/Button';
import type { Project, Session } from '../../engine';

export function SessionOverlay({ session, project, background, children, onClose, onOpenProject }: {
  session?: Session; project?: Project; background?: ReactNode; children: ReactNode;
  onClose: () => void; onOpenProject: (session: Session) => void;
}) {
  const heading = useRef<HTMLHeadingElement>(null);
  const headingId = useId();
  useEffect(() => {
    if (!session) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    heading.current?.focus({ preventScroll: true });
    return () => { if (previous?.isConnected) previous.focus({ preventScroll: true }); };
  }, [session?.id]);
  if (!session) return children;
  return <div className="klm-overlay-stack">
    <div className="klm-overlay-background" inert aria-hidden="true">{background}</div>
    <section className="klm-session-overlay" aria-labelledby={headingId}>
      <header className="klm-overlay-header">
        <div className="klm-overlay-title"><h2 ref={heading} id={headingId} tabIndex={-1}>{session.title}</h2>{project && <span>{project.name}</span>}</div>
        <div className="klm-overlay-actions">
          <IconButton label="Open overlay session in project" onClick={() => onOpenProject(session)}><ArrowUpRight /></IconButton>
          <IconButton label="Close session overlay" onClick={onClose}><X /></IconButton>
        </div>
      </header>
      {children}
    </section>
  </div>;
}
