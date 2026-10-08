import { useLayoutEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import type { GraphRequestProjection, GraphRunProjection } from '../../engine';
import { PermissionCard } from '../chat/PermissionCard';
import { QuestionCard } from '../chat/QuestionCard';
import type { GraphFile } from './files';

export function GraphRequestQueue({ requests, run, graphs, projectName, target, onResolved }: {
  requests: GraphRequestProjection[]; run?: GraphRunProjection | null; graphs: GraphFile[];
  projectName: string; target: HTMLDivElement | null; onResolved: () => void;
}) {
  // Move one stable portal host, rather than remounting cards between surfaces.
  // This retains question drafts, errors and in-flight submission guards.
  const [host] = useState(() => document.createElement('div'));
  useLayoutEffect(() => {
    target?.appendChild(host);
    return () => { host.remove(); };
  }, [host, target]);

  return createPortal(<div className="graph-request-queue">
    {requests.map(item => {
      const graphName = run?.id === item.runId ? run.snapshot.definition.name : graphs.find(graph => graph.id === item.graphId)?.definition.name ?? item.graphId;
      const node = run?.id === item.runId ? run.snapshot.definition.nodes[item.nodeId] : undefined;
      const nodeName = node?.name || (node?.type === 'join' ? `Join · ${node.agent || item.nodeId}` : item.nodeName || item.nodeId);
      const origin = `${graphName || 'Graph'} · ${nodeName}`;
      const key = `${item.runId}:${item.activationId}:${item.sessionId}:${item.kind}:${item.requestId}`;
      return item.kind === 'permission' && item.permission
        ? <PermissionCard key={key} permission={{ ...item.permission, id: item.requestId }} sessionId={item.sessionId} projectName={projectName} origin={origin} onResolved={onResolved} />
        : item.kind === 'question' && item.question
          ? <QuestionCard key={key} question={{ ...item.question, id: item.requestId }} sessionId={item.sessionId} origin={origin} onResolved={onResolved} /> : null;
    })}
  </div>, host);
}
