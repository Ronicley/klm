import { ArrowUp, FileText, Folder, ListPlus, LoaderCircle, Mic, Square } from 'lucide-react';
import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { Button, IconButton } from '../../design-system/Button';
import { request, transcribeAudio, type FileMention, type ProjectPath } from '../../engine';
import { mentionQuery } from './mentions';
import { MentionEditor, type MentionEditorHandle } from './MentionEditor';

export type SendMode = 'queue' | 'steer';

export type ComposerDraft = { text: string; mentions: FileMention[] };

type AudioState = 'idle' | 'recording' | 'transcribing';

const recordingTypes = ['audio/webm;codecs=opus', 'audio/webm', 'audio/mp4', 'audio/ogg;codecs=opus'];

function recordingFilename(type: string) {
  if (type.startsWith('audio/mp4')) return 'recording.mp4';
  if (type.startsWith('audio/ogg')) return 'recording.ogg';
  return 'recording.webm';
}

export function MessageComposer({ onSend, onTranscription, draft, onDraftChange, projectId, disabled = false, running = false, onStop, modelControl, graphControl, placeholder = 'Message the agent' }: {
  onSend: (draft: ComposerDraft, mode?: SendMode) => boolean | void; draft: ComposerDraft; onDraftChange: (draft: ComposerDraft) => void;
  onTranscription?: (text: string) => void;
  projectId?: string;
  disabled?: boolean; running?: boolean; onStop?: () => void;
  modelControl?: ReactNode;
  graphControl?: ReactNode;
  placeholder?: string;
}) {
  const { text, mentions } = draft;
  const [caret, setCaret] = useState(0);
  const [focused, setFocused] = useState(false);
  const [composing, setComposing] = useState(false);
  const [dismissed, setDismissed] = useState('');
  const [active, setActive] = useState(0);
  const listId = useId();
  const submittedDraft = useRef<ComposerDraft | null>(null);
  // A submission guard is only valid while the editor still shows that draft.
  // Clearing the editor ends the gesture, even if Restore later reuses its object.
  if (submittedDraft.current !== draft) submittedDraft.current = null;
  const [error, setError] = useState('');
  const editor = useRef<MentionEditorHandle>(null);
  const [audioState, setAudioState] = useState<AudioState>('idle');
  const recorder = useRef<MediaRecorder | null>(null);
  const audioStream = useRef<MediaStream | null>(null);
  const transcriptionRequest = useRef<AbortController | null>(null);
  const mounted = useRef(true);
  const query = focused && !composing && projectId ? mentionQuery(text, caret, mentions) : null;
  const queryKey = query ? JSON.stringify([projectId, query.start, query.query]) : '';
  const open = !!queryKey && dismissed !== queryKey;
  const [search, setSearch] = useState<{ key: string; paths: ProjectPath[]; partial: boolean; error: string }>({ key: '', paths: [], partial: false, error: '' });
  const paths = search.key === queryKey ? search.paths : [];
  const loading = search.key !== queryKey;
  const searchText = query?.query;
  useEffect(() => {
    if (!open || !projectId || searchText === undefined) return;
    let current = true;
    setActive(0);
    const timer = window.setTimeout(() => {
      request<{ paths: ProjectPath[]; partial: boolean }>(`/api/projects/${encodeURIComponent(projectId)}/paths?q=${encodeURIComponent(searchText)}`)
        .then(result => { if (current) setSearch({ key: queryKey, ...result, error: '' }); })
        .catch(error => { if (current) setSearch({ key: queryKey, paths: [], partial: false, error: error instanceof Error ? error.message : 'Could not search project paths.' }); });
    }, 150);
    return () => { current = false; window.clearTimeout(timer); };
  }, [open, projectId, searchText, queryKey]);
  useEffect(() => {
    if (open) document.getElementById(`${listId}-${active}`)?.scrollIntoView({ block: 'nearest' });
  }, [active, open, listId]);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      transcriptionRequest.current?.abort();
      const activeRecorder = recorder.current;
      if (activeRecorder) {
        activeRecorder.ondataavailable = null;
        activeRecorder.onstop = null;
        activeRecorder.onerror = null;
        if (activeRecorder.state !== 'inactive') activeRecorder.stop();
      }
      audioStream.current?.getTracks().forEach(track => track.stop());
      recorder.current = null;
      audioStream.current = null;
    };
  }, []);
  function selectPath(path: ProjectPath) {
    if (!query) return;
    editor.current?.insertMention(path, query);
    setDismissed(queryKey);
  }
  function send(mode: SendMode = 'queue') {
    if (!text.trim() || disabled || submittedDraft.current === draft) return;
    setError('');
    try {
      // The parent synchronously owns the pending draft before starting HTTP.
      if (onSend(draft, mode) !== false) { submittedDraft.current = draft; onDraftChange({ text: '', mentions: [] }); }
    } catch (error) { setError(error instanceof Error ? error.message : 'Message could not be sent. Retry.'); }
  }
  function releaseAudio() {
    audioStream.current?.getTracks().forEach(track => track.stop());
    audioStream.current = null;
    recorder.current = null;
  }
  async function toggleRecording() {
    if (!onTranscription || audioState === 'transcribing') return;
    if (audioState === 'recording') {
      setAudioState('transcribing');
      if (recorder.current?.state !== 'inactive') recorder.current?.stop();
      return;
    }
    if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === 'undefined') {
      setError('Audio recording is not supported by this client.');
      return;
    }
    setAudioState('transcribing');
    setError('');
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      if (!mounted.current) {
        stream.getTracks().forEach(track => track.stop());
        return;
      }
      audioStream.current = stream;
      const mimeType = recordingTypes.find(type => MediaRecorder.isTypeSupported(type));
      const nextRecorder = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream);
      const chunks: Blob[] = [];
      let failed = false;
      recorder.current = nextRecorder;
      nextRecorder.ondataavailable = event => { if (event.data.size) chunks.push(event.data); };
      nextRecorder.onerror = () => {
        failed = true;
        releaseAudio();
        if (mounted.current) {
          setAudioState('idle');
          setError('Audio recording failed. Retry.');
        }
      };
      nextRecorder.onstop = async () => {
        const type = nextRecorder.mimeType || mimeType || 'audio/webm';
        releaseAudio();
        if (failed || !mounted.current) return;
        const audio = new Blob(chunks, { type });
        if (!audio.size) {
          setAudioState('idle');
          setError('No audio was recorded. Retry.');
          return;
        }
        const controller = new AbortController();
        transcriptionRequest.current = controller;
        try {
          const text = await transcribeAudio(audio, recordingFilename(type), controller.signal);
          if (mounted.current) onTranscription(text);
        } catch (error) {
          if (mounted.current && !controller.signal.aborted) setError(error instanceof Error ? error.message : 'Transcription failed. Retry.');
        } finally {
          if (transcriptionRequest.current === controller) transcriptionRequest.current = null;
          if (mounted.current) setAudioState('idle');
        }
      };
      nextRecorder.start();
      setAudioState('recording');
    } catch (error) {
      releaseAudio();
      if (mounted.current) {
        setAudioState('idle');
        setError(error instanceof DOMException && error.name === 'NotAllowedError' ? 'Microphone access was denied.' : 'Could not start audio recording. Retry.');
      }
    }
  }
  return <form className="composer" aria-label="Message composer" onSubmit={event => { event.preventDefault(); send(); }}>
    {open && <div className="mention-picker">
      <ul id={listId} role="listbox" aria-label="Project files and folders">{paths.map((path, index) => {
        const Icon = path.kind === 'directory' ? Folder : FileText;
        return <li key={path.path} id={`${listId}-${index}`} role="option" aria-selected={index === active} className={index === active ? 'is-active' : ''}
          onMouseDown={event => event.preventDefault()} onClick={() => selectPath(path)} onMouseMove={() => setActive(index)}>
          <Icon aria-hidden="true" /><span><strong>{path.path.split('/').pop()}{path.kind === 'directory' ? '/' : ''}</strong><small>{path.path}</small></span><span className="sr-only">{path.kind === 'directory' ? 'Folder' : 'File'}</span>
        </li>;
      })}</ul>
      <div className="mention-search-status" role="status">{loading ? 'Searching…' : search.error || (!paths.length ? 'No matching paths.' : search.partial ? 'Partial results. Refine your search.' : '')}</div>
    </div>}
    <MentionEditor ref={editor} aria-label={placeholder} placeholder={placeholder} draft={draft} readOnly={false}
      aria-autocomplete={projectId ? 'list' : undefined} aria-controls={open ? listId : undefined} aria-expanded={open} aria-haspopup="listbox" aria-activedescendant={open && paths[active] ? `${listId}-${active}` : undefined}
      onFocus={() => setFocused(true)} onBlur={() => setFocused(false)}
      onCompositionStart={() => setComposing(true)} onCompositionEnd={() => setComposing(false)}
      onCaretChange={setCaret}
      onChange={next => { onDraftChange(next); setDismissed(''); }} onKeyDown={event => {
       if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229 || composing) return;
      if (open && !event.shiftKey && ['ArrowDown', 'ArrowUp', 'Enter', 'Tab', 'Escape'].includes(event.key)) {
        event.preventDefault(); event.stopPropagation();
        if (event.key === 'Escape') setDismissed(queryKey);
        else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') setActive(index => paths.length ? (index + (event.key === 'ArrowDown' ? 1 : -1) + paths.length) % paths.length : 0);
        else if (paths[active]) selectPath(paths[active]);
        return;
      }
      if ((event.ctrlKey && event.key.toLowerCase() === 'j') || (event.key === 'Enter' && event.shiftKey)) {
        event.preventDefault();
        editor.current?.insertText('\n');
      } else if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); send(); }
    }} />
    {error && <p role="alert" className="form-error composer-error">{error}</p>}
    <div className="composer-controls">{graphControl && <div className="composer-graph-control">{graphControl}</div>}<div className="composer-model-controls">{modelControl}{running && text.trim() && <Button size="sm" disabled={disabled} onClick={() => void send('steer')}>Send now</Button>}{running && onStop && <IconButton label="Stop response" variant="outline" onClick={onStop}><Square /></IconButton>}{onTranscription && <IconButton label={audioState === 'recording' ? 'Stop recording' : audioState === 'transcribing' ? 'Transcribing audio' : 'Record audio'} aria-pressed={audioState === 'recording'} variant={audioState === 'recording' ? 'danger-soft' : 'ghost'} disabled={(disabled && audioState === 'idle') || audioState === 'transcribing'} className={`audio-record-button ${audioState === 'recording' ? 'is-recording' : ''}`} onClick={() => void toggleRecording()}>{audioState === 'recording' ? <Square /> : audioState === 'transcribing' ? <LoaderCircle className="audio-spinner" /> : <Mic />}</IconButton>}<IconButton label={running ? 'Queue message' : 'Send message'} variant="soft" type="submit" disabled={!text.trim() || disabled} className="send-button">{running ? <ListPlus /> : <ArrowUp />}</IconButton></div></div>
  </form>;
}
