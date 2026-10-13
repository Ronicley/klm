import { ENGINE_URL, request } from '../../engine';

export type WebhookRetention = '1d' | '3d' | '1w' | '1mo' | '3mo' | '6mo' | '1y' | 'forever';
export type WebhookConnection = {
  id: string; projectId: string; provider: 'github'; name: string;
  enabled: boolean; retention: WebhookRetention; secretConfigured: boolean;
  endpointPath: string; createdAt: string; updatedAt: string;
  lastReceivedAt?: string; lastPingAt?: string;
};
export type WebhookEventMetadata = {
  id: string; projectId: string; webhookId: string; provider: 'github';
  deliveryId: string; eventType: string; action?: string; isPing: boolean;
  receivedAt: string; expiresAt?: string; receivedSequence: string;
  status: 'pending' | 'emitted'; emittedAt?: string; releaseSequence?: string;
  payloadBytes: number;
};
export type WebhookPage<T> = { items: T[]; nextCursor?: string; hasMore: boolean };
export type WebhookReleasePage = {
  items: WebhookEventMetadata[]; nextAfter: string; highWatermark: string; hasMore: boolean;
};
export type WebhookCreate = { provider: 'github'; name: string; secret: string; retention: WebhookRetention };
export type WebhookUpdate = { name?: string; enabled?: boolean; retention?: WebhookRetention; secret?: string };
type PageOptions = { limit?: number; cursor?: string };

export const webhookRetentions: readonly { value: WebhookRetention; label: string }[] = [
  { value: '1d', label: '1 day' }, { value: '3d', label: '3 days' },
  { value: '1w', label: '1 week' }, { value: '1mo', label: '1 month' },
  { value: '3mo', label: '3 months' }, { value: '6mo', label: '6 months' },
  { value: '1y', label: '1 year' }, { value: 'forever', label: 'Indefinitely' },
];

export function validateWebhookSecret(secret: string): string | null {
  const bytes = new TextEncoder().encode(secret).byteLength;
  if (bytes < 16 || bytes > 512) return 'Secret must contain 16–512 UTF-8 bytes.';
  if (/[\u0000-\u001f\u007f-\u009f]/.test(secret)) return 'Secret must not contain control characters.';
  return null;
}

function webhookPath(projectId: string, webhookId?: string) {
  return `/api/projects/${encodeURIComponent(projectId)}/webhooks${webhookId === undefined ? '' : `/${encodeURIComponent(webhookId)}`}`;
}

function pageQuery(options: PageOptions) {
  const query = new URLSearchParams();
  if (options.limit !== undefined) query.set('limit', String(options.limit));
  if (options.cursor !== undefined) query.set('cursor', options.cursor);
  return query.size ? `?${query}` : '';
}

export const webhookURL = (connection: Pick<WebhookConnection, 'endpointPath'>) => `${ENGINE_URL}${connection.endpointPath}`;

export function listWebhooks(projectId: string, options: PageOptions = {}, signal?: AbortSignal) {
  return request<WebhookPage<WebhookConnection>>(`${webhookPath(projectId)}${pageQuery(options)}`, 'GET', undefined, 15000, signal);
}

export async function createWebhook(projectId: string, input: WebhookCreate, signal?: AbortSignal) {
  const failure = validateWebhookSecret(input.secret);
  if (failure) throw new Error(failure);
  return request<WebhookConnection>(webhookPath(projectId), 'POST', input, 15000, signal);
}

export async function updateWebhook(projectId: string, webhookId: string, input: WebhookUpdate, signal?: AbortSignal) {
  if (input.secret !== undefined) {
    const failure = validateWebhookSecret(input.secret);
    if (failure) throw new Error(failure);
  }
  return request<WebhookConnection>(webhookPath(projectId, webhookId), 'PATCH', input, 15000, signal);
}

export function removeWebhook(projectId: string, webhookId: string, signal?: AbortSignal) {
  return request<{ removed: true }>(webhookPath(projectId, webhookId), 'DELETE', undefined, 15000, signal);
}

export function listWebhookEvents(projectId: string, webhookId: string, options: PageOptions = {}, signal?: AbortSignal) {
  return request<WebhookPage<WebhookEventMetadata>>(`${webhookPath(projectId, webhookId)}/events${pageQuery(options)}`, 'GET', undefined, 15000, signal);
}

export function getWebhookEvent(projectId: string, webhookId: string, eventId: string, signal?: AbortSignal) {
  return request<{ event: WebhookEventMetadata; payload: string }>(`${webhookPath(projectId, webhookId)}/events/${encodeURIComponent(eventId)}`, 'GET', undefined, 15000, signal);
}

export function listReleasedWebhookEvents(projectId: string, options: { after?: string; limit?: number } = {}, signal?: AbortSignal) {
  const query = new URLSearchParams();
  if (options.after !== undefined) query.set('after', options.after);
  if (options.limit !== undefined) query.set('limit', String(options.limit));
  return request<WebhookReleasePage>(`/api/projects/${encodeURIComponent(projectId)}/webhook-events${query.size ? `?${query}` : ''}`, 'GET', undefined, 15000, signal);
}
