import { useEffect, useState } from 'react';
import { Button } from '../../design-system/Button';
import { SearchSelect } from '../../design-system/SearchSelect';
import { boardError, boardSprints, type BoardSprint, type BoardSprints } from './api';

export function SprintPicker({ projectId, projectionRevision, visible, initialized, value, onChange }: {
  projectId: string; projectionRevision: string; visible: boolean; initialized: boolean; value?: BoardSprint; onChange: (sprint?: BoardSprint) => void;
}) {
  const [catalog, setCatalog] = useState<BoardSprints>();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!visible) return;
    const controller = new AbortController();
    setLoading(true); setError('');
    void boardSprints(projectId, controller.signal).then(next => {
      if (!controller.signal.aborted) { setCatalog(next); setError(next.currentError ?? ''); }
    }).catch(e => {
      if (!controller.signal.aborted) {
        const message = boardError(e);
        setError(message);
        setCatalog(previous => previous ? { ...previous, currentError: message, items: previous.items.map(item => ({ ...item, current: false })) } : previous);
      }
    }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [projectId, projectionRevision, visible, retry]);

  useEffect(() => {
    if (!visible || initialized || loading || error || !catalog || catalog.currentError) return;
    const current = catalog.items.filter(item => item.current);
    // Default once per project visit. Later refreshes must not overwrite an
    // explicit past sprint or All sprints selection.
    onChange(current.length === 1 ? current[0] : undefined);
  }, [visible, initialized, loading, error, catalog, onChange]);

  const items = catalog?.items ?? [];
  const currentCount = items.filter(item => item.current).length;
  const choices = items.map(sprint => ({ value: sprint.id, label: sprint.current ? `${sprint.name} · Current` : sprint.name, description: `${sprint.organization} / ${sprint.path}` }));
  // A failed refresh, rename or removed connection must not silently broaden a
  // selected filter to All sprints. Keep it visible until the user clears it.
  if (value && !items.some(item => item.id === value.id)) choices.push({ value: value.id, label: `${value.name} (Unavailable)`, description: `${value.organization} / ${value.path}` });
  return <div className="board-sprint-picker" aria-busy={loading}>
    <SearchSelect label="Sprint" value={value?.id ?? ''} options={[{ value: '', label: 'All sprints' }, ...choices]} disabled={loading} emptyMessage="No matching sprints" onChange={id => { if (!id) onChange(); else { const next = items.find(item => item.id === id); if (next) onChange(next); } }} />
    {loading && <small role="status" className="muted">Loading sprints…</small>}
    {!loading && !error && catalog && currentCount === 0 && <small role="status" className="muted">No current sprint for the default team.</small>}
    {!loading && !error && currentCount > 1 && <small role="status" className="muted">Multiple current sprints. Choose a sprint.</small>}
    {!loading && catalog?.needsRefresh && <small role="status" className="muted">Refresh tasks to load sprint data.</small>}
    {error && <div className="form-error" role="alert"><small>{error}</small><Button size="sm" disabled={loading} onClick={() => setRetry(current => current + 1)}>Retry</Button></div>}
  </div>;
}
