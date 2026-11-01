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
      setState(old => ({ key, page: old.key === key ? old.page : undefined, loading: true }));
      try {
        const page = await getAgentPage(projectId, search, offset, requestController.signal);
        if (current() && !requestController.signal.aborted) setState({ key, page, loading: false });
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
  return { page: state.key === key ? state.page : undefined,
    error: state.key === key ? state.error : undefined,
    loading: state.key !== key || state.loading, reload };
}
