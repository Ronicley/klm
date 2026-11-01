import { useEffect, useId } from 'react';
import { RotateCcw, X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { RadioGroup } from '../../design-system/RadioGroup';
import { SearchSelect } from '../../design-system/SearchSelect';
import { Slider } from '../../design-system/Slider';
import { HarnessIcon } from '../chat/HarnessIcon';
import type { Agent } from '../agents/types';
import { useAgentModels } from '../agents/catalog';
import type { Harness } from '../../engine';
import { effortLabel, resolveAgentNode, type AgentNodeOverrides } from './agent-node';

export function AgentNodePanel({ agents, name, agentId, overrides, onNameChange, onSelectAgent, onOverridesChange, onClose }: {
  agents: Agent[]; name: string; agentId: string; overrides: AgentNodeOverrides;
  onNameChange: (name: string) => void; onSelectAgent: (id: string) => void;
  onOverridesChange: (overrides: AgentNodeOverrides) => void; onClose: () => void;
}) {
  const id = useId();
  const available = agents.filter(agent => agent.enabled);
  const agent = available.find(item => item.id === agentId);
  const { models, loading, error, retry } = useAgentModels(overrides.harness ?? agent?.defaultHarness);
  const settings = agent ? resolveAgentNode(agent, overrides, models) : undefined;
  const hasOverrides = Object.values(overrides).some(value => value !== undefined);
  useEffect(() => { document.getElementById(`${id}-name`)?.focus(); }, [id]);

  return <aside className="graph-agent-panel nodrag nopan nowheel" role="dialog" aria-labelledby={`${id}-title`} onKeyDown={event => {
    if (event.key === 'Escape' && !event.defaultPrevented) { event.stopPropagation(); onClose(); }
  }}>
    <div className="graph-agent-panel-header"><h2 id={`${id}-title`}>AI agent</h2><IconButton label="Close node settings" onClick={onClose}><X /></IconButton></div>
    <div className="graph-agent-panel-field"><label htmlFor={`${id}-name`}>Name</label><Input id={`${id}-name`} maxLength={80} placeholder="Node name" value={name} onChange={event => onNameChange(event.target.value)} /></div>
    <div className="graph-agent-panel-field"><label htmlFor={`${id}-agent`}>Agent</label><SearchSelect id={`${id}-agent`} label="Agent" value={agentId} options={available.map(item => ({ value: item.id, label: item.name, description: item.description }))} onChange={onSelectAgent} placeholder="Select agent" searchPlaceholder="Search agents" emptyMessage="No enabled agents available" /></div>
    {agent && settings && <div className="graph-agent-overrides">
      <div className="graph-agent-overrides-header"><h3>Overrides</h3>{hasOverrides && <Button size="sm" variant="ghost" onClick={() => onOverridesChange({})}><RotateCcw />Reset</Button>}</div>
      <RadioGroup<Harness['id']> label="Harness" value={settings.harness} options={[
        { value: 'opencode', label: 'OpenCode', icon: <HarnessIcon harness="opencode" /> },
        { value: 'codex', label: 'Codex', icon: <HarnessIcon harness="codex" /> },
        { value: 'pi', label: 'Pi', icon: <HarnessIcon harness="pi" /> },
      ]} onChange={harness => onOverridesChange({ harness: harness === agent.defaultHarness ? undefined : harness })} />
      <div className="graph-agent-panel-field"><label htmlFor={`${id}-model`}>Model</label><SearchSelect key={`${agent.id}:${settings.harness}`} id={`${id}-model`} label="Model" value={settings.model?.id ?? ''} options={models.map(model => ({ value: model.id, label: model.name, description: model.provider }))} placeholder={loading ? 'Loading models...' : 'Select model'} searchPlaceholder="Search models" emptyMessage={loading ? 'Loading models...' : 'No models available from connected providers'} onChange={modelId => {
        const model = models.find(item => item.id === modelId);
        if (!model) return;
        const baseEffort = agent.effort ?? '';
        const effort = model.efforts.includes(settings.effort) ? settings.effort : model.efforts.includes(baseEffort) ? baseEffort : model.defaultEffort ?? model.efforts[0] ?? '';
        onOverridesChange({ ...overrides, model: modelId === agent.model ? undefined : modelId, effort: effort === baseEffort ? undefined : effort });
      }} />{!settings.model && !loading && <p className="form-error" role="status">Select a model available in this harness.</p>}{error && <p className="form-error" role="alert">{error} <Button size="sm" onClick={retry}>Retry</Button></p>}</div>
      {settings.model && settings.model.efforts.length > 0 && <Slider label="Effort level" min={0} max={settings.model.efforts.length - 1} value={Math.max(0, settings.model.efforts.indexOf(settings.effort))} valueText={effortLabel(settings.effort)} marks={settings.model.efforts.map((effort, index) => ({ value: index, label: effortLabel(effort) }))} onValueChange={index => {
        const effort = settings.model?.efforts[index];
        if (effort) onOverridesChange({ ...overrides, effort: effort === agent.effort ? undefined : effort });
      }} />}
    </div>}
  </aside>;
}
