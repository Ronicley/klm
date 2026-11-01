import type { Edge } from '@xyflow/react';
import { agentSlug } from '../agents/types';
import type { CanvasNode } from './GraphCanvas';
import { connectionKey, type BorderPoint, type FixedHandle } from './connections';

// This is an authoring shortcut only: the saved graph still contains an ordinary
// Choice and the same two relationships as a manually configured connection.
export function intermediateChoice(source: CanvasNode, target: CanvasNode, sourceHandle: string, targetHandle: string, nodes: CanvasNode[]): { node: CanvasNode; edges: Edge[] } {
  const taken = new Set(nodes.flatMap(n => n.data.choice ? [n.data.choice.id, agentSlug(n.data.choice.name)] : []));
  let name = 'continue';
  for (let suffix = 2; taken.has(agentSlug(name)); suffix++) name = `continue_${suffix}`;
  const id = crypto.randomUUID();
  const center = (node: CanvasNode) => ({ x: node.position.x + (node.measured?.width ?? 280) / 2, y: node.position.y + (node.measured?.height ?? 140) / 2 });
  const from = center(source), to = center(target);
  const dx = to.x - from.x, dy = to.y - from.y;
  const horizontal = Math.abs(dx) >= Math.abs(dy);
  const incoming: BorderPoint = horizontal ? { side: dx >= 0 ? 'left' : 'right', left: dx >= 0 ? 0 : 100, top: 50 }
    : { side: dy >= 0 ? 'top' : 'bottom', left: 50, top: dy >= 0 ? 0 : 100 };
  const outgoing: BorderPoint = horizontal ? { side: dx >= 0 ? 'right' : 'left', left: dx >= 0 ? 100 : 0, top: 50 }
    : { side: dy >= 0 ? 'bottom' : 'top', left: 50, top: dy >= 0 ? 100 : 0 };
  const input: FixedHandle = { ...incoming, id: `tgt-${crypto.randomUUID()}`, type: 'target' };
  const output: FixedHandle = { ...outgoing, id: `src-${crypto.randomUUID()}`, type: 'source' };
  return {
    node: { id, type: 'choice', position: { x: (from.x + to.x) / 2 - 120, y: (from.y + to.y) / 2 - 36 },
      data: { choice: { id: agentSlug(name), name, description: '', terminal: false, fields: [], outputFields: [] }, fixedHandles: [input, output] },
      draggable: true, selectable: false, connectable: true, deletable: false },
    edges: [
      { id: connectionKey(source.id, 'choices', id), source: source.id, target: id, sourceHandle, targetHandle: input.id, data: { sourcePort: 'choices' } },
      { id: connectionKey(id, 'destination', target.id), source: id, target: target.id, sourceHandle: output.id, targetHandle, data: { sourcePort: 'destination' } },
    ],
  };
}
