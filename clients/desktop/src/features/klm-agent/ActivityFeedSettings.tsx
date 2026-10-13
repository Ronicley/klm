import { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { ENGINE_URL } from '../../engine';
import type { ActivityCatalogItem } from './githubActivityApi';

export const activityPreferencesKey = `klm.agent.github-feed.preferences.v1:${encodeURIComponent(ENGINE_URL)}`;
export type ActivityOverrides = Record<string, boolean>;
export function readActivityPreferences(): ActivityOverrides {
  const raw = localStorage.getItem(activityPreferencesKey);
  if (raw === null) return {};
  const value: unknown = JSON.parse(raw);
  if (!value || typeof value !== 'object' || !('version' in value) || value.version !== 1 || !('overrides' in value) || !value.overrides || typeof value.overrides !== 'object' || Array.isArray(value.overrides)) throw new Error('Feed preferences could not be loaded.');
  const overrides: ActivityOverrides = Object.create(null);
  for (const [id, enabled] of Object.entries(value.overrides)) {
    if (!/^[a-z0-9_]+$/.test(id) || ['__proto__', 'constructor', 'prototype'].includes(id) || typeof enabled !== 'boolean') throw new Error('Feed preferences could not be loaded.');
    overrides[id] = enabled;
  }
  return overrides;
}
export function ActivityFeedSettings({ items, overrides, error, onChange, onClose }: {
  items: ActivityCatalogItem[]; overrides: ActivityOverrides; error: string;
  onChange: (overrides: ActivityOverrides) => void; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [search, setSearch] = useState('');
  useEffect(() => {
    const focus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialog.current?.showModal();
    return () => { if (focus?.isConnected) focus.focus(); };
  }, []);
  return <dialog ref={dialog} className="settings-dialog activity-settings" aria-labelledby="activity-settings-title" onCancel={event => { event.preventDefault(); onClose(); }}>
    <div className="dialog-heading"><h2 id="activity-settings-title">Feed settings</h2><IconButton label="Close feed settings" onClick={onClose}><X /></IconButton></div>
    <div className="activity-settings-toolbar"><Input aria-label="Search event types" placeholder="Search event types" value={search} onChange={event => setSearch(event.target.value)} /><Button size="sm" variant="ghost" onClick={() => onChange({ ...overrides, ...Object.fromEntries(items.map(item => [item.id, true])) })}>Enable all</Button></div>
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="activity-settings-list">{items.filter(item => item.label.toLowerCase().includes(search.toLowerCase())).map(item => <label key={item.id}><input type="checkbox" checked={overrides[item.id] ?? item.defaultEnabled} onChange={event => onChange({ ...overrides, [item.id]: event.target.checked })} /><span>{item.label}</span></label>)}</div>
  </dialog>;
}
