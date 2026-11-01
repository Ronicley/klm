import { useEffect, useRef, useState } from 'react';
import { Button } from '../../design-system/Button';
import { ENGINE_URL, request, type EngineEvent } from '../../engine';
import { openContentDownload } from '../../platform';

type ContentPage = { data: string; offset: number; nextOffset: number; hasMore: boolean; totalBytes: number; display?: string };
const decode = (data: string) => Uint8Array.from(atob(data), c => c.charCodeAt(0));

export function EventContent({ event }: { event: EngineEvent }) {
  const content = event.content!;
  const pageSize = content.domain ? 16384 : 65536;
  const [page, setPage] = useState<ContentPage>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const controller = useRef<AbortController | undefined>(undefined);
  useEffect(() => { setPage(undefined); return () => controller.current?.abort(); }, [content.digest]);
  const path = (offset: number) => content.domain
    ? `/api/sessions/${encodeURIComponent(content.sessionId)}/graph/content?domain=${encodeURIComponent(content.domain)}&key=${encodeURIComponent(content.key ?? '')}&digest=${encodeURIComponent(content.digest)}&offset=${offset}`
    : `/api/sessions/${encodeURIComponent(content.sessionId)}/content?ordinal=${content.ordinal}&digest=${encodeURIComponent(content.digest)}&offset=${offset}`;
  async function load(offset: number) {
    controller.current?.abort(); const current = new AbortController(); controller.current = current; setBusy(true); setError('');
    try {
      // Inspect a few bytes on both sides of the logical page boundary so UTF-8
      // code points are displayed once, never replaced by decoding a partial rune.
      const start = Math.max(0, offset - 3);
      const page = await request<ContentPage>(path(start), 'GET', undefined, 15000, current.signal);
      const first = decode(page.data);
      const following = page.hasMore ? decode((await request<ContentPage>(path(page.nextOffset), 'GET', undefined, 15000, current.signal)).data).slice(0, 8) : new Uint8Array();
      const bytes = new Uint8Array(first.length + following.length); bytes.set(first); bytes.set(following, first.length);
      let left = offset - start, right = Math.min(bytes.length, offset - start + pageSize);
      while (left < bytes.length && (bytes[left] & 0xc0) === 0x80) left++;
      while (right < bytes.length && (bytes[right] & 0xc0) === 0x80) right++;
      const nextOffset = Math.min(offset + pageSize, page.totalBytes);
      if (!current.signal.aborted) setPage({ ...page, offset, nextOffset, hasMore: nextOffset < page.totalBytes, display: new TextDecoder('utf-8', { fatal: true }).decode(bytes.slice(left, right)) });
    }
    catch (error) { if (!current.signal.aborted) setError(error instanceof Error ? error.message : 'Could not read content.'); }
    finally { if (controller.current === current) setBusy(false); }
  }
  async function download() {
    controller.current?.abort(); const current = new AbortController(); controller.current = current; setBusy(true); setError('');
    try {
      const ticket = await request<{ path: string }>(`/api/sessions/${encodeURIComponent(content.sessionId)}/content-downloads`, 'POST', {
        ordinal: content.ordinal, domain: content.domain, key: content.key, digest: content.digest,
      }, 15000, current.signal);
      if (current.signal.aborted) return;
      if (!/^\/api\/content-downloads\/[A-Za-z0-9_-]{43}\?contract=3$/.test(ticket.path)) throw new Error('Invalid download response.');
      await openContentDownload(ENGINE_URL + ticket.path);
    } catch (error) { if (!current.signal.aborted) setError(error instanceof Error ? error.message : 'Could not download content.'); }
    finally { if (controller.current === current) setBusy(false); }
  }
  return <section className="session-event"><strong>{event.title || 'Message content'}</strong>
    {event.previewText && <div><span>Preview</span><pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{event.previewText}</pre></div>}<div>
    {!page && <Button size="sm" disabled={busy} onClick={() => void load(0)}>Open content</Button>}
    <Button size="sm" disabled={busy} onClick={() => void download()}>Download event</Button>
  </div>{page && <><pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{page.display ?? new TextDecoder().decode(decode(page.data))}</pre>
    <Button size="sm" disabled={busy || page.offset === 0} onClick={() => void load(Math.max(0, page.offset - pageSize))}>Previous part</Button>
    <Button size="sm" disabled={busy || !page.hasMore} onClick={() => void load(page.nextOffset)}>Next part</Button></>}
    {error && <p role="alert" className="form-error">{error}</p>}</section>;
}
