import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { BookOpen, Square, Volume2, X } from 'lucide-react';
import { Button, IconButton } from '../../design-system/Button';
import { Input } from '../../design-system/Input';
import { Select } from '../../design-system/Select';
import { TabNav } from '../../design-system/TabNav';
import { ENGINE_URL, request } from '../../engine';
import { MarkdownContent } from '../chat/ChatMessage';
import './plan-reader.css';

// Bound each request while retaining paragraph/sentence boundaries where possible.
function speechChunks(text: string): string[] {
  const characters = Array.from(text.trim());
  const chunks: string[] = [];
  while (characters.length) {
    let length = Math.min(2400, characters.length);
    if (length < characters.length) {
      const candidate = characters.slice(0, length).join('');
      const boundary = Math.max(candidate.lastIndexOf('\n'), candidate.lastIndexOf('. '), candidate.lastIndexOf(' '));
      if (boundary > 0) length = Array.from(candidate.slice(0, boundary + 1)).length;
    }
    const chunk = characters.splice(0, length).join('').trim();
    if (chunk) chunks.push(chunk);
  }
  return chunks;
}

export function PlanReader({ title, text, onClose }: { title: string; text: string; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const rendered = useRef<HTMLDivElement>(null);
  const audio = useRef<HTMLAudioElement>(null);
  const controller = useRef<AbortController | null>(null);
  const audioURL = useRef('');
  const chunks = useRef<string[]>([]);
  const [mode, setMode] = useState<'reading' | 'plain'>('reading');
  const [fontSize, setFontSize] = useState('18');
  const [width, setWidth] = useState('comfortable');
  const [voice, setVoice] = useState('marin');
  const [speed, setSpeed] = useState('1');
  const playbackSpeed = useRef(1);
  const [configured, setConfigured] = useState<boolean | null>(null);
  const [apiKey, setAPIKey] = useState('');
  const [saving, setSaving] = useState(false);
  const [busy, setBusy] = useState(false);
  const [part, setPart] = useState(0);
  const [error, setError] = useState('');

  function stop() {
    controller.current?.abort(); controller.current = null;
    if (audio.current) { audio.current.pause(); audio.current.removeAttribute('src'); audio.current.load(); }
    if (audioURL.current) URL.revokeObjectURL(audioURL.current);
    audioURL.current = ''; chunks.current = []; setBusy(false); setPart(0);
  }

  useEffect(() => {
    dialog.current?.showModal();
    const load = new AbortController();
    void request<{ apiKeyConfigured: boolean }>('/api/speech/settings', 'GET', undefined, 15000, load.signal)
      .then(result => setConfigured(result.apiKeyConfigured))
      .catch(cause => { if (!load.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load speech settings.'); });
    return () => {
      load.abort(); controller.current?.abort(); audio.current?.pause();
      if (audioURL.current) URL.revokeObjectURL(audioURL.current);
    };
  }, []);

  async function saveKey() {
    setSaving(true); setError('');
    try {
      await request('/api/speech/settings', 'PATCH', { apiKey: apiKey.trim() });
      setConfigured(true); setAPIKey('');
    } catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not save API key.'); }
    finally { setSaving(false); }
  }

  async function playChunk(index: number, signal: AbortSignal) {
    setBusy(true); setPart(index + 1);
    try {
      const response = await fetch(`${ENGINE_URL}/api/speech`, {
        method: 'POST', credentials: 'omit', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: chunks.current[index], voice }), signal: AbortSignal.any([signal, AbortSignal.timeout(150000)]),
      });
      if (!response.ok) {
        const result = await response.json().catch(() => null);
        throw new Error(result?.error ?? 'Could not generate narration. Retry.');
      }
      const blob = await response.blob();
      if (signal.aborted) return;
      if (audioURL.current) URL.revokeObjectURL(audioURL.current);
      audioURL.current = URL.createObjectURL(blob);
      if (audio.current) {
        audio.current.src = audioURL.current; audio.current.playbackRate = playbackSpeed.current;
        setBusy(false);
        await audio.current.play();
      }
    } catch (cause) {
      if (!signal.aborted) {
        setBusy(false);
        setError(cause instanceof Error ? cause.message : 'Could not play narration. Retry.');
      }
    }
  }

  function readAloud() {
    stop(); setError('');
    const copy = rendered.current?.cloneNode(true) as HTMLElement | undefined;
    copy?.querySelectorAll('pre, button, svg').forEach(element => element.remove());
    copy?.querySelectorAll('p, li, h1, h2, h3, h4, h5, h6, tr, blockquote, br').forEach(element => element.append('\n'));
    chunks.current = speechChunks(copy?.textContent ?? text);
    if (!chunks.current.length) { setError('No text available for narration.'); return; }
    const next = new AbortController(); controller.current = next;
    void playChunk(0, next.signal);
  }

  return createPortal(<dialog ref={dialog} className="settings-dialog plan-reader" aria-labelledby="plan-reader-title" onCancel={onClose} onClose={onClose}>
    <header className="plan-reader-heading"><BookOpen /><h2 id="plan-reader-title">{title}</h2><IconButton label="Close reader" onClick={onClose}><X /></IconButton></header>
    <div className="plan-reader-toolbar">
      <TabNav value={mode} onChange={setMode} items={[{ value: 'reading', label: 'Reading' }, { value: 'plain', label: 'Plain text' }]} />
      <Select label="Text size" value={fontSize} onChange={event => setFontSize(event.target.value)}><option value="16">Small text</option><option value="18">Medium text</option><option value="21">Large text</option></Select>
      <Select label="Reading width" value={width} onChange={event => setWidth(event.target.value)}><option value="comfortable">Comfortable width</option><option value="wide">Wide</option></Select>
    </div>
    <div className="plan-reader-scroll" style={{ fontSize: `${fontSize}px` }}>
      <article className={`plan-reader-document plan-reader-document--${width}`}>
        <div ref={rendered} hidden={mode !== 'reading'}><MarkdownContent text={text} /></div>
        {mode === 'plain' && <pre className="plan-reader-plain">{text}</pre>}
      </article>
    </div>
    <footer className="plan-reader-footer">
      <div className="plan-reader-audio-toolbar">
        <Button variant="primary" disabled={busy || configured !== true} onClick={readAloud}><Volume2 />{busy ? 'Generating audio...' : 'Read aloud'}</Button>
        <IconButton label="Stop narration" disabled={!part} onClick={stop}><Square /></IconButton>
        <Select label="Narration voice" value={voice} onChange={event => { stop(); setVoice(event.target.value); }}><option value="marin">Marin</option><option value="cedar">Cedar</option><option value="coral">Coral</option><option value="alloy">Alloy</option><option value="sage">Sage</option></Select>
        <Select label="Playback speed" value={speed} onChange={event => { setSpeed(event.target.value); playbackSpeed.current = Number(event.target.value); if (audio.current) audio.current.playbackRate = playbackSpeed.current; }}><option value="0.8">0.8×</option><option value="1">1×</option><option value="1.2">1.2×</option><option value="1.5">1.5×</option></Select>
        {!!part && <span className="muted" role="status">Part {part} of {chunks.current.length}</span>}
      </div>
      <audio ref={audio} controls aria-label="Plan narration" onEnded={() => {
        if (part < chunks.current.length && controller.current) void playChunk(part, controller.current.signal);
      }} onError={() => setError('Audio could not be played. Retry narration.')} />
      <details className="plan-reader-key" open={configured === false}>
        <summary>OpenAI API key{configured ? ' · Configured' : ''}</summary>
        <div><Input type="password" aria-label="OpenAI speech API key" autoComplete="off" maxLength={512} placeholder="OpenAI API key" value={apiKey} disabled={saving} onChange={event => setAPIKey(event.target.value)} /><Button disabled={saving || !apiKey.trim()} onClick={() => void saveKey()}>{saving ? 'Saving...' : 'Save key'}</Button></div>
      </details>
      <small className="muted">AI-generated voice · Read aloud sends this text to OpenAI.</small>
      {error && <p className="form-error" role="alert">{error}</p>}
    </footer>
  </dialog>, document.body);
}
