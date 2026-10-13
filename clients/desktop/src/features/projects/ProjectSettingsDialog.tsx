import { useEffect, useRef, useState } from 'react';
import { Settings, Webhook, X } from 'lucide-react';
import { IconButton } from '../../design-system/Button';
import type { Project } from '../../engine';
import { ProjectGeneralSettings } from './ProjectGeneralSettings';
import { WebhooksSettings } from '../webhooks/WebhooksSettings';
import './project-settings.css';

export function ProjectSettingsDialog({ project, onSave, onRemove, onClose }: {
  project: Project;
  onSave: (id: string, input: Pick<Project, 'name' | 'icon'>) => Promise<string | null>;
  onRemove: (id: string) => Promise<string | null>;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const [section, setSection] = useState<'general' | 'webhooks'>('general');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const element = dialog.current;
    if (!returnFocus.current) returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (element && !element.open) element.showModal();
    return () => { if (!element?.isConnected && returnFocus.current?.isConnected) returnFocus.current.focus(); };
  }, []);

  return <dialog ref={dialog} className="settings-dialog app-settings-dialog project-settings-dialog" aria-labelledby="project-settings-title" onCancel={event => { event.preventDefault(); onClose(); }} onClose={onClose}>
    <div className="dialog-heading app-settings-heading"><h2 id="project-settings-title">Project settings</h2><IconButton label="Close project settings" onClick={onClose}><X /></IconButton></div>
    <div className="app-settings-layout">
      <nav className="settings-sidebar" aria-label="Project settings sections">
        <button type="button" disabled={busy} className={section === 'general' ? 'is-active' : ''} aria-current={section === 'general' ? 'page' : undefined} onClick={() => setSection('general')}><Settings />General</button>
        <button type="button" disabled={busy} className={section === 'webhooks' ? 'is-active' : ''} aria-current={section === 'webhooks' ? 'page' : undefined} onClick={() => setSection('webhooks')}><Webhook />Webhooks</button>
      </nav>
      <div className="settings-panel">
        {section === 'general' ? <ProjectGeneralSettings key={project.id} project={project} onSave={onSave} onRemove={onRemove} onClose={onClose} onBusyChange={setBusy} /> : <WebhooksSettings key={project.id} projectId={project.id} onBusyChange={setBusy} />}
      </div>
    </div>
  </dialog>;
}
