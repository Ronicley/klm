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
import { agentSlug, type Agent } from './demo';
import { AuthoringProject, useAgentModels } from './catalog';
import { CapabilitySelector } from '../library/CapabilitySelector';
import type { MCPSelection } from '../library/api';

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

export function AgentEditorDialog({ agent, agents, onSave, onClose }: {
  agent?: Agent;
  agents: Agent[];
  onSave: (draft: AgentDraft) => void | Promise<void>;
  onClose: () => void;
}) {
  const id = useId();
  const projectId = useContext(AuthoringProject);
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => { dialog.current?.showModal(); }, []);
  const [draft, setDraft] = useState<Omit<AgentDraft, 'defaultHarness'> & { defaultHarness?: Harness['id'] }>(() => ({
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
  const { models, loading, error: catalogError, retry } = useAgentModels(draft.defaultHarness);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');
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

  return <dialog ref={dialog} className="settings-dialog agent-editor-dialog" aria-labelledby={`${id}-title`} onCancel={onClose} onClose={onClose}>
    <form className="agent-editor" onSubmit={async event => {
      event.preventDefault();
      if (!valid || !draft.defaultHarness || saving) return;
      setSaving(true); setSaveError('');
      try { await onSave({ ...draft, effort: efforts.length ? draft.effort : '', defaultHarness: draft.defaultHarness, name: draft.name.trim(), description: draft.description.trim() }); }
      catch (error) { setSaveError(error instanceof Error ? error.message : 'Could not save agent.'); }
      finally { setSaving(false); }
    }}>
      <div className="agent-editor-header">
        <h2 id={`${id}-title`}>{agent ? 'Edit agent' : 'New agent'}</h2>
        <IconButton label="Close agent editor" onClick={onClose}><X /></IconButton>
      </div>
      <div className="agent-editor-grid" inert={saving}>
        <div className="agent-editor-field"><label htmlFor={`${id}-name`}>Name</label><Input id={`${id}-name`} autoFocus required maxLength={80} value={draft.name} aria-invalid={!!nameError} aria-describedby={nameError ? `${id}-name-error` : undefined} onChange={event => setDraft(current => ({ ...current, name: event.target.value }))} />{nameError && <p id={`${id}-name-error`} className="form-error" role="alert">{nameError}</p>}</div>
        <div className="agent-editor-field"><label htmlFor={`${id}-slug`}>Identifier</label><Input id={`${id}-slug`} readOnly value={slug} /></div>
        <div className="agent-editor-field agent-editor-wide"><label htmlFor={`${id}-description`}>Description</label><Input id={`${id}-description`} maxLength={240} value={draft.description} onChange={event => setDraft(current => ({ ...current, description: event.target.value }))} /></div>
        <div className="agent-editor-wide"><RadioGroup<Harness['id']> label="Default harness" value={draft.defaultHarness} options={[
          { value: 'opencode', label: 'OpenCode', icon: <HarnessIcon harness="opencode" /> },
          { value: 'codex', label: 'Codex', icon: <HarnessIcon harness="codex" /> },
          { value: 'pi', label: 'Pi', icon: <HarnessIcon harness="pi" /> },
        ]} onChange={harness => setDraft(current => ({ ...current, defaultHarness: harness, model: '', effort: '' }))} /></div>
        {draft.defaultHarness && <div className="agent-editor-field agent-editor-wide"><label htmlFor={`${id}-model`}>Model</label><SearchSelect key={draft.defaultHarness} id={`${id}-model`} label="Model" value={draft.model} options={models.map(model => ({ value: model.id, label: model.name, description: model.provider }))} onChange={selectModel} placeholder={loading ? 'Loading models...' : 'Select model'} searchPlaceholder="Search models" emptyMessage={loading ? 'Loading models...' : 'No models available from connected providers'} />{catalogError && <p className="form-error" role="alert">{catalogError} <Button size="sm" onClick={retry}>Retry</Button></p>}</div>}
        {!!efforts.length && <div className="agent-editor-wide"><Slider label="Effort level" min={0} max={Math.max(0, efforts.length - 1)} value={Math.max(0, efforts.indexOf(draft.effort))} valueText={effortLabel(draft.effort)} marks={efforts.map((effort, index) => ({ value: index, label: effortLabel(effort) }))} onValueChange={index => setDraft(current => ({ ...current, effort: efforts[index] }))} /></div>}
        <div className="agent-editor-field agent-editor-wide"><label htmlFor={`${id}-prompt`}>Prompt</label><Textarea id={`${id}-prompt`} required rows={6} value={draft.prompt} onChange={event => setDraft(current => ({ ...current, prompt: event.target.value }))} /></div>
        {projectId && <div className="agent-editor-wide"><h3>Library</h3><CapabilitySelector harness={draft.defaultHarness} value={{ skills: draft.skills, mcpConnections: draft.mcpConnections }} onChange={selection => setDraft(current => ({ ...current, ...selection }))} /></div>}
      </div>
      {saveError && <p className="form-error" role="alert">{saveError}</p>}
      <div className="agent-editor-actions"><Button disabled={saving} onClick={onClose}>Cancel</Button><Button type="submit" variant="primary" disabled={!valid || saving}>{saving ? 'Saving...' : agent ? 'Save changes' : 'Create agent'}</Button></div>
    </form>
  </dialog>;
}
