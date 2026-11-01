import { useEffect, useId, useRef, useState } from 'react';
import { ArrowLeft, Check, Pencil, Plus, Search, X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { request, type Topic } from '../../engine';
import { topicColors } from './topics';
import './topic-dialog.css';
import { useTopicPage } from './useTopicPage';
import { bindDraftDiscard, flushDrafts, useEngineDraft } from '../drafts/draftStore';
import { IS_DESKTOP } from '../../platform';
import { beginLocalActivity } from '../engines/localActivities';

type TopicInput = Pick<Topic, 'name' | 'color'>;
type TopicSelection = {
  projectId?: string;
  topics: Topic[];
  onSelect: (id: string) => Promise<string | null>;
  onCreate: (input: TopicInput) => Promise<string | null>;
};

export function TopicDialog({ topic, selection, onSave, onClose, draftKey, projectId }: {
  projectId?: string;
  draftKey?: string;
  topic?: Topic;
  selection?: TopicSelection;
  onSave: (input: TopicInput) => Promise<string | null>;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const focusTarget = useRef<HTMLInputElement>(null);
  const submitting = useRef(false);
  const id = useId();
  const key = draftKey ?? `form.topic:${selection?.projectId ?? ''}:${topic?.id ?? 'new'}`;
  const forgetDrafts = bindDraftDiscard();
  const [baseTopic] = useEngineDraft<Topic | undefined>(`${key}.base`, topic, true);
  const [mode, setMode] = useEngineDraft<'choose' | 'create' | 'edit'>(`${key}.mode`, selection ? 'choose' : topic ? 'edit' : 'create');
  const [name, setName] = useEngineDraft(`${key}.${mode}.name`, mode === 'create' ? '' : topic?.name ?? '');
  const [color, setColor] = useEngineDraft<Topic['color']>(`${key}.${mode}.color`, mode === 'create' ? 'neutral' : topic?.color ?? 'neutral');
  const [query, setQuery] = useState('');
  const [selectedId, setSelectedId] = useEngineDraft(`${key}.selected`, topic?.id ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const choosing = mode === 'choose';
  const creating = mode === 'create';
  const title = choosing ? 'Choose topic' : creating ? 'New topic' : 'Edit topic';
  const page = useTopicPage(selection?.projectId, choosing, query);
  const [chosenTopic, setChosenTopic] = useEngineDraft<Topic | undefined>(`${key}.chosen`, topic);
  const candidates = selection?.projectId ? page.items : selection?.topics ?? [];
  const activeTopics = [...new Map([...candidates, ...(chosenTopic ? [chosenTopic] : [])].filter(item => !item.archived).map(item => [item.id, item])).values()];
  const matches = activeTopics.filter(item => item.name.toLowerCase().includes(query.trim().toLowerCase()));
  const canSubmit = choosing
    ? selectedId !== topic?.id && activeTopics.some(item => item.id === selectedId)
    : !!name.trim();

  useEffect(() => {
    const node = dialog.current;
    node?.showModal();
    return () => { node?.close(); };
  }, []);
  useEffect(() => { focusTarget.current?.focus(); }, [mode]);

  function changeMode(next: typeof mode) {
    setError('');
    if (!IS_DESKTOP && next === 'create') { setName(''); setColor('neutral'); }
    if (!IS_DESKTOP && next === 'edit') { setName(topic?.name ?? ''); setColor(topic?.color ?? 'neutral'); }
    setMode(next);
  }

  return <dialog ref={dialog} className="settings-dialog topic-dialog" aria-labelledby={`${id}-title`}
    onCancel={event => { event.preventDefault(); if (!submitting.current) onClose(); }}>
    <form onSubmit={async event => {
      event.preventDefault();
      if (submitting.current || !canSubmit) return;
      const finished = beginLocalActivity('Saving topic');
      submitting.current = true;
      setBusy(true); setError('');
      try {
        await flushDrafts();
        const owner = projectId ?? selection?.projectId;
        if (IS_DESKTOP && mode === 'edit' && owner && baseTopic) {
          const latest = await request<Topic>(`/api/projects/${encodeURIComponent(owner)}/topics/${encodeURIComponent(baseTopic.id)}`);
          if (topic?.id !== baseTopic.id || (baseTopic.revision !== undefined && String(latest.revision) !== String(baseTopic.revision))) throw new Error('Topic changed. Cancel this edit and reopen it to review the saved topic.');
        }
        const input = { name: name.trim(), color };
        const failure = choosing && selection ? await selection.onSelect(selectedId)
          : creating && selection ? await selection.onCreate(input) : await onSave(input);
        if (failure) setError(failure); else { forgetDrafts(key); onClose(); }
      } catch (failure) {
        setError(failure instanceof Error ? failure.message : 'Could not save the topic. Try again.');
      } finally { submitting.current = false; setBusy(false); finished(); }
    }}>
      <header className="topic-dialog-heading">
        {selection && !choosing && <IconButton label="Back to topics" disabled={busy} onClick={() => changeMode('choose')}><ArrowLeft aria-hidden="true" /></IconButton>}
        <h2 id={`${id}-title`}>{title}</h2>
        <IconButton label="Close topic dialog" disabled={busy} onClick={onClose}><X aria-hidden="true" /></IconButton>
      </header>
      <div className="topic-dialog-body">
        {choosing ? <>
          <div className="topic-search">
            <Search aria-hidden="true" />
            <input ref={focusTarget} type="search" aria-label="Search topics" placeholder="Search topics" value={query} disabled={busy} onChange={event => setQuery(event.target.value)} />
          </div>
          <fieldset className="topic-choices" disabled={busy}>
            <legend className="topic-visually-hidden">Existing topics</legend>
            {matches.map(item => <div key={item.id} className="topic-choice-row">
              <label className={`topic-choice topic-color-${item.color}`}>
                <input type="radio" name={`${id}-topic`} value={item.id} checked={selectedId === item.id} onChange={() => { setSelectedId(item.id); setChosenTopic(item); setError(''); }} />
                <span className="topic-color-swatch" aria-hidden="true" />
                <span className="topic-choice-name">{item.name}</span>
                {item.id === topic?.id && <span className="topic-current-label">Current</span>}
                <Check className="topic-choice-check" aria-hidden="true" />
              </label>
              {item.id === topic?.id && <IconButton label={`Edit topic ${item.name}`} disabled={busy} onClick={() => changeMode('edit')}><Pencil aria-hidden="true" /></IconButton>}
            </div>)}
            {!matches.length && <p className="topic-picker-empty" role="status">{page.loading ? 'Loading topics…' : query.trim() ? 'No matching topics' : 'No active topics'}</p>}
          </fieldset>
          {selection?.projectId && <div>{page.older && <Button disabled={busy || page.loading} size="sm" onClick={page.first}>First topics</Button>}{page.nextCursor && <Button disabled={busy || page.loading} size="sm" onClick={page.next}>Next topics</Button>}{page.error && <p role="alert" className="form-error">{page.error}<Button size="sm" onClick={page.first}>Reload topics</Button></p>}</div>}
          <Button className="topic-create-action" variant="ghost" disabled={busy} onClick={() => changeMode('create')}><Plus aria-hidden="true" />New topic</Button>
        </> : <>
          <div className="topic-name-field">
            <label htmlFor={`${id}-name`}>Name</label>
            <input className="input" ref={focusTarget} id={`${id}-name`} required maxLength={60} placeholder="Topic name" autoComplete="off" value={name} disabled={busy} onChange={event => setName(event.target.value)} />
          </div>
          <fieldset className="topic-palette" disabled={busy}>
            <legend>Color</legend>
            <div className="topic-palette-grid">
              {topicColors.map(value => <label key={value} className={`topic-palette-option topic-color-${value}`}>
                <input type="radio" name={`${id}-color`} value={value} checked={color === value} onChange={() => setColor(value)} />
                <span className="topic-palette-swatch"><Check aria-hidden="true" /></span>
                <span>{value[0].toUpperCase() + value.slice(1)}</span>
              </label>)}
            </div>
          </fieldset>
        </>}
        {error && <p role="alert" className="form-error topic-dialog-error">{error}</p>}
      </div>
      <footer className="topic-dialog-footer">
        <Button disabled={busy} variant="ghost" onClick={() => { forgetDrafts(key); onClose(); }}>Cancel</Button>
        <Button type="submit" variant="primary" disabled={busy || !canSubmit}>
          {busy ? 'Saving…' : choosing ? 'Apply topic' : creating ? selection ? 'Create and move' : 'Create topic' : 'Save changes'}
        </Button>
      </footer>
    </form>
  </dialog>;
}
