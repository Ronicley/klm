import { useEffect, useMemo, useRef, useState } from 'react';
import { Background, MarkerType, Panel, ReactFlow, type NodeChange } from '@xyflow/react';
import type { Agent } from '../agents/demo';
import type { GraphRequestProjection, GraphRunProjection } from '../../engine';
import { resolveAgentNode } from './agent-node';
import { CanvasControls, graphViewNodeTypes, type CanvasNode } from './GraphCanvas';
import { graphDrawing, graphViewport, type GraphFile } from './files';
import { NodeConfigurationPanel } from './NodeConfigurationPanel';
import { GraphRunActivity } from './GraphRunActivity';
import { GraphRunControl } from './GraphRunControl';

export function GraphView({ sessionId, graph, agents, visible, run, requests, windowFocused, parentFocused, onRequestTarget, controlPending, controlError, onControl }: {
  sessionId: string; graph: GraphFile; agents: Agent[]; visible: boolean; run?: GraphRunProjection | null;
  requests: GraphRequestProjection[]; windowFocused: boolean; parentFocused: boolean;
  onRequestTarget: (target: HTMLDivElement | null) => void;
  controlPending: boolean; controlError: string; onControl: (control: 'pause' | 'resume') => void;
}) {
  const root = useRef<HTMLElement>(null);
  const [measurements, setMeasurements] = useState<Record<string, { width: number; height: number }>>({});
  function onNodesChange(changes: NodeChange<CanvasNode>[]) {
    setMeasurements(current => {
      let next = current;
      for (const change of changes) {
        if (change.type !== 'dimensions' || !change.dimensions) continue;
        const { width, height } = change.dimensions;
        if (width <= 0 || height <= 0 || current[change.id]?.width === width && current[change.id]?.height === height) continue;
        next = { ...next, [change.id]: { width, height } };
      }
      return next;
    });
  }
  const [inspectingId, setInspectingId] = useState<string | null>(null);
  const [inspectionRun, setInspectionRun] = useState<GraphRunProjection | null>(null);
  const activityRun = run ?? inspectionRun;
  const [hasOpened, setHasOpened] = useState(visible);
  useEffect(() => { if (visible) setHasOpened(true); }, [visible]);
  const runId = run?.id;
  useEffect(() => {
    if (runId) { setInspectingId(null); setInspectionRun(null); }
  }, [runId]);
  // SSE may repeat the snapshot on every revision; rebuild only when it changes.
  const drawingKey = JSON.stringify(!run && inspectionRun ? inspectionRun.snapshot : graph);
  const drawing = useMemo(() => graphDrawing(JSON.parse(drawingKey) as GraphFile), [drawingKey]);
  function closeInspection() {
    root.current?.querySelector<HTMLButtonElement>(`[data-id="${CSS.escape(inspectingId ?? '')}"] .graph-node-inspect-button`)?.focus();
    setInspectingId(null);
    setInspectionRun(null);
  }
  const running = !!run?.active;
  const panelOpen = visible && !!activityRun && drawing.nodes.some(node => node.id === inspectingId && (node.type === 'aiAgent' || node.type === 'join'));
  const requestsVisible = parentFocused || (panelOpen && windowFocused);
  const nodes = useMemo<CanvasNode[]>(() => {
    const waiting = new Set(requests.filter(item => item.runId === run?.id).map(item => item.nodeId));
    const active = new Set(running ? run?.activeNodeIds : []);
    const collecting = new Set(running ? run?.collectingJoinIds : []);
    const completed = new Set(running ? [...(run?.completedNodeIds ?? []), ...(run?.completedChoiceIds ?? []).map(id => `choice:${id}`)] : []);
    const agentById = new Map(agents.map(agent => [agent.id, agent]));
    return drawing.nodes.map(node => {
      // GraphRecord contains topology, not captured agent TOMLs. Do not display
      // live-catalog model settings as if they were this run's captured settings.
      const agent = running ? undefined : agentById.get(node.data.join?.agentId ?? node.data.agentId ?? '');
      const status = waiting.has(node.id) ? 'waiting' : active.has(node.id) ? 'running' : collecting.has(node.id) ? 'collecting' : completed.has(node.id) ? 'completed' : 'pending';
      // Controlled-node updates must retain ResizeObserver measurements. Otherwise
      // ReactFlow resets dimensions and hides unchanged nodes until another resize.
      return { ...node, measured: measurements[node.id], draggable: false, selectable: false, connectable: false, deletable: false, focusable: false,
        data: { ...node.data, readOnly: true, agent, settings: agent ? resolveAgentNode(agent, node.data.overrides ?? {}) : undefined,
          running: status === 'running', activityStatus: running ? status : undefined,
          attention: status === 'waiting' && !requestsVisible,
          inspectionMode: running ? 'activity' : 'configuration', configuring: !running && node.id === inspectingId,
          inspecting: node.id === inspectingId, onInspect: running && node.type !== 'aiAgent' && node.type !== 'join' ? undefined : () => { setInspectingId(node.id); setInspectionRun(running ? run ?? null : null); } },
      };
    });
  }, [drawing, agents, run, requests, requestsVisible, inspectingId, running, measurements]);
  const inspectedNode = nodes.find(node => node.id === inspectingId);

  return <section ref={root} hidden={!visible} className="graph-canvas graph-canvas--readonly" aria-label={`${graph.definition.name} graph canvas`} onKeyDown={event => {
    if (event.key === 'Escape' && inspectingId) { event.stopPropagation(); closeInspection(); }
  }}>
    {hasOpened && <ReactFlow<CanvasNode> nodes={nodes} onNodesChange={onNodesChange} edges={drawing?.edges ?? []} nodeTypes={graphViewNodeTypes}
      nodesDraggable={false} nodesConnectable={false} nodesFocusable={false}
      edgesReconnectable={false} edgesFocusable={false} elementsSelectable={false}
      deleteKeyCode={null} selectionKeyCode={null} multiSelectionKeyCode={null}
      panOnDrag zoomOnScroll zoomOnPinch minZoom={0.1} maxZoom={2}
      defaultViewport={graphViewport(graph.layout)} fitView={!graphViewport(graph.layout)} fitViewOptions={{ maxZoom: 1, padding: 0.15 }}
      defaultEdgeOptions={{ type: 'smoothstep', markerEnd: { type: MarkerType.ArrowClosed, color: 'var(--color-muted)' }, style: { stroke: 'var(--color-muted)' } }}>
      <Background gap={24} size={1} color="var(--color-faint)" />
      <CanvasControls />
      {run?.active && <Panel position="top-left"><GraphRunControl run={run} pending={controlPending} error={controlError} onControl={onControl} /></Panel>}
      {visible && inspectedNode && (inspectedNode.type === 'aiAgent' || inspectedNode.type === 'join') && activityRun && <GraphRunActivity key={`${activityRun.id}:${inspectedNode.id}`} sessionId={sessionId} runId={activityRun.id} nodeId={inspectedNode.id} nodeName={inspectedNode.data.name || inspectedNode.id} onClose={closeInspection} hasRequests={requests.length > 0} onRequestTarget={onRequestTarget} />}
      {inspectedNode && !running && !inspectionRun && <Panel position="center-right" className="graph-agent-panel-position"><NodeConfigurationPanel key={inspectedNode.id} node={inspectedNode} nodes={nodes} edges={drawing.edges} onClose={closeInspection} /></Panel>}
    </ReactFlow>}
  </section>;
}
