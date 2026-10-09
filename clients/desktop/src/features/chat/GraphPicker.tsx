import { useRef, useState, type KeyboardEvent } from 'react';
import { Bot, Check, ChevronDown, Workflow } from 'lucide-react';
import { Button } from '../../design-system/Button';
import { Menu, MenuItem } from '../../design-system/Menu';
import type { Graph } from '../graphs/demo';
import '../graphs/graph-running-led.css';

export function GraphPicker({ graphs, value, selectedName, running = false, disabled = false, loading = false, error, onReload, onChange, kind = 'graph' }: {
	 kind?: 'graph' | 'agent';
  graphs: Graph[];
  value: string;
  running?: boolean;
  selectedName?: string;
  disabled?: boolean;
  loading?: boolean;
  error?: string;
  onReload?: () => void;
  onChange: (graphId: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const options = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const selected = graphs.find(graph => graph.id === value);
  const label = selectedName ?? selected?.name ?? (value || 'None');
  const noun = kind === 'agent' ? 'Agent' : 'Graph';
  const plural = kind === 'agent' ? 'agents' : 'graphs';
  const Icon = kind === 'agent' ? Bot : Workflow;
  const searchable = graphs.length > 5;
  const query = searchable ? search.trim().toLowerCase() : '';
  const matches = graphs.filter(graph => `${graph.name} ${graph.description}`.toLowerCase().includes(query));

  function choose(graphId: string) { if (!disabled) onChange(graphId); setOpen(false); }

  function navigate(event: KeyboardEvent<HTMLDivElement>) {
    const inSearch = event.target instanceof HTMLInputElement;
    if (inSearch && event.key === 'Enter') {
      event.preventDefault();
      const first = matches.find(graph => graph.enabled);
      if (first) choose(first.id);
      return;
    }
    const items = [...(options.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
    if (!items.length) return;
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    let next: number;
    if (event.key === 'ArrowDown') next = Math.min(index + 1, items.length - 1);
    else if (event.key === 'ArrowUp') next = index <= 0 ? items.length - 1 : index - 1;
    else if (!inSearch && event.key === 'Home') next = 0;
    else if (!inSearch && event.key === 'End') next = items.length - 1;
    else return;
    event.preventDefault(); items[next].focus(); items[next].scrollIntoView({ block: 'nearest' });
  }

  return <Menu label={plural} className="model-menu graph-picker-menu" align="start" open={open} onOpenChange={next => { setSearch(''); setOpen(next); if (next) onReload?.(); }} trigger={props => <Button {...props} disabled={disabled} variant="ghost" size="sm" className="model-picker-trigger graph-picker-trigger" aria-label={`Choose ${kind}: ${value ? label : 'None'}`} title={`${noun}: ${label}`}>
    {running && value ? <span className="graph-picker-run-indicator" role="img" aria-label="Graph run in progress" title="Graph run in progress" /> : <Icon />}<span className="graph-picker-name">{noun} · {label}</span><ChevronDown />
  </Button>}>
    <div ref={options} onKeyDown={navigate}>
      {searchable && <div className="model-menu-search"><input data-menu-autofocus type="search" aria-label={`Search ${plural}`} placeholder={`Search ${plural}`} value={search} onChange={event => { setSearch(event.target.value); list.current?.scrollTo({ top: 0 }); }} /></div>}
      <div ref={list} className="model-menu-list" aria-label={`Available ${plural}`}>
        {matches.map(graph => <MenuItem key={graph.id} className="model-menu-option graph-picker-option" selected={selected?.id === graph.id} disabled={!graph.enabled} title={graph.description} onClick={() => choose(graph.id)}>
          <span><span className="model-menu-option-name">{graph.name}</span><small>{graph.enabled ? graph.description : 'Disabled'}</small></span>{selected?.id === graph.id && <Check />}
        </MenuItem>)}
        {error ? <p className="menu-feedback" role="alert">{error}</p> : !matches.length && <p className="menu-feedback muted" role="status">{loading ? `Loading ${plural}...` : query ? `No matching ${plural}.` : `No ${plural} available.`}</p>}
      </div>
      <div className="model-menu-footer"><MenuItem selected={!value} onClick={() => choose('')}>None{!value && <Check />}</MenuItem></div>
    </div>
  </Menu>;
}
