import { useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { Check, ChevronDown } from 'lucide-react';
import { Button } from './Button';
import { Input } from './Input';
import { Menu, MenuItem } from './Menu';

export function SearchSelect({ id, label, value, options, onChange, placeholder = 'Select an option', searchPlaceholder = 'Search', emptyMessage = 'No results found', disabled = false, footer, searchValue, onSearchChange, required = false }: {
  id?: string;
  label: string;
  value: string;
  options: readonly { value: string; label: string; description?: string }[];
  onChange: (value: string) => void;
  placeholder?: string;
  searchPlaceholder?: string;
  emptyMessage?: string;
  disabled?: boolean;
  footer?: ReactNode;
  searchValue?: string;
  onSearchChange?: (value: string) => void;
  required?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const list = useRef<HTMLDivElement>(null);
  const selected = options.find(option => option.value === value);
  const query = (searchValue ?? search).trim().toLowerCase();
  const matches = onSearchChange ? options : options.filter(option => `${option.label} ${option.description ?? ''}`.toLowerCase().includes(query));

  function choose(next: string) { onChange(next); setOpen(false); }

  function navigate(event: KeyboardEvent<HTMLDivElement>) {
    const items = [...(list.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
    const inSearch = event.target instanceof HTMLInputElement;
    if (inSearch && event.key === 'Enter') {
      event.preventDefault();
      if (matches[0]) choose(matches[0].value);
      return;
    }
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

  return <div className="search-select">
    {required && <input aria-label={`${label} selection`} tabIndex={-1} style={{ position: 'absolute', width: 1, height: 1, opacity: 0, pointerEvents: 'none' }} required value={value} onChange={() => {}} onInvalid={() => setOpen(true)} />}
    <Menu label={label} side="bottom" className="search-select-menu" open={open && !disabled} onOpenChange={next => { setSearch(''); setOpen(next); }}
      trigger={props => <Button {...props} id={id} className="search-select-trigger" disabled={disabled} aria-label={`${label}: ${selected?.label ?? placeholder}`}>
        <span className={!selected ? 'muted' : undefined}>{selected?.label ?? placeholder}</span><ChevronDown />
      </Button>}>
      <div onKeyDown={navigate}>
        <Input data-menu-autofocus type="search" aria-label={`Search ${label.toLowerCase()}`} placeholder={searchPlaceholder} value={searchValue ?? search} onChange={event => onSearchChange ? onSearchChange(event.target.value) : setSearch(event.target.value)} />
        <div ref={list} className="search-select-options">
          {matches.map(option => <MenuItem key={option.value} selected={option.value === value} onClick={() => choose(option.value)}>
            <span className="search-select-option-text"><span>{option.label}</span>{option.description && <small>{option.description}</small>}</span>{option.value === value && <Check />}
          </MenuItem>)}
          {!matches.length && <p className="search-select-empty" role="status">{emptyMessage}</p>}
          {footer}
        </div>
      </div>
    </Menu>
  </div>;
}
