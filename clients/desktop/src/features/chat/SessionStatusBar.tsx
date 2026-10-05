import type { Session } from '../../engine';
import { QuotaIndicator } from './QuotaIndicator';

const compact = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 });
const exact = new Intl.NumberFormat('en-US');
const percent = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 });

function tokens(value: number, full = false) {
  return (full ? exact : compact).format(value);
}

// Session Status Bar: the compact row below the message input with skills, MCPs, quota, context, and token counts.
export function SessionStatusBar({ session, side = false }: { session: Session; side?: boolean }) {
  const usage = session.usage;
  const inputTokens = usage?.inputTokens;
  const outputTokens = usage?.outputTokens;
  const contextTokens = usage?.context?.tokens;
  const contextWindow = usage?.context?.window;
  const hasContextWindow = contextWindow != null && contextWindow > 0;
  const contextPercent = contextTokens != null && hasContextWindow ? contextTokens / contextWindow * 100 : null;
  const contextDetails = contextTokens != null
    ? `Context: ${tokens(contextTokens, true)}${hasContextWindow ? ` / ${tokens(contextWindow, true)}` : ''} tokens${contextPercent !== null ? ` · ${percent.format(contextPercent)}% used` : ''}`
    : undefined;
  const tokenDetails = [
    inputTokens != null ? `Session input: ${tokens(inputTokens, true)} tokens (including cache).` : '',
    outputTokens != null ? `Output: ${tokens(outputTokens, true)} tokens (including reported reasoning).` : '',
  ].filter(Boolean).join(' ');

  // Skills and MCPs remain reference values.
  return <div className="session-status-bar" role="group" aria-label="Session Status Bar">
    {!side && <>{session.role !== 'general_agent' && <span>3 skills · 2 MCPs</span>}
    <QuotaIndicator key={`${session.id}/${session.model ?? ''}/${session.resolvedModel ?? ''}`} session={session} /></>}
    {contextTokens != null && <span title={contextDetails} aria-label={contextDetails}>
      {tokens(contextTokens)}{hasContextWindow ? ` / ${tokens(contextWindow)}` : ''}{contextPercent !== null ? ` · ${percent.format(contextPercent)}%` : ''}
    </span>}
    {(inputTokens != null || outputTokens != null) && <span title={tokenDetails} aria-label={tokenDetails}>
      {inputTokens != null && <>↑ {tokens(inputTokens)}</>}{inputTokens != null && outputTokens != null && ' · '}{outputTokens != null && <>↓ {tokens(outputTokens)}</>}
    </span>}
  </div>;
}
