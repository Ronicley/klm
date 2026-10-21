import { ArrowDown, ArrowUp, X } from 'lucide-react';
import { IconButton } from '../../design-system/Button';
import { SearchSelect } from '../../design-system/SearchSelect';
import type { BoardValue } from './api';

export function BoardMappingsEditor({ label, values, catalog, excluded = [], disabled, onChange }: {
  label: string; values: BoardValue[]; catalog: BoardValue[]; excluded?: string[]; disabled?: boolean; onChange: (values: BoardValue[]) => void;
}) {
  function swap(index: number, delta: number) {
    const next = [...values]; [next[index], next[index + delta]] = [next[index + delta], next[index]]; onChange(next);
  }
  const ids = new Set([...values.map(v => v.id), ...excluded]);
  return <div className="board-mapping"><strong>{label}</strong><ol>{values.map((v, i) => <li key={v.id}><span>{v.name}{i === 0 && <small>Outbound</small>}</span><IconButton label={`Move ${v.name} up`} disabled={disabled || i === 0} onClick={() => swap(i, -1)}><ArrowUp /></IconButton><IconButton label={`Move ${v.name} down`} disabled={disabled || i === values.length - 1} onClick={() => swap(i, 1)}><ArrowDown /></IconButton><IconButton label={`Remove ${v.name}`} disabled={disabled} onClick={() => onChange(values.filter(item => item.id !== v.id))}><X /></IconButton></li>)}</ol>
    <SearchSelect label={`Map ${label}`} value="" disabled={disabled} placeholder="Add label or state" options={catalog.filter(v => !ids.has(v.id)).map(v => ({ value: v.id, label: v.name }))} onChange={id => { const v = catalog.find(item => item.id === id); if (v) onChange([...values, v]); }} />
  </div>;
}
