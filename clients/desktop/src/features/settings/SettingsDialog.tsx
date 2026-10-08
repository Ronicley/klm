import { useEffect, useRef, useState, type FormEvent } from 'react';
import { KeyRound, MessageSquare, Mic, Plus, Trash2, X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { ENGINE_URL, getTranscriptionSettings, updateTranscriptionSettings, type TranscriptionSettings } from '../../engine';
import { configureEngineURL, enableNotifications, IS_DESKTOP, IS_MOBILE_HOST, notificationPermission } from '../../platform';

type SettingsSection = 'chat' | 'transcription';

const emptyTranscription: TranscriptionSettings = {
  apiKeyConfigured: false,
  model: 'gpt-4o-mini-transcribe',
  language: '',
  vocabulary: [],
};

function failureMessage(error: unknown) {
  return error instanceof Error ? error.message : 'Could not update transcription settings.';
}

export function SettingsDialog({ onClose, onDesignSystem }: { onClose: () => void; onDesignSystem: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [section, setSection] = useState<SettingsSection>('chat');
  const [engineURL, setEngineURL] = useState(ENGINE_URL);
  const [engineError, setEngineError] = useState('');
  const [notifications, setNotifications] = useState(notificationPermission);
  const [notificationError, setNotificationError] = useState('');
  const [enablingNotifications, setEnablingNotifications] = useState(false);
  const [settings, setSettings] = useState(emptyTranscription);
  const [apiKey, setAPIKey] = useState('');
  const [removeAPIKey, setRemoveAPIKey] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [settingsError, setSettingsError] = useState('');
  const [saved, setSaved] = useState(false);

  async function loadSettings() {
    setLoading(true);
    setSettingsError('');
    try { setSettings(await getTranscriptionSettings()); }
    catch (error) { setSettingsError(failureMessage(error)); }
    finally { setLoading(false); }
  }

  useEffect(() => {
    if (dialog.current && !dialog.current.open) dialog.current.showModal();
    void loadSettings();
  }, []);

  function connectEngine(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    try {
      configureEngineURL(engineURL);
      window.location.reload();
    } catch (error) {
      setEngineError(error instanceof Error ? error.message : 'Enter a valid engine URL.');
    }
  }

  async function activateNotifications() {
    setEnablingNotifications(true);
    setNotificationError('');
    try { setNotifications(await enableNotifications()); }
    catch (error) { setNotificationError(error instanceof Error ? error.message : 'Could not enable notifications.'); }
    finally { setEnablingNotifications(false); }
  }

  async function saveTranscription(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (saving || loading) return;
    setSaving(true);
    setSettingsError('');
    setSaved(false);
    try {
      const next = await updateTranscriptionSettings({
        ...(apiKey.trim() ? { apiKey: apiKey.trim() } : removeAPIKey ? { apiKey: null } : {}),
        model: settings.model,
        language: settings.language,
        vocabulary: settings.vocabulary,
      });
      setSettings(next);
      setAPIKey('');
      setRemoveAPIKey(false);
      setSaved(true);
    } catch (error) {
      setSettingsError(failureMessage(error));
    } finally {
      setSaving(false);
    }
  }

  return <dialog ref={dialog} className="settings-dialog app-settings-dialog" aria-labelledby="settings-title" onCancel={onClose} onClose={onClose}>
    <div className="dialog-heading app-settings-heading"><h2 id="settings-title">Settings</h2><IconButton label="Close settings" onClick={onClose}><X /></IconButton></div>
    <div className="app-settings-layout">
      <nav className="settings-sidebar" aria-label="Settings sections">
        <button type="button" className={section === 'chat' ? 'is-active' : ''} aria-current={section === 'chat' ? 'page' : undefined} onClick={() => setSection('chat')}><MessageSquare />Chat</button>
        <button type="button" className={section === 'transcription' ? 'is-active' : ''} aria-current={section === 'transcription' ? 'page' : undefined} onClick={() => setSection('transcription')}><Mic />Transcription</button>
      </nav>
      <div className="settings-panel">
        {section === 'chat' ? <section aria-labelledby="chat-settings-title">
          <h3 id="chat-settings-title">Chat</h3>
          <form className="engine-settings-form settings-form" onSubmit={connectEngine}>
            <label htmlFor="engine-url">Engine URL</label>
            <Input id="engine-url" type="text" inputMode="url" required autoCapitalize="none" autoCorrect="off" spellCheck={false} placeholder="http://localhost:7331" value={engineURL} aria-invalid={!!engineError} aria-describedby={engineError ? 'engine-url-error' : undefined} onChange={event => { setEngineURL(event.target.value); setEngineError(''); }} />
            {engineError && <p id="engine-url-error" className="form-error" role="alert">{engineError}</p>}
            <div className="dialog-actions"><Button variant="primary" type="submit">Connect</Button></div>
          </form>
          {!IS_DESKTOP && !IS_MOBILE_HOST && <div className="settings-form">
            <h4>Notifications</h4>
            {notifications === 'default' && <Button disabled={enablingNotifications} onClick={() => void activateNotifications()}>{enablingNotifications ? 'Enabling...' : 'Enable notifications'}</Button>}
            {notifications === 'granted' && <p role="status">Notifications enabled</p>}
            {notifications === 'denied' && <p>Notifications blocked. Allow notifications in your browser's site settings.</p>}
            {notifications === 'unsupported' && <p>Notifications unavailable. Open this client over HTTPS or localhost.</p>}
            {notificationError && <p className="form-error" role="alert">{notificationError}</p>}
          </div>}
          <div className="settings-design-system"><span>Interface components and tokens</span><Button onClick={onDesignSystem}>Open design system</Button></div>
        </section> : <section aria-labelledby="transcription-settings-title">
          <div className="settings-panel-title"><div><h3 id="transcription-settings-title">Transcription</h3>{settings.apiKeyConfigured && !removeAPIKey && <span className="settings-configured"><KeyRound />API key configured</span>}</div>{loading && <span className="muted">Loading...</span>}</div>
          <form className="settings-form transcription-settings-form" onSubmit={saveTranscription}>
            <div className="settings-field settings-api-key">
              <label htmlFor="transcription-api-key">API key</label>
              <div><Input id="transcription-api-key" type="password" autoComplete="off" maxLength={512} placeholder={settings.apiKeyConfigured ? 'Leave blank to keep current key' : 'OpenAI API key'} value={apiKey} disabled={loading || saving || removeAPIKey} onChange={event => { setAPIKey(event.target.value); setRemoveAPIKey(false); setSaved(false); }} />
              {settings.apiKeyConfigured && <Button size="sm" variant={removeAPIKey ? 'soft' : 'outline'} disabled={loading || saving} onClick={() => { setRemoveAPIKey(current => !current); setAPIKey(''); setSaved(false); }}>{removeAPIKey ? 'Keep key' : 'Remove key'}</Button>}</div>
            </div>
            <div className="settings-field"><label htmlFor="transcription-model">Model</label><Input id="transcription-model" required maxLength={200} autoCapitalize="none" autoCorrect="off" spellCheck={false} value={settings.model} disabled={loading || saving} onChange={event => { setSettings(current => ({ ...current, model: event.target.value })); setSaved(false); }} /></div>
            <div className="settings-field"><label htmlFor="transcription-language">Language</label><Input id="transcription-language" maxLength={100} placeholder="Optional" value={settings.language} disabled={loading || saving} onChange={event => { setSettings(current => ({ ...current, language: event.target.value })); setSaved(false); }} /></div>
            <div className="vocabulary-heading"><h4>Vocabulary</h4><Button size="sm" disabled={loading || saving || settings.vocabulary.length >= 200} onClick={() => { setSettings(current => ({ ...current, vocabulary: [...current.vocabulary, { term: '', note: '' }] })); setSaved(false); }}><Plus />Add term</Button></div>
            <div className="vocabulary-list">{settings.vocabulary.map((entry, index) => <div className="vocabulary-row" key={index}>
              <Input aria-label={`Vocabulary term ${index + 1}`} maxLength={120} placeholder="Term" value={entry.term} disabled={loading || saving} onChange={event => { const term = event.target.value; setSettings(current => ({ ...current, vocabulary: current.vocabulary.map((item, itemIndex) => itemIndex === index ? { ...item, term } : item) })); setSaved(false); }} />
              <Input aria-label={`Vocabulary note ${index + 1}`} maxLength={500} placeholder="Note" value={entry.note} disabled={loading || saving} onChange={event => { const note = event.target.value; setSettings(current => ({ ...current, vocabulary: current.vocabulary.map((item, itemIndex) => itemIndex === index ? { ...item, note } : item) })); setSaved(false); }} />
              <IconButton label={`Remove vocabulary term ${index + 1}`} disabled={loading || saving} onClick={() => { setSettings(current => ({ ...current, vocabulary: current.vocabulary.filter((_, itemIndex) => itemIndex !== index) })); setSaved(false); }}><Trash2 /></IconButton>
            </div>)}</div>
            {settingsError && <p className="form-error" role="alert">{settingsError} {loading ? null : <Button size="sm" onClick={() => void loadSettings()}>Reload</Button>}</p>}
            {saved && <p className="settings-saved" role="status">Saved</p>}
            <div className="dialog-actions"><Button variant="primary" type="submit" disabled={loading || saving || !settings.model.trim()}>{saving ? 'Saving...' : 'Save transcription'}</Button></div>
          </form>
        </section>}
      </div>
    </div>
  </dialog>;
}
