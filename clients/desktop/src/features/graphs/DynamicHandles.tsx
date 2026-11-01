import { useEffect, useRef, useState, type RefObject } from 'react';
import { Handle, Position, useUpdateNodeInternals, type Connection, type OnConnectStartParams, type XYPosition } from '@xyflow/react';
import type { CanvasNode } from './GraphCanvas';
import { DYNAMIC_SOURCE, DYNAMIC_TARGET, type BorderPoint, type FixedHandle } from './connections';

const MARGIN = 50;
const positions = { top: Position.Top, bottom: Position.Bottom, left: Position.Left, right: Position.Right };
export type DynamicPoint = BorderPoint & { valid: boolean; target: boolean };
type Drag = { source: string; sourceHandle: string; point?: BorderPoint; made: boolean; cancelled: boolean };

function pointer(event: MouseEvent | TouchEvent): XYPosition | undefined {
  const p = 'changedTouches' in event ? event.changedTouches[0] ?? event.touches[0] : event;
  return p ? { x: p.clientX, y: p.clientY } : undefined;
}
function project(p: XYPosition, r: DOMRect): BorderPoint {
  const x = Math.max(r.left, Math.min(r.right, p.x));
  const y = Math.max(r.top, Math.min(r.bottom, p.y));
  const distances = [y - r.top, r.bottom - y, x - r.left, r.right - x];
  const side = (['top', 'bottom', 'left', 'right'] as const)[distances.indexOf(Math.min(...distances))];
  return { side, left: side === 'left' ? 0 : side === 'right' ? 100 : (x - r.left) / (r.width || 1) * 100,
    top: side === 'top' ? 0 : side === 'bottom' ? 100 : (y - r.top) / (r.height || 1) * 100 };
}

export function useDynamicHandles(root: RefObject<HTMLDivElement | null>, nodes: CanvasNode[], valid: (connection: Connection) => boolean, enabled: boolean) {
  const [points, setPoints] = useState<Record<string, DynamicPoint>>({});
  const drag = useRef<Drag | null>(null);
  const moving = useRef(false);
  const latest = useRef({ nodes, valid, enabled });
  latest.current = { nodes, valid, enabled };
  const lastPointer = useRef<XYPosition | null>(null);

  function publish(next: Record<string, DynamicPoint>) {
    // Throttle rendered positions; commits project the final pointer directly.
    setPoints(current => {
      const keys = Object.keys(next);
      return keys.length === Object.keys(current).length && keys.every(id => {
        const a = current[id], b = next[id];
        return a && a.side === b.side && a.valid === b.valid && a.target === b.target && Math.abs(a.left - b.left) < 1 && Math.abs(a.top - b.top) < 1;
      }) ? current : next;
    });
  }
  function allowedAt(p: XYPosition) {
    const element = document.elementFromPoint(p.x, p.y);
    return !!element && !!root.current?.contains(element) && !element.closest('.react-flow__panel, .react-flow__controls, .react-flow__attribution, .graph-ai-agent-heading button, .graph-edge-actions');
  }
  function nearby(p: XYPosition, eligibleOnly = false) {
    if (!root.current || !allowedAt(p)) return undefined;
    const candidates = Array.from(root.current.querySelectorAll<HTMLElement>('.react-flow__node')).flatMap(element => {
      const id = element.dataset.id;
      const node = latest.current.nodes.find(n => n.id === id);
      if (!id || !node || node.type === 'initialPlaceholder' || id === drag.current?.source) return [];
      const r = element.getBoundingClientRect();
      if (!r.width || !r.height) return [];
      const distance = Math.hypot(Math.max(r.left - p.x, 0, p.x - r.right), Math.max(r.top - p.y, 0, p.y - r.bottom));
      const connection = drag.current ? { source: drag.current.source, sourceHandle: drag.current.sourceHandle, target: id, targetHandle: DYNAMIC_TARGET } : null;
      const valid = connection ? latest.current.valid(connection) : true;
      return distance < MARGIN && (!eligibleOnly || valid) ? [{ id, point: project(p, r), valid, distance }] : [];
    });
    candidates.sort((a, b) => a.distance - b.distance || a.id.localeCompare(b.id));
    return candidates[0];
  }
  function refresh(p = lastPointer.current) {
    if (!p || !latest.current.enabled || moving.current || drag.current?.cancelled) { publish({}); return; }
    lastPointer.current = p;
    const next: Record<string, DynamicPoint> = {};
    const d = drag.current;
    if (d?.point) next[d.source] = { ...d.point, valid: true, target: false };
    const candidate = nearby(p);
    if (candidate) {
      // Fork has labeled branch outputs, but still accepts a dynamic input.
      next[candidate.id] = { ...candidate.point, valid: candidate.valid, target: !!d };
    }
    publish(next);
  }
  function reset() { drag.current = null; publish({}); }
  function cancel() { if (drag.current) drag.current.cancelled = true; publish({}); }

  useEffect(() => {
    const move = (event: PointerEvent) => refresh({ x: event.clientX, y: event.clientY });
    const up = (event: PointerEvent) => { if (drag.current) refresh({ x: event.clientX, y: event.clientY }); };
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape') cancel(); };
    const leave = () => { if (!drag.current) publish({}); };
    const element = root.current;
    document.addEventListener('pointermove', move);
    document.addEventListener('pointerup', up, true);
    document.addEventListener('keydown', key, true);
    document.addEventListener('pointercancel', cancel);
    window.addEventListener('blur', cancel);
    element?.addEventListener('pointerleave', leave);
    return () => {
      document.removeEventListener('pointermove', move);
      document.removeEventListener('pointerup', up, true);
      document.removeEventListener('keydown', key, true);
      document.removeEventListener('pointercancel', cancel);
      window.removeEventListener('blur', cancel);
      element?.removeEventListener('pointerleave', leave);
    };
  // Listeners read current nodes/validation through refs; one listener per canvas.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => { if (!enabled) reset(); }, [enabled]);

  return {
    points, drag,
    start(event: MouseEvent | TouchEvent, params: OnConnectStartParams) {
      if (!params.nodeId || !params.handleId || params.handleType !== 'source') return;
      const p = pointer(event);
      const element = root.current?.querySelector<HTMLElement>(`.react-flow__node[data-id="${CSS.escape(params.nodeId)}"]`);
      // React Flow starts after its drag threshold, but passes the original down
      // event. Do not use hover state that may already have moved to another node.
      if (!lastPointer.current && p) lastPointer.current = p;
      const node = latest.current.nodes.find(n => n.id === params.nodeId);
      const borderSource = params.handleId === DYNAMIC_SOURCE || (node?.type !== 'fork' && !node?.data.fixedHandles?.some(h => h.id === params.handleId));
      drag.current = { source: params.nodeId, sourceHandle: params.handleId,
        point: borderSource && p && element ? project(p, element.getBoundingClientRect()) : undefined,
        made: false, cancelled: false };
      refresh();
    },
    canCommit(targetId: string) {
      const p = lastPointer.current;
      const element = root.current?.querySelector<HTMLElement>(`.react-flow__node[data-id="${CSS.escape(targetId)}"]`);
      if (!p || !element || !allowedAt(p)) return false;
      const r = element.getBoundingClientRect();
      return Math.hypot(Math.max(r.left - p.x, 0, p.x - r.right), Math.max(r.top - p.y, 0, p.y - r.bottom)) < MARGIN;
    },
    targetPoint(id: string): BorderPoint | undefined {
      const p = lastPointer.current;
      const element = root.current?.querySelector<HTMLElement>(`.react-flow__node[data-id="${CSS.escape(id)}"]`);
      return p && allowedAt(p) && element ? project(p, element.getBoundingClientRect()) : undefined;
    },
    finish(event: MouseEvent | TouchEvent, commit: (connection: Connection, targetPoint?: BorderPoint) => void) {
      const d = drag.current;
      const p = pointer(event);
      if (p) lastPointer.current = p;
      if (d && !d.made && !d.cancelled && event.type !== 'touchcancel' && p) {
        const candidate = nearby(p, true);
        if (candidate) commit({ source: d.source, sourceHandle: d.sourceHandle, target: candidate.id, targetHandle: DYNAMIC_TARGET }, candidate.point);
      }
      reset();
    },
    moveStart() { moving.current = true; cancel(); },
    moveEnd() { moving.current = false; reset(); },
    viewportChanged() { refresh(); },
  };
}

export function GraphNodeHandles({ id, handles = [], dynamic, readOnly, legacyHandles = [], target, source }: {
  id: string; handles?: FixedHandle[]; dynamic?: DynamicPoint; readOnly?: boolean; legacyHandles?: string[]; target: string; source?: string;
}) {
  const update = useUpdateNodeInternals();
  const signature = JSON.stringify([handles, dynamic?.side, dynamic?.left, dynamic?.top, legacyHandles, source]);
  useEffect(() => { update(id); }, [id, signature, update]);
  const style = dynamic ? { left: `${dynamic.left}%`, top: `${dynamic.top}%`, right: 'auto', bottom: 'auto', transform: 'translate(-50%, -50%)' } : {};
  const active = !!dynamic && !readOnly;
  return <>
    <Handle id={target} type="target" position={Position.Left} isConnectableStart={false} isConnectableEnd={false}
      className={legacyHandles.includes(target) ? 'graph-legacy-handle' : 'graph-unused-handle'} aria-label="Input" />
    {source && <Handle id={source} type="source" position={Position.Right} isConnectableStart={!readOnly}
      className={`graph-touch-source ${legacyHandles.includes(source) ? 'graph-legacy-handle' : 'graph-unused-handle'}`} aria-label="Output" />}
    {handles.map(h => <Handle key={h.id} id={h.id} type={h.type} position={positions[h.side]}
      isConnectableStart={!readOnly && h.type === 'source'} isConnectableEnd={!readOnly && h.type === 'target'}
      style={{ left: `${h.left}%`, top: `${h.top}%`, right: 'auto', bottom: 'auto', transform: 'translate(-50%, -50%)' }} aria-label={h.type === 'source' ? 'Connection output' : 'Connection input'} />)}
    {!readOnly && <>
      {source && <Handle id={DYNAMIC_SOURCE} type="source" position={positions[dynamic?.side ?? 'right']}
        className={`graph-dynamic-handle ${active && !dynamic?.target ? 'is-active' : ''}`} style={style} aria-label="Connect from border" />}
      <Handle id={DYNAMIC_TARGET} type="target" position={positions[dynamic?.side ?? 'left']} isConnectableStart={false} isConnectableEnd={active && !!dynamic?.target}
        className={`graph-dynamic-handle ${active ? 'is-active' : ''} ${dynamic?.valid === false ? 'is-invalid' : ''}`} style={style} aria-label="Connect to border" />
    </>}
  </>;
}

export function nodeLegacyHandles(node: CanvasNode, edges: { source: string; target: string; sourceHandle?: string | null; targetHandle?: string | null }[]): string[] {
  const fixed = new Set(node.data.fixedHandles?.map(h => h.id));
  return [...new Set(edges.flatMap(e => [e.source === node.id ? e.sourceHandle : null, e.target === node.id ? e.targetHandle : null])
    .filter((id): id is string => !!id && !fixed.has(id)))];
}

