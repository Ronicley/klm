import { useRef, useState } from 'react';
import { MessageCircle } from 'lucide-react';
import { Button } from '../../design-system/Button';
import { request, type QuestionRequest, type SessionResponse } from '../../engine';
import { MarkdownContent } from './ChatMessage';
import { PlanReader } from '../graphs/PlanReader';
import { MCPElicitationCard } from '../library/MCPElicitationCard';
export function QuestionCard({ question, sessionId, origin, onResolved }: {
  question: QuestionRequest; sessionId: string; origin?: string; onResolved: (session: SessionResponse) => void;
}) {
  if (question.elicitation) return <MCPElicitationCard question={question} sessionId={sessionId} origin={origin} onResolved={onResolved} />;
  return <NativeQuestionCard question={question} sessionId={sessionId} origin={origin} onResolved={onResolved} />;
}

function NativeQuestionCard({ question, sessionId, origin, onResolved }: { question: QuestionRequest; sessionId: string; origin?: string; onResolved: (session: SessionResponse) => void }) {
  const [responses, setResponses] = useState(() => question.items.map(() => ({ selected: [] as string[], custom: '' })));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const [reading, setReading] = useState<{ title: string; text: string } | null>(null);
  const pending = useRef(false);
  const busy = submitting || question.resolving;
  const answers = responses.map((response, index) => [...new Set([
    ...response.selected,
    ...(response.custom.trim() ? [question.items[index].secret ? response.custom : response.custom.trim()] : []),
  ])]);
  const ready = answers.every((answer, index) => answer.length > 0 && (question.items[index].multiple || answer.length === 1));

  async function submit(cancelled = false) {
    if (pending.current || question.resolving || (!cancelled && !ready)) return;
    pending.current = true; setSubmitting(true); setError('');
    try {
      const snapshot = await request<SessionResponse>(`/api/sessions/${encodeURIComponent(sessionId)}/questions/${encodeURIComponent(question.id)}/reply`, 'POST', cancelled ? { cancelled: true } : { answers });
      onResolved(snapshot);
    } catch (error) { setError(error instanceof Error ? error.message : 'Could not send the answer. Please retry.'); }
    finally { pending.current = false; setSubmitting(false); }
  }

  const harness = question.harness === 'opencode' ? 'OpenCode' : question.harness === 'pi' ? 'Pi' : 'Codex';
  return <form className="permission-card question-card" aria-label={`${harness} question`} onSubmit={event => { event.preventDefault(); void submit(); }}>
    <div className="permission-heading"><MessageCircle aria-hidden="true" /><strong>Your input</strong><span>{harness}</span></div>
    {origin && <p className="permission-description">{origin}</p>}
    {question.context && <Button size="sm" variant="soft" onClick={() => setReading({ title: origin || 'Node context', text: question.context! })}>Read node context</Button>}
    {question.items.map((item, index) => <fieldset key={item.id} className="question-item" disabled={busy}>
      <legend>{item.header || `Question ${index + 1}`}{item.multiple && <span className="muted"> · Select one or more</span>}</legend>
      <div className="question-prompt"><MarkdownContent text={item.text} />{item.description && <MarkdownContent text={item.description} />}</div>
      {!item.secret && <Button size="sm" variant="ghost" onClick={() => setReading({ title: item.header || 'Question', text: [item.text, item.description].filter(Boolean).join('\n\n') })}>Open reader</Button>}
      {item.options.length > 0 && <div className="question-options">{item.options.map(option => <Button key={option.label} className="question-choice" variant={responses[index].selected.includes(option.label) ? 'soft' : 'outline'} aria-pressed={responses[index].selected.includes(option.label)} onClick={() => setResponses(current => current.map((response, i) => i !== index ? response : {
        selected: item.multiple ? (response.selected.includes(option.label) ? response.selected.filter(label => label !== option.label) : [...response.selected, option.label]) : [option.label],
        custom: item.multiple ? response.custom : '',
      }))}><strong>{option.label}</strong>{option.description && <span>{option.description}</span>}</Button>)}</div>}
      {(item.custom || item.options.length === 0) && (item.secret ? <input
        className="question-answer" type="password" autoComplete="off" aria-label={`Answer: ${item.header || item.text}`} placeholder="Your answer" maxLength={16384} value={responses[index].custom}
        onChange={event => setResponses(current => current.map((response, i) => i !== index ? response : { selected: item.multiple ? response.selected : [], custom: event.target.value }))}
      /> : <textarea
        className="question-answer" aria-label={`Answer: ${item.header || item.text}`} placeholder={item.options.length ? 'Or write an answer' : 'Your answer'} rows={2} maxLength={16384} value={responses[index].custom}
        onChange={event => setResponses(current => current.map((response, i) => i !== index ? response : { selected: item.multiple ? response.selected : [], custom: event.target.value }))}
      />)}
    </fieldset>)}
    <div className="permission-actions"><Button type="submit" variant="primary" size="sm" disabled={busy || !ready}>{submitting ? 'Sending...' : 'Send answer'}</Button><Button variant="ghost" size="sm" disabled={busy} onClick={() => void submit(true)}>Dismiss</Button></div>
    {error && <p className="form-error" role="alert">{error}</p>}
    {reading && <PlanReader title={reading.title} text={reading.text} onClose={() => setReading(null)} />}
  </form>;
}
