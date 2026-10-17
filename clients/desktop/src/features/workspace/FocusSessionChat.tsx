import { useEffect, type ReactNode } from 'react';
import { ArrowLeft } from 'lucide-react';
import { Button } from '../../design-system/Button';
import { getConversationGraph, type ConversationGraphState, type EngineEvent, type EventPage, type Session, type SessionResponse } from '../../engine';
import { ConversationHistory } from '../chat/ConversationHistory';
import { GraphPicker } from '../chat/GraphPicker';
import { MessageComposer, type ComposerDraft, type SendMode } from '../chat/MessageComposer';
import { MessageQueue } from '../chat/MessageQueue';
import { ModelPicker } from '../chat/ModelPicker';
import { PendingSubmission, type LocalSubmission } from '../chat/PendingSubmission';
import { PermissionCard } from '../chat/PermissionCard';
import { QuestionCard } from '../chat/QuestionCard';
import { SessionStatusBar } from '../chat/SessionStatusBar';
import { SubagentView } from '../chat/SubagentView';
import { SessionNavigationProvider } from '../chat/SessionNavigation';
import { useAuthoringCatalog } from '../graphs/catalog';
import { graphListEntry } from '../graphs/files';

export function FocusSessionChat({ session, projectName, subagents, draft, pending, disconnected, error, submissions, onRestoreSubmission, onRetrySubmission, onSnapshot, onHistory, onEventChange, onDraftChange, onTranscription, onSend, onStop, onInterrupt, onModel, onGraph, onSelectGraph, selectingGraph, selectedGraphId, onOpenSubagent, activeSubagentId, onBackToSession, onOpenSession, generalControls, harnessUnavailable = false }: {
  session: Session; projectName: string; subagents: Session[]; draft: ComposerDraft; pending: boolean; disconnected: boolean; error?: string;
  submissions: LocalSubmission[]; onRestoreSubmission: (item: LocalSubmission) => void; onRetrySubmission: (item: LocalSubmission) => void;
  onSnapshot: (snapshot: SessionResponse) => void; onHistory: (page: EventPage) => void; onEventChange: (id: string, event: EngineEvent) => void;
  onDraftChange: (draft: ComposerDraft) => void; onTranscription: (text: string) => void; onSend: (draft: ComposerDraft, mode?: SendMode) => boolean; onStop: () => void;
  onInterrupt: (sessionId: string) => void;
  onModel: (model: string, effort: string) => Promise<boolean>; onGraph: (graph: ConversationGraphState) => void;
  onSelectGraph: (id: string) => void; selectingGraph: boolean; selectedGraphId: string; onOpenSubagent: (id: string) => void;
  generalControls?: ReactNode; harnessUnavailable?: boolean;
  activeSubagentId: string; onBackToSession: () => void; onOpenSession: (session: Session) => void;
}) {
  const general = session.role === 'general_agent';
  const title = general ? 'KLM Agent' : session.title;
  const { catalog, error: catalogError, loading, reload } = useAuthoringCatalog(general ? '' : session.projectId);
  const running = session.status === 'running';
  const graphRun = session.graph?.run;
  useEffect(() => {
    if (general) return;
    let active = true;
    void getConversationGraph(session.id).then(snapshot => { if (active) onGraph(snapshot); }).catch(() => {});
    return () => { active = false; };
  }, [session.id, general, onGraph]);
  function refreshGraph() {
    void getConversationGraph(session.id).then(onGraph).catch(() => {});
  }
  const graphRequests = general ? [] : session.graph?.requests ?? [];
  const activeSubagent = subagents.find(child => child.id === activeSubagentId);
  return <SessionNavigationProvider onOpenSession={onOpenSession}>
    <div className="focus-session-chat" hidden={!!activeSubagentId}>
    <ConversationHistory session={session} subagents={subagents} working={running || submissions.some(item => item.status === 'sending')} label={`${title} floating conversation`} onSnapshot={onSnapshot} onHistory={onHistory} onEventChange={onEventChange} onOpenSubagent={onOpenSubagent} empty={<div className="empty-chat"><h2>What would you like to work on?</h2></div>} />
    {error && <div className="storage-error" role="alert">{error}</div>}
    <div className="composer-area">
      {(!!session.permissions?.length || !!session.questions?.length || graphRequests.length > 0) && <div className="permission-queue">
        {session.permissions?.map(permission => <PermissionCard key={permission.id} permission={permission} sessionId={session.id} projectName={projectName} onResolved={onSnapshot} />)}
        {session.questions?.map(question => <QuestionCard key={question.id} question={question} sessionId={session.id} onResolved={onSnapshot} />)}
        {graphRequests.map(item => {
          const origin = `${graphRun?.snapshot.definition.name ?? item.graphId} · ${item.nodeName || item.nodeId}`;
          const key = `${item.sessionId}:${item.kind}:${item.requestId}`;
          return item.kind === 'permission' && item.permission
            ? <PermissionCard key={key} permission={{ ...item.permission, id: item.requestId }} sessionId={item.sessionId} projectName={projectName} origin={origin} onResolved={refreshGraph} />
            : item.kind === 'question' && item.question
              ? <QuestionCard key={key} question={{ ...item.question, id: item.requestId }} sessionId={item.sessionId} origin={origin} onResolved={refreshGraph} /> : null;
        })}
      </div>}
      <PendingSubmission items={submissions} onRestore={onRestoreSubmission} onRetry={onRetrySubmission} />
      <MessageQueue session={session} disabled={pending} onSnapshot={onSnapshot} onInterrupt={onInterrupt} />
      {general && generalControls}
      {harnessUnavailable && <p role="status" className="form-error">The selected harness is not installed.</p>}
      <MessageComposer projectId={general ? undefined : session.projectId} placeholder={`Message ${title}`} draft={draft} onDraftChange={onDraftChange} onSend={onSend} onStop={onStop} running={running} disabled={pending || harnessUnavailable}
        onTranscription={onTranscription}
        graphControl={general ? undefined : <GraphPicker graphs={catalog?.graphs.map(graphListEntry) ?? []} value={selectedGraphId} selectedName={catalog?.graphs.find(graph => graph.id === selectedGraphId)?.definition.name ?? (graphRun?.graphId === selectedGraphId ? graphRun.snapshot.definition.name : undefined)} running={!!graphRun?.active && graphRun.graphId === selectedGraphId} disabled={selectingGraph || !session.graph} loading={loading} error={catalogError || catalog?.errors.join(' ')} onReload={() => void reload().catch(() => {})} onChange={onSelectGraph} />}
        modelControl={<ModelPicker key={`${session.id}/${session.harness}`} session={session} disabled={pending || running} optionsDisabled={pending || disconnected} onSave={onModel} onSnapshot={onSnapshot} />} />
      <SessionStatusBar session={session} />
      {catalogError && <div className="form-error" role="alert">{catalogError}<Button size="sm" variant="ghost" onClick={() => void reload().catch(() => {})}>Retry</Button></div>}
    </div>
    </div>
    {activeSubagentId && <>
      <div className="focus-subagent-navigation">
        <Button variant="ghost" size="sm" onClick={onBackToSession} title={`Back to ${title}`}><ArrowLeft /><span>Back to {title}</span></Button>
        <span title={activeSubagent?.title}>{activeSubagent?.title || 'Subagent'}</span>
      </div>
      <SubagentView key={activeSubagentId} session={activeSubagent} onSnapshot={onSnapshot} onHistory={onHistory} />
    </>}
  </SessionNavigationProvider>;
}
