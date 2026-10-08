import { useEffect, useRef } from 'react';
import type { Project, Session } from '../../engine';
import { IS_DESKTOP, IS_MOBILE_HOST, preparePermissionAudio, showPermissionAlert } from '../../platform';

export function usePermissionAlerts(sessions: Session[], projects: Project[], loaded: boolean) {
  const seen = useRef(new Set<string>());
  const initialized = useRef(false);

  useEffect(() => {
    if (IS_DESKTOP || IS_MOBILE_HOST) return;
    const prepare = () => { void preparePermissionAudio().catch(error => console.warn('Permission alert audio unavailable:', error)); };
    window.addEventListener('pointerdown', prepare);
    window.addEventListener('keydown', prepare);
    return () => {
      window.removeEventListener('pointerdown', prepare);
      window.removeEventListener('keydown', prepare);
    };
  }, []);

  useEffect(() => {
    if (!loaded || IS_MOBILE_HOST) return;
    for (const session of sessions) {
      if (session.parentId || session.role === 'graph_node' || session.role === 'subagent' || session.role === 'side_agent') continue;
      const project = projects.find(item => item.id === session.projectId);
      const muted = session.archived || project?.archivedFolders.includes(session.workspace);
      const requests = [
        ...(session.permissions ?? []).map(permission => ({ sessionId: session.id, permission, origin: '' })),
        ...(session.graph?.requests ?? []).flatMap(item => item.kind === 'permission' && item.permission
          ? [{ sessionId: item.sessionId, permission: { ...item.permission, id: item.requestId }, origin: item.nodeName }] : []),
      ];
      for (const { sessionId, permission, origin } of requests) {
        const key = JSON.stringify([sessionId, permission.id]);
        if (seen.current.has(key)) continue;
        seen.current.add(key);
        if (!initialized.current || muted || permission.resolving) continue;
        const body = [project?.name, session.title, origin, permission.title || 'Permission required'].filter(Boolean).join(' · ').slice(0, 300);
        void showPermissionAlert(body, key).catch(error => console.warn('Permission notification unavailable:', error));
      }
    }
    initialized.current = true;
  }, [sessions, projects, loaded]);
}
