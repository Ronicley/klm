import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { EngineDialog } from './EngineProvider';
import { EngineAvatar, engineIcons } from './EngineSwitcher';
import { loadConnectionDrafts, updateConnectionDrafts, useConnectionDrafts, type ConnectionForm } from './connectionDrafts';
import { engineIconIds, type SavedEngine } from './engineRegistry';

export function EngineConnectionDialog({ mode, slot, error, busy, duplicate, submit, close, useDuplicate }: {
  mode: 'add' | 'edit' | 'repair'; slot: string; error: string; busy: boolean; duplicate?: SavedEngine;
  submit: (form: ConnectionForm, savedAccess: boolean) => Promise<void>; close: () => void; useDuplicate: (engine: SavedEngine) => void;
}) {
  const drafts = useConnectionDrafts();
  const form = drafts.document.forms[slot];
  const title = mode === 'add' ? 'Add engine' : mode === 'edit' ? 'Edit engine' : 'Pair engine again';
  const patch = (value: Partial<ConnectionForm>) => updateConnectionDrafts(document => ({ ...document, forms: { ...document.forms, [slot]: { ...document.forms[slot], ...value } } }));
  return <EngineDialog title={title} busy={busy} close={close}>
    {!drafts.ready || !form ? <div><p role="alert">{drafts.error || 'Loading connection draft…'}</p>{drafts.error && <Button onClick={() => void loadConnectionDrafts().catch(() => {})}>Retry connection draft</Button>}</div> : <form className="settings-form engine-connection-form" onSubmit={event => { event.preventDefault(); void submit(form, false); }}>
      <fieldset disabled={busy}>
        <label>Engine name<Input autoFocus required maxLength={80} value={form.name} onChange={event => patch({ name: event.target.value })} /></label>
        <div className="engine-icon-field"><span>Icon</span><div className="engine-icon-options" role="group" aria-label="Engine icon">
          <Button aria-label="Use initials" aria-pressed={!form.iconId} variant={!form.iconId ? 'soft' : 'ghost'} onClick={() => patch({ iconId: undefined })}><EngineAvatar engine={{ name: form.name || 'Engine' }} /></Button>
          {engineIconIds.map(iconId => { const Icon = engineIcons[iconId]; return <Button key={iconId} aria-label={iconId} aria-pressed={form.iconId === iconId} variant={form.iconId === iconId ? 'soft' : 'ghost'} onClick={() => patch({ iconId })}><Icon aria-hidden="true" /></Button>; })}
        </div></div>
        <label>Engine URL<Input required value={form.url} readOnly={mode !== 'add'} placeholder="https://engine.example.com" autoCapitalize="none" autoCorrect="off" spellCheck={false} onChange={event => patch({ url: event.target.value })} /></label>
        {mode !== 'edit' && <>
          <label>Client name<Input required maxLength={80} value={form.clientName} onChange={event => patch({ clientName: event.target.value })} /></label>
          <label>Pairing code<Input type="password" autoComplete="off" maxLength={100} value={form.code} onChange={event => patch({ code: event.target.value, codeURL: form.url })} /></label>
        </>}
      </fieldset>
      {(error || drafts.error) && <p role="alert" className="form-error">{error || drafts.error}</p>}
      {duplicate && <Button disabled={busy} onClick={() => useDuplicate(duplicate)}>Use {duplicate.name}</Button>}
      <div className="dialog-actions"><Button disabled={busy} onClick={close}>Close</Button><Button disabled={busy} onClick={() => { updateConnectionDrafts(document => { const forms = { ...document.forms }; delete forms[slot]; return { ...document, forms }; }); close(); }}>Discard draft</Button>{mode !== 'edit' && <Button disabled={busy} onClick={() => void submit(form, true)}>Use saved access</Button>}<Button type="submit" variant="primary" disabled={busy}>{busy ? 'Connecting…' : mode === 'edit' ? 'Save changes' : 'Pair and connect'}</Button></div>
    </form>}
  </EngineDialog>;
}
