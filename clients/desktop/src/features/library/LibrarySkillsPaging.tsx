import { useEffect, useRef, useState } from 'react';
import { AutoPage } from '../pagination/AutoPage';
import { Button } from '../../design-system/Button';
import { request } from '../../engine';
import type { LibraryInventory } from './api';

export function LibrarySkillsPaging({ inventory, onPage }: { inventory: LibraryInventory; onPage: (value: LibraryInventory) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const requestRef = useRef<AbortController | null>(null);
  useEffect(() => () => requestRef.current?.abort(), []);
  async function load(offset: number) {
    if (requestRef.current) return;
    const controller = new AbortController(); requestRef.current = controller; setBusy(true); setError('');
    try {
      const page = await request<LibraryInventory>(`/api/library?limit=25&skillsOffset=${offset}&search=${encodeURIComponent(inventory.search ?? '')}`, 'GET', undefined, 20000, controller.signal);
      if (!controller.signal.aborted) { if (page.skillsHasMore && (page.skillsNextOffset ?? 0) <= offset) throw new Error('Skill pagination did not advance. Retry.'); onPage({ ...inventory, skills: [...new Map([...inventory.skills, ...page.skills].map(item => [item.id, item])).values()], skillsHasMore: page.skillsHasMore, skillsNextOffset: page.skillsNextOffset }); }
    } catch (error) { if (!controller.signal.aborted) setError(error instanceof Error ? error.message : 'Could not load skills.'); }
    finally { if (requestRef.current === controller) { requestRef.current = null; if (!controller.signal.aborted) setBusy(false); } }
  }
  return <AutoPage hasMore={!!inventory.skillsHasMore} cursor={`${inventory.search}/${inventory.skillsNextOffset}`} busy={busy} error={error} onLoad={() => void load(inventory.skillsNextOffset ?? 0)} />;
}
