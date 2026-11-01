import { useCallback, useEffect, useRef, useSyncExternalStore } from 'react';
import { ENGINE_URL, request, type Session } from '../../engine';
import { currentEngineIdentity, engineIdentityGeneration } from '../../engineIdentity';
import { subscribeScope } from '../chat/scopedConnection';
import type { AgentOverridePatch, SessionAgent } from './types';

type Snapshot = { agent?: SessionAgent | null; loading: boolean; error?: unknown };
type Entry = { state: Snapshot; epoch: number; listeners: Set<() => void>; refresh?: () => void; stop?: () => void };
const entries = new Map<string, Entry>();
const identity = () => JSON.stringify([ENGINE_URL, currentEngineIdentity(), engineIdentityGeneration()]);
const keyFor = (sessionId: string) => `${identity()}/${sessionId}`;
export const editableAgentSession = (session: Session) => !!session.agentConfig && session.role !== 'subagent' && session.role !== 'graph_node';
const path = (id: string) => `/api/sessions/${encodeURIComponent(id)}/agent`;
const publish = (entry: Entry, state: Snapshot) => { entry.state = state; entry.listeners.forEach(listener => listener()); };

function entryFor(id: string) {
  const key = keyFor(id);
  let entry = entries.get(key);
  if (!entry) { entry = { state: { loading: true }, epoch: 0, listeners: new Set() }; entries.set(key, entry); }
  return { key, entry };
}

function start(entry: Entry, session: Session, key: string) {
  let active = true, inFlight = false, dirty = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let controller: AbortController | undefined;
  const current = () => active && key === keyFor(session.id);
  async function read() {
    if (!current()) return;
    if (inFlight) { dirty = true; return; }
    inFlight = true; dirty = false;
    const epoch = entry.epoch;
    controller = new AbortController();
    publish(entry, { ...entry.state, loading: true });
    try {
      const result = await request<{ agent: SessionAgent | null }>(path(session.id), 'GET', undefined, 60000, controller.signal);
      if (current() && entry.epoch === epoch) publish(entry, { agent: result.agent, loading: false });
    } catch (error) { if (current() && entry.epoch === epoch) publish(entry, { ...entry.state, loading: false, error }); }
    finally { inFlight = false; if (entry.epoch !== epoch) dirty = true; if (current() && dirty) schedule(); }
  }
  function schedule() { if (!timer && current()) timer = setTimeout(() => { timer = undefined; void read(); }, 250); }
  entry.refresh = schedule;
  const stops = [subscribeScope('global', '', schedule), subscribeScope('conversation', session.id, schedule)];
  if (session.projectId) stops.push(subscribeScope('project', session.projectId, schedule));
  void read();
  entry.stop = () => { active = false; controller?.abort(); clearTimeout(timer); stops.forEach(stop => stop()); entry.refresh = undefined; };
}

const unbound: Snapshot = { loading: false };
const inactiveEntry: Entry = { state: unbound, epoch: 0, listeners: new Set() };
export function useSessionAgent(session: Session) {
  const enabled = editableAgentSession(session);
  const { key, entry } = enabled ? entryFor(session.id) : { key: keyFor(session.id), entry: inactiveEntry };
  const observed = useRef({ key, token: session.agentConfig?.execution.token });
  const subscribe = useCallback((listener: () => void) => {
    if (!enabled) return () => {};
    // StrictMode may resubscribe this same entry after its last-listener cleanup.
    entries.set(key, entry);
    entry.listeners.add(listener);
    if (entry.listeners.size === 1) start(entry, session, key);
    return () => { entry.listeners.delete(listener); if (!entry.listeners.size) { entry.stop?.(); if (entries.get(key) === entry) entries.delete(key); } };
  }, [key, entry, enabled, session.id, session.projectId]);
  const snapshot = useSyncExternalStore(subscribe, () => enabled ? entry.state : unbound);
  useEffect(() => {
    const token = session.agentConfig?.execution.token;
    if (enabled && observed.current.key === key && observed.current.token !== token) entry.refresh?.();
    observed.current = { key, token };
  }, [entry, enabled, key, session.agentConfig?.execution.token]);
  return { ...snapshot, refresh: () => entry.refresh?.() };
}

export function agentExecutionBlocked(session: Session) {
  if (!editableAgentSession(session)) return false;
  const state = entries.get(keyFor(session.id))?.state;
  return state ? !!state.error || !state.agent || !state.agent.execution.allowed : !session.agentConfig!.execution.allowed;
}

export async function patchSessionAgent(session: Session, revision: string, overrides: AgentOverridePatch) {
  const key = keyFor(session.id);
  const result = await request<{ agent: SessionAgent }>(path(session.id), 'PATCH', { revision, overrides }, 60000, AbortSignal.timeout(60000));
  const entry = entries.get(key);
  if (key === keyFor(session.id) && entry) {
    entry.epoch++;
    publish(entry, { agent: result.agent, loading: false });
    // Reconcile any GET that was already resolving before this mutation.
    entry.refresh?.();
  }
  return result.agent;
}
