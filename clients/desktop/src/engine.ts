import type { AuthoringCatalog, GraphFile } from './features/graphs/files';
import { IS_DESKTOP, resolveEngineURL } from './platform';
import { clientFetch } from './clientAuth';
import type { ConversationCapabilities } from './features/library/api';
import { engineIdentityGeneration } from './engineIdentity';
import { beginLocalActivity } from './features/engines/localActivities';
import { draftStorageReady } from './features/drafts/draftStore';
import type { AgentConfigSummary } from './features/agents/types';

export const ENGINE_URL = resolveEngineURL();
export type Revision = string | number; // Numeric values are accepted only for existing local fixtures.
export const compareRevision = (a: Revision, b: Revision) => BigInt(a) < BigInt(b) ? -1 : BigInt(a) > BigInt(b) ? 1 : 0;

export type Project = {
  topics?: Topic[];
  topicOrder?: string[];
  id: string;
  name: string;
  folder: string;
  icon: string;
  folders: string[];
  archivedFolders: string[];
};

export type Topic = { id: string; name: string; color: 'neutral' | 'blue' | 'purple' | 'green' | 'amber' | 'rose' | 'cyan' | 'teal' | 'orange' | 'red'; createdAt: string; archived?: boolean; order?: number; revision?: Revision };

export type Harness = {
  id: 'pi' | 'opencode' | 'codex';
  name: string;
  available: boolean;
  error?: string;
  installedVersion?: string;
  requiredVersion?: string;
  path?: string;
  source?: string;
  compatible?: boolean;
  executionSupported?: boolean;
  executionReason?: string;
};

export type HarnessInstallPlan = {
  token: string; harnessId: Harness['id']; package: string; version: string; currentVersion: string;
  destination: string; replace: boolean; noop: boolean; blocked?: string;
  prerequisites: { node: string; npm: string; version: string; npmVersion: string; prefix: string; root: string; bootstrap: boolean; error?: string };
};
export type HarnessInstallJob = {
  id: string; operationId: string; harnessId: Harness['id']; planToken: string;
  state: 'checking' | 'installing_prerequisites' | 'installing_harness' | 'verifying' | 'completed' | 'failed' | 'interrupted' | 'needs_permission';
  step: string; log: string[]; error?: string; createdAt: string; updatedAt: string;
};
export type HarnessInventory = { harnesses: Harness[]; plans: HarnessInstallPlan[]; jobs: HarnessInstallJob[]; activeJobId: string; host: string; platform: string };
export type HarnessInstallRequest = { operationId: string; planToken: string; confirmGlobal: boolean; confirmReplacement: boolean; confirmPrerequisites: boolean };
export async function getHarnessInventory(cached = false, signal?: AbortSignal): Promise<HarnessInventory> {
  const result = await request<HarnessInventory>(`/api/harnesses${cached ? '?cached=true' : ''}`, 'GET', undefined, 65000, signal);
  if (!Array.isArray(result.harnesses)) throw new Error('The engine returned an invalid harness inventory. Refresh and retry.');
  if (result.jobs != null && !Array.isArray(result.jobs) || result.plans != null && !Array.isArray(result.plans)) throw new Error('The engine returned invalid installation history. Refresh and retry.');
  return { ...result, jobs: result.jobs ?? [], plans: result.plans ?? [] };
}
export const installHarness = (id: Harness['id'], body: HarnessInstallRequest) => request<HarnessInstallJob>(`/api/harnesses/${id}/install`, 'POST', body, 75000, AbortSignal.timeout(75000));
export const findHarnessInstallation = (operationId: string) => request<HarnessInstallJob>(`/api/harness-installations?operationId=${encodeURIComponent(operationId)}`);

export type EngineEvent = {
	previewText?: string;
	content?: { sessionId: string; ordinal: string; digest: string; bytes: number; domain?: string; key?: string };
	consultationId?: string;
  id: string;
  type: 'user' | 'agent_prompt' | 'session_spawn' | 'assistant' | 'reasoning' | 'command' | 'mcp' | 'tool' | 'error' | 'status' | 'consultation' | 'subagent';
  text: string;
  title?: string;
  status?: string;
  favorite?: boolean;
  createdAt: string;
  data?: Record<string, unknown> & { mentions?: FileMention[]; mentionPreparation?: MentionPreparation[] };
};

export type SessionUsage = {
  inputTokens: number | null;
  outputTokens: number | null;
  context: { tokens: number | null; window: number | null } | null;
};

export type QueuedMessage = { id: string; text: string; mode: "queue" | "steer" | "interrupt"; status: "queued" | "steering" | "sending" | "accepted" | "interrupting" | "paused" | "uncertain"; error?: string; origin?: { sessionId: string; title: string; kind?: 'instruction' | 'question_answer' } };

export type Session = {
	delegation?: { availability: { available: boolean; code?: string; detail?: string }; waiting: boolean; actions: DelegationActions };
  agentConfig?: AgentConfigSummary;
	metadataOnly?: boolean;
	detailLoaded?: boolean;
	permissionCount?: number;
	questionCount?: number;
	activityListed?: boolean;
	pendingMore?: Record<string, boolean>;
  automation?: { runId: string; automationId: string; status: 'running' | 'succeeded' | 'failed' | 'execution_failed' | 'interrupted_by_user' };
  capabilities?: ConversationCapabilities;
  queue?: QueuedMessage[] | null;
  role?: 'side_agent' | 'subagent' | 'graph_node' | 'general_agent';
  selectedGraphId?: string;
  graph?: ConversationGraphState;
  parentId?: string;
  sources?: SourceReference[];
  id: string;
  projectId: string;
  title: string;
  latestReasoningTitle?: string;
  lastMessageAt?: string;
  workspace: string;
  topicId?: string;
  topic?: Topic;
  harness: Harness['id'];
  harnessLocked?: boolean;
  model?: string;
  yolo?: boolean;
  effort?: string;
  resolvedModel?: string;
  resolvedEffort?: string;
  status: 'idle' | 'running' | 'error';
  archived?: boolean;
  runtimeActive?: boolean;
  events: EngineEvent[];
  history?: { revision: Revision; startIndex: number; total: number; nextCursor?: string; hasMore: boolean; hasNewer?: boolean };
  permissions?: PermissionRequest[];
  questions?: QuestionRequest[];
  usage?: SessionUsage;
  createdAt: string;
  updatedAt: string;
};

export type DelegationActions = { send: boolean; queue: boolean; steer: boolean; interrupt: boolean; settings: boolean; stop: boolean };
export type DelegationBatch = { id: string; parentSessionId: string; revision: string; state: string; total: number; terminal: number; remaining: number; waiting: boolean; delivery: string; outcome?: string; actions: DelegationActions };
export type DelegationMember = { key: string; batchId: string; invocationId: string; childSessionId: string; parentSessionId: string; ownerConversationId: string; title: string; harness: string; model: string; effort: string; agentName?: string; executionState: string; outcome?: string; terminal: boolean; invocationNumber: string; reused: boolean; error?: string; resultReference?: { invocationId: string; digest: string; totalBytes: string } };
export type DelegationMemberPage = { batch: DelegationBatch; items: DelegationMember[]; revision: string; hasMore: boolean; nextOffset: number };
export type DelegationOutputPage = { invocationId: string; digest: string; offset: string; nextOffset: string; totalBytes: string; content: string; hasMore: boolean };
export type DelegationRequest = { ownerSessionId: string; childSessionId: string; childTitle: string; batchId: string; invocationId: string; kind: 'permission' | 'question'; request: PermissionRequest | QuestionRequest };

export type SourceReference = { sessionId: string; messageId: string; passage: string };

export type SessionSummary = Omit<Session, 'events' | 'history' | 'sources'>;
export type EventPage = {
  sessionId: string; revision: Revision; startIndex: number; endIndex: number;
  total: number; events: EngineEvent[] | null; nextCursor?: string; hasMore: boolean; hasNewer?: boolean;
};
export type SessionUpdate = {
  windowRefresh?: boolean;
  kind: 'reset' | 'delta'; revision: Revision; summary: SessionSummary; total: number;
  changes: { index: number; revision: Revision; event: EngineEvent }[];
  history?: EventPage;
};
export type SessionResponse = Session | SessionUpdate;
export type EngineSnapshot = Omit<EngineState, 'sessions'> & { sessions: SessionSummary[]; revision?: Revision; contract?: string; engineId?: string };

export type ConversationGraphState = {
  sessionId: string;
  selectedGraphId: string;
  revision: Revision;
  run: GraphRunProjection | null;
  requests: GraphRequestProjection[];
  requestsHasMore?: boolean; requestsNextOffset?: number;
};
export type GraphRunProjection = {
  id: string; graphId: string; active: boolean;
  status: 'starting' | 'running' | 'ending'; revision: Revision;
  snapshot: GraphFile;
  activeNodeIds: string[]; completedNodeIds: string[];
  completedChoiceIds: string[]; collectingJoinIds: string[];
};
export type GraphActivationSummary = { id: string; occurrence: Revision; status: string };
export type GraphNodeActivity = {
  capabilities?: ConversationCapabilities['applied'];
  runActive: boolean;
  activations: GraphActivationSummary[];
  hasMore?: boolean; nextOffset?: number;
  activation: (GraphActivationSummary & {
    input: Record<string, string>; events: EngineEvent[]; error?: string;
    createdAt: string; updatedAt: string;
    hasMore?: boolean; nextOffset?: number; hasEarlier?: boolean; previousOffset?: number; total?: number;
    submission?: { choice: { origin: string; id: string }; payload: Record<string, string>; acceptedAt?: string };
  }) | null;
};
export type GraphRequestProjection = {
  runId: string; graphId: string; nodeId: string; nodeName: string;
  activationId: string; sessionId: string; requestId: string;
  kind: 'permission' | 'question';
  permission?: PermissionRequest; question?: QuestionRequest;
};

// Graph revisions advance independently of the chat's updatedAt timestamp.
export function mergeGraphState(previous: ConversationGraphState | undefined, incoming: ConversationGraphState | undefined) {
  if (!incoming || (previous && compareRevision(incoming.revision, previous.revision) <= 0)) return previous;
  return incoming;
}

export const getAuthoringCatalog = (projectId: string) => request<AuthoringCatalog>(`/api/projects/${encodeURIComponent(projectId)}/authoring`);
export const getConversationGraph = (sessionId: string) => request<ConversationGraphState>(`/api/sessions/${encodeURIComponent(sessionId)}/graph`);
export const selectConversationGraph = (sessionId: string, selectedGraphId: string) => request<ConversationGraphState>(`/api/sessions/${encodeURIComponent(sessionId)}/graph`, 'PATCH', { selectedGraphId });

export type ProjectPath = { path: string; kind: 'file' | 'directory' };
// Offsets use UTF-16 units in canonical message text (full @path), not UTF-8 bytes
// or the visible length of the editor's basename-only badges.
export type FileMention = ProjectPath & { id: string; start: number; end: number };
export type MentionPreparation = ProjectPath & { mode: 'native' | 'prepared'; bytes?: number; truncated?: boolean };
export type MessageSubmission = { text: string; mentions: FileMention[]; sources?: SourceReference[] };

export type PermissionDecision = 'once' | 'session' | 'always' | 'reject' | 'deny_project' | 'allow_global' | 'deny_global';
export type QuestionItem = {
  id: string;
  header: string;
  text: string;
  options: { label: string; description: string }[];
  multiple: boolean;
  custom: boolean;
  secret: boolean;
};
export type QuestionRequest = {
  elicitation?: { connectionId: string; connectionName: string; mode: 'form' | 'url'; message: string; url?: string; schema?: Record<string, unknown>; callId: string };
  id: string;
  harness: Harness['id'];
  items: QuestionItem[];
  createdAt: string;
  resolving?: boolean;
};
export type PermissionRequest = {
  id: string;
  harness: Harness['id'];
  kind: string;
  title: string;
  description: string;
  patterns: string[];
  details?: Record<string, unknown>;
  command?: string;
  path?: string;
  scopeLabel?: string;
  dangerous?: boolean;
  decisions: PermissionDecision[];
  allowLabel?: string;
  createdAt: string;
  resolving?: boolean;
};

export type EngineState = {
  projects: Project[];
  sessions: Session[];
  harnesses: Harness[];
};

export type ModelOption = { id: string; name: string; provider: string; providerName: string; efforts: string[]; defaultEffort?: string };
export type ModelCatalog = { models: ModelOption[]; connectedProviders: { id: string; name: string; authType: string }[]; defaultModel?: string; defaultEffort?: string; effortLabel: string };
export type SessionMetadata = { sessionId: string; gitBranch?: string };
export type QuotaSnapshot = { source: string; observedAt: string; stale?: boolean; windows: { name: string; usedPercent: number; resetsAt: number }[] };
export type TranscriptionVocabularyEntry = { term: string; note: string };
export type TranscriptionSettings = {
  apiKeyConfigured: boolean;
  model: string;
  language: string;
  vocabulary: TranscriptionVocabularyEntry[];
};
export type TranscriptionSettingsUpdate = {
  apiKey?: string | null;
  model?: string;
  language?: string;
  vocabulary?: TranscriptionVocabularyEntry[];
};

export class EngineRequestError extends Error {
  constructor(message: string, public readonly status: number, public readonly details?: Record<string, unknown>) { super(message); }
}

export async function request<T>(path: string, method = 'GET', body?: unknown, timeoutMs = 15000, signal?: AbortSignal): Promise<T> {
  if (IS_DESKTOP && method.toUpperCase() !== 'GET' && !draftStorageReady()) throw new DOMException('Engine workspace identity is not ready.', 'AbortError');
  const finished = IS_DESKTOP && method.toUpperCase() !== 'GET' ? beginLocalActivity('Engine request') : undefined;
  try {
  const identityGeneration = engineIdentityGeneration();
  if (body && typeof body === 'object' && (/\/automations(?:\/|$)/.test(path) || path === '/api/settings/engine' || path.endsWith('/capabilities'))) {
    const value = { ...body } as Record<string, unknown>;
    if (value.revision !== undefined) value.revision = String(value.revision);
    if (value.automation && typeof value.automation === 'object') {
      const automation = { ...value.automation } as Record<string, unknown>;
      if (automation.revision !== undefined) automation.revision = String(automation.revision);
      if (automation.afterSequence !== undefined) automation.afterSequence = String(automation.afterSequence);
      value.automation = automation;
    }
    body = value;
  }
  let response: Response;
  const timeout = method === 'GET' ? AbortSignal.timeout(timeoutMs) : undefined;
  const requestSignal = signal && timeout ? AbortSignal.any([signal, timeout]) : signal ?? timeout;
  try {
    response = await clientFetch(path, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: requestSignal,
    });
  } catch (error) {
    if (signal?.aborted && signal.reason instanceof DOMException && signal.reason.name === 'TimeoutError') throw new Error('Engine request timed out. Delivery may be unconfirmed.');
    if (signal?.aborted) throw new DOMException('Request cancelled.', 'AbortError');
    if (timeout?.aborted || (error instanceof DOMException && error.name === 'TimeoutError')) throw new Error('Engine request timed out. Retry.');
    throw new Error(`Cannot connect to the engine at ${ENGINE_URL}. Start the engine and retry.`);
  }
  const result: unknown = await response.json().catch(() => null);
  if (path.split('?')[0] !== '/api/state' && identityGeneration !== engineIdentityGeneration()) throw new DOMException('Engine identity changed.', 'AbortError');
  if (!response.ok) {
    const detail = result && typeof result === 'object' && 'error' in result && typeof result.error === 'string' ? result.error : `Engine request failed (${response.status}).`;
    throw new EngineRequestError(detail, response.status, result && typeof result === 'object' ? result as Record<string, unknown> : undefined);
  }
  if (result === null) throw new Error('The engine returned an invalid JSON response. Retry the request.');
  return normalizeEngineResponse(path, result) as T;
  } finally {
    // Callers finish their synchronous acceptance/update continuations first.
    // Editable save handlers and local queues additionally hold their own lease.
    if (finished) setTimeout(finished, 0);
  }
}

export function sessionMetadata(value: Partial<Session>): Session {
  return { ...value, id: value.id!, title: value.title ?? '', projectId: value.projectId ?? '', workspace: value.workspace ?? 'Ungrouped',
    harness: value.harness ?? 'pi', status: value.status ?? 'idle', role: value.role || undefined,
    createdAt: value.createdAt ?? '', updatedAt: value.updatedAt ?? '', events: value.events ?? [], metadataOnly: true };
}
function ordinal(value: string | number) {
  const number = Number(value); if (!Number.isSafeInteger(number) || number < 0) throw new Error('Invalid history position.'); return number;
}
function normalizeEngineResponse(path: string, value: unknown): unknown {
  if (!value || typeof value !== 'object') return value;
  const object = value as Record<string, any>;
  if (typeof object.data === 'string' && typeof object.nextOffset === 'string') return { ...object, offset: ordinal(object.offset), nextOffset: ordinal(object.nextOffset), totalBytes: ordinal(object.totalBytes) };
  if (/\/graph\/runs\/[^/]+\/nodes\/[^/]+\/activity/.test(path) && object.activation?.events) {
    const sessionId = decodeURIComponent(path.split('/')[3]);
    return { ...object, activation: { ...object.activation, events: object.activation.events.map((entry: any) => entry.content ? { id: entry.id, type: 'status', text: '', createdAt: '', content: { ...entry.content, bytes: Number(entry.content.bytes), sessionId, ordinal: '' } } : entry) } };
  }
  if (path.split('?')[0] === '/api/state') {
    if (object.contract !== '3') throw new Error('This client requires engine storage contract 3.');
    return { ...object, projects: object.projects.map((p: Project) => ({ ...p, folders: [], archivedFolders: [] })), sessions: object.general ? [sessionMetadata(object.general)] : [] };
  }
  if (object.session && typeof object.eventCount === 'string') {
    // Full detail responses omit empty request lists. Make those authoritative
    // empties explicit so metadata merges cannot retain already resolved cards.
    const permissions = object.session.permissions ?? [];
    const questions = object.session.questions ?? [];
    return { ...sessionMetadata(object.session), permissions, questions, detailLoaded: true, permissionCount: permissions.length, questionCount: questions.length, pendingMore: object.more };
  }
  if (/^\/api\/projects(?:\/[^/?]+)?$/.test(path.split('?')[0]) && typeof object.folder === 'string') return { ...object, folders: [], archivedFolders: [] };
  if (typeof object.sessionId === 'string' && typeof object.count === 'string' && Array.isArray(object.events)) {
    const events: EngineEvent[] = object.events.map((row: any) => row.event ?? ({ id: row.id, type: row.preview?.type ?? 'status', title: row.preview?.title, status: row.preview?.status, consultationId: row.preview?.consultationId, previewText: row.preview?.textPreview, text: '', createdAt: row.preview?.createdAt ?? '', content: { sessionId: object.sessionId, ordinal: row.ordinal, digest: row.content.digest, bytes: row.content.bytes } }));
    const start = object.before === '' ? 0 : ordinal(object.before);
    return { sessionId: object.sessionId, revision: object.revision, startIndex: start, endIndex: start + events.length, total: ordinal(object.count), events, nextCursor: object.hasOlder ? object.before : undefined, hasMore: object.hasOlder, hasNewer: object.hasNewer } satisfies EventPage;
  }
  return value;
}

export async function pickDirectory(signal?: AbortSignal): Promise<string | null> {
  const result = await request<{ path: string | null }>('/api/dialogs/directory', 'POST', {}, 15000, signal);
  return result.path;
}

export type FilesystemDirectory = { name: string; path: string };
export type FilesystemRoots = { roots: FilesystemDirectory[]; home: string | null; nativePicker: boolean };
export type FilesystemListing = { path: string; parent: string | null; directories: FilesystemDirectory[]; incomplete: boolean; skipped: number };
export const getFilesystemRoots = (signal?: AbortSignal) => request<FilesystemRoots>('/api/filesystem/roots', 'GET', undefined, 15000, signal);
export const getFilesystemDirectories = (path: string, signal?: AbortSignal) => request<FilesystemListing>(`/api/filesystem/directories?path=${encodeURIComponent(path)}`, 'GET', undefined, 15000, signal);
export const createFilesystemDirectory = (parent: string, name: string, signal?: AbortSignal) => request<FilesystemDirectory>('/api/filesystem/directories', 'POST', { parent, name }, 15000, signal);
export const renameFilesystemDirectory = (parent: string, name: string, newName: string, signal?: AbortSignal) => request<FilesystemDirectory>('/api/filesystem/directories', 'PATCH', { parent, name, newName }, 15000, signal);
export const deleteFilesystemDirectory = (parent: string, name: string, confirmation: string, signal?: AbortSignal) => request<{ path: string }>('/api/filesystem/directories', 'DELETE', { parent, name, confirmation }, 15000, signal);

export const getEngineVersion = () => request<{ version: string }>('/api/version');
export const getTranscriptionSettings = () => request<TranscriptionSettings>('/api/transcription/settings');
export const updateTranscriptionSettings = (settings: TranscriptionSettingsUpdate) => request<TranscriptionSettings>('/api/transcription/settings', 'PATCH', settings);

export async function transcribeAudio(audio: Blob, filename: string, signal?: AbortSignal): Promise<string> {
  const form = new FormData();
  form.append('file', audio, filename);
  let response: Response;
  try {
    response = await clientFetch('/api/transcriptions', {
      method: 'POST',
      body: form,
      signal,
    });
  } catch {
    throw new Error(`Cannot connect to the engine at ${ENGINE_URL}. Start the engine and retry.`);
  }
  const result: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    const detail = result && typeof result === 'object' && 'error' in result && typeof result.error === 'string' ? result.error : `Transcription failed (${response.status}).`;
    throw new Error(detail);
  }
  if (!result || typeof result !== 'object' || !('text' in result) || typeof result.text !== 'string') {
    throw new Error('The engine returned an invalid transcription. Retry.');
  }
  return result.text;
}
