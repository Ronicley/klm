import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ImagePlus, MoreVertical, Trash2 } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Menu, MenuItem } from '../../design-system/Menu';
import type { Project } from '../../engine';
import { prepareProjectIcon } from './projectIcon';

export function ProjectGeneralSettings({ project, onSave, onRemove, onClose, onBusyChange }: {
  project: Project;
  onSave: (id: string, input: Pick<Project, 'name' | 'icon'>) => Promise<string | null>;
  onRemove: (id: string) => Promise<string | null>;
  onClose: () => void;
  onBusyChange: (busy: boolean) => void;
}) {
  const [name, setName] = useState(project.name);
  const [icon, setIcon] = useState(project.icon);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const pending = useRef(false);
  const active = useRef(false);
  const removal = useRef<HTMLDivElement>(null);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);

  async function run(action: () => Promise<void>) {
    if (pending.current) return;
    pending.current = true; setBusy(true); onBusyChange(true); setError(''); setSaved(false);
    try { await action(); }
    catch (error) { if (active.current) setError(error instanceof Error ? error.message : 'Could not update the project.'); }
    finally {
      pending.current = false;
      if (active.current) { setBusy(false); onBusyChange(false); }
    }
  }

  function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!name.trim()) return;
    void run(async () => {
      const failure = await onSave(project.id, { name: name.trim(), icon });
      if (!active.current) return;
      if (failure) setError(failure); else setSaved(true);
    });
  }

  return <section aria-labelledby="project-general-title">
    <h3 id="project-general-title">General</h3>
    <form className="settings-form" onSubmit={save} aria-busy={busy}>
      <div className="settings-field"><label htmlFor="project-settings-icon">Project icon</label>
        <div className="project-picture-picker project-icon-trigger">
          <div className="project-picture-preview">{icon ? <img src={icon} alt="" /> : <ImagePlus aria-hidden="true" />}</div>
          <span className="button button--outline button--md">{icon ? 'Change icon' : 'Choose icon'}</span>
          <input id="project-settings-icon" type="file" className="native-picker-overlay" aria-label={icon ? 'Change project icon' : 'Choose project icon'} accept="image/png,image/jpeg,image/webp" disabled={busy} onChange={event => {
            const file = event.target.files?.[0]; event.target.value = '';
            if (file) void run(async () => { const next = await prepareProjectIcon(file); if (active.current) setIcon(next); });
          }} />
        </div>
      </div>
      <div className="settings-field"><label htmlFor="project-settings-name">Project name</label>
        <div ref={removal} className="project-settings-name-row">
          <Input id="project-settings-name" required maxLength={60} value={name} disabled={busy} onChange={event => { setName(event.target.value); setSaved(false); }} />
          <Menu label="Project actions" role="menu" open={menuOpen} onOpenChange={setMenuOpen} side="bottom" className="project-context-menu" trigger={props => <IconButton {...props} id="project-settings-more" label="More project actions" disabled={busy || confirming}><MoreVertical /></IconButton>}>
            <MenuItem role="menuitem" className="project-remove-action" onClick={() => { setMenuOpen(false); setConfirming(true); }}><Trash2 />Remove project</MenuItem>
          </Menu>
        </div>
      </div>
      <div className="settings-field"><label htmlFor="project-settings-directory">Project directory</label><Input id="project-settings-directory" value={project.folder} readOnly title={project.folder} /></div>
      {error && <p role="alert" className="form-error">{error}</p>}
      {saved && <p role="status" className="settings-saved">Saved</p>}
      <div className="dialog-actions"><Button type="submit" variant="primary" disabled={busy || !name.trim()}>{busy ? 'Please wait...' : 'Save changes'}</Button></div>
    </form>
    {confirming && <div className="project-settings-removal" role="group" aria-labelledby="project-remove-title">
        <h4 id="project-remove-title">Remove project?</h4><p>Remove {project.name} from the workspace?</p>
        <div className="dialog-actions"><Button autoFocus disabled={busy} onClick={() => { setConfirming(false); requestAnimationFrame(() => { if (active.current) removal.current?.querySelector<HTMLElement>('#project-settings-more')?.focus(); }); }}>Cancel</Button>
          <Button variant="danger-soft" disabled={busy} onClick={() => void run(async () => {
            const failure = await onRemove(project.id);
            if (!active.current) return;
            if (failure) setError(failure); else onClose();
          })}>{busy ? 'Removing...' : 'Remove project'}</Button></div>
    </div>}
  </section>;
}
