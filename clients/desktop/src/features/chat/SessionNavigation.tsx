import { createContext, useContext, useMemo, type ReactNode } from 'react';
import type { Project, Session } from '../../engine';

type SessionNavigation = {
  sessions: ReadonlyMap<string, Session>;
  projects: ReadonlyMap<string, Project>;
  onOpenSession?: (session: Session) => void;
};
const Context = createContext<SessionNavigation>({ sessions: new Map(), projects: new Map() });

// Nested chat surfaces keep the same inventory but choose their navigation destination.
export function SessionNavigationProvider({ sessions, projects, onOpenSession, children }: {
  sessions?: Session[]; projects?: Project[]; onOpenSession: (session: Session) => void; children: ReactNode;
}) {
  const inherited = useContext(Context);
  const sessionsById = useMemo(() => sessions ? new Map(sessions.map(session => [session.id, session])) : inherited.sessions, [sessions, inherited.sessions]);
  const projectsById = useMemo(() => projects ? new Map(projects.map(project => [project.id, project])) : inherited.projects, [projects, inherited.projects]);
  const value = useMemo(() => ({ sessions: sessionsById, projects: projectsById, onOpenSession }), [sessionsById, projectsById, onOpenSession]);
  return <Context.Provider value={value}>{children}</Context.Provider>;
}

export function useSessionNavigation() {
  return useContext(Context);
}
