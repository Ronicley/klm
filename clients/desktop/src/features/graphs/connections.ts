import type { Connection, Edge } from '@xyflow/react';
import type { CanvasNode } from './GraphCanvas';

export type HandleSide = 'top' | 'bottom' | 'left' | 'right';
export type BorderPoint = { side: HandleSide; left: number; top: number };
export type FixedHandle = BorderPoint & { id: string; type: 'source' | 'target' };
export type EdgeBinding = { sourceHandle: string; targetHandle: string };
export const DYNAMIC_SOURCE = 'dyn-src';
export const DYNAMIC_TARGET = 'dyn-tgt';
export const connectionKey = (source: string, port: string, target: string) => JSON.stringify([source, port, target]);

export function defaultSourcePort(node?: CanvasNode): string {
  if (node?.type === 'aiAgent' || node?.type === 'join') return 'choices';
  if (node?.type === 'choice' && !node.data.choice?.terminal) return 'destination';
  if (node?.type === 'terminal') return 'output';
  return '';
}

// Logical ports belong to graph topology; React Flow handle IDs belong to layout.
export function sourcePort(edge: Connection | Edge, nodes: CanvasNode[], edges: Edge[] = []): string {
  if ('data' in edge && typeof edge.data?.sourcePort === 'string') return edge.data.sourcePort;
  const node = nodes.find(n => n.id === edge.source);
  if (node?.type === 'fork') return edge.sourceHandle ?? '';
  const existing = edges.find(e => e.source === edge.source && e.sourceHandle === edge.sourceHandle);
  if (typeof existing?.data?.sourcePort === 'string') return existing.data.sourcePort;
  return defaultSourcePort(node);
}

export function needsIntermediateChoice(source?: CanvasNode, target?: CanvasNode): boolean {
  return source?.type === 'aiAgent' && ['aiAgent', 'terminal', 'fork', 'join'].includes(target?.type ?? '');
}

export function validGraphConnection(connection: Connection | Edge, nodes: CanvasNode[], edges: Edge[]): boolean {
  const source = nodes.find(n => n.id === connection.source);
  const target = nodes.find(n => n.id === connection.target);
  const port = sourcePort(connection, nodes, edges);
  if (!source || !target || source.id === target.id || !port) return false;
  const outgoing = edges.filter(e => e.source === source.id);
  if (outgoing.some(e => e.target === target.id && sourcePort(e, nodes) === port)) return false;
  if ((source.type === 'aiAgent' || source.type === 'join') && target.type === 'choice') return true;
  if (needsIntermediateChoice(source, target)) return true;
  if (source.type === 'fork') return (target.type === 'aiAgent' || target.type === 'terminal')
    && !!source.data.fork?.branches.some(b => b.id === port) && !outgoing.some(e => sourcePort(e, nodes) === port);
  const executableTarget = ['aiAgent', 'terminal', 'fork', 'join'].includes(target.type ?? '');
  if (source.type === 'terminal') return executableTarget && !outgoing.length;
  return source.type === 'choice' && !source.data.choice?.terminal && executableTarget && !outgoing.length;
}

export function isFixedHandle(value: unknown): value is FixedHandle {
  if (!value || typeof value !== 'object') return false;
  const h = value as FixedHandle;
  return typeof h.id === 'string' && !!h.id && ![DYNAMIC_SOURCE, DYNAMIC_TARGET].includes(h.id)
    && ['source', 'target'].includes(h.type) && ['left', 'right', 'top', 'bottom'].includes(h.side)
    && Number.isFinite(h.left) && Number.isFinite(h.top) && h.left >= 0 && h.left <= 100 && h.top >= 0 && h.top <= 100
    && (h.side === 'left' ? h.left === 0 : h.side === 'right' ? h.left === 100 : h.side === 'top' ? h.top === 0 : h.top === 100);
}

export function pruneHandles(nodes: CanvasNode[], edges: Edge[]): CanvasNode[] {
  const used = new Set(edges.flatMap(e => [JSON.stringify([e.source, e.sourceHandle]), JSON.stringify([e.target, e.targetHandle])]));
  return nodes.map(node => {
    const handles = node.data.fixedHandles;
    if (!handles?.some(h => !used.has(JSON.stringify([node.id, h.id])))) return node;
    return { ...node, data: { ...node.data, fixedHandles: handles.filter(h => used.has(JSON.stringify([node.id, h.id]))) } };
  });
}
