import { request, type Revision } from '../../engine';

export type MCPSelection = { connectionId: string; tools: string[] };
export type CapabilitySelection = { skills: string[]; mcpConnections: MCPSelection[] };
export type CapabilitySnapshot = { revision: string; selection: CapabilitySelection; skills: { id: string; name: string; revision: string; internal?: boolean }[]; connections?: { id: string; name: string; revision: string; selection: MCPSelection }[]; diagnostics: string[] };
export type ConversationCapabilities = { revision: Revision; desired: CapabilitySelection; applied?: CapabilitySnapshot };
export type SkillEntry = { id: string; name: string; description: string; revision?: string; valid: boolean; error?: string; source: string };
export type MCPConfig = { transport: 'stdio' | 'http'; command?: string; args?: string[]; cwd?: string; url?: string; auth: 'none' | 'manual' | 'oauth'; clientId?: string; redirectUri?: string; scopes?: string[] };
export type MCPConnection = { id: string; name: string; revision: string; config: MCPConfig; credentialsConfigured: boolean; catalog?: { tools: { name: string; description?: string }[]; resources: boolean; revision: string } | null; lastTestAt?: string; lastTestError?: string };
export type LibraryInventory = { skillsRoot: string; skills: SkillEntry[]; skillsHasMore?: boolean; skillsNextOffset?: number; skillsWindowStart?: number; search?: string; connections: MCPConnection[]; recoveryPending: boolean; harnesses: Record<string, { managed: boolean; status: string }> };
export type DeletionPreview = { revision: string; itemRevision: string; references: { kind: string; id: string; title: string; projectId?: string }[]; hasMore?: boolean; nextOffset?: number; total?: number };
export const emptySelection = (): CapabilitySelection => ({ skills: [], mcpConnections: [] });
export const selectionOf = (value?: CapabilitySelection): CapabilitySelection => ({ skills: value?.skills ?? [], mcpConnections: value?.mcpConnections ?? [] });
export const libraryPath = (id: string) => `/api/library/mcp/${encodeURIComponent(id)}`;
export const getLibrary = async (signal?: AbortSignal, search = ''): Promise<LibraryInventory> => {
  const query = `search=${encodeURIComponent(search)}`;
  const page = await request<LibraryInventory & { hasMore?: boolean; nextOffset?: number }>(`/api/library?${query}`, 'GET', undefined, 20000, signal);
  // The engine caps the configured connection catalog at 64. Tool selectors need
  // those catalogs only while their Library surface is open; never at bootstrap.
  let more = page.hasMore, offset = page.nextOffset;
  while (more) {
    const next = await request<LibraryInventory & { hasMore?: boolean; nextOffset?: number }>(`/api/library?${query}&offset=${offset}`, 'GET', undefined, 20000, signal);
    if (!next.connections.length || next.nextOffset === offset) throw new Error('Library changed. Refresh before continuing.');
    page.connections = [...page.connections, ...next.connections]; more = next.hasMore; offset = next.nextOffset;
    if (page.connections.length > 64) throw new Error('Library changed. Refresh before continuing.');
  }
  page.connections = await Promise.all(page.connections.map(c => (c as MCPConnection & { detailRequired?: boolean }).detailRequired ? request<MCPConnection>(libraryPath(c.id), 'GET', undefined, 20000, signal) : Promise.resolve(c)));
  return page;
};
export const capabilityPath = (id: string) => `/api/sessions/${encodeURIComponent(id)}/capabilities`;
export function selectionKey(selection?: CapabilitySelection) {
  const value = selectionOf(selection);
  return JSON.stringify({ skills: [...value.skills].sort(), mcpConnections: value.mcpConnections.map(c => ({ connectionId: c.connectionId, tools: [...(c.tools ?? [])].sort() })).sort((a, b) => a.connectionId.localeCompare(b.connectionId)) });
}
