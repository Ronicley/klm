import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react';
import { Copy, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Select } from '../../design-system/Select';
import { Toggle } from '../../design-system/Toggle';
import { EngineRequestError } from '../../engine';
import { createWebhook, listWebhooks, removeWebhook, updateWebhook, validateWebhookSecret, webhookRetentions, webhookURL, type WebhookConnection, type WebhookRetention } from './api';
import './webhooks.css';

type WebhookFormInput = { name: string; retention: WebhookRetention; secret?: string };

function failureMessage(error: unknown) {
  return error instanceof Error ? error.message : 'The webhook request failed.';
}

function Timestamp({ value }: { value?: string }) {
  return value ? <time dateTime={value} title={value}>{new Date(value).toLocaleString()}</time> : <span>Never</span>;
}

function WebhookForm({ connection, busy, onSave, onCancel }: {
  connection?: WebhookConnection; busy: boolean;
  onSave: (input: WebhookFormInput) => void; onCancel: () => void;
}) {
  const [name, setName] = useState(connection?.name ?? '');
  const [retention, setRetention] = useState<WebhookRetention | ''>(connection?.retention ?? '');
  const [changeSecret, setChangeSecret] = useState(!connection);
  const [secret, setSecret] = useState('');
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
    onSave({ name: name.trim(), retention, ...(changeSecret ? { secret } : {}) });
  }

  return <form className="settings-form webhook-form" onSubmit={submit} aria-busy={busy}>
    <h4>{connection ? 'Edit webhook' : 'Add webhook'}</h4>
    <div className="settings-field"><label htmlFor="webhook-name">Name</label><Input id="webhook-name" autoFocus required maxLength={60} value={name} disabled={busy} onChange={event => setName(event.target.value)} /></div>
    <div className="settings-field"><label htmlFor="webhook-retention">Event retention</label>
      <Select id="webhook-retention" label="Event retention" variant="field" required value={retention} disabled={busy} onChange={event => setRetention(event.target.value as WebhookRetention | '')}>
        <option value="" disabled>Choose retention</option>{webhookRetentions.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </Select>
    </div>
    {connection && <div className="settings-field"><Toggle label="Change secret" checked={changeSecret} disabled={busy} onCheckedChange={checked => { secretVersion.current += 1; setChangeSecret(checked); setSecret(''); setCopied(false); setError(''); }} /></div>}
    {changeSecret && <div className="settings-field"><label htmlFor="webhook-secret">Secret</label>
      <div className="webhook-secret-field"><Input id="webhook-secret" type="password" required maxLength={512} value={secret} disabled={busy} autoComplete="new-password" autoCapitalize="none" autoCorrect="off" spellCheck={false} aria-describedby="webhook-secret-hint" onChange={event => { secretVersion.current += 1; setSecret(event.target.value); setCopied(false); }} />
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
  const [nextCursor, setNextCursor] = useState<string>();
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState('');
  const [mutationError, setMutationError] = useState('');
  const [busy, setBusy] = useState(false);
  const [editor, setEditor] = useState<WebhookConnection | 'new' | null>(null);
  const [removingId, setRemovingId] = useState('');
  const [copyStatus, setCopyStatus] = useState('');
  const active = useRef(false);
  const listing = useRef<AbortController | null>(null);
  const mutation = useRef<AbortController | null>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const section = useRef<HTMLElement>(null);

  const load = useCallback(async (cursor?: string) => {
    listing.current?.abort();
    const controller = new AbortController(); listing.current = controller;
    setLoading(true); setListError('');
    try {
      const page = await listWebhooks(projectId, { limit: 50, ...(cursor ? { cursor } : {}) }, controller.signal);
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

  function restoreFocus() {
    requestAnimationFrame(() => {
      if (!active.current) return;
      const target = returnFocus.current?.isConnected ? returnFocus.current : section.current?.querySelector<HTMLElement>('[data-webhook-add]');
      if (target?.getClientRects().length && !target.matches(':disabled')) target.focus();
    });
  }

  async function mutate(action: (signal: AbortSignal) => Promise<unknown>, afterSuccess: () => void) {
    if (mutation.current) return;
    const controller = new AbortController(); mutation.current = controller;
    listing.current?.abort(); listing.current = null; setLoading(false);
    setBusy(true); onBusyChange(true); setMutationError(''); setCopyStatus('');
    try {
      await action(AbortSignal.any([controller.signal, AbortSignal.timeout(15000)]));
      if (!active.current || controller.signal.aborted || mutation.current !== controller) return;
      afterSuccess();
      await load();
    } catch (error) {
      if (!active.current || controller.signal.aborted || mutation.current !== controller) return;
      if (error instanceof EngineRequestError) {
        setMutationError(failureMessage(error));
        setRemovingId('');
      } else {
        // A transport failure may follow a committed mutation. Refresh before
        // offering another creation; never retry POST automatically.
        setEditor(null); setRemovingId(''); restoreFocus();
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
      void mutate(signal => createWebhook(projectId, { provider: 'github', name: input.name, retention: input.retention, secret }, signal), () => { setEditor(null); restoreFocus(); });
    } else {
      void mutate(signal => updateWebhook(projectId, target.id, input, signal), () => { setEditor(null); restoreFocus(); });
    }
  }

  return <section ref={section} className="webhooks-settings" aria-labelledby="project-webhooks-title">
    <div className="settings-panel-title"><h3 id="project-webhooks-title">Webhooks</h3><span className="webhook-provider">GitHub</span></div>
    {mutationError && <p role="alert" className="form-error">{mutationError}</p>}
    {copyStatus && <p role="status" className="webhook-copy-status">{copyStatus}</p>}
    <div hidden={editor !== null}>
      <div className="webhooks-toolbar"><Button data-webhook-add size="sm" variant="primary" disabled={busy || !!removingId} onClick={event => { returnFocus.current = event.currentTarget; setMutationError(''); setCopyStatus(''); setEditor('new'); }}><Plus />Add webhook</Button>
        <Button size="sm" disabled={loading || busy || !!removingId} onClick={() => { setCopyStatus(''); void load(); }}><RefreshCw />Refresh</Button></div>
      {listError && <p role="alert" className="form-error">{listError}</p>}
      {loading && <p role="status" className="webhook-loading">Loading webhooks...</p>}
      {!loading && !listError && items.length === 0 && <p className="webhook-empty">No webhooks</p>}
      <div className="webhooks-list" aria-busy={loading || busy}>
        {items.map(connection => <article key={connection.id} className="webhook-card" aria-label={connection.name}>
          <div className="webhook-card-heading"><h4>{connection.name}</h4><Toggle label={connection.enabled ? 'Enabled' : 'Disabled'} aria-label={`Enable ${connection.name}`} checked={connection.enabled} disabled={busy || loading || !!removingId} onCheckedChange={enabled => void mutate(signal => updateWebhook(projectId, connection.id, { enabled }, signal), () => {})} /></div>
          <div className="webhook-endpoint"><Input aria-label={`Webhook URL for ${connection.name}`} readOnly value={webhookURL(connection)} onFocus={event => event.target.select()} />
            <Button size="sm" disabled={busy} onClick={async () => {
              try { await navigator.clipboard.writeText(webhookURL(connection)); if (active.current) setCopyStatus('URL copied'); }
              catch { if (active.current) setCopyStatus('Could not copy the URL. Select and copy it from the URL field.'); }
            }}><Copy />Copy URL</Button></div>
          <dl className="webhook-metadata"><div><dt>Retention</dt><dd>{webhookRetentions.find(option => option.value === connection.retention)?.label}</dd></div>
            <div><dt>Last received</dt><dd><Timestamp value={connection.lastReceivedAt} /></dd></div>
            <div><dt>{connection.lastPingAt ? 'Verified' : 'Not verified'}</dt><dd>{connection.lastPingAt ? <Timestamp value={connection.lastPingAt} /> : '—'}</dd></div>
          </dl>
          <div className="webhook-card-actions" hidden={removingId === connection.id}>
            <Button size="sm" disabled={busy || loading || !!removingId} onClick={event => { returnFocus.current = event.currentTarget; setMutationError(''); setCopyStatus(''); setEditor(connection); }}><Pencil />Edit</Button>
            <IconButton label={`Remove ${connection.name}`} disabled={busy || loading || !!removingId} onClick={event => { returnFocus.current = event.currentTarget; setMutationError(''); setRemovingId(connection.id); }}><Trash2 /></IconButton>
          </div>
          {removingId === connection.id && <div className="webhook-remove-confirmation" role="group" aria-labelledby="webhook-remove-title" aria-describedby="webhook-remove-description">
            <h4 id="webhook-remove-title">Remove webhook?</h4><p id="webhook-remove-description">Removing {connection.name} deletes its stored events.</p>
            <div className="dialog-actions"><Button autoFocus size="sm" disabled={busy} onClick={() => { setRemovingId(''); restoreFocus(); }}>Cancel</Button>
              <Button size="sm" variant="danger-soft" disabled={busy} onClick={() => void mutate(signal => removeWebhook(projectId, connection.id, signal), () => { setRemovingId(''); setItems(current => current.filter(item => item.id !== connection.id)); })}>{busy ? 'Removing...' : 'Remove webhook'}</Button></div>
          </div>}
        </article>)}
      </div>
      {hasMore && <div className="webhook-pagination"><Button size="sm" disabled={loading || busy || !nextCursor || !!removingId} onClick={() => void load(nextCursor)}>Load more</Button></div>}
    </div>
    {editor !== null && <WebhookForm key={editor === 'new' ? 'new' : editor.id} connection={editor === 'new' ? undefined : editor} busy={busy} onSave={save} onCancel={() => { setEditor(null); setMutationError(''); restoreFocus(); }} />}
  </section>;
}
