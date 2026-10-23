import { useState, type ComponentProps, type ReactNode } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { IS_DESKTOP, IS_MOBILE_HOST } from '../../platform';

export function ExternalLink({ href, children }: ComponentProps<'a'>) {
  const [failed, setFailed] = useState(false);
  return <><a href={href || undefined} target={href?.startsWith('#') || IS_MOBILE_HOST ? undefined : '_blank'} rel="noopener noreferrer" onClick={event => {
    if (!IS_DESKTOP || !href || href.startsWith('#')) return;
    event.preventDefault();
    setFailed(false);
    void invoke('open_external_url', { url: new URL(href, window.location.href).href }).catch(() => setFailed(true));
  }}>{children}</a>{failed && <span role="alert"> Could not open link. Try again.</span>}</>;
}

export function PlainLinks({ text }: { text: string }) {
  const content: ReactNode[] = [];
  let offset = 0;
  for (const match of text.matchAll(/\b(?:https?:\/\/|www\.)[^\s<>"`]+/gi)) {
    let url = match[0].replace(/[.,;:!?']+$/, '');
    for (const [open, close] of [['(', ')'], ['[', ']']]) {
      while (url.endsWith(close) && url.split(close).length > url.split(open).length) url = url.slice(0, -1);
    }
    const href = /^www\./i.test(url) ? `https://${url}` : url;
    try { if (!new URL(href).hostname) continue; } catch { continue; }
    content.push(text.slice(offset, match.index), <ExternalLink key={match.index} href={href}>{url}</ExternalLink>);
    offset = match.index + url.length;
  }
  content.push(text.slice(offset));
  return <>{content}</>;
}
