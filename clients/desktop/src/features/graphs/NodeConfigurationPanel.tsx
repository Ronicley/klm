import { useEffect, useId, useRef, type ReactNode } from 'react';
import type { Edge } from '@xyflow/react';
import { X } from 'lucide-react';
import { IconButton } from '../../design-system/Button';
import { Badge } from '../../design-system/Badge';
import { HarnessIcon } from '../chat/HarnessIcon';
import { effortLabel, harnessNames } from './agent-node';
import { choiceOutputFields, type ChoiceOutputField } from './choice';
import type { CanvasNode } from './GraphCanvas';
import { sourcePort } from './connections';

function Field({ label, children }: { label: string; children: ReactNode }) {
  return <div className="graph-config-field"><dt>{label}</dt><dd>{children || '—'}</dd></div>;
}

function OutputFields({ fields }: { fields: ChoiceOutputField[] }) {
  return <section className="graph-config-section"><h3>Output payload</h3>
    {fields.length ? <dl className="graph-config-fields">{fields.map(field => <Field key={field.key} label={field.name}><code>{field.value || '""'}</code></Field>)}</dl> : <p>None</p>}
  </section>;
}

function nodeName(node?: CanvasNode) {
  if (!node) return 'Not connected';
  return node.data.name || node.data.choice?.name || node.data.fork?.name || node.data.terminal?.name || (node.type === 'join' ? `Join · ${node.data.agent?.name ?? 'Integration'}` : 'Unnamed node');
}

const nodeTitles: Record<string, string> = { aiAgent: 'AI agent', choice: 'Choice', fork: 'Fork', join: 'Join', terminal: 'Terminal command' };

export function NodeConfigurationPanel({ node, nodes, edges, onClose }: {
  node: CanvasNode; nodes: CanvasNode[]; edges: Edge[]; onClose: () => void;
}) {
  const id = useId();
  const panel = useRef<HTMLElement>(null);
  const { data } = node;
  const { agent, settings, choice, fork, join, terminal } = data;
  const destination = nodes.find(item => item.id === edges.find(edge => edge.source === node.id)?.target);
  const supportsSession = destination?.type === 'aiAgent' || destination?.type === 'join';
  const title = nodeTitles[node.type ?? ''] ?? 'Node';
  useEffect(() => { panel.current?.focus(); }, [node.id]);

  return <aside ref={panel} className="graph-agent-panel graph-config-panel nodrag nopan nowheel" role="dialog" aria-labelledby={`${id}-title`} tabIndex={-1}>
    <div className="graph-agent-panel-header"><h2 id={`${id}-title`}>{title} configuration</h2>{data.initial && <Badge>Start</Badge>}<IconButton label="Close node configuration" onClick={onClose}><X /></IconButton></div>
    {(node.type === 'aiAgent' || node.type === 'join') && <>
      <dl className="graph-config-fields">
        {node.type === 'aiAgent' && <Field label="Name">{data.name}</Field>}
        <Field label="Agent">{agent?.name ?? data.agentId ?? join?.agentId}{agent?.description && <p className="graph-config-description">{agent.description}</p>}</Field>
      </dl>
      {settings && <section className="graph-config-section"><h3>Execution</h3><dl className="graph-config-fields">
        <Field label="Harness"><span className="graph-config-harness"><HarnessIcon harness={settings.harness} />{harnessNames[settings.harness]}</span><small>{data.overrides?.harness !== undefined ? 'Override' : 'Inherited'}</small></Field>
        <Field label="Model">{settings.model?.name ?? data.overrides?.model ?? agent?.model ?? '—'}<small>{data.overrides?.model !== undefined ? 'Override' : 'Inherited'}</small></Field>
        <Field label="Effort level">{effortLabel(settings.effort) || '—'}<small>{data.overrides?.effort !== undefined ? 'Override' : 'Inherited'}</small></Field>
      </dl></section>}
      {join && <dl className="graph-config-fields"><Field label="Additional prompt">{join.prompt}</Field><Field label="Output Git branch">{join.outputBranch && <code>{join.outputBranch}</code>}</Field></dl>}
    </>}
    {choice && <>
      <dl className="graph-config-fields"><Field label="Name">{choice.name}</Field><Field label="Description">{choice.description}</Field><Field label="Outcome">{choice.terminal ? 'End graph' : 'Continue'}</Field></dl>
      <section className="graph-config-section"><h3>Input payload</h3>
        {choice.fields.length ? <ul className="graph-config-input-fields">{choice.fields.map(field => <li key={field.key}><code>{field.name}</code><span>{field.type}</span><span>{field.required ? 'Required' : 'Optional'}</span></li>)}</ul> : <p>None</p>}
      </section>
      <OutputFields fields={choiceOutputFields(choice)} />
      {!choice.terminal && <>
        <dl className="graph-config-fields"><Field label="Destination">{nodeName(destination)}</Field>{supportsSession && <Field label="Session policy">{choice.sessionPolicy === 'continue_target' ? 'Continue target' : 'New session'}</Field>}</dl>
      </>}
    </>}
    {fork && <>
      <dl className="graph-config-fields"><Field label="Name">{fork.name}</Field></dl>
      <section className="graph-config-section"><h3>Branches</h3>{fork.branches.map((branch, index) => {
        const target = nodes.find(item => item.id === edges.find(edge => edge.source === node.id && sourcePort(edge, nodes) === branch.id)?.target);
        return <details className="graph-config-branch" key={branch.id} open={index === 0}><summary>{branch.name}</summary><div>
          <dl className="graph-config-fields"><Field label="Destination">{nodeName(target)}</Field><Field label="Workspace">{branch.separateWorktree === false ? 'Shared workspace' : 'Separate worktree'}</Field><Field label="Git branch name"><code>{branch.gitBranch || '—'}</code></Field>{branch.separateWorktree !== false && <Field label="Base revision">Incoming revision</Field>}</dl>
          <OutputFields fields={branch.outputFields} />
        </div></details>;
      })}</section>
    </>}
    {terminal && <><dl className="graph-config-fields"><Field label="Name">{terminal.name}</Field><Field label="Command"><pre>{terminal.command || '—'}</pre></Field><Field label="Destination">{nodeName(destination)}</Field></dl><OutputFields fields={terminal.outputFields ?? []} /></>}
  </aside>;
}
