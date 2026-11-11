import { useEffect, useRef, useState } from 'react';
import { AutoPage } from '../pagination/AutoPage';
import { Button } from '../../design-system/Button';
import { request, type PermissionRequest, type QuestionRequest, type Session, type SessionResponse } from '../../engine';
import { PermissionCard } from './PermissionCard';
import { QuestionCard } from './QuestionCard';
import { useSessionNavigation } from './SessionNavigation';

export function usePendingPage<T extends { id: string }>(session: Session, kind: 'permissions' | 'questions' | 'queue', base: T[]) {
  const [page, setPage] = useState<{ items: T[]; hasMore: boolean; nextAfterId: string }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const controller = useRef<AbortController | null>(null);
  const flight = useRef(false);
  const signature = base.map(item => item.id).join('/');
  useEffect(() => { controller.current?.abort(); flight.current = false; setPage(undefined); setError(''); setBusy(false); return () => controller.current?.abort(); }, [session.id, signature]);
  async function load(first = false) {
    if (flight.current) return; flight.current = true;
    const current = new AbortController(); controller.current = current; setBusy(true); setError('');
    const after = first ? base.at(-1)?.id : page?.nextAfterId ?? base.at(-1)?.id;
    try {
      const next = await request<{ items: T[]; hasMore: boolean; nextAfterId: string }>(`/api/sessions/${encodeURIComponent(session.id)}/pending?limit=25&kind=${kind}${after ? `&afterId=${encodeURIComponent(after)}` : ''}`, 'GET', undefined, 15000, current.signal);
      if (!current.signal.aborted) { if (next.hasMore && next.nextAfterId === after) throw new Error('Request pagination did not advance. Retry.'); setPage(old => ({ ...next, items: [...new Map([...(first ? [] : old?.items ?? []), ...next.items].map(item => [item.id, item])).values()] })); }
    } catch (error) { if (!current.signal.aborted) setError(error instanceof Error ? error.message : 'Requests changed. Reload requests.'); }
    finally { if (controller.current === current && !current.signal.aborted) { flight.current = false; setBusy(false); } }
  }
  return { items: session.pendingMore?.[kind] ? page?.items.filter(item => !base.some(loaded => loaded.id === item.id)) ?? [] : [], cursor: page?.nextAfterId ?? base.at(-1)?.id ?? '', busy, error, load, more: session.pendingMore?.[kind] && (page?.hasMore ?? true), loaded: !!page, resolved: (id: string) => setPage(old => old ? { ...old, items: old.items.filter(item => item.id !== id) } : old) };
}

export function PendingRequestPages({ session, onSnapshot }: { session: Session; onSnapshot: (value: SessionResponse) => void }) {
  const permissions = usePendingPage(session, 'permissions', session.permissions ?? []);
  const questions = usePendingPage(session, 'questions', session.questions ?? []);
  const navigation = useSessionNavigation();
  return <>
    {permissions.items.map(permission => <PermissionCard key={permission.id} permission={permission as PermissionRequest} sessionId={session.id} projectName={navigation.projects.get(session.projectId)?.name ?? ''} onResolved={value => { permissions.resolved(permission.id); onSnapshot(value); }} />)}
    <AutoPage hasMore={!!permissions.more} cursor={`${session.id}/permissions/${permissions.cursor}`} busy={permissions.busy} error={permissions.error} onLoad={() => void permissions.load()} onRetry={() => void permissions.load(true)} />
    {questions.items.map(question => <QuestionCard key={question.id} question={question as QuestionRequest} sessionId={session.id} onResolved={value => { questions.resolved(question.id); onSnapshot(value); }} />)}
    <AutoPage hasMore={!!questions.more} cursor={`${session.id}/questions/${questions.cursor}`} busy={questions.busy} error={questions.error} onLoad={() => void questions.load()} onRetry={() => void questions.load(true)} />
  </>;
}
