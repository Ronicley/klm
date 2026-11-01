import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { request, type Session, type SessionResponse } from '../../engine';
import { useModelCatalog } from '../chat/modelCatalog';
import { AgentOverridesEditor, compatibleAgentConfiguration } from './AgentOverridesEditor';
import { AgentProblemMessage } from './AgentProblemMessage';
import { patchSessionAgent, useSessionAgent } from './binding';
import { agentOrigin, type AgentOverridePatch, type SessionAgent } from './types';
import { selectionOf } from '../library/api';
import { bindDraftDiscard, flushDrafts, useEngineDraft } from '../drafts/draftStore';
import './agents.css';

export function SessionAgentDialog({ session, onClose, onSnapshot, libraryOnly = false }: {
  session: Session; onClose: () => void; onSnapshot?: (value: SessionResponse) => void; libraryOnly?: boolean;
}) {
  const live = useSessionAgent(session);
  const key = `form.sessionAgent:${session.id}:${libraryOnly ? 'library' : 'settings'}`;
  const forgetDrafts = bindDraftDiscard();
  const [base, setBase] = useEngineDraft<SessionAgent | undefined>(`${key}.base`, undefined);
  const [patch, setPatch] = useEngineDraft<AgentOverridePatch>(`${key}.patch`, {});
  const [error, setError] = useState<unknown>();
  const [saving, setSaving] = useState(false);
  const [discard, setDiscard] = useState<'close' | 'reload' | null>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  const active = useRef(true);
  useEffect(() => { dialog.current?.showModal(); active.current = true; return () => { active.current = false; }; }, []);
  useEffect(() => { if (!base && live.agent) setBase(live.agent); }, [base, live.agent]);
  const { catalog, loading, error: modelError, refresh } = useModelCatalog(session.projectId, session.harness, session.id, !libraryOnly);
  const dirty = Object.keys(patch).length > 0;
  const stale = !!base && !!live.agent && (base.revision !== live.agent.revision || base.execution.token !== live.agent.execution.token || base.sourceStatus !== live.agent.sourceStatus);
  const locked = session.automation?.status === 'running';
  const modelLocked = session.status === 'running' || locked;
  const resetFields = base?.overriddenFields.filter(field => !libraryOnly || field === 'skills' || field === 'mcpConnections') ?? [];
  const compatible = !!base && (libraryOnly || modelLocked && !Object.hasOwn(patch, 'model') && !Object.hasOwn(patch, 'effort') || compatibleAgentConfiguration(base.desired, patch, catalog));
  function close() { if (!saving) { if (dirty) setDiscard('close'); else { forgetDrafts(key); onClose(); } } }
  function reload() {
    setPatch({}); setError(undefined); setDiscard(null);
    setBase(live.agent ?? undefined); live.refresh();
  }
  return createPortal(<dialog ref={dialog} className="settings-dialog agent-editor-dialog" aria-labelledby="session-agent-title" onCancel={event => { event.preventDefault(); event.stopPropagation(); close(); }} onClose={event => { event.stopPropagation(); onClose(); }}>
    <form className="agent-editor" onSubmit={async event => {
      event.preventDefault(); event.stopPropagation(); if (!base || !dirty || saving || locked || stale || !compatible) return;
      setSaving(true); setError(undefined);
      try {
        await flushDrafts();
        const next = await patchSessionAgent(session, base.revision, patch);
        if (!active.current) return;
        setBase(next); setPatch({});
        if (onSnapshot) { try { onSnapshot(await request<SessionResponse>(`/api/sessions/${encodeURIComponent(session.id)}`)); } catch { /* Shared binding cache already contains the saved result; SSE reconciles session metadata. */ } }
      } catch (failure) { if (active.current) setError(failure); }
      finally { if (active.current) setSaving(false); }
    }}>
      <div className="agent-editor-header"><h2 id="session-agent-title">{libraryOnly ? 'Session Library' : 'Agent settings'}</h2><IconButton label="Close agent settings" disabled={saving} onClick={close}><X /></IconButton></div>
      {live.agent && <div className="agent-binding-identity"><strong>{live.agent.name}</strong><span>{agentOrigin(live.agent.source)} · {live.agent.source.id}</span><span>Harness: {session.harness} · {live.agent.sourceStatus.replaceAll('_', ' ')}</span><span>{live.agent.pending ? 'Changes next turn' : 'Configuration up to date'}{live.agent.overriddenFields.length ? ' · Overrides' : ' · Following agent'}</span>{['deleted', 'disabled'].includes(live.agent.sourceStatus) && <span>Saved configuration</span>}</div>}
      {live.agent && !live.agent.execution.allowed && <p className="form-error" role="alert">{live.agent.execution.message || 'Agent settings need attention.'}</p>}
      <AgentProblemMessage error={error || live.error} />
      {(!base || live.loading) && <p role="status">{base ? 'Refreshing agent settings...' : 'Loading agent settings...'}</p>}
      {(stale || !!live.error) && <div><p role="status">{stale ? 'Agent settings changed. Reload before saving.' : 'Agent settings are unavailable.'}</p><Button disabled={saving} onClick={() => dirty ? setDiscard('reload') : reload()}>Reload agent settings</Button></div>}
      {locked && <p role="status">Read-only during this automation run.</p>}
      {!!resetFields.length && <Button size="sm" disabled={saving || locked || modelLocked && resetFields.some(field => field === 'model' || field === 'effort')} onClick={() => setPatch(Object.fromEntries(resetFields.map(field => [field, null])))}>Reset overrides</Button>}
      {base && <AgentOverridesEditor base={base.desired} patch={patch} overridden={base.overriddenFields} onChange={setPatch} onReset={field => setPatch(old => ({ ...old, [field]: null }))} catalog={catalog} disabled={saving || locked} modelDisabled={saving || modelLocked} libraryOnly={libraryOnly} />}
      {!libraryOnly && modelError && <p className="form-error" role="alert">{modelError}<Button size="sm" onClick={() => void refresh()}>Refresh models</Button></p>}
      {live.agent?.applied && <details><summary>Applied configuration</summary><p>{live.agent.applied.config.harness} · {live.agent.applied.config.model} · {live.agent.applied.config.effort || 'Default'}</p>{!libraryOnly && <pre className="agent-config-prompt">{live.agent.applied.config.prompt}</pre>}<pre className="agent-config-prompt">{JSON.stringify(live.agent.applied.config.selection, null, 2)}</pre></details>}
      {live.agent && (selectionOf(live.agent.inheritedCapabilities).skills.length > 0 || selectionOf(live.agent.inheritedCapabilities).mcpConnections.length > 0) && <details><summary>Captured parent Library contribution</summary><pre className="agent-config-prompt">{JSON.stringify(live.agent.inheritedCapabilities, null, 2)}</pre></details>}
      {discard ? <div role="alert"><p>Discard unsaved changes?</p><Button onClick={() => setDiscard(null)}>Keep editing</Button><Button variant="danger-soft" onClick={() => { forgetDrafts(key); if (discard === 'close') onClose(); else reload(); }}>Discard changes</Button></div>
        : <div className="agent-editor-actions"><Button disabled={saving} onClick={close}>Close</Button><Button type="submit" variant="primary" disabled={!base || !dirty || saving || locked || stale || !!live.error || !compatible || !libraryOnly && loading}>Save changes</Button></div>}
    </form>
  </dialog>, document.body);
}
