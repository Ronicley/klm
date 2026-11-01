import type { Harness } from '../../engine';
import type { MCPSelection } from '../library/api';
import type { CapabilitySelection } from '../library/api';

export type AgentDefinition = {
  name: string;
  description: string;
  enabled: boolean;
  model?: string;
  effort?: string;
  defaultHarness?: Harness['id'];
  prompt?: string;
  skills?: string[];
  mcpConnections?: MCPSelection[];
};

export type Agent = AgentDefinition & {
  id: string;
  revision?: string;
  graphReferences?: string[];
  source?: AgentSource;
  references?: AgentReference[] | null;
  referencesHasMore?: boolean;
  referencesNextOffset?: number;
};

export type AgentAuthoringScope = { kind: 'global' } | { kind: 'project'; projectId: string };
export type AgentSource = { sourceUid: string; id: string; scope: 'global' | 'project'; projectId?: string; projectName?: string };
export type AgentReference = { kind: string; id: string; name: string; projectId: string; projectName: string };
export type AgentDiagnostic = { code: string; message: string; source?: AgentSource; conflicts?: AgentSource[] };
export type CatalogAgent = Agent & {
  revision: string; source: AgentSource; status: string; available: boolean;
  unavailableReason?: string; references: AgentReference[] | null;
  referencesHasMore: boolean; referencesNextOffset?: number;
};
export type AgentSummary = Omit<CatalogAgent, 'prompt' | 'skills' | 'mcpConnections' | 'references' | 'referencesHasMore' | 'referencesNextOffset' | 'graphReferences'> & { referenceCount: number };
export type AgentPage = { agents: AgentSummary[]; diagnostics: AgentDiagnostic[] | null; complete: boolean; revision: string; hasMore: boolean; nextOffset: number };
export type AgentProblem = { code?: string; field?: string; hint?: string; conflicts?: AgentSource[]; currentRevision?: string; blockingReferences?: AgentReference[] };

export const agentOrigin = (source: AgentSource) => source.scope === 'global' ? 'Global' : `Project · ${source.projectName || source.projectId}`;

export type AgentOverrides = { model?: string; effort?: string; prompt?: string; skills?: string[]; mcpConnections?: MCPSelection[] };
export type AgentOverridePatch = { [Field in keyof AgentOverrides]?: AgentOverrides[Field] | null };
export type AgentConfiguration = { harness: Harness['id']; model: string; effort: string; prompt: string; selection: CapabilitySelection };
export type AgentConfigSummary = {
  source: AgentSource; name: string; sourceStatus: string; capturedRevision: string; pendingRevision?: string;
  overriddenFields: string[]; pending: boolean;
  execution: { allowed: boolean; code?: string; message?: string; fields?: string[]; token: string };
};
export type SessionAgent = AgentConfigSummary & {
  revision: string; overrides: AgentOverrides; inheritedCapabilities: CapabilitySelection; desired: AgentConfiguration;
  applied?: { config: AgentConfiguration; source?: { name?: string; source: AgentSource; revision: string; config: AgentConfiguration }; revision: string };
};
export type SessionCreateInput = {
  projectId: string; title: string; topicId?: string; harness?: Harness['id']; model?: string; effort?: string;
  agent?: string; agentSourceUid?: string; agentRevision?: string; agentOverrides?: AgentOverrides;
};

export function agentSlug(name: string) {
  return name.normalize('NFKD').replace(/\p{M}/gu, '').toLowerCase().trim()
    .replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-+|-+$/g, '');
}
