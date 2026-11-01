import { pauseClientReads, resumeClientReads, stopClientTransport } from '../../clientAuth';
import { reloadClient } from '../../platform';
import { resumeScopedConnection, stopScopedConnection } from '../chat/scopedConnection';
import { flushDraftsForEngineChange, sealDraftEdits } from '../drafts/draftStore';
import { flushConnectionDrafts } from './connectionDrafts';
import { freezeEngineActions, resumeEngineActions, waitForLocalActivities } from './localActivities';

export type EngineChangePhase = 'waiting' | 'saving' | 'committing';

// One transition for saved selection, Add, active removal, Retry and Pair again.
// Cancelling ends only this wait; it never aborts server mutations or sends Stop.
export async function switchEngine(options: {
  signal: AbortSignal;
  status: (phase: EngineChangePhase, labels: string[]) => void;
  detach: () => void;
  restore: () => void;
  commit: () => void | Promise<void>;
}) {
  freezeEngineActions();
  let detached = false;
  let completed = false;
  const cancelled = () => { if (options.signal.aborted) throw new DOMException('Engine change cancelled.', 'AbortError'); };
  try {
    await waitForLocalActivities(options.signal, labels => options.status('waiting', labels));
    cancelled();
    options.status('saving', []);
    options.detach(); detached = true;
    pauseClientReads(); stopScopedConnection();
    await flushDraftsForEngineChange(true);
    await flushConnectionDrafts();
    cancelled();
    options.status('committing', []);
    await options.commit();
    await stopClientTransport();
    reloadClient();
    completed = true;
  } catch (error) {
    sealDraftEdits(false);
    if (detached) { resumeClientReads(); resumeScopedConnection(); options.restore(); }
    throw error;
  } finally { if (!completed) resumeEngineActions(); }
}
