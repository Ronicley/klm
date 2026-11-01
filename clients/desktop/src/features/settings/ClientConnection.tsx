import { useEffect, useState, type FormEvent, type ReactNode } from 'react';
import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { AUTH_REQUIRED_EVENT, clientAuthGeneration, clientFetch, initializeClientAuth, isClientAuthCurrent, pairClient, retryClientAuth } from '../../clientAuth';
import { configureEngineURL, IS_DESKTOP, IS_MOBILE_HOST, reloadClient, returnToHosts, selectableEngineURL } from '../../platform';
import { ENGINE_URL } from '../../engine';
import './client-connection.css';
import { EngineRecoveryActions, EngineRecoverySummary } from '../engines/EngineProvider';
import { engineChangePending } from '../engines/localActivities';

export function ClientConnection({ children }: { children: ReactNode }) {
  const [connected, setConnected] = useState(false);
  const [checking, setChecking] = useState(true);
  const [engineURL, setEngineURL] = useState(ENGINE_URL);
  const [name, setName] = useState(IS_MOBILE_HOST ? 'Mobile' : 'My client');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function check() {
    const version = clientAuthGeneration();
    try {
      await initializeClientAuth();
      const response = await clientFetch('/api/auth/session', { signal: AbortSignal.timeout(15000) });
      if (!isClientAuthCurrent(version)) return false;
      if (!response.ok) {
        if (response.status !== 401) { const result = await response.json(); throw new Error(result.error || 'Could not connect to the engine.'); }
        setConnected(false);
        return false;
      }
      setConnected(true); setError('');
      return true;
    } catch (error) {
      if (isClientAuthCurrent(version)) setError(error instanceof Error ? error.message : 'Could not connect to the engine.');
      return false;
    } finally { if (version === clientAuthGeneration()) setChecking(false); }
  }

  useEffect(() => {
    const denied = () => { setConnected(false); setCode(''); setChecking(false); };
    window.addEventListener(AUTH_REQUIRED_EVENT, denied);
    void check();
    return () => window.removeEventListener(AUTH_REQUIRED_EVENT, denied);
  }, []);

  useEffect(() => {
    if (!connected) return;
    // Authenticated client-originated requests renew both durable activity and
    // the cookie. Server output alone is never activity.
    const renew = () => { if (!engineChangePending()) void check(); };
    const timer = window.setInterval(renew, 60000);
    window.addEventListener('focus', renew);
    return () => { window.clearInterval(timer); window.removeEventListener('focus', renew); };
  }, [connected]);

  async function pair(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true); setError('');
    try {
      const base = selectableEngineURL(engineURL);
      await pairClient(base, name.trim(), code.trim());
      setCode('');
      configureEngineURL(base);
      if (base !== ENGINE_URL) { reloadClient(); return; }
      if (!await check()) setError('Pairing could not be retained. Check that the frontend and engine use same-site HTTPS URLs and cookies are enabled.');
    } catch (error) { setError(error instanceof Error ? error.message : 'Could not pair this client.'); }
    finally { setBusy(false); }
  }

  async function connect() {
    if (busy) return;
    setError('');
    try {
      const base = configureEngineURL(engineURL);
      // Resolve a new engine and its existing cookie/native credential only after
      // reload; the current engine's in-memory bearer never follows the edit.
      if (base !== ENGINE_URL) { reloadClient(); return; }
      retryClientAuth();
      setChecking(true);
      await check();
    } catch (error) { setError(error instanceof Error ? error.message : 'Could not connect to the engine.'); }
  }

  if (connected) return children;
  if (IS_DESKTOP) return <main className="client-connection"><section className="client-connection-card"><h1>{checking ? 'Connecting to engine…' : 'Engine connection unavailable'}</h1><EngineRecoverySummary />{error && <p role="alert" className="form-error">{error}</p>}<EngineRecoveryActions /></section></main>;
  return <main className="client-connection"><section className="client-connection-card">
    <h1>Connect to engine</h1>
    {checking ? <p role="status">Connecting...</p> : <form className="settings-form" onSubmit={pair}>
      <div className="settings-field"><label htmlFor="pair-engine">Engine URL</label><Input id="pair-engine" required value={engineURL} onChange={event => setEngineURL(event.target.value)} autoCapitalize="none" autoCorrect="off" spellCheck={false} /></div>
      <div className="settings-field"><label htmlFor="pair-name">Client name</label><Input id="pair-name" required maxLength={80} value={name} onChange={event => setName(event.target.value)} /></div>
      <div className="settings-field"><label htmlFor="pair-code">Pairing code</label><Input id="pair-code" type="password" required autoComplete="off" maxLength={100} value={code} onChange={event => setCode(event.target.value)} /></div>
      <p className="muted">Run <code>klm pair</code> on the engine computer.</p>
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="dialog-actions"><Button type="button" disabled={busy} onClick={() => void connect()}>Connect</Button><Button variant="primary" type="submit" disabled={busy}>{busy ? 'Pairing...' : 'Pair client'}</Button></div>
    </form>}
    {IS_MOBILE_HOST && <Button onClick={returnToHosts}>Back to hosts</Button>}
  </section></main>;
}
