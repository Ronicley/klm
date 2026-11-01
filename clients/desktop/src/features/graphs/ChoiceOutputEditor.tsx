import { useContext, useId, useRef, useState } from 'react';
import { Braces, Plus, Trash2 } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { TokenTextarea } from '../../design-system/TokenTextarea';
import { Menu, MenuItem } from '../../design-system/Menu';
import { normalizeChoiceName, type ChoiceOutputField } from './choice';
import { useEngineDraft } from '../drafts/draftStore';
import { GraphDraftScope } from './draftScope';

function OutputField({ field, variables, payloadContext, onChange, onRemove }: {
  field: ChoiceOutputField;
  variables: string[];
  payloadContext?: boolean;
  onChange: (field: ChoiceOutputField) => void;
  onRemove: () => void;
}) {
  const id = useId();
  const [menuOpen, setMenuOpen] = useState(false);
  const scope = useContext(GraphDraftScope);
  const [payloadField, setPayloadField] = useEngineDraft(`${scope}.payloadField:${field.key}`, '');
  const selection = useRef<{ start: number; end: number } | null>(null);
  const tokens = [...field.value.matchAll(/\{\{\s*[^{}]+?\s*\}\}/g)].map(match => ({ start: match.index, end: match.index + match[0].length }));

  function insertVariable(variable: string) {
    const token = `{{${variable}}}`;
    const { start, end } = selection.current ?? { start: field.value.length, end: field.value.length };
    onChange({ ...field, value: field.value.slice(0, start) + token + field.value.slice(end) });
    setMenuOpen(false);
    requestAnimationFrame(() => {
      const input = document.getElementById(id) as HTMLTextAreaElement | null;
      input?.focus(); input?.setSelectionRange(start + token.length, start + token.length);
    });
  }

  return <div className="graph-choice-output-field">
    <div className="graph-choice-output-heading">
      <Input aria-label="Output field name" placeholder="Field name" required maxLength={80} pattern="[a-z0-9_]+" autoCapitalize="none" autoCorrect="off" spellCheck={false} value={field.name} onChange={event => onChange({ ...field, name: normalizeChoiceName(event.target.value) })} onBlur={() => onChange({ ...field, name: normalizeChoiceName(field.name, true) })} />
      <Menu label="Variables" role="menu" className="graph-choice-variable-menu" side="bottom" open={menuOpen} onOpenChange={setMenuOpen} trigger={props => <IconButton {...props} label="Insert variable"><Braces /></IconButton>}>
        {variables.map(variable => <MenuItem key={variable} role="menuitem" onClick={() => insertVariable(variable)}><code>{`{{${variable}}}`}</code></MenuItem>)}
        {payloadContext && <div className="graph-agent-panel-field">
          <Input aria-label="Payload field" placeholder="Payload field" value={payloadField} onChange={event => setPayloadField(normalizeChoiceName(event.target.value))} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); event.stopPropagation(); const name = normalizeChoiceName(payloadField, true); if (name) insertVariable(`payload.${name}`); } }} />
          <Button size="sm" disabled={!normalizeChoiceName(payloadField, true)} onClick={() => insertVariable(`payload.${normalizeChoiceName(payloadField, true)}`)}>Insert payload field</Button>
        </div>}
      </Menu>
      <IconButton label={`Remove output field ${field.name || ''}`.trim()} onClick={onRemove}><Trash2 /></IconButton>
    </div>
    <TokenTextarea id={id} aria-label={`Value for ${field.name || 'output field'}`} placeholder="Value" rows={2} className="graph-choice-output-value" value={field.value} tokens={tokens} spellCheck={false} onChange={event => onChange({ ...field, value: event.target.value })} onSelect={event => { selection.current = { start: event.currentTarget.selectionStart, end: event.currentTarget.selectionEnd }; }} />
  </div>;
}

export function ChoiceOutputEditor({ fields, variables, payloadContext, onChange }: {
  fields: ChoiceOutputField[];
  variables: string[];
  payloadContext?: boolean;
  onChange: (fields: ChoiceOutputField[]) => void;
}) {
  return <div className="graph-choice-payload">
    <div className="graph-agent-overrides-header"><h3>Output payload</h3><Button size="sm" variant="ghost" onClick={() => onChange([...fields, { key: crypto.randomUUID(), name: '', value: '' }])}><Plus />Add field</Button></div>
    {fields.map(field => <OutputField key={field.key} field={field} variables={variables} payloadContext={payloadContext} onChange={next => onChange(fields.map(item => item.key === field.key ? next : item))} onRemove={() => onChange(fields.filter(item => item.key !== field.key))} />)}
  </div>;
}
