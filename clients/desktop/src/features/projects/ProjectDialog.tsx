import { useEffect, useRef, useState } from 'react';
import { ImagePlus, X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { pickDirectory, type Project } from '../../engine';
import { prepareProjectIcon } from './projectIcon';

export function ProjectDialog({ initialFolder, project, onSave, onClose }: {
  initialFolder: string; project?: Project;
  onSave: (project: Omit<Project, 'id' | 'folders' | 'archivedFolders'>) => Promise<string | null>; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [name, setName] = useState(project?.name ?? initialFolder.split(/[\\/]/).filter(Boolean).pop() ?? '');
  const [folder, setFolder] = useState(initialFolder);
  const [icon, setIcon] = useState(project?.icon ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [picking, setPicking] = useState(false);
  const picker = useRef<AbortController | null>(null);
  useEffect(() => () => { picker.current?.abort(); }, []);

  async function chooseDirectory() {
    if (picker.current) return;
    const controller = new AbortController();
    picker.current = controller;
    setPicking(true); setError('');
    try {
      const selected = await pickDirectory(controller.signal);
      if (selected && !controller.signal.aborted) {
        setFolder(selected);
        setName(current => current || selected.split(/[\\/]/).filter(Boolean).pop() || '');
      }
    } catch (error) {
      if (!controller.signal.aborted) setError(error instanceof Error ? error.message : 'Could not open the directory picker.');
    } finally {
      if (picker.current === controller) {
        picker.current = null;
        setPicking(false);
      }
    }
  }
  useEffect(() => { dialog.current?.showModal(); }, []);

  async function chooseIcon(file: File | undefined) {
    if (!file) return;
    setError('');
    setBusy(true);
    try {
      setIcon(await prepareProjectIcon(file));
    } catch (error) { setError(error instanceof Error ? error.message : 'This image could not be opened. Please choose another image.'); }
    finally { setBusy(false); }
  }

  return <dialog ref={dialog} className="settings-dialog add-project-dialog" aria-labelledby="project-dialog-title" onCancel={event => { if (busy) event.preventDefault(); else onClose(); }} onClose={onClose}>
    <form onSubmit={async event => {
      event.preventDefault();
      if (busy || picking || !name.trim() || !folder.trim()) return;
      setBusy(true); setError('');
      try {
        const failure = await onSave({ name: name.trim(), folder: folder.trim(), icon });
        if (failure) setError(failure); else onClose();
      } catch (error) { setError(error instanceof Error ? error.message : 'Could not save the project.'); }
      finally { setBusy(false); }
    }}>
      <div className="dialog-heading"><h2 id="project-dialog-title">{project ? 'Edit project' : 'Add project'}</h2><IconButton label={project ? 'Cancel editing project' : 'Cancel adding project'} disabled={busy} onClick={onClose}><X /></IconButton></div>
      <label className="project-field" htmlFor="project-picture">Project icon</label>
      <div className="project-picture-picker project-icon-trigger">
        <div className="project-picture-preview">{icon ? <img src={icon} alt="" /> : <ImagePlus aria-hidden="true" />}</div>
        <span className="button button--outline button--md">{icon ? 'Change icon' : 'Choose icon'}</span>
        <input id="project-picture" type="file" className="native-picker-overlay" aria-label={icon ? 'Change project icon' : 'Choose project icon'} accept="image/png,image/jpeg,image/webp" disabled={busy} onChange={event => { void chooseIcon(event.target.files?.[0]); event.target.value = ''; }} />
      </div>
      <label className="project-field" htmlFor="project-name">Project name</label>
      <input id="project-name" required maxLength={60} disabled={busy} value={name} onChange={event => setName(event.target.value)} placeholder="e.g. My application" />
      {!project && <><label className="project-field" htmlFor="project-directory">Project directory</label>
      <div className="folder-picker">
        <input id="project-directory" disabled={busy || picking} value={folder} title={folder} onChange={event => setFolder(event.target.value)} />
        <Button disabled={busy || picking} onClick={() => void chooseDirectory()}>{picking ? 'Choosing directory...' : folder ? 'Change directory' : 'Choose directory'}</Button>
        {picking && <Button onClick={() => picker.current?.abort()}>Cancel selection</Button>}
      </div></>}
      {error && <p role="alert" className="form-error">{error}</p>}
      <div className="dialog-actions"><Button disabled={busy} onClick={onClose}>Cancel</Button><Button type="submit" variant="primary" disabled={busy || picking || !folder.trim() || !name.trim()}>{busy ? 'Please wait...' : project ? 'Save changes' : 'Create project'}</Button></div>
    </form>
  </dialog>;
}
