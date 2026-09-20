import type { AuthoringCatalog, GraphFile } from './features/graphs/files';
import { resolveEngineURL } from './platform';

export const ENGINE_URL = resolveEngineURL();

export type Project = {
  id: string;
  name: string;
  folder: string;
  icon: string;
  folders: string[];
  archivedFolders: string[];
};

export type Harness = {
  id: 'pi' | 'opencode' | 'codex';
  name: string;
  available: boolean;
  error?: string;
};

export type EngineEvent = {
	consultationId?: string;
  id: string;
  type: 'user' | 'assistant' | 'reasoning' | 'command' | 'mcp' | 'tool' | 'error' | 'status' | 'consultation' | 'subagent';
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

export type QueuedMessage = { id: string; text: string; mode: "queue" | "steer"; status: "queued" | "steering" | "sending" | "paused" | "uncertain"; error?: string };

export type Session = {
  queue?: QueuedMessage[] | null;
  role?: 'side_agent' | 'subagent' | 'graph_node';
  selectedGraphId?: string;
  graph?: ConversationGraphState;
  parentId?: string;
  sources?: SourceReference[];
  id: string;
  projectId: string;
  title: string;
  workspace: string;
  harness: Harness['id'];
  model?: string;
  yolo?: boolean;
  effort?: string;
  resolvedModel?: string;
  resolvedEffort?: string;
  status: 'idle' | 'running' | 'error';
  archived?: boolean;
  runtimeActive?: boolean;
  events: EngineEvent[];
  history?: { revision: number; startIndex: number; total: number; nextCursor?: string; hasMore: boolean };
  permissions?: PermissionRequest[];
  questions?: QuestionRequest[];
  usage?: SessionUsage;
  createdAt: string;
  updatedAt: string;
};

export type SourceReference = { sessionId: string; messageId: string; passage: string };

export type SessionSummary = Omit<Session, 'events' | 'history' | 'sources'>;
export type EventPage = {
  sessionId: string; revision: number; startIndex: number; endIndex: number;
  total: number; events: EngineEvent[] | null; nextCursor?: string; hasMore: boolean;
};
export type SessionUpdate = {
  kind: 'reset' | 'delta'; revision: number; summary: SessionSummary; total: number;
  changes: { index: number; revision: number; event: EngineEvent }[];
  history?: EventPage;
};
export type SessionResponse = Session | SessionUpdate;
export type EngineSnapshot = Omit<EngineState, 'sessions'> & { sessions: SessionSummary[]; revision?: number };

export type ConversationGraphState = {
  sessionId: string;
  selectedGraphId: string;
  revision: number;
  run: GraphRunProjection | null;
  requests: GraphRequestProjection[];
};
export type GraphRunProjection = {
  id: string; graphId: string; active: boolean;
  status: 'starting' | 'running' | 'ending'; revision: number;
  snapshot: GraphFile;
  activeNodeIds: string[]; completedNodeIds: string[];
  completedChoiceIds: string[]; collectingJoinIds: string[];
};
export type GraphActivationSummary = { id: string; occurrence: number; status: string };
export type GraphNodeActivity = {
  runActive: boolean;
  activations: GraphActivationSummary[];
  activation: (GraphActivationSummary & {
    input: Record<string, string>; events: EngineEvent[]; error?: string;
    createdAt: string; updatedAt: string;
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
  if (!incoming || (previous && incoming.revision <= previous.revision)) return previous;
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

export async function request<T>(path: string, method = 'GET', body?: unknown, timeoutMs = 15000, signal?: AbortSignal): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`${ENGINE_URL}${path}`, {
      method,
      headers: { 'Content-Type': 'application/json' },
      credentials: 'omit',
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: signal ?? (method === 'GET' ? AbortSignal.timeout(timeoutMs) : undefined),
    });
  } catch {
    throw new Error(`Cannot connect to the engine at ${ENGINE_URL}. Start the engine and retry.`);
  }
  const result: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    const detail = result && typeof result === 'object' && 'error' in result && typeof result.error === 'string' ? result.error : `Engine request failed (${response.status}).`;
    throw new Error(detail);
  }
  if (result === null) throw new Error('The engine returned an invalid JSON response. Retry the request.');
  return result as T;
}

export async function pickDirectory(signal?: AbortSignal): Promise<string | null> {
  const result = await request<{ path: string | null }>('/api/dialogs/directory', 'POST', {}, 15000, signal);
  return result.path;
}

export const getTranscriptionSettings = () => request<TranscriptionSettings>('/api/transcription/settings');
export const updateTranscriptionSettings = (settings: TranscriptionSettingsUpdate) => request<TranscriptionSettings>('/api/transcription/settings', 'PATCH', settings);

export async function transcribeAudio(audio: Blob, filename: string, signal?: AbortSignal): Promise<string> {
  const form = new FormData();
  form.append('file', audio, filename);
  let response: Response;
  try {
    response = await fetch(`${ENGINE_URL}/api/transcriptions`, {
      method: 'POST',
      credentials: 'omit',
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
