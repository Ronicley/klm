import { request } from '../../engine';

export type MCPSelection = { connectionId: string; tools: string[] };
export type CapabilitySelection = { skills: string[]; mcpConnections: MCPSelection[] };
export type CapabilitySnapshot = { revision: string; selection: CapabilitySelection; skills: { id: string; name: string; revision: string; internal?: boolean }[]; connections?: { id: string; name: string; revision: string; selection: MCPSelection }[]; diagnostics: string[] };
export type ConversationCapabilities = { revision: number; desired: CapabilitySelection; applied?: CapabilitySnapshot };
export type SkillEntry = { id: string; name: string; description: string; revision?: string; valid: boolean; error?: string; source: string };
export type MCPConfig = { transport: 'stdio' | 'http'; command?: string; args?: string[]; cwd?: string; url?: string; auth: 'none' | 'manual' | 'oauth'; clientId?: string; redirectUri?: string; scopes?: string[] };
export type MCPConnection = { id: string; name: string; revision: string; config: MCPConfig; credentialsConfigured: boolean; catalog?: { tools: { name: string; description?: string }[]; resources: boolean; revision: string } | null; lastTestAt?: string; lastTestError?: string };
export type LibraryInventory = { skillsRoot: string; skills: SkillEntry[]; connections: MCPConnection[]; recoveryPending: boolean; harnesses: Record<string, { managed: boolean; status: string }> };
export type DeletionPreview = { revision: string; itemRevision: string; references: { kind: string; id: string; title: string; projectId?: string }[] };
export const emptySelection = (): CapabilitySelection => ({ skills: [], mcpConnections: [] });
export const selectionOf = (value?: CapabilitySelection): CapabilitySelection => ({ skills: value?.skills ?? [], mcpConnections: value?.mcpConnections ?? [] });
export const libraryPath = (id: string) => `/api/library/mcp/${encodeURIComponent(id)}`;
export const getLibrary = (signal?: AbortSignal) => request<LibraryInventory>('/api/library', 'GET', undefined, 20000, signal);
export const capabilityPath = (id: string) => `/api/sessions/${encodeURIComponent(id)}/capabilities`;
export function selectionKey(selection?: CapabilitySelection) {
  const value = selectionOf(selection);
  return JSON.stringify({ skills: [...value.skills].sort(), mcpConnections: value.mcpConnections.map(c => ({ connectionId: c.connectionId, tools: [...(c.tools ?? [])].sort() })).sort((a, b) => a.connectionId.localeCompare(b.connectionId)) });
}
