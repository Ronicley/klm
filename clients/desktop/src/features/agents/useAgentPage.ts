import { useCallback, useEffect, useRef, useState } from 'react';
import { ENGINE_URL } from '../../engine';
import { currentEngineIdentity, engineIdentityGeneration } from '../../engineIdentity';
import { subscribeScope } from '../chat/scopedConnection';
import { getAgentPage } from './api';
import type { AgentPage } from './types';

// The mounted page is its bounded cache; it never shares data across engine/scope.
export function useAgentPage(projectId: string, search: string, offset: number) {
  const identity = `${ENGINE_URL}/${currentEngineIdentity()}/${engineIdentityGeneration()}`;
  const key = JSON.stringify([identity, projectId, search, offset]);
  const scope = JSON.stringify([identity, projectId, search]);
  const retained = useRef<{ scope: string; page: AgentPage } | undefined>(undefined);
  const [state, setState] = useState<{ key: string; page?: AgentPage; error?: unknown; loading: boolean }>({ key: '', loading: true });
  const reader = useRef<{ key: string; refresh: () => void } | null>(null);
  const latestKey = useRef(key);
  latestKey.current = key;
  const reload = useCallback(() => { if (reader.current?.key === key) reader.current.refresh(); }, [key]);
  useEffect(() => {
    let active = true, inFlight = false, dirty = false;
    let controller: AbortController | undefined;
    const current = () => active && latestKey.current === key && identity === `${ENGINE_URL}/${currentEngineIdentity()}/${engineIdentityGeneration()}`;
    async function read() {
      inFlight = true; dirty = false;
      const requestController = new AbortController(); controller = requestController;
      setState(old => ({ key, page: retained.current?.scope === scope ? retained.current.page : old.key === key ? old.page : undefined, loading: true }));
      try {
        let page = await getAgentPage(projectId, search, 0, requestController.signal);
        const agents = [...page.agents];
        const boundary = retained.current?.scope === scope ? retained.current.page.agents.at(-1)?.source.sourceUid : undefined;
        while (page.hasMore && (page.nextOffset <= offset || !!boundary && !agents.some(agent => agent.source.sourceUid === boundary))) {
          const cursor = page.nextOffset;
          page = await getAgentPage(projectId, search, cursor, requestController.signal);
          if (page.hasMore && page.nextOffset <= cursor) throw new Error('Agent pagination did not advance. Retry.');
          agents.push(...page.agents);
        }
        page = { ...page, agents: [...new Map(agents.map(agent => [`${agent.source.sourceUid}/${agent.source.projectId}/${agent.id}`, agent])).values()] };
        if (current() && !requestController.signal.aborted) { retained.current = { scope, page }; setState({ key, page, loading: false }); }
      } catch (error) {
        if (current() && !requestController.signal.aborted) setState(old => ({ ...old, key, error, loading: false }));
      } finally {
        inFlight = false;
        // Multiple notices during one read require one follow-up, not cancellation.
        if (current() && dirty) void read();
      }
    }
    const entry = { key, refresh: () => {
      if (!current()) return;
      if (inFlight) dirty = true;
      else void read();
    } };
    reader.current = entry;
    entry.refresh();
    return () => {
      active = false; controller?.abort();
      if (reader.current === entry) reader.current = null;
    };
  }, [identity, key, projectId, search, offset]);
  useEffect(() => {
    const refresh = () => reader.current?.refresh();
    const global = subscribeScope('global', '', refresh);
    const project = projectId ? subscribeScope('project', projectId, refresh) : undefined;
    return () => { global(); project?.(); };
  }, [identity, projectId]);
  return { page: state.key === key ? state.page : retained.current?.scope === scope ? retained.current.page : undefined,
    error: state.key === key ? state.error : undefined,
    loading: state.key !== key || state.loading, reload };
}
