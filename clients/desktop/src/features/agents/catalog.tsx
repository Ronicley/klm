import { createContext, useContext, useEffect, useState } from 'react';
import { ENGINE_URL, request, type Harness, type ModelCatalog } from '../../engine';
import { currentEngineIdentity, engineIdentityGeneration } from '../../engineIdentity';
import type { AgentAuthoringScope } from './types';

export const AuthoringProject = createContext<string | undefined>(undefined);
export type AgentModel = { id: string; name: string; provider: string; efforts: string[]; defaultEffort?: string };
// Visual previews must opt in explicitly; a missing production scope is not a demo.
export const DemoAgentModels = createContext<Partial<Record<Harness['id'], AgentModel[]>> | undefined>(undefined);
const cache = new Map<string, { expires: number; promise: Promise<AgentModel[]> }>();
let cacheIdentity = '';

export function useAgentModels(harness?: Harness['id'], scope?: AgentAuthoringScope): { models: AgentModel[]; loading: boolean; error: string; retry: () => void } {
  const projectId = useContext(AuthoringProject);
  const demoModels = useContext(DemoAgentModels);
  const scopeProject = scope?.kind === 'project' ? scope.projectId : scope?.kind === 'global' ? '' : projectId;
  const global = scope?.kind === 'global';
  const identity = `${ENGINE_URL}/${currentEngineIdentity()}/${engineIdentityGeneration()}`;
  if (cacheIdentity !== identity) { cache.clear(); cacheIdentity = identity; }
  const [snapshot, setSnapshot] = useState<{ key: string; models: AgentModel[]; error: string }>({ key: '', models: [], error: '' });
  const [attempt, setAttempt] = useState(0);
  const cacheKey = JSON.stringify([identity, global ? 'global' : 'project', scopeProject, harness]);
  const key = `${cacheKey}/${attempt}`;
  useEffect(() => {
    if ((!scopeProject && !global) || !harness) return;
    let active = true;
    let entry = cache.get(cacheKey);
    if (!entry || entry.expires < Date.now()) {
      const endpoint = global ? `/api/models/${harness}` : `/api/projects/${encodeURIComponent(scopeProject!)}/models/${harness}`;
      const promise = request<ModelCatalog>(endpoint, 'GET', undefined, 55000)
        .then(c => c.models.map(m => ({ id: m.id, name: m.name, provider: m.providerName, efforts: m.efforts ?? [], defaultEffort: m.defaultEffort })));
      entry = { expires: Date.now() + 120000, promise }; cache.set(cacheKey, entry);
    }
    entry.promise.then(models => { if (active) setSnapshot({ key, models, error: '' }); }, error => {
      cache.delete(cacheKey); if (active) setSnapshot({ key, models: [], error: error instanceof Error ? error.message : 'Could not load models.' });
    });
    return () => { active = false; };
  }, [scopeProject, global, harness, key, cacheKey]);
  if (!scopeProject && !global) return {
    models: harness && demoModels ? demoModels[harness] ?? [] : [],
    loading: false,
    error: harness && !demoModels ? 'Model catalog context is unavailable.' : '',
    retry: () => {},
  };
  return { models: snapshot.key === key ? snapshot.models : [], error: snapshot.key === key ? snapshot.error : '', loading: !!harness && snapshot.key !== key,
    retry: () => { cache.delete(cacheKey); setAttempt(n => n + 1); } };
}
