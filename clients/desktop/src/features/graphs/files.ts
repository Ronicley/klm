import type { Edge, Viewport, XYPosition } from '@xyflow/react';
import type { Agent } from '../agents/types';
import { agentSlug } from '../agents/types';
import type { CanvasNode } from './GraphCanvas';
import type { AgentNodeOverrides } from './agent-node';
import { choiceOutputFields } from './choice';
import { connectionKey, isFixedHandle, sourcePort, type EdgeBinding, type FixedHandle } from './connections';

export type AgentFile = Agent & { revision: string };
export type FileBranch = { name: string; git_branch: string; separate_worktree?: boolean; output: Record<string, string>; to: string };
export type FileNode = {
  type: 'agent' | 'terminal' | 'fork' | 'join'; name?: string; agent?: string;
  overrides?: AgentNodeOverrides; choices?: string[]; command?: string; to?: string;
  branches?: Record<string, FileBranch>; prompt?: string; output_branch?: string; output?: Record<string, string>;
};
export type FileChoice = {
  name: string; description?: string; input: Record<string, { type: 'string'; required: boolean }>;
  output: Record<string, string>; to?: string; session?: 'new' | 'continue_target'; terminal?: boolean;
};
export type GraphDefinition = {
  name: string; description: string; enabled: boolean; initial_node: string;
  nodes: Record<string, FileNode>; choices: Record<string, FileChoice>;
};
export type GraphLayout = {
  positions: Record<string, XYPosition>; viewport?: Viewport;
  handles?: Record<string, FixedHandle[]>;
  connections?: Record<string, EdgeBinding>;
};
export type GraphFile = { id: string; revision: string; definition: GraphDefinition; layout: GraphLayout };
export type GraphRecord = GraphFile;
export type AuthoringCatalog = { agents: AgentFile[]; graphs: GraphFile[]; errors: string[] };
export const authoringPath = (projectId: string) => `/api/projects/${encodeURIComponent(projectId)}`;

export function graphViewport(layout: GraphLayout | undefined) {
  const v = layout?.viewport;
  return v && Number.isFinite(v.x) && Number.isFinite(v.y) && Number.isFinite(v.zoom) && v.zoom > 0 ? v : undefined;
}

export function graphDrawing(graph: GraphFile): { nodes: CanvasNode[]; edges: Edge[] } {
  const nodes: CanvasNode[] = [];
  const edges: Edge[] = [];
  const position = (id: string) => {
    const saved = graph.layout?.positions?.[id];
    return saved && Number.isFinite(saved.x) && Number.isFinite(saved.y) ? saved : { x: (nodes.length % 4) * 360, y: Math.floor(nodes.length / 4) * 300 };
  };
  const edge = (source: string, target: string, sourceHandle: string, targetHandle: string) => {
    const key = connectionKey(source, sourceHandle, target);
    const binding = graph.layout?.connections?.[key];
    const boundHandle = (nodeId: string, id: string | undefined, type: 'source' | 'target', fallback: string) =>
      graph.layout?.handles?.[nodeId]?.some(h => isFixedHandle(h) && h.id === id && h.type === type) ? id! : fallback;
    edges.push({ id: key, source, target, data: { sourcePort: sourceHandle },
      sourceHandle: boundHandle(source, binding?.sourceHandle, 'source', sourceHandle),
      targetHandle: boundHandle(target, binding?.targetHandle, 'target', targetHandle) });
  };
  const inputHandle = (target: string) => graph.definition.nodes[target]?.type === 'join' ? 'branches' : 'input';
  for (const [id, n] of Object.entries(graph.definition.nodes).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0)) {
    const data: CanvasNode['data'] = { initial: id === graph.definition.initial_node };
    if (n.type === 'agent') Object.assign(data, { name: n.name, agentId: n.agent, overrides: n.overrides ?? {} });
    if (n.type === 'terminal') data.terminal = { name: n.name ?? '', command: n.command ?? '', outputFields: Object.entries(n.output ?? {}).map(([name, value]) => ({ key: name, name, value })) };
    if (n.type === 'join') data.join = { agentId: n.agent ?? '', prompt: n.prompt ?? '', outputBranch: n.output_branch ?? '' };
    if (n.type === 'fork') data.fork = { name: n.name ?? '', branches: Object.entries(n.branches ?? {}).map(([branchID, b]) => ({ id: branchID, name: b.name, gitBranch: b.git_branch ?? '', separateWorktree: b.separate_worktree ?? true, outputFields: Object.entries(b.output ?? {}).map(([name, value]) => ({ key: name, name, value })) })) };
    nodes.push({ id, type: n.type === 'agent' ? 'aiAgent' : n.type, position: position(id), data, selectable: false, deletable: false });
    for (const choice of n.choices ?? []) edge(id, `choice:${choice}`, 'choices', 'sources');
    if (n.to) edge(id, n.to, 'output', inputHandle(n.to));
    for (const [branchID, b] of Object.entries(n.branches ?? {})) if (b.to) edge(id, b.to, branchID, inputHandle(b.to));
  }
  for (const [id, c] of Object.entries(graph.definition.choices).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0)) {
    const canvasID = `choice:${id}`;
    nodes.push({ id: canvasID, type: 'choice', position: position(canvasID), selectable: false, deletable: false, data: { choice: {
      id, name: c.name, description: c.description ?? '', terminal: !!c.terminal, sessionPolicy: c.session,
      fields: Object.entries(c.input ?? {}).map(([name, f]) => ({ key: name, name, ...f })),
      outputFields: Object.entries(c.output ?? {}).map(([name, value]) => ({ key: name, name, value })),
    } } });
    if (!c.terminal && c.to) edge(canvasID, c.to, 'destination', inputHandle(c.to));
  }
  for (const node of nodes) {
    const used = new Set(edges.flatMap(e => [e.source === node.id ? e.sourceHandle : null, e.target === node.id ? e.targetHandle : null]));
    node.data.fixedHandles = graph.layout?.handles?.[node.id]?.filter(h => isFixedHandle(h) && used.has(h.id)) ?? [];
  }
  return { nodes, edges };
}

export function canvasLayout(nodes: CanvasNode[], viewport?: Viewport, edges: Edge[] = []): GraphLayout {
  const canonical = (id: string) => {
    const choice = nodes.find(n => n.id === id)?.data.choice;
    return choice?.id ? `choice:${choice.id}` : id;
  };
  const handles: Record<string, FixedHandle[]> = {};
  for (const node of nodes) {
    const used = new Set(edges.flatMap(e => [e.source === node.id ? e.sourceHandle : null, e.target === node.id ? e.targetHandle : null]));
    const fixed = node.data.fixedHandles?.filter(h => used.has(h.id)) ?? [];
    if (fixed.length) handles[canonical(node.id)] = fixed;
  }
  const connections: Record<string, EdgeBinding> = {};
  for (const edge of edges) {
    if (edge.sourceHandle && edge.targetHandle && (handles[canonical(edge.source)]?.some(h => h.id === edge.sourceHandle)
      || handles[canonical(edge.target)]?.some(h => h.id === edge.targetHandle))) {
      connections[connectionKey(canonical(edge.source), sourcePort(edge, nodes), canonical(edge.target))] = { sourceHandle: edge.sourceHandle, targetHandle: edge.targetHandle };
    }
  }
  return { positions: Object.fromEntries(nodes.filter(n => n.type !== 'initialPlaceholder').map(n => [canonical(n.id), n.position])), viewport, handles, connections };
}

export function canvasDefinition(meta: GraphDefinition, nodes: CanvasNode[], edges: Edge[]): GraphDefinition {
  const result: GraphDefinition = { ...meta, nodes: {}, choices: {}, initial_node: nodes.find(n => n.data.initial)?.id ?? '' };
  const destination = (id: string, handle?: string) => edges.find(e => e.source === id && (!handle || sourcePort(e, nodes) === handle))?.target ?? '';
  for (const n of nodes) {
    if (n.type === 'initialPlaceholder') continue;
    const d = n.data;
    if (n.type === 'choice' && d.choice) {
      const c = d.choice;
      if (!c.id || result.choices[c.id]) throw new Error('Apply a unique name to each Choice before saving.');
      const to = destination(n.id);
      result.choices[c.id] = { name: c.name, description: c.description,
        input: Object.fromEntries(c.fields.map(f => [f.name, { type: f.type, required: f.required }])),
        output: Object.fromEntries(choiceOutputFields(c).map(f => [f.name, f.value])),
        ...(c.terminal ? { terminal: true } : { to, session: c.sessionPolicy }),
      };
      continue;
    }
    const choices = edges.filter(e => e.source === n.id).map(e => nodes.find(target => target.id === e.target)?.data.choice?.id).filter((id): id is string => !!id);
    if (n.type === 'aiAgent') result.nodes[n.id] = { type: 'agent', name: d.name?.trim(), agent: d.agentId, choices, overrides: Object.values(d.overrides ?? {}).some(v => v !== undefined) ? d.overrides : undefined };
    if (n.type === 'terminal') result.nodes[n.id] = { type: 'terminal', name: d.terminal?.name, command: d.terminal?.command, output: Object.fromEntries((d.terminal?.outputFields ?? []).map(f => [f.name, f.value])), to: destination(n.id) };
    if (n.type === 'join') result.nodes[n.id] = { type: 'join', agent: d.join?.agentId, prompt: d.join?.prompt, output_branch: d.join?.outputBranch, choices };
    if (n.type === 'fork') result.nodes[n.id] = { type: 'fork', name: d.fork?.name, branches: Object.fromEntries((d.fork?.branches ?? []).map(b => [b.id, { name: b.name, git_branch: b.gitBranch, separate_worktree: b.separateWorktree ?? true, to: destination(n.id, b.id), output: Object.fromEntries(b.outputFields.map(f => [f.name, f.value])) }])) };
  }
  return result;
}

export const graphListEntry = (g: GraphFile) => ({ id: g.id, name: g.definition.name, description: g.definition.description, enabled: g.definition.enabled });
export const graphNameTaken = (name: string, graphs: GraphFile[], id?: string) => graphs.some(g => g.id !== id && agentSlug(g.definition.name) === agentSlug(name));
