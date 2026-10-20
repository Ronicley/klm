import { useEffect, useState } from 'react';
import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Toggle } from '../../design-system/Toggle';
import { getLibrary, selectionOf, type CapabilitySelection, type LibraryInventory } from './api';
import './library.css';

export function CapabilitySelector({ value, onChange, harness, general = false, disabled = false }: {
  value: CapabilitySelection; onChange: (selection: CapabilitySelection) => void; harness?: string; general?: boolean; disabled?: boolean;
}) {
  const [inventory, setInventory] = useState<LibraryInventory>();
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const [search, setSearch] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    getLibrary(controller.signal).then(setInventory, reason => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Could not load Library.'); });
    return () => controller.abort();
  }, [attempt]);
  const selection = selectionOf(value);
  const unavailable = !!harness && inventory && !inventory.harnesses[harness]?.managed;
  const matches = (text: string) => text.toLowerCase().includes(search.trim().toLowerCase());
  return <div className="capability-selector">
    <div className="library-toolbar"><Input aria-label="Search Library" placeholder="Search skills and connections" value={search} onChange={event => setSearch(event.target.value)} /><Button size="sm" onClick={() => { setError(''); setAttempt(n => n + 1); }}>Refresh</Button></div>
    {unavailable && <p className="form-error" role="status">External Library selections are unavailable for this harness. Native inventory is unknown.</p>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {!inventory && !error && <p className="muted">Loading Library...</p>}
    {inventory && <>
      <h4>Skills</h4>
      {general && <p className="library-system-skill">skill-library-manager · System managed</p>}
      {inventory.skills.filter(skill => matches(`${skill.id} ${skill.description}`)).map(skill => <div className="library-selection-row" key={skill.id}>
        <Toggle label={skill.name || skill.id} checked={selection.skills.includes(skill.id)} disabled={disabled || (!skill.valid && !selection.skills.includes(skill.id))} onCheckedChange={checked => onChange({ ...selection, skills: checked ? [...selection.skills, skill.id] : selection.skills.filter(id => id !== skill.id) })} />
        <p className={skill.valid ? 'muted' : 'form-error'}>{skill.error || skill.description}</p>
      </div>)}
      {selection.skills.filter(id => !inventory.skills.some(s => s.id === id)).map(id => <div className="library-selection-row" key={id}><Toggle label={`${id} · Missing`} checked disabled={disabled} onCheckedChange={() => onChange({ ...selection, skills: selection.skills.filter(s => s !== id) })} /></div>)}
      {!inventory.skills.length && <p className="muted">No user skills installed.</p>}
      <h4>MCP connections</h4>
      {inventory.connections.filter(c => matches(`${c.name} ${c.catalog?.tools.map(t => t.name).join(' ') ?? ''}`)).map(connection => {
        const selected = selection.mcpConnections.find(c => c.connectionId === connection.id);
        const changeTools = (tools: string[]) => onChange({ ...selection, mcpConnections: selection.mcpConnections.map(c => c.connectionId === connection.id ? { ...c, tools } : c) });
        const tools = connection.catalog?.tools ?? [];
        const missing = selected?.tools.filter(name => !tools.some(t => t.name === name)) ?? [];
        return <div className="library-selection-row" key={connection.id}>
          <Toggle label={connection.name} checked={!!selected} disabled={disabled} onCheckedChange={checked => onChange({ ...selection, mcpConnections: checked ? [...selection.mcpConnections, { connectionId: connection.id, tools: [] }] : selection.mcpConnections.filter(c => c.connectionId !== connection.id) })} />
          {connection.lastTestError && <p className="form-error">{connection.lastTestError}</p>}
          {selected && <details open><summary>{selected.tools.length} tools selected{connection.catalog?.resources ? ' · Resources enabled' : ''}</summary>
            <div className="library-tool-actions"><Button size="sm" disabled={disabled || !tools.length} onClick={() => changeTools([...new Set([...tools.map(t => t.name), ...missing])])}>Select known tools</Button><Button size="sm" disabled={disabled} onClick={() => changeTools([])}>Clear tools</Button></div>
            {!connection.catalog && <p className="muted">Test this connection in Library to discover tools. Existing selections are retained.</p>}
            {[...tools, ...missing.map(name => ({ name, description: 'Not in the last known catalog; selection retained.' }))].map(tool => <div className="library-tool" key={tool.name}><Toggle label={tool.name} checked={selected.tools.includes(tool.name)} disabled={disabled} onCheckedChange={checked => changeTools(checked ? [...selected.tools, tool.name] : selected.tools.filter(name => name !== tool.name))} />{tool.description && <p className="muted">{tool.description}</p>}</div>)}
          </details>}
        </div>;
      })}
      {selection.mcpConnections.filter(c => !inventory.connections.some(i => i.id === c.connectionId)).map(c => <Toggle key={c.connectionId} label={`${c.connectionId} · Missing`} checked disabled={disabled} onCheckedChange={() => onChange({ ...selection, mcpConnections: selection.mcpConnections.filter(s => s.connectionId !== c.connectionId) })} />)}
      {!inventory.connections.length && <p className="muted">No MCP connections.</p>}
    </>}
  </div>;
}
