import { useEffect, useRef, useState } from 'react';
import { ListTodo, Settings, Webhook, X, Zap } from 'lucide-react';
import { AutomationsSettings } from '../automations/AutomationsSettings';
import { Button, IconButton } from '../../design-system/Button';
import type { Project, Session } from '../../engine';
import { ProjectGeneralSettings } from './ProjectGeneralSettings';
import { WebhooksSettings } from '../webhooks/WebhooksSettings';
import { ProjectTaskConnections } from '../activity-board/ProjectTaskConnections';
import type { ActivityBoardState } from '../activity-board/useActivityBoard';
import './project-settings.css';
import { useEngineDraft } from '../drafts/draftStore';

export type ProjectSettingsSection = 'general' | 'webhooks' | 'tasks' | 'automations';

export function ProjectSettingsDialog({ project, sessions, onSave, onRemove, onClose, onOpenSession, tasks, initialSection = 'general' }: {
  project: Project;
  sessions: Session[];
  onSave: (id: string, input: Pick<Project, 'name' | 'icon'>) => Promise<string | null>;
  onRemove: (id: string) => Promise<string | null>;
  onClose: () => void;
  onOpenSession: (id: string) => void | Promise<void>;
  tasks: ActivityBoardState;
  initialSection?: ProjectSettingsSection;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const [section, setSection] = useEngineDraft<ProjectSettingsSection>(`ui.projectSettingsSection:${project.id}`, initialSection);
  const [busy, setBusy] = useState(false);
  const [sessionError, setSessionError] = useState('');
  async function openSession(id: string) {
    setSessionError('');
    try { await onOpenSession(id); }
    catch (error) { setSessionError(error instanceof Error ? error.message : 'Could not open session.'); }
  }
  useEffect(() => {
    const element = dialog.current;
    if (!returnFocus.current) returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (element && !element.open) element.showModal();
    return () => { if (!element?.isConnected && returnFocus.current?.isConnected) returnFocus.current.focus(); };
  }, []);

  return <dialog ref={dialog} className="settings-dialog app-settings-dialog project-settings-dialog" aria-labelledby="project-settings-title" onCancel={event => { event.preventDefault(); if (!busy) onClose(); }} onClose={onClose}>
    <div className="dialog-heading app-settings-heading"><h2 id="project-settings-title">Project settings</h2><IconButton label="Close project settings" disabled={busy} onClick={onClose}><X /></IconButton></div>
    <div className="app-settings-layout">
      <nav className="settings-sidebar" aria-label="Project settings sections">
        <button type="button" disabled={busy} className={section === 'general' ? 'is-active' : ''} aria-current={section === 'general' ? 'page' : undefined} onClick={() => setSection('general')}><Settings />General</button>
        <button type="button" disabled={busy} className={section === 'webhooks' ? 'is-active' : ''} aria-current={section === 'webhooks' ? 'page' : undefined} onClick={() => setSection('webhooks')}><Webhook />Webhooks</button>
        <button type="button" disabled={busy} className={section === 'tasks' ? 'is-active' : ''} aria-current={section === 'tasks' ? 'page' : undefined} onClick={() => setSection('tasks')}><ListTodo />Tasks</button>
        <button type="button" disabled={busy} className={section === 'automations' ? 'is-active' : ''} aria-current={section === 'automations' ? 'page' : undefined} onClick={() => setSection('automations')}><Zap />Automations</button>
      </nav>
      <div className="settings-panel">
        {sessionError && <p className="form-error" role="alert">{sessionError}</p>}
        {section === 'automations' ? <AutomationsSettings project={project} sessions={sessions} onOpenSession={id => void openSession(id)} onBusyChange={setBusy} /> : section === 'general' ? <ProjectGeneralSettings key={project.id} project={project} onSave={onSave} onRemove={onRemove} onClose={onClose} onBusyChange={setBusy} /> : section === 'webhooks' ? <WebhooksSettings key={project.id} projectId={project.id} onBusyChange={setBusy} /> : <>
          {tasks.error && <p className="form-error" role="alert">{tasks.error}<Button size="sm" onClick={() => void tasks.load()}>Retry</Button></p>}
          {tasks.config ? <ProjectTaskConnections key={project.id} project={project} config={tasks.config} onSaved={tasks.setConfig} onBusyChange={setBusy} /> : !tasks.error && <p role="status">Loading…</p>}
        </>}
      </div>
    </div>
  </dialog>;
}
