import { Check, Cloud, Database, House, Monitor, Pencil, Plus, Server, Trash2 } from 'lucide-react';
import { useLayoutEffect, useState } from 'react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Menu } from '../../design-system/Menu';
import { IS_DESKTOP } from '../../platform';
import { useEngines } from './EngineProvider';
import type { SavedEngine } from './engineRegistry';

export const engineIcons = { monitor: Monitor, server: Server, cloud: Cloud, house: House, database: Database };
export function engineInitials(name: string) {
  const words = name.normalize('NFC').trim().split(/\s+/u);
  return (words.length > 1 ? Array.from(words[0])[0] + (Array.from(words[1])[0] ?? '') : Array.from(words[0] ?? '').slice(0, 2).join('')).toUpperCase();
}
export function EngineAvatar({ engine }: { engine?: Pick<SavedEngine, 'name' | 'iconId'> }) {
  const Icon = engine?.iconId && Object.hasOwn(engineIcons, engine.iconId) ? engineIcons[engine.iconId] : undefined;
  return <span className="engine-avatar" aria-hidden="true">{Icon ? <Icon /> : engine ? engineInitials(engine.name) : <Server />}</span>;
}
export function EngineList({ close }: { close: () => void }) {
  const engines = useEngines()!;
  const [search, setSearch] = useState('');
  const matches = engines.registry?.engines.filter(engine => `${engine.name} ${engine.url}`.toLocaleLowerCase().includes(search.toLocaleLowerCase())) ?? [];
  return <div className="engine-list-content">
    <Input data-menu-autofocus aria-label="Search saved engines" placeholder="Search engines" value={search} onChange={event => setSearch(event.target.value)} />
    <ul className="engine-list" onKeyDown={event => {
      if (!(event.target instanceof HTMLButtonElement) || !event.target.hasAttribute('data-engine-select') || !['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
      event.preventDefault();
      const buttons = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[data-engine-select]')];
      const index = buttons.indexOf(event.target);
      buttons[event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length]?.focus();
    }}>
      {matches.map(engine => <li key={engine.id} className="engine-row">
        <Button className="engine-select" variant="ghost" data-engine-select aria-current={engines.active?.url === engine.url ? 'true' : undefined} disabled={engines.busy} onClick={() => { close(); engines.select(engine); }}>
          <EngineAvatar engine={engine} /><span className="engine-entry-copy"><strong>{engine.name}</strong><small title={engine.url}>{engine.url}</small></span>{engines.active?.url === engine.url && <Check aria-hidden="true" className="engine-current" />}
        </Button>
        <IconButton size="sm" label={`Edit ${engine.name}`} disabled={engines.busy} onClick={() => { close(); engines.edit(engine); }}><Pencil /></IconButton>
        <IconButton size="sm" label={`Remove ${engine.name}`} disabled={engines.busy} onClick={() => { close(); engines.remove(engine); }}><Trash2 /></IconButton>
      </li>)}
      {!matches.length && <li className="engine-list-empty">{search ? 'No matching engines' : 'No saved engines'}</li>}
    </ul>
    <Button className="engine-add" variant="ghost" disabled={engines.busy} onClick={() => { close(); engines.add(); }}><Plus aria-hidden="true" />Add engine</Button>
    <Button variant="ghost" size="sm" disabled={engines.busy} onClick={() => { close(); engines.manage(); }}>Manage connections</Button>
  </div>;
}
export function EngineSwitcher({ rail = false }: { rail?: boolean }) {
  if (!IS_DESKTOP) return null;
  return <NativeSwitcher rail={rail} />;
}
function NativeSwitcher({ rail }: { rail: boolean }) {
  const engines = useEngines();
  const [open, setOpen] = useState(false);
  useLayoutEffect(() => { if (!rail || !engines) return; engines.rail(true); return () => engines.rail(false); }, [rail, engines?.rail]);
  if (!engines) return null;
  return <Menu label="Saved engines" open={open} onOpenChange={setOpen} className="engine-switcher-menu" side="top" align="start"
    trigger={props => <IconButton {...props} className="engine-switcher-trigger" label={`Switch engine${engines.active ? `: ${engines.active.name}` : ''}`} disabled={engines.busy} onKeyDown={event => { if (['ArrowUp', 'ArrowDown'].includes(event.key)) { event.preventDefault(); setOpen(true); } }}><EngineAvatar engine={engines.active} /></IconButton>}>
    <EngineList close={() => setOpen(false)} />
  </Menu>;
}
