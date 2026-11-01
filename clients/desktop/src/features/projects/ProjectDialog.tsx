import { useEffect, useRef, useState } from 'react';
import { ImagePlus, X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { type Project } from '../../engine';
import { prepareProjectIcon } from './projectIcon';
import { DirectoryBrowser } from './DirectoryBrowser';
import { bindDraftDiscard, flushDrafts, useEngineDraft } from '../drafts/draftStore';
import { beginLocalActivity, useFilePickerActivity } from '../engines/localActivities';

export function ProjectDialog({ initialFolder, project, onSave, onClose }: {
  initialFolder: string; project?: Project;
  onSave: (project: Omit<Project, 'id' | 'folders' | 'archivedFolders'>) => Promise<string | null>; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const iconPicker = useFilePickerActivity();
  const key = `form.project:${project?.id ?? 'new'}`;
  const forgetDrafts = bindDraftDiscard();
  const [name, setName] = useEngineDraft(`${key}.name`, project?.name ?? initialFolder.split(/[\\/]/).filter(Boolean).pop() ?? '');
  const [folder, setFolder] = useEngineDraft(`${key}.folder`, initialFolder);
  const [icon, setIcon] = useEngineDraft(`${key}.icon`, project?.icon ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [browsing, setBrowsing] = useEngineDraft(`${key}.browsing`, false);
  const browseButton = useRef<HTMLDivElement>(null);
  const wasBrowsing = useRef(false);
  useEffect(() => {
    const opener = document.activeElement;
    dialog.current?.showModal();
    return () => { if (opener instanceof HTMLElement && opener.isConnected) opener.focus(); };
  }, []);
  useEffect(() => {
    if (wasBrowsing.current && !browsing) browseButton.current?.querySelector('button')?.focus();
    wasBrowsing.current = browsing;
  }, [browsing]);

  async function chooseIcon(file: File | undefined) {
    if (!file) return;
    const finished = beginLocalActivity('Preparing project icon');
    setError('');
    setBusy(true);
    try {
      setIcon(await prepareProjectIcon(file));
    } catch (error) { setError(error instanceof Error ? error.message : 'This image could not be opened. Please choose another image.'); }
    finally { setBusy(false); finished(); }
  }

  return <dialog ref={dialog} className={`settings-dialog add-project-dialog${browsing ? ' directory-browser-dialog' : ''}`} aria-labelledby="project-dialog-title" onCancel={event => {
    if (browsing) { event.preventDefault(); setBrowsing(false); }
    else if (busy) event.preventDefault(); else onClose();
  }} onClose={onClose}>
    {browsing ? <DirectoryBrowser draftKey={`${key}.directory`} initialPath={folder} onCancel={() => setBrowsing(false)} onSelect={selected => {
      setFolder(selected);
      setName(current => current.trim() ? current : selected.split(/[\\/]/).filter(Boolean).pop() || '');
      setBrowsing(false);
    }} /> : <form onSubmit={async event => {
      event.preventDefault();
      if (busy || !name.trim() || !folder.trim()) return;
      const finished = beginLocalActivity('Saving project');
      setBusy(true); setError('');
      try {
        await flushDrafts();
        const failure = await onSave({ name: name.trim(), folder: folder.trim(), icon });
        if (failure) setError(failure); else { forgetDrafts(key); onClose(); }
      } catch (error) { setError(error instanceof Error ? error.message : 'Could not save the project.'); }
      finally { setBusy(false); finished(); }
    }}>
      <div className="dialog-heading"><h2 id="project-dialog-title">{project ? 'Edit project' : 'Add project'}</h2><IconButton label={project ? 'Cancel editing project' : 'Cancel adding project'} disabled={busy} onClick={onClose}><X /></IconButton></div>
      <label className="project-field" htmlFor="project-picture">Project icon</label>
      <div className="project-picture-picker project-icon-trigger">
        <div className="project-picture-preview">{icon ? <img src={icon} alt="" /> : <ImagePlus aria-hidden="true" />}</div>
        <span className="button button--outline button--md">{icon ? 'Change icon' : 'Choose icon'}</span>
        <input id="project-picture" type="file" className="native-picker-overlay" aria-label={icon ? 'Change project icon' : 'Choose project icon'} accept="image/png,image/jpeg,image/webp" disabled={busy} onClick={iconPicker.open} onChange={event => { iconPicker.close(); void chooseIcon(event.target.files?.[0]); event.target.value = ''; }} />
      </div>
      <label className="project-field" htmlFor="project-name">Project name</label>
      <input id="project-name" required maxLength={60} disabled={busy} value={name} onChange={event => setName(event.target.value)} placeholder="e.g. My application" />
      {!project && <><label className="project-field" htmlFor="project-directory">Project directory</label>
      <div className="folder-picker project-directory-field" ref={browseButton}>
        <input id="project-directory" disabled={busy} value={folder} title={folder} onChange={event => setFolder(event.target.value)} />
        <Button disabled={busy} onClick={() => setBrowsing(true)}>Browse directories</Button>
      </div></>}
      {error && <p role="alert" className="form-error">{error}</p>}
      <div className="dialog-actions"><Button disabled={busy} onClick={() => { forgetDrafts(key); onClose(); }}>Cancel</Button><Button type="submit" variant="primary" disabled={busy || !folder.trim() || !name.trim()}>{busy ? 'Please wait...' : project ? 'Save changes' : 'Create project'}</Button></div>
    </form>}
  </dialog>;
}
