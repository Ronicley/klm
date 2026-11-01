import { useEffect, useState } from 'react';
import { request, type Topic } from '../../engine';

export function useTopicPage(projectId: string | undefined, enabled: boolean, search: string) {
  const [page, setPage] = useState<{ items: Topic[]; nextCursor?: string }>({ items: [] });
  const [cursor, setCursor] = useState('');
  const [query, setQuery] = useState(search);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => { const timer = setTimeout(() => { setCursor(''); setQuery(search); }, 200); return () => clearTimeout(timer); }, [search]);
  useEffect(() => { setCursor(''); }, [projectId]);
  useEffect(() => {
    if (!enabled || !projectId) return;
    const controller = new AbortController(); setLoading(true); setError('');
    const params = new URLSearchParams({ active: 'true', search: query, limit: '100', ...(cursor ? { cursor } : {}) });
    void request<{ items: Topic[]; nextCursor?: string }>(`/api/projects/${encodeURIComponent(projectId)}/topics?${params}`, 'GET', undefined, 15000, controller.signal)
      .then(value => { if (!controller.signal.aborted) setPage(value); })
      .catch(error => { if (!controller.signal.aborted) setError(error instanceof Error ? error.message : 'Could not load topics.'); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [projectId, enabled, cursor, query, attempt]);
  return { ...page, loading, error, older: !!cursor, next: () => setCursor(page.nextCursor ?? ''), first: () => { setCursor(''); setAttempt(n => n + 1); } };
}
