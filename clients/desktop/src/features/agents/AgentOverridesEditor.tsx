import { useId } from 'react';
import { Button } from '../../design-system/Button';
import { SearchSelect } from '../../design-system/SearchSelect';
import { Textarea } from '../../design-system/Textarea';
import type { ModelCatalog } from '../../engine';
import { SessionCapabilityPicker } from '../library/SessionCapabilityPicker';
import { selectionKey, selectionOf } from '../library/api';
import type { AgentConfiguration, AgentOverridePatch, AgentOverrides } from './types';

export function overrideConfiguration(base: AgentConfiguration, patch: AgentOverridePatch): AgentConfiguration {
  return { ...base, model: patch.model ?? base.model, effort: patch.effort ?? base.effort, prompt: patch.prompt ?? base.prompt,
    selection: selectionOf({ skills: patch.skills ?? base.selection.skills, mcpConnections: patch.mcpConnections ?? base.selection.mcpConnections }) };
}

export function compatibleAgentConfiguration(base: AgentConfiguration, patch: AgentOverridePatch, catalog?: ModelCatalog | null) {
  if (patch.model === null || patch.effort === null) return true; // Reset is resolved authoritatively on Save.
  if (!catalog) return false;
  const config = overrideConfiguration(base, patch);
  const model = catalog.models.find(model => model.id === (config.model || catalog.defaultModel));
  return !!model && (!config.effort || model.efforts.includes(config.effort));
}

export function AgentOverridesEditor({ base, patch, overridden = [], onChange, onReset, catalog, disabled = false, modelDisabled = disabled, libraryOnly = false }: {
  base: AgentConfiguration; patch: AgentOverridePatch; overridden?: string[]; onChange: (patch: AgentOverridePatch) => void;
  onReset: (field: keyof AgentOverrides) => void; catalog?: ModelCatalog | null; disabled?: boolean; modelDisabled?: boolean; libraryOnly?: boolean;
}) {
  const id = useId();
  const value = overrideConfiguration(base, patch);
  const model = catalog?.models.find(model => model.id === (value.model || catalog.defaultModel));
  function origin(field: keyof AgentOverrides) {
    return Object.hasOwn(patch, field) ? patch[field] === null ? 'Follow agent after save' : 'Override' : overridden.includes(field) ? 'Override' : 'Following agent';
  }
  const reset = (field: keyof AgentOverrides) => <Button size="sm" variant="ghost" disabled={disabled || (field === 'model' || field === 'effort') && modelDisabled} onClick={() => onReset(field)}>Use agent value</Button>;
  return <div className="agent-override-fields">
    {!libraryOnly && <>
      <div className="agent-override-heading"><label htmlFor={`${id}-model`}>Model · {origin('model')}</label>{reset('model')}</div>
      <SearchSelect id={`${id}-model`} label="Model" value={value.model} disabled={modelDisabled} placeholder={value.model || 'Harness default'} options={[{ value: '', label: 'Harness default' }, ...(catalog?.models ?? []).map(model => ({ value: model.id, label: model.name, description: model.providerName }))]} onChange={model => onChange({ ...patch, model })} />
      <div className="agent-override-heading"><label htmlFor={`${id}-effort`}>Effort · {origin('effort')}</label>{reset('effort')}</div>
      <SearchSelect id={`${id}-effort`} label="Effort" value={value.effort} disabled={modelDisabled} placeholder={value.effort || 'Harness default'} options={[{ value: '', label: 'Harness default' }, ...(model?.efforts ?? []).map(effort => ({ value: effort, label: effort === 'xhigh' ? 'Extra high' : effort }))]} onChange={effort => onChange({ ...patch, effort })} />
      {catalog && !compatibleAgentConfiguration(base, patch, catalog) && <p className="form-error" role="status">Choose a compatible model and effort explicitly. Requested: {value.model || 'Harness default'} · {value.effort || 'Harness default'}.</p>}
      <div className="agent-override-heading"><label htmlFor={`${id}-prompt`}>Prompt · {origin('prompt')}</label>{reset('prompt')}</div>
      <Textarea id={`${id}-prompt`} rows={6} disabled={disabled} value={value.prompt} onChange={event => onChange({ ...patch, prompt: event.target.value })} />
    </>}
    <div className="agent-override-heading"><strong>Skills · {origin('skills')}</strong>{reset('skills')}</div>
    <Button size="sm" disabled={disabled} onClick={() => onChange({ ...patch, skills: [] })}>Clear skills</Button>
    <div className="agent-override-heading"><strong>MCPs · {origin('mcpConnections')}</strong>{reset('mcpConnections')}</div>
    <Button size="sm" disabled={disabled} onClick={() => onChange({ ...patch, mcpConnections: [] })}>Clear MCPs</Button>
    <SessionCapabilityPicker value={value.selection} harness={base.harness} general={false} disabled={disabled} onChange={selection => {
      const next = { ...patch };
      if (selectionKey({ skills: selection.skills, mcpConnections: [] }) !== selectionKey({ skills: value.selection.skills, mcpConnections: [] })) next.skills = selection.skills;
      if (selectionKey({ skills: [], mcpConnections: selection.mcpConnections }) !== selectionKey({ skills: [], mcpConnections: value.selection.mcpConnections })) next.mcpConnections = selection.mcpConnections;
      onChange(next);
    }} />
  </div>;
}
