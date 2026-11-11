import { useEffect, useRef, useState } from 'react';
import { Button } from '../../design-system/Button';
import { useNearEdge } from './useNearEdge';

export function AutoPage({ hasMore, cursor, busy = false, error = '', onLoad, onRetry = onLoad }: {
  hasMore: boolean; cursor: string | number; busy?: boolean; error?: string; onLoad: () => void; onRetry?: () => void;
}) {
  const target = useRef<HTMLDivElement>(null);
  const [retry, setRetry] = useState(0);
  const [failure, setFailure] = useState<{ cursor: string | number; message: string }>();
  useEffect(() => { if (error) setFailure({ cursor, message: error }); }, [error, cursor]);
  // A background refresh must not silently turn a failed pagination cursor back
  // into an automatic retry (or remove its only recovery action).
  const message = error || (failure?.cursor === cursor ? failure.message : '');
  useNearEdge(target, { enabled: hasMore, token: `${cursor}/${retry}`, busy, error: message, load: onLoad });
  return <div ref={target} style={{ minHeight: 1, flexShrink: 0 }}>
    {busy && <span className="muted" role="status">Loading…</span>}
    {message && <p className="form-error" role="alert">{message} <Button size="sm" disabled={busy} onClick={() => { setFailure(undefined); setRetry(value => value + 1); onRetry(); }}>Retry</Button></p>}
  </div>;
}
