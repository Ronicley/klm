import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Button } from '../../design-system/Button';
import { Textarea } from '../../design-system/Textarea';
import { request } from '../../engine';
import { flushDrafts, forgetDrafts, hasEngineDraft, useEngineDraft } from '../drafts/draftStore';
import { IS_DESKTOP } from '../../platform';
import { beginLocalActivity } from '../engines/localActivities';

type AccessSettings = {
  allowedOrigins: string[];
  trustedTlsProxies: string[];
  restartRequired: boolean;
  restarting: boolean;
};

function failure(error: unknown) { return error instanceof Error ? error.message : 'Could not update engine settings.'; }
function entries(value: string) { return value.split(/[\n,]/).map(item => item.trim()).filter(Boolean); }

function RestartEngineDialog({ onDecision }: { onDecision: (confirmed: boolean) => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => { dialog.current?.showModal(); }, []);
  return <dialog ref={dialog} className="settings-dialog add-project-dialog" aria-labelledby="restart-engine-title"
    onClose={event => event.stopPropagation()}
    onCancel={event => { event.preventDefault(); event.stopPropagation(); onDecision(false); }}>
    <h2 id="restart-engine-title">Restart engine?</h2>
    <p>Restarting will interrupt all agents and graph runs in progress. Saved engine settings will take effect after restart.</p>
    <div className="dialog-actions"><Button onClick={() => onDecision(false)}>Cancel</Button><Button variant="danger-soft" onClick={() => onDecision(true)}>Restart engine</Button></div>
  </dialog>;
}

export function EngineAccessSettings() {
  const [settings, setSettings] = useState<AccessSettings>();
  const [origins, setOrigins, clearOrigins] = useEngineDraft('form.access.origins', '');
  const [proxies, setProxies, clearProxies] = useEngineDraft('form.access.proxies', '');
  const [base, setBase] = useEngineDraft<Pick<AccessSettings, 'allowedOrigins' | 'trustedTlsProxies'> | undefined>('form.access.base', undefined);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [restarting, setRestarting] = useEngineDraft('form.access.restarting', false);

  function receive(next: AccessSettings, confirmed = false) {
    setSettings(next);
    if (next.restarting) setRestarting(true);
    if (confirmed || !hasEngineDraft('form.access.origins')) setOrigins(next.allowedOrigins.join('\n'));
    if (confirmed || !hasEngineDraft('form.access.proxies')) setProxies(next.trustedTlsProxies.join('\n'));
    if (confirmed || !hasEngineDraft('form.access.base')) setBase({ allowedOrigins: next.allowedOrigins, trustedTlsProxies: next.trustedTlsProxies });
    if (confirmed) { clearOrigins(next.allowedOrigins.join('\n')); clearProxies(next.trustedTlsProxies.join('\n')); }
  }

  async function load(discard = false) {
    if (discard && IS_DESKTOP) forgetDrafts('form.access');
    setLoading(true); setError('');
    try { receive(await request<AccessSettings>('/api/settings/access')); }
    catch (error) { setError(failure(error)); }
    finally { setLoading(false); }
  }
  useEffect(() => { void load(); }, []);

  useEffect(() => {
    if (!restarting) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const deadline = Date.now() + 120000;
    async function reconnect() {
      try {
        const next = await request<AccessSettings>('/api/settings/access', 'GET', undefined, 2500);
        if (!active) return;
        if (!next.restarting) { receive(next); setRestarting(false); setError(''); return; }
      } catch { /* Expected while the worker releases its lock and starts again. */ }
      if (!active) return;
      if (Date.now() >= deadline) {
        setRestarting(false);
        setError('Restart could not be confirmed. Reload settings or reconnect to the engine.');
        return;
      }
      timer = setTimeout(() => void reconnect(), 2000);
    }
    timer = setTimeout(() => void reconnect(), 2000);
    return () => { active = false; clearTimeout(timer); };
  }, [restarting]);

  const dirty = !!settings && (origins !== settings.allowedOrigins.join('\n') || proxies !== settings.trustedTlsProxies.join('\n'));
  const busy = loading || saving || restarting;

  async function save(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    const finished = beginLocalActivity('Saving engine access settings');
    setSaving(true); setError(''); setSaved(false);
    try {
      await flushDrafts();
      if (base && hasEngineDraft('form.access.base')) {
        const latest = await request<AccessSettings>('/api/settings/access');
        if (JSON.stringify([base.allowedOrigins, base.trustedTlsProxies]) !== JSON.stringify([latest.allowedOrigins, latest.trustedTlsProxies])) throw new Error('Engine access settings changed. Reload settings before saving this draft.');
      }
      receive(await request<AccessSettings>('/api/settings/access', 'PATCH', {
        allowedOrigins: entries(origins), trustedTlsProxies: entries(proxies),
      }), true);
      setSaved(true);
    } catch (error) { setError(failure(error)); }
    finally { setSaving(false); finished(); }
  }

  async function restart(confirmed: boolean) {
    setConfirming(false);
    if (!confirmed || busy) return;
    const finished = beginLocalActivity('Requesting engine restart');
    setSaving(true); setError('');
    try {
      await request('/api/engine/restart', 'POST', { confirmInterrupt: true });
      setRestarting(true);
    } catch (error) { setError(`${failure(error)} Restart delivery may be unconfirmed; reload settings before retrying.`); }
    finally { setSaving(false); finished(); }
  }

  return <section aria-labelledby="engine-access-title">
    <div className="settings-panel-title"><h3 id="engine-access-title">Engine access</h3>{loading && <span className="muted">Loading...</span>}</div>
    <form className="settings-form" onSubmit={save}>
      <div className="settings-field"><label htmlFor="engine-allowed-origins">Allowed frontend origins</label>
        <Textarea id="engine-allowed-origins" rows={4} maxLength={8192} placeholder="https://app.example.com" value={origins} disabled={busy || !settings} autoCapitalize="none" autoCorrect="off" spellCheck={false} onChange={event => { setOrigins(event.target.value); setSaved(false); }} />
      </div>
      <div className="settings-field"><label htmlFor="engine-trusted-proxies">Trusted TLS proxies (CIDR)</label>
        <Textarea id="engine-trusted-proxies" rows={4} maxLength={8192} placeholder="192.168.1.10/32" value={proxies} disabled={busy || !settings} autoCapitalize="none" autoCorrect="off" spellCheck={false} onChange={event => { setProxies(event.target.value); setSaved(false); }} />
      </div>
      <p className="muted">One entry per line. Saved changes take effect after restarting the engine.</p>
      {error && <p className="form-error" role="alert">{error} <Button size="sm" disabled={busy} onClick={() => void load(true)}>{IS_DESKTOP ? 'Discard and reload' : 'Reload settings'}</Button></p>}
      {saved && <p className="settings-saved" role="status">Saved</p>}
      {settings?.restartRequired && !restarting && <p role="status">Restart required</p>}
      {restarting && <p role="status">Restarting engine...</p>}
      <div className="dialog-actions">
        <Button variant="danger-soft" disabled={busy || !settings || dirty} onClick={() => setConfirming(true)}>Restart engine</Button>
        <Button variant="primary" type="submit" disabled={busy || !dirty}>{saving ? 'Saving...' : 'Save'}</Button>
      </div>
    </form>
    {confirming && <RestartEngineDialog onDecision={confirmed => void restart(confirmed)} />}
  </section>;
}
