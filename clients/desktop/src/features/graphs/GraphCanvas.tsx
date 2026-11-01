import { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { applyEdgeChanges, applyNodeChanges, Background, ConnectionLineType, Handle, MarkerType, Panel, Position, ReactFlow, useOnViewportChange, useReactFlow, useUpdateNodeInternals, type Connection, type Edge, type EdgeChange, type Node, type NodeChange, type NodeProps, type ReactFlowInstance, type XYPosition } from '@xyflow/react';
import { ArrowLeft, Bot, Diamond, GitFork, GitMerge, Maximize, Minus, Plus, Save, Settings2, Terminal, Trash2 } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Badge } from '../../design-system/Badge';
import { Menu, MenuItem } from '../../design-system/Menu';
import { HarnessIcon } from '../chat/HarnessIcon';
import type { Agent } from '../agents/types';
import { AgentNodePanel } from './AgentNodePanel';
import { ChoiceNodePanel } from './ChoiceNodePanel';
import { ForkNodePanel } from './ForkNodePanel';
import { JoinNodePanel } from './JoinNodePanel';
import { TerminalNodePanel } from './TerminalNodePanel';
import type { TerminalDefinition } from './terminal';
import type { JoinDefinition } from './join';
import { createFork, type ForkDefinition } from './fork';
import { choiceOutputFields, type ChoiceDefinition } from './choice';
import { RemoveNodeDialog } from './RemoveNodeDialog';
import { RemoveEdgeDialog } from './RemoveEdgeDialog';
import { graphEditorEdgeTypes } from './EditableEdge';
import { intermediateChoice } from './intermediate-choice';
import { effortLabel, harnessNames, resolveAgentNode, type AgentNodeOverrides } from './agent-node';
import { request } from '../../engine';
import { authoringPath, canvasDefinition, canvasLayout, graphDrawing, graphListEntry, type GraphFile, type GraphLayout } from './files';
import { GraphDetailsDialog } from './GraphDetailsDialog';
import { GraphNodeHandles, nodeLegacyHandles, useDynamicHandles, type DynamicPoint } from './DynamicHandles';
import { connectionKey, DYNAMIC_SOURCE, DYNAMIC_TARGET, needsIntermediateChoice, pruneHandles, sourcePort, validGraphConnection, type BorderPoint, type FixedHandle } from './connections';
import '@xyflow/react/dist/style.css';
import './graph-shimmer.css';
import './graph-running-led.css';
import './graph-canvas.css';
import { bindDraftDiscard, draftRecord, flushDrafts, setDraftValues, useEngineDraft } from '../drafts/draftStore';
import { IS_DESKTOP } from '../../platform';
import { GraphDraftScope } from './draftScope';
import { beginLocalActivity } from '../engines/localActivities';

type CanvasNodeData = {
  fixedHandles?: FixedHandle[];
  dynamicPoint?: DynamicPoint;
  legacyHandles?: string[];
  readOnly?: boolean;
  running?: boolean;
  activityStatus?: 'pending' | 'running' | 'collecting' | 'completed';
  inspecting?: boolean;
  inspectionMode?: 'activity' | 'configuration';
  onInspect?: () => void;
  terminal?: TerminalDefinition;
  name?: string;
  join?: JoinDefinition;
  fork?: ForkDefinition;
  choice?: ChoiceDefinition;
  agentId?: string;
  overrides?: AgentNodeOverrides;
  initial?: boolean;
  onAddAgent?: () => void;
  onAddTerminal?: () => void;
  onConfigure?: () => void;
  agent?: Agent;
  settings?: ReturnType<typeof resolveAgentNode>;
  configuring?: boolean;
};
export type CanvasNode = Node<CanvasNodeData>;
type CanvasContextMenu = { x: number; y: number } & ({ kind: 'node'; nodeId: string } | { kind: 'canvas' });

function InitialNodePlaceholder({ data }: NodeProps<CanvasNode>) {
  const [open, setOpen] = useState(false);
  useOnViewportChange({ onStart: () => setOpen(false) });
  return <Menu label="Node catalog" role="menu" side="bottom" className="graph-node-catalog" open={open} onOpenChange={setOpen}
    trigger={props => <Button {...props} className="graph-initial-node-placeholder nodrag nopan">Add initial node</Button>}>
    <MenuItem role="menuitem" onClick={() => { setOpen(false); data.onAddAgent?.(); }}><Bot />AI agent</MenuItem>
    <MenuItem role="menuitem" onClick={() => { setOpen(false); data.onAddTerminal?.(); }}><Terminal />Terminal command</MenuItem>
  </Menu>;
}

function NodeInspectionButton({ data }: { data: CanvasNodeData }) {
  if (!data.readOnly || !data.onInspect) return null;
  const name = data.name ?? data.choice?.name ?? data.fork?.name ?? data.terminal?.name ?? `Join · ${data.agent?.name ?? 'Integration'}`;
  const label = data.inspectionMode === 'configuration' ? `View configuration for ${name}` : `View activity for ${name}, ${data.activityStatus ?? 'pending'}`;
  return <button type="button" className="graph-node-inspect-button nodrag nopan" aria-label={label} aria-expanded={data.inspecting} onClick={data.onInspect} />;
}

function AgentNodeCard({ id, data }: NodeProps<CanvasNode>) {
  return <div className={`graph-ai-agent-card ${data.configuring ? 'is-configuring' : ''} ${data.running ? 'graph-node--running' : ''}`}>
    <GraphNodeHandles id={id} handles={data.fixedHandles} dynamic={data.dynamicPoint} readOnly={data.readOnly} legacyHandles={data.legacyHandles} target="input" source="choices" />
    <div className="graph-ai-agent-heading"><span><Bot />AI agent</span><span className="graph-node-heading-meta">
      {data.initial && <Badge>Start</Badge>}
      {data.running && <span className="graph-node-running-label"><i aria-hidden="true" />Running</span>}
      {!data.running && data.activityStatus === 'completed' && <span className="graph-run-status graph-run-status--completed">Completed</span>}
      {!data.readOnly && <IconButton label="Configure agent node" className="nodrag nopan" onClick={data.onConfigure}><Settings2 /></IconButton>}
    </span></div>
    <strong>{data.name?.trim() || 'New agent node'}</strong>
    <span className="graph-ai-agent-name" title="Agent">{data.agent?.name ?? data.agentId ?? 'Select agent'}</span>
    {data.agent && <span className="graph-ai-agent-model">{data.settings?.model?.name ?? 'Select model'}{data.settings?.effort && <> · {effortLabel(data.settings.effort)}</>}</span>}
    {data.settings && <div className="graph-ai-agent-metadata"><span><span aria-hidden="true"><HarnessIcon harness={data.settings.harness} /></span>{harnessNames[data.settings.harness]}</span></div>}
    <NodeInspectionButton data={data} />
  </div>;
}

function NodeProgress({ data }: { data: CanvasNodeData }) {
  if (data.running) return <span className="graph-node-running-label"><i aria-hidden="true" />Running</span>;
  if (data.activityStatus === 'collecting') return <span className="graph-run-status">Collecting</span>;
  return data.activityStatus === 'completed' ? <span className="graph-run-status graph-run-status--completed">Completed</span> : null;
}

function ChoiceNodeCard({ id, data }: NodeProps<CanvasNode>) {
  return <div className={`graph-choice-node ${data.configuring ? 'is-configuring' : ''} ${data.running ? 'graph-node--running' : ''}`}>
    <GraphNodeHandles id={id} handles={data.fixedHandles} dynamic={data.dynamicPoint} readOnly={data.readOnly} legacyHandles={data.legacyHandles} target="sources" source={data.choice?.terminal ? undefined : 'destination'} />
    {data.readOnly ? <div className="graph-choice-content graph-choice-content--readonly"><strong>{data.choice?.name || 'New choice'}</strong><NodeProgress data={data} /></div> : <Button variant="ghost" className="graph-choice-content nopan" onClick={data.onConfigure} aria-label={`Configure choice: ${data.choice?.name || 'New choice'}`}>
      <strong>{data.choice?.name || 'New choice'}</strong>
    </Button>}
    <NodeInspectionButton data={data} />
  </div>;
}

function ForkNodeCard({ id, data }: NodeProps<CanvasNode>) {
  const updateNodeInternals = useUpdateNodeInternals();
  useEffect(() => { updateNodeInternals(id); }, [id, data.fork, updateNodeInternals]);
  return <div className={`graph-fork-card ${data.configuring ? 'is-configuring' : ''} ${data.running ? 'graph-node--running' : ''}`}>
    <GraphNodeHandles id={id} handles={data.fixedHandles} dynamic={data.dynamicPoint} readOnly={data.readOnly} legacyHandles={data.legacyHandles} target="input" />
    <div className="graph-ai-agent-heading"><span><GitFork />Fork</span><NodeProgress data={data} />{!data.readOnly && <IconButton label="Configure fork node" className="nodrag nopan" onClick={data.onConfigure}><Settings2 /></IconButton>}</div>
    <strong>{data.fork?.name || 'Fork'}</strong>
    <div className="graph-fork-outputs">{data.fork?.branches.map(branch => <div key={branch.id} className="graph-fork-output">
      <span>{branch.name}</span><span className="graph-fork-worktree-label">{branch.separateWorktree === false ? 'Shared workspace' : branch.gitBranch || 'Worktree'}</span><Handle type="source" position={Position.Right} id={branch.id} aria-label={`Branch output: ${branch.name}`} />
    </div>)}</div>
    <NodeInspectionButton data={data} />
  </div>;
}

function JoinNodeCard({ id, data }: NodeProps<CanvasNode>) {
  return <div className={`graph-ai-agent-card ${data.configuring ? 'is-configuring' : ''} ${data.running ? 'graph-node--running' : ''}`}>
    <GraphNodeHandles id={id} handles={data.fixedHandles} dynamic={data.dynamicPoint} readOnly={data.readOnly} legacyHandles={data.legacyHandles} target="branches" source="choices" />
    <div className="graph-ai-agent-heading"><span><GitMerge />Join</span><NodeProgress data={data} />{!data.readOnly && <IconButton label="Configure join node" className="nodrag nopan" onClick={data.onConfigure}><Settings2 /></IconButton>}</div>
    <strong>{data.agent?.name ?? data.join?.agentId ?? 'Select agent'}</strong>
    {data.agent && <span className="graph-ai-agent-model">{data.settings?.model?.name ?? 'Select model'}{data.settings?.effort && <> · {effortLabel(data.settings.effort)}</>}</span>}
    {data.join?.outputBranch && <span className="graph-join-output-branch" title="Output Git branch">{data.join.outputBranch}</span>}
    <NodeInspectionButton data={data} />
  </div>;
}

function TerminalNodeCard({ id, data }: NodeProps<CanvasNode>) {
  return <div className={`graph-ai-agent-card ${data.configuring ? 'is-configuring' : ''} ${data.running ? 'graph-node--running' : ''}`}>
    <GraphNodeHandles id={id} handles={data.fixedHandles} dynamic={data.dynamicPoint} readOnly={data.readOnly} legacyHandles={data.legacyHandles} target="input" source="output" />
    <div className="graph-ai-agent-heading"><span><Terminal />Terminal command</span><span className="graph-node-heading-meta">{data.initial && <Badge>Start</Badge>}<NodeProgress data={data} />{!data.readOnly && <IconButton label="Configure terminal node" className="nodrag nopan" onClick={data.onConfigure}><Settings2 /></IconButton>}</span></div>
    <strong>{data.terminal?.name || 'New terminal node'}</strong>
    {data.terminal?.command && <code className="graph-terminal-command-preview" title={data.terminal.command}>{data.terminal.command}</code>}
    <NodeInspectionButton data={data} />
  </div>;
}

export const graphViewNodeTypes = { aiAgent: AgentNodeCard, choice: ChoiceNodeCard, fork: ForkNodeCard, join: JoinNodeCard, terminal: TerminalNodeCard };
const nodeTypes = { ...graphViewNodeTypes, initialPlaceholder: InitialNodePlaceholder };
// Presentation only; this placeholder is not an executable graph node.
const initialNodes: CanvasNode[] = [{
  id: 'initial-node-placeholder', type: 'initialPlaceholder', position: { x: 0, y: 0 }, data: {},
  draggable: false, selectable: false, connectable: false, deletable: false, focusable: false,
}];

export function CanvasControls() {
  const { zoomIn, zoomOut, fitView } = useReactFlow();
  return <Panel position="bottom-left" className="graph-canvas-toolbar">
    <IconButton label="Zoom in" onClick={() => void zoomIn()}><Plus /></IconButton>
    <IconButton label="Zoom out" onClick={() => void zoomOut()}><Minus /></IconButton>
    <IconButton label="Reset view" onClick={() => void fitView({ maxZoom: 1 })}><Maximize /></IconButton>
  </Panel>;
}

export function GraphCanvas({ agents, graph, graphs, projectId, onSaved, onClose }: { agents: Agent[]; graph: GraphFile; graphs: GraphFile[]; projectId: string; onSaved: () => Promise<void>; onClose: () => void }) {
  const key = `form.graph:${projectId}:${graph.id}`;
  const forgetDrafts = bindDraftDiscard();
  const [baseFile, setBaseFile] = useEngineDraft(`${key}.base`, graph, true);
  const [drawing] = useState(() => graphDrawing(IS_DESKTOP ? baseFile : graph));
  const [nodes, setNodes] = useEngineDraft<CanvasNode[]>(`${key}.nodes`, drawing.nodes.length ? drawing.nodes : initialNodes, false,
    value => Array.isArray(value) && value.every(node => draftRecord(node) && typeof node.id === 'string' && draftRecord(node.data) && draftRecord(node.position) && Number.isFinite(node.position.x) && Number.isFinite(node.position.y)));
  const [edges, setEdges] = useEngineDraft<Edge[]>(`${key}.edges`, drawing.edges, false,
    value => Array.isArray(value) && value.every(edge => draftRecord(edge) && typeof edge.id === 'string' && typeof edge.source === 'string' && typeof edge.target === 'string'));
  const onNodesChange = useCallback((changes: NodeChange<CanvasNode>[]) => setNodes(current => applyNodeChanges(changes, current)), [setNodes]);
  const onEdgesChange = useCallback((changes: EdgeChange<Edge>[]) => setEdges(current => applyEdgeChanges(changes, current)), [setEdges]);
  const [definition, setDefinition, clearDefinition] = useEngineDraft(`${key}.definition`, IS_DESKTOP ? baseFile.definition : graph.definition);
  const file = useRef(IS_DESKTOP ? baseFile : graph);
  const [detailsOpen, setDetailsOpen] = useEngineDraft(`${key}.detailsOpen`, false);
  const [viewport, setViewport] = useEngineDraft(`${key}.viewport`, baseFile.layout.viewport);
  const [saving, setSaving] = useState(false);
  const savingRef = useRef(false);
  const [saveError, setSaveError] = useState('');
  const [layoutError, setLayoutError] = useEngineDraft(`${key}.layoutError`, '');
  const [saved, setSaved] = useState('');
  const layoutQueue = useRef<Promise<unknown>>(Promise.resolve());
  const [layoutDraft, setLayoutDraft] = useEngineDraft<GraphLayout>(`${key}.layout`, baseFile.layout);
  const lastLayout = useRef<GraphLayout>(IS_DESKTOP ? layoutDraft : graph.layout);
  const base = authoringPath(projectId);
  const headerLeading = document.getElementById('workspace-header-leading');
  const headerActions = document.getElementById('workspace-header-actions');
  const flow = useRef<ReactFlowInstance<CanvasNode> | null>(null);
  const canvasRoot = useRef<HTMLDivElement>(null);
  const dynamic = useDynamicHandles(canvasRoot, nodes, connection => validGraphConnection(connection, nodes, edges), !saving);
  // All deletion paths (including configuration changes) share anchor cleanup.
  useEffect(() => { setNodes(current => pruneHandles(current, edges)); }, [edges, setNodes]);
  const [configuringId, setConfiguringId] = useEngineDraft<string | null>(`${key}.configuring`, null);
  const [contextMenu, setContextMenu] = useState<CanvasContextMenu | null>(null);
  const [removingId, setRemovingId] = useState<string | null>(null);
  const [removingEdgeIds, setRemovingEdgeIds] = useState<string[]>([]);
  const removingEdges = edges.filter(edge => removingEdgeIds.includes(edge.id));
  const removingNode = nodes.find(node => node.id === removingId);
  const configuringNode = nodes.find(node => node.id === configuringId);
  const choiceDestination = nodes.find(node => node.id === edges.find(edge => edge.source === configuringId)?.target);
  const choiceSupportsSession = choiceDestination?.type === 'aiAgent' || choiceDestination?.type === 'join';
  const payloadFields = [...new Set([
    ...(configuringNode?.data.initial ? ['task'] : []),
    ...edges.filter(edge => edge.target === configuringId).flatMap(edge => {
      const source = nodes.find(node => node.id === edge.source);
      const fields = source?.data.choice ? choiceOutputFields(source.data.choice)
        : source?.data.terminal?.outputFields ?? source?.data.fork?.branches.find(branch => branch.id === sourcePort(edge, nodes))?.outputFields ?? [];
      return fields.map(field => field.name);
    }),
  ])];

  function persistLayout(layout: GraphLayout) {
    const finished = beginLocalActivity('Saving graph layout');
    // Keep positions of the saved topology too, even when unsaved edits remove or
    // rename an element. Autosaving layout must not erase the saved graph's layout.
    const merged = { ...lastLayout.current, viewport: layout.viewport, positions: { ...lastLayout.current.positions, ...layout.positions } };
    lastLayout.current = merged;
    if (IS_DESKTOP) setLayoutDraft(merged);
    const next = layoutQueue.current.catch(() => {}).then(async () => { if (IS_DESKTOP) await flushDrafts(); return request<GraphLayout>(`${base}/graphs/${encodeURIComponent(file.current.id)}/layout`, 'POST', merged); })
      .then(result => { setLayoutError(''); return result; }, error => { setLayoutError(error instanceof Error ? error.message : 'Could not save layout.'); throw error; }).finally(finished);
    layoutQueue.current = next;
    return next;
  }
  async function save() {
    if (savingRef.current || configuringId) return;
    const finished = beginLocalActivity('Saving graph');
    savingRef.current = true; setSaving(true); setSaveError(''); setSaved('');
    try {
      await flushDrafts();
      const nextDefinition = canvasDefinition(definition, nodes, edges);
      await layoutQueue.current.catch(() => {});
      const layout = canvasLayout(nodes, flow.current?.getViewport(), edges);
      const next = await request<GraphFile>(`${base}/graphs/${encodeURIComponent(file.current.id)}`, 'POST', { definition: nextDefinition, revision: file.current.revision, layout });
      lastLayout.current = next.layout;
      if (IS_DESKTOP) setLayoutDraft(next.layout);
      layoutQueue.current = Promise.resolve();
      setLayoutError('');
      file.current = next; setBaseFile(next); setDefinition(next.definition);
      // Keep this current scene's local node IDs: server-canonical choice IDs
      // must not orphan unapplied subeditors or raw preview inputs on return.
      if (IS_DESKTOP) setDraftValues({ [`${key}.nodes`]: nodes, [`${key}.edges`]: edges });
      clearDefinition(next.definition); setSaved('Saved');
      await onSaved();
    } catch (error) { setSaveError(error instanceof Error ? error.message : 'Could not save graph.'); }
    finally { savingRef.current = false; setSaving(false); finished(); }
  }
  // Only executable edits invalidate the explicit-save indicator; measurements
  // and position changes are handled independently by layout autosave.
  const definitionFingerprint = JSON.stringify([definition.name, definition.description, definition.enabled, nodes.map(n => { const { fixedHandles: _handles, dynamicPoint: _point, legacyHandles: _legacy, ...data } = n.data; return [n.id, n.type, data]; }), edges.map(e => [e.source, e.target, sourcePort(e, nodes)])]);
  useEffect(() => setSaved(''), [definitionFingerprint]);

  function clearChoiceSessionPolicies(sourceIds: Set<string>) {
    setNodes(current => current.map(node => sourceIds.has(node.id) && node.data.choice?.sessionPolicy
      ? { ...node, data: { ...node.data, choice: { ...node.data.choice, sessionPolicy: undefined } } } : node));
  }

  function addAgent(position?: XYPosition) {
    const placeholder = nodes.find(node => node.type === 'initialPlaceholder');
    if (!position && !placeholder) return;
    const node: CanvasNode = {
      id: crypto.randomUUID(), type: 'aiAgent', position: position ?? placeholder!.position,
      data: { name: '', initial: !position, overrides: {} },
      draggable: true, selectable: false, connectable: true, deletable: false,
    };
    setNodes(current => position ? [...current, node] : current.map(item => item.type === 'initialPlaceholder' ? node : item));
    setConfiguringId(node.id);
    setContextMenu(null);
  }

  function addTerminal(position?: XYPosition) {
    const placeholder = nodes.find(node => node.type === 'initialPlaceholder');
    if (!position && !placeholder) return;
    const node: CanvasNode = {
      id: crypto.randomUUID(), type: 'terminal', position: position ?? placeholder!.position,
      data: { initial: !position, terminal: { name: '', command: '' } },
      draggable: true, selectable: false, connectable: true, deletable: false,
    };
    setNodes(current => position ? [...current, node] : current.map(item => item.type === 'initialPlaceholder' ? node : item));
    setConfiguringId(node.id); setContextMenu(null);
  }

  function addChoice(position: XYPosition) {
    const node: CanvasNode = {
      id: crypto.randomUUID(), type: 'choice', position,
      data: { choice: { id: '', name: '', description: '', terminal: false, fields: [], outputFields: [] } },
      draggable: true, selectable: false, connectable: true, deletable: false,
    };
    setNodes(current => [...current, node]);
    setConfiguringId(node.id); setContextMenu(null);
  }

  function commitConnection(connection: Connection, targetPoint?: BorderPoint) {
    const drag = dynamic.drag.current;
    if (savingRef.current || !drag || drag.cancelled || drag.made || !dynamic.canCommit(connection.target) || !validGraphConnection(connection, nodes, edges)) return;
    const additions = new Map<string, FixedHandle[]>();
    const promote = (nodeId: string, handleId: string | null, type: 'source' | 'target', point?: BorderPoint): string | null => {
      const node = nodes.find(n => n.id === nodeId);
      const legacySource = type === 'source' && node?.type !== 'fork' && !node?.data.fixedHandles?.some(h => h.id === handleId);
      if (handleId !== DYNAMIC_SOURCE && handleId !== DYNAMIC_TARGET && !legacySource) return handleId;
      if (!point) return null;
      const handle: FixedHandle = { ...point, id: `${type === 'source' ? 'src' : 'tgt'}-${crypto.randomUUID()}`, type };
      additions.set(nodeId, [...(additions.get(nodeId) ?? []), handle]);
      return handle.id;
    };
    const sourceHandle = promote(connection.source, connection.sourceHandle, 'source', drag.point);
    const targetHandle = promote(connection.target, connection.targetHandle, 'target', targetPoint ?? dynamic.targetPoint(connection.target));
    if (!sourceHandle || !targetHandle) return;
    const port = sourcePort(connection, nodes, edges);
    const source = nodes.find(n => n.id === connection.source)!;
    const target = nodes.find(n => n.id === connection.target)!;
    const intermediate = needsIntermediateChoice(source, target) ? intermediateChoice(source, target, sourceHandle, targetHandle, nodes) : null;
    drag.made = true;
    setNodes(current => {
      const updated = current.map(node => {
        const added = additions.get(node.id);
        const clearPolicy = node.id === connection.source && node.data.choice?.sessionPolicy && target.type !== 'aiAgent' && target.type !== 'join';
        if (!added && !clearPolicy) return node;
        return { ...node, data: { ...node.data, fixedHandles: [...(node.data.fixedHandles ?? []), ...(added ?? [])],
          ...(clearPolicy ? { choice: { ...node.data.choice!, sessionPolicy: undefined } } : {}) } };
      });
      return intermediate ? [...updated, intermediate.node] : updated;
    });
    setEdges(current => [...current, ...(intermediate?.edges ?? [{ ...connection, id: connectionKey(connection.source, port, connection.target), sourceHandle, targetHandle, data: { sourcePort: port } }])]);
    if (intermediate) setConfiguringId(intermediate.node.id);
  }

  function requestEdgeRemoval(ids: string[]) {
    if (savingRef.current) return;
    setContextMenu(null);
    setRemovingEdgeIds(ids);
  }
  const canvasEdges = edges.map(edge => ({ ...edge, type: 'editable', data: { ...edge.data, onRequestDelete: (id: string) => requestEdgeRemoval([id]) } }));
  const nodeLabel = (id: string) => {
    const node = nodes.find(n => n.id === id);
    return node?.data.choice?.name || node?.data.name || node?.data.terminal?.name || node?.data.fork?.name
      || agents.find(agent => agent.id === (node?.data.join?.agentId ?? node?.data.agentId))?.name || 'Unnamed node';
  };

  const canvasNodes: CanvasNode[] = nodes.map(node => {
    const agent = agents.find(item => item.id === (node.data.join?.agentId ?? node.data.agentId) && item.enabled);
    return { ...node, data: node.type === 'initialPlaceholder'
      ? { ...node.data, onAddAgent: () => addAgent(), onAddTerminal: () => addTerminal() }
      : { ...node.data, agent, settings: agent ? resolveAgentNode(agent, node.data.overrides ?? {}) : undefined,
        dynamicPoint: dynamic.points[node.id], legacyHandles: nodeLegacyHandles(node, edges),
        configuring: node.id === configuringId, onConfigure: () => setConfiguringId(node.id) },
    };
  });

  return <GraphDraftScope.Provider value={`${key}.node:${configuringId ?? ''}`}><section className="graph-editor" aria-label={`${definition.name} editor`}>
    {headerLeading && createPortal(<div className="graph-editor-heading">
      <Button disabled={saving} onClick={() => { void layoutQueue.current.catch(() => {}).then(onClose); }}><ArrowLeft />Back to graphs</Button>
      <strong>{definition.name}</strong>
    </div>, headerLeading)}
    {headerActions && createPortal(<>
      <span className="small muted" role="status">{saved}</span>
      {IS_DESKTOP && <Button disabled={saving} onClick={() => { forgetDrafts(key); onClose(); }}>Discard changes</Button>}
      <IconButton label="Graph details" disabled={saving} onClick={() => setDetailsOpen(true)}><Settings2 /></IconButton>
      <Button variant="primary" disabled={saving || !!configuringId} onClick={() => void save()}><Save />{saving ? 'Saving...' : 'Save'}</Button>
    </>, headerActions)}
    {saveError && <div className="storage-error" role="alert">{saveError}</div>}
    {layoutError && <div className="storage-error" role="alert">Layout not saved: {layoutError} <Button size="sm" disabled={saving} onClick={() => { void persistLayout(lastLayout.current).catch(() => {}); }}>Retry</Button></div>}
    <div ref={canvasRoot} className="graph-canvas" inert={saving}>
    <ReactFlow<CanvasNode> nodes={canvasNodes} onNodesChange={onNodesChange} edges={canvasEdges} onEdgesChange={onEdgesChange} nodeTypes={nodeTypes} edgeTypes={graphEditorEdgeTypes} onInit={instance => { flow.current = instance; }} onNodeClick={(_, node) => { if (node.type !== 'initialPlaceholder') setConfiguringId(node.id); }}
      isValidConnection={connection => validGraphConnection(connection, nodes, edges)}
      onConnectStart={(event, params) => { setContextMenu(null); dynamic.start(event, params); }}
      onConnect={connection => commitConnection(connection)} onConnectEnd={event => dynamic.finish(event, commitConnection)}
      connectOnClick={false} edgesReconnectable={false} connectionRadius={10}
      connectionLineType={ConnectionLineType.SmoothStep} connectionLineStyle={{ stroke: Object.values(dynamic.points).some(p => !p.valid) ? 'var(--color-danger)' : 'var(--color-accent)' }}
      onBeforeDelete={async ({ edges: deleted }) => { if (deleted.length) requestEdgeRemoval(deleted.map(edge => edge.id)); return false; }}
      defaultEdgeOptions={{ type: 'smoothstep', markerEnd: { type: MarkerType.ArrowClosed, color: 'var(--color-muted)' }, style: { stroke: 'var(--color-muted)' } }}
      onNodeContextMenu={(event, node) => {
        if (node.type === 'initialPlaceholder') return;
        event.preventDefault();
        const bounds = event.currentTarget.getBoundingClientRect();
        setContextMenu({ kind: 'node', nodeId: node.id, x: event.clientX || bounds.right, y: event.clientY || bounds.bottom });
      }}
      onPaneContextMenu={event => {
        event.preventDefault();
        setContextMenu({ kind: 'canvas', x: event.clientX, y: event.clientY });
      }}
      onNodeDragStart={() => { setContextMenu(null); dynamic.moveStart(); }} onMoveStart={() => setContextMenu(null)} onMove={() => dynamic.viewportChanged()}
      onNodeDragStop={(_, moved) => { dynamic.moveEnd(); if (!savingRef.current) void persistLayout(canvasLayout(nodes.map(n => n.id === moved.id ? { ...n, position: moved.position } : n), flow.current?.getViewport())).catch(() => {}); }}
      onMoveEnd={(event, viewport) => { if (event && !savingRef.current) { setViewport(viewport); void persistLayout(canvasLayout(nodes, viewport)).catch(() => {}); } }}
      minZoom={0.2} maxZoom={2} defaultViewport={IS_DESKTOP ? viewport : graph.layout.viewport} fitView={!(IS_DESKTOP ? viewport : graph.layout.viewport)} fitViewOptions={{ maxZoom: 1 }}>
      <Background gap={24} size={1} color="var(--color-faint)" />
      <CanvasControls />
      {configuringNode?.type === 'aiAgent' && <Panel position="center-right" className="graph-agent-panel-position"><AgentNodePanel key={configuringNode.id} agents={agents} name={configuringNode.data.name ?? ''} onNameChange={name => setNodes(current => current.map(node => node.id === configuringNode.id ? { ...node, data: { ...node.data, name } } : node))} agentId={configuringNode.data.agentId ?? ''} overrides={configuringNode.data.overrides ?? {}} onSelectAgent={id => {
        if (id !== configuringNode.data.agentId) setNodes(current => current.map(node => node.id === configuringNode.id ? { ...node, data: { ...node.data, agentId: id, overrides: {} } } : node));
      }} onOverridesChange={overrides => setNodes(current => current.map(node => node.id === configuringNode.id ? { ...node, data: { ...node.data, overrides } } : node))} onClose={() => setConfiguringId(null)} /></Panel>}
      {configuringNode?.type === 'choice' && configuringNode.data.choice && <Panel position="center-right" className="graph-agent-panel-position"><ChoiceNodePanel key={configuringNode.id} choice={configuringNode.data.choice} supportsSession={choiceSupportsSession} otherChoices={nodes.flatMap(node => node.id !== configuringNode.id && node.data.choice ? [node.data.choice] : [])} onSave={choice => {
        setNodes(current => current.map(node => node.id === configuringNode.id ? { ...node, data: { ...node.data, choice } } : node));
        if (choice.terminal) setEdges(current => current.filter(edge => edge.source !== configuringNode.id));
        setConfiguringId(null);
      }} onClose={() => setConfiguringId(null)} /></Panel>}
      {configuringNode?.type === 'fork' && configuringNode.data.fork && <Panel position="center-right" className="graph-agent-panel-position graph-fork-panel-position"><ForkNodePanel key={configuringNode.id} fork={configuringNode.data.fork} payloadFields={payloadFields} destinations={Object.fromEntries(edges.filter(edge => edge.source === configuringNode.id).map(edge => {
        const target = nodes.find(node => node.id === edge.target);
        return [sourcePort(edge, nodes), target?.type === 'terminal' ? target.data.terminal?.name || 'New terminal node' : target?.data.name?.trim() || 'New agent node'];
      }))} onSave={fork => {
        setNodes(current => current.map(node => node.id === configuringNode.id ? { ...node, data: { ...node.data, fork } } : node));
        setEdges(current => current.filter(edge => edge.source !== configuringNode.id || fork.branches.some(branch => branch.id === sourcePort(edge, nodes))));
        setConfiguringId(null);
      }} onClose={() => setConfiguringId(null)} /></Panel>}
      {configuringNode?.type === 'join' && configuringNode.data.join && <Panel position="center-right" className="graph-agent-panel-position"><JoinNodePanel key={configuringNode.id} join={configuringNode.data.join} agents={agents} onSave={join => {
        setNodes(current => current.map(node => node.id === configuringNode.id ? { ...node, data: { ...node.data, join } } : node));
        setConfiguringId(null);
      }} onClose={() => setConfiguringId(null)} /></Panel>}
      {configuringNode?.type === 'terminal' && configuringNode.data.terminal && <Panel position="center-right" className="graph-agent-panel-position"><TerminalNodePanel key={configuringNode.id} terminal={configuringNode.data.terminal} payloadFields={payloadFields} onSave={terminal => {
        setNodes(current => current.map(node => node.id === configuringNode.id ? { ...node, data: { ...node.data, terminal } } : node));
        setConfiguringId(null);
      }} onClose={() => setConfiguringId(null)} /></Panel>}
    </ReactFlow>
    </div>
    {detailsOpen && <GraphDetailsDialog draftKey={`${key}.details`} graph={{ ...graphListEntry(file.current), name: definition.name, description: definition.description }} graphs={graphs.map(graphListEntry)} onClose={() => setDetailsOpen(false)} onSave={(name, description) => { setDefinition(current => ({ ...current, name, description })); setDetailsOpen(false); }} />}
    <Menu label={contextMenu?.kind === 'canvas' ? 'Node catalog' : 'Node actions'} role="menu" className={contextMenu?.kind === 'canvas' ? 'graph-node-catalog' : 'graph-node-actions-menu'} open={contextMenu !== null} position={contextMenu ?? undefined} onOpenChange={open => { if (!open) setContextMenu(null); }} trigger={() => null}>
      {contextMenu?.kind === 'canvas' ? <>
        <MenuItem role="menuitem" onClick={() => {
          if (flow.current) addAgent(flow.current.screenToFlowPosition({ x: contextMenu.x, y: contextMenu.y }));
        }}><Bot />AI agent</MenuItem>
        <MenuItem role="menuitem" onClick={() => {
          if (flow.current) addChoice(flow.current.screenToFlowPosition({ x: contextMenu.x, y: contextMenu.y }));
        }}><Diamond />Choice</MenuItem>
        <MenuItem role="menuitem" onClick={() => {
          if (flow.current) addTerminal(flow.current.screenToFlowPosition({ x: contextMenu.x, y: contextMenu.y }));
        }}><Terminal />Terminal command</MenuItem>
        <MenuItem role="menuitem" onClick={() => {
          if (!flow.current) return;
          const node: CanvasNode = { id: crypto.randomUUID(), type: 'fork', position: flow.current.screenToFlowPosition({ x: contextMenu.x, y: contextMenu.y }), data: { fork: createFork() }, draggable: true, selectable: false, connectable: true, deletable: false };
          setNodes(current => [...current, node]);
          setConfiguringId(node.id); setContextMenu(null);
        }}><GitFork />Fork</MenuItem>
        <MenuItem role="menuitem" onClick={() => {
          if (!flow.current) return;
          const node: CanvasNode = { id: crypto.randomUUID(), type: 'join', position: flow.current.screenToFlowPosition({ x: contextMenu.x, y: contextMenu.y }), data: { join: { agentId: '', prompt: '', outputBranch: '' } }, draggable: true, selectable: false, connectable: true, deletable: false };
          setNodes(current => [...current, node]);
          setConfiguringId(node.id); setContextMenu(null);
        }}><GitMerge />Join</MenuItem>
      </> : <MenuItem role="menuitem" className="graph-node-remove-action" onClick={() => {
        if (contextMenu?.kind === 'node') setRemovingId(contextMenu.nodeId);
        setContextMenu(null);
      }}><Trash2 />Remove</MenuItem>}
    </Menu>
    {!!removingEdges.length && <RemoveEdgeDialog count={removingEdges.length} connectionName={`${nodeLabel(removingEdges[0].source)} → ${nodeLabel(removingEdges[0].target)}`} onClose={() => setRemovingEdgeIds([])} onConfirm={() => {
      if (savingRef.current) return;
      const ids = new Set(removingEdges.map(edge => edge.id));
      clearChoiceSessionPolicies(new Set(removingEdges.map(edge => edge.source)));
      setEdges(current => current.filter(edge => !ids.has(edge.id)));
      setRemovingEdgeIds([]);
    }} />}
    {removingNode && <RemoveNodeDialog kind={removingNode.type === 'choice' ? 'choice' : 'node'} nodeName={removingNode.type === 'terminal' ? removingNode.data.terminal?.name || 'New terminal node' : removingNode.type === 'join' ? `Join · ${agents.find(agent => agent.id === removingNode.data.join?.agentId)?.name ?? 'Select agent'}` : removingNode.type === 'fork' ? removingNode.data.fork?.name || 'Fork' : removingNode.type === 'choice' ? removingNode.data.choice?.name || 'New choice' : removingNode.data.name?.trim() || 'New agent node'} onClose={() => setRemovingId(null)} onConfirm={() => {
      setNodes(current => {
        const affectedChoices = new Set(edges.filter(edge => edge.target === removingNode.id).map(edge => edge.source));
        const remaining = current.filter(node => node.id !== removingNode.id).map(node => affectedChoices.has(node.id) && node.data.choice?.sessionPolicy
          ? { ...node, data: { ...node.data, choice: { ...node.data.choice, sessionPolicy: undefined } } } : node);
        return removingNode.data.initial ? [...remaining, { ...initialNodes[0], position: removingNode.position }] : remaining;
      });
      setEdges(current => current.filter(edge => edge.source !== removingNode.id && edge.target !== removingNode.id));
      if (configuringId === removingNode.id) setConfiguringId(null);
      setRemovingId(null);
    }} />}
  </section></GraphDraftScope.Provider>;
}
