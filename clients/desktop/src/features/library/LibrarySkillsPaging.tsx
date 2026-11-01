import { useEffect, useRef, useState } from 'react';
import { Button } from '../../design-system/Button';
import { request } from '../../engine';
import type { LibraryInventory } from './api';

export function LibrarySkillsPaging({ inventory, onPage }: { inventory: LibraryInventory; onPage: (value: LibraryInventory) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const requestRef = useRef<AbortController | null>(null);
  useEffect(() => () => requestRef.current?.abort(), []);
  async function load(offset: number) {
    if (busy) return;
    const controller = new AbortController(); requestRef.current = controller; setBusy(true); setError('');
    try {
      const page = await request<LibraryInventory>(`/api/library?skillsOffset=${offset}&search=${encodeURIComponent(inventory.search ?? '')}`, 'GET', undefined, 20000, controller.signal);
      if (!controller.signal.aborted) onPage({ ...inventory, skills: page.skills, skillsHasMore: page.skillsHasMore, skillsNextOffset: page.skillsNextOffset, skillsWindowStart: offset });
    } catch (error) { if (!controller.signal.aborted) setError(error instanceof Error ? error.message : 'Could not load skills.'); }
    finally { if (!controller.signal.aborted) setBusy(false); }
  }
  return <div>{!!inventory.skillsWindowStart && <Button size="sm" disabled={busy} onClick={() => void load(Math.max(0, (inventory.skillsWindowStart ?? 0) - 50))}>Previous skills</Button>}{inventory.skillsHasMore && <Button size="sm" disabled={busy} onClick={() => void load(inventory.skillsNextOffset ?? 0)}>Next skills</Button>}{error && <p className="form-error" role="alert">{error}</p>}</div>;
}
