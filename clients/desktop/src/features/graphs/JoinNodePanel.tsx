import { useContext, useEffect, useId } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { SearchSelect } from '../../design-system/SearchSelect';
import { Textarea } from '../../design-system/Textarea';
import type { Agent } from '../agents/types';
import { gitBranchNameError } from './fork';
import type { JoinDefinition } from './join';
import { forgetDrafts, useEngineDraft } from '../drafts/draftStore';
import { GraphDraftScope } from './draftScope';

export function JoinNodePanel({ join, agents, onSave, onClose }: {
  join: JoinDefinition;
  agents: Agent[];
  onSave: (join: JoinDefinition) => void;
  onClose: () => void;
}) {
  const id = useId();
  const key = useContext(GraphDraftScope);
  const [draft, setDraft, clearDraft] = useEngineDraft(`${key}.join`, join);
  const available = agents.filter(agent => agent.enabled);
  const branchError = draft.outputBranch ? gitBranchNameError(draft.outputBranch) : '';
  const valid = available.some(agent => agent.id === draft.agentId) && !branchError;
  useEffect(() => { document.getElementById(`${id}-agent`)?.focus(); }, [id]);

  return <aside className="graph-agent-panel nodrag nopan nowheel" role="dialog" aria-labelledby={`${id}-title`} onKeyDown={event => {
    if (event.key === 'Escape' && !event.defaultPrevented) { event.stopPropagation(); onClose(); }
  }}>
    <form className="graph-choice-form" onSubmit={event => { event.preventDefault(); if (valid) { onSave({ ...draft, prompt: draft.prompt.trim() }); clearDraft(draft); } }}>
      <div className="graph-agent-panel-header"><h2 id={`${id}-title`}>Join</h2><IconButton label="Close join settings" onClick={onClose}><X /></IconButton></div>
      <div className="graph-agent-panel-field"><label htmlFor={`${id}-agent`}>Agent</label><SearchSelect id={`${id}-agent`} label="Agent" value={draft.agentId} options={available.map(agent => ({ value: agent.id, label: agent.name, description: agent.description }))} onChange={agentId => setDraft(current => ({ ...current, agentId }))} placeholder="Select agent" searchPlaceholder="Search agents" emptyMessage={available.length ? 'No agents found' : 'No enabled agents available'} /></div>
      <div className="graph-agent-panel-field"><label htmlFor={`${id}-prompt`}>Additional prompt <span className="graph-optional-label">Optional</span></label><Textarea id={`${id}-prompt`} rows={5} value={draft.prompt} onChange={event => setDraft(current => ({ ...current, prompt: event.target.value }))} /></div>
      <div className="graph-agent-panel-field"><label htmlFor={`${id}-branch`}>Output Git branch <span className="graph-optional-label">Optional</span></label><Input id={`${id}-branch`} placeholder="feature/integrated" autoCapitalize="none" autoCorrect="off" spellCheck={false} value={draft.outputBranch} aria-invalid={!!branchError} aria-describedby={branchError ? `${id}-branch-error` : undefined} onChange={event => setDraft(current => ({ ...current, outputBranch: event.target.value }))} />{branchError && <p id={`${id}-branch-error`} className="form-error" role="alert">{branchError}</p>}</div>
      <div className="graph-choice-form-actions"><Button onClick={() => { forgetDrafts(key); onClose(); }}>Cancel</Button><Button type="submit" variant="primary" disabled={!valid}>Apply</Button></div>
    </form>
  </aside>;
}
