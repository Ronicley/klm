import { useContext, useId } from 'react';
import { Input } from '../../design-system/Input';
import { Textarea } from '../../design-system/Textarea';
import { previewOutput, type OutputContext, type ChoiceOutputField, type ChoicePayloadField } from './choice';
import { useEngineDraft } from '../drafts/draftStore';
import { GraphDraftScope } from './draftScope';

export function ChoiceOutputPreview({ inputFields, outputFields, context = 'choice' }: { inputFields: ChoicePayloadField[]; outputFields: ChoiceOutputField[]; context?: OutputContext }) {
  const id = useId();
  const key = `${useContext(GraphDraftScope)}.preview`;
  const [task, setTask] = useEngineDraft(`${key}.task`, '');
  const [commandResult, setCommandResult] = useEngineDraft(`${key}.result`, '');
  const [inputText, setInputText] = useEngineDraft(`${key}.input`, () => JSON.stringify(Object.fromEntries(inputFields.filter(field => field.required).map(field => [field.name.trim(), ''])), null, 2));
  let result: ReturnType<typeof previewOutput>;
  try { result = previewOutput(context, inputFields, outputFields, JSON.parse(inputText), task, context === 'terminal' ? commandResult : undefined); }
  catch { result = { output: null, error: 'Enter valid JSON for input values.' }; }

  return <details className="graph-choice-output-preview">
    <summary>Preview JSON</summary>
    <div className="graph-choice-preview-content">
      <div className="graph-agent-panel-field"><label htmlFor={`${id}-task`}>Task</label><Input id={`${id}-task`} value={task} onChange={event => setTask(event.target.value)} /></div>
      <div className="graph-agent-panel-field"><label htmlFor={`${id}-input`}>Input values</label><Textarea id={`${id}-input`} className="graph-choice-json-input" rows={4} value={inputText} spellCheck={false} onChange={event => setInputText(event.target.value)} /></div>
      {context === 'terminal' && <div className="graph-agent-panel-field"><label htmlFor={`${id}-result`}>Command result</label><Textarea id={`${id}-result`} rows={3} value={commandResult} onChange={event => setCommandResult(event.target.value)} /></div>}
      {result.error ? <p className="form-error" role="status">{result.error}</p> : <pre className="graph-choice-json-preview" aria-label="Output JSON"><code>{JSON.stringify(result.output, null, 2)}</code></pre>}
    </div>
  </details>;
}
