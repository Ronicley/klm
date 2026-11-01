import { EngineRequestError } from '../../engine';
import { agentOrigin, type AgentProblem } from './types';

export function AgentProblemMessage({ error }: { error: unknown }) {
  if (!error) return null;
  const problem = error instanceof EngineRequestError ? error.details as AgentProblem | undefined : undefined;
  return <div className="form-error agent-problem" role="alert">
    <p>{error instanceof Error ? error.message : 'Could not update agent.'}</p>
    {problem?.hint && <p>{problem.hint}</p>}
    {!!problem?.conflicts?.length && <ul>{problem.conflicts.map((source, index) => <li key={`${source.sourceUid}/${source.projectId}/${index}`}>{source.id} · {agentOrigin(source)}</li>)}</ul>}
    {!!problem?.blockingReferences?.length && <ul>{problem.blockingReferences.map(ref => <li key={`${ref.projectId}/${ref.id}`}>{ref.name} · {ref.projectName}</li>)}</ul>}
  </div>;
}
