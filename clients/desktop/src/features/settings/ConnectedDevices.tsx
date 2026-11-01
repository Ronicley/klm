import { useEffect, useState } from 'react';
import { Button } from '../../design-system/Button';
import { request } from '../../engine';
import { invalidateClientAuth } from '../../clientAuth';

type Device = { id: string; name: string; createdAt: string; lastUsedAt: string; current: boolean };

export function ConnectedDevices() {
  const [devices, setDevices] = useState<Device[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  async function load() {
    try { setDevices(await request<Device[]>('/api/auth/devices')); setError(''); }
    catch (error) { setError(error instanceof Error ? error.message : 'Could not load connected devices.'); }
  }
  useEffect(() => { void load(); }, []);
  async function revoke(device?: Device) {
    if (!window.confirm(device ? `Revoke access for ${device.name}?` : 'Revoke access for all devices, including this client?')) return;
    setBusy(true); setError('');
    try {
      await request(`/api/auth/devices${device ? `/${encodeURIComponent(device.id)}` : ''}`, 'DELETE', {});
      if (!device || device.current) invalidateClientAuth();
      else await load();
    } catch (error) { setError(error instanceof Error ? error.message : 'Could not revoke access.'); }
    finally { setBusy(false); }
  }
  return <section aria-labelledby="connected-devices-title">
    <div className="settings-panel-title"><h3 id="connected-devices-title">Connected devices</h3><Button disabled={busy || !devices.length} onClick={() => void revoke()}>Revoke all</Button></div>
    <div className="connected-device-list">{devices.map(device => <div className="connected-device" key={device.id}>
      <div><strong>{device.name}</strong>{device.current && <span className="muted"> · This client</span>}<div className="muted">Last used {new Date(device.lastUsedAt).toLocaleString('en')}</div></div>
      <Button size="sm" disabled={busy} onClick={() => void revoke(device)}>Revoke</Button>
    </div>)}</div>
    {error && <p className="form-error" role="alert">{error} <Button size="sm" onClick={() => void load()}>Retry</Button></p>}
  </section>;
}
