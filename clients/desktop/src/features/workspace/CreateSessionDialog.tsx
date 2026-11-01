import { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import type { Harness, Project } from '../../engine';
import { bindDraftDiscard, flushDrafts, useEngineDraft } from '../drafts/draftStore';
import { beginLocalActivity } from '../engines/localActivities';
import { AgentPicker } from '../agents/AgentPicker';
import { AgentOverridesEditor, compatibleAgentConfiguration } from '../agents/AgentOverridesEditor';
import type { AgentOverrides, CatalogAgent, SessionCreateInput } from '../agents/types';
import { useModelCatalog } from '../chat/modelCatalog';
import { selectionOf } from '../library/api';
import '../agents/agents.css';

export function CreateSessionDialog({ project, harnesses, topicId, onCreate, onClose }: {
  project: Project; harnesses: Harness[]; topicId?: string;
  onCreate: (input: SessionCreateInput) => Promise<string | null>;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const key = `form.session.new:${project.id}:${topicId ?? ''}`;
  const forgetDrafts = bindDraftDiscard();
  const [harness, setHarness] = useEngineDraft<Harness['id']>(`${key}.harness`, harnesses.find(item => item.available)?.id ?? 'pi');
  const [title, setTitle] = useEngineDraft(`${key}.title`, '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [agent, setAgent] = useEngineDraft<CatalogAgent | null>(`${key}.agent`, null);
  const [overrides, setOverrides] = useEngineDraft<AgentOverrides>(`${key}.agentOverrides`, {});
  const [harnessOverride, setHarnessOverride] = useEngineDraft<Harness['id'] | undefined>(`${key}.harnessOverride`, undefined);
  const [selectingAgent, setSelectingAgent] = useState(false);
  useEffect(() => { dialog.current?.showModal(); }, []);
  const effectiveHarness = agent ? harnessOverride ?? agent.defaultHarness : harness;
  const selected = harnesses.find(item => item.id === effectiveHarness);
  const models = useModelCatalog(project.id, effectiveHarness ?? '', undefined, !!agent && !!effectiveHarness);
  const base = agent && effectiveHarness ? { harness: effectiveHarness, model: agent.model ?? '', effort: agent.effort ?? '', prompt: agent.prompt ?? '', selection: selectionOf({ skills: agent.skills ?? [], mcpConnections: agent.mcpConnections ?? [] }) } : null;
  const valid = !selectingAgent && !!selected?.available && (!agent || !!base && agent.enabled && agent.available && compatibleAgentConfiguration(base, overrides, models.catalog));
  return <dialog ref={dialog} className="settings-dialog add-project-dialog" aria-labelledby="create-session-title" onCancel={event => { if (busy) event.preventDefault(); else onClose(); }} onClose={onClose}>
    <form onSubmit={async event => {
      event.preventDefault();
      if (busy || !valid) return;
      const finished = beginLocalActivity('Creating session');
      setBusy(true); setError('');
      try {
        await flushDrafts();
        const failure = await onCreate({ projectId: project.id, title: title.trim() || 'New session', ...(topicId ? { topicId } : {}),
          ...(agent ? { agent: agent.id, agentSourceUid: agent.source.sourceUid, agentRevision: agent.revision,
            ...(harnessOverride ? { harness: harnessOverride } : {}), ...(Object.keys(overrides).length ? { agentOverrides: overrides } : {}) } : { harness }) });
        if (failure) setError(failure); else { forgetDrafts(key); onClose(); }
      } catch (error) { setError(error instanceof Error ? error.message : 'Could not create the session.'); }
      finally { setBusy(false); finished(); }
    }}>
      <div className="dialog-heading"><h2 id="create-session-title">New session</h2><IconButton label="Cancel creating session" disabled={busy} onClick={onClose}><X /></IconButton></div>
      <label className="project-field" htmlFor="session-title">Session name</label>
      <input id="session-title" autoFocus maxLength={200} placeholder="New session" value={title} onChange={event => setTitle(event.target.value)} disabled={busy} />
      <AgentPicker projectId={project.id} value={agent} disabled={busy} onBusyChange={setSelectingAgent} onChange={next => { setAgent(next); if (!next) { setOverrides({}); setHarnessOverride(undefined); } setError(''); }} />
      {agent && <p className="agent-binding-identity">{agent.model} · {agent.effort || 'Default'}</p>}
      <fieldset className="harness-picker" disabled={busy}><legend className="project-field">Harness{agent ? harnessOverride ? ' · Override' : ' · Following agent' : ''}</legend>{harnesses.map(item => <label key={item.id} className={`harness-option ${item.id === effectiveHarness ? 'is-active' : ''}`}><input type="radio" name="harness" value={item.id} checked={item.id === effectiveHarness} disabled={!item.available} onChange={() => agent ? setHarnessOverride(item.id) : setHarness(item.id)} /><span>{item.name}</span>{!item.available && <small>Not installed</small>}</label>)}</fieldset>
      {agent && <Button size="sm" disabled={busy} onClick={() => setHarnessOverride(undefined)}>Use agent harness</Button>}
      {agent && models.loading && <p role="status">Loading models...</p>}
      {agent && models.error && <p className="form-error" role="alert">{models.error}<Button size="sm" onClick={() => void models.refresh()}>Refresh models</Button></p>}
      {base && models.catalog && !compatibleAgentConfiguration(base, overrides, models.catalog) && <p className="form-error" role="alert">Choose a compatible model and effort in Overrides.</p>}
      {agent && base && <details><summary>Overrides</summary><AgentOverridesEditor base={base} patch={overrides} onChange={next => setOverrides(next as AgentOverrides)} onReset={field => setOverrides(current => { const next = { ...current }; delete next[field]; return next; })} catalog={models.catalog} disabled={busy} /></details>}
      {!selected?.available && <p className="form-error" role="alert">{selected?.error ?? 'No harness is available. Install Pi, OpenCode, or Codex, then restart the engine.'}</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      <div className="dialog-actions"><Button disabled={busy} onClick={() => { forgetDrafts(key); onClose(); }}>Cancel</Button><Button type="submit" variant="primary" disabled={busy || !valid}>{busy ? 'Creating...' : 'Create session'}</Button></div>
    </form>
  </dialog>;
}
