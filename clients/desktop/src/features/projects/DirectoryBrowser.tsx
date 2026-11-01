import { useEffect, useRef, useState } from 'react';
import { ArrowRight, ChevronDown, ChevronRight, ChevronUp, Folder, FolderPlus, FolderUp, HardDrive, Home, List, MoreHorizontal, Pencil, Search, Trash2, X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Menu, MenuItem } from '../../design-system/Menu';
import { createFilesystemDirectory, deleteFilesystemDirectory, renameFilesystemDirectory, EngineRequestError, getFilesystemDirectories, getFilesystemRoots, pickDirectory, type FilesystemDirectory, type FilesystemListing, type FilesystemRoots } from '../../engine';
import './directoryBrowser.css';
import { draftRecord, flushDrafts, hasEngineDraft, useEngineDraft } from '../drafts/draftStore';
import { IS_DESKTOP } from '../../platform';
import { beginLocalActivity } from '../engines/localActivities';

type Target = { kind: 'roots'; initial?: string } | { kind: 'directory'; path: string };
type FolderEdit = { kind: 'create'; parent: string } | { kind: 'rename' | 'delete'; parent: string; item: FilesystemDirectory };

export function DirectoryBrowser({ initialPath, onSelect, onCancel, draftKey = 'form.directory' }: {
  draftKey?: string;
  initialPath: string; onSelect: (path: string) => void; onCancel: () => void;
}) {
  const [roots, setRoots] = useState<FilesystemRoots | null>(null);
  const [listing, setListing] = useState<FilesystemListing | null>(null);
  const [listingValid, setListingValid] = useState(false);
  const [locationTarget, setLocationTarget] = useEngineDraft<Target>(`${draftKey}.location`, { kind: 'roots', initial: initialPath }, false,
    value => draftRecord(value) && (value.kind === 'roots' && (value.initial === undefined || typeof value.initial === 'string') || value.kind === 'directory' && typeof value.path === 'string'));
  const [draft, setDraft] = useEngineDraft(`${draftKey}.path`, initialPath);
  const [selectedPath, setSelectedPath] = useEngineDraft(`${draftKey}.selected`, '');
  const [query, setQuery] = useEngineDraft(`${draftKey}.query`, '');
  const [descending, setDescending] = useEngineDraft(`${draftKey}.descending`, false);
  const [busy, setBusy] = useState<'listing' | 'native' | 'mutation' | null>(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const [menuPosition, setMenuPosition] = useState<{ x: number; y: number } | null>(null);
  const [menuTarget, setMenuTarget] = useState<FilesystemDirectory | null>(null);
  const [folderEdit, setFolderEdit] = useEngineDraft<FolderEdit | null>(`${draftKey}.edit`, null);
  const [folderName, setFolderName] = useEngineDraft(`${draftKey}.folderName`, '');
  const [folderError, setFolderError] = useState('');
  const [mutationUncertain, setMutationUncertain] = useEngineDraft(`${draftKey}.uncertain`, false);
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const [nativeError, setNativeError] = useState('');
  const [failedTarget, setFailedTarget] = useState<Target | null>(null);
  const generation = useRef(0);
  const request = useRef<AbortController | null>(null);
  const pathInput = useRef<HTMLInputElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const directoryList = useRef<HTMLUListElement>(null);
  const folderNameInput = useRef<HTMLInputElement>(null);
  const deleteButtons = useRef<HTMLDivElement>(null);

  function invalidate() {
    generation.current++;
    request.current?.abort();
    request.current = null;
  }

  function begin() {
    invalidate();
    setMenuOpen(false); setMenuPosition(null); setMenuTarget(null);
    const controller = new AbortController();
    const version = generation.current;
    request.current = controller;
    return { controller, current: () => generation.current === version && request.current === controller && !controller.signal.aborted };
  }

  function cancel() {
    invalidate();
    onCancel();
  }

  async function navigate(target: Target, selectedAfter = '', message = '', restoring = false) {
    const { controller, current } = begin();
    setBusy('listing'); setError(''); setNativeError(''); setFailedTarget(null);
    if (!restoring) { setSelectedPath(''); setQuery(''); setFolderEdit(null); setMutationUncertain(false); setLocationTarget(target); }
    setNotice(''); setFolderError('');
    // Keep parent metadata for recovery, but never select a failed/stale listing.
    setListingValid(false);
    if (target.kind === 'directory' && !restoring) setDraft(target.path);
    else setListing(null);
    let attempted = target;
    try {
      let path: string | null = target.kind === 'directory' ? target.path : null;
      if (restoring && target.kind === 'directory') {
        const locations = await getFilesystemRoots(controller.signal);
        if (!current()) return;
        setRoots(locations);
      }
      if (target.kind === 'roots') {
        const result = await getFilesystemRoots(controller.signal);
        if (!current()) return;
        setRoots(result);
        path = target.initial?.trim() ? target.initial : target.initial !== undefined ? result.home : null;
        if (!restoring || !hasEngineDraft(`${draftKey}.path`)) setDraft(path ?? '');
      }
      if (path !== null) {
        attempted = { kind: 'directory', path };
        const result = await getFilesystemDirectories(path, controller.signal);
        if (!current()) return;
        setListing(result); setListingValid(true);
        if (!restoring || !hasEngineDraft(`${draftKey}.path`)) setDraft(result.path);
        setLocationTarget({ kind: 'directory', path: result.path });
        if (result.directories.some(item => item.path === selectedAfter)) setSelectedPath(selectedAfter);
        setNotice(message);
      }
    } catch (failure) {
      if (current()) {
        setFailedTarget(attempted);
        setError(failure instanceof Error ? failure.message : 'Could not load directories. Retry or enter another path.');
      }
    } finally {
      if (current()) {
        request.current = null;
        setBusy(null);
        heading.current?.focus();
      }
    }
  }

  async function nativePicker() {
    const finished = beginLocalActivity('Native directory picker', true);
    const { controller, current } = begin();
    setBusy('native'); setNativeError('');
    try {
      const selected = await pickDirectory(controller.signal);
      if (current() && selected) {
        invalidate();
        onSelect(selected);
      }
    } catch (failure) {
      if (current()) setNativeError(failure instanceof Error ? failure.message : 'Could not open the native picker. Browse directories instead.');
    } finally {
      finished();
      if (current()) {
        request.current = null;
        setBusy(null);
        heading.current?.focus();
      }
    }
  }

  function cancelNative() {
    invalidate();
    setBusy(null); setNativeError('');
    heading.current?.focus();
  }

  useEffect(() => {
    pathInput.current?.focus();
    void navigate(IS_DESKTOP ? locationTarget : { kind: 'roots', initial: initialPath }, '', '', IS_DESKTOP);
    return invalidate;
    // Each browser visit mounts anew; interactions own subsequent navigation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (folderEdit?.kind === 'delete') deleteButtons.current?.querySelector('button')?.focus();
    else if (folderEdit) { folderNameInput.current?.focus(); folderNameInput.current?.select(); }
  }, [folderEdit]);

  const nativeBusy = busy === 'native';
  const mutating = busy === 'mutation';
  const navigationLocked = nativeBusy || mutating;
  const pathDraft = selectedPath || draft;
  const selectionBelongsToListing = !selectedPath || listing?.directories.some(item => item.path === selectedPath);
  const canSelect = listingValid && listing !== null && !busy && folderEdit === null && !error && !nativeError && draft === listing.path && selectionBelongsToListing;
  const canCreate = listingValid && listing !== null && !busy && folderEdit === null && !error && draft === listing.path;
  const items = listingValid && listing ? listing.directories : listing === null ? roots?.roots ?? [] : [];
  const filter = query.trim().toLocaleLowerCase();
  const visibleItems = items.filter(item => item.name.toLocaleLowerCase().includes(filter))
    .sort((a, b) => (descending ? -1 : 1) * a.name.localeCompare(b.name));
  const location = listingValid && listing ? listing.path : draft;
  const locationName = location === roots?.home ? 'Home' : location.split(/[\\/]/).filter(Boolean).pop() || location || 'Locations';
  const rows = [
    ...(listingValid && listing?.parent ? [{ name: '...', path: listing.parent, parent: true }] : []),
    ...visibleItems.map(item => ({ ...item, parent: false })),
  ];
  const sideRoots = roots?.roots.filter(item => item.path !== roots.home) ?? [];

  function selectItem(path: string) {
    if (listing === null) { void navigate({ kind: 'directory', path }); return; }
    if (!listingValid || busy || folderEdit !== null || error) return;
    setDraft(listing.path); setSelectedPath(path); setNativeError('');
  }

  function moveSelection(index: number) {
    const item = rows[index];
    if (!item) return;
    if (item.parent && listing) { setSelectedPath(''); setDraft(listing.path); }
    else if (listing !== null) selectItem(item.path);
    directoryList.current?.querySelectorAll<HTMLButtonElement>('[data-directory-row]')[index]?.focus();
  }

  function openContextMenu(path: string | null, position: { x: number; y: number }) {
    if (!canCreate || !listing) return;
    const item = listing.directories.find(item => item.path === path) ?? null;
    setSelectedPath(item?.path ?? ''); setDraft(listing.path);
    setMenuTarget(item); setMenuPosition(position); setMenuOpen(true);
  }

  function startFolderEdit(kind: FolderEdit['kind']) {
    if (!canCreate || !listing || kind !== 'create' && !menuTarget) return;
    const edit: FolderEdit = kind === 'create' ? { kind, parent: listing.path } : { kind, parent: listing.path, item: menuTarget! };
    setMenuOpen(false); setMenuPosition(null); setMenuTarget(null);
    if (kind === 'create') { setSelectedPath(''); setQuery(''); }
    setNativeError(''); setNotice('');
    setFolderEdit(edit); setFolderName(edit.kind === 'create' ? 'New folder' : edit.item.name);
    setFolderError(''); setMutationUncertain(false);
  }

  function closeFolderEdit() {
    if (mutating) return;
    setFolderEdit(null); setFolderError(''); setMutationUncertain(false);
    heading.current?.focus();
  }

  async function submitFolderEdit() {
    if (!folderEdit || !folderName.trim() || busy || request.current || mutationUncertain) return;
    const finished = beginLocalActivity('Updating directory');
    const edit = folderEdit;
    const { controller, current } = begin();
    setBusy('mutation'); setFolderError('');
    const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(15000)]);
    try {
      if (IS_DESKTOP) { setMutationUncertain(true); await flushDrafts(); }
      if (edit.kind === 'create') {
        const result = await createFilesystemDirectory(edit.parent, folderName, signal);
        if (current()) void navigate({ kind: 'directory', path: result.path }, '', 'Folder created.');
      } else if (edit.kind === 'rename') {
        const result = await renameFilesystemDirectory(edit.parent, edit.item.name, folderName, signal);
        if (current()) void navigate({ kind: 'directory', path: edit.parent }, result.path, 'Folder renamed.');
      } else {
        await deleteFilesystemDirectory(edit.parent, edit.item.name, edit.item.name, signal);
        if (current()) void navigate({ kind: 'directory', path: edit.parent }, '', 'Folder deleted.');
      }
    } catch (failure) {
      if (current()) {
        const uncertain = !(failure instanceof EngineRequestError) || failure.status >= 500;
        setMutationUncertain(uncertain);
        setFolderError(failure instanceof EngineRequestError ? failure.message : 'The folder change could not be confirmed. Refresh this directory before trying again.');
      }
    } finally {
      finished();
      if (current()) { request.current = null; setBusy(null); }
    }
  }

  return <section className="directory-browser" onKeyDown={event => {
    if (event.key === 'Escape') {
      event.preventDefault(); event.stopPropagation();
      if (mutating) return;
      if (folderEdit !== null) closeFolderEdit(); else cancel();
    }
  }}>
    <header className="directory-browser__header">
      <Button size="sm" disabled={mutating} onClick={cancel}>Cancel</Button>
      <h2 id="project-dialog-title" ref={heading} tabIndex={-1}>Choose directory</h2>
      <Button size="sm" variant="primary" disabled={!canSelect} onClick={() => {
        if (canSelect && listing) { invalidate(); onSelect(selectedPath || listing.path); }
      }}>Select directory</Button>
    </header>
    <div className="directory-browser__body">
      <nav className="directory-browser__sidebar" aria-label="Filesystem locations">
        <div className="directory-browser__places">
          <span className="directory-browser__sidebar-label">Locations</span>
          <Button variant="ghost" className={listingValid && listing?.path === roots?.home ? 'is-active' : ''} disabled={navigationLocked || !roots?.home} onClick={() => {
            if (roots?.home) void navigate({ kind: 'directory', path: roots.home });
          }}><Home aria-hidden="true" /><span>Home</span></Button>
          {sideRoots.map(item => <Button key={item.path} variant="ghost" title={item.path} className={listingValid && listing?.path === item.path ? 'is-active' : ''} disabled={navigationLocked} onClick={() => void navigate({ kind: 'directory', path: item.path })}><HardDrive aria-hidden="true" /><span>{item.name === '/' ? 'Filesystem' : item.name}</span></Button>)}
          <Button variant="ghost" className={!listing && !busy && !error ? 'is-active' : ''} disabled={navigationLocked} onClick={() => void navigate({ kind: 'roots' })}><List aria-hidden="true" /><span>All locations</span></Button>
        </div>
        {roots?.nativePicker && <Button size="sm" variant="ghost" className="directory-browser__native" disabled={navigationLocked || folderEdit !== null} onClick={() => void nativePicker()}>Native picker on engine</Button>}
      </nav>
      <div className="directory-browser__main">
        <div className="directory-browser__toolbar">
          <Button variant="ghost" className="directory-browser__location" aria-label="Edit directory path" title={location || 'Locations'} disabled={navigationLocked || folderEdit !== null} onClick={() => { pathInput.current?.focus(); pathInput.current?.select(); }}>
            {location === roots?.home ? <Home aria-hidden="true" /> : <Folder aria-hidden="true" />}<span>{locationName}</span><ChevronDown aria-hidden="true" />
          </Button>
          <div className="directory-browser__search">
            <Search aria-hidden="true" />
            <input aria-label="Search directories" type="search" placeholder="Search" value={query} disabled={navigationLocked || folderEdit !== null} onChange={event => { setQuery(event.target.value); setSelectedPath(''); }} />
            {query && <IconButton label="Clear search" disabled={navigationLocked || folderEdit !== null} onClick={() => { setQuery(''); setSelectedPath(''); }}><X aria-hidden="true" /></IconButton>}
          </div>
          <Menu label="Directory actions" role="menu" open={menuOpen} position={menuPosition ?? undefined} side="bottom" className="directory-browser__context-menu" onOpenChange={open => {
            setMenuOpen(open); setMenuPosition(null);
            setMenuTarget(open ? listing?.directories.find(item => item.path === selectedPath) ?? null : null);
          }} trigger={props => <IconButton {...props} label="Directory actions" disabled={!canCreate}><MoreHorizontal aria-hidden="true" /></IconButton>}>
            <MenuItem role="menuitem" disabled={!canCreate} onClick={() => startFolderEdit('create')}><FolderPlus aria-hidden="true" />New folder</MenuItem>
            {menuTarget && <>
              <MenuItem role="menuitem" disabled={!canCreate} onClick={() => startFolderEdit('rename')}><Pencil aria-hidden="true" />Rename</MenuItem>
              <MenuItem role="menuitem" className="directory-browser__delete-action" disabled={!canCreate} onClick={() => startFolderEdit('delete')}><Trash2 aria-hidden="true" />Delete</MenuItem>
            </>}
          </Menu>
        </div>
        <div className="directory-browser__column-heading">
          <Button variant="ghost" size="sm" aria-label={descending ? 'Sort directories A to Z' : 'Sort directories Z to A'} disabled={navigationLocked || folderEdit !== null} onClick={() => setDescending(value => !value)}>Name{descending ? <ChevronDown aria-hidden="true" /> : <ChevronUp aria-hidden="true" />}</Button>
        </div>
        <div className="directory-browser__contents" tabIndex={0} aria-label="Directory contents" aria-busy={busy === 'listing' || mutating} onContextMenu={event => {
          if (!canCreate || event.target instanceof Element && event.target.closest('input, textarea, [contenteditable="true"]')) return;
          event.preventDefault(); event.stopPropagation();
          const row = event.target instanceof Element ? event.target.closest('[data-directory-entry]') : null;
          openContextMenu(row?.getAttribute('data-directory-entry') ?? null, { x: event.clientX, y: event.clientY });
        }} onKeyDown={event => {
          if (canCreate && (event.key === 'ContextMenu' || event.shiftKey && event.key === 'F10')) {
            event.preventDefault(); event.stopPropagation();
            const row = event.target instanceof Element ? event.target.closest('[data-directory-entry]') : null;
            const bounds = (row ?? event.currentTarget).getBoundingClientRect();
            openContextMenu(row ? row.getAttribute('data-directory-entry') : selectedPath, { x: bounds.left + 16, y: bounds.top + 16 });
          }
        }}>
          {busy === 'listing' || nativeBusy ? <div className="directory-browser__message" role="status"><Folder aria-hidden="true" /><p>{nativeBusy ? 'Choosing directory on engine...' : 'Loading directories...'}</p>{nativeBusy && <Button size="sm" onClick={cancelNative}>Cancel selection</Button>}</div> : error ? <div className="directory-browser__message"><p role="alert" className="form-error">{error}</p>{failedTarget && <Button size="sm" onClick={() => void navigate(failedTarget)}>Retry</Button>}</div> : <>
            {nativeError && <p role="alert" className="form-error directory-browser__notice">{nativeError}</p>}
            {folderEdit && <form className="directory-browser__new-folder" aria-label={folderEdit.kind === 'create' ? 'Create folder' : folderEdit.kind === 'rename' ? 'Rename folder' : 'Delete folder'} onSubmit={event => { event.preventDefault(); void submitFolderEdit(); }}>
              {folderEdit.kind === 'delete' ? <>
                <p className="directory-browser__delete-question">Delete “{folderEdit.item.name}”?</p>
                <p className="directory-browser__target-path">{folderEdit.item.path}</p>
                <div ref={deleteButtons} className="directory-browser__delete-buttons">
                  <Button size="sm" disabled={mutating} onClick={closeFolderEdit}>Cancel</Button>
                  <Button size="sm" type="submit" variant="danger-soft" disabled={mutating || mutationUncertain}>{mutating ? 'Deleting...' : 'Delete folder'}</Button>
                </div>
              </> : <>
                {folderEdit.kind === 'rename' && <p className="directory-browser__target-path">{folderEdit.item.path}</p>}
                <div className="directory-browser__new-folder-fields">
                  {folderEdit.kind === 'create' ? <FolderPlus aria-hidden="true" /> : <Pencil aria-hidden="true" />}
                  <label className="sr-only" htmlFor="new-directory-name">Folder name</label>
                  <input ref={folderNameInput} id="new-directory-name" value={folderName} maxLength={255} required disabled={mutating} autoComplete="off" spellCheck={false} aria-invalid={!!folderError} aria-describedby={folderError ? 'new-directory-error' : undefined} onChange={event => { setFolderName(event.target.value); if (!mutationUncertain) setFolderError(''); }} />
                  <Button size="sm" type="submit" variant="primary" disabled={mutating || !folderName.trim() || mutationUncertain}>{folderEdit.kind === 'create' ? mutating ? 'Creating...' : 'Create' : mutating ? 'Renaming...' : 'Rename'}</Button>
                  <Button size="sm" disabled={mutating} onClick={closeFolderEdit}>Cancel</Button>
                </div>
              </>}
              {folderError && <p id="new-directory-error" role="alert" className="form-error">{folderError}</p>}
              {mutationUncertain && <Button size="sm" onClick={() => void navigate({ kind: 'directory', path: folderEdit.parent })}>Refresh directory</Button>}
            </form>}
            <ul ref={directoryList} className="directory-browser__list" aria-label={listing ? 'Subdirectories' : 'Filesystem roots'}>
              {rows.map((item, index) => <li key={item.path} data-directory-entry={item.parent ? '' : item.path} className={!item.parent && selectedPath === item.path ? 'is-selected' : ''}>
                <Button variant="ghost" data-directory-row disabled={navigationLocked || folderEdit !== null} tabIndex={selectedPath ? selectedPath === item.path ? 0 : -1 : index === 0 ? 0 : -1} aria-pressed={!item.parent && listing !== null ? selectedPath === item.path : undefined} aria-label={item.parent ? 'Parent directory (...)' : undefined} className="directory-browser__row" title={item.path}
                  onClick={() => { if (item.parent && listing) { setSelectedPath(''); setDraft(listing.path); } else selectItem(item.path); }} onDoubleClick={() => { if (listing !== null) void navigate({ kind: 'directory', path: item.path }); }}
                  onKeyDown={event => {
                    if (event.key === 'Enter' || event.key === 'ArrowRight') { event.preventDefault(); void navigate({ kind: 'directory', path: item.path }); }
                    else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
                      event.preventDefault();
                      moveSelection(event.key === 'Home' ? 0 : event.key === 'End' ? rows.length - 1 : Math.max(0, Math.min(rows.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1))));
                    }
                  }}>{item.parent ? <FolderUp aria-hidden="true" /> : <Folder aria-hidden="true" />}<span>{item.name}</span></Button>
                <IconButton className="directory-browser__open" disabled={navigationLocked || folderEdit !== null} label={item.parent ? 'Open parent directory' : `Open ${item.name}`} tabIndex={selectedPath === item.path ? 0 : -1} onClick={() => void navigate({ kind: 'directory', path: item.path })}><ChevronRight aria-hidden="true" /></IconButton>
              </li>)}
            </ul>
            {visibleItems.length === 0 && <div className="directory-browser__message"><Folder aria-hidden="true" /><p>{filter ? 'No matching directories' : listing?.incomplete ? 'No directories to display' : listing ? 'No subdirectories' : 'No locations available'}</p>{filter && <Button size="sm" onClick={() => setQuery('')}>Clear search</Button>}</div>}
            {listingValid && listing?.incomplete && <p className="directory-browser__notice" role="status">Directory list is incomplete. Enter a path to open another directory.{listing.skipped > 0 ? ` Skipped entries: ${listing.skipped}.` : ''}</p>}
          </>}
        </div>
        <div className="directory-browser__summary" role="status">{!busy && !error && (listing === null || listingValid) ? `${notice ? `${notice} · ` : ''}${visibleItems.length}${filter ? ` of ${items.length}` : ''} ${listing ? items.length === 1 ? 'directory' : 'directories' : items.length === 1 ? 'location' : 'locations'}` : '\u00a0'}</div>
      </div>
    </div>
    <footer className="directory-browser__footer">
      <label htmlFor="engine-directory-path">Directory</label>
      <div className="directory-browser__path">
        <input ref={pathInput} id="engine-directory-path" value={pathDraft} disabled={navigationLocked || folderEdit !== null} spellCheck={false} autoComplete="off" title={pathDraft} onChange={event => {
          if (busy === 'listing') { invalidate(); setBusy(null); }
          setSelectedPath(''); setDraft(event.target.value);
        }} onKeyDown={event => {
          if (event.key === 'Enter') { event.preventDefault(); event.stopPropagation(); if (pathDraft.trim() && !navigationLocked && folderEdit === null) void navigate({ kind: 'directory', path: pathDraft }); }
        }} />
        <IconButton label="Open directory path" disabled={navigationLocked || folderEdit !== null || !pathDraft.trim()} onClick={() => void navigate({ kind: 'directory', path: pathDraft })}><ArrowRight aria-hidden="true" /></IconButton>
      </div>
    </footer>
  </section>;
}
