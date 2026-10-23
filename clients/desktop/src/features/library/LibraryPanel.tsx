import { useEffect, useState } from 'react';
import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Textarea } from '../../design-system/Textarea';
import { Select } from '../../design-system/Select';
import { Toggle } from '../../design-system/Toggle';
import { ENGINE_URL, request } from '../../engine';
import { IS_DESKTOP, IS_MOBILE_HOST } from '../../platform';
import { getLibrary, libraryPath, type DeletionPreview, type LibraryInventory, type MCPConfig, type MCPConnection } from './api';
import './library.css';

const failure = (error: unknown) => error instanceof Error ? error.message : 'Library request failed.';

export function LibraryPanel({ onGeneralAgent }: { onGeneralAgent?: () => void }) {
  const [inventory, setInventory] = useState<LibraryInventory>();
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const [editing, setEditing] = useState<MCPConnection | 'new'>();
  const [busy, setBusy] = useState(false);
  const [deletion, setDeletion] = useState<{ kind: string; id: string; name: string; preview: DeletionPreview }>();
  const [authorization, setAuthorization] = useState<{ name: string; url: string }>();
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    getLibrary(controller.signal).then(setInventory, reason => { if (!controller.signal.aborted) setError(failure(reason)); });
    return () => controller.abort();
  }, [attempt]);
  function refresh() { setError(''); setAttempt(n => n + 1); }
  async function action(fn: () => Promise<unknown>) {
    setBusy(true); setError('');
    try { await fn(); refresh(); } catch (reason) { setError(failure(reason)); } finally { setBusy(false); }
  }
  async function preview(kind: string, id: string, name: string) {
    await action(async () => setDeletion({ kind, id, name, preview: await request<DeletionPreview>(`/api/library/${kind}/${encodeURIComponent(id)}/deletion`) }));
  }
  if (editing) return <ConnectionEditor connection={editing === 'new' ? undefined : editing} onClose={() => setEditing(undefined)} onSaved={() => { setEditing(undefined); refresh(); }} />;
  return <section className="library-panel" aria-labelledby="library-title">
    <div className="settings-panel-title"><h3 id="library-title">Library</h3><Button size="sm" disabled={busy} onClick={refresh}>Refresh</Button></div>
    <Input aria-label="Search Library items" placeholder="Search Library" value={search} onChange={event => setSearch(event.target.value)} />
    {error && <p className="form-error" role="alert">{error}</p>}
    {inventory?.recoveryPending && <p className="form-error">A deletion needs recovery. <Button size="sm" onClick={() => void action(() => request('/api/library/recover', 'POST', {}))}>Retry recovery</Button></p>}
    <div className="library-heading"><h4>Skills</h4>{onGeneralAgent && <Button size="sm" onClick={onGeneralAgent}>Open KLM Agent</Button>}</div>
    {inventory && <p className="library-root">{inventory.skillsRoot}</p>}
    {inventory?.skills.filter(s => `${s.name} ${s.id} ${s.description}`.toLowerCase().includes(search.toLowerCase())).map(skill => <article className="library-item" key={skill.id}>
      <div><strong>{skill.name || skill.id}</strong><p className="muted">{skill.description}</p>{skill.error && <p className="form-error">{skill.error}</p>}{skill.revision && <small title={skill.revision}>{skill.source} · {skill.revision.slice(0, 12)}</small>}</div>
      <Button size="sm" disabled={busy} onClick={() => void preview('skills', skill.id, skill.name || skill.id)}>Usage / Delete</Button>
    </article>)}
    {inventory && !inventory.skills.length && <p className="muted">No user skills installed.</p>}
    <div className="library-heading"><h4>MCP connections</h4><Button size="sm" disabled={busy} onClick={() => setEditing('new')}>Add connection</Button></div>
    {inventory?.connections.filter(c => c.name.toLowerCase().includes(search.toLowerCase())).map(connection => <article className="library-item" key={connection.id}>
      <div><strong>{connection.name}</strong><p className="muted">{connection.config.transport === 'stdio' ? connection.config.command : connection.config.url}</p>
        <small>{connection.catalog ? `${connection.catalog.tools.length} known tools${connection.catalog.resources ? ' · Resources' : ''}` : 'Not discovered'} · {connection.lastTestAt ? `Last test ${new Date(connection.lastTestAt).toLocaleString()}` : 'Never tested'}</small>
        {connection.lastTestError && <p className="form-error">{connection.lastTestError}</p>}
        {connection.config.auth !== 'none' && <p className="muted">{connection.credentialsConfigured ? 'Credentials configured' : 'Authentication required'}</p>}
        <details><summary>Known tools</summary>{connection.catalog?.tools.map(tool => <p key={tool.name}><strong>{tool.name}</strong>{tool.description && <> · {tool.description}</>}</p>)}</details>
      </div>
      <div className="library-item-actions"><Button size="sm" disabled={busy} onClick={() => setEditing(connection)}>Edit</Button><Button size="sm" disabled={busy} onClick={() => void action(() => request(`${libraryPath(connection.id)}/test`, 'POST', {}))}>Test connection</Button>
        {connection.config.auth === 'oauth' && <Button size="sm" disabled={busy} onClick={() => void action(async () => { const result = await request<{ authorizationUrl: string }>(`${libraryPath(connection.id)}/oauth`, 'POST', IS_DESKTOP ? {} : { returnUrl: `${window.location.origin}${window.location.pathname}?klm-library=1` }); setAuthorization({ name: connection.name, url: result.authorizationUrl }); })}>Authenticate</Button>}
        {connection.credentialsConfigured && <Button size="sm" disabled={busy} onClick={() => void action(() => request(`${libraryPath(connection.id)}/disconnect`, 'POST', {}))}>Disconnect</Button>}
        <Button size="sm" disabled={busy} onClick={() => void preview('mcp', connection.id, connection.name)}>Usage / Delete</Button>
      </div>
    </article>)}
    {inventory && !inventory.connections.length && <p className="muted">No MCP connections.</p>}
    {authorization && <div className="library-confirmation"><strong>Authenticate {authorization.name}</strong><p><a href={authorization.url} target={IS_MOBILE_HOST ? '_self' : '_blank'} rel="noreferrer">Open authorization page</a></p><Button size="sm" onClick={() => { setAuthorization(undefined); refresh(); }}>Done / Refresh</Button></div>}
    {deletion && <div className="library-confirmation" role="group" aria-label="Confirm library deletion"><h4>Delete {deletion.name}?</h4><p>Remove these saved references. Captured runs and active turns keep their snapshots.</p>
      <ul>{deletion.preview.references.map(ref => <li key={`${ref.kind}/${ref.projectId}/${ref.id}`}>{ref.title} · {ref.kind === 'captured_run' ? 'Captured run (preserved)' : ref.kind}</li>)}</ul>
      {!deletion.preview.references.length && <p>No saved references.</p>}
      <div className="dialog-actions"><Button disabled={busy} onClick={() => setDeletion(undefined)}>Cancel</Button><Button variant="primary" disabled={busy} onClick={() => void action(async () => { await request(`/api/library/${deletion.kind}/${encodeURIComponent(deletion.id)}`, 'DELETE', { revision: deletion.preview.revision }); setDeletion(undefined); })}>Delete and remove references</Button></div>
    </div>}
  </section>;
}

function ConnectionEditor({ connection, onClose, onSaved }: { connection?: MCPConnection; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(connection?.name ?? '');
  const [config, setConfig] = useState<MCPConfig>(connection?.config ?? { transport: 'http', auth: 'none' });
  const [args, setArgs] = useState(JSON.stringify(connection?.config.args ?? [], null, 2));
  const [replaceSecrets, setReplaceSecrets] = useState(false);
  const [removeSecrets, setRemoveSecrets] = useState(false);
  const [environment, setEnvironment] = useState('{}');
  const [headers, setHeaders] = useState('{}');
  const [bearer, setBearer] = useState('');
  const [secret, setSecret] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const set = (patch: Partial<MCPConfig>) => setConfig(current => ({ ...current, ...patch }));
  return <form className="settings-form library-connection-form" onSubmit={async event => {
    event.preventDefault(); setSaving(true); setError('');
    try {
      const parsed: unknown = JSON.parse(args);
      if (!Array.isArray(parsed) || !parsed.every(arg => typeof arg === 'string')) throw new Error('Arguments must be a JSON array of strings.');
      await request(connection ? libraryPath(connection.id) : '/api/library/mcp', connection ? 'PATCH' : 'POST', {
        name, revision: connection?.revision ?? '', config: { ...config, args: parsed }, removeSecrets,
        ...(replaceSecrets ? { secrets: { environment: JSON.parse(environment), headers: JSON.parse(headers), bearer, clientSecret: secret } } : {}),
      });
      onSaved();
    } catch (reason) { setError(failure(reason)); } finally { setSaving(false); }
  }}>
    <h3>{connection ? 'Edit connection' : 'New MCP connection'}</h3>
    <label>Name<Input required value={name} maxLength={100} onChange={event => setName(event.target.value)} /></label>
    <label>Transport<Select label="Transport" variant="field" value={config.transport} onChange={event => set({ transport: event.target.value as MCPConfig['transport'], auth: 'none' })}><option value="http">Streamable HTTP</option><option value="stdio">Stdio</option></Select></label>
    {config.transport === 'stdio' ? <>
      <label>Executable<Input required value={config.command ?? ''} onChange={event => set({ command: event.target.value })} /></label>
      <label>Arguments (JSON array)<Textarea value={args} onChange={event => setArgs(event.target.value)} rows={3} /></label>
      <label>Working directory<Input value={config.cwd ?? ''} placeholder="Execution directory" onChange={event => set({ cwd: event.target.value })} /></label>
    </> : <label>Endpoint URL<Input required type="url" value={config.url ?? ''} onChange={event => set({ url: event.target.value })} /></label>}
    <label>Authentication<Select label="Authentication" variant="field" value={config.auth} onChange={event => set({ auth: event.target.value as MCPConfig['auth'] })}><option value="none">None</option><option value="manual">Manual credentials</option>{config.transport === 'http' && <option value="oauth">OAuth</option>}</Select></label>
    {config.auth === 'oauth' && <>
      <label>Client ID<Input value={config.clientId ?? ''} placeholder="Dynamic registration when empty" onChange={event => set({ clientId: event.target.value })} /></label>
      <label>Redirect URI<Input required type="url" value={config.redirectUri ?? ''} placeholder={`${ENGINE_URL}/api/library/mcp/oauth/callback`} onChange={event => set({ redirectUri: event.target.value })} /></label>
      <label>Scopes<Input value={(config.scopes ?? []).join(' ')} onChange={event => set({ scopes: event.target.value.split(/\s+/).filter(Boolean) })} /></label>
    </>}
    <Toggle label={connection?.credentialsConfigured ? 'Replace secrets' : 'Set secrets'} checked={replaceSecrets} onCheckedChange={checked => { setReplaceSecrets(checked); setRemoveSecrets(false); }} />
    {connection?.credentialsConfigured && <Toggle label="Remove existing secrets" checked={removeSecrets} onCheckedChange={checked => { setRemoveSecrets(checked); setReplaceSecrets(false); }} />}
    {replaceSecrets && <>
      {config.transport === 'stdio' ? <label>Secret environment (JSON object)<Textarea value={environment} onChange={event => setEnvironment(event.target.value)} rows={3} /></label> : <>
        <label>Secret headers (JSON object)<Textarea value={headers} onChange={event => setHeaders(event.target.value)} rows={3} /></label>
        <label>Bearer token<Input type="password" autoComplete="off" value={bearer} onChange={event => setBearer(event.target.value)} /></label>
      </>}
      {config.auth === 'oauth' && <label>Client secret<Input type="password" autoComplete="off" value={secret} onChange={event => setSecret(event.target.value)} /></label>}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="dialog-actions"><Button disabled={saving} onClick={onClose}>Cancel</Button><Button type="submit" variant="primary" disabled={saving}>{saving ? 'Saving...' : 'Save connection'}</Button></div>
  </form>;
}
