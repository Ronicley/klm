import { BaseEdge, getSmoothStepPath, type Edge, type EdgeProps } from '@xyflow/react';
import { Trash2 } from 'lucide-react';
import { IconButton } from '../../design-system/Button';

type EditableEdgeData = { sourcePort?: string; onRequestDelete: (id: string) => void };

export function EditableEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, style, markerStart, markerEnd, data }: EdgeProps<Edge<EditableEdgeData>>) {
  const [path, x, y] = getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition });
  return <g className="graph-editable-edge">
    <BaseEdge id={id} path={path} style={style} markerStart={markerStart} markerEnd={markerEnd} interactionWidth={28} />
    <foreignObject x={x - 20} y={y - 20} width={40} height={40} className="graph-edge-actions nodrag nopan">
      <IconButton label="Remove connection" onPointerDown={event => event.stopPropagation()} onClick={event => {
        event.stopPropagation();
        data?.onRequestDelete(id);
      }}><Trash2 /></IconButton>
    </foreignObject>
  </g>;
}

export const graphEditorEdgeTypes = { editable: EditableEdge };
