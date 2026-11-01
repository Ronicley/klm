import { useEffect, useState } from 'react';
import { getVersion } from '@tauri-apps/api/app';
import { Button } from '../../design-system/Button';
import { getEngineVersion } from '../../engine';
import { IS_DESKTOP } from '../../platform';

export function AboutSettings() {
  const [desktopVersion, setDesktopVersion] = useState<string | null>(IS_DESKTOP ? null : import.meta.env.VITE_DESKTOP_VERSION);
  const [engineVersion, setEngineVersion] = useState<string | null>(null);
  const [desktopError, setDesktopError] = useState('');
  const [engineError, setEngineError] = useState('');
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let active = true;
    setDesktopError('');
    setEngineError('');
    setEngineVersion(null);
    if (IS_DESKTOP) {
      setDesktopVersion(null);
      void getVersion().then(version => {
        if (active) setDesktopVersion(version);
      }).catch(() => {
        if (active) setDesktopError('Could not read the desktop client version.');
      });
    }
    void getEngineVersion().then(result => {
      if (active) {
        if (typeof result.version === 'string' && result.version.trim()) setEngineVersion(result.version);
        else setEngineError('The engine did not return its version.');
      }
    }).catch(error => {
      if (active) setEngineError(error instanceof Error ? error.message : 'Could not read the engine version.');
    });
    return () => { active = false; };
  }, [attempt]);

  return <section aria-labelledby="about-settings-title">
    <h3 id="about-settings-title">About</h3>
    <dl className="settings-versions" aria-live="polite">
      <div><dt>Desktop client</dt><dd>{desktopError ? 'Unavailable' : desktopVersion ?? 'Loading...'}</dd></div>
      <div><dt>Engine</dt><dd>{engineError ? 'Unavailable' : engineVersion ?? 'Loading...'}</dd></div>
    </dl>
    {desktopError && <p className="form-error" role="alert">{desktopError}</p>}
    {engineError && <p className="form-error" role="alert">{engineError}</p>}
    {(desktopError || engineError) && <Button size="sm" onClick={() => setAttempt(current => current + 1)}>Reload</Button>}
  </section>;
}
