import { useEffect, useRef, useState } from 'react';
import { Button } from '../../design-system/Button';
import { request, type PermissionRequest, type QuestionRequest, type Session, type SessionResponse } from '../../engine';
import { PermissionCard } from './PermissionCard';
import { QuestionCard } from './QuestionCard';
import { useSessionNavigation } from './SessionNavigation';

function usePendingPage<T extends { id: string }>(session: Session, kind: 'permissions' | 'questions', base: T[]) {
  const [page, setPage] = useState<{ items: T[]; hasMore: boolean; nextAfterId: string }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), [session.id]);
  async function load(first = false) {
    if (busy) return;
    const current = new AbortController(); controller.current = current; setBusy(true); setError('');
    const after = first ? base.at(-1)?.id : page?.nextAfterId ?? base.at(-1)?.id;
    try {
      const next = await request<{ items: T[]; hasMore: boolean; nextAfterId: string }>(`/api/sessions/${encodeURIComponent(session.id)}/pending?kind=${kind}${after ? `&afterId=${encodeURIComponent(after)}` : ''}`, 'GET', undefined, 15000, current.signal);
      if (!current.signal.aborted) setPage(next);
    } catch (error) { if (!current.signal.aborted) setError(error instanceof Error ? error.message : 'Requests changed. Reload requests.'); }
    finally { if (!current.signal.aborted) setBusy(false); }
  }
  return { items: session.pendingMore?.[kind] ? page?.items.filter(item => !base.some(loaded => loaded.id === item.id)) ?? [] : [], busy, error, load, more: session.pendingMore?.[kind] && (page?.hasMore ?? true), loaded: !!page, resolved: (id: string) => setPage(old => old ? { ...old, items: old.items.filter(item => item.id !== id) } : old) };
}

export function PendingRequestPages({ session, onSnapshot }: { session: Session; onSnapshot: (value: SessionResponse) => void }) {
  const permissions = usePendingPage(session, 'permissions', session.permissions ?? []);
  const questions = usePendingPage(session, 'questions', session.questions ?? []);
  const navigation = useSessionNavigation();
  return <>
    {permissions.items.map(permission => <PermissionCard key={permission.id} permission={permission as PermissionRequest} sessionId={session.id} projectName={navigation.projects.get(session.projectId)?.name ?? ''} onResolved={value => { permissions.resolved(permission.id); onSnapshot(value); }} />)}
    {permissions.more && <Button size="sm" disabled={permissions.busy} onClick={() => void permissions.load()}>More permission requests</Button>}
    {permissions.loaded && session.pendingMore?.permissions && <Button size="sm" disabled={permissions.busy} onClick={() => void permissions.load(true)}>First additional permission requests</Button>}
    {permissions.error && <p role="alert" className="form-error">{permissions.error}<Button size="sm" onClick={() => void permissions.load(true)}>Reload requests</Button></p>}
    {questions.items.map(question => <QuestionCard key={question.id} question={question as QuestionRequest} sessionId={session.id} onResolved={value => { questions.resolved(question.id); onSnapshot(value); }} />)}
    {questions.more && <Button size="sm" disabled={questions.busy} onClick={() => void questions.load()}>More questions</Button>}
    {questions.loaded && session.pendingMore?.questions && <Button size="sm" disabled={questions.busy} onClick={() => void questions.load(true)}>First additional questions</Button>}
    {questions.error && <p role="alert" className="form-error">{questions.error}<Button size="sm" onClick={() => void questions.load(true)}>Reload requests</Button></p>}
  </>;
}
