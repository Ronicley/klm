import { useEffect, useRef, useState } from 'react';
import { AlertCircle, ArrowLeft, BookOpen, Check, ChevronRight, LockKeyhole, Plug, RefreshCw, Search } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { getLibrary, type CapabilitySelection, type LibraryInventory, type MCPConnection, type MCPSelection, type SkillEntry } from './api';
import { LibrarySkillsPaging } from './LibrarySkillsPaging';

type Category = 'skills' | 'connections';
type PickerEntry = { id: string; name: string; description: string; missing?: boolean; skill?: SkillEntry; connection?: MCPConnection };

export function SessionCapabilityPicker({ value, onChange, harness, general, disabled }: {
  value: CapabilitySelection; onChange: (value: CapabilitySelection) => void; harness: string; general: boolean; disabled: boolean;
}) {
  const [inventory, setInventory] = useState<LibraryInventory>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const [category, setCategory] = useState<Category>('skills');
  const [query, setQuery] = useState('');
  const [remoteQuery, setRemoteQuery] = useState('');
  useEffect(() => { const timer = setTimeout(() => setRemoteQuery(query), 200); return () => clearTimeout(timer); }, [query]);
  const [selectedOnly, setSelectedOnly] = useState(false);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [showDetail, setShowDetail] = useState(false);
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setError('');
    getLibrary(controller.signal,remoteQuery).then(result => { if (!controller.signal.aborted) setInventory(result); }, reason => {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Could not load Library.');
    }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [attempt,remoteQuery]);

  const skills: PickerEntry[] = inventory ? [
    ...inventory.skills.map(skill => ({ id: skill.id, name: skill.name || skill.id, description: skill.description, skill })),
    ...value.skills.filter(id => !inventory.skills.some(skill => skill.id === id)).map(id => ({ id, name: id, description: inventory.search || inventory.skillsHasMore || inventory.skillsWindowStart ? 'Saved selection outside this page.' : 'This selected skill is no longer in the Library.', missing: !(inventory.search || inventory.skillsHasMore || inventory.skillsWindowStart) })),
  ] : [];
  const connections: PickerEntry[] = inventory ? [
    ...inventory.connections.map(connection => ({ id: connection.id, name: connection.name, description: connection.catalog ? `${connection.catalog.tools.length} known ${connection.catalog.tools.length === 1 ? 'tool' : 'tools'}${connection.catalog.resources ? ' · Resources' : ''}` : 'No tool catalog', connection })),
    ...value.mcpConnections.filter(selected => !inventory.connections.some(connection => connection.id === selected.connectionId)).map(selected => ({ id: selected.connectionId, name: selected.connectionId, description: inventory.search ? 'Saved selection outside these results.' : 'This selected connection is no longer in the Library.', missing: !inventory.search })),
  ] : [];
  const entries = category === 'skills' ? skills : connections;
  const isSelected = (id: string) => category === 'skills' ? value.skills.includes(id) : value.mcpConnections.some(connection => connection.connectionId === id);
  const search = query.trim().toLowerCase();
  const visible = entries.filter(entry => (!selectedOnly || isSelected(entry.id)) && (!search || `${entry.name} ${entry.id} ${entry.description} ${entry.connection?.catalog?.tools.map(tool => `${tool.name} ${tool.description || ''}`).join(' ') || ''}`.toLowerCase().includes(search)));
  const inspected = entries.find(entry => entry.id === activeId) || visible[0];
  const unavailable = inventory && !inventory.harnesses[harness]?.managed;

  function changeCategory(next: Category) { setCategory(next); setQuery(''); setActiveId(null); setShowDetail(false); }
  function toggle(entry: PickerEntry, checked: boolean) {
    if (disabled) return;
    if (category === 'skills') onChange({ ...value, skills: checked ? [...value.skills, entry.id] : value.skills.filter(id => id !== entry.id) });
    else onChange({ ...value, mcpConnections: checked ? [...value.mcpConnections, { connectionId: entry.id, tools: [] }] : value.mcpConnections.filter(connection => connection.connectionId !== entry.id) });
  }
  function inspect(id: string) {
    setActiveId(id); setShowDetail(true);
    requestAnimationFrame(() => { root.current?.querySelector<HTMLElement>('.session-picker-detail h3')?.focus(); root.current?.querySelector('.session-picker-detail')?.scrollTo(0, 0); });
  }
  function backToList() {
    setShowDetail(false);
    requestAnimationFrame(() => {
      const button = [...(root.current?.querySelectorAll<HTMLButtonElement>('[data-picker-entry]') || [])].find(element => element.dataset.pickerEntry === inspected?.id);
      (button || root.current?.querySelector<HTMLInputElement>('input[type="search"]'))?.focus();
    });
  }
  function clearFilters() { setQuery(''); setSelectedOnly(false); setActiveId(null); }
  const canSelect = (entry: PickerEntry) => isSelected(entry.id) || !entry.missing && (category !== 'skills' || entry.skill?.valid);

  return <div ref={root} className={`session-capability-picker${showDetail && inspected ? ' is-inspecting' : ''}`}>
    <div className="session-picker-navigation">
      <nav className="session-picker-categories" aria-label="Library categories">
        <button type="button" aria-current={category === 'skills' ? 'page' : undefined} onClick={() => changeCategory('skills')}><BookOpen aria-hidden="true" />Skills<span>{inventory ? skills.length : '…'}</span></button>
        <button type="button" aria-current={category === 'connections' ? 'page' : undefined} onClick={() => changeCategory('connections')}><Plug aria-hidden="true" />MCP connections<span>{inventory ? connections.length : '…'}</span></button>
      </nav>
      <IconButton label="Refresh Library" disabled={loading || disabled} onClick={() => setAttempt(n => n + 1)}><RefreshCw /></IconButton>
    </div>
    {unavailable && <div className="session-picker-warning" role="status"><AlertCircle aria-hidden="true" /><p>External Library selections are unavailable for this harness. Native inventory is unknown.</p></div>}
    {error && <div className="session-picker-warning" role="alert"><AlertCircle aria-hidden="true" /><p>{error}{inventory ? ' Showing the last loaded catalog.' : ''}</p><Button size="sm" disabled={loading} onClick={() => setAttempt(n => n + 1)}>Retry</Button></div>}
    <div className="session-picker-workspace">
      <div className="session-picker-browser">
        <div className="session-picker-filters">
          <div className="session-picker-search"><Search aria-hidden="true" /><Input type="search" aria-label={`Search ${category === 'skills' ? 'skills' : 'MCP connections'}`} placeholder={category === 'skills' ? 'Search skills' : 'Search connections or tools'} value={query} onChange={event => { setQuery(event.target.value); setActiveId(null); setShowDetail(false); }} /></div>
          <div className="session-picker-filter-row"><span aria-live="polite">{loading ? 'Loading…' : `${visible.length} ${visible.length === 1 ? 'item' : 'items'}`}</span><Button variant="ghost" size="sm" aria-pressed={selectedOnly} onClick={() => { setSelectedOnly(!selectedOnly); setActiveId(null); }}><Check aria-hidden="true" />Selected only</Button></div>
        </div>
        <div className="session-picker-list" aria-label={category === 'skills' ? 'Skills' : 'MCP connections'}>
          {loading && !inventory ? <div className="session-picker-loading" role="status">Loading Library…</div> : !inventory ? <div className="session-picker-empty"><AlertCircle aria-hidden="true" /><h3>Library unavailable</h3><p>Retry to load your skills and connections.</p></div> : !visible.length ? <div className="session-picker-empty">{search || selectedOnly ? <Search aria-hidden="true" /> : category === 'skills' ? <BookOpen aria-hidden="true" /> : <Plug aria-hidden="true" />}<h3>{search ? 'No matches' : selectedOnly ? 'Nothing selected here' : category === 'skills' ? 'No skills installed' : 'No MCP connections'}</h3><p>{search ? 'Try another name or description.' : selectedOnly ? 'Show all items to make a selection.' : 'Manage your Library in Settings.'}</p>{(search || selectedOnly) && <Button size="sm" onClick={clearFilters}>Show all</Button>}</div> : visible.map(entry => {
            const checked = isSelected(entry.id);
            const selectedConnection = value.mcpConnections.find(connection => connection.connectionId === entry.id);
            const issue = entry.missing ? 'Missing' : entry.skill && !entry.skill.valid ? 'Invalid skill' : entry.connection?.lastTestError ? 'Connection warning' : '';
            return <div key={entry.id} className={`session-picker-row${inspected?.id === entry.id ? ' is-inspected' : ''}`}>
              <label className="session-picker-checkbox"><input type="checkbox" aria-label={`Select ${category === 'skills' ? 'skill' : 'connection'} ${entry.name}`} checked={checked} disabled={disabled || !canSelect(entry)} onChange={event => toggle(entry, event.target.checked)} /></label>
              <button type="button" data-picker-entry={entry.id} aria-label={`View ${entry.name} details`} aria-current={inspected?.id === entry.id ? 'true' : undefined} className="session-picker-row-open" onClick={() => inspect(entry.id)}>
                <span><strong>{entry.name}</strong><small>{issue || (selectedConnection ? `${selectedConnection.tools.length} tools selected` : entry.description || entry.id)}</small></span>{issue && <AlertCircle className="session-picker-issue-icon" aria-label={issue} />}<ChevronRight aria-hidden="true" />
              </button>
            </div>;
          })}
        </div>
        {inventory && category === 'skills' && <LibrarySkillsPaging inventory={inventory} onPage={setInventory} />}
        {general && category === 'skills' && <div className="session-picker-system"><LockKeyhole aria-hidden="true" /><span><strong>skill-library-manager</strong><small>System managed · Always included</small></span></div>}
      </div>
      <section className="session-picker-detail" aria-label="Item details">
        {inspected ? <>
          <Button variant="ghost" size="sm" className="session-picker-back" onClick={backToList}><ArrowLeft aria-hidden="true" />All {category === 'skills' ? 'skills' : 'connections'}</Button>
          <div className="session-picker-detail-heading"><span className="session-picker-detail-icon" aria-hidden="true">{category === 'skills' ? <BookOpen /> : <Plug />}</span><h3 tabIndex={-1}>{inspected.name}</h3></div>
          {category === 'skills' ? <>
            <label className="session-picker-selection"><input type="checkbox" checked={isSelected(inspected.id)} disabled={disabled || !canSelect(inspected)} onChange={event => toggle(inspected, event.target.checked)} /><span>Include in this session</span></label>
            {inspected.missing ? <div className="session-picker-warning"><AlertCircle aria-hidden="true" /><p>This skill is missing from the Library. Remove it from the selection or restore it in Library settings.</p></div> : inspected.skill && !inspected.skill.valid && <div className="session-picker-warning"><AlertCircle aria-hidden="true" /><p>{inspected.skill.error || 'This skill is invalid. Fix it in Library settings before selecting it.'}</p></div>}
            <div className="session-picker-description"><h4>Description</h4><p>{inspected.description || 'No description provided.'}</p></div>
            {inspected.name !== inspected.id && <dl className="session-picker-metadata"><dt>Skill ID</dt><dd>{inspected.id}</dd></dl>}
          </> : <ConnectionTools key={inspected.id} entry={inspected} selected={value.mcpConnections.find(connection => connection.connectionId === inspected.id)} disabled={disabled} onToggle={checked => toggle(inspected, checked)} onChange={tools => onChange({ ...value, mcpConnections: value.mcpConnections.map(connection => connection.connectionId === inspected.id ? { ...connection, tools } : connection) })} />}
        </> : <div className="session-picker-empty session-picker-detail-empty"><span aria-hidden="true">{category === 'skills' ? <BookOpen /> : <Plug />}</span><h3>{category === 'skills' ? 'Skill details' : 'Connection details'}</h3><p>Select an item to inspect {category === 'skills' ? 'its description.' : 'its tools and resources.'}</p></div>}
      </section>
    </div>
  </div>;
}

function ConnectionTools({ entry, selected, disabled, onToggle, onChange }: {
  entry: PickerEntry; selected?: MCPSelection; disabled: boolean; onToggle: (checked: boolean) => void; onChange: (tools: string[]) => void;
}) {
  const [query, setQuery] = useState('');
  const connection = entry.connection;
  const knownTools = connection?.catalog?.tools || [];
  const missing = selected?.tools.filter(name => !knownTools.some(tool => tool.name === name)) || [];
  const tools = [...knownTools.map(tool => ({ ...tool, missing: false })), ...missing.map(name => ({ name, description: 'Not in the current catalog. Your saved selection is retained.', missing: true }))];
  const search = query.trim().toLowerCase();
  const visible = tools.filter(tool => `${tool.name} ${tool.description || ''}`.toLowerCase().includes(search));
  const allKnownSelected = knownTools.length > 0 && knownTools.every(tool => selected?.tools.includes(tool.name));
  return <>
    <label className="session-picker-selection"><input type="checkbox" checked={!!selected} disabled={disabled || !!entry.missing && !selected} onChange={event => onToggle(event.target.checked)} /><span>Include in this session</span></label>
    {entry.missing && <div className="session-picker-warning"><AlertCircle aria-hidden="true" /><p>This connection is missing from the Library. Remove it from the selection or restore it in Library settings.</p></div>}
    {connection?.lastTestError && <div className="session-picker-warning"><AlertCircle aria-hidden="true" /><p>{connection.lastTestError}</p></div>}
    {connection?.catalog?.resources && <div className="session-picker-resource"><BookOpen aria-hidden="true" /><div><strong>Resources {selected ? 'included' : 'available'}</strong><p>Resource discovery and reading are included when this connection is selected.</p></div></div>}
    <div className="session-picker-tools-heading"><h4>Tools</h4><span>{selected?.tools.length || 0} selected · {knownTools.length} known</span></div>
    {!selected && !entry.missing && <p className="session-picker-hint">Include this connection to choose its tools.</p>}
    {!connection?.catalog && !entry.missing && <p className="session-picker-hint">No tool catalog yet. Test this connection in Library settings, then refresh here. Existing selections are retained.</p>}
    {tools.length > 0 && <>
      <div className="session-picker-search"><Search aria-hidden="true" /><Input type="search" aria-label="Search tools" placeholder="Search tools" value={query} onChange={event => setQuery(event.target.value)} /></div>
      <div className="session-picker-tool-actions"><Button size="sm" disabled={disabled || !selected || !knownTools.length || allKnownSelected} onClick={() => onChange([...new Set([...knownTools.map(tool => tool.name), ...missing])])}>Select all known tools</Button><Button variant="ghost" size="sm" disabled={disabled || !selected?.tools.length} onClick={() => onChange([])}>Clear tools</Button></div>
      <div className="session-picker-tools">{visible.map(tool => <div className="session-picker-tool" key={tool.name}>
        <label><input type="checkbox" checked={selected?.tools.includes(tool.name) || false} disabled={disabled || !selected} onChange={event => onChange(event.target.checked ? [...(selected?.tools || []), tool.name] : (selected?.tools || []).filter(name => name !== tool.name))} /><span><strong>{tool.name}</strong>{tool.missing && <small>Unavailable</small>}</span></label>
        {tool.description && <div className="session-picker-tool-description"><p className={tool.description.length > 180 ? 'is-truncated' : undefined}>{tool.description}</p>{tool.description.length > 180 && <details><summary>Full description</summary><p>{tool.description}</p></details>}</div>}
      </div>)}</div>
      {!visible.length && <div className="session-picker-empty"><h3>No matching tools</h3><Button size="sm" onClick={() => setQuery('')}>Clear search</Button></div>}
    </>}
    {!!connection?.catalog && !tools.length && <p className="session-picker-hint">No tools in this catalog.</p>}
    {selected && selected.tools.length === 0 && knownTools.length > 0 && <p className="session-picker-hint">No tools selected. This connection’s tools will not be available to the session.</p>}
  </>;
}
