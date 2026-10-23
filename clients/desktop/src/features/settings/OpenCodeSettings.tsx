import { useEffect, useState, type FormEvent } from 'react';
import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Select } from '../../design-system/Select';
import { getOpenCodeSettings, updateOpenCodeSettings, type OpenCodeConfiguration, type OpenCodeSettingsState } from '../../engine';

const defaults: OpenCodeConfiguration = { version: 'v2', v1Path: '', v2Path: '' };

export function OpenCodeSettings() {
  const [settings, setSettings] = useState(defaults);
  const [state, setState] = useState<OpenCodeSettingsState | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');

  async function load(signal?: AbortSignal) {
    setLoading(true);
    setError('');
    try {
      const next = await getOpenCodeSettings(signal);
      if (signal?.aborted) return;
      setSettings(next.settings);
      setState(next);
      setSaved(false);
    } catch (error) {
      if (!signal?.aborted) setError(error instanceof Error ? error.message : 'Could not load OpenCode settings.');
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, []);

  function change(values: Partial<OpenCodeConfiguration>) {
    setSettings(current => ({ ...current, ...values }));
    setSaved(false);
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (loading || saving || !state) return;
    setSaving(true);
    setError('');
    setSaved(false);
    try {
      const next = await updateOpenCodeSettings(settings);
      setSettings(next.settings);
      setState(next);
      setSaved(true);
    } catch (error) {
      setError(error instanceof Error ? error.message : 'Could not save OpenCode settings.');
    } finally {
      setSaving(false);
    }
  }

  return <section aria-labelledby="opencode-settings-title">
    <div className="settings-panel-title"><h3 id="opencode-settings-title">OpenCode</h3>{loading && <span className="muted">Loading...</span>}</div>
    <form className="settings-form" onSubmit={save}>
      <div className="settings-field"><label htmlFor="opencode-version">Version</label>
        <Select id="opencode-version" label="OpenCode version" variant="field" value={settings.version} disabled={loading || saving || !state} onChange={event => change({ version: event.target.value as OpenCodeConfiguration['version'] })}>
          <option value="v1">OpenCode V1</option><option value="v2">OpenCode V2</option>
        </Select>
      </div>
      <div className="settings-field"><label htmlFor="opencode-v1-path">V1 executable path</label>
        <Input id="opencode-v1-path" value={settings.v1Path} maxLength={4096} autoCapitalize="none" autoCorrect="off" spellCheck={false} placeholder="Automatic discovery" disabled={loading || saving || !state} onChange={event => change({ v1Path: event.target.value })} />
      </div>
      <div className="settings-field"><label htmlFor="opencode-v2-path">V2 executable path</label>
        <Input id="opencode-v2-path" value={settings.v2Path} maxLength={4096} autoCapitalize="none" autoCorrect="off" spellCheck={false} placeholder="Automatic discovery" disabled={loading || saving || !state} onChange={event => change({ v2Path: event.target.value })} />
      </div>
      <p className="muted">Paths are on the engine computer. Leave blank for automatic discovery.</p>
      {state && <p>Active: OpenCode {state.activeVersion.toUpperCase()}</p>}
      {state?.restartRequired && <p role="status">Restart the engine to apply the saved configuration. Existing conversations keep their native version.</p>}
      {saved && !state?.restartRequired && <p className="settings-saved" role="status">Saved</p>}
      {error && <p className="form-error" role="alert">{error} <Button size="sm" disabled={loading || saving} onClick={() => void load()}>Reload</Button></p>}
      <div className="dialog-actions"><Button type="submit" variant="primary" disabled={loading || saving || !state}>{saving ? 'Saving...' : 'Save OpenCode'}</Button></div>
    </form>
  </section>;
}
