// EventSource's built-in retry reuses its original URL. Recreate the connection
// so each retry starts at the revisions currently applied by the client.
export function connectUpdates(
  url: string,
  ids: string[],
  cursors: () => Record<string, number>,
  onMessage: (event: MessageEvent, source: EventSource) => void,
  onError: () => void,
  onConnection: (source: EventSource | null) => void,
  create: (url: string) => EventSource = value => new EventSource(value),
) {
  let active = true;
  let source: EventSource | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let delay = 1000;
  function open() {
    if (!active) return;
    const query = new URLSearchParams({ ids: ids.join(','), cursors: JSON.stringify(cursors()) });
    const next = create(`${url}/api/updates?${query}`);
    source = next;
    onConnection(next);
    next.onopen = () => { if (source === next) delay = 1000; };
    next.onmessage = event => { if (source === next) onMessage(event, next); };
    next.onerror = () => {
      if (source !== next) return;
      next.close();
      source = null;
      onConnection(null);
      onError();
      timer = setTimeout(() => { timer = null; open(); }, delay);
      delay = Math.min(delay * 2, 30000);
    };
  }
  open();
  return () => {
    active = false;
    if (timer !== null) clearTimeout(timer);
    source?.close();
    source = null;
    onConnection(null);
  };
}
