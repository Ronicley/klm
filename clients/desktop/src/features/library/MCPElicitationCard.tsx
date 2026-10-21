import { useState } from 'react';
import { Button } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Select } from '../../design-system/Select';
import { request, type QuestionRequest, type SessionResponse } from '../../engine';
import { IS_MOBILE_HOST } from '../../platform';
import './library.css';

type Field = { type?: string; title?: string; description?: string; format?: string; enum?: unknown[]; enumNames?: string[]; oneOf?: { const: string; title?: string }[]; default?: unknown; minimum?: number; maximum?: number; minLength?: number; maxLength?: number; items?: Field };
const fieldOptions = (field: Field) => field.oneOf?.map(value => ({ value: value.const, label: value.title ?? value.const })) ?? field.enum?.map((value, i) => ({ value: String(value), label: field.enumNames?.[i] ?? String(value) })) ?? [];

export function MCPElicitationCard({ question, sessionId, origin, onResolved }: { question: QuestionRequest; sessionId: string; origin?: string; onResolved: (session: SessionResponse) => void }) {
  const elicitation = question.elicitation!;
  const properties = (elicitation.schema?.properties ?? {}) as Record<string, Field>;
  const required = (elicitation.schema?.required ?? []) as string[];
  const [values, setValues] = useState<Record<string, unknown>>(() => Object.fromEntries(Object.entries(properties).filter(([, field]) => field.default !== undefined).map(([name, field]) => [name, field.default])));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const update = (name: string, value: unknown) => setValues(current => ({ ...current, [name]: value }));
  async function reply(action: 'accept' | 'decline' | 'cancel') {
    if (busy) return;
    setBusy(true); setError('');
    try {
      const content: Record<string, unknown> = {};
      if (action === 'accept' && elicitation.mode === 'form') for (const [name, field] of Object.entries(properties)) {
        const value = values[name];
        if (value === undefined || value === '' && !required.includes(name)) continue;
        content[name] = field.type === 'number' || field.type === 'integer' ? Number(value) : value;
      }
      onResolved(await request<SessionResponse>(`/api/sessions/${encodeURIComponent(sessionId)}/questions/${encodeURIComponent(question.id)}/elicitation`, 'POST', { action, ...(action === 'accept' && elicitation.mode === 'form' ? { content } : {}) }));
    } catch (reason) { setError(reason instanceof Error ? reason.message : 'Could not deliver elicitation response.'); } finally { setBusy(false); }
  }
  return <form className="permission-card question-card" aria-label="MCP elicitation" onSubmit={event => { event.preventDefault(); void reply('accept'); }}>
    <div className="permission-heading"><strong>{elicitation.connectionName}</strong><span>Your input</span></div>
    {origin && <p className="permission-description">{origin}</p>}
    <p className="mcp-form-message">{elicitation.message}</p>
    {elicitation.mode === 'url' ? <p><a href={elicitation.url} target={IS_MOBILE_HOST ? '_self' : '_blank'} rel="noreferrer">Open requested page</a></p> : <fieldset className="mcp-form-fields" disabled={busy}>
      {Object.entries(properties).map(([name, field]) => {
        const options = fieldOptions(field.type === 'array' ? field.items ?? {} : field);
        return <label key={name}>{field.title ?? name}{required.includes(name) ? ' *' : ''}
          {field.type === 'boolean' ? <Select label={field.title ?? name} value={values[name] === undefined ? '' : String(values[name])} required={required.includes(name)} onChange={event => update(name, event.target.value === '' ? undefined : event.target.value === 'true')}><option value="">Select</option><option value="true">Yes</option><option value="false">No</option></Select>
            : options.length ? <Select label={field.title ?? name} multiple={field.type === 'array'} required={required.includes(name)} value={field.type === 'array' ? (values[name] ?? []) as string[] : String(values[name] ?? '')} onChange={event => update(name, field.type === 'array' ? [...event.target.selectedOptions].map(option => option.value) : event.target.value)}>{field.type !== 'array' && <option value="">Select</option>}{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</Select>
            : <Input required={required.includes(name)} type={field.type === 'number' || field.type === 'integer' ? 'number' : field.format === 'email' ? 'email' : 'text'} step={field.type === 'integer' ? 1 : 'any'} min={field.minimum} max={field.maximum} minLength={field.minLength} maxLength={Math.min(field.maxLength ?? 16384, 16384)} value={String(values[name] ?? '')} onChange={event => update(name, event.target.value)} />}
          {field.description && <span className="muted">{field.description}</span>}
        </label>;
      })}
    </fieldset>}
    <div className="permission-actions"><Button type="submit" variant="primary" disabled={busy || question.resolving}>Accept</Button><Button disabled={busy} onClick={() => void reply('decline')}>Decline</Button><Button variant="ghost" disabled={busy} onClick={() => void reply('cancel')}>Cancel</Button></div>
    {error && <p className="form-error" role="alert">{error}</p>}
  </form>;
}
