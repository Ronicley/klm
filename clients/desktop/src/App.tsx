import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ArrowLeft, Download, Focus, GitBranch, Maximize2, Minimize2, Minus, PanelLeft, PanelRight, PanelsTopLeft } from 'lucide-react';
import { IS_DESKTOP, IS_MOBILE_HOST, openContentDownload, openFocus, startWindowDrag } from './platform';
import { bindEngineIdentity, currentEngineIdentity, engineStorageKey, persistEngineIdentity } from './engineIdentity';
import { Button, IconButton } from './design-system/Button';
import { BottomSheet } from './design-system/BottomSheet';
import { Badge } from './design-system/Badge';
import { TabNav } from './design-system/TabNav';
import { WorkspaceSidebar } from './features/workspace/WorkspaceSidebar';
import { FocusWindowResizeHandles } from './features/workspace/FocusWindowResizeHandles';
import { FocusWorkspace } from './features/workspace/FocusWorkspace';
import { FocusSessionChat } from './features/workspace/FocusSessionChat';
import { readFocusLayout, reachableMainGeometry, saveFocusLayout, type FocusLayout } from './features/workspace/focusLayout';
import { ProjectAuthoring } from './features/graphs/ProjectAuthoring';
import { useAuthoringCatalog } from './features/graphs/catalog';
import { graphListEntry } from './features/graphs/files';
import { SideChatPanel } from './features/chat/SideChatPanel';
import { CreateSessionDialog } from './features/workspace/CreateSessionDialog';
import { RenameSessionDialog } from './features/workspace/RenameSessionDialog';
import { TopicDialog } from './features/workspace/TopicDialog';
import { newestSessionFirst, sessionTopic, topicArchived, topicKey } from './features/workspace/topics';
import type { Topic } from './engine';
import { SessionStatusBar } from './features/chat/SessionStatusBar';
import { ModelPicker } from './features/chat/ModelPicker';
import { GeneralHarnessPicker } from './features/chat/GeneralHarnessPicker';
import { KlmAgentWorkspace } from './features/klm-agent/KlmAgentWorkspace';
import { ActivityPagingContext } from './features/klm-agent/activityPaging';
import { SessionOverlay } from './features/klm-agent/SessionOverlay';
import { isSessionResponse, mergeSessions, prependHistory } from './features/chat/sessionState';
import { ConversationHistory } from './features/chat/ConversationHistory';
import { DelegationRequests } from './features/chat/DelegationRequests';
import { SessionNavigationProvider } from './features/chat/SessionNavigation';
import { SubagentView } from './features/chat/SubagentView';
import { GraphPicker } from './features/chat/GraphPicker';
import { PermissionCard } from './features/chat/PermissionCard';
import { QuestionCard } from './features/chat/QuestionCard';
import { MessageQueue } from './features/chat/MessageQueue';
import { GraphRequestPages } from './features/chat/GraphRequestPages';
import { PendingSubmission, readPendingSubmissions, savePendingSubmissions, type LocalSubmission } from './features/chat/PendingSubmission';
import { chatDraftKeys, restoreChatDraftContext } from './features/chat/chatDrafts';
import { acceptChatDeliveries, sameComposerDraft, type ChatDraftState, type DeliveryConfirmation } from './features/chat/chatAcceptance';
import { draftStorageReady, flushDrafts, hasEngineDraft, readDraftValue, setDraftValues, useDraftStorageStatus, useEngineDraft } from './features/drafts/draftStore';
import { beginLocalActivity, engineChangePending } from './features/engines/localActivities';
import { type ComposerDraft, type SendMode } from './features/chat/MessageComposer';
import { AgentMessageComposer as MessageComposer } from './features/agents/AgentMessageComposer';
import { agentExecutionBlocked } from './features/agents/binding';
import type { SessionCreateInput } from './features/agents/types';
import { readConversationWindow, subscribeScope } from './features/chat/scopedConnection';
import { DesignSystem } from './DesignSystem';
import { ProjectRail } from './features/projects/ProjectRail';
import { ProjectDialog } from './features/projects/ProjectDialog';
import { ProjectSettingsDialog, type ProjectSettingsSection } from './features/projects/ProjectSettingsDialog';
import { StopAutomationDialog } from './features/automations/StopAutomationDialog';
import { useActivityBoard } from './features/activity-board/useActivityBoard';
import { SettingsDialog } from './features/settings/SettingsDialog';
import { BackgroundPicker } from './features/settings/BackgroundPicker';
import { ENGINE_URL, EngineRequestError, getConversationGraph, mergeGraphState, selectConversationGraph, request, sessionMetadata as normalizeSessionMetadata, type ConversationGraphState, type EngineEvent, type EngineState, type EngineSnapshot, type EventPage, type Harness, type Project, type Session, type SessionResponse, type SessionMetadata, type SourceReference } from './engine';
import klmIcon from './features/projects/klm-icon.png';

const GraphView = lazy(() => import('./features/graphs/GraphView').then(module => ({ default: module.GraphView })));
const ActivityBoardPage = lazy(() => import('./features/activity-board/ActivityBoardPage').then(module => ({ default: module.ActivityBoardPage })));
type SessionTab = 'chat' | 'graph' | `subagent:${string}`;
type MobileChatSheet = { id: string; sessionId: string; subagentId?: string };

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : 'The engine request failed. Please retry.';
}

function clientMessageId() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function moveBefore<T extends { id: string }>(items: T[], id: string, beforeId: string) {
  const moved = items.find(item => item.id === id);
  if (!moved) return items;
  const remaining = items.filter(item => item.id !== id);
  const target = beforeId ? remaining.findIndex(item => item.id === beforeId) : remaining.length;
  if (target < 0) return items;
  return [...remaining.slice(0, target), moved, ...remaining.slice(target)];
}

function orderSessionSnapshot(current: Session[], incoming: EngineSnapshot['sessions']) {
  const merged = mergeSessions(current, incoming);
  const byId = new Map(merged.map(session => [session.id, session]));
  const incomingIds = new Set(incoming.map(session => session.id));
  return [...incoming.flatMap(session => byId.get(session.id) ?? []), ...merged.filter(session => !incomingIds.has(session.id))];
}

type TopicCollapseState = Record<string, boolean>;
type WorkspaceNavigation = { activeProjectId: string; selectedSessions: Record<string, string>; openSideSessions: string[]; collapsedTopics: Record<string, TopicCollapseState | string[]>; collapsedArchivedProjects: string[]; generalOpen?: boolean };

function topicCollapseState(navigation: WorkspaceNavigation, project: Pick<Project, 'id' | 'topics'>): TopicCollapseState {
  const saved = navigation.collapsedTopics[project.id];
  if (!Array.isArray(saved)) return saved ?? {};
  // Legacy lists implicitly expanded every group not listed as collapsed.
  const keys = ['', ...(project.topics ?? []).map(topic => topic.id)];
  return Object.fromEntries(keys.flatMap(id => [id, `archived:${id}`]).map(key => [key, saved.includes(key)]));
}
const navigationStorageKey = `klm.workspace-navigation.v1:${ENGINE_URL}`;

function readWorkspaceNavigation(): WorkspaceNavigation {
  const fallback: WorkspaceNavigation = { activeProjectId: '', selectedSessions: {}, openSideSessions: [], collapsedTopics: {}, collapsedArchivedProjects: [] };
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(engineStorageKey(navigationStorageKey)) ?? 'null');
    if (!saved || typeof saved !== 'object' || Array.isArray(saved)) return fallback;
    const value = saved as Partial<WorkspaceNavigation>;
    return {
      activeProjectId: typeof value.activeProjectId === 'string' ? value.activeProjectId : '',
      generalOpen: value.generalOpen === true,
      selectedSessions: value.selectedSessions && typeof value.selectedSessions === 'object' && !Array.isArray(value.selectedSessions)
        ? Object.fromEntries(Object.entries(value.selectedSessions).filter((entry): entry is [string, string] => typeof entry[1] === 'string')) : {},
      openSideSessions: Array.isArray(value.openSideSessions) ? [...new Set(value.openSideSessions.filter((id): id is string => typeof id === 'string'))] : [],
      collapsedTopics: value.collapsedTopics && typeof value.collapsedTopics === 'object' && !Array.isArray(value.collapsedTopics)
        ? Object.fromEntries(Object.entries(value.collapsedTopics).flatMap(([id, topics]) => {
          if (Array.isArray(topics)) return [[id, topics.filter((topic): topic is string => typeof topic === 'string')]] as [string, TopicCollapseState | string[]][];
          if (topics && typeof topics === 'object') return [[id, Object.fromEntries(Object.entries(topics).filter(([, collapsed]) => typeof collapsed === 'boolean'))]] as [string, TopicCollapseState | string[]][];
          return [];
        })) : {},
      collapsedArchivedProjects: Array.isArray(value.collapsedArchivedProjects) ? [...new Set(value.collapsedArchivedProjects.filter((id): id is string => typeof id === 'string'))] : [],
    };
  } catch { return fallback; }
}

export function App() {
  const draftStorage = useDraftStorageStatus();
  const [engineIdentity, setEngineIdentity] = useState(currentEngineIdentity);
  const inventoryIdentity = useRef(currentEngineIdentity());
  const [engine, setEngine] = useState<EngineState>({ projects: [], sessions: [], harnesses: [] });
  const [modelPreviews, setModelPreviews] = useState<Record<string, { model: string; effort: string }>>({});
  const { projects, harnesses } = engine;
  const sessions = useMemo(() => engine.sessions.map(item => modelPreviews[item.id] ? { ...item, ...modelPreviews[item.id] } : item), [engine.sessions, modelPreviews]);
  const sessionsRef = useRef(sessions);
  sessionsRef.current = sessions;
  const [loaded, setLoaded] = useState(false);
  const [pageVisible, setPageVisible] = useState(!document.hidden);
  const [generalChatVisible, setGeneralChatVisible] = useState(false);
  const [projectSearch, setProjectSearch] = useState('');
  const [projectPage, setProjectPage] = useState<{ id: string; next: string; topicsNext: string; busy: boolean }>({ id: '', next: '', topicsNext: '', busy: false });
  const historyAccess = useRef(new Map<string, number>());
  const projectWindow = useRef({ key: '', pages: 1, start: '' });
  const topicWindow = useRef({ key: '', cursor: '' });
  const metadataPins = useRef(new Set<string>());
  const projectMetadataIds = useRef(new Set<string>());
  const activityIds = useRef(new Set<string>());
  const [activityCursor, setActivityCursor] = useState('');
  const [activityNext, setActivityNext] = useState('');
  useEffect(() => { const update = () => setPageVisible(!document.hidden); document.addEventListener('visibilitychange', update); return () => document.removeEventListener('visibilitychange', update); }, []);
  const [connectionError, setConnectionError] = useState('');
  const [focusError, setFocusError] = useState('');
  const [savedFocusLayout] = useState<FocusLayout>(() => !IS_DESKTOP && !IS_MOBILE_HOST ? readFocusLayout() : { maximized: false, minimized: false, front: 'main', sessionIds: [] });
  const [focusMaximized, setFocusMaximized] = useState(savedFocusLayout.maximized);
  const [focusMinimized, setFocusMinimized] = useState(savedFocusLayout.minimized);
  const [focusFront, setFocusFront] = useState(savedFocusLayout.front);
  const [focusMainGeometry, setFocusMainGeometry] = useState(savedFocusLayout.mainGeometry);
  const [focusWide, setFocusWide] = useState(() => window.matchMedia('(min-width: 761px)').matches);
  const [floatingSessionIds, setFloatingSessionIds] = useState<string[]>(savedFocusLayout.sessionIds);
  const [unavailableFloatingIds, setUnavailableFloatingIds] = useState<string[]>([]);
  const [floatingSubagentIds, setFloatingSubagentIds] = useState<Record<string, string>>(savedFocusLayout.subagentIds ?? {});
  const focusEnabled = !IS_DESKTOP && !IS_MOBILE_HOST && focusWide;
  const floatingSessions = floatingSessionIds.flatMap(id => {
    const item = sessions.find(session => session.id === id);
    return item && (item.role === 'general_agent' || !item.archived && !item.parentId && !item.role && projects.some(owner => owner.id === item.projectId && !topicArchived(owner, item))) ? [item] : [];
  });
  const [openingGeneralWindow, setOpeningGeneralWindow] = useState(false);
  const openingGeneralWindowRef = useRef(false);
  const [generalWindowError, setGeneralWindowError] = useState('');
  useEffect(() => {
    const media = window.matchMedia('(min-width: 761px)');
    const resize = () => setFocusWide(media.matches);
    media.addEventListener('change', resize);
    return () => media.removeEventListener('change', resize);
  }, []);
  useEffect(() => {
    if (!loaded || !pageVisible) return;
    const valid = new Set(floatingSessions.map(item => item.id));
    const known = new Set(sessions.map(item => item.id));
    const unavailable = (id: string) => !valid.has(id) && (known.has(id) || unavailableFloatingIds.includes(id));
    if (floatingSessionIds.some(unavailable)) setFloatingSessionIds(ids => ids.filter(id => !unavailable(id)));
  }, [loaded, floatingSessions, floatingSessionIds, sessions, unavailableFloatingIds]);
  useEffect(() => {
    if (!focusEnabled) return;
    saveFocusLayout({ maximized: focusMaximized, minimized: focusMinimized, front: focusFront, sessionIds: floatingSessionIds, subagentIds: floatingSubagentIds, mainGeometry: focusMainGeometry });
  }, [focusEnabled, focusMaximized, focusMinimized, focusFront, floatingSessionIds, floatingSubagentIds, focusMainGeometry]);
  async function focus() {
    try { await openFocus(); setFocusError(''); }
    catch (error) { setFocusError(error instanceof Error ? error.message : String(error)); }
  }
  const [sessionErrors, setSessionErrors] = useState<Record<string, string>>({});
  const [pendingSessions, setPendingSessions] = useState<Record<string, boolean>>({});
  const [localMessages, setLocalMessages] = useEngineDraft<Record<string, LocalSubmission[]>>(chatDraftKeys.submissions, () => IS_DESKTOP ? {} : readPendingSubmissions());
  // Reserve synchronously before React renders pending submissions. Accepted or
  // uncertain deliveries keep the reservation; only definite rejection releases it.
  const generalHarnessBlocks = useRef(new Map(Object.entries(localMessages).flatMap(([sessionId, items]) =>
    items.filter(item => item.status !== 'failed').map(item => [item.id, sessionId] as const))));
  useEffect(() => { if (!IS_DESKTOP) savePendingSubmissions(localMessages); }, [localMessages]);
  const submissionTails = useRef(new Map<string, Promise<void>>());
  const submissionRequests = useRef(new Set<string>());
  const submissionControllers = useRef(new Map<string, { sessionId: string; controller: AbortController }>());
  const stopVersions = useRef(new Map<string, number>());
  const pending = useRef(new Set<string>());
  const [refreshVersion, setRefreshVersion] = useState(0);
  const unhydratedFloatingIds = JSON.stringify(floatingSessionIds.filter(id => !sessions.some(item => item.id === id) && !unavailableFloatingIds.includes(id)));
  useEffect(() => {
    const ids = JSON.parse(unhydratedFloatingIds) as string[];
    if (!loaded || !ids.length) return;
    // Project inventory omits the general conversation. Hydrate saved IDs before
    // deciding they are unavailable, without creating a new general conversation.
    const controller = new AbortController();
    let pending = false;
    const hydrate = async () => {
      if (pending) return;
      pending = true;
      await Promise.all(ids.map(async id => {
        try {
          const next = await request<SessionResponse>(`/api/sessions/${encodeURIComponent(id)}`, 'GET', undefined, 15000, controller.signal);
          if (!controller.signal.aborted) receiveSession(next);
        } catch (error) {
          if (controller.signal.aborted) return;
          if (error instanceof EngineRequestError && error.status === 404) setUnavailableFloatingIds(current => [...new Set([...current, id])]);
          else setConnectionError(errorMessage(error));
        }
      }));
      pending = false;
    };
    void hydrate();
    const retry = () => { void hydrate(); };
    const timer = window.setInterval(retry, 30000);
    window.addEventListener('focus', retry);
    return () => { controller.abort(); window.clearInterval(timer); window.removeEventListener('focus', retry); };
  }, [loaded, unhydratedFloatingIds, refreshVersion]);
  const [sessionMetadata, setSessionMetadata] = useState<SessionMetadata | null>(null);
  const metadataRequest = useRef(0);
  const projectRevision = useRef(0);
  const navigation = useRef(0);
  const stream = useRef<EventSource | null>(null);
  const [workspaceNavigation, setWorkspaceNavigation] = useState(readWorkspaceNavigation);
  const [view, setView] = useEngineDraft<'chat' | 'general' | 'design' | 'agents' | 'graphs' | 'activity-board'>('ui.view', () => IS_MOBILE_HOST || workspaceNavigation.generalOpen ? 'general' : 'chat');
  const [boardProjectId, setBoardProjectId] = useEngineDraft('ui.boardProject', '');
  const boardOrigin = useRef<'chat' | 'general'>('general');
  const boardView = view === 'activity-board';
  const tasksBoard = useActivityBoard(loaded && (!focusMinimized || !focusEnabled));
  function openBoard(projectId = '') {
    boardOrigin.current = projectId ? 'chat' : 'general';
    setBoardProjectId(projectId); setSidebarOpen(false); setView('activity-board'); navigation.current += 1;
  }
  const generalView = view === 'general';
  const chatView = view === 'chat' || generalView;
  const [generalError, setGeneralError] = useState('');
  const [generalRetry, setGeneralRetry] = useState(0);
  const { activeProjectId, selectedSessions, openSideSessions, collapsedArchivedProjects } = workspaceNavigation;
  useEffect(() => {
    if (!loaded) return;
    setWorkspaceNavigation(current => {
      const legacyProjects = projects.filter(project => Array.isArray(current.collapsedTopics[project.id]));
      if (!legacyProjects.length) return current;
      const collapsedTopics = { ...current.collapsedTopics };
      for (const project of legacyProjects) collapsedTopics[project.id] = topicCollapseState(current, project);
      return { ...current, collapsedTopics };
    });
  }, [loaded, projects]);
  const [addingProject, setAddingProject] = useEngineDraft<string | null>('ui.addProject', null);
  const [editingProject, setEditingProject] = useEngineDraft<Project | null>('ui.editProject', null);
  const [projectSettingsId, setProjectSettingsId] = useEngineDraft('ui.projectSettings', '');
  const [projectSettingsSection, setProjectSettingsSection] = useEngineDraft<ProjectSettingsSection>('ui.projectSettingsSection', 'general');
  const settingsProject = projects.find(item => item.id === projectSettingsId);
  useEffect(() => { if (!IS_DESKTOP && projectSettingsId && !settingsProject) setProjectSettingsId(''); }, [projectSettingsId, settingsProject?.id]);
  const [creatingSession, setCreatingSession] = useEngineDraft<{ projectId: string; topicId?: string } | null>('ui.createSession', null);
  const [renamingSession, setRenameLocator] = useEngineDraft<Pick<Session, 'id' | 'title'> | null>('ui.renameSession', null);
  const setRenamingSession = (value: Pick<Session, 'id' | 'title'> | null) => setRenameLocator(IS_DESKTOP && value ? { id: value.id, title: value.title } : value);
  const [editingTopic, setEditingTopic] = useEngineDraft<{ projectId: string; topic: Topic } | null>('ui.editTopic', null);
  const [creatingTopicProjectId, setCreatingTopicProjectId] = useEngineDraft('ui.createTopic', '');
  const [choosingTopicSessionId, setChoosingTopicSessionId] = useEngineDraft('ui.chooseTopic', '');
  const choosingTopicSession = sessions.find(item => item.id === choosingTopicSessionId && !item.role && !item.parentId);
  const choosingTopicProject = projects.find(item => item.id === choosingTopicSession?.projectId);
  const canChooseTopic = !!choosingTopicSession && !!choosingTopicProject && !choosingTopicSession.archived && !topicArchived(choosingTopicProject, choosingTopicSession);
  useEffect(() => { if (!IS_DESKTOP && choosingTopicSessionId && !canChooseTopic) setChoosingTopicSessionId(''); }, [choosingTopicSessionId, canChooseTopic]);
  const project = projects.find(item => item.id === activeProjectId) ?? projects[0];
  const projectSessions = sessions.filter(item => item.projectId === project?.id && !item.parentId && item.role !== 'graph_node');
  const visibleProjectSessions = projectSessions.filter(item => !item.archived && !topicArchived(project, item)).sort(newestSessionFirst);
  const selectedProjectSessionId = selectedSessions[project?.id ?? ''];
  const projectSession = selectedProjectSessionId ? projectSessions.find(item => item.id === selectedProjectSessionId) : visibleProjectSessions[0];
  const unresolvedSelection = selectedProjectSessionId && !sessions.some(item => item.id === selectedProjectSessionId) ? selectedProjectSessionId : '';
  useEffect(() => {
    if (!loaded || !pageVisible || generalView || !project || !unresolvedSelection) return;
    const controller = new AbortController(); const projectId = project.id;
    void request<Session>(`/api/sessions/${encodeURIComponent(unresolvedSelection)}`, 'GET', undefined, 15000, controller.signal).then(next => {
      if (!controller.signal.aborted) receiveSession(next);
    }).catch(error => {
      if (controller.signal.aborted) return;
      if (error instanceof EngineRequestError && error.status === 404) setWorkspaceNavigation(current => {
        if (current.selectedSessions[projectId] !== unresolvedSelection) return current;
        const selectedSessions = { ...current.selectedSessions }; delete selectedSessions[projectId]; return { ...current, selectedSessions };
      }); else setConnectionError(errorMessage(error));
    });
    return () => controller.abort();
  }, [loaded, pageVisible, generalView, project?.id, unresolvedSelection]);
  const currentTopic = sessionTopic(project, projectSession);
  const projectSessionArchived = !!projectSession?.archived || topicArchived(project, projectSession);
  const generalSession = sessions.find(item => item.role === 'general_agent');
  const [overlaySessionId, setOverlaySessionId] = useState('');
  const [mobileChatSheets, setMobileChatSheets] = useState<MobileChatSheet[]>([]);
  const mobileChats = IS_MOBILE_HOST ? mobileChatSheets.flatMap(sheet => {
    const target = sessions.find(item => item.id === sheet.sessionId && (item.role === 'general_agent' || !item.role && !item.parentId && !item.archived && projects.some(owner => owner.id === item.projectId && !topicArchived(owner, item))));
    if (!target) return [];
    const subagent = sheet.subagentId ? sessions.find(item => item.id === sheet.subagentId && item.parentId === target.id && item.role === 'subagent') : undefined;
    return [{ ...sheet, session: target, title: sheet.subagentId ? subagent?.title ?? 'Subagent' : target.role === 'general_agent' ? 'KLM Agent' : target.title }];
  }) : [];
  useEffect(() => {
    const valid = new Set(mobileChats.map(sheet => sheet.id));
    if (mobileChatSheets.some(sheet => !valid.has(sheet.id))) setMobileChatSheets(current => current.filter(sheet => valid.has(sheet.id)));
  }, [mobileChatSheets, mobileChats]);
  const overlaySession = generalView ? sessions.find(item => item.id === overlaySessionId && !item.role && !item.parentId && !item.archived && projects.some(owner => owner.id === item.projectId && !topicArchived(owner, item))) : undefined;
  const overlayProject = projects.find(item => item.id === overlaySession?.projectId);
  const session = generalView ? overlaySession ?? generalSession : projectSession;
  const generalConversation = generalView && !overlaySession;
  const conversationProject = overlaySession ? overlayProject : generalView ? undefined : project;
  useEffect(() => {
    if (overlaySessionId && !overlaySession) setOverlaySessionId('');
  }, [overlaySessionId, overlaySession?.id]);
  const activeId = session?.id ?? '';
  useEffect(() => {
    if (IS_DESKTOP) {
      if (!loaded || !draftStorageReady()) return;
      acceptLocalDeliveries(sessions.map(item => ({ sessionId: item.id, parentSessionId: item.role === 'side_agent' ? item.parentId : undefined,
        ids: [...(item.queue ?? []).map(entry => entry.id), ...item.events.map(event => event.id)] })));
      return;
    }
    setLocalMessages(current => {
      let updated = current;
      for (const item of sessions) {
        const pending = current[item.id];
        if (!pending?.length) continue;
        const remaining = pending.filter(message => !item.queue?.some(entry => entry.id === message.id) && !item.events.some(event => event.id === message.id));
        if (remaining.length !== pending.length) {
          updated = { ...updated, [item.id]: remaining };
        }
      }
      return updated;
    });
  }, [sessions, IS_DESKTOP && loaded, draftStorage.ready]);
  // Spawned sessions arrive in the multiplexed inventory; no history scan or
  // reconnect is necessary to discover them.
  const gitBranch = sessionMetadata?.sessionId === activeId ? sessionMetadata.gitBranch ?? '' : '';
  const working = session?.status === 'running' || !!localMessages[activeId]?.some(item => item.status === 'sending');
  const awaitingPermission = !!session?.permissions?.length;
  const awaitingQuestion = !!session?.questions?.length;
  const createProject = projects.find(item => item.id === creatingSession?.projectId);
  const { catalog, error: catalogError, loading: catalogLoading, reload: reloadCatalog } = useAuthoringCatalog(conversationProject?.id ?? '');
  const catalogGraphs = useMemo(() => catalog?.graphs.map(graphListEntry) ?? [], [catalog]);
  const [graphErrors, setGraphErrors] = useState<Record<string, string>>({});
  const [selectingGraphs, setSelectingGraphs] = useState<Record<string, boolean>>({});
  const [graphPreview, setGraphPreview] = useState<Record<string, string>>({});
  const graphSelectionPending = useRef(new Set<string>());
  const [sessionTabs, setSessionTabs] = useState<Record<string, SessionTab>>({});
  const [openSubagentTabs, setOpenSubagentTabs] = useState<Record<string, string[]>>({});
  const selectedGraphId = graphPreview[activeId] ?? session?.graph?.selectedGraphId ?? session?.selectedGraphId ?? '';
  const graphRun = session?.graph?.run;
  const graphRunInProgress = !!selectedGraphId && !!graphRun?.active && graphRun.graphId === selectedGraphId;
  const catalogSelectedGraph = catalog?.graphs.find(graph => graph.id === selectedGraphId);
  const selectedGraph = graphRunInProgress ? graphRun!.snapshot : catalogSelectedGraph;
  const requestedTab = sessionTabs[activeId] ?? 'chat';
  const activeTab: SessionTab = requestedTab === 'graph' && !selectedGraphId ? 'chat' : requestedTab;
  const openSubagentIds = openSubagentTabs[activeId] ?? [];
  const activeSubagentId = activeTab.startsWith('subagent:') ? activeTab.slice('subagent:'.length) : '';
  const activeSubagent = sessions.find(item => item.id === activeSubagentId && item.parentId === activeId && item.role === 'subagent');
  const graphRequests = session?.graph?.requests ?? [];
  useEffect(() => {
    void reloadCatalog().catch(() => {});
  }, [reloadCatalog, selectedGraphId, graphRun?.id, view]);
  const [agentsVisit, setAgentsVisit] = useState(0);
  const [graphsVisit, setGraphsVisit] = useState(0);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [mobileNavigationOpen, setMobileNavigationOpen] = useState(false);
  const [mobileProjectRailOpen, setMobileProjectRailOpen] = useState(false);
  const mobileNavigationVisible = IS_MOBILE_HOST && view === 'chat' && !!project && (mobileNavigationOpen || loaded && !session);
  const mobileSessionVisible = IS_MOBILE_HOST && view === 'chat' && !!session && !mobileNavigationVisible;
  const sideOpen = openSideSessions.includes(activeId);
  const sideVisible = sideOpen && !generalView && view === 'chat' && (!IS_DESKTOP || loaded && draftStorage.ready) && !mobileNavigationVisible && !sidebarOpen && !creatingSession && !editingProject && !projectSettingsId && addingProject === null;
  const sideSession = sessions.find(item => item.parentId === activeId && item.role === 'side_agent');
  const [sideSources, setSideSources] = useEngineDraft<Record<string, SourceReference[]>>(chatDraftKeys.sources, {});
  const [sideErrors, setSideErrors] = useState<Record<string, string>>({});
  const openingSide = useRef(new Set<string>());
  const [drafts, setDrafts] = useEngineDraft<Record<string, ComposerDraft>>(chatDraftKeys.composers, {});
  metadataPins.current = new Set([activeId, generalSession?.id ?? '', sideSession?.id ?? '', ...floatingSessionIds, ...Object.values(floatingSubagentIds), ...mobileChatSheets.flatMap(sheet => [sheet.sessionId, sheet.subagentId ?? '']), ...[...historyAccess.current].sort((a,b) => b[1]-a[1]).slice(0,6).map(([id]) => id)]);
  const [settingsOpen, setSettingsOpen] = useEngineDraft('ui.settings', () => new URLSearchParams(window.location.search).get('klm-library') === '1');
  const [automationStop, setAutomationStop] = useState<{ resolve: (confirmed: boolean) => void }>();
  const shell = useRef<HTMLDivElement>(null);
  const readMainGeometry = useCallback(() => {
    const element = shell.current;
    if (!focusEnabled || focusMaximized || !element) return;
    const bounds = element.getBoundingClientRect();
    if (!bounds.width || !bounds.height) return;
    return reachableMainGeometry({ left: parseFloat(element.style.left) || 0, top: parseFloat(element.style.top) || 0, width: bounds.width, height: bounds.height });
  }, [focusEnabled, focusMaximized]);
  function saveMainGeometry() {
    const geometry = readMainGeometry();
    if (geometry) {
      if (shell.current) { shell.current.style.left = `${geometry.left}px`; shell.current.style.top = `${geometry.top}px`; }
      setFocusMainGeometry(geometry);
    }
  }
  useEffect(() => {
    if (!focusEnabled) return;
    // Capture an in-progress drag/resize too when the tab is hidden or closed.
    const save = () => saveFocusLayout({ maximized: focusMaximized, minimized: focusMinimized, front: focusFront, sessionIds: floatingSessionIds, subagentIds: floatingSubagentIds, mainGeometry: readMainGeometry() ?? focusMainGeometry });
    const onVisibility = () => { if (document.visibilityState === 'hidden') save(); };
    window.addEventListener('pagehide', save);
    document.addEventListener('visibilitychange', onVisibility);
    return () => { window.removeEventListener('pagehide', save); document.removeEventListener('visibilitychange', onVisibility); };
  }, [focusEnabled, focusMaximized, focusMinimized, focusFront, floatingSessionIds, floatingSubagentIds, focusMainGeometry, readMainGeometry]);
  const drag = useRef<{ pointerId: number; x: number; y: number; left: number; top: number; minX: number; maxX: number; minY: number; maxY: number } | null>(null);
  useEffect(() => {
    // Wait for engine data before persisting fallback selections; an initial
    // empty render must not erase the project/session saved before a reload.
    if (!loaded) return;
    try {
      localStorage.setItem(engineStorageKey(navigationStorageKey), JSON.stringify({
        ...workspaceNavigation,
        generalOpen: generalView,
        activeProjectId: project?.id ?? '',
        selectedSessions,
      }));
    } catch { /* Navigation still works when browser storage is unavailable. */ }
  }, [loaded, workspaceNavigation, project?.id, projectSession?.id, generalView, selectedSessions]);
  useEffect(() => {
    if (!generalView || generalSession || IS_DESKTOP && (!loaded || engineChangePending())) return;
    const controller = new AbortController();
    setGeneralError('');
    void request<SessionResponse>('/api/general-session', 'POST', {}, 15000, AbortSignal.any([controller.signal, AbortSignal.timeout(15000)]))
      .then(next => { if (!controller.signal.aborted) receiveSession(next); })
      .catch(error => { if (!controller.signal.aborted) setGeneralError(errorMessage(error)); });
    return () => controller.abort();
  }, [generalView, generalSession?.id, generalRetry, IS_DESKTOP && loaded]);
  function setSideOpen(open: boolean) {
    if (!activeId) return;
    setWorkspaceNavigation(current => ({ ...current, openSideSessions: open
      ? [...new Set([...current.openSideSessions, activeId])]
      : current.openSideSessions.filter(id => id !== activeId) }));
  }
  useEffect(() => {
    let active = true;
    let refreshing = false;
    let refreshAgain = false;
    if (!pageVisible) return;
    async function refresh() {
      if (refreshing) { refreshAgain = true; return; }
      refreshing = true;
      const revision = projectRevision.current;
      try {
        const next = await request<EngineSnapshot>('/api/state');
        if (!active) return;
        if (IS_DESKTOP && !next.engineId) throw new Error('The engine did not confirm its database identity. Drafts have not been loaded.');
        const identityChanged = (next.engineId ? bindEngineIdentity(next.engineId, !IS_DESKTOP) : false) ||
          IS_DESKTOP && !!inventoryIdentity.current && next.engineId !== inventoryIdentity.current;
        if (next.engineId) setEngineIdentity(next.engineId);
        if (IS_DESKTOP) {
          if (identityChanged) setLoaded(false);
          await restoreChatDraftContext(next.engineId!, !inventoryIdentity.current);
          if (!active) return;
          // Do not record first identity adoption until legacy delivery data is
          // protected; a failed write must remain retryable after a page reload.
          persistEngineIdentity();
          const restored = readDraftValue<Record<string, LocalSubmission[]>>(chatDraftKeys.submissions, {});
          for (const [session, items] of Object.entries(restored)) for (const item of items) if (item.status !== 'failed') generalHarnessBlocks.current.set(item.id, session);
        }
        if (identityChanged) {
          historyAccess.current.clear(); projectMetadataIds.current.clear(); activityIds.current.clear(); metadataPins.current.clear();
          projectWindow.current = { key: '', pages: 1, start: '' }; topicWindow.current = { key: '', cursor: '' };
          for (const id of submissionTails.current.keys()) stopVersions.current.set(id, (stopVersions.current.get(id) ?? 0) + 1);
          const restoredNavigation = readWorkspaceNavigation();
          const restoredSubmissions = IS_DESKTOP ? readDraftValue<Record<string, LocalSubmission[]>>(chatDraftKeys.submissions, {}) : readPendingSubmissions();
          for (const pending of submissionControllers.current.values()) pending.controller.abort();
          submissionControllers.current.clear(); submissionTails.current.clear(); submissionRequests.current.clear(); generalHarnessBlocks.current.clear();
          pending.current.clear(); setPendingSessions({}); setSessionErrors({}); setSideErrors({}); setGraphErrors({}); setSessionTabs({}); setOpenSubagentTabs({});
          automationStop?.resolve(false); setAutomationStop(undefined);
          if (!IS_DESKTOP) { setCreatingSession(null); setRenamingSession(null); setEditingProject(null); setProjectSettingsId(''); setChoosingTopicSessionId(''); setAddingProject(null); setSettingsOpen(false); }
          for (const [session, items] of Object.entries(restoredSubmissions)) for (const item of items) if (item.status !== 'failed') generalHarnessBlocks.current.set(item.id, session);
          setEngine({ projects: [], sessions: [], harnesses: [] }); setLoaded(false);
          if (!IS_DESKTOP) { setDrafts({}); setLocalMessages(restoredSubmissions); }
          setModelPreviews({}); setWorkspaceNavigation(restoredNavigation);
          if (!IS_DESKTOP) setView(restoredNavigation.generalOpen ? 'general' : 'chat');
          else if (!hasEngineDraft('ui.view')) setDraftValues({ 'ui.view': restoredNavigation.generalOpen ? 'general' : 'chat' });
          const layout = readFocusLayout(); setFloatingSessionIds(layout.sessionIds); setFloatingSubagentIds(layout.subagentIds ?? {}); setUnavailableFloatingIds([]);
          setOverlaySessionId(''); setMobileChatSheets([]); setActivityCursor(''); setRefreshVersion(n => n + 1);
          inventoryIdentity.current = next.engineId ?? '';
          return;
        }
        const activity = await request<{ items: Session[]; nextCursor?: string }>(`/api/activity/sessions?limit=100${activityCursor ? `&cursor=${encodeURIComponent(activityCursor)}` : ''}`);
        if (!active) return;
        inventoryIdentity.current = next.engineId ?? '';
        const projectsUnchanged = revision === projectRevision.current;
        const previousActivityIds = activityIds.current;
        setEngine(current => {
          if (identityChanged) current = { projects: [], sessions: [], harnesses: [] };
          const visibleProjects = projectsUnchanged ? next.projects.map(p => ({ ...current.projects.find(old => old.id === p.id), ...p })) : current.projects;
          const visibleIds = new Set(visibleProjects.map(project => project.id));
          return {
            projects: visibleProjects,
            sessions: orderSessionSnapshot(current.sessions.filter(s => !previousActivityIds.has(s.id) || activity.items.some(next => next.id === s.id) || metadataPins.current.has(s.id) || projectMetadataIds.current.has(s.id)).map(s => previousActivityIds.has(s.id) && !activity.items.some(next => next.id === s.id) ? { ...s, activityListed: false } : s), [...next.sessions, ...activity.items.map(s => ({ ...normalizeSessionMetadata(s), activityListed: true }))]).filter(session => visibleIds.has(session.projectId) || session.role === 'general_agent' || session.projectId === '' && session.role === 'subagent'),
            harnesses: next.harnesses,
          };
        });
        activityIds.current = new Set(activity.items.map(s => s.id));
        setActivityNext(activity.nextCursor ?? '');
        setLoaded(true);
        setConnectionError('');
      } catch (error) {
        if (active && !loaded) setConnectionError(errorMessage(error));
      } finally {
        refreshing = false;
        if (active && refreshAgain) { refreshAgain = false; void refresh(); }
      }
    }
    void refresh();
    const timer = window.setInterval(() => { if (!document.hidden) void refresh(); }, 30000);
    let queued: ReturnType<typeof setTimeout> | undefined;
    const unsubscribe = pageVisible ? subscribeScope('global', '', () => { if (!queued) queued = setTimeout(() => { queued = undefined; void refresh(); }, 1000); }) : () => {};
    return () => { active = false; clearTimeout(queued); window.clearInterval(timer); unsubscribe(); };
  }, [refreshVersion, pageVisible, activityCursor]);

  useEffect(() => {
    if (!loaded || !pageVisible || generalView || !project) return;
    const id = project.id; const controller = new AbortController(); let pending = false; let again = false;
    const key = `${id}/${projectSearch}`;
    if (projectWindow.current.key !== key) projectWindow.current = { key, pages: 1, start: '' };
    if (topicWindow.current.key !== key) topicWindow.current = { key, cursor: '' };
    async function readWindow() {
      const items: Session[] = []; let next = projectWindow.current.start;
      for (let pageIndex = 0; pageIndex < projectWindow.current.pages; pageIndex++) {
        const page = await request<{ items: Session[]; nextCursor?: string }>(`/api/projects/${encodeURIComponent(id)}/sessions?limit=100&archived=all&search=${encodeURIComponent(projectSearch)}${next ? `&cursor=${encodeURIComponent(next)}` : ''}`, 'GET', undefined, 15000, controller.signal);
        items.push(...page.items); next = page.nextCursor ?? ''; if (!next) break;
      }
      return { items, nextCursor: next };
    }
    async function load() {
      if (pending) { again = true; return; } pending = true;
      try {
        const [page, topics] = await Promise.all([
          readWindow(),
          request<{ items: Topic[]; nextCursor?: string }>(`/api/projects/${encodeURIComponent(id)}/topics?limit=200&search=${encodeURIComponent(projectSearch)}${topicWindow.current.cursor ? `&cursor=${encodeURIComponent(topicWindow.current.cursor)}` : ''}`, 'GET', undefined, 15000, controller.signal),
        ]);
        if (controller.signal.aborted) return;
        const listed = new Set(page.items.map(item => item.id));
        projectMetadataIds.current = listed;
        setEngine(current => ({ ...current, projects: current.projects.map(p => p.id === id ? { ...p, topics: topics.items, topicOrder: topics.items.map(t => t.id) } : { ...p, topics: undefined, topicOrder: undefined }), sessions: mergeSessions(current.sessions.filter(s => listed.has(s.id) || metadataPins.current.has(s.id) || activityIds.current.has(s.id)), page.items.map(normalizeSessionMetadata)) }));
        setProjectPage({ id, next: page.nextCursor ?? '', topicsNext: topics.nextCursor ?? '', busy: false });
        setConnectionError('');
      } catch (error) {
        if (!controller.signal.aborted && error instanceof EngineRequestError && error.status === 409 && (projectWindow.current.start || topicWindow.current.cursor)) { projectWindow.current.start = ''; topicWindow.current.cursor = ''; again = true; }
        else if (!controller.signal.aborted) setConnectionError(errorMessage(error));
      }
      finally { pending = false; if (again && !controller.signal.aborted) { again = false; void load(); } }
    }
    void load(); let timer: ReturnType<typeof setTimeout> | undefined;
    const unsubscribe = subscribeScope('project', id, () => { if (!timer) timer = setTimeout(() => { timer = undefined; void load(); }, 750); });
    return () => { controller.abort(); clearTimeout(timer); unsubscribe(); };
  }, [loaded, project?.id, generalView, pageVisible, refreshVersion, projectSearch]);

  async function loadMoreProject(topics = false) {
    if (!project || projectPage.id !== project.id || projectPage.busy) return;
    const id = project.id; const cursor = topics ? projectPage.topicsNext : projectPage.next; if (!cursor) return;
    setProjectPage(p => ({ ...p, busy: true }));
    try {
      if (topics) {
        const page = await request<{ items: Topic[]; nextCursor?: string }>(`/api/projects/${id}/topics?limit=200&search=${encodeURIComponent(projectSearch)}&cursor=${encodeURIComponent(cursor)}`);
        topicWindow.current.cursor = cursor;
        setEngine(current => ({ ...current, projects: current.projects.map(p => p.id === id ? { ...p, topics: page.items, topicOrder: page.items.map(t => t.id) } : p) }));
        setProjectPage(p => ({ ...p, topicsNext: page.nextCursor ?? '' }));
      } else {
        const page = await request<{ items: Session[]; nextCursor?: string }>(`/api/projects/${id}/sessions?archived=all&search=${encodeURIComponent(projectSearch)}&cursor=${encodeURIComponent(cursor)}`);
        const rotate = projectWindow.current.pages >= 4;
        if (rotate) projectWindow.current = { ...projectWindow.current, start: cursor, pages: 1 }; else projectWindow.current.pages++;
        projectMetadataIds.current = new Set([...(rotate ? [] : projectMetadataIds.current), ...page.items.map(s => s.id)]);
        setEngine(current => ({ ...current, sessions: mergeSessions(rotate ? current.sessions.filter(s => metadataPins.current.has(s.id) || activityIds.current.has(s.id)) : current.sessions, page.items.map(normalizeSessionMetadata)) })); setProjectPage(p => ({ ...p, next: page.nextCursor ?? '' }));
      }
    } catch (error) { setConnectionError(errorMessage(error)); } finally { setProjectPage(p => ({ ...p, busy: false })); }
  }

  useEffect(() => {
    if (!activeId) {
      setSessionMetadata(null);
      return;
    }
    const controller = new AbortController();
    setSessionMetadata(current => current?.sessionId === activeId ? current : { sessionId: activeId });
    async function refreshMetadata() {
      const requestId = ++metadataRequest.current;
      try {
        const next = await request<SessionMetadata>(`/api/sessions/${encodeURIComponent(activeId)}/metadata`, 'GET', undefined, 15000, controller.signal);
        if (!controller.signal.aborted && requestId === metadataRequest.current && next.sessionId === activeId) setSessionMetadata(next);
      } catch {
        if (!controller.signal.aborted && requestId === metadataRequest.current) setSessionMetadata({ sessionId: activeId });
      }
    }
    void refreshMetadata();
    const onFocus = () => void refreshMetadata();
    window.addEventListener('focus', onFocus);
    return () => { controller.abort(); window.removeEventListener('focus', onFocus); };
  }, [activeId, session?.status]);

  const applyGraph = useCallback((snapshot: ConversationGraphState) => {
    setEngine(current => ({ ...current, sessions: current.sessions.map(item => {
      if (item.id !== snapshot.sessionId) return item;
      const graph = mergeGraphState(item.graph, snapshot);
      return graph === item.graph ? item : { ...item, graph, selectedGraphId: graph?.selectedGraphId };
    }) }));
  }, []);
  // Hydrate/reconcile selection after CRUD, and poll even while the chat is idle.
  useEffect(() => {
    if (!activeId || generalConversation) return;
    let active = true;
    let pending = false;
    async function refreshGraph() {
      if (pending) return;
      pending = true;
      try {
        const snapshot = await getConversationGraph(activeId);
        if (active) { applyGraph(snapshot); setGraphErrors(current => ({ ...current, [activeId]: '' })); }
      } catch (error) {
        if (active) setGraphErrors(current => ({ ...current, [activeId]: errorMessage(error) }));
      } finally { pending = false; }
    }
    void refreshGraph();
    let queued: ReturnType<typeof setTimeout> | undefined;
    const unsubscribe = pageVisible ? subscribeScope('conversation', activeId, () => { if (!queued) queued = setTimeout(() => { queued = undefined; void refreshGraph(); }, 250); }) : () => {};
    const timer = window.setInterval(() => { if (stream.current?.readyState !== EventSource.OPEN) void refreshGraph(); }, 30000);
    return () => { active = false; clearTimeout(queued); unsubscribe(); window.clearInterval(timer); };
  }, [activeId, generalConversation, catalog, refreshVersion, applyGraph, pageVisible]);
  useEffect(() => {
    if (!selectedGraphId) setSessionTabs(current => current[activeId] === 'graph' ? { ...current, [activeId]: 'chat' } : current);
  }, [activeId, selectedGraphId]);
  async function chooseGraph(id: string, graphId: string) {
    if (graphSelectionPending.current.has(id)) return;
    graphSelectionPending.current.add(id);
    setGraphPreview(current => ({ ...current, [id]: graphId }));
    setSelectingGraphs(current => ({ ...current, [id]: true }));
    setGraphErrors(current => ({ ...current, [id]: '' }));
    try { applyGraph(await selectConversationGraph(id, graphId)); }
    catch (error) { setGraphErrors(current => ({ ...current, [id]: errorMessage(error) })); }
    finally { graphSelectionPending.current.delete(id); setGraphPreview(current => { const next = { ...current }; delete next[id]; return next; }); setSelectingGraphs(current => ({ ...current, [id]: false })); }
  }
  const streamSet = new Set<string>();
  if (pageVisible) {
    if (activeId && chatView && activeTab !== 'graph' && (!focusEnabled || !focusMinimized) && !mobileNavigationVisible && (!generalView || generalChatVisible)) streamSet.add(activeSubagentId || activeId);
    if (generalView && generalChatVisible && overlaySession && generalSession) streamSet.add(generalSession.id);
    if (sideVisible && sideSession) streamSet.add(sideSession.id);
    const sheet = mobileChats.at(-1); if (sheet) streamSet.add(sheet.subagentId || sheet.session.id);
    if (focusEnabled) floatingSessions.forEach(target => streamSet.add(floatingSubagentIds[target.id] || target.id));
  }
  const streamIds = JSON.stringify([...streamSet].sort());
  useEffect(() => {
    const ids = JSON.parse(streamIds) as string[];
    const cleanups = ids.map(id => {
      const controller = new AbortController(); let pending = false; let again = false; let timer: ReturnType<typeof setTimeout> | undefined;
      let latestNotice: { kind: string; revision: string } | undefined;
      let resetPending = false;
      const schedule = () => {
        if (controller.signal.aborted) return;
        if (pending) { again = true; return; }
        if (timer) return;
        timer = setTimeout(() => {
          timer = undefined; const notice = latestNotice; latestNotice = undefined; const reset = resetPending; resetPending = false;
          const history = sessionsRef.current.find(s => s.id === id)?.history;
          if (!reset && notice?.kind === 'ready' && history && BigInt(history.revision) >= BigInt(notice.revision)) return;
          void refresh(reset);
        }, 150);
      };
      const refresh = async (reset = false) => {
        if (controller.signal.aborted) return;
        if (pending) { again = true; return; } pending = true;
        try {
          const update = await readConversationWindow(id, sessionsRef.current.find(s => s.id === id), controller.signal, reset);
          if (controller.signal.aborted) return;
          historyAccess.current.set(id, Date.now());
          if (historyAccess.current.size > 64) for (const [old] of [...historyAccess.current].sort((a,b) => a[1]-b[1])) { if (!ids.includes(old)) historyAccess.current.delete(old); if (historyAccess.current.size <= 64) break; }
          setEngine(current => {
            const merged = mergeSessions(current.sessions, [update], true);
            const keep = new Set([...ids, ...[...historyAccess.current].sort((a,b) => b[1]-a[1]).slice(0,6).map(([id]) => id)]);
            return { ...current, sessions: merged.map(s => !keep.has(s.id) && s.history ? { ...s, events: [], history: undefined } : s) };
          }); setConnectionError('');
        } catch (error) { if (!controller.signal.aborted) setConnectionError(errorMessage(error)); }
        finally { pending = false; if (again && !controller.signal.aborted) { again = false; schedule(); } }
      };
      const unsubscribe = subscribeScope('conversation', id, notice => { latestNotice = notice; resetPending ||= notice.kind === 'reset'; schedule(); });
      void refresh();
      return () => { controller.abort(); clearTimeout(timer); unsubscribe(); };
    });
    return () => cleanups.forEach(close => close());
  }, [streamIds, refreshVersion]);

  useEffect(() => {
    const keepPositionReachable = () => {
      drag.current = null;
      if (shell.current) delete shell.current.dataset.dragging;
      setFocusMainGeometry(current => current ? reachableMainGeometry(current) : current);
    };
    window.addEventListener('resize', keepPositionReachable);
    return () => window.removeEventListener('resize', keepPositionReachable);
  }, []);
  useEffect(() => {
    const mobileSidebar = sidebarOpen && window.matchMedia('(max-width: 760px)').matches;
    if (!mobileSidebar) return;
    const panel = document.querySelector<HTMLElement>('.workspace-sidebar');
    if (!panel) return;
    const previous = document.activeElement as HTMLElement | null;
    const covered = Array.from(document.querySelectorAll<HTMLElement>('.main-panel, .side-chat-panel, .project-rail'));
    covered.forEach(element => { element.inert = true; });
    const focusable = () => Array.from(panel.querySelectorAll<HTMLElement>('button, input, select, summary, [tabindex="0"]')).filter(element => element.getClientRects().length > 0);
    focusable()[0]?.focus();
    function onKey(event: KeyboardEvent) {
      if (event.key === 'Escape') { setSidebarOpen(false); }
      if (event.key === 'Tab') {
        const items = focusable(); const first = items[0]; const last = items[items.length - 1];
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
      }
    }
    const resize = () => { setSidebarOpen(false); };
    document.addEventListener('keydown', onKey); window.addEventListener('resize', resize);
    return () => { covered.forEach(element => { element.inert = false; }); document.removeEventListener('keydown', onKey); window.removeEventListener('resize', resize); previous?.focus(); };
  }, [sidebarOpen, project?.id]);
  useEffect(() => { if (sideVisible && session) void ensureSide(session); }, [sideVisible, activeId]);
  function newSession(topic?: Topic) {
    if (!project) return;
    navigation.current += 1;
    setCreatingSession({ projectId: project.id, topicId: topic?.id });
    setSidebarOpen(false);
  }
  function generalHarnessBlocked(target: Session) {
    const current = sessionsRef.current.find(item => item.id === target.id) ?? target;
    return !!current.harnessLocked || current.status === 'running' ||
      [...generalHarnessBlocks.current.values()].includes(target.id) ||
      !!localMessages[target.id]?.some(item => item.status === 'sending' || item.status === 'accepted' || item.status === 'uncertain');
  }
  function interruptSubmissions(id: string) {
    stopVersions.current.set(id, (stopVersions.current.get(id) ?? 0) + 1);
    for (const item of submissionControllers.current.values()) if (item.sessionId === id) item.controller.abort();
    submissionTails.current.delete(id);
  }
  async function updateSession(target: Session, action: 'messages' | 'stop' | 'harness', body: (ComposerDraft & { sources?: SourceReference[]; mode?: SendMode }) | { harness: Harness['id'] } | Record<string, never>) {
    const identity = currentEngineIdentity();
    const id = target.id;
    let stopBody: { automationRunId: string } | undefined;
    if (action === 'stop' && target.automation?.status === 'running') {
      if (automationStop) return false;
      const confirmed = await new Promise<boolean>(resolve => setAutomationStop({ resolve }));
      if (!confirmed || identity !== currentEngineIdentity()) return false;
      stopBody = { automationRunId: target.automation.runId };
    }
    if (action === 'harness' && target.role === 'general_agent' && generalHarnessBlocked(target)) return false;
    if (action === 'stop') interruptSubmissions(id);
    if (action !== 'stop' && pending.current.has(id)) return false;
    if (action !== 'stop') pending.current.add(id);
    if (action !== 'stop') setPendingSessions(current => ({ ...current, [id]: true }));
    setSessionErrors(current => ({ ...current, [id]: '' }));
    try {
      const next = await request<SessionResponse>(`/api/sessions/${encodeURIComponent(id)}/${action}`, action === 'harness' ? 'PATCH' : 'POST', stopBody ?? body);
      setEngine(current => ({ ...current, sessions: mergeSessions(current.sessions, [next], action === 'messages' ? 'mutation' : false) }));
      return true;
    } catch (error) {
      setSessionErrors(current => ({ ...current, [id]: errorMessage(error) }));
      return false;
    } finally {
      if (action !== 'stop') { pending.current.delete(id); setPendingSessions(current => ({ ...current, [id]: false })); }
    }
  }
  async function patchSession(target: Pick<Session, 'id'>, body: { title?: string; archived?: boolean; topicId?: string }): Promise<string | null> {
    const id = target.id;
    if (pending.current.has(id)) return 'Another session update is still in progress.';
    pending.current.add(id); setPendingSessions(current => ({ ...current, [id]: true })); setSessionErrors(current => ({ ...current, [id]: '' }));
    try {
      const next = await request<SessionResponse>(`/api/sessions/${encodeURIComponent(id)}`, 'PATCH', body);
      setEngine(current => {
        const merged = mergeSessions(current.sessions, [next]);
        return { ...current, sessions: merged };
      });
      return null;
    } catch (error) {
      const failure = errorMessage(error);
      setSessionErrors(current => ({ ...current, [id]: failure }));
      return failure;
    } finally {
      pending.current.delete(id); setPendingSessions(current => ({ ...current, [id]: false }));
    }
  }
  async function applyModelSettings(target: Session, model: string, effort: string) {
    const id = target.id;
    if (pending.current.has(id) || target.status === 'running') return false;
    pending.current.add(id); setPendingSessions(current => ({ ...current, [id]: true }));
    setModelPreviews(current => ({ ...current, [id]: { model, effort } }));
    setSessionErrors(current => ({ ...current, [id]: '' }));
    try {
      const next = await request<SessionResponse>(`/api/sessions/${encodeURIComponent(id)}/settings`, 'PATCH', { model, effort });
      setEngine(current => ({ ...current, sessions: mergeSessions(current.sessions, [next]) }));
      return true;
    } catch (error) { setSessionErrors(current => ({ ...current, [id]: errorMessage(error) })); return false; }
    finally {
      pending.current.delete(id); setPendingSessions(current => ({ ...current, [id]: false }));
      setModelPreviews(current => { const next = { ...current }; delete next[id]; return next; });
    }
  }
  function acceptLocalDeliveries(confirmations: DeliveryConfirmation[]) {
    const current: ChatDraftState = {
      drafts: readDraftValue(chatDraftKeys.composers, {}),
      sources: readDraftValue(chatDraftKeys.sources, {}),
      submissions: readDraftValue(chatDraftKeys.submissions, {}),
    };
    const accepted = acceptChatDeliveries(current, confirmations);
    if (accepted === current) return;
    setDraftValues({ [chatDraftKeys.composers]: accepted.drafts, [chatDraftKeys.sources]: accepted.sources, [chatDraftKeys.submissions]: accepted.submissions });
  }
  function submitLocal(target: Session, message: LocalSubmission) {
    if (IS_DESKTOP && !draftStorageReady()) { setSessionErrors(current => ({ ...current, [target.id]: 'Load this engine’s drafts before sending a message.' })); return; }
    if (agentExecutionBlocked(target)) { setSessionErrors(current => ({ ...current, [target.id]: 'Agent settings need attention before sending.' })); return; }
    const identity = currentEngineIdentity();
    const { id, draft, sources, mode } = message;
    if (submissionRequests.current.has(id)) return;
    const finished = beginLocalActivity('Delivering message');
    submissionRequests.current.add(id);
    if (target.role === 'general_agent') generalHarnessBlocks.current.set(id, target.id);
    setLocalMessages(current => ({ ...current, [target.id]: (current[target.id] ?? []).map(item => item.id === id ? { ...item, status: 'sending' } : item) }));
    // Ordinary inputs keep their submission order. Send now bypasses slow
    // requests and invalidates older local sends that have not reached HTTP.
    if (mode === 'interrupt') interruptSubmissions(target.id);
    const previous = submissionTails.current.get(target.id) ?? Promise.resolve();
    const stopVersion = stopVersions.current.get(target.id) ?? 0;
    let dispatched = false;
    const task = previous.then(async () => {
      if (identity !== currentEngineIdentity()) throw new DOMException('Engine identity changed.', 'AbortError');
      if ((stopVersions.current.get(target.id) ?? 0) !== stopVersion) throw new EngineRequestError('Message paused by an interruption. Restore it when ready.', 409);
      if (IS_DESKTOP) await flushDrafts();
      if (identity !== currentEngineIdentity()) throw new DOMException('Engine identity changed.', 'AbortError');
      if ((stopVersions.current.get(target.id) ?? 0) !== stopVersion) throw new EngineRequestError('Message paused by an interruption. Restore it when ready.', 409);
      const controller = new AbortController();
      submissionControllers.current.set(id, { sessionId: target.id, controller });
      const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(45000)]);
      dispatched = true;
      return request<SessionResponse>(`/api/sessions/${encodeURIComponent(target.id)}/messages`, 'POST', { ...draft, sources, mode, clientId: id }, 45000, signal);
    })
      .then(next => {
        if (identity !== currentEngineIdentity()) return;
        setEngine(current => { const sessions = mergeSessions(current.sessions, [next], 'mutation'); return sessions === current.sessions ? current : { ...current, sessions }; });
        if (IS_DESKTOP) {
          acceptLocalDeliveries([{ sessionId: target.id, parentSessionId: target.role === 'side_agent' ? target.parentId : undefined, ids: [id] }]);
        } else setLocalMessages(current => ({ ...current, [target.id]: (current[target.id] ?? []).map(item => item.id === id ? { ...item, status: 'accepted' } : item) }));
        setSessionErrors(current => ({ ...current, [target.id]: '' }));
      }).catch(error => {
        if (identity !== currentEngineIdentity()) return;
        // Queue/history may have confirmed this delivery while POST was pending.
        if (IS_DESKTOP && !(readDraftValue<Record<string, LocalSubmission[]>>(chatDraftKeys.submissions, {})[target.id] ?? []).some(item => item.id === id)) return;
        const status = !dispatched || error instanceof EngineRequestError && error.status >= 400 && error.status < 500 && error.status !== 408 ? 'failed' : 'uncertain';
        if (status === 'failed') generalHarnessBlocks.current.delete(id);
        setLocalMessages(current => ({ ...current, [target.id]: (current[target.id] ?? []).map(item => item.id === id ? { ...item, status } : item) }));
        setSessionErrors(current => ({ ...current, [target.id]: errorMessage(error) }));
      }).finally(() => {
        finished();
        if (identity !== currentEngineIdentity()) return;
        submissionRequests.current.delete(id);
        submissionControllers.current.delete(id);
        if (submissionTails.current.get(target.id) === task) submissionTails.current.delete(target.id);
      });
    submissionTails.current.set(target.id, task);
  }
  function sendMessage(target: Session, draft: ComposerDraft, sources: SourceReference[], mode: SendMode = 'steer', composerKey = target.id) {
    if (agentExecutionBlocked(target)) return false;
    if (target.automation?.status === 'running') mode = 'steer';
    if (target.role === 'general_agent' && pending.current.has(target.id)) return false;
    if (IS_DESKTOP) {
      if (!loaded || !draftStorageReady()) return false;
      const current = readDraftValue<Record<string, LocalSubmission[]>>(chatDraftKeys.submissions, {});
      if ((current[target.id] ?? []).some(item => item.status !== 'failed' && sameComposerDraft(item.draft, draft))) {
        setSessionErrors(current => ({ ...current, [target.id]: 'Resolve the pending message before sending this draft again.' }));
        return false;
      }
    }
    const message: LocalSubmission = { id: clientMessageId(), draft, sources, mode, status: 'sending', ...(IS_DESKTOP ? { composerKey } : {}) };
    setLocalMessages(current => ({ ...current, [target.id]: [...(current[target.id] ?? []), message] }));
    submitLocal(target, message);
    return true;
  }
  function restoreLocal(target: Session, message: LocalSubmission, key = target.id) {
    if ((drafts[key]?.text || drafts[key]?.mentions.length) && !(IS_DESKTOP && sameComposerDraft(drafts[key], message.draft))) {
      setSessionErrors(current => ({ ...current, [target.id]: 'Clear the current draft before restoring this message.' }));
      return;
    }
    setDrafts(current => ({ ...current, [key]: IS_DESKTOP ? { ...message.draft, mentions: [...message.draft.mentions] } : message.draft }));
    if (message.sources.length && key.startsWith('side:')) setSideSources(current => {
      const previous = current[key.slice(5)] ?? [];
      const added = IS_DESKTOP ? message.sources.filter(source => !previous.some(item => item.sessionId === source.sessionId && item.messageId === source.messageId && item.passage === source.passage)) : message.sources;
      return { ...current, [key.slice(5)]: [...added, ...previous] };
    });
    generalHarnessBlocks.current.delete(message.id);
    setLocalMessages(current => {
      const remaining = (current[target.id] ?? []).filter(item => item.id !== message.id);
      const next = { ...current, [target.id]: remaining };
      if (IS_DESKTOP && !remaining.length) delete next[target.id];
      return next;
    });
  }
  function send(draft: ComposerDraft, mode?: SendMode) {
    if (!session || !draft.text.trim()) return false;
    return sendMessage(session, draft, [], mode);
  }
  function receiveSession(snapshot: SessionResponse) {
    setEngine(current => ({ ...current, sessions: mergeSessions(current.sessions, [snapshot], 'kind' in snapshot && snapshot.kind === 'reset' && !!snapshot.history) }));
  }
  function receiveHistory(page: EventPage) {
    setEngine(current => ({ ...current, sessions: prependHistory(current.sessions, page) }));
  }
  function receiveEvent(sessionId: string, event: EngineEvent) {
    setEngine(current => ({ ...current, sessions: current.sessions.map(session => session.id === sessionId ? { ...session, events: session.events.map(item => item.id === event.id ? event : item) } : session) }));
  }
  function graphRequestResolved(conversationId: string) {
    // Callback responses describe the private node session, not a public chat.
    void getConversationGraph(conversationId).then(applyGraph, error => setGraphErrors(current => ({ ...current, [conversationId]: errorMessage(error) })));
  }
  function openSubagent(sessionId: string) {
    if (!activeId) return;
    if (IS_MOBILE_HOST) {
      pushMobileChatSheet(activeId, sessionId);
      return;
    }
    setOpenSubagentTabs(current => ({
      ...current,
      [activeId]: (current[activeId] ?? []).includes(sessionId) ? current[activeId] : [...(current[activeId] ?? []), sessionId],
    }));
    setSessionTabs(current => ({ ...current, [activeId]: `subagent:${sessionId}` }));
  }
  function closeSubagent(sessionId: string) {
    setOpenSubagentTabs(current => ({ ...current, [activeId]: (current[activeId] ?? []).filter(id => id !== sessionId) }));
    setSessionTabs(current => current[activeId] === `subagent:${sessionId}` ? { ...current, [activeId]: 'chat' } : current);
  }
  async function ensureSide(main: Session) {
    if (engineChangePending()) return;
    if (sessions.some(item => item.parentId === main.id && item.role === 'side_agent') || openingSide.current.has(main.id)) return;
    openingSide.current.add(main.id);
    setSideErrors(current => ({ ...current, [main.id]: '' }));
    try { receiveSession(await request<SessionResponse>(`/api/sessions/${encodeURIComponent(main.id)}/side`, 'POST', {})); }
    catch (error) { setSideErrors(current => ({ ...current, [main.id]: errorMessage(error) })); }
    finally { openingSide.current.delete(main.id); }
  }
  function askSide(messageId: string, passage: string) {
    if (!session) return;
    setSideSources(current => ({ ...current, [session.id]: [...(current[session.id] ?? []), { sessionId: session.id, messageId, passage }] }));
    setSideOpen(true); setSidebarOpen(false);
  }
  function sendSide(draft: ComposerDraft, mode?: SendMode) {
    if (!sideSession) return false;
    const mainId = activeId;
    const sources = sideSources[mainId] ?? [];
    if (!IS_DESKTOP) setSideSources(current => ({ ...current, [mainId]: (current[mainId] ?? []).filter(source => !sources.includes(source)) }));
    return sendMessage(sideSession, draft, sources, mode, `side:${mainId}`);
  }
  async function exportSession() {
    if (!session) return;
    const id = session.id;
    try {
      const ticket = await request<{ path: string }>(`/api/sessions/${encodeURIComponent(id)}/export-downloads`, 'POST', {});
      if (!/^\/api\/content-downloads\/[A-Za-z0-9_-]{43}\?contract=3$/.test(ticket.path)) throw new Error('Invalid export response.');
      await openContentDownload(ENGINE_URL + ticket.path);
    } catch (error) { setSessionErrors(current => ({ ...current, [id]: errorMessage(error) })); }
  }
  function selectProject(next: Project) {
    navigation.current += 1;
    setMobileProjectRailOpen(false);
    setMobileNavigationOpen(true);
    setWorkspaceNavigation(current => ({ ...current, activeProjectId: next.id })); setView('chat'); setSidebarOpen(false);
  }
  function pushMobileChatSheet(sessionId: string, subagentId?: string) {
    const sheet: MobileChatSheet = { id: clientMessageId(), sessionId, subagentId };
    setMobileChatSheets(current => [...current, sheet]);
  }
  function closeMobileChatSheet(id: string) {
    setMobileChatSheets(current => current.filter(sheet => sheet.id !== id));
  }
  function openSessionOverlay(target: Session) {
    receiveSession(target);
    if (IS_MOBILE_HOST) {
      pushMobileChatSheet(target.id);
      return;
    }
    setOverlaySessionId(target.id);
    setSessionTabs(current => ({ ...current, [target.id]: 'chat' }));
  }
  function openSessionInProject(target: Session) {
    receiveSession(target);
    if (target.role === 'general_agent') {
      navigation.current += 1;
      setMobileProjectRailOpen(false);
      setOverlaySessionId(''); setSidebarOpen(false); setView('general');
      setSessionTabs(current => ({ ...current, [target.id]: 'chat' }));
      setFocusMinimized(false); setFocusFront('main');
      return;
    }
    const owner = projects.find(item => item.id === target.projectId);
    if (!owner || target.archived || topicArchived(owner, target)) return;
    navigation.current += 1;
    setMobileNavigationOpen(false);
    setWorkspaceNavigation(current => ({
      ...current, activeProjectId: owner.id,
      selectedSessions: { ...current.selectedSessions, [owner.id]: target.id },
      collapsedTopics: { ...current.collapsedTopics, [owner.id]: { ...topicCollapseState(current, owner), [topicKey(target)]: false } },
    }));
    setSessionTabs(current => ({ ...current, [target.id]: 'chat' }));
    setOverlaySessionId(''); setSidebarOpen(false); setView('chat');
    setFocusMinimized(false); setFocusFront('main');
  }
  function openFloatingSession(target: Session) {
    receiveSession(target);
    setFloatingSessionIds(ids => ids.includes(target.id) ? ids : [...ids, target.id]);
    setFloatingSubagentIds(current => { if (!current[target.id]) return current; const next = { ...current }; delete next[target.id]; return next; });
    setFocusMaximized(false);
    setFocusFront(target.id);
  }
  async function openGeneralFloatingWindow() {
    if (openingGeneralWindowRef.current) return;
    setGeneralWindowError('');
    if (generalSession) { openFloatingSession(generalSession); return; }
    openingGeneralWindowRef.current = true;
    setOpeningGeneralWindow(true);
    try {
      const next = await request<SessionResponse>('/api/general-session', 'POST', {});
      receiveSession(next);
      const target = 'summary' in next ? next.summary : next;
      setFloatingSessionIds(ids => ids.includes(target.id) ? ids : [...ids, target.id]);
      setFocusMaximized(false); setFocusFront(target.id);
    } catch (error) { setGeneralWindowError(errorMessage(error)); }
    finally { openingGeneralWindowRef.current = false; setOpeningGeneralWindow(false); }
  }
  function openProjectEditor(target: Project) {
    navigation.current += 1;
    setEditingProject(IS_DESKTOP ? { id: target.id, name: target.name, folder: target.folder, icon: target.icon, folders: [], archivedFolders: [] } : target); setSidebarOpen(false);
  }
  function openProjectSettings(target: Project, section?: ProjectSettingsSection) {
    navigation.current += 1;
    let nextSection = section ?? 'general';
    if (IS_DESKTOP) {
      const key = `ui.projectSettingsSection:${target.id}`;
      const saved = readDraftValue<unknown>(key, undefined);
      if (section === undefined && (saved === 'general' || saved === 'webhooks' || saved === 'tasks' || saved === 'automations')) nextSection = saved;
      else setDraftValues({ [key]: nextSection });
    }
    setProjectSettingsSection(nextSection); setProjectSettingsId(target.id); setSidebarOpen(false);
  }
  function openProjectPicker() {
    navigation.current += 1;
    setSidebarOpen(false);
    setAddingProject('');
  }
  async function addProject(input: Omit<Project, 'id' | 'folders' | 'archivedFolders'>): Promise<string | null> {
    const selection = navigation.current;
    try {
      const next = await request<Project>('/api/projects', 'POST', input);
      projectRevision.current += 1;
      setEngine(current => ({ ...current, projects: [...current.projects.filter(item => item.id !== next.id), next] }));
      if (navigation.current === selection) { setWorkspaceNavigation(current => ({ ...current, activeProjectId: next.id })); setView('chat'); setMobileNavigationOpen(true); }
      return null;
    } catch (error) { return errorMessage(error); }
  }
  async function editProject(projectId: string, input: Pick<Project, 'name' | 'icon'>): Promise<string | null> {
    try {
      if (IS_DESKTOP && editingProject?.id === projectId) {
        const latest = projects.find(project => project.id === projectId);
        if (!latest) return 'Project is unavailable. The draft has been retained.';
        if (latest.name !== editingProject.name || latest.icon !== editingProject.icon) return 'Project changed. Cancel this edit and reopen it to review the saved values.';
      }
      const next = await request<Project>(`/api/projects/${encodeURIComponent(projectId)}`, 'PATCH', { name: input.name, icon: input.icon });
      projectRevision.current += 1;
      setEngine(current => ({ ...current, projects: current.projects.map(item => item.id === projectId ? next : item) }));
      return null;
    } catch (error) { return errorMessage(error); }
  }
  async function reorderProject(target: Project, beforeId: string): Promise<boolean> {
    try {
      const next = await request<Project>(`/api/projects/${encodeURIComponent(target.id)}`, 'PATCH', { position: { beforeId } });
      projectRevision.current += 1;
      setEngine(current => ({
        ...current,
        projects: moveBefore(current.projects.map(item => item.id === target.id ? next : item), target.id, beforeId),
      }));
      return true;
    } catch (error) {
      setConnectionError(errorMessage(error));
      return false;
    }
  }
  async function removeProject(projectId: string): Promise<string | null> {
    try {
      await request(`/api/projects/${encodeURIComponent(projectId)}`, 'DELETE');
      projectRevision.current += 1;
      setEngine(current => ({
        ...current,
        projects: current.projects.filter(item => item.id !== projectId),
        sessions: current.sessions.filter(item => item.projectId !== projectId),
      }));
      setWorkspaceNavigation(current => ({ ...current, activeProjectId: current.activeProjectId === projectId ? '' : current.activeProjectId }));
      return null;
    } catch (error) { return errorMessage(error); }
  }
  async function createSession(input: SessionCreateInput): Promise<string | null> {
    const selection = navigation.current;
    try {
      const next = await request<SessionResponse>('/api/sessions', 'POST', input, 60000, AbortSignal.timeout(60000));
      const created = 'summary' in next ? next.summary : next;
      setEngine(current => ({ ...current, sessions: mergeSessions(current.sessions, [next]) }));
      if (navigation.current === selection) {
        setWorkspaceNavigation(current => ({ ...current, activeProjectId: created.projectId, selectedSessions: { ...current.selectedSessions, [created.projectId]: created.id }, collapsedTopics: { ...current.collapsedTopics, [created.projectId]: { ...topicCollapseState(current, projects.find(project => project.id === created.projectId) ?? { id: created.projectId, topics: [] }), [created.topicId ?? '']: false } } }));
        setMobileNavigationOpen(false);
        setView('chat');
      }
      return null;
    } catch (error) { return errorMessage(error); }
  }
  async function chooseSessionTopic(target: Session, topicId: string): Promise<string | null> {
    const failure = await patchSession(target, { topicId });
    const owner = projects.find(item => item.id === target.projectId);
    if (!failure && owner) setWorkspaceNavigation(current => ({ ...current, collapsedTopics: { ...current.collapsedTopics, [owner.id]: { ...topicCollapseState(current, owner), [topicId]: false } } }));
    return failure;
  }
  async function createTopic(projectId: string, input: Pick<Topic, 'name' | 'color'>, target?: Session): Promise<string | null> {
    if (target && pending.current.has(target.id)) return 'Another session update is still in progress.';
    if (target) { pending.current.add(target.id); setPendingSessions(current => ({ ...current, [target.id]: true })); }
    try {
      const result = await request<{ topic: Topic; sessionId?: string }>(`/api/projects/${encodeURIComponent(projectId)}/topics`, 'POST', { ...input, ...(target ? { sessionId: target.id } : {}) });
      const previous = projects.find(p => p.id === projectId)!;
      const next = { ...previous, topics: [result.topic, ...(previous.topics ?? []).filter(t => t.id !== result.topic.id)], topicOrder: [result.topic.id, ...(previous.topicOrder ?? []).filter(id => id !== result.topic.id)] };
      const assignedSession = result.sessionId ? await request<SessionResponse>(`/api/sessions/${encodeURIComponent(result.sessionId)}`) : undefined;
      projectRevision.current += 1;
      setEngine(current => ({ ...current, projects: current.projects.map(item => item.id === projectId ? next : item), sessions: assignedSession ? mergeSessions(current.sessions, [assignedSession]) : current.sessions }));
      if (assignedSession) {
        const assigned = 'summary' in assignedSession ? assignedSession.summary : assignedSession;
        if (assigned.topicId) {
          const topicId = assigned.topicId;
          setWorkspaceNavigation(current => ({ ...current, collapsedTopics: { ...current.collapsedTopics, [projectId]: { ...topicCollapseState(current, next), [topicId]: false } } }));
        }
      }
      return null;
    } catch (error) { return errorMessage(error); }
    finally { if (target) { pending.current.delete(target.id); setPendingSessions(current => ({ ...current, [target.id]: false })); } }
  }
  async function patchTopic(projectId: string, topic: Topic, input: Partial<Pick<Topic, 'name' | 'color' | 'archived'>> & { position?: { beforeId: string } }): Promise<string | null> {
    try {
      const updated = await request<Topic>(`/api/projects/${encodeURIComponent(projectId)}/topics/${encodeURIComponent(topic.id)}`, 'PATCH', input);
      const previous = projects.find(p => p.id === projectId)!;
      const next = { ...previous, topics: previous.topics?.map(t => t.id === updated.id ? { ...t, ...updated } : t) };
      if (input.position) next.topicOrder = moveBefore((previous.topicOrder ?? previous.topics?.map(t => t.id) ?? []).map(id => ({ id })), updated.id, input.position.beforeId).map(t => t.id);
      projectRevision.current += 1;
      setEngine(current => ({ ...current, projects: current.projects.map(item => item.id === projectId ? next : item) }));
      return null;
    } catch (error) { return errorMessage(error); }
  }

  const composerGraphControl = session && !generalConversation ? <GraphPicker key={session.id} graphs={catalogGraphs} value={selectedGraphId} selectedName={catalogSelectedGraph?.definition.name ?? selectedGraph?.definition.name} running={graphRunInProgress} disabled={session.automation?.status === 'running' || !!selectingGraphs[session.id] || !session.graph} loading={catalogLoading} error={catalogError || catalog?.errors.join(' ')} onReload={() => void reloadCatalog().catch(() => {})} onChange={graphId => void chooseGraph(session.id, graphId)} /> : undefined;
  const tabItems: { value: SessionTab; label: string; onClose?: () => void; closeLabel?: string }[] = [{ value: 'chat', label: 'Chat' }];
  if (selectedGraphId) tabItems.push({ value: 'graph', label: 'Graph' });
  for (const id of openSubagentIds) {
    const child = sessions.find(item => item.id === id && item.parentId === activeId && item.role === 'subagent');
    const source = session?.events.find(event => event.type === 'subagent' && event.data?.childSessionId === id);
    const label = child?.title || source?.title || 'Subagent';
    tabItems.push({ value: `subagent:${id}`, label, closeLabel: `Close ${label}`, onClose: () => closeSubagent(id) });
  }

  if (IS_DESKTOP && (!loaded || !draftStorage.ready)) return <main className="app-shell"><section className="empty-chat"><h2>{draftStorage.error || connectionError || 'Loading engine and drafts…'}</h2>{(draftStorage.error || connectionError) && <Button onClick={() => setRefreshVersion(value => value + 1)}>Retry</Button>}</section></main>;

  return <ActivityPagingContext.Provider value={{ hasMore: !!activityNext, older: !!activityCursor, next: () => setActivityCursor(activityNext), latest: () => setActivityCursor('') }}><SessionNavigationProvider key={engineIdentity} sessions={sessions} projects={projects} onOpenSession={IS_MOBILE_HOST ? openSessionOverlay : openSessionInProject}><div ref={shell} className={`app-shell ${focusMaximized ? 'is-maximized' : ''} ${focusMinimized && focusEnabled ? 'is-minimized' : ''} ${sidebarOpen ? 'sidebar-open' : ''} ${sideVisible && session ? 'side-agent-open' : ''}`}
    data-mobile-screen={mobileNavigationVisible ? 'sessions' : mobileSessionVisible ? 'chat' : undefined}
    data-mobile-project-rail={IS_MOBILE_HOST && generalView ? mobileProjectRailOpen ? 'open' : 'hidden' : undefined}
    style={{ ...(focusEnabled ? focusMainGeometry : undefined), zIndex: focusFront === 'main' ? 3 : 1 }} data-resized={focusEnabled && focusMainGeometry ? true : undefined} inert={focusMinimized && focusEnabled}
    onPointerDownCapture={() => setFocusFront('main')} onFocusCapture={() => setFocusFront('main')}
    onPointerDown={event => {
      const target = event.target as HTMLElement;
      if (event.button !== 0 || !event.isPrimary || !target.closest('.session-header') || target.closest('button, a, input, select, textarea')) return;
      if (IS_DESKTOP) { void startWindowDrag().catch(error => setFocusError(errorMessage(error))); return; }
      if (IS_MOBILE_HOST || focusMaximized || window.innerWidth <= 760) return;
      const element = event.currentTarget;
      const bounds = element.getBoundingClientRect();
      drag.current = {
        pointerId: event.pointerId, x: event.clientX, y: event.clientY,
        left: parseFloat(element.style.left) || 0, top: parseFloat(element.style.top) || 0,
        minX: bounds.width > window.innerWidth - 16 ? 80 - bounds.right : 8 - bounds.left,
        maxX: bounds.width > window.innerWidth - 16 ? window.innerWidth - bounds.left - 80 : window.innerWidth - bounds.right - 8,
        minY: 8 - bounds.top,
        maxY: bounds.height > window.innerHeight - 16 ? window.innerHeight - bounds.top - 56 : window.innerHeight - bounds.bottom - 8,
      };
      element.setPointerCapture(event.pointerId);
      element.dataset.dragging = 'true';
      event.preventDefault();
    }}
    onPointerMove={event => {
      const start = drag.current;
      if (!start || start.pointerId !== event.pointerId) return;
      const dx = Math.max(start.minX, Math.min(start.maxX, event.clientX - start.x));
      const dy = Math.max(start.minY, Math.min(start.maxY, event.clientY - start.y));
      event.currentTarget.style.left = `${start.left + dx}px`;
      event.currentTarget.style.top = `${start.top + dy}px`;
    }}
    onPointerUp={event => {
      if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    }}
    onLostPointerCapture={() => { if (drag.current) saveMainGeometry(); drag.current = null; if (shell.current) delete shell.current.dataset.dragging; }}
    onPointerCancel={() => { if (drag.current) saveMainGeometry(); drag.current = null; if (shell.current) delete shell.current.dataset.dragging; }}
  >
    <ProjectRail projects={projects} sessions={sessions} activeId={boardView ? boardProjectId : generalView ? '' : project?.id ?? ''} generalActive={generalView} onGeneralAgent={() => { navigation.current += 1; setOverlaySessionId(''); setSidebarOpen(false); setMobileProjectRailOpen(false); setView('general'); }} onSelect={selectProject} onEdit={openProjectEditor} onRemove={target => removeProject(target.id)} onReorder={reorderProject} onAdd={() => void openProjectPicker()} onSettings={() => { setSidebarOpen(false); setSettingsOpen(true); }} />
    {!generalView && sidebarOpen && <button className="panel-backdrop" aria-label="Close side panel" onClick={() => setSidebarOpen(false)} />}
    {!generalView && !boardView && project && <WorkspaceSidebar key={`sidebar:${project.id}`} project={project} sessions={projectSessions} activeId={view === 'chat' ? activeId : ''} activeSection={view} collapsed={topicCollapseState(workspaceNavigation, project)} archivedCollapsed={collapsedArchivedProjects.includes(project.id)} onSearchChange={setProjectSearch}
      paging={<>{projectWindow.current.start && <Button size="sm" onClick={() => { projectWindow.current.start = ''; projectWindow.current.pages = 1; setRefreshVersion(n => n + 1); }}>Newest sessions</Button>}{projectPage.id === project.id && projectPage.next && <Button size="sm" disabled={projectPage.busy} onClick={() => void loadMoreProject()}>Load more sessions</Button>}{topicWindow.current.cursor && <Button size="sm" onClick={() => { topicWindow.current.cursor = ''; setRefreshVersion(n => n + 1); }}>First topics</Button>}{projectPage.id === project.id && projectPage.topicsNext && <Button size="sm" disabled={projectPage.busy} onClick={() => void loadMoreProject(true)}>Next topics</Button>}</>}
      onCollapsedChange={topics => setWorkspaceNavigation(current => ({ ...current, collapsedTopics: { ...current.collapsedTopics, [project.id]: typeof topics === 'function' ? topics(topicCollapseState(current, project)) : topics } }))}
      onArchivedCollapsedChange={collapsed => setWorkspaceNavigation(current => ({ ...current, collapsedArchivedProjects: collapsed ? [...new Set([...current.collapsedArchivedProjects, project.id])] : current.collapsedArchivedProjects.filter(id => id !== project.id) }))}
      onNew={newSession} onRename={setRenamingSession} onEditTopic={topic => setEditingTopic({ projectId: project.id, topic })}
      onCreateTopic={() => setCreatingTopicProjectId(project.id)}
      onArchiveSession={async (target, archived) => (await patchSession(target, { archived })) === null}
      onArchiveTopic={async (topic, archived) => (await patchTopic(project.id, topic, { archived })) === null}
      onMoveSession={async (target, topic) => (await patchSession(target, { topicId: topic.id })) === null}
      onReorderTopic={async (topic, beforeId) => (await patchTopic(project.id, topic, { position: { beforeId } })) === null}
      onSelect={id => { navigation.current += 1; setWorkspaceNavigation(current => ({ ...current, selectedSessions: { ...current.selectedSessions, [project.id]: id } })); setView('chat'); setSidebarOpen(false); setMobileNavigationOpen(false); }}
      onClose={() => setSidebarOpen(false)} onAgents={() => { navigation.current += 1; setAgentsVisit(current => current + 1); setView('agents'); setSidebarOpen(false); }} onGraphs={() => { navigation.current += 1; setGraphsVisit(current => current + 1); setView('graphs'); setSidebarOpen(false); }} onProjectSettings={openProjectSettings} onActivityBoard={() => openBoard(project.id)} hasTaskProvider={tasksBoard.config?.connections.some(c => c.projectId === project.id) ?? false} />}
    <main className="main-panel">
      {!(IS_MOBILE_HOST && generalView) && <header className="session-header"><div id="workspace-header-leading" className="header-leading">{!generalView && !boardView && project && (IS_MOBILE_HOST && view === 'chat' && session ? <IconButton label="Back to sessions" className="mobile-session-back" onClick={() => { navigation.current += 1; setSidebarOpen(false); setMobileNavigationOpen(true); }}><ArrowLeft /></IconButton> : <IconButton label="Open sessions" className="mobile-nav" onClick={() => setSidebarOpen(true)}><PanelLeft /></IconButton>)}<h1>{boardView ? 'Tasks' : generalView ? 'KLM Agent' : view === 'chat' && session ? <button className="session-title-button" title="Rename session" onClick={() => setRenamingSession(session)}>{session.title}</button> : view === 'design' ? 'Design system' : view === 'agents' ? 'Agents' : view === 'graphs' ? 'Graphs' : project?.name ?? 'KLM'}</h1>{view === 'chat' && session && gitBranch && <span className="mode-label session-header-branch" title={gitBranch}><GitBranch aria-hidden="true" /><span>{gitBranch}</span></span>}</div><div id="workspace-header-actions" className="header-actions">
        {session && chatView && <Button size="sm" className="export-button" onClick={exportSession}>Session log<Download /></Button>}
        {focusEnabled && <IconButton label="Minimize window" className="focus-minimize-button" onClick={() => setFocusMinimized(true)}><Minus /></IconButton>}
        {IS_DESKTOP ? <IconButton label="Focus" onClick={() => void focus()}><Focus /></IconButton> : !IS_MOBILE_HOST && <IconButton label={focusMaximized ? 'Restore window' : 'Maximize window'} aria-pressed={focusMaximized} onClick={() => setFocusMaximized(current => !current)}>{focusMaximized ? <Minimize2 /> : <Maximize2 />}</IconButton>}
        {session && view === 'chat' && <IconButton label={sideOpen ? 'Close side agent' : 'Open side agent'} aria-expanded={sideOpen} onClick={() => { setSideOpen(!sideOpen); setSidebarOpen(false); }}><PanelRight /></IconButton>}
      </div></header>}
      {focusError && <div role="alert" className="storage-error">{focusError} <Button size="sm" onClick={() => void focus()}>Retry</Button></div>}
      {generalWindowError && !focusMinimized && <div role="alert" className="storage-error">{generalWindowError} <Button size="sm" disabled={openingGeneralWindow} onClick={() => void openGeneralFloatingWindow()}>Retry</Button></div>}
      {connectionError && <div role="alert" className="storage-error">{connectionError} <Button size="sm" onClick={() => setRefreshVersion(current => current + 1)}>Retry</Button></div>}
      {draftStorage.error && <div role="alert" className="storage-error">{draftStorage.error} Keep this window open. <Button size="sm" onClick={() => { if (!draftStorage.ready) setRefreshVersion(current => current + 1); else void flushDrafts().catch(() => {}); }}>Retry saving drafts</Button></div>}
      {boardView && <Suspense fallback={<p role="status">Loading tasks…</p>}><ActivityBoardPage projects={projects} projectId={boardProjectId} visible={!focusMinimized || !focusEnabled} board={tasksBoard} onProjectChange={setBoardProjectId} onBack={() => setView(boardOrigin.current)} onConnections={id => { const target = projects.find(p => p.id === id); if (target) openProjectSettings(target, 'tasks'); }} /></Suspense>}
      <KlmAgentWorkspace enabled={generalView} visible={!focusMinimized || !focusEnabled} projects={projects} sessions={sessions} onChatVisibility={setGeneralChatVisible} projectRailOpen={mobileProjectRailOpen} onToggleProjectRail={IS_MOBILE_HOST ? () => setMobileProjectRailOpen(current => !current) : undefined} overlaySessionId={IS_MOBILE_HOST ? mobileChats.at(-1)?.session.id : overlaySession?.id} onOpenOverlay={openSessionOverlay} onCloseOverlay={() => setOverlaySessionId('')} onOpenProject={openSessionInProject} onOpenFloatingChat={focusEnabled ? () => void openGeneralFloatingWindow() : undefined} openingFloatingChat={openingGeneralWindow} onOpenBoard={() => openBoard()}>
      {(view === 'chat' || view === 'design') && <div className="session-navigation"><TabNav<SessionTab> value={view === 'design' ? 'chat' : activeTab} onChange={tab => { setView('chat'); setSessionTabs(current => ({ ...current, [activeId]: tab })); }} items={view === 'chat' ? tabItems : [{ value: 'chat', label: 'Chat' }]} />{view === 'chat' && project && session && <>{focusEnabled && !projectSessionArchived && <IconButton className="session-open-floating" label="Open session in floating window and minimize main window" onClick={() => { openFloatingSession(session); setFocusMinimized(true); }}><PanelsTopLeft /></IconButton>}<button className={`topic-tag topic-color-${currentTopic?.color ?? 'neutral'}`} title={projectSessionArchived ? `Edit topic: ${currentTopic?.name ?? 'Ungrouped'}` : 'Change topic'} aria-haspopup="dialog" disabled={!!connectionError || projectSessionArchived && !currentTopic} onClick={() => { if (projectSessionArchived && currentTopic) setEditingTopic({ projectId: project.id, topic: currentTopic }); else setChoosingTopicSessionId(session.id); }}>{currentTopic?.name ?? 'Choose topic'}</button></>}</div>}
      {boardView ? null : view === 'design' ? <DesignSystem /> : (view === 'agents' || view === 'graphs') && project ? <ProjectAuthoring key={`${project.id}:${view}:${agentsVisit}:${graphsVisit}`} projectId={project.id} area={view} /> : !loaded ? <div className="empty-chat"><h2>{connectionError ? 'Engine disconnected' : 'Connecting to engine...'}</h2></div> : generalView && !session ? <div className="empty-chat"><h2>{generalError || 'Opening KLM Agent...'}</h2>{generalError && <Button onClick={() => setGeneralRetry(current => current + 1)}>Retry</Button>}</div> : !generalView && !project ? <div className="empty-chat"><h2>Add a project</h2><Button onClick={() => void openProjectPicker()}>Add project</Button></div> : !session ? <div className="empty-chat"><h2>Create a session</h2><Button onClick={() => newSession()} disabled={!!connectionError}>Create session</Button></div> : <>
        <SessionOverlay session={overlaySession} project={overlayProject} onClose={() => setOverlaySessionId('')} onOpenProject={openSessionInProject}
          background={overlaySession && generalSession ? <ConversationHistory session={generalSession} working={generalSession.status === 'running'} label="KLM Agent background conversation" onSnapshot={receiveSession} onHistory={receiveHistory} /> : null}>
        {generalView && (openSubagentIds.length > 0 || overlaySession && selectedGraphId) && <div className="session-navigation"><TabNav<SessionTab> value={activeTab} onChange={tab => setSessionTabs(current => ({ ...current, [activeId]: tab }))} items={tabItems} /></div>}
        {activeTab === 'chat' && <ConversationHistory key={session.id} session={session} subagents={sessions.filter(item => item.parentId === activeId && item.role === 'subagent')} working={working} label={overlaySession ? `${session.title} conversation` : generalConversation ? 'KLM Agent conversation' : 'Conversation'} onSnapshot={receiveSession} onHistory={receiveHistory} onEventChange={receiveEvent} onAskSide={generalView ? undefined : askSide} onOpenSubagent={openSubagent} empty={<div className="empty-chat"><div className="empty-chat-brand" role="img" aria-label={generalConversation ? 'KLM Agent' : 'KLM Harness'}><img className="empty-chat-brand-icon" src={klmIcon} alt="" /><span className="empty-chat-wordmark" aria-hidden="true"><span>K</span><span>L</span><span>M</span></span><Badge>{generalConversation ? 'Agent' : 'Harness'}</Badge></div><h2>What would you like to work on?</h2></div>} />}
        {activeTab.startsWith('subagent:') && <SubagentView session={activeSubagent} onSnapshot={receiveSession} onHistory={receiveHistory} />}
        {!generalConversation && (catalogError || !!catalog?.errors.length) && <div role="alert" className="storage-error">{catalogError || catalog?.errors.join(' ')} <Button size="sm" disabled={catalogLoading} onClick={() => void reloadCatalog().catch(() => {})}>Retry</Button></div>}
        {graphErrors[session.id] && <div role="alert" className="storage-error">{graphErrors[session.id]} <Button size="sm" onClick={() => setRefreshVersion(current => current + 1)}>Retry</Button></div>}
        {selectedGraph ? <Suspense fallback={activeTab === 'graph' ? <section className="graph-view-loading" aria-label="Loading graph canvas" aria-busy="true" /> : null}><GraphView key={`${session.id}:${selectedGraphId}`} sessionId={session.id} graph={selectedGraph} agents={catalog?.agents ?? []} visible={activeTab === 'graph'} run={graphRunInProgress ? graphRun : undefined} /></Suspense> : activeTab === 'graph' && <section className="graph-view-loading" role="status">{catalogLoading ? 'Loading graph...' : 'Selected graph is unavailable.'} <Button size="sm" disabled={catalogLoading} onClick={() => void reloadCatalog().catch(() => {})}>Reload</Button></section>}
        {sessionErrors[session.id] && <div role="alert" className="storage-error">{sessionErrors[session.id]}</div>}
        </SessionOverlay>
        <div className="composer-area" hidden={activeTab !== 'chat'}>
          {(awaitingPermission || awaitingQuestion || graphRequests.length > 0) && <div className="permission-queue">
            {session.permissions?.map(permission => <PermissionCard key={permission.id} permission={permission} sessionId={session.id} projectName={conversationProject?.name ?? ''} onResolved={snapshot => setEngine(current => ({ ...current, sessions: mergeSessions(current.sessions, [snapshot]) }))} />)}
            {session.questions?.map(question => <QuestionCard key={question.id} question={question} sessionId={session.id} onResolved={snapshot => setEngine(current => ({ ...current, sessions: mergeSessions(current.sessions, [snapshot]) }))} />)}
            {graphRequests.map(item => {
              const graphName = graphRun?.id === item.runId ? graphRun.snapshot.definition.name : catalog?.graphs.find(graph => graph.id === item.graphId)?.definition.name ?? item.graphId;
              const node = graphRun?.id === item.runId ? graphRun.snapshot.definition.nodes[item.nodeId] : undefined;
              const nodeName = node?.name || (node?.type === 'join' ? `Join · ${node.agent || item.nodeId}` : item.nodeName || item.nodeId);
              const origin = `${graphName || 'Graph'} · ${nodeName}`;
              const key = `${item.sessionId}:${item.kind}:${item.requestId}`;
              return item.kind === 'permission' && item.permission
                ? <PermissionCard key={key} permission={{ ...item.permission, id: item.requestId }} sessionId={item.sessionId} projectName={conversationProject?.name ?? ''} origin={origin} onResolved={() => graphRequestResolved(session.id)} />
                : item.kind === 'question' && item.question
                  ? <QuestionCard key={key} question={{ ...item.question, id: item.requestId }} sessionId={item.sessionId} origin={origin} onResolved={() => graphRequestResolved(session.id)} /> : null;
            })}
          </div>}
          <PendingSubmission items={localMessages[session.id] ?? []} onRestore={item => restoreLocal(session, item)} onRetry={item => submitLocal(session, item)} />
          <DelegationRequests key={`delegation-requests/${session.id}`} session={session} projectName={conversationProject?.name ?? ''} onSnapshot={receiveSession} />
          <GraphRequestPages key={`graph-requests/${session.id}`} session={session} projectName={conversationProject?.name ?? ''} onResolved={() => graphRequestResolved(session.id)} />
          <MessageQueue key={`queue/${session.id}`} session={session} disabled={!!pendingSessions[session.id]} onSnapshot={receiveSession} onInterrupt={interruptSubmissions} />
          {generalConversation && <GeneralHarnessPicker session={session} harnesses={harnesses} disabled={!!pendingSessions[session.id] || generalHarnessBlocked(session) || !!connectionError} onChange={harness => void updateSession(session, 'harness', { harness })} />}
          {generalView && !harnesses.some(harness => harness.id === session.harness && harness.available) && <p role="status" className="form-error">The selected harness is not installed.</p>}
          {!generalView && projectSessionArchived && <div className="archived-conversation-actions"><span>{currentTopic?.archived ? 'Topic archived' : 'Session archived'}</span><Button size="sm" onClick={async () => { if (currentTopic?.archived && project) { const failure = await patchTopic(project.id, currentTopic, { archived: false }); setSessionErrors(current => ({ ...current, [session.id]: failure ?? '' })); } else void patchSession(session, { archived: false }); }}>{currentTopic?.archived ? 'Restore topic' : 'Restore session'}</Button></div>}
          <MessageComposer key={session.id} clearOnSend={!IS_DESKTOP} session={session} onSnapshot={receiveSession} steeringOnly={session.automation?.status === 'running'} placeholder={overlaySession ? `Message ${session.title}` : undefined} projectId={session.projectId} draft={drafts[session.id] ?? { text: '', mentions: [] }} onDraftChange={draft => setDrafts(current => ({ ...current, [session.id]: draft }))} onSend={send} onTranscription={text => setDrafts(current => {
            const draft = current[session.id] ?? { text: '', mentions: [] };
            const separator = !draft.text || draft.text.endsWith('\n\n') ? '' : draft.text.endsWith('\n') ? '\n' : '\n\n';
            return { ...current, [session.id]: { ...draft, text: `${draft.text}${separator}${text.trim()}` } };
          })} disabled={!draftStorage.ready || !!pendingSessions[session.id] || !generalView && projectSessionArchived || generalView && !harnesses.some(harness => harness.id === session.harness && harness.available)} running={session.status === 'running'} onStop={() => void updateSession(session, 'stop', {})} graphControl={IS_MOBILE_HOST ? undefined : composerGraphControl} modelControl={<ModelPicker key={`${session.id}/${session.harness}`} session={session} disabled={!!pendingSessions[session.id] || working} optionsDisabled={!!pendingSessions[session.id] || !!connectionError} onSave={(model, effort) => applyModelSettings(session, model, effort)} onSnapshot={receiveSession} graphControl={IS_MOBILE_HOST ? composerGraphControl : undefined} />} />
          <SessionStatusBar session={session} />
        </div>
      </>}
      </KlmAgentWorkspace>
    </main>
    {sideVisible && project && session && <SideChatPanel key={session.id} onInterrupt={interruptSubmissions} session={sideSession} project={project} harnesses={harnesses}
      draft={drafts[`side:${session.id}`] ?? { text: '', mentions: [] }} sources={sideSources[session.id] ?? []} submissions={sideSession ? localMessages[sideSession.id] ?? [] : []} onRestoreSubmission={item => { if (sideSession) restoreLocal(sideSession, item, `side:${session.id}`); }} onRetrySubmission={item => { if (sideSession) submitLocal(sideSession, item); }}
       error={sideSession ? sessionErrors[sideSession.id] ?? '' : sideErrors[session.id] ?? ''} disconnected={false} pending={!!sideSession && !!pendingSessions[sideSession.id]}
      onClose={() => setSideOpen(false)} onRetry={() => void ensureSide(session)} onSnapshot={receiveSession} onHistory={receiveHistory} onEventChange={receiveEvent}
      onDraftChange={draft => setDrafts(current => ({ ...current, [`side:${session.id}`]: draft }))}
      onRemoveSource={index => setSideSources(current => ({ ...current, [session.id]: (current[session.id] ?? []).filter((_, i) => i !== index) }))}
      onSend={sendSide} onStop={() => { if (sideSession) void updateSession(sideSession, 'stop', {}); }}
      onHarness={harness => { if (sideSession) void updateSession(sideSession, 'harness', { harness }); }}
      onModel={(model, effort) => sideSession ? applyModelSettings(sideSession, model, effort) : Promise.resolve(false)} />}
    {!IS_DESKTOP && !IS_MOBILE_HOST && !focusMaximized && <FocusWindowResizeHandles onResizeEnd={saveMainGeometry} />}
    {addingProject !== null && <ProjectDialog initialFolder={addingProject} onSave={addProject} onClose={() => { navigation.current += 1; setAddingProject(null); }} />}
    {editingProject && <ProjectDialog key={`edit-project:${editingProject.id}`} initialFolder={editingProject.folder} project={editingProject} onSave={input => editProject(editingProject.id, input)} onClose={() => setEditingProject(null)} />}
    {settingsProject && <ProjectSettingsDialog key={`${settingsProject.id}:${projectSettingsSection}`} project={settingsProject} sessions={engine.sessions} initialSection={projectSettingsSection} tasks={tasksBoard} onSave={editProject} onRemove={removeProject} onOpenSession={async id => {
      const target = sessionsRef.current.find(s => s.id === id) ?? await request<Session>(`/api/sessions/${encodeURIComponent(id)}`);
      receiveSession(target); setProjectSettingsId(''); openSessionInProject(target);
    }} onClose={() => setProjectSettingsId('')} />}
    {automationStop && <StopAutomationDialog onDecision={confirmed => { const resolve = automationStop.resolve; setAutomationStop(undefined); resolve(confirmed); }} />}
    {creatingSession && createProject && <CreateSessionDialog key={`${createProject.id}:${creatingSession.topicId ?? ''}`} project={createProject} harnesses={harnesses} topicId={creatingSession.topicId} onCreate={createSession} onClose={() => { navigation.current += 1; setCreatingSession(null); }} />}
    {canChooseTopic && choosingTopicSession && choosingTopicProject && <TopicDialog key={`choose-topic:${choosingTopicSession.id}`} draftKey={`form.topic.choose:${choosingTopicProject.id}:${choosingTopicSession.id}`} topic={sessionTopic(choosingTopicProject, choosingTopicSession)} selection={{ projectId: choosingTopicProject.id, topics: choosingTopicProject.topics ?? [], onSelect: id => chooseSessionTopic(choosingTopicSession, id), onCreate: input => createTopic(choosingTopicProject.id, input, choosingTopicSession) }} onSave={input => {
      const topic = sessionTopic(choosingTopicProject, choosingTopicSession);
      return topic ? patchTopic(choosingTopicProject.id, topic, input) : Promise.resolve('Topic is unavailable. Close and try again.');
    }} onClose={() => setChoosingTopicSessionId('')} />}
    {creatingTopicProjectId && <TopicDialog key={`new-topic:${creatingTopicProjectId}`} draftKey={`form.topic.new:${creatingTopicProjectId}`} onSave={input => createTopic(creatingTopicProjectId, input)} onClose={() => setCreatingTopicProjectId('')} />}
    {editingTopic && <TopicDialog key={editingTopic.topic.id} projectId={editingTopic.projectId} draftKey={`form.topic.edit:${editingTopic.projectId}:${editingTopic.topic.id}`} topic={editingTopic.topic} onSave={input => patchTopic(editingTopic.projectId, editingTopic.topic, input)} onClose={() => setEditingTopic(null)} />}
  </div>
    {mobileChats.map(({ session: mobileChatSession, ...sheet }) => <BottomSheet key={sheet.id} title={sheet.title} className="mobile-session-chat-sheet" onClose={() => closeMobileChatSheet(sheet.id)}>
      <FocusSessionChat session={mobileChatSession} projectName={projects.find(owner => owner.id === mobileChatSession.projectId)?.name ?? ''} subagents={sessions.filter(item => item.parentId === mobileChatSession.id && item.role === 'subagent')}
        draft={drafts[mobileChatSession.id] ?? { text: '', mentions: [] }} pending={!!pendingSessions[mobileChatSession.id]} disconnected={!!connectionError} error={sessionErrors[mobileChatSession.id] || graphErrors[mobileChatSession.id]}
        submissions={localMessages[mobileChatSession.id] ?? []} onRestoreSubmission={item => restoreLocal(mobileChatSession, item)} onRetrySubmission={item => submitLocal(mobileChatSession, item)}
        onSnapshot={receiveSession} onHistory={receiveHistory} onEventChange={receiveEvent} onInterrupt={interruptSubmissions}
        onDraftChange={draft => setDrafts(current => ({ ...current, [mobileChatSession.id]: draft }))} onSend={(draft, mode) => sendMessage(mobileChatSession, draft, [], mode)} onStop={() => void updateSession(mobileChatSession, 'stop', {})}
        onTranscription={text => setDrafts(current => { const draft = current[mobileChatSession.id] ?? { text: '', mentions: [] }; return { ...current, [mobileChatSession.id]: { ...draft, text: `${draft.text}${draft.text ? '\n\n' : ''}${text.trim()}` } }; })}
        onModel={(model, effort) => applyModelSettings(mobileChatSession, model, effort)} onGraph={applyGraph} selectingGraph={!!selectingGraphs[mobileChatSession.id]}
        generalControls={mobileChatSession.role === 'general_agent' ? <GeneralHarnessPicker session={mobileChatSession} harnesses={harnesses} disabled={!!pendingSessions[mobileChatSession.id] || generalHarnessBlocked(mobileChatSession) || !!connectionError} onChange={harness => void updateSession(mobileChatSession, 'harness', { harness })} /> : undefined}
        harnessUnavailable={mobileChatSession.role === 'general_agent' && !harnesses.some(harness => harness.id === mobileChatSession.harness && harness.available)}
        selectedGraphId={graphPreview[mobileChatSession.id] ?? mobileChatSession.graph?.selectedGraphId ?? mobileChatSession.selectedGraphId ?? ''} onSelectGraph={id => void chooseGraph(mobileChatSession.id, id)}
        onOpenSession={openSessionOverlay} activeSubagentId={sheet.subagentId ?? ''} onOpenSubagent={id => pushMobileChatSheet(mobileChatSession.id, id)} onBackToSession={() => closeMobileChatSheet(sheet.id)} />
    </BottomSheet>)}
    {settingsOpen && <SettingsDialog onClose={() => setSettingsOpen(false)} onGeneralAgent={() => { navigation.current += 1; setSettingsOpen(false); setOverlaySessionId(''); setView('general'); setFocusMinimized(false); setFocusFront('main'); }} onDesignSystem={() => { setSettingsOpen(false); setView('design'); setFocusMinimized(false); setFocusFront('main'); }} />}
    {renamingSession && <RenameSessionDialog key={renamingSession.id} session={renamingSession} onRename={title => patchSession(renamingSession, { title })} onClose={() => setRenamingSession(null)} />}
    {!IS_DESKTOP && !IS_MOBILE_HOST && <><BackgroundPicker hidden={focusMaximized && !focusMinimized} />
      <FocusWorkspace visible={focusEnabled && (!focusMaximized || focusMinimized)} projects={projects} sessions={sessions} floatingSessions={floatingSessions} front={focusFront} onActivate={setFocusFront}
        onOpenMain={openSessionInProject} onOpenFloating={openFloatingSession} onCloseFloating={id => setFloatingSessionIds(ids => ids.filter(item => item !== id))} onRenameSession={setRenamingSession}
        minimized={focusMinimized} onSettings={() => { setSidebarOpen(false); setSettingsOpen(true); }} onRestore={() => { setFocusMinimized(false); setFocusFront('main'); }}
        onOpenGeneral={() => void openGeneralFloatingWindow()} openingGeneral={openingGeneralWindow} generalError={generalWindowError}
        renderSession={target => <FocusSessionChat onInterrupt={interruptSubmissions} session={target} projectName={projects.find(owner => owner.id === target.projectId)?.name ?? ''} subagents={sessions.filter(item => item.parentId === target.id && item.role === 'subagent')}
          draft={drafts[target.id] ?? { text: '', mentions: [] }} pending={!!pendingSessions[target.id]} disconnected={!!connectionError} error={sessionErrors[target.id] || graphErrors[target.id]}
          submissions={localMessages[target.id] ?? []} onRestoreSubmission={item => restoreLocal(target, item)} onRetrySubmission={item => submitLocal(target, item)}
          onSnapshot={receiveSession} onHistory={receiveHistory} onEventChange={receiveEvent}
          onDraftChange={draft => setDrafts(current => ({ ...current, [target.id]: draft }))} onSend={(draft, mode) => sendMessage(target, draft, [], mode)} onStop={() => void updateSession(target, 'stop', {})}
          onTranscription={text => setDrafts(current => { const draft = current[target.id] ?? { text: '', mentions: [] }; return { ...current, [target.id]: { ...draft, text: `${draft.text}${draft.text ? '\n\n' : ''}${text.trim()}` } }; })}
          onModel={(model, effort) => applyModelSettings(target, model, effort)} onGraph={applyGraph} selectingGraph={!!selectingGraphs[target.id]}
          generalControls={target.role === 'general_agent' ? <GeneralHarnessPicker session={target} harnesses={harnesses} disabled={!!pendingSessions[target.id] || generalHarnessBlocked(target) || !!connectionError} onChange={harness => void updateSession(target, 'harness', { harness })} /> : undefined}
          harnessUnavailable={target.role === 'general_agent' && !harnesses.some(harness => harness.id === target.harness && harness.available)}
          selectedGraphId={graphPreview[target.id] ?? target.graph?.selectedGraphId ?? target.selectedGraphId ?? ''} onSelectGraph={id => void chooseGraph(target.id, id)}
          onOpenSession={openFloatingSession} activeSubagentId={floatingSubagentIds[target.id] ?? ''}
          onOpenSubagent={id => setFloatingSubagentIds(current => ({ ...current, [target.id]: id }))}
          onBackToSession={() => setFloatingSubagentIds(current => { const next = { ...current }; delete next[target.id]; return next; })} />}
      />
    </>}
  </SessionNavigationProvider></ActivityPagingContext.Provider>;
}
