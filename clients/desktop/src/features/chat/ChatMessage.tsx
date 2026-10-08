import { Atom, Check, Copy, FileText, Star } from 'lucide-react';
import { memo, useMemo, useState } from 'react';
import Markdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { IconButton } from '../../design-system/Button';
import type { Message } from '../workspace/demo';
import { MentionText } from './MentionText';
import { FileAttachments } from './FileAttachments';
import { Flowchart } from './Flowchart';
import { normalizeFlowchartMarkdown } from './flowchartMarkdown';

const markdownPlugins = [remarkGfm];
const markdownComponents: Components = {
  a: ({ href, children }) => <a href={href || undefined} target={href?.startsWith('#') ? undefined : '_blank'} rel="noopener noreferrer">{children}</a>,
  // Model-generated images must not trigger unsolicited external requests.
  img: ({ src, alt }) => <a href={typeof src === 'string' && src ? src : undefined} target="_blank" rel="noopener noreferrer">{alt || 'Image'}</a>,
  table: ({ children }) => <div className="markdown-table" role="region" aria-label="Table" tabIndex={0}><table>{children}</table></div>,
  pre: ({ node, children }) => {
    const code = node?.children.find(child => child.type === 'element' && child.tagName === 'code');
    if (code?.type === 'element') {
      const source = code.children.map(child => child.type === 'text' ? child.value : '').join('').trimEnd();
      const classes = code.properties.className;
      const language = Array.isArray(classes) ? classes.find(value => String(value).startsWith('language-')) : undefined;
      if (language === 'language-mermaid' || (!language && /^\s*(?:flowchart|graph)\s+(?:TB|TD|BT|RL|LR)\b/.test(source))) return <Flowchart source={source} />;
    }
    return <pre tabIndex={0} aria-label="Code block">{children}</pre>;
  },
};

const inlineMarkdownComponents: Components = {
  p: ({ children }) => <>{children}</>,
  h1: ({ children }) => <strong>{children}</strong>,
  h2: ({ children }) => <strong>{children}</strong>,
  h3: ({ children }) => <strong>{children}</strong>,
  h4: ({ children }) => <strong>{children}</strong>,
  h5: ({ children }) => <strong>{children}</strong>,
  h6: ({ children }) => <strong>{children}</strong>,
  blockquote: ({ children }) => <span>{children}</span>,
  ul: ({ children }) => <span>{children}</span>,
  ol: ({ children }) => <span>{children}</span>,
  li: ({ children }) => <span>{children} </span>,
  pre: ({ children }) => <code>{children}</code>,
  table: ({ children }) => <span>{children}</span>,
  thead: ({ children }) => <span>{children}</span>,
  tbody: ({ children }) => <span>{children}</span>,
  tr: ({ children }) => <span>{children} </span>,
  th: ({ children }) => <strong>{children}: </strong>,
  td: ({ children }) => <span>{children} </span>,
  a: markdownComponents.a,
  img: markdownComponents.img,
};

export const MarkdownContent = memo(function MarkdownContent({ text, className = '' }: { text: string; className?: string }) {
  const markdown = useMemo(() => normalizeFlowchartMarkdown(text), [text]);
  return <div className={`markdown-body ${className}`.trim()}><Markdown remarkPlugins={markdownPlugins} components={markdownComponents} skipHtml>{markdown}</Markdown></div>;
});

export const InlineMarkdownContent = memo(function InlineMarkdownContent({ text }: { text: string }) {
  return <Markdown remarkPlugins={markdownPlugins} components={inlineMarkdownComponents} skipHtml>{text}</Markdown>;
});

export function ChatMessage({ message, onFavorite, senderLabel }: { message: Message; onFavorite?: (favorite: boolean) => Promise<void>; senderLabel?: string }) {
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState(false);
  const [favoriting, setFavoriting] = useState(false);
  const [favoriteError, setFavoriteError] = useState(false);
  async function copy() {
    try { await navigator.clipboard.writeText(message.text); setCopied(true); setCopyError(false); }
    catch { setCopyError(true); }
  }
  async function favorite() {
    if (!onFavorite || favoriting) return;
    setFavoriting(true); setFavoriteError(false);
    try { await onFavorite(!message.favorite); }
    catch { setFavoriteError(true); }
    finally { setFavoriting(false); }
  }
  const streaming = message.role === 'assistant' && message.status === 'running';
  const canFavorite = message.role === 'assistant' && (!message.status || message.status === 'completed') && !!onFavorite;
  return <article data-message-id={message.id} className={`message message--${message.role}`} aria-label={senderLabel ?? `${message.role === 'user' ? 'Your' : 'Agent'} message`}>
    <div className="message-body">
      {message.context && <div className="context-injections">{message.context.map(file => <div key={file}><FileText /><span>Context injection</span><span aria-hidden="true">·</span><span className="context-file">{file}</span></div>)}</div>}
      {message.thought && <details className="thought"><summary><Atom /><strong>Think</strong><span>·</span><span className="thought-preview">{message.thought}</span></summary><p>{message.thought}</p></details>}
      {message.role === 'assistant' ? <MarkdownContent text={message.text} className="message-text" /> : <p className="message-text"><MentionText text={message.text} mentions={message.mentions} preparation={message.mentionPreparation} /></p>}
      <FileAttachments files={message.files} />
      {!streaming && <div className="message-actions"><IconButton label={copied ? 'Copied' : 'Copy message'} onClick={copy}>{copied ? <Check /> : <Copy />}</IconButton>{canFavorite && <IconButton label={message.favorite ? 'Remove from favorites' : 'Add to favorites'} aria-pressed={!!message.favorite} disabled={favoriting} onClick={() => void favorite()}><Star /></IconButton>}</div>}
      {copyError && <p role="status" className="small muted">Clipboard unavailable. Select the message to copy it.</p>}
      {favoriteError && <p role="alert" className="small muted">Could not update the favorite. Try again.</p>}
    </div>
  </article>;
}
