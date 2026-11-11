import { useEffect, useRef } from 'react';
import { request } from '../../engine';
import { IS_DESKTOP, IS_MOBILE_HOST, preparePermissionAudio, showPermissionAlert } from '../../platform';
import { subscribeScope } from './scopedConnection';

type RequestAlert = { id: string; kind: 'permission' | 'question'; projectName: string; sessionTitle: string; origin: string };
type AlertPage = { engineId: string; items: RequestAlert[]; hasMore: boolean; nextAfterId: string };

export function usePermissionAlerts(loaded: boolean, engineIdentity: string) {
  const state = useRef({ identity: engineIdentity, initialized: false, seen: new Set<string>() });

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
    if (state.current.identity !== engineIdentity) state.current = { identity: engineIdentity, initialized: false, seen: new Set() };
    const alerts = state.current;
    const controller = new AbortController();
    let refreshing = false;
    let refreshAgain = false;
    let queued: ReturnType<typeof setTimeout> | undefined;
    const schedule = () => {
      queued ??= setTimeout(() => { queued = undefined; void refresh(); }, 750);
    };
    async function refresh() {
      if (refreshing) { refreshAgain = true; return; }
      refreshing = true;
      try {
        let after = '';
        do {
          const page = await request<AlertPage>(`/api/activity/requests${after ? `?afterId=${encodeURIComponent(after)}` : ''}`, 'GET', undefined, 15000, controller.signal);
          if (controller.signal.aborted || page.engineId !== engineIdentity) return;
          for (const item of page.items) {
            if (alerts.seen.has(item.id)) continue;
            alerts.seen.add(item.id);
            if (!alerts.initialized) continue;
            const label = item.kind === 'question' ? 'Question received' : 'Permission required';
            const body = [item.projectName, item.sessionTitle, item.origin, label].filter(Boolean).join(' · ').slice(0, 300);
            void showPermissionAlert(body, item.id).catch(error => console.warn('Request notification unavailable:', error));
          }
          if (page.hasMore && (!page.nextAfterId || page.nextAfterId === after)) throw new Error('Invalid request alert cursor.');
          after = page.hasMore ? page.nextAfterId : '';
        } while (after);
        alerts.initialized = true;
      } catch (error) {
        if (!controller.signal.aborted) console.warn('Request alerts unavailable:', error);
      } finally {
        refreshing = false;
        if (refreshAgain && !controller.signal.aborted) { refreshAgain = false; schedule(); }
      }
    }
    // Alerts keep their own compact subscription when hidden UI scopes stop loading.
    const unsubscribe = subscribeScope('global', '', schedule);
    const timer = window.setInterval(schedule, 30000);
    void refresh();
    return () => { controller.abort(); clearTimeout(queued); window.clearInterval(timer); unsubscribe(); };
  }, [loaded, engineIdentity]);
}
