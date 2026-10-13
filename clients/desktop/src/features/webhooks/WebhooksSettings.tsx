import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { ArrowLeft, Copy, Github, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { ListTile } from '../../design-system/ListTile';
import { Menu, MenuItem } from '../../design-system/Menu';
import { Select } from '../../design-system/Select';
import { Toggle } from '../../design-system/Toggle';
import { EngineRequestError } from '../../engine';
import { createWebhook, listWebhooks, removeWebhook, updateWebhook, validateWebhookSecret, webhookRetentions, webhookURL, type WebhookConnection, type WebhookRetention } from './api';
import './webhooks.css';

type WebhookFormInput = { name: string; retention: WebhookRetention; secret?: string; enabled?: boolean };

function retentionLabel(retention: WebhookRetention) {
  return webhookRetentions.find(option => option.value === retention)?.label ?? retention;
}

function failureMessage(error: unknown) {
  return error instanceof Error ? error.message : 'The webhook request failed.';
}

function Timestamp({ value }: { value?: string }) {
  return value ? <time dateTime={value} title={value}>{new Date(value).toLocaleString()}</time> : <span>Never</span>;
}

function timeSinceReceived(value: string | undefined, now: number) {
  if (!value || !Number.isFinite(Date.parse(value))) return 'Never';
  const minutes = Math.floor(Math.max(0, now - Date.parse(value)) / 60000);
  if (minutes < 1) return 'Just now';
  if (minutes < 60) return `${minutes}min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return days < 365 ? `${days}d ago` : `${Math.floor(days / 365)}y ago`;
}

function generateWebhookSecret() {
  return Array.from(crypto.getRandomValues(new Uint8Array(32)), byte => byte.toString(16).padStart(2, '0')).join('');
}

function WebhookURL({ connection, readMode = false }: { connection?: WebhookConnection; readMode?: boolean }) {
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState('');
  const active = useRef(false);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);
  const url = webhookURL(connection ?? { endpointPath: '/api/webhooks/github/{webhookId}' });
  return <>
    <div className="webhook-url-field">
      {readMode ? <span className="webhook-url-text">{url}</span> : <Input id="webhook-url" className="webhook-form-url" readOnly value={url} aria-describedby="webhook-url-hint" onFocus={event => { if (connection) event.target.select(); }} />}
      <IconButton label={copied ? 'URL copied' : 'Copy URL'} disabled={!connection} onClick={async () => {
        try { await navigator.clipboard.writeText(url); if (active.current) { setCopied(true); setError(''); } }
        catch { if (active.current) setError('Could not copy the URL.'); }
      }}><Copy /></IconButton>
    </div>
    {copied && <span className="webhook-field-hint" role="status">URL copied</span>}
    {error && <span className="form-error" role="alert">{error}</span>}
  </>;
}

function WebhookForm({ connection, busy, onSave, onCancel }: {
  connection?: WebhookConnection; busy: boolean;
  onSave: (input: WebhookFormInput) => void; onCancel: () => void;
}) {
  const [name, setName] = useState(connection?.name ?? '');
  const [retention, setRetention] = useState<WebhookRetention | ''>(connection?.retention ?? '');
  const [enabled, setEnabled] = useState(connection?.enabled ?? true);
  const [changeSecret, setChangeSecret] = useState(!connection);
  const [secret, setSecret] = useState(() => connection ? '' : generateWebhookSecret());
  const [error, setError] = useState('');
  const [copied, setCopied] = useState(false);
  const active = useRef(false);
  const secretVersion = useRef(0);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;
    if (!name.trim()) { setError('Enter a webhook name.'); return; }
    if (!retention) { setError('Choose event retention.'); return; }
    if (changeSecret) {
      const failure = validateWebhookSecret(secret);
      if (failure) { setError(failure); return; }
    }
    setError('');
    onSave({ name: name.trim(), retention, ...(connection ? { enabled } : {}), ...(changeSecret ? { secret } : {}) });
  }

  return <form className="settings-form webhook-form" onSubmit={submit} aria-busy={busy}>
    <h4>{connection ? 'Edit GitHub webhook' : 'Add GitHub webhook'}</h4>
    <div className="settings-field"><label htmlFor="webhook-name">Name</label><Input id="webhook-name" autoFocus required maxLength={60} value={name} disabled={busy} onChange={event => setName(event.target.value)} /></div>
    <div className="settings-field"><label htmlFor="webhook-url">Webhook URL</label>
      <WebhookURL connection={connection} />
      <span id="webhook-url-hint" className="webhook-field-hint">{connection ? "Use this URL as the Payload URL in GitHub." : "The webhook id is generated after creation"}</span>
    </div>
    <div className="settings-field"><label htmlFor="webhook-retention">Event retention</label>
      <Select id="webhook-retention" label="Event retention" variant="field" required value={retention} disabled={busy} onChange={event => setRetention(event.target.value as WebhookRetention | '')}>
        <option value="" disabled>Choose retention</option>{webhookRetentions.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </Select>
    </div>
    {connection && <div className="settings-field"><Toggle label="Enabled" checked={enabled} disabled={busy} onCheckedChange={setEnabled} /></div>}
    {connection && <div className="settings-field"><Toggle label="Change secret" checked={changeSecret} disabled={busy} onCheckedChange={checked => { secretVersion.current += 1; setChangeSecret(checked); setSecret(checked ? generateWebhookSecret() : ''); setCopied(false); setError(''); }} /></div>}
    {changeSecret && <div className="settings-field"><label htmlFor="webhook-secret">Secret</label>
      <div className="webhook-secret-field"><div className="webhook-secret-input">
        <Input id="webhook-secret" type="text" required maxLength={512} value={secret} disabled={busy} autoComplete="off" autoCapitalize="none" autoCorrect="off" spellCheck={false} aria-describedby="webhook-secret-hint" onChange={event => { secretVersion.current += 1; setSecret(event.target.value); setCopied(false); }} />
        <IconButton className="webhook-secret-regenerate" label="Generate new secret" disabled={busy} onClick={() => { secretVersion.current += 1; setSecret(generateWebhookSecret()); setCopied(false); setError(''); }}><RefreshCw /></IconButton>
      </div>
        <Button size="sm" disabled={busy || !secret} onClick={async () => {
          const value = secret;
          const version = secretVersion.current;
          try { await navigator.clipboard.writeText(value); if (active.current && version === secretVersion.current) { setCopied(true); setError(''); } }
          catch { if (active.current && version === secretVersion.current) setError('Could not copy the secret.'); }
        }}><Copy />{copied ? 'Copied' : 'Copy secret'}</Button>
      </div><span id="webhook-secret-hint" className="webhook-field-hint">16–512 UTF-8 bytes, without control characters.</span>
    </div>}
    {error && <p role="alert" className="form-error">{error}</p>}
    <div className="dialog-actions"><Button disabled={busy} onClick={onCancel}>Cancel</Button><Button type="submit" variant="primary" disabled={busy || !name.trim() || !retention || changeSecret && !secret}>{busy ? 'Saving...' : connection ? 'Save changes' : 'Add webhook'}</Button></div>
  </form>;
}

export function WebhooksSettings({ projectId, onBusyChange }: { projectId: string; onBusyChange: (busy: boolean) => void }) {
  const [items, setItems] = useState<WebhookConnection[]>([]);
  const [now, setNow] = useState(Date.now);
  const [nextCursor, setNextCursor] = useState<string>();
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState('');
  const [mutationError, setMutationError] = useState('');
  const [busy, setBusy] = useState(false);
  const [editor, setEditor] = useState<WebhookConnection | 'new' | null>(null);
  const [selected, setSelected] = useState<WebhookConnection | null>(null);
  const [providerMenuOpen, setProviderMenuOpen] = useState(false);
  const [removingId, setRemovingId] = useState('');
  const active = useRef(false);
  const listing = useRef<AbortController | null>(null);
  const mutation = useRef<AbortController | null>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const section = useRef<HTMLElement>(null);
  const loadedLimit = useRef(50);
  loadedLimit.current = Math.max(50, Math.min(100, items.length));

  const load = useCallback(async (cursor?: string, quiet = false) => {
    if (quiet && (listing.current || mutation.current)) return;
    listing.current?.abort();
    const controller = new AbortController(); listing.current = controller;
    if (!quiet) setLoading(true);
    setListError('');
    try {
      const page = await listWebhooks(projectId, { limit: quiet ? loadedLimit.current : 50, ...(cursor ? { cursor } : {}) }, controller.signal);
      if (!active.current || controller.signal.aborted || listing.current !== controller) return;
      setItems(current => cursor ? [...new Map([...current, ...page.items].map(item => [item.id, item])).values()] : page.items);
      setNextCursor(page.nextCursor); setHasMore(page.hasMore);
    } catch (error) {
      if (active.current && !controller.signal.aborted && listing.current === controller) setListError(failureMessage(error));
    } finally {
      if (active.current && listing.current === controller) { listing.current = null; setLoading(false); }
    }
  }, [projectId]);

  useEffect(() => {
    active.current = true; void load();
    return () => { active.current = false; listing.current?.abort(); mutation.current?.abort(); };
  }, [load]);

  useEffect(() => {
    if (editor || selected || busy) return;
    const refresh = () => {
      if (document.visibilityState !== 'visible') return;
      setNow(Date.now());
      void load(undefined, true);
    };
    refresh();
    const interval = window.setInterval(refresh, 30000);
    document.addEventListener('visibilitychange', refresh);
    return () => { window.clearInterval(interval); document.removeEventListener('visibilitychange', refresh); };
  }, [load, editor, selected, busy]);

  function restoreFocus() {
    requestAnimationFrame(() => {
      if (!active.current) return;
      const previous = returnFocus.current;
      const target = previous?.isConnected && previous.getClientRects().length && !previous.matches(':disabled') ? previous : section.current?.querySelector<HTMLElement>('[data-webhook-edit]') ?? section.current?.querySelector<HTMLElement>('[data-webhook-add]');
      if (target?.getClientRects().length && !target.matches(':disabled')) target.focus();
    });
  }

  async function mutate<T>(action: (signal: AbortSignal) => Promise<T>, afterSuccess: (result: T) => void) {
    if (mutation.current) return;
    const controller = new AbortController(); mutation.current = controller;
    listing.current?.abort(); listing.current = null; setLoading(false);
    setBusy(true); onBusyChange(true); setMutationError('');
    try {
      const result = await action(AbortSignal.any([controller.signal, AbortSignal.timeout(15000)]));
      if (!active.current || controller.signal.aborted || mutation.current !== controller) return;
      afterSuccess(result);
      await load();
    } catch (error) {
      if (!active.current || controller.signal.aborted || mutation.current !== controller) return;
      if (error instanceof EngineRequestError) {
        setMutationError(failureMessage(error));
        setRemovingId('');
      } else {
        // A transport failure may follow a committed mutation. Refresh before
        // offering another creation; never retry POST automatically.
        setEditor(null); setSelected(null); setRemovingId(''); restoreFocus();
        setMutationError(`${failureMessage(error)} Delivery may be unconfirmed. Check the refreshed list before trying again.`);
      }
      await load();
    } finally {
      if (mutation.current === controller) {
        mutation.current = null;
        if (active.current) { setBusy(false); onBusyChange(false); restoreFocus(); }
      }
    }
  }

  function save(input: WebhookFormInput) {
    const target = editor;
    if (!target) return;
    if (target === 'new') {
      if (input.secret === undefined) return;
      const secret = input.secret;
      void mutate(signal => createWebhook(projectId, { provider: 'github', name: input.name, retention: input.retention, secret }, signal), connection => { setSelected(connection); setEditor(null); });
    } else {
      void mutate(signal => updateWebhook(projectId, target.id, input, signal), connection => { setSelected(connection); setEditor(null); });
    }
  }

  return <section ref={section} className="webhooks-settings" aria-labelledby="project-webhooks-title">
    <div className="settings-panel-title" hidden={selected !== null}><h3 id="project-webhooks-title">Webhooks</h3>
      {editor === null && selected === null && <Menu label="Webhook providers" role="menu" className="webhook-provider-menu" open={providerMenuOpen} onOpenChange={setProviderMenuOpen} side="bottom" align="start" trigger={props => <Button {...props} data-webhook-add size="sm" variant="primary" disabled={busy || !!removingId}><Plus />Add</Button>}>
        <MenuItem role="menuitem" onClick={() => { returnFocus.current = section.current?.querySelector<HTMLElement>('[data-webhook-add]') ?? null; setProviderMenuOpen(false); setMutationError(''); setEditor('new'); }}><Github aria-hidden="true" />GitHub</MenuItem>
      </Menu>}
    </div>
    {mutationError && <p role="alert" className="form-error">{mutationError}</p>}
    <div hidden={editor !== null || selected !== null}>
      {listError && <p role="alert" className="form-error">{listError}</p>}
      {loading && <p role="status" className="webhook-loading">Loading webhooks...</p>}
      {!loading && !listError && items.length === 0 && <p className="webhook-empty">No webhooks</p>}
      <div className="webhooks-list" aria-busy={loading || busy}>
        {items.map(connection => <ListTile key={connection.id} className="webhook-list-tile" title={connection.name} description={retentionLabel(connection.retention)} metadata={connection.lastReceivedAt ? <time className="webhook-last-received" dateTime={connection.lastReceivedAt} title={`Last received: ${new Date(connection.lastReceivedAt).toLocaleString()}`}>{timeSinceReceived(connection.lastReceivedAt, now)}</time> : <span className="webhook-last-received" title="No events received">Never</span>} actions={<Toggle label={connection.enabled ? 'Enabled' : 'Disabled'} aria-label={`Enable ${connection.name}`} checked={connection.enabled} disabled={busy || loading} onCheckedChange={enabled => void mutate(signal => updateWebhook(projectId, connection.id, { enabled }, signal), updated => setItems(current => current.map(item => item.id === updated.id ? updated : item)))} />} onClick={() => {
          if (busy) return;
          returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
          setMutationError(''); setSelected(connection);
        }} />)}
      </div>
      {hasMore && <div className="webhook-pagination"><Button size="sm" disabled={loading || busy || !nextCursor || !!removingId} onClick={() => void load(nextCursor)}>Load more</Button></div>}
    </div>
    {selected && editor === null && <div className="webhook-detail">
      <div className="webhook-detail-heading"><Button size="sm" variant="ghost" disabled={busy} onClick={() => { setSelected(null); setRemovingId(''); setMutationError(''); restoreFocus(); }}><ArrowLeft />Back</Button>
        <Button data-webhook-edit size="sm" disabled={busy || !!removingId} onClick={() => { setMutationError(''); setEditor(selected); }}><Pencil />Edit</Button></div>
      <dl className="webhook-details">
        <div><dt>Name</dt><dd>{selected.name}</dd></div>
        <div><dt>Webhook URL</dt><dd><WebhookURL key={selected.id} connection={selected} readMode /></dd></div>
        <div><dt>Event retention</dt><dd>{retentionLabel(selected.retention)}</dd></div>
        <div><dt>Status</dt><dd>{selected.enabled ? 'Enabled' : 'Disabled'}</dd></div>
        <div><dt>Secret</dt><dd>{selected.secretConfigured ? 'Configured' : 'Not configured'}</dd></div>
        <div><dt>Last received</dt><dd><Timestamp value={selected.lastReceivedAt} /></dd></div>
        <div><dt>{selected.lastPingAt ? 'Verified' : 'Not verified'}</dt><dd>{selected.lastPingAt ? <Timestamp value={selected.lastPingAt} /> : '—'}</dd></div>
      </dl>
      {!removingId ? <div className="webhook-detail-actions"><Button size="sm" variant="danger-soft" disabled={busy} onClick={() => setRemovingId(selected.id)}><Trash2 />Remove webhook</Button></div> : <div className="webhook-remove-confirmation" role="group" aria-labelledby="webhook-remove-title" aria-describedby="webhook-remove-description">
        <h4 id="webhook-remove-title">Remove webhook?</h4><p id="webhook-remove-description">Removing {selected.name} deletes its stored events.</p>
        <div className="dialog-actions"><Button autoFocus size="sm" disabled={busy} onClick={() => setRemovingId('')}>Cancel</Button>
          <Button size="sm" variant="danger-soft" disabled={busy} onClick={() => void mutate(signal => removeWebhook(projectId, selected.id, signal), () => { setRemovingId(''); setSelected(null); restoreFocus(); })}>{busy ? 'Removing...' : 'Remove webhook'}</Button></div>
      </div>}
    </div>}
    {editor !== null && <WebhookForm key={editor === 'new' ? 'new' : editor.id} connection={editor === 'new' ? undefined : editor} busy={busy} onSave={save} onCancel={() => { setEditor(null); setMutationError(''); if (!selected) restoreFocus(); else requestAnimationFrame(() => { if (active.current) section.current?.querySelector<HTMLElement>('[data-webhook-edit]')?.focus(); }); }} />}
  </section>;
}
