import { useContext, useId } from 'react';
import { X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Textarea } from '../../design-system/Textarea';
import type { TerminalDefinition } from './terminal';
import { ChoiceOutputEditor } from './ChoiceOutputEditor';
import { ChoiceOutputPreview } from './ChoiceOutputPreview';
import { normalizeChoiceName, outputMappingError, outputVariables } from './choice';
import { forgetDrafts, useEngineDraft } from '../drafts/draftStore';
import { GraphDraftScope } from './draftScope';

export function TerminalNodePanel({ terminal, payloadFields = [], onSave, onClose }: {
  terminal: TerminalDefinition;
  payloadFields?: string[];
  onSave: (terminal: TerminalDefinition) => void;
  onClose: () => void;
}) {
  const id = useId();
  const key = useContext(GraphDraftScope);
  const [draft, setDraft, clearDraft] = useEngineDraft(`${key}.terminal`, () => ({ ...terminal, outputFields: terminal.outputFields ?? [] }));
  const outputError = outputMappingError(draft.outputFields, 'terminal');
  const valid = !!draft.name.trim() && !!draft.command.trim() && !outputError;

  return <aside className="graph-agent-panel nodrag nopan nowheel" role="dialog" aria-labelledby={`${id}-title`} onKeyDown={event => {
    if (event.key === 'Escape' && !event.defaultPrevented) { event.stopPropagation(); onClose(); }
  }}>
    <form className="graph-choice-form" onSubmit={event => {
      event.preventDefault();
      if (valid) { onSave({ name: draft.name.trim(), command: draft.command, outputFields: draft.outputFields.map(field => ({ ...field, name: normalizeChoiceName(field.name, true) })) }); clearDraft(draft); }
    }}>
      <div className="graph-agent-panel-header"><h2 id={`${id}-title`}>Terminal command</h2><IconButton label="Close terminal settings" onClick={onClose}><X /></IconButton></div>
      <div className="graph-agent-panel-field"><label htmlFor={`${id}-name`}>Name</label><Input id={`${id}-name`} autoFocus required maxLength={80} placeholder="Update repository" value={draft.name} onChange={event => setDraft(current => ({ ...current, name: event.target.value }))} /></div>
      <div className="graph-agent-panel-field"><label htmlFor={`${id}-command`}>Command</label><Textarea id={`${id}-command`} className="graph-terminal-command-input" required rows={7} placeholder="git pull" spellCheck={false} autoCapitalize="none" autoCorrect="off" value={draft.command} onChange={event => setDraft(current => ({ ...current, command: event.target.value }))} /></div>
      <ChoiceOutputEditor fields={draft.outputFields} variables={outputVariables('terminal', payloadFields)} payloadContext onChange={outputFields => setDraft(current => ({ ...current, outputFields }))} />
      {outputError && <p className="form-error" role="alert">{outputError}</p>}
      {!outputError && draft.outputFields.length > 0 && <ChoiceOutputPreview context="terminal" inputFields={[]} outputFields={draft.outputFields} />}
      <div className="graph-choice-form-actions"><Button onClick={() => { forgetDrafts(key); onClose(); }}>Cancel</Button><Button type="submit" variant="primary" disabled={!valid}>Apply</Button></div>
    </form>
  </aside>;
}
