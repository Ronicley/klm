import { useContext, useEffect, useId, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { RadioGroup } from '../../design-system/RadioGroup';
import { SearchSelect } from '../../design-system/SearchSelect';
import { Slider } from '../../design-system/Slider';
import { Textarea } from '../../design-system/Textarea';
import { HarnessIcon } from '../chat/HarnessIcon';
import type { Harness } from '../../engine';
import { agentOrigin, agentSlug, type Agent, type AgentAuthoringScope } from './types';
import { AuthoringProject, useAgentModels } from './catalog';
import { CapabilitySelector } from '../library/CapabilitySelector';
import type { MCPSelection } from '../library/api';
import { bindDraftDiscard, flushDrafts, useEngineDraft } from '../drafts/draftStore';
import { beginLocalActivity } from '../engines/localActivities';
import { getAgent } from './api';
import { AgentProblemMessage } from './AgentProblemMessage';

type AgentDraft = {
  skills: string[];
  mcpConnections: MCPSelection[];
  name: string;
  description: string;
  defaultHarness: Harness['id'];
  model: string;
  effort: string;
  prompt: string;
};

function effortLabel(value: string) {
  return value === 'xhigh' ? 'Extra high' : value ? value[0].toUpperCase() + value.slice(1) : '';
}

export function AgentEditorDialog({ agent, agents, projectId: managedProject, managed = false, onSave, onClose, onReload }: {
  agent?: Agent;
  agents: Agent[];
  projectId?: string;
  managed?: boolean;
  onSave: (draft: AgentDraft, scope: 'global' | 'project', revision?: string) => void | Promise<void>;
  onClose: () => void;
  onReload?: () => Promise<void>;
}) {
  const id = useId();
  const contextProject = useContext(AuthoringProject);
  const projectId = managedProject ?? contextProject;
  const key = `form.agent:${projectId ?? ''}:${agent?.id ?? 'new'}`;
  const [scope, setScope] = useEngineDraft<'global' | 'project'>(`${key}.scope`, agent?.source?.scope ?? (projectId ? 'project' : 'global'));
  const modelScope: AgentAuthoringScope | undefined = agent?.source
    ? agent.source.scope === 'global' ? { kind: 'global' } : { kind: 'project', projectId: agent.source.projectId! }
    : managed ? scope === 'global' ? { kind: 'global' } : { kind: 'project', projectId: projectId! } : undefined;
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => { dialog.current?.showModal(); }, []);
  const forgetDrafts = bindDraftDiscard();
  const [revision] = useEngineDraft(`${key}.revision`, agent?.revision ?? '', true);
  const [draft, setDraft] = useEngineDraft<Omit<AgentDraft, 'defaultHarness'> & { defaultHarness?: Harness['id'] }>(`${key}.values`, () => ({
    name: agent?.name ?? '',
    description: agent?.description ?? '',
    defaultHarness: agent?.defaultHarness,
    model: agent?.model ?? '',
    effort: agent?.effort ?? '',
    prompt: agent?.prompt ?? '',
    skills: agent?.skills ?? [],
    mcpConnections: agent?.mcpConnections ?? [],
  }));
  const slug = agentSlug(draft.name);
  const duplicate = !!slug && agents.some(item => item.id !== agent?.id && (item.id === slug || agentSlug(item.name) === slug));
  const nameError = duplicate ? 'An agent with this identifier already exists.' : draft.name.trim() && !slug ? 'Use at least one letter or number in the name.' : '';
  const { models, loading, error: catalogError, retry } = useAgentModels(draft.defaultHarness, modelScope);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>();
  const [references, setReferences] = useState(agent?.references ?? []);
  const [referencesMore, setReferencesMore] = useState(!!agent?.referencesHasMore);
  const [referencesOffset, setReferencesOffset] = useState(agent?.referencesNextOffset ?? 0);
  const [referencesBusy, setReferencesBusy] = useState(false);
  const referencesController = useRef<AbortController | null>(null);
  useEffect(() => () => referencesController.current?.abort(), []);
  const selectedModel = models.find(model => model.id === draft.model);
  const efforts = selectedModel?.efforts ?? [];
  const valid = !!slug && !nameError && !!draft.prompt.trim() && !!selectedModel && (!efforts.length || efforts.includes(draft.effort));

  function selectModel(modelName: string) {
    const model = models.find(item => item.id === modelName);
    if (!model) return;
    setDraft(current => ({ ...current, model: model.id,
      effort: model.efforts.includes(current.effort) ? current.effort : model.defaultEffort ?? model.efforts[0] ?? '',
    }));
  }

  return <dialog ref={dialog} className="settings-dialog agent-editor-dialog" aria-labelledby={`${id}-title`} onCancel={event => { event.stopPropagation(); if (saving) event.preventDefault(); else onClose(); }} onClose={event => { event.stopPropagation(); onClose(); }}>
    <form className="agent-editor" onSubmit={async event => {
      event.preventDefault();
      if (!valid || !draft.defaultHarness || saving) return;
      const finished = beginLocalActivity('Saving agent');
      setSaving(true); setSaveError(undefined);
      try { await flushDrafts(); await onSave({ ...draft, effort: efforts.length ? draft.effort : '', defaultHarness: draft.defaultHarness, name: draft.name.trim(), description: draft.description.trim() }, scope, revision); forgetDrafts(key); }
      catch (error) { setSaveError(error); }
      finally { setSaving(false); finished(); }
    }}>
      <div className="agent-editor-header">
        <h2 id={`${id}-title`}>{agent ? 'Edit agent' : 'New agent'}</h2>
        <IconButton label="Close agent editor" disabled={saving} onClick={onClose}><X /></IconButton>
      </div>
      <div className="agent-editor-grid" inert={saving}>
        {agent?.source ? <div className="agent-editor-wide agent-editor-field"><label>Scope</label><p>{agentOrigin(agent.source)}</p></div>
          : managed && projectId ? <div className="agent-editor-wide"><RadioGroup<'global' | 'project'> label="Scope" value={scope} options={[{ value: 'project', label: 'This project' }, { value: 'global', label: 'Global' }]} onChange={setScope} /></div>
          : managed ? <div className="agent-editor-wide agent-editor-field"><label>Scope</label><p>Global</p></div> : null}
        <div className="agent-editor-field"><label htmlFor={`${id}-name`}>Name</label><Input id={`${id}-name`} autoFocus required maxLength={80} value={draft.name} aria-invalid={!!nameError} aria-describedby={nameError ? `${id}-name-error` : undefined} onChange={event => setDraft(current => ({ ...current, name: event.target.value }))} />{nameError && <p id={`${id}-name-error`} className="form-error" role="alert">{nameError}</p>}</div>
        <div className="agent-editor-field"><label htmlFor={`${id}-slug`}>Identifier</label><Input id={`${id}-slug`} readOnly value={slug} /></div>
        <div className="agent-editor-field agent-editor-wide"><label htmlFor={`${id}-description`}>Description</label><Input id={`${id}-description`} maxLength={240} value={draft.description} onChange={event => setDraft(current => ({ ...current, description: event.target.value }))} /></div>
        <div className="agent-editor-wide"><RadioGroup<Harness['id']> label="Default harness" value={draft.defaultHarness} options={[
          { value: 'opencode', label: 'OpenCode', icon: <HarnessIcon harness="opencode" /> },
          { value: 'codex', label: 'Codex', icon: <HarnessIcon harness="codex" /> },
          { value: 'pi', label: 'Pi', icon: <HarnessIcon harness="pi" /> },
        ]} onChange={harness => setDraft(current => ({ ...current, defaultHarness: harness, model: '', effort: '' }))} /></div>
        {draft.defaultHarness && <div className="agent-editor-field agent-editor-wide"><label htmlFor={`${id}-model`}>Model</label><SearchSelect key={draft.defaultHarness} id={`${id}-model`} label="Model" value={draft.model} options={models.map(model => ({ value: model.id, label: model.name, description: model.provider }))} onChange={selectModel} placeholder={loading ? 'Loading models...' : 'Select model'} searchPlaceholder="Search models" emptyMessage={loading ? 'Loading models...' : 'No models available from connected providers'} />{catalogError && <p className="form-error" role="alert">{catalogError} <Button size="sm" onClick={retry}>Retry</Button></p>}</div>}
        {!!draft.model && !loading && !selectedModel && <p className="form-error agent-editor-wide" role="status">Model {draft.model} is unavailable in this catalog.</p>}
        {!!efforts.length && <div className="agent-editor-wide"><Slider label="Effort level" min={0} max={Math.max(0, efforts.length - 1)} value={Math.max(0, efforts.indexOf(draft.effort))} valueText={effortLabel(draft.effort)} marks={efforts.map((effort, index) => ({ value: index, label: effortLabel(effort) }))} onValueChange={index => setDraft(current => ({ ...current, effort: efforts[index] }))} /></div>}
        <div className="agent-editor-field agent-editor-wide"><label htmlFor={`${id}-prompt`}>Prompt</label><Textarea id={`${id}-prompt`} required rows={6} value={draft.prompt} onChange={event => setDraft(current => ({ ...current, prompt: event.target.value }))} /></div>
        {(managed || projectId) && <div className="agent-editor-wide"><h3>Library</h3><CapabilitySelector harness={draft.defaultHarness} value={{ skills: draft.skills, mcpConnections: draft.mcpConnections }} onChange={selection => setDraft(current => ({ ...current, ...selection }))} /></div>}
        {!!references.length && <div className="agent-editor-wide"><h3>Graph references</h3><ul className="agent-graph-references">{references.map(reference => <li key={`${reference.projectId}/${reference.id}`}>{reference.name} · {reference.projectName}</li>)}</ul></div>}
        {referencesMore && agent?.source && <div className="agent-editor-wide"><Button size="sm" disabled={referencesBusy} onClick={() => {
          const controller = new AbortController(); referencesController.current = controller;
          setReferencesBusy(true); setSaveError(undefined);
          void getAgent(agent.source!, referencesOffset, controller.signal).then(next => {
            if (controller.signal.aborted) return;
            if (next.revision !== agent.revision) throw new Error('Agent changed. Discard changes and reload before reading more references.');
            setReferences(current => [...current, ...(next.references ?? []).filter(item => !current.some(ref => ref.projectId === item.projectId && ref.id === item.id))]);
            setReferencesMore(next.referencesHasMore); setReferencesOffset(next.referencesNextOffset ?? 0);
          }).catch(error => { if (!controller.signal.aborted) setSaveError(error); }).finally(() => { if (!controller.signal.aborted) setReferencesBusy(false); });
        }}>{referencesBusy ? 'Loading references...' : 'More references'}</Button></div>}
      </div>
      <AgentProblemMessage error={saveError} />
      {!!saveError && onReload && <Button disabled={saving} onClick={() => { setSaving(true); forgetDrafts(key); void onReload().catch(setSaveError).finally(() => setSaving(false)); }}>Discard changes and reload</Button>}
      <div className="agent-editor-actions"><Button disabled={saving} onClick={() => { forgetDrafts(key); onClose(); }}>Cancel</Button><Button type="submit" variant="primary" disabled={!valid || saving}>{saving ? 'Saving...' : agent ? 'Save changes' : 'Create agent'}</Button></div>
    </form>
  </dialog>;
}
